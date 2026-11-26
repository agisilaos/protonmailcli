package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/store"
)

func TestLostFallbackMoveKeepsOriginalDestinationInRecovery(t *testing.T) {
	for _, mode := range []string{"create", "create-many", "follow-up"} {
		t.Run(mode, func(t *testing.T) {
			const original = "Former Drafts"
			peer := &workflowServer{draftBox: original, appendFailure: true, moveCompletionLost: true}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			delivered := captureWorkflowSMTP(t, base)
			args, input := append(base, "draft", "create", "--to", "recipient@example.invalid", "--body", "fixture", "--idempotency-key", "recovery"), ""
			if mode == "create-many" {
				args = append(base, "draft", "create-many", "--stdin")
				input = `[{"to":["recipient@example.invalid"],"body":"fixture","idempotency_key":"recovery"}]`
			} else if mode == "follow-up" {
				args = append(base, "message", "follow-up", "--message-id", "41", "--body", "fixture", "--idempotency-key", "recovery")
			}
			code, out := runWorkflow(t, args, input)
			state, err := store.New(base[4]).Load()
			if err != nil {
				t.Fatal(err)
			}
			failure := state.Idempotency["recovery"].Failure
			commands, _ := peer.snapshot()
			if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || !strings.Contains(out, original) || len(delivered()) != 1 ||
				failure == nil || !strings.Contains(failure.Message, original) || !strings.Contains(failure.Hint, original) || !strings.Contains(failure.Hint, "INBOX") ||
				!strings.Contains(strings.Join(commands, "\n"), fmt.Sprintf("UID MOVE 41 %q", original)) {
				t.Fatalf("lost MOVE omitted original destination: exit=%d sent=%d failure=%+v commands=%v out=%s", code, len(delivered()), failure, commands, out)
			}
			saved, err := os.ReadFile(base[4])
			if err != nil {
				t.Fatal(err)
			}
			peer.mu.Lock()
			peer.draftBox = "New Drafts"
			peer.mu.Unlock()
			code, replay := runWorkflow(t, args, input)
			after, err := os.ReadFile(base[4])
			if code != 4 || !strings.Contains(replay, original) || !strings.Contains(replay, "imap_draft_create_uncertain") || len(delivered()) != 1 || err != nil || string(after) != string(saved) {
				t.Fatalf("recovery followed the new role or rewrote evidence: exit=%d sent=%d err=%v out=%s", code, len(delivered()), err, replay)
			}
		})
	}
}
