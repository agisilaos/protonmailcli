package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFallbackDraftLostSMTPCompletionRequiresReconciliation(t *testing.T) {
	for _, key := range []string{"", "draft-receipt"} {
		t.Run("key="+key, func(t *testing.T) {
			server := &workflowServer{appendFailure: true}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			delivered := captureWorkflowSMTPWithCompletion(t, base, false)
			args := append(base, "draft", "create", "--to", "recipient@example.invalid", "--body", "fixture")
			attempts := 1
			if key != "" {
				args = append(args, "--idempotency-key", key)
				attempts = 2
			}
			for range attempts {
				code, out := runWorkflow(t, args, "")
				var result struct {
					Error struct {
						Code, Category, Hint string
						Retryable            bool
					}
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if code != 4 || result.Error.Code != "imap_draft_create_uncertain" || result.Error.Category != "uncertain" || result.Error.Retryable ||
					!strings.Contains(result.Error.Hint, "INBOX") || !strings.Contains(result.Error.Hint, "Drafts") || len(delivered()) != 1 {
					t.Fatalf("exit=%d captured=%d out=%s", code, len(delivered()), out)
				}
			}
		})
	}
}
