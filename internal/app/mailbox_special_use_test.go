package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecialUseDraftsMailboxIdentity(t *testing.T) {
	for _, tc := range []struct{ name, row string }{
		{"Brouillons", `* LIST (\Drafts) "/" "Brouillons"`},
		{"Entwuerfe", `* LIST (\dRaFtS) "/" Entwuerfe`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{listRows: []string{`* LIST () NIL INBOX`, tc.row}}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			code, out := runWorkflow(t, append(append([]string(nil), base...), "mailbox", "list"), "")
			if code != 0 {
				t.Fatalf("list exit=%d out=%s", code, out)
			}
			boxes := regressionData(t, out)["mailboxes"].([]any)
			if len(boxes) != 2 {
				t.Fatalf("expected both quoted/atom mailbox names: %s", out)
			}
			for _, value := range boxes {
				box := value.(map[string]any)
				wantID := "inbox"
				if box["name"] == tc.name {
					wantID = "drafts"
				}
				if box["id"] != wantID || box["kind"] != "system" {
					t.Fatalf("special-use identity lost: %s", out)
				}
			}
			for _, query := range []struct{ name, match string }{
				{"drafts", "id_exact"}, {tc.name, "name_exact"}, {strings.ToUpper(tc.name), "name_casefold"},
			} {
				code, out := runWorkflow(t, append(append([]string(nil), base...), "mailbox", "resolve", "--name", query.name), "")
				if code != 0 {
					t.Fatalf("resolve %q exit=%d out=%s", query.name, code, out)
				}
				data := regressionData(t, out)
				box := data["mailbox"].(map[string]any)
				if box["name"] != tc.name || box["id"] != "drafts" || box["kind"] != "system" || data["matchedBy"] != query.match {
					t.Fatalf("incorrect resolution for %q: %s", query.name, out)
				}
			}
		})
	}
}

func TestSpecialUseDraftsIDCollisionRequiresExactName(t *testing.T) {
	s := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`}}
	base := imapCommandArgs(t, newWorkflowServer(t, s))
	code, out := runWorkflow(t, append(append([]string(nil), base...), "mailbox", "resolve", "--name", "drafts"), "")
	if code != 2 || !strings.Contains(out, "ambiguous") {
		t.Fatalf("duplicate canonical ID selected a mailbox: exit=%d out=%s", code, out)
	}
	var failure struct{ Error struct{ Hint string } }
	if err := json.Unmarshal([]byte(out), &failure); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Entwuerfe", "Drafts"} {
		if !strings.Contains(failure.Error.Hint, name) {
			t.Errorf("ambiguity hint omits exact server name %q: %s", name, failure.Error.Hint)
		}
		code, out := runWorkflow(t, append(append([]string(nil), base...), "mailbox", "resolve", "--name", name), "")
		if code != 0 {
			t.Fatalf("exact name %q failed: exit=%d out=%s", name, code, out)
		}
		data := regressionData(t, out)
		if data["mailbox"].(map[string]any)["name"] != name || data["matchedBy"] != "name_exact" {
			t.Fatalf("exact name precedence changed: %s", out)
		}
	}
}

func TestExactLowercaseDraftsNameWinsOverLocalizedRole(t *testing.T) {
	s := &workflowServer{listRows: []string{`* LIST () "/" "drafts"`, `* LIST (\Drafts) "/" "Brouillons"`}}
	base := imapCommandArgs(t, newWorkflowServer(t, s))
	for _, name := range []string{"drafts", "Brouillons"} {
		code, out := runWorkflow(t, append(base, "mailbox", "resolve", "--name", name), "")
		if code != 0 {
			t.Fatalf("resolve %q: exit=%d out=%s", name, code, out)
		}
		data := regressionData(t, out)
		if data["mailbox"].(map[string]any)["name"] != name || data["matchedBy"] != "name_exact" {
			t.Fatalf("resolve %q: %s", name, out)
		}
	}
}

func TestAmbiguousLegacyDraftIDRejectedBeforeMailboxAccess(t *testing.T) {
	const old = "imap:Drafts:41"
	for _, tc := range []struct {
		args  []string
		input string
	}{
		{[]string{"draft", "get", "--draft-id", old}, ""},
		{[]string{"draft", "get", "--draft-id", "41"}, ""},
		{[]string{"draft", "update", "--draft-id", old, "--subject", "preview"}, ""},
		{[]string{"draft", "delete", "--draft-id", old}, ""},
		{[]string{"message", "send", "--draft-id", old, "--confirm-send", old}, ""},
		{[]string{"message", "send-many", "--stdin"}, `[{"draft_id":"imap:Drafts:41","confirm_send":"41"}]`},
	} {
		t.Run(strings.Join(tc.args[:2], "-"), func(t *testing.T) {
			s := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`}}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "--dry-run")
			code, out := runWorkflow(t, append(args, tc.args...), tc.input)
			cmds, _ := s.snapshot()
			if code == 0 || !strings.Contains(out, "validation_error") || !strings.Contains(out, "ambiguous") || workflowOpenedMailbox(cmds, "") {
				t.Fatalf("legacy ID exit=%d out=%s commands=%v", code, out, cmds)
			}
		})
	}
}

