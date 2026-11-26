package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"protonmailcli/internal/config"
	"protonmailcli/internal/store"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type boundedBodyFile struct{ bodyFile }

func (f boundedBodyFile) Read(p []byte) (int, error) {
	if len(p) > 64*1024 {
		return 0, fmt.Errorf("body read requested an unbounded window: %d", len(p))
	}
	return f.bodyFile.Read(p)
}

type failingBodyReader struct{}

func (failingBodyReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic body read failure")
}

func TestKeyedDraftBatchDiscardsPartialSnapshotsAndReplaysReadErrors(t *testing.T) {
	const goodBody = "complete second body"
	s := &workflowServer{}
	sourceDir := t.TempDir()
	badPath, goodPath := filepath.Join(sourceDir, "bad.txt"), filepath.Join(sourceDir, "good.txt")
	for path, body := range map[string]string{badPath: "partial first body", goodPath: goodBody} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	opens := 0
	openBodyFileFn = func(path string) (bodyFile, error) {
		opens++
		f, err := openFile(path)
		if err != nil || path != badPath {
			return f, err
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return &syntheticBodyFile{Reader: io.MultiReader(strings.NewReader("incomplete bytes"), failingBodyReader{}), info: info}, nil
	}
	createFile := createDraftSnapshotFileFn
	t.Cleanup(func() { createDraftSnapshotFileFn = createFile })
	var snapshot atomic.Pointer[os.File]
	createDraftSnapshotFileFn = func(dir, pattern string) (*os.File, error) {
		file, err := createFile(dir, pattern)
		if err == nil {
			snapshot.Store(file)
		}
		return file, err
	}
	s.afterAppend = func() {
		file := snapshot.Load()
		if file == nil {
			t.Error("missing snapshot storage")
			return
		}
		info, err := file.Stat()
		if err != nil || info.Size() != int64(len(goodBody)) {
			t.Errorf("incomplete source bytes were retained: info=%v err=%v", info, err)
		}
	}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "partial-stream")
	manifest := fmt.Sprintf(`[{"to":["first@example.invalid"],"body_file":%q},{"to":["second@example.invalid"],"body_file":%q},{"to":["third@example.invalid"],"body_file":%q}]`, badPath, goodPath, badPath)
	for run := 1; run <= 2; run++ {
		code, out := runWorkflow(t, args, manifest)
		if code != 10 || opens != run*2 || strings.Count(out, "synthetic body read failure") != 2 {
			t.Fatalf("partial read was not retained per item: run=%d exit=%d opens=%d out=%s", run, code, opens, out)
		}
	}
	_, appended := s.snapshot()
	if len(appended) != 1 || !strings.HasSuffix(appended[0], "\r\n\r\n"+goodBody) {
		t.Fatalf("partial source contaminated the next body or replay dispatched: %v", appended)
	}
}

