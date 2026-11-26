package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/store"
)

type singleConsumptionBody struct{ reads int }

func (r *singleConsumptionBody) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(p, "one"), io.EOF
	}
	return 0, io.EOF
}

func TestBatchRecoveryUsesPreparedBodySources(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		for _, source := range []string{"stdin", "file"} {
			t.Run(fmt.Sprintf("readonly=%t/%s", readonly, source), func(t *testing.T) {
				s := &workflowServer{}
				args := imapCommandArgs(t, newWorkflowServer(t, s))
				receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`, `{"index":0,"ok":true,"draftId":"imap:Drafts:91"}`, "")
				saved := `{"idempotency":{"item":` + receipt + `}}`
				if readonly {
					saved = readonlyReceiptFixture(t, args, `{"item":`+receipt+`}`)
				} else if err := os.WriteFile(args[4], []byte(saved), 0600); err != nil {
					t.Fatal(err)
				}
				bodyPath := "-"
				if source == "file" {
					bodyPath = filepath.Join(t.TempDir(), "body.txt")
					if err := os.WriteFile(bodyPath, []byte("one"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				manifestPath := filepath.Join(t.TempDir(), "drafts.json")
				manifest := fmt.Sprintf(`[{"to":["synthetic@example.invalid"],"body_file":%q,"idempotency_key":"item"}]`, bodyPath)
				if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
					t.Fatal(err)
				}
				openFile := openBodyFileFn
				t.Cleanup(func() { openBodyFileFn = openFile })
				opens := 0
				openBodyFileFn = func(path string) (bodyFile, error) {
					opens++
					return openFile(path)
				}
				snapshotDir := t.TempDir()
				t.Setenv("TMPDIR", snapshotDir)
				input := &singleConsumptionBody{}
				var out bytes.Buffer
				code := Run(append(args, "draft", "create-many", "--file", manifestPath, "--idempotency-key", "fresh-command"), input, &out, io.Discard)
				wantReads, wantOpens := 1, 0
				if source == "file" {
					wantReads, wantOpens = 0, 1
				}
				wantCode := 0
				if readonly {
					wantCode = 1
					if !strings.Contains(out.String(), "state_save_failed") || !strings.Contains(out.String(), "not recorded") {
						t.Fatalf("new command key reported as saved: exit=%d out=%s", code, out.String())
					}
				}
				if code != wantCode || input.reads != wantReads || opens != wantOpens || !strings.Contains(out.String(), "imap:Drafts:91") {
					t.Fatalf("recovery reread the prepared source: exit=%d reads=%d opens=%d out=%s", code, input.reads, opens, out.String())
				}
				if readonly {
					assertReadonlyReceiptUnchanged(t, args[4], saved, s)
				} else {
					state, err := store.New(args[4]).Load()
					commands, _ := s.snapshot()
					if err != nil || state.Idempotency["fresh-command"].Status != "complete" || len(commands) != 0 {
						t.Fatalf("local recovery did not bind command receipt: err=%v receipts=%v commands=%v", err, state.Idempotency, commands)
					}
				}
				if entries, err := os.ReadDir(snapshotDir); err != nil || len(entries) != 0 {
					t.Fatalf("recovery retained captured body storage: entries=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestReadonlyBatchRecoveryRejectsExplicitConflictingSources(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		for _, emptyField := range []string{"body", "body_file"} {
			t.Run(fmt.Sprintf("keyed=%t/empty=%s", keyed, emptyField), func(t *testing.T) {
				s := &workflowServer{}
				args := imapCommandArgs(t, newWorkflowServer(t, s))
				receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`, `{"index":0,"ok":true,"draftId":"imap:Drafts:91"}`, "")
				saved := readonlyReceiptFixture(t, args, `{"item":`+receipt+`}`)
				bodyPath := filepath.Join(t.TempDir(), "body.txt")
				if err := os.WriteFile(bodyPath, []byte("one"), 0600); err != nil {
					t.Fatal(err)
				}
				body := ""
				if emptyField == "body_file" {
					body, bodyPath = "one", ""
				}
				manifest := fmt.Sprintf(`[{"to":["synthetic@example.invalid"],"body":%q,"body_file":%q,"idempotency_key":"item"}]`, body, bodyPath)
				openFile := openBodyFileFn
				t.Cleanup(func() { openBodyFileFn = openFile })
				opens := 0
				openBodyFileFn = func(path string) (bodyFile, error) {
					opens++
					return openFile(path)
				}
				args = append(args, "draft", "create-many", "--stdin")
				if keyed {
					args = append(args, "--idempotency-key", "fresh-command")
				}
				code, out := runWorkflow(t, args, manifest)
				wantCode := 4
				if keyed {
					wantCode = 1
					if !strings.Contains(out, "state_save_failed") || !strings.Contains(out, "not recorded") {
						t.Fatalf("new command key reported as saved: exit=%d out=%s", code, out)
					}
				}
				if code != wantCode || !strings.Contains(out, "validation_error") || !strings.Contains(out, "provide only one") || opens != 0 {
					t.Fatalf("conflicting sources replayed or read a body: exit=%d opens=%d out=%s", code, opens, out)
				}
				assertReadonlyReceiptUnchanged(t, args[4], saved, s)
			})
		}
	}
}

