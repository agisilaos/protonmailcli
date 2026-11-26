package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func legacySourceReplayArgs(t *testing.T, server *workflowServer, mailbox string) ([]string, string) {
	t.Helper()
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	state := fmt.Sprintf(`{"idempotency":{"legacy-source":{"operation":"message.follow-up","status":"complete","payloadHash":%q,"response":{"draft":{"id":"imap:Drafts:91"},"source":"imap"}}}}`, legacyFollowUpHash)
	if err := os.WriteFile(base[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	return append(base, "message", "follow-up", "--message-id", "imap:"+mailbox+":41", "--to", "reply@example.invalid", "--subject", "reply subject", "--body", "reply body", "--idempotency-key", "legacy-source"), state
}

func TestLegacyFollowUpAcceptsInboxCaseVariantsAsOneSource(t *testing.T) {
	s := &workflowServer{listRows: []string{`* LIST () "/" "inbox"`, `* LIST () "/" "INBOX"`}}
	args, _ := legacySourceReplayArgs(t, s, "INBOX")
	const payload = `{"body":"reply body","inReplyTo":"\u003cdraft@example.invalid\u003e","messageId":"imap:INBOX:41","references":["\u003croot@example.invalid\u003e","\u003cparent@example.invalid\u003e","\u003cdraft@example.invalid\u003e"],"subject":"reply subject","to":["reply@example.invalid"]}`
	hash := sha256.Sum256([]byte(payload))
	rewriteReplayRecord(t, args[4], "legacy-source", func(record map[string]any) { record["payloadHash"] = fmt.Sprintf("%x", hash) })
	code, out := runWorkflow(t, args, "")
	_, appended := s.snapshot()
	if code != 0 || !strings.Contains(out, "imap:Drafts:91") || len(appended) != 0 {
		t.Fatalf("INBOX case variants became distinct sources: exit=%d out=%s", code, out)
	}
}

func TestLegacyFollowUpMatchingNormalizedHashStillChecksSourceAmbiguity(t *testing.T) {
	// Both folders expose the same fixture message, so recipients and threading
	// cannot disambiguate the old hash that discarded mailbox whitespace.
	s := &workflowServer{listRows: []string{`* LIST () "/" "Project"`, `* LIST () "/" "Project "`}}
	args, state := legacySourceReplayArgs(t, s, "Project")
	code, out := runWorkflow(t, args, "")
	commands, appended := s.snapshot()
	if code != 6 || !strings.Contains(out, "idempotency_conflict") || len(appended) != 0 || workflowOpenedMailbox(commands, "") {
		t.Fatalf("matching normalized hash bypassed source identity: exit=%d commands=%v out=%s", code, commands, out)
	}
	if after, err := os.ReadFile(args[4]); err != nil || string(after) != state {
		t.Fatalf("rejected replay changed state: %v", err)
	}
}

func TestLegacyFollowUpDistinguishesMissingSourceFromTransportFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		server       *workflowServer
		exit         int
		code         string
		selectSource bool
	}{
		{"missing-source", &workflowServer{listRows: []string{`* LIST () "/" "Project"`}, unavailableMailbox: "Project "}, 6, "idempotency_conflict", false},
		{"listing-failed", &workflowServer{listFailure: true}, 4, "imap_message_fetch_failed", false},
		{"fetch-failed", &workflowServer{listRows: []string{`* LIST () "/" "Project "`}, fetchFailure: true}, 4, "imap_message_fetch_failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, state := legacySourceReplayArgs(t, tc.server, "Project ")
			code, out := runWorkflow(t, args, "")
			commands, appended := tc.server.snapshot()
			selected := workflowOpenedMailbox(commands, "")
			if code != tc.exit || !strings.Contains(out, tc.code) || selected != tc.selectSource || len(appended) != 0 {
				t.Fatalf("source/transport classification failed: exit=%d selected=%t commands=%v out=%s", code, selected, commands, out)
			}
			if after, err := os.ReadFile(args[4]); err != nil || string(after) != state {
				t.Fatalf("failed replay changed state: %v", err)
			}
		})
	}
}

func TestLegacyFollowUpPreservesOrdinaryMailboxCase(t *testing.T) {
	for _, mailbox := range []string{"Project", "Project "} {
		t.Run(mailbox, func(t *testing.T) {
			s := &workflowServer{listRows: []string{
				fmt.Sprintf(`* LIST () "/" %q`, mailbox), `* LIST () "/" "PROJECT"`,
			}}
			args, state := legacySourceReplayArgs(t, s, mailbox)
			code, out := runWorkflow(t, args, "")
			_, appended := s.snapshot()
			if code != 0 || !strings.Contains(out, "imap:Drafts:91") || len(appended) != 0 {
				t.Fatalf("legacy ID should distinguish ordinary mailbox case: exit=%d %s", code, out)
			}
			if after, err := os.ReadFile(args[4]); err != nil || string(after) != state {
				t.Fatalf("replay changed saved state: %v", err)
			}
		})
	}
}
