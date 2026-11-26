package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/store"
)

func TestDraftItemChecksOperationBeforeOpeningBody(t *testing.T) {
	for _, source := range []string{"-", "missing"} {
		t.Run(source, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			if err := os.WriteFile(base[4], []byte(`{"idempotency":{"taken":{"operation":"message.send","status":"complete"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "manifest.json")
			manifest := fmt.Sprintf(`[{"to":["fixture@example.invalid"],"body_file":%q,"idempotency_key":"taken"},{"to":["fixture@example.invalid"],"body_file":"-"}]`, source)
			if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			code, out := runWorkflow(t, append(base, "draft", "create-many", "--file", path), "the remaining body")
			_, appended := server.snapshot()
			if code != 10 || !strings.Contains(out, "idempotency_conflict") || len(appended) != 1 || !strings.HasSuffix(appended[0], "\r\n\r\nthe remaining body") {
				t.Fatalf("exit=%d out=%s appends=%q", code, out, appended)
			}
		})
	}
}

func TestCommandKeyConflictLeavesStdinForTheNextDraft(t *testing.T) {
	for _, key := range []string{"taken", "batch"} {
		t.Run(key, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			if err := os.WriteFile(base[4], []byte(`{"idempotency":{"taken":{"operation":"message.send","status":"complete"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "manifest.json")
			manifest := fmt.Sprintf(`[{"to":["fixture@example.invalid"],"body_file":"-","idempotency_key":%q},{"to":["fixture@example.invalid"],"body_file":"-"}]`, key)
			if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			args := append(base, "draft", "create-many", "--file", path, "--idempotency-key", "batch")
			for _, attempt := range []string{"create", "replay"} {
				if attempt == "replay" && key == "taken" {
					s := store.New(base[4])
					state, err := s.Load()
					if err != nil {
						t.Fatal(err)
					}
					delete(state.Idempotency, "taken")
					if err := s.Save(state); err != nil {
						t.Fatal(err)
					}
				}
				code, out := runWorkflow(t, args, "the remaining body")
				_, appended := server.snapshot()
				if code != 10 || !strings.Contains(out, "idempotency_conflict") || len(appended) != 1 || !strings.HasSuffix(appended[0], "\r\n\r\nthe remaining body") {
					t.Fatalf("%s exit=%d output=%s appends=%q", attempt, code, out, appended)
				}
			}
		})
	}
}

func TestLegacyBatchReceiptVerifiesOriginallyConsumedConflictBody(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	manifest := `[{"to":["fixture@example.invalid"],"body_file":"-","idempotency_key":"taken"},{"to":["fixture@example.invalid"],"body_file":"-"}]`
	oldPayload := fmt.Sprintf(`[{"to":["fixture@example.invalid"],"subject":"","body_file":"-","idempotency_key":"taken","body_file_fingerprint":"%x"},{"to":["fixture@example.invalid"],"subject":"","body_file":"-","body_file_fingerprint":"%x"}]`, sha256.Sum256([]byte("original")), sha256.Sum256(nil))
	outcome := `{"source":"imap","success":1,"failed":1,"results":[{"index":0,"ok":false,"errorCode":"idempotency_conflict"},{"index":1,"ok":true,"draftId":"imap-v2:Drafts:91"}]}`
	record := terminalReceiptFixture("draft.create-many", oldPayload, outcome, "")
	state := `{"idempotency":{"taken":{"operation":"message.send","status":"complete"},"batch":` + record + `}}`
	if err := os.WriteFile(base[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(base, "draft", "create-many", "--file", path, "--idempotency-key", "batch")
	code, out := runWorkflow(t, args, "original")
	commands, appended := server.snapshot()
	if code != 10 || !strings.Contains(out, "imap-v2:Drafts:91") || len(commands) != 0 || len(appended) != 0 {
		t.Fatalf("legacy replay exit=%d output=%s commands=%v appends=%d", code, out, commands, len(appended))
	}
	if code, _ := runWorkflow(t, args, "changed"); code != 6 {
		t.Fatalf("changed original bytes did not conflict: exit=%d", code)
	}
}
