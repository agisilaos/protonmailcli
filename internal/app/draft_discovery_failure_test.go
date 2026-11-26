package app

import (
	"errors"
	"strings"
	"testing"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

type unresolvedDraftClient struct{ fakeIMAPDraftClient }

func (*unresolvedDraftClient) DraftMailboxName() (string, error) {
	return "", errors.New("incomplete listing")
}

func TestDraftDiscoveryFailureStopsBeforeFallback(t *testing.T) {
	t.Setenv("PMAIL_SMTP_PASSWORD", "fixture")
	original := smtpSendFn
	t.Cleanup(func() { smtpSendFn = original })
	smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
		t.Fatal("attempted SMTP fallback without a resolved draft mailbox")
		return nil
	}
	cfg := config.Default()
	cfg.Bridge.Username = "fixture@example.invalid"
	_, _, err := saveDraftWithFallback(&unresolvedDraftClient{}, cfg, &model.State{}, cfg.Bridge.Username, nil, "", "", "", nil, nil)
	if err == nil || err.Error() != "incomplete listing" {
		t.Fatalf("discovery error=%v", err)
	}
}

func TestLegacyDraftCannotBeDeletedAcrossMailboxDiscoveryVariants(t *testing.T) {
	for name, additionalRow := range map[string]string{
		"another role":    `* LIST (\Drafts) "/" "Brouillons"`,
		"literal mailbox": "* LIST () \"/\" {6}\r\nDrafts",
		"case variant":    `* LIST () "/" DRAFTS`,
	} {
		t.Run(name, func(t *testing.T) {
			server := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" Entwuerfe`, additionalRow}}
			args := append(imapCommandArgs(t, newWorkflowServer(t, server)), "--dry-run", "draft", "delete", "--draft-id", "imap:Drafts:41")
			code, out := runWorkflow(t, args, "")
			if code != 2 || !strings.Contains(out, "validation_error") || !strings.Contains(out, "ambiguous") {
				t.Fatalf("ambiguous deletion plan: exit=%d out=%s", code, out)
			}
		})
	}
}
