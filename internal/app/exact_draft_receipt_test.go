package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func TestNewDraftReceiptNeverFollowsRoleToAnotherMailbox(t *testing.T) {
	for _, originalMailbox := range []string{"Drafts", "Brouillons"} {
		for _, action := range []string{"get", "update", "delete", "send", "send-many"} {
			t.Run(originalMailbox+"/"+action, func(t *testing.T) {
				original := &workflowServer{draftBox: originalMailbox}
				base := imapCommandArgs(t, newWorkflowServer(t, original))
				create := append(append([]string(nil), base...), "draft", "create", "--to", "intended@example.invalid", "--subject", "original draft", "--body", "original body")
				code, out := runWorkflow(t, create, "")
				if code != 0 {
					t.Fatalf("create original receipt: exit=%d out=%s", code, out)
				}
				id := regressionData(t, out)["draft"].(map[string]any)["id"].(string)
				// The original folder is gone. The new Drafts-role mailbox contains
				// an unrelated message with the same numerical UID.
				nextMailbox := "Brouillons"
				if originalMailbox == "Brouillons" {
					nextMailbox = "Entwuerfe"
				}
				changed := &workflowServer{
					unavailableMailbox: originalMailbox,
					listRows:           []string{"* LIST (\\Drafts) \"/\" \"" + nextMailbox + "\""},
					raw:                "From: other@example.invalid\r\nTo: unintended@example.invalid\r\nSubject: unrelated draft\r\n\r\nunrelated body",
				}
				cfg, err := config.Load(base[2])
				if err != nil {
					t.Fatal(err)
				}
				configureIMAPTestEndpoint(t, &cfg, newWorkflowServer(t, changed))
				if err := config.Save(base[2], cfg); err != nil {
					t.Fatal(err)
				}
				sent := captureWorkflowSMTP(t, base)
				args := append([]string(nil), base...)
				input := ""
				switch action {
				case "get", "delete":
					args = append(args, "draft", action, "--draft-id", id)
				case "update":
					args = append(args, "draft", action, "--draft-id", id, "--subject", "changed")
				case "send":
					args = append(args, "message", "send", "--draft-id", id, "--confirm-send", id)
				case "send-many":
					args = append(args, "message", "send-many", "--stdin")
					manifest, err := json.Marshal([]map[string]string{{"draft_id": id, "confirm_send": id}})
					if err != nil {
						t.Fatal(err)
					}
					input = string(manifest)
				}
				code, out = runWorkflow(t, args, input)
				commands, appended := changed.snapshot()
				if code == 0 || len(sent()) != 0 || len(appended) != 0 ||
					workflowOpenedMailbox(commands, nextMailbox) ||
					strings.Contains(strings.Join(commands, "\n"), "UID STORE") {
					t.Fatalf("receipt %q redirected after topology change: action=%s exit=%d smtp=%d commands=%v out=%s", id, action, code, len(sent()), commands, out)
				}
			})
		}
	}
}

func TestVersionedDraftsIDPreservesExistingFollowUpPayload(t *testing.T) {
	server := &workflowServer{draftBox: "Drafts"}
	args, _ := legacySourceReplayArgs(t, server, "Drafts")
	for i, arg := range args {
		if arg == "--message-id" {
			args[i+1] = "imap-v2:Drafts:41"
		}
	}
	// This is the canonical payload recorded before versioned exact receipts.
	const payload = "{\"body\":\"reply body\",\"inReplyTo\":\"\\u003cdraft@example.invalid\\u003e\",\"messageId\":\"imap:Drafts:41\",\"references\":[\"\\u003croot@example.invalid\\u003e\",\"\\u003cparent@example.invalid\\u003e\",\"\\u003cdraft@example.invalid\\u003e\"],\"subject\":\"reply subject\",\"to\":[\"reply@example.invalid\"]}"
	hash := sha256.Sum256([]byte(payload))
	rewriteReplayRecord(t, args[4], "legacy-source", func(record map[string]any) {
		record["payloadHash"] = fmt.Sprintf("%x", hash)
		record["sourceId"] = "imap:Drafts:41"
	})
	code, out := runWorkflow(t, args, "")
	_, appended := server.snapshot()
	if code != 0 || !strings.Contains(out, "imap:Drafts:91") || len(appended) != 0 {
		t.Fatalf("versioned source changed an existing follow-up hash: exit=%d appends=%d out=%s", code, len(appended), out)
	}
}

func TestVersionedDraftsIDWorksAcrossCommandConsumers(t *testing.T) {
	const id = "imap-v2:Drafts:41"
	for _, tc := range []struct {
		name, input, expected string
		args                  []string
	}{
		{"draft list", "", id, []string{"draft", "list"}},
		{"draft search", "", id, []string{"search", "drafts"}},
		{"message search", "", id, []string{"search", "messages", "--mailbox", "Drafts"}},
		{"draft get", "", id, []string{"draft", "get", "--draft-id", id}},
		{"message get", "", id, []string{"message", "get", "--message-id", id}},
		{"draft update", "", id, []string{"--dry-run", "draft", "update", "--draft-id", id, "--subject", "changed"}},
		{"draft delete", "", id, []string{"--dry-run", "draft", "delete", "--draft-id", id}},
		{"single send", "", id, []string{"--dry-run", "message", "send", "--draft-id", id, "--confirm-send", id}},
		{"batch send", "[{\"draft_id\":\"imap-v2:Drafts:41\",\"confirm_send\":\"imap-v2:Drafts:41\"}]", id, []string{"--dry-run", "message", "send-many", "--stdin"}},
		{"tag add", "", id, []string{"--dry-run", "tag", "add", "--message-id", id, "--tag", "reviewed"}},
		{"tag remove", "", id, []string{"--dry-run", "tag", "remove", "--message-id", id, "--tag", "reviewed"}},
		{"follow-up preview", "", id, []string{"--dry-run", "message", "follow-up", "--message-id", id, "--body", "reply"}},
		{"single create", "", "imap-v2:Drafts:91", []string{"draft", "create", "--to", "fixture@example.invalid", "--body", "fixture"}},
		{"batch create", "[{\"to\":[\"fixture@example.invalid\"],\"body\":\"fixture\"}]", "imap-v2:Drafts:91", []string{"draft", "create-many", "--stdin"}},
		{"follow-up create", "", "imap-v2:Drafts:91", []string{"message", "follow-up", "--message-id", id, "--body", "reply"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The selected literal folder is exact even when the old alias
			// would be ambiguous because another Drafts role is advertised.
			server := &workflowServer{listRows: []string{"* LIST (\\Drafts) \"/\" Drafts", "* LIST (\\Drafts) \"/\" Brouillons"}}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			code, out := runWorkflow(t, append(base, tc.args...), tc.input)
			commands, _ := server.snapshot()
			if code != 0 || !strings.Contains(out, tc.expected) || workflowOpenedMailbox(commands, "Brouillons") {
				t.Fatalf("versioned exact ID failed: exit=%d out=%s commands=%v", code, out, commands)
			}
		})
	}
}
