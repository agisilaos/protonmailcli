package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestFileBatchRetryReportsUnfinishedIntentBeforePayloadConflict(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	// An interrupted older binary can leave a reservation identified only by the manifest.
	const manifest = `[{"to":["user@example.invalid"],"subject":"draft","body_file":"unavailable.txt"}]`
	state := fmt.Sprintf(`{"idempotency":{"batch":{"status":"pending","operation":"draft.create-many","payloadHash":"%x"}}}`, sha256.Sum256([]byte(manifest)))
	if err := os.WriteFile(args[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{manifest, "incomplete JSON"} {
		code, out := runWorkflow(t, append(args, "draft", "create-many", "--stdin", "--idempotency-key", "batch"), input)
		if code != 4 || !strings.Contains(out, `"code":"imap_draft_create_uncertain"`) {
			t.Fatalf("unfinished batch returned %d: %s", code, out)
		}
	}
	commands, _ := s.snapshot()
	if len(commands) != 0 {
		t.Fatalf("retry contacted IMAP: %v", commands)
	}
}

func TestPendingSingleDraftPrecedesMissingBody(t *testing.T) {
	server := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, server))
	const state = `{"idempotency":{"old":{"status":"pending","operation":"draft.create"}}}`
	if err := os.WriteFile(args[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, append(args, "draft", "create", "--to", "fixture@example.invalid", "--body-file", args[4]+".missing", "--idempotency-key", "old"), "")
	commands, _ := server.snapshot()
	if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || len(commands) != 0 {
		t.Fatalf("exit=%d out=%s commands=%v", code, out, commands)
	}
}