func TestKeyedDraftBatchStreamsSnapshotsAndReplaysWithoutTemporaryStorage(t *testing.T) {
	sourceDir := t.TempDir()
	snapshotDir := t.TempDir()
	snapshotDirectory := snapshotDir
	firstBody := bytes.Repeat([]byte("first\xff\t\r\n"), 40*1024)
	secondBody := bytes.Repeat([]byte("second\xfe\t\r\n"), 40*1024)
	firstPath, secondPath := filepath.Join(sourceDir, "first.txt"), filepath.Join(sourceDir, "second.txt")
	for path, body := range map[string][]byte{firstPath: firstBody, secondPath: secondBody} {
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var snapshotsSeen atomic.Int32
	var snapshotFile atomic.Pointer[os.File]
	createFile := createDraftSnapshotFileFn
	t.Cleanup(func() { createDraftSnapshotFileFn = createFile })
	created := 0
	createDraftSnapshotFileFn = func(dir, pattern string) (*os.File, error) {
		file, err := createFile(snapshotDirectory, pattern)
		if err == nil {
			created++
			snapshotFile.Store(file)
		}
		return file, err
	}
	s := &workflowServer{afterAppend: func() {
		file := snapshotFile.Load()
		if file == nil {
			t.Error("draft dispatch did not have captured snapshot storage")
			return
		}
		info, err := file.Stat()
		if err != nil || info.Size() != int64(len(firstBody)+len(secondBody)) {
			t.Errorf("incorrect repeated body storage: info=%v err=%v", info, err)
			return
		}
		if err := draftSnapshotPrivacyError(file); err != nil {
			t.Error(err)
		}
		if runtime.GOOS != "windows" {
			if _, err := os.Stat(file.Name()); !os.IsNotExist(err) {
				t.Errorf("draft body remained named during dispatch: %v", err)
			}
		}
		snapshotsSeen.Add(1)
	}}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "streamed-batch")
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	opens := 0
	openBodyFileFn = func(path string) (bodyFile, error) {
		opens++
		f, err := openFile(path)
		if err != nil {
			return nil, err
		}
		return boundedBodyFile{f}, nil
	}
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"first@example.invalid"}, "body_file": firstPath},
		{"to": []string{"second@example.invalid"}, "body_file": secondPath},
		{"to": []string{"third@example.invalid"}, "body_file": firstPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	if code != 0 || opens != 2 || created != 1 || snapshotsSeen.Load() != 3 {
		t.Fatalf("streamed batch failed: exit=%d opens=%d created=%d snapshots=%d out=%s", code, opens, created, snapshotsSeen.Load(), out)
	}
	commands, appended := s.snapshot()
	if len(appended) != 3 || !strings.HasSuffix(appended[0], string(firstBody)) || !strings.HasSuffix(appended[1], string(secondBody)) || !strings.HasSuffix(appended[2], string(firstBody)) {
		t.Fatal("dispatch did not preserve the exact captured bytes")
	}
	if entries, err := os.ReadDir(snapshotDir); err != nil || len(entries) != 0 {
		t.Fatalf("snapshot was not removed: entries=%v err=%v", entries, err)
	}
	if _, err := snapshotFile.Load().Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("snapshot descriptor was not closed: %v", err)
	}
	// A completed replay requires neither a writable temporary directory nor IMAP.
	snapshotDirectory = filepath.Join(snapshotDir, "unavailable")
	code, out = runWorkflow(t, args, string(manifest))
	moreCommands, moreAppended := s.snapshot()
	if code != 0 || opens != 4 || strings.Count(strings.Join(moreCommands, "\n"), " STARTTLS") != strings.Count(strings.Join(commands, "\n"), " STARTTLS") || len(moreAppended) != 3 {
		t.Fatalf("replay needed storage or dispatched: exit=%d opens=%d out=%s", code, opens, out)
	}
	firstBody[5] = 0xfe
	if err := os.WriteFile(firstPath, firstBody, 0600); err != nil {
		t.Fatal(err)
	}
	code, out = runWorkflow(t, args, string(manifest))
	if code != 6 || !strings.Contains(out, `"code":"idempotency_conflict"`) {
		t.Fatalf("changed binary bytes did not conflict: exit=%d out=%s", code, out)
	}
}

func TestKeyedDraftBatchSnapshotFailuresCleanUpBeforeDispatch(t *testing.T) {
	for _, failure := range []string{"temporary-storage", "credentials"} {
		t.Run(failure, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			bodyPath := filepath.Join(t.TempDir(), "body.txt")
			if err := os.WriteFile(bodyPath, []byte("private draft contents"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshotDir := t.TempDir()
			snapshotDirectory := snapshotDir
			wantExit, wantCode := 1, "batch_snapshot_failed"
			if failure == "temporary-storage" {
				snapshotDirectory = filepath.Join(snapshotDir, "missing")
			} else {
				cfg, err := config.Load(args[2])
				if err != nil {
					t.Fatal(err)
				}
				cfg.Bridge.Username = ""
				if err := config.Save(args[2], cfg); err != nil {
					t.Fatal(err)
				}
				wantExit, wantCode = 3, "config_error"
			}
			createFile := createDraftSnapshotFileFn
			t.Cleanup(func() { createDraftSnapshotFileFn = createFile })
			var snapshot *os.File
			createDraftSnapshotFileFn = func(dir, pattern string) (*os.File, error) {
				file, err := createFile(snapshotDirectory, pattern)
				if err == nil {
					snapshot = file
				}
				return file, err
			}
			manifest := fmt.Sprintf(`[{"to":["recipient@example.invalid"],"body_file":%q}]`, bodyPath)
			code, out := runWorkflow(t, append(args, "draft", "create-many", "--stdin", "--idempotency-key", "snapshot-failure"), manifest)
			commands, _ := s.snapshot()
			if code != wantExit || !strings.Contains(out, `"code":"`+wantCode+`"`) || len(commands) != 0 {
				t.Fatalf("failure reached backend: exit=%d commands=%v out=%s", code, commands, out)
			}
			if failure == "credentials" && snapshot == nil {
				t.Fatal("credential failure did not exercise captured storage cleanup")
			}
			if snapshot != nil {
				if _, err := snapshot.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("failed invocation retained snapshot descriptor: %v", err)
				}
			}
			if entries, err := os.ReadDir(snapshotDir); err != nil || len(entries) != 0 {
				t.Fatalf("failed invocation retained snapshot data: entries=%v err=%v", entries, err)
			}
			state, err := store.New(args[4]).Load()
			if err != nil || len(state.Idempotency) != 0 {
				t.Fatalf("pre-dispatch failure reserved a key: state=%v err=%v", state.Idempotency, err)
			}
		})
	}
}