func TestDraftReceiptsQualifyActualServerMailbox(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		input, id string
	}{
		{[]string{"draft", "list"}, "", "imap:Entwuerfe:41"},
		{[]string{"search", "drafts"}, "", "imap:Entwuerfe:41"},
		{[]string{"draft", "get", "--draft-id", "imap:Entwuerfe:41"}, "", "imap:Entwuerfe:41"},
		{[]string{"--dry-run", "message", "send", "--draft-id", "imap:Entwuerfe:41", "--confirm-send", "41"}, "", "imap:Entwuerfe:41"},
		{[]string{"draft", "create", "--to", "fixture@example.invalid", "--body", "fixture"}, "", "imap:Entwuerfe:91"},
		{[]string{"draft", "create-many", "--stdin"}, `[{"to":["fixture@example.invalid"],"body":"fixture"}]`, "imap:Entwuerfe:91"},
		{[]string{"message", "follow-up", "--message-id", "41", "--body", "fixture"}, "", "imap:Entwuerfe:91"},
	} {
		s := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, `* LIST () "/" "Drafts"`}}
		base := imapCommandArgs(t, newWorkflowServer(t, s))
		code, out := runWorkflow(t, append(base, tc.args...), tc.input)
		if code != 0 || !strings.Contains(out, tc.id) {
			t.Errorf("%v exit=%d missing %s in %s", tc.args, code, tc.id, out)
		}
	}
}

func TestUnambiguousLegacyDraftIDStillResolves(t *testing.T) {
	for _, id := range []string{"41", "imap:Drafts:41", "imap:Brouillons:41"} {
		s := &workflowServer{draftBox: "Brouillons"}
		args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "get", "--draft-id", id)
		code, out := runWorkflow(t, args, "")
		if code != 0 || !strings.Contains(out, "imap:Brouillons:41") {
			t.Fatalf("legacy/localized ID %q on unambiguous server exit=%d out=%s", id, code, out)
		}
	}
}

func TestResolveAcceptsExactNameFromAmbiguityHint(t *testing.T) {
	s := &workflowServer{listRows: []string{`* LIST () "/" "Project "`, `* LIST () "/" "Project"`}}
	base := append(imapCommandArgs(t, newWorkflowServer(t, s)), "mailbox", "resolve", "--name")
	code, out := runWorkflow(t, append(base, "project"), "")
	if code != 2 || !strings.Contains(out, "ambiguous") {
		t.Fatalf("collision exit=%d out=%s", code, out)
	}
	code, out = runWorkflow(t, append(base, "Project "), "")
	if code != 0 || regressionData(t, out)["mailbox"].(map[string]any)["name"] != "Project " {
		t.Fatalf("hinted exact name failed: exit=%d out=%s", code, out)
	}
}