func TestReadonlyFreshBatchReportsPendingItemBeforePreparingBodies(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run=%t", dryRun), func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			saved := readonlyReceiptFixture(t, args, `{"pending-item":{"operation":"imap.draft.create-item","status":"pending"}}`)
			statePath := args[4]
			manifestPath := filepath.Join(t.TempDir(), "drafts.json")
			if err := os.WriteFile(manifestPath, []byte(`[{"to":["synthetic@example.invalid"],"body_file":"-","idempotency_key":"pending-item"}]`), 0600); err != nil {
				t.Fatal(err)
			}
			input := &forbiddenBatchInput{}
			var out bytes.Buffer
			args = append(args, "draft", "create-many", "--file", manifestPath, "--idempotency-key", "fresh-command")
			if dryRun {
				args = append([]string{"--dry-run"}, args...)
			}
			code := Run(args, input, &out, io.Discard)
			if code != 4 || !strings.Contains(out.String(), "imap_draft_create_uncertain") || input.reads != 0 {
				t.Fatalf("pending item was checked after its body: exit=%d reads=%d out=%s", code, input.reads, out.String())
			}
			assertReadonlyReceiptUnchanged(t, statePath, saved, s)
		})
	}
}

func TestReadonlyCompletedBatchReceiptTakesPrecedenceOverItemRecovery(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	const manifest = `[{"to":["synthetic@example.invalid"],"subject":"","body":"one","idempotency_key":"pending-item"}]`
	const response = `{"results":[{"index":0,"ok":true,"draftId":"imap:Drafts:91"}],"count":1,"success":1,"failed":0,"source":"imap"}`
	receipt := terminalReceiptFixture("draft.create-many", manifest, response, "")
	saved := readonlyReceiptFixture(t, args, `{"command":`+receipt+`,"pending-item":{"operation":"imap.draft.create-item","status":"pending"}}`)
	code, out := runWorkflow(t, append(args, "draft", "create-many", "--stdin", "--idempotency-key", "command"), manifest)
	if code != 0 || !strings.Contains(out, "imap:Drafts:91") {
		t.Fatalf("item recovery overrode the completed matching command receipt: exit=%d out=%s", code, out)
	}
	assertReadonlyReceiptUnchanged(t, args[4], saved, s)
}

func TestDraftBatchPreviewPreservesCachedItemAndPreparedFreshBody(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`, `{"index":0,"ok":true,"draftId":"imap:Drafts:91"}`, "")
	saved := readonlyReceiptFixture(t, base, `{"cached":`+receipt+`}`)
	manifest := filepath.Join(t.TempDir(), "drafts.json")
	if err := os.WriteFile(manifest, []byte(`[{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"cached"},{"to":["synthetic@example.invalid"],"body_file":"-","idempotency_key":"fresh"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	snapshotDir := t.TempDir()
	t.Setenv("TMPDIR", snapshotDir)
	input := &singleConsumptionBody{}
	var out bytes.Buffer
	code := Run(append(base, "--dry-run", "draft", "create-many", "--file", manifest, "--idempotency-key", "preview"), input, &out, io.Discard)
	_, appended := server.snapshot()
	after, err := os.ReadFile(base[4])
	if code != 0 || input.reads != 1 || len(appended) != 0 || !strings.Contains(out.String(), "imap:Drafts:91") || !strings.Contains(out.String(), `"dryRun":true`) || err != nil || string(after) != saved {
		t.Fatalf("preview lost cached result or changed state: exit=%d reads=%d appended=%d err=%v out=%s", code, input.reads, len(appended), err, out.String())
	}
	if entries, err := os.ReadDir(snapshotDir); err != nil || len(entries) != 0 {
		t.Fatalf("preview retained captured body storage: entries=%v err=%v", entries, err)
	}
}
