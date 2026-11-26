package app

import (
	"strings"
	"testing"
)

func TestDraftBatchPreviewDetectsSharedCommandAndItemKey(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	for _, dry := range []bool{true, false} {
		args := append([]string(nil), base...)
		if dry {
			args = append(args, "--dry-run")
		}
		code, out := runWorkflow(t, append(args, "draft", "create-many", "--stdin", "--idempotency-key", "both"), `[{"to":["fixture@example.invalid"],"body":"test","idempotency_key":"both"}]`)
		if code != 4 || !strings.Contains(out, `"errorCode":"idempotency_conflict"`) {
			t.Fatalf("dry=%v exit=%d out=%s", dry, code, out)
		}
	}
	commands, _ := server.snapshot()
	if len(commands) != 0 {
		t.Fatalf("commands=%v", commands)
	}
}

func TestSendBatchPreviewDetectsSharedCommandAndItemKey(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	sent := captureWorkflowSMTP(t, base)
	for _, dry := range []bool{true, false} {
		args := append([]string(nil), base...)
		if dry {
			args = append(args, "--dry-run")
		}
		code, out := runWorkflow(t, append(args, "message", "send-many", "--stdin", "--idempotency-key", "both"), `[{"draft_id":"41","confirm_send":"41","idempotency_key":"both"}]`)
		if code != 1 || !strings.Contains(out, `"errorCode":"idempotency_conflict"`) || len(sent()) != 0 {
			t.Fatalf("dry=%v exit=%d out=%s deliveries=%d", dry, code, out, len(sent()))
		}
	}
}
