package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestSendUncertaintyIdentifiesResolvedDraft(t *testing.T) {
	for _, tc := range []struct{ command, inputID, mailbox, wantID string }{
		{"send", "41", "Brouillons", "imap:Brouillons:41"},
		{"send", "imap:Drafts:41", "Drafts", "imap-v2:Drafts:41"},
		{"send-many", "imap:Drafts:41", "Brouillons", "imap:Brouillons:41"},
		{"send-many", "41", "Drafts", "imap-v2:Drafts:41"},
	} {
		for _, keyed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/keyed=%t", tc.command, tc.mailbox, keyed), func(t *testing.T) {
				peer := &workflowServer{draftBox: tc.mailbox}
				base := imapCommandArgs(t, newWorkflowServer(t, peer))
				sent := captureWorkflowSMTPWithCompletion(t, base, false)
				args := append(base, "message", tc.command)
				input := ""
				if tc.command == "send" {
					args = append(args, "--draft-id", tc.inputID, "--confirm-send", tc.inputID)
				} else {
					args = append(args, "--stdin")
					input = fmt.Sprintf(`[{"draft_id":%q,"confirm_send":%q}]`, tc.inputID, tc.inputID)
				}
				if keyed {
					args = append(args, "--idempotency-key", "receipt")
				}
				code, out := runWorkflow(t, args, input)
				var result struct {
					Error struct{ Code, Message string }
					Data  batchResultResponse
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if code != 4 || result.Error.Code != "imap_send_uncertain" || !strings.Contains(result.Error.Message, tc.wantID) || len(sent()) != 1 {
					t.Fatalf("uncertainty reported a movable draft alias: exit=%d sent=%d out=%s", code, len(sent()), out)
				}
				if tc.command == "send-many" && (len(result.Data.Results) != 1 || result.Data.Results[0].DraftID != tc.wantID) {
					t.Fatalf("row lost resolved identity: %s", out)
				}
			})
		}
	}
}

func TestAcceptedSendReceiptFailureIdentifiesResolvedDraft(t *testing.T) {
	for _, command := range []string{"send", "send-many"} {
		t.Run(command, func(t *testing.T) {
			peer := &workflowServer{draftBox: "Brouillons"}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			sent := captureWorkflowSMTP(t, base)
			args := append(base, "message", command)
			input := ""
			if command == "send" {
				args = append(args, "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "receipt")
			} else {
				args = append(args, "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41","idempotency_key":"receipt"}]`
			}
			disk := store.New(base[4])
			checkpoints := 0
			var out bytes.Buffer
			app := App{Stdin: strings.NewReader(input), Stdout: &out, Stderr: io.Discard, checkpoint: func(st model.State) error {
				checkpoints++
				if checkpoints == 2 {
					return fmt.Errorf("synthetic receipt save failure")
				}
				return disk.Save(st)
			}}
			code := app.run(args)
			if code != 4 || !strings.Contains(out.String(), "imap_send_uncertain") || !strings.Contains(out.String(), "imap:Brouillons:41") || len(sent()) != 1 {
				t.Fatalf("accepted send error lost exact identity: exit=%d sent=%d out=%s", code, len(sent()), out.String())
			}
		})
	}
}
