package app

import (
	"fmt"
	"strings"
	"testing"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func TestBuildIMAPCriteriaDatesAndFields(t *testing.T) {
	criteria, err := buildIMAPCriteria("invoice", "billing subject", "billing@example.com", "me@example.com", "finance", true, "120", "2026-01-01", "2026-02-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"TEXT \"invoice\"", "SUBJECT \"billing subject\"", "FROM \"billing@example.com\"", "TO \"me@example.com\"", "KEYWORD \"finance\"", "UNSEEN", "UID 120:*", "SINCE 01-Jan-2026", "BEFORE 01-Feb-2026"} {
		if !strings.Contains(criteria, want) {
			t.Fatalf("criteria missing %q: %s", want, criteria)
		}
	}
}

func TestBuildIMAPCriteriaInvalidDate(t *testing.T) {
	_, err := buildIMAPCriteria("", "", "", "", "", false, "", "2026-99-99", "")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildIMAPCriteriaInvalidSinceID(t *testing.T) {
	_, err := buildIMAPCriteria("", "", "", "", "", false, "abc", "", "")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestSaveDraftWithFallbackWhenAppendFails(t *testing.T) {
	prevSend := smtpSendFn
	prevOpen := openBridgeClientFn
	defer func() {
		smtpSendFn = prevSend
		openBridgeClientFn = prevOpen
	}()

	smtpCalled := false
	smtpSendFn = func(_ bridge.SMTPConfig, _ bridge.SendInput) error {
		smtpCalled = true
		return nil
	}

	fake := &fakeIMAPDraftClient{searchUIDs: map[string][]string{"INBOX": {"88"}, "Drafts": {"99"}}, draftMailbox: "Drafts"}
	openBridgeClientFn = func(_ config.Config, _ *model.State, _ string) (imapDraftClient, string, string, error) {
		return fake, "u", "p", nil
	}

	primary := &fakeIMAPDraftClient{appendErr: fmt.Errorf("append failed")}
	t.Setenv("PMAIL_SMTP_PASSWORD", "secret")
	cfg := config.Default()
	cfg.Bridge.Username = "u@example.com"
	uid, err := saveDraftWithFallback(primary, cfg, &model.State{Auth: model.AuthState{Username: "u@example.com"}}, "u@example.com", []string{"a@example.com"}, "s", "b", "raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != "99" {
		t.Fatalf("expected uid 99 got %s", uid)
	}
	if !smtpCalled {
		t.Fatalf("expected smtp fallback to be used")
	}
}

type fakeIMAPDraftClient struct {
	appendErr    error
	appendUID    string
	draftMailbox string
	searchUIDs   map[string][]string
	moved        bool
}

func (f *fakeIMAPDraftClient) AppendDraft(raw string) (string, error) {
	if f.appendErr != nil {
		return "", f.appendErr
	}
	if f.appendUID == "" {
		return "1", nil
	}
	return f.appendUID, nil
}

func (f *fakeIMAPDraftClient) DraftMailboxName() (string, error) {
	if f.draftMailbox == "" {
		return "Drafts", nil
	}
	return f.draftMailbox, nil
}

func (f *fakeIMAPDraftClient) SearchUIDs(mailbox, criteria string) ([]string, error) {
	_ = criteria
	if f.searchUIDs == nil {
		return []string{}, nil
	}
	return f.searchUIDs[mailbox], nil
}

func (f *fakeIMAPDraftClient) MoveUID(srcMailbox, uid, dstMailbox string) error {
	_ = srcMailbox
	_ = uid
	_ = dstMailbox
	f.moved = true
	return nil
}

func (f *fakeIMAPDraftClient) Close() error { return nil }
