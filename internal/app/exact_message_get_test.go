package app

import (
	"strings"
	"testing"
)

func TestMessageGetReadsExactMailboxAndUID(t *testing.T) {
	s := &workflowServer{lowercaseSearch: true}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "message", "get", "--message-id", "imap:Project:2026:77")
	code, out := runWorkflow(t, args, "")
	if code != 0 {
		t.Fatalf("exact message read failed: exit=%d output=%s", code, out)
	}
	message := regressionData(t, out)["message"].(map[string]any)
	if message["id"] != "imap:Project:2026:77" || message["subject"] != "original" {
		t.Fatalf("unexpected message: %v", message)
	}
	commands, _ := s.snapshot()
	transcript := strings.Join(commands, "\n")
	if !strings.Contains(transcript, `EXAMINE "Project:2026"`) || strings.Contains(transcript, " SELECT ") || !strings.Contains(transcript, "UID FETCH 77 ") || strings.Contains(transcript, "UID SEARCH") {
		t.Fatalf("expected a direct lookup: %v", commands)
	}
}

func TestFollowUpReadsExactSourceForCreationAndReplay(t *testing.T) {
	server := &workflowServer{lowercaseSearch: true}
	args := append(imapCommandArgs(t, newWorkflowServer(t, server)), "message", "follow-up", "--message-id", "imap:INBOX:41", "--body", "reply", "--idempotency-key", "once")
	for range 2 {
		code, out := runWorkflow(t, args, "")
		if code != 0 {
			t.Fatalf("exit=%d out=%s", code, out)
		}
	}
	commands, appended := server.snapshot()
	if len(appended) != 1 || strings.Contains(strings.Join(commands, "\n"), "UID SEARCH") {
		t.Fatalf("appends=%d commands=%v", len(appended), commands)
	}
}
