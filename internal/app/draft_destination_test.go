package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func TestDraftCreationNeedsSelectableDestinationBeforeFallback(t *testing.T) {
	prev := smtpSendFn
	t.Cleanup(func() { smtpSendFn = prev })
	for _, tc := range []struct {
		name   string
		rows   []string
		reject string
	}{
		{"no draft mailbox", []string{`* LIST () NIL INBOX`}, "Drafts"},
		{"nonselectable role", []string{`* LIST (\Drafts) NIL Brouillons`, `* LIST (\nOsElEcT) NIL "Brouillons"`}, "Brouillons"},
		{"nonselectable name", []string{`* LIST (\Noselect) NIL DRAFTS`}, "DRAFTS"},
		{"listed but inaccessible", []string{`* LIST (\Drafts) NIL Brouillons`}, "Brouillons"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			smtpCalls := 0
			smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
				smtpCalls++
				return errors.New("fixture SMTP call")
			}
			server := &workflowServer{listRows: tc.rows, unavailableMailbox: tc.reject}
			args := append(imapCommandArgs(t, newWorkflowServer(t, server)), "draft", "create", "--to", "recipient@example.invalid", "--body", "fixture")
			code, out := runWorkflow(t, args, "")
			commands, _ := server.snapshot()
			if code != 4 || !strings.Contains(out, "imap_draft_create_failed") || smtpCalls != 0 || strings.Contains(strings.Join(commands, "\n"), " APPEND ") {
				t.Fatalf("exit=%d smtp=%d commands=%v output=%s", code, smtpCalls, commands, out)
			}
			if tc.name != "listed but inaccessible" && strings.Contains(strings.Join(commands, "\n"), " SELECT ") {
				t.Fatalf("attempted SELECT without a listed selectable destination: %v", commands)
			}
		})
	}
}

func TestFreshDraftPreviewsCheckDestinationReadOnly(t *testing.T) {
	for _, plan := range []struct {
		name  string
		args  []string
		input string
	}{
		{"single", []string{"draft", "create", "--to", "user@example.invalid", "--body", "fixture"}, ""},
		{"batch", []string{"draft", "create-many", "--stdin"}, `[{"to":["user@example.invalid"],"body":"one"},{"to":["user@example.invalid"],"body":"two"}]`},
		{"follow-up", []string{"message", "follow-up", "--message-id", "imap:INBOX:41", "--body", "fixture"}, ""},
	} {
		for _, situation := range []struct {
			name        string
			rows        []string
			unavailable string
			wantExit    int
			wantExamine int
		}{
			{"available", []string{`* LIST (\Drafts) NIL Brouillons`}, "", 0, 1},
			{"ambiguous", []string{`* LIST () NIL drafts`, `* LIST () NIL DRAFTS`}, "", 4, 0},
			{"inaccessible", []string{`* LIST (\Drafts) NIL Brouillons`}, "Brouillons", 4, 1},
		} {
			t.Run(plan.name+"/"+situation.name, func(t *testing.T) {
				server := &workflowServer{listRows: situation.rows, unavailableMailbox: situation.unavailable}
				base := imapCommandArgs(t, newWorkflowServer(t, server))
				args := append(append([]string{"--dry-run"}, base...), plan.args...)
				code, out := runWorkflow(t, args, plan.input)
				cmds, appends := server.snapshot()
				lines := strings.Join(cmds, "\n")
				if code != situation.wantExit || strings.Count(lines, ` EXAMINE "Brouillons"`) != situation.wantExamine || len(appends) != 0 ||
					strings.Contains(lines, ` SELECT "Brouillons"`) || strings.Contains(lines, " APPEND ") {
					t.Fatalf("exit=%d commands=%v out=%s", code, cmds, out)
				}
				if code != 0 && !strings.Contains(out, "imap_draft_create_failed") {
					t.Fatalf("missing creation failure in %s", out)
				}
				if _, err := os.Stat(base[4]); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("preview created state: %v", err)
				}
			})
		}
	}
}

func TestDraftBatchPreviewPreservesPriorItemReceipts(t *testing.T) {
	server := &workflowServer{listRows: []string{`* LIST () NIL INBOX`}}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	const state = `{"idempotency":{
		"complete":{"operation":"imap.draft.create-item","payloadHash":"4d5a9dad545a0f69861ed3f796f470a0fd4667770af87fc7f2958feebb194812","status":"complete","response":{"ok":true,"draftId":"imap:Brouillons:41","uid":"41"}},
		"pending":{"operation":"imap.draft.create-item","payloadHash":"4d5a9dad545a0f69861ed3f796f470a0fd4667770af87fc7f2958feebb194812","status":"pending"}
	}}`
	if err := os.WriteFile(base[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	const items = `[{"to":["user@example.invalid"],"body":"fixture","idempotency_key":"complete"},
		{"to":["user@example.invalid"],"body":"fixture","idempotency_key":"pending"},
		{"to":["user@example.invalid"],"body":"fixture","idempotency_key":"fresh"}]`
	args := append(base, "--dry-run", "draft", "create-many", "--stdin")
	code, out := runWorkflow(t, args, items)
	if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") {
		t.Fatalf("pending recovery must precede preview: exit=%d out=%s", code, out)
	}
	if commands, _ := server.snapshot(); len(commands) != 0 {
		t.Fatalf("pending recovery contacted IMAP: %v", commands)
	}
	if after, err := os.ReadFile(base[4]); err != nil || string(after) != state {
		t.Fatalf("pending preview changed receipts: %v", err)
	}
	// Without an unfinished item, preserve the terminal receipt even when the
	// fresh row cannot be created in the current mailbox topology.
	const terminalAndFresh = `[{"to":["user@example.invalid"],"body":"fixture","idempotency_key":"complete"},
		{"to":["user@example.invalid"],"body":"fixture","idempotency_key":"fresh"}]`
	code, out = runWorkflow(t, args, terminalAndFresh)
	if code != 10 || !strings.Contains(out, "imap:Brouillons:41") || strings.Contains(out, "imap_draft_create_uncertain") ||
		!strings.Contains(out, "imap_draft_create_failed") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if after, err := os.ReadFile(base[4]); err != nil || string(after) != state {
		t.Fatalf("preview changed receipts: %v", err)
	}
}

type vanishedDraftMailbox struct{ fakeIMAPDraftClient }

func (*vanishedDraftMailbox) ExamineDraftDestination() error {
	return errors.New("draft mailbox has disappeared")
}

func TestDraftFallbackChecksDestinationAfterAppendRejection(t *testing.T) {
	prev := smtpSendFn
	t.Cleanup(func() { smtpSendFn = prev })
	smtpCalls := 0
	smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
		smtpCalls++
		return errors.New("fixture SMTP call")
	}
	t.Setenv("PMAIL_SMTP_PASSWORD", "synthetic")
	cfg := config.Default()
	cfg.Bridge.Username = "user@example.invalid"
	client := &vanishedDraftMailbox{fakeIMAPDraftClient{appendErr: bridge.ErrAppendRejected}}
	_, _, err := saveDraftWithFallback(client, cfg, &model.State{}, cfg.Bridge.Username, nil, "", "", "", nil, nil)
	if smtpCalls != 0 || err == nil || !strings.Contains(err.Error(), "draft mailbox has disappeared") {
		t.Fatalf("smtp=%d err=%v", smtpCalls, err)
	}
}
