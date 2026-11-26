package app

import (
	"fmt"
	"os"
	"protonmailcli/internal/config"
	"strings"
	"testing"
)

// Pre-upgrade Go JSON hash of the fixture request after its source mailbox was
// normalized to imap:Project:41. It includes the explicit recipient, subject,
// body, and the synthetic message's Message-ID and complete References chain.
const legacyFollowUpHash = "55236c47635e9ba1c8f0564a4f7e8cca78406d9fac0bd1658fb031a7b5b62531"

func TestLegacyFollowUpReceiptReplaysUnambiguousWhitespaceSource(t *testing.T) {
	for _, mailbox := range []string{"Project ", " Project", " Project "} {
		t.Run(mailbox, func(t *testing.T) {
			s := &workflowServer{listRows: []string{fmt.Sprintf(`* LIST () "/" %q`, mailbox)}}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			state := fmt.Sprintf(`{"idempotency":{"legacy-follow-up":{"status":"complete","operation":"message.follow-up","payloadHash":%q,"response":{"draft":{"id":"imap:Drafts:91"},"source":"imap"}}}}`, legacyFollowUpHash)
			if err := os.WriteFile(base[4], []byte(state), 0600); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string(nil), base...), "message", "follow-up", "--message-id", "imap:"+mailbox+":41", "--to", "reply@example.invalid", "--subject", "reply subject", "--body", "reply body", "--idempotency-key", "legacy-follow-up")
			for _, dryRun := range []bool{false, true} {
				replayArgs := args
				if dryRun {
					replayArgs = append([]string{"--dry-run"}, args...)
				}
				code, out := runWorkflow(t, replayArgs, "")
				if code != 0 || !strings.Contains(out, `"id":"imap:Drafts:91"`) {
					t.Fatalf("legacy follow-up did not replay: dryRun=%v exit=%d out=%s", dryRun, code, out)
				}
			}
			commands, appended := s.snapshot()
			if len(appended) != 0 || !strings.Contains(strings.Join(commands, "\n"), `EXAMINE "`+mailbox+`"`) {
				t.Fatalf("legacy retry dispatched or fetched the wrong source: commands=%v appends=%d", commands, len(appended))
			}
			after, err := os.ReadFile(base[4])
			if err != nil || string(after) != state {
				t.Fatalf("legacy replay changed state: err=%v", err)
			}
			changedArgs := append([]string(nil), args...)
			for i, arg := range changedArgs {
				if arg == "--body" {
					changedArgs[i+1] = "changed reply body"
				}
			}
			code, out := runWorkflow(t, changedArgs, "")
			if code != 6 || !strings.Contains(out, "idempotency_conflict") {
				t.Fatalf("legacy compatibility hid a changed body: exit=%d out=%s", code, out)
			}
		})
	}
}

func TestNewFollowUpReceiptDoesNotUseLegacyWhitespaceHash(t *testing.T) {
	original := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" "Drafts"`, `* LIST () "/" "Project"`}}
	base := imapCommandArgs(t, newWorkflowServer(t, original))
	args := append(append([]string(nil), base...), "message", "follow-up", "--message-id", "imap:Project:41", "--to", "reply@example.invalid", "--subject", "reply subject", "--body", "reply body", "--idempotency-key", "exact-follow-up")
	if code, out := runWorkflow(t, args, ""); code != 0 {
		t.Fatalf("initial follow-up failed: exit=%d out=%s", code, out)
	}
	changed := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" "Drafts"`, `* LIST () "/" "Project "`}}
	cfg, err := config.Load(base[2])
	if err != nil {
		t.Fatal(err)
	}
	configureIMAPTestEndpoint(t, &cfg, newWorkflowServer(t, changed))
	if err := config.Save(base[2], cfg); err != nil {
		t.Fatal(err)
	}
	for i, arg := range args {
		if arg == "--message-id" {
			args[i+1] = "imap:Project :41"
		}
	}
	code, out := runWorkflow(t, args, "")
	_, appended := changed.snapshot()
	if code != 6 || !strings.Contains(out, "idempotency_conflict") || len(appended) != 0 {
		t.Fatalf("new exact-source receipt accepted a different mailbox: exit=%d appends=%d out=%s", code, len(appended), out)
	}
}

func TestLegacyFollowUpHashRequiresOneExactListedSource(t *testing.T) {
	for _, otherMailbox := range []string{"Project", " Project", "missing-source"} {
		t.Run(otherMailbox, func(t *testing.T) {
			rows := []string{fmt.Sprintf(`* LIST () "/" %q`, otherMailbox)}
			if otherMailbox != "missing-source" {
				rows = append(rows, `* LIST () "/" "Project "`)
			}
			s := &workflowServer{listRows: rows}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			state := fmt.Sprintf(`{"idempotency":{"legacy-follow-up":{"operation":"message.follow-up","payloadHash":%q,"response":{"draft":{"id":"imap:Drafts:91"},"source":"imap"}}}}`, legacyFollowUpHash)
			if err := os.WriteFile(base[4], []byte(state), 0600); err != nil {
				t.Fatal(err)
			}
			args := append(base, "message", "follow-up", "--message-id", "imap:Project :41", "--to", "reply@example.invalid", "--subject", "reply subject", "--body", "reply body", "--idempotency-key", "legacy-follow-up")
			code, out := runWorkflow(t, args, "")
			_, appended := s.snapshot()
			if code != 6 || !strings.Contains(out, "idempotency_conflict") || len(appended) != 0 {
				t.Fatalf("unproven legacy source identity replayed or dispatched: exit=%d appends=%d out=%s", code, len(appended), out)
			}
		})
	}
}
