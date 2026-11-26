package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/store"
)

func TestUnkeyedSendLostSMTPCompletionRequiresReconciliation(t *testing.T) {
	for _, command := range []string{"send", "send-many"} {
		t.Run(command, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			delivered := captureWorkflowSMTPWithCompletion(t, base, false)
			args := append(base, "message", command)
			input := ""
			if command == "send" {
				args = append(args, "--draft-id", "41", "--confirm-send", "41")
			} else {
				args = append(args, "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42"}]`
			}
			code, out := runWorkflow(t, args, input)
			var result struct {
				Error struct {
					Code, Category, Hint, Message string
					Retryable                     bool
				}
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if code != 4 || result.Error.Code != "imap_send_uncertain" || result.Error.Category != "uncertain" || result.Error.Retryable ||
				!strings.Contains(result.Error.Hint, "Do not retry automatically") || !strings.Contains(result.Error.Hint, "Inspect delivery") ||
				!strings.Contains(result.Error.Message, "41") || len(delivered()) != 1 {
				t.Fatalf("exit=%d captured=%d out=%s", code, len(delivered()), out)
			}
			if _, err := os.Stat(base[4]); !os.IsNotExist(err) {
				t.Fatalf("unkeyed failed send wrote local state: %v", err)
			}
		})
	}
}

func TestUnkeyedSendDefiniteSMTPRejectionPreservesFailure(t *testing.T) {
	for _, command := range []string{"send", "send-many"} {
		t.Run(command, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			attempts := captureWorkflowSMTPWithReplies(t, base, "451 synthetic temporary rejection")
			args := append(base, "message", command)
			input := ""
			wantCode, wantAttempts := 4, 1
			if command == "send" {
				args = append(args, "--draft-id", "41", "--confirm-send", "41")
			} else {
				args = append(args, "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42"}]`
				wantCode, wantAttempts = 1, 2
			}
			code, out := runWorkflow(t, args, input)
			var result struct {
				Error *struct {
					Code, Category string
					Retryable      bool
				}
				Data batchResultResponse
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if code != wantCode || len(attempts()) != wantAttempts || strings.Contains(out, "imap_send_uncertain") {
				t.Fatalf("definite rejection changed: exit=%d attempts=%d out=%s", code, len(attempts()), out)
			}
			if command == "send" {
				if result.Error == nil || result.Error.Code != "send_failed" || result.Error.Category != "transient" || !result.Error.Retryable {
					t.Fatalf("temporary rejection is no longer retryable: %s", out)
				}
			} else {
				if len(result.Data.Results) != 2 || result.Data.Failed != 2 {
					t.Fatalf("missing rejected rows: %s", out)
				}
				for _, row := range result.Data.Results {
					if row.OK || row.ErrorCode != "send_failed" || !strings.Contains(row.Error, "451") {
						t.Fatalf("incorrect rejection: %s", out)
					}
				}
			}
		})
	}
}

func TestUnkeyedSendBatchUncertaintyPreservesEarlierOutcomes(t *testing.T) {
	peer := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, peer))
	delivered := captureWorkflowSMTPWithReplies(t, base, "250 accepted", "")
	input := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42"},{"draft_id":"43","confirm_send":"43"}]`
	code, out := runWorkflow(t, append(base, "message", "send-many", "--stdin"), input)
	var result struct {
		Error struct {
			Code, Category, Message string
			Retryable               bool
		}
		Data batchResultResponse
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if code != 4 || result.Error.Code != "imap_send_uncertain" || result.Error.Category != "uncertain" || result.Error.Retryable ||
		!strings.Contains(result.Error.Message, "42") || len(delivered()) != 2 ||
		result.Data.Count != 2 || result.Data.Success != 1 || result.Data.Failed != 1 || len(result.Data.Results) != 2 {
		t.Fatalf("uncertainty lost earlier results or sent later rows: exit=%d captured=%d out=%s", code, len(delivered()), out)
	}
	first, uncertain := result.Data.Results[0], result.Data.Results[1]
	if first.Index != 0 || !first.OK || first.DraftID != "imap-v2:Drafts:41" || first.SentAt == "" ||
		uncertain.Index != 1 || uncertain.OK || uncertain.DraftID != "imap-v2:Drafts:42" || uncertain.ErrorCode != "imap_send_uncertain" {
		t.Fatalf("cannot reconcile processed rows: %s", out)
	}
	commands, _ := peer.snapshot()
	if strings.Contains(strings.Join(commands, "\n"), "UID FETCH 43 ") {
		t.Fatalf("later row was fetched after uncertain delivery: %v", commands)
	}
}

