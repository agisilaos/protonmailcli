package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestUnreadableDraftSnapshotStopsBeforeAnyDispatch(t *testing.T) {
	body := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(body, []byte("source is readable"), 0600); err != nil {
		t.Fatal(err)
	}
	create := createDraftSnapshotFileFn
	t.Cleanup(func() { createDraftSnapshotFileFn = create })
	// Storage accepts the complete capture, then the first readback fails.
	createDraftSnapshotFileFn = func(dir, pattern string) (*os.File, error) {
		path := filepath.Join(t.TempDir(), "write-only")
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	peer := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, peer))
	args := append(base, "draft", "create-many", "--stdin", "--idempotency-key", "batch")
	input := fmt.Sprintf(`[{"to":["r@example.invalid"],"body_file":%q},{"to":["r@example.invalid"],"body":"later"}]`, body)
	for _, flags := range [][]string{nil, {"--dry-run"}} {
		code, out := runWorkflow(t, append(append([]string(nil), flags...), args...), input)
		_, appended := peer.snapshot()
		state, err := store.New(base[4]).Load()
		if code != 1 || !strings.Contains(out, "batch_snapshot_failed") || strings.Contains(out, "validation_error") || len(appended) != 0 || err != nil || len(state.Idempotency) != 0 {
			t.Fatalf("exit=%d appended=%d receipts=%v err=%v out=%s", code, len(appended), state.Idempotency, err, out)
		}
	}
	createDraftSnapshotFileFn = create
	code, out := runWorkflow(t, args, input)
	_, appended := peer.snapshot()
	if code != 0 || len(appended) != 2 {
		t.Fatalf("healthy retry exit=%d appended=%d out=%s", code, len(appended), out)
	}
}

func TestShortDraftSnapshotStopsAfterEarlierAppend(t *testing.T) {
	body := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(body, []byte("second body"), 0600); err != nil {
		t.Fatal(err)
	}
	create := createDraftSnapshotFileFn
	t.Cleanup(func() { createDraftSnapshotFileFn = create })
	var snapshot atomic.Pointer[os.File]
	createDraftSnapshotFileFn = func(dir, pattern string) (*os.File, error) {
		f, err := create(dir, pattern)
		snapshot.Store(f)
		return f, err
	}
	peer := &workflowServer{afterAppend: sync.OnceFunc(func() {
		if err := snapshot.Load().Truncate(1); err != nil {
			t.Error(err)
		}
	})}
	base := imapCommandArgs(t, newWorkflowServer(t, peer))
	args := append(base, "draft", "create-many", "--stdin", "--idempotency-key", "batch")
	input := fmt.Sprintf(`[{"to":["r@example.invalid"],"body":"first","idempotency_key":"first"},{"to":["r@example.invalid"],"body_file":%q},{"to":["r@example.invalid"],"body":"later","idempotency_key":"later"}]`, body)
	code, out := runWorkflow(t, args, input)
	var result struct {
		Error struct{ Code string }
		Data  batchResultResponse
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	_, appended := peer.snapshot()
	if code != 1 || result.Error.Code != "batch_snapshot_failed" || len(appended) != 1 || len(result.Data.Results) != 1 || !result.Data.Results[0].OK || result.Data.Results[0].DraftID == "" {
		t.Fatalf("exit=%d appended=%d out=%s", code, len(appended), out)
	}
	state, err := store.New(base[4]).Load()
	if err != nil || state.Idempotency["batch"].Status != "pending" || state.Idempotency["first"].Status != "complete" || state.Idempotency["later"].Status != "" {
		t.Fatalf("unexpected recovery receipts=%v err=%v", state.Idempotency, err)
	}
	code, out = runWorkflow(t, args, input)
	_, appended = peer.snapshot()
	if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || len(appended) != 1 {
		t.Fatalf("unsafe retry exit=%d appended=%d out=%s", code, len(appended), out)
	}
}

func TestKeyedDraftBatchCompletesWithoutReservingWhenNoDispatchNeeded(t *testing.T) {
	const good = `[{"to":["r@example.invalid"],"body":"first","idempotency_key":"item"}]`
	for _, tc := range []struct {
		name     string
		input    string
		wantExit int
	}{
		{"all invalid", `[{"body":"no recipient"}]`, 4},
		{"replayed item", good, 0},
		{"shared command and item key", `[{"to":["r@example.invalid"],"body":"first","idempotency_key":"batch"}]`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			unkeyed := append(append([]string(nil), base...), "draft", "create-many", "--stdin")
			wantAppends := 0
			if tc.name == "replayed item" {
				if code, out := runWorkflow(t, unkeyed, good); code != 0 {
					t.Fatalf("seed item exit=%d out=%s", code, out)
				}
				wantAppends = 1
			}
			args := append(unkeyed, "--idempotency-key", "batch")
			disk := store.New(base[4])
			var checkpoints []string
			var out bytes.Buffer
			app := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(tc.input), checkpoint: func(st model.State) error {
				checkpoints = append(checkpoints, st.Idempotency["batch"].Status)
				return disk.Save(st)
			}}
			code := app.run(args)
			if code != tc.wantExit || !slices.Equal(checkpoints, []string{"complete"}) {
				t.Fatalf("exit=%d checkpoints=%v out=%s", code, checkpoints, out.String())
			}
			code, replay := runWorkflow(t, args, tc.input)
			_, appends := peer.snapshot()
			if code != tc.wantExit || len(appends) != wantAppends {
				t.Fatalf("replay exit=%d appends=%d out=%s", code, len(appends), replay)
			}
		})
	}
}
