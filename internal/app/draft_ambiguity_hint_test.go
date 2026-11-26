package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAmbiguousDraftRecoveryInspectsEveryExactMailbox(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []string
		ids  []string
	}{
		{"legacy ordinary folder", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`}, []string{"imap-v2:Drafts:41", "imap:Entwuerfe:41"}},
		{"multiple roles", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST (\Drafts) "/" "Brouillons"`}, []string{"imap:Brouillons:41", "imap:Entwuerfe:41"}},
		{"case variant", []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "DRAFTS"`}, []string{"imap:DRAFTS:41", "imap:Entwuerfe:41"}},
		{"multiple fallback spellings", []string{`* LIST () "/" "Drafts"`, `* LIST () "/" "DRAFTS"`}, []string{"imap:DRAFTS:41", "imap-v2:Drafts:41"}},
		{"quoted exact name", []string{`* LIST (\Drafts) "/" "Brouillons d'été "`, `* LIST () "/" "Drafts"`}, []string{"imap:Brouillons d'été :41", "imap-v2:Drafts:41"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, legacyID := range []string{"imap:Drafts:41", "41"} {
				s := &workflowServer{listRows: tc.rows}
				base := imapCommandArgs(t, newWorkflowServer(t, s))
				args := append(append([]string(nil), base...), "--dry-run", "message", "send", "--draft-id", legacyID, "--confirm-send", legacyID)
				code, out := runWorkflow(t, args, "")
				var failure struct {
					Error struct{ Message, Hint string }
				}
				if err := json.Unmarshal([]byte(out), &failure); err != nil {
					t.Fatal(err)
				}
				guidance := failure.Error.Message + " " + failure.Error.Hint
				commands, _ := s.snapshot()
				if code != 2 || strings.Contains(guidance, "draft list") || workflowOpenedMailbox(commands, "") {
					t.Fatalf("unsafe or misleading ambiguity recovery: exit=%d out=%s commands=%v", code, out, commands)
				}
				for _, id := range tc.ids {
					quotedID := "'" + strings.ReplaceAll(id, "'", "'\\''") + "'"
					if !strings.Contains(guidance, " message get --message-id "+quotedID) {
						t.Fatalf("recovery omits exact read command for %q: %s", id, guidance)
					}
					// Each suggested ID must bypass draft-role selection and read its
					// exact mailbox through the public message-get command.
					readArgs := append(append([]string(nil), base...), "message", "get", "--message-id", id)
					readCode, readOut := runWorkflow(t, readArgs, "")
					if readCode != 0 || regressionData(t, readOut)["message"].(map[string]any)["id"] != id {
						t.Fatalf("suggested read command %q failed: exit=%d out=%s", id, readCode, readOut)
					}
				}
			}
		})
	}
}

func TestAmbiguousDraftCommandsRespectOutputRecordBoundaries(t *testing.T) {
	s := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" Drafts`}}
	base := imapCommandArgs(t, newWorkflowServer(t, s))
	command := []string{"--dry-run", "draft", "get", "--draft-id", "41"}
	plainArgs := append(append([]string{"--plain"}, base[1:]...), command...)
	code, out := runWorkflow(t, plainArgs, "")
	if code != 2 || strings.Count(out, "\n") != 1 || strings.Count(out, `\nprotonmailcli `) != 2 {
		t.Fatalf("cannot read candidate commands from one plain record: exit=%d output=%q", code, out)
	}
	humanArgs := append(append([]string(nil), base[1:]...), command...)
	code, out = runWorkflow(t, humanArgs, "")
	if code != 2 || strings.Count(out, "\nprotonmailcli ") != 2 || !strings.HasSuffix(strings.TrimSuffix(out, "\n"), "'") {
		t.Fatalf("cannot copy candidate commands from human output: exit=%d output=%q", code, out)
	}
}