func TestMixedKeySendBatchUncertaintyPreservesEarlierOutcomes(t *testing.T) {
	for _, keyed := range []string{"item", "command"} {
		t.Run(keyed, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			delivered := captureWorkflowSMTPWithReplies(t, base, "250 accepted", "")
			input := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42","idempotency_key":"item-key"},{"draft_id":"43","confirm_send":"43"}]`
			args := append(base, "message", "send-many", "--stdin")
			key := "item-key"
			if keyed == "command" {
				args = append(args, "--idempotency-key", "batch-key")
				key = "batch-key"
			}
			code, out := runWorkflow(t, args, input)
			var result struct {
				Error struct {
					Code      string
					Retryable bool
				}
				Data batchResultResponse
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if code != 4 || result.Error.Code != "imap_send_uncertain" || result.Error.Retryable || len(delivered()) != 2 || result.Data.Count != 2 || result.Data.Success != 1 || result.Data.Failed != 1 || len(result.Data.Results) != 2 {
				t.Fatalf("keyed uncertainty lost processed rows: exit=%d sent=%d out=%s", code, len(delivered()), out)
			}
			first, uncertain := result.Data.Results[0], result.Data.Results[1]
			if !first.OK || first.Index != 0 || first.DraftID != "imap-v2:Drafts:41" || first.SentAt == "" || uncertain.OK || uncertain.Index != 1 || uncertain.DraftID != "imap-v2:Drafts:42" || uncertain.ErrorCode != "imap_send_uncertain" {
				t.Fatalf("missing reconciliation outcome: %s", out)
			}
			state, err := store.New(base[4]).Load()
			if err != nil || state.Idempotency[key].Status != "pending" {
				t.Fatalf("lost pending receipt: %+v %v", state.Idempotency, err)
			}
			code, out = runWorkflow(t, args, input)
			if code != 4 || !strings.Contains(out, "imap_send_uncertain") || len(delivered()) != 2 {
				t.Fatalf("retry resent mixed batch: exit=%d sent=%d out=%s", code, len(delivered()), out)
			}
		})
	}
}

func TestCodeOnlySMTPCompletionPreservesCommandOutcome(t *testing.T) {
	for _, command := range []string{"send", "send-many"} {
		for _, reply := range []string{"250", "451"} {
			t.Run(command+"/"+reply, func(t *testing.T) {
				peer := &workflowServer{}
				base := imapCommandArgs(t, newWorkflowServer(t, peer))
				sent := captureWorkflowSMTPWithReplies(t, base, reply)
				args := append(base, "message", command)
				input := ""
				if command == "send" {
					args = append(args, "--draft-id", "41", "--confirm-send", "41")
				} else {
					args = append(args, "--stdin")
					input = `[{"draft_id":"41","confirm_send":"41"}]`
				}
				code, out := runWorkflow(t, args, input)
				wantCode := 0
				if reply == "451" {
					wantCode = 4
					if command == "send-many" {
						wantCode = 1
					}
				}
				if code != wantCode || len(sent()) != 1 || strings.Contains(out, "imap_send_uncertain") || (reply == "451" && !strings.Contains(out, "send_failed")) || (reply == "250" && !strings.Contains(out, "imap-v2:Drafts:41")) {
					t.Fatalf("code-only reply changed outcome: exit=%d sent=%d out=%s", code, len(sent()), out)
				}
			})
		}
	}
}
