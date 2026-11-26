package app

import (
	"encoding/json"
	"os"
	"protonmailcli/internal/config"
	"strings"
	"testing"
)

func TestCompletedSendReplaysRecordedMailboxAfterTopologyChanges(t *testing.T) {
	for _, mode := range []string{"single", "item"} {
		for _, mailbox := range []string{"Brouillons", "Drafts"} {
			t.Run(mode+"/"+mailbox, func(t *testing.T) {
				expectedID := map[string]string{"Drafts": "imap-v2:Drafts:41", "Brouillons": "imap:Brouillons:41"}[mailbox]
				original := &workflowServer{draftBox: mailbox}
				base := imapCommandArgs(t, newWorkflowServer(t, original))
				sent := captureWorkflowSMTP(t, base)
				args := append(append([]string(nil), base...), "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "mailbox-replay")
				input := ""
				if mode == "item" {
					args = append(append([]string(nil), base...), "message", "send-many", "--stdin")
					input = `[{"draft_id":"41","confirm_send":"41","idempotency_key":"mailbox-replay"}]`
				}
				code, out := runWorkflow(t, args, input)
				if code != 0 || len(sent()) != 1 || !strings.Contains(out, expectedID) {
					t.Fatalf("initial send failed: exit=%d sends=%d out=%s", code, len(sent()), out)
				}
				// Both non-Drafts legacy names and versioned literal Drafts IDs
				// identify the exact mailbox without separate source metadata.
				if mailbox == "Brouillons" || mode == "item" {
					rewriteReplayRecord(t, base[4], "mailbox-replay", func(record map[string]any) { delete(record, "sourceId") })
				}
				changed := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`, `* LIST () "/" "Brouillons"`}}
				cfg, err := config.Load(base[2])
				if err != nil {
					t.Fatal(err)
				}
				configureIMAPTestEndpoint(t, &cfg, newWorkflowServer(t, changed))
				if err := config.Save(base[2], cfg); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(base[4])
				if err != nil {
					t.Fatal(err)
				}
				for _, dryRun := range []bool{false, true} {
					replayArgs := append([]string(nil), args...)
					if dryRun {
						replayArgs = append([]string{"--dry-run"}, args...)
					}
					code, out = runWorkflow(t, replayArgs, input)
					if code != 0 || len(sent()) != 1 || !strings.Contains(out, expectedID) {
						t.Fatalf("completed send became a failure or dispatched again: dryRun=%v exit=%d sends=%d out=%s", dryRun, code, len(sent()), out)
					}
				}
				commands, _ := changed.snapshot()
				joined := strings.Join(commands, "\n")
				if !strings.Contains(joined, `EXAMINE "`+mailbox+`"`) || workflowOpenedMailbox(commands, "Entwuerfe") {
					t.Fatalf("replay read the newly selected mailbox instead of the recorded one: %v", commands)
				}
				after, err := os.ReadFile(base[4])
				if err != nil || string(before) != string(after) {
					t.Fatalf("replay changed its receipt: err=%v", err)
				}
				for _, changedField := range []string{"confirmation", "draft", "body"} {
					conflictArgs, conflictInput := append([]string(nil), args...), input
					if changedField == "body" {
						changed.mu.Lock()
						changed.raw += " changed after sending"
						changed.mu.Unlock()
					} else if mode == "single" {
						flag := "--confirm-send"
						if changedField == "draft" {
							flag = "--draft-id"
						}
						for i, arg := range conflictArgs {
							if arg == flag {
								conflictArgs[i+1] = "42"
							}
						}
					} else {
						field := "confirm_send"
						if changedField == "draft" {
							field = "draft_id"
						}
						conflictInput = strings.Replace(input, `"`+field+`":"41"`, `"`+field+`":"42"`, 1)
					}
					code, out = runWorkflow(t, conflictArgs, conflictInput)
					wantCode := 6
					if mode == "item" {
						wantCode = 1
					}
					if code != wantCode || !strings.Contains(out, "idempotency_conflict") || len(sent()) != 1 {
						t.Fatalf("changed %s bypassed conflict: exit=%d sends=%d out=%s", changedField, code, len(sent()), out)
					}
				}
			})
		}
	}
}

func rewriteReplayRecord(t *testing.T, path, key string, update func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	update(state["idempotency"].(map[string]any)[key].(map[string]any))
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySendAliasReceiptDoesNotAuthorizeAmbiguousMailboxRead(t *testing.T) {
	for _, mode := range []string{"single", "item"} {
		t.Run(mode, func(t *testing.T) {
			base := imapCommandArgs(t, newWorkflowServer(t, &workflowServer{}))
			sent := captureWorkflowSMTP(t, base)
			args := append(append([]string(nil), base...), "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "legacy-alias")
			input := ""
			if mode == "item" {
				args = append(append([]string(nil), base...), "message", "send-many", "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41","idempotency_key":"legacy-alias"}]`
			}
			if code, out := runWorkflow(t, args, input); code != 0 {
				t.Fatalf("initial send failed: exit=%d out=%s", code, out)
			}
			rewriteReplayRecord(t, base[4], "legacy-alias", func(record map[string]any) {
				delete(record, "sourceId")
				record["response"].(map[string]any)["draftId"] = "imap:Drafts:41"
			})
			changed := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`}}
			cfg, err := config.Load(base[2])
			if err != nil {
				t.Fatal(err)
			}
			configureIMAPTestEndpoint(t, &cfg, newWorkflowServer(t, changed))
			if err := config.Save(base[2], cfg); err != nil {
				t.Fatal(err)
			}
			code, out := runWorkflow(t, args, input)
			commands, _ := changed.snapshot()
			if code == 0 || !strings.Contains(out, "ambiguous") || workflowOpenedMailbox(commands, "") || len(sent()) != 1 {
				t.Fatalf("legacy alias was treated as an exact target: exit=%d sends=%d commands=%v out=%s", code, len(sent()), commands, out)
			}
		})
	}
}
