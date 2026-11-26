package app

import (
	"os"
	"protonmailcli/internal/config"
	"strings"
	"testing"
)

func TestRecordedSendReplayFetchesItsKnownUIDWithoutSearch(t *testing.T) {
	for _, mode := range []string{"single", "item"} {
		t.Run(mode, func(t *testing.T) {
			s := &workflowServer{draftBox: "Brouillons", lowercaseSearch: true}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, base)
			args := append(append([]string(nil), base...), "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "known-uid")
			input := ""
			if mode == "item" {
				args = append(append([]string(nil), base...), "message", "send-many", "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41","idempotency_key":"known-uid"}]`
			}
			for attempt := 0; attempt < 2; attempt++ {
				code, out := runWorkflow(t, args, input)
				if code != 0 || len(sent()) != 1 || !strings.Contains(out, "imap:Brouillons:41") {
					t.Fatalf("known UID replay depended on SEARCH: attempt=%d exit=%d sends=%d out=%s", attempt, code, len(sent()), out)
				}
			}
			commands, _ := s.snapshot()
			if strings.Contains(strings.Join(commands, "\n"), "UID SEARCH") {
				t.Fatalf("known UID unexpectedly searched: %v", commands)
			}
			s.mu.Lock()
			s.raw += " changed body"
			s.mu.Unlock()
			code, out := runWorkflow(t, args, input)
			if code == 0 || !strings.Contains(out, "idempotency_conflict") || len(sent()) != 1 {
				t.Fatalf("direct fetch skipped payload verification: exit=%d sends=%d out=%s", code, len(sent()), out)
			}
		})
	}
}

func TestRecordedSendReplayUsesOuterTrimmedIdentity(t *testing.T) {
	for _, mode := range []string{"legacy-item-receipt", "source-metadata"} {
		t.Run(mode, func(t *testing.T) {
			original := &workflowServer{draftBox: "Brouillons"}
			base := imapCommandArgs(t, newWorkflowServer(t, original))
			sent := captureWorkflowSMTP(t, base)
			const rawID = " \timap:Brouillons:41 \t"
			args := append(append([]string(nil), base...), "message", "send", "--draft-id", rawID, "--confirm-send", "41", "--idempotency-key", "spaced-source")
			input := ""
			if mode == "legacy-item-receipt" {
				args = append(append([]string(nil), base...), "message", "send-many", "--stdin")
				input = `[{"draft_id":" \timap:Brouillons:41 \t","confirm_send":"41","idempotency_key":"spaced-source"}]`
			}
			if code, out := runWorkflow(t, args, input); code != 0 || len(sent()) != 1 {
				t.Fatalf("first send failed: exit=%d sends=%d out=%s", code, len(sent()), out)
			}
			rewriteReplayRecord(t, base[4], "spaced-source", func(record map[string]any) {
				if mode == "legacy-item-receipt" {
					delete(record, "sourceId")
					record["response"].(map[string]any)["draftId"] = rawID
				} else {
					record["sourceId"] = rawID
				}
			})
			changed := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Brouillons"`}}
			cfg, err := config.Load(base[2])
			if err != nil {
				t.Fatal(err)
			}
			replacementArgs := imapCommandArgs(t, newWorkflowServer(t, changed))
			replacement, err := config.Load(replacementArgs[2])
			if err != nil {
				t.Fatal(err)
			}
			cfg.Bridge.IMAPPort = replacement.Bridge.IMAPPort
			cfg.Bridge.TLSCertFile = replacement.Bridge.TLSCertFile
			if err := config.Save(base[2], cfg); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(base[4])
			if err != nil {
				t.Fatal(err)
			}
			code, out := runWorkflow(t, args, input)
			commands, _ := changed.snapshot()
			if code != 0 || len(sent()) != 1 || !strings.Contains(strings.Join(commands, "\n"), `EXAMINE "Brouillons"`) {
				t.Fatalf("outer whitespace hid recorded identity: exit=%d sends=%d commands=%v out=%s", code, len(sent()), commands, out)
			}
			if after, err := os.ReadFile(base[4]); err != nil || string(after) != string(before) {
				t.Fatalf("identity normalization changed receipt state: %v", err)
			}
		})
	}
}
