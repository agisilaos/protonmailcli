package app

import (
	"bytes"
	"flag"
	"fmt"
	"io"
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

func TestLoadBodyFromStdinFlag(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) { return []byte("stdin body"), nil }

	body, err := loadBody("", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != "stdin body" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestLoadBodyRejectsMultipleBodyInputs(t *testing.T) {
	_, err := loadBody("inline", "", true)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestLoadDraftCreateManifestFromStdin(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) {
		return []byte(`[{"to":["a@example.com"],"subject":"s","body":"b"}]`), nil
	}

	items, err := loadDraftCreateManifest("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Subject != "s" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestLoadSendManyManifestFromStdin(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) {
		return []byte(`[{"draft_id":"imap:Drafts:1","confirm_send":"imap:Drafts:1"}]`), nil
	}

	items, err := loadSendManyManifest("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].DraftID != "imap:Drafts:1" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestReadManifestFromDashUsesStdin(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) { return []byte(`[]`), nil }

	b, err := readManifest("-", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("unexpected body: %q", string(b))
	}
}

func TestUsageForFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
	fs.String("file", "", "manifest json path or -")
	fs.Bool("stdin", false, "read manifest json from stdin")
	usage := usageForFlagSet(fs)
	if !strings.Contains(usage, "Usage of draft create-many:") {
		t.Fatalf("missing usage heading: %s", usage)
	}
	if !strings.Contains(usage, "-file") || !strings.Contains(usage, "-stdin") {
		t.Fatalf("missing expected flags: %s", usage)
	}
}

func TestLoadDraftCreateManifestAllowsPerItemValidation(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) {
		return []byte(`[{"subject":"s","body":"b"}]`), nil
	}
	items, err := loadDraftCreateManifest("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected item count: %d", len(items))
	}
}

func TestLoadSendManyManifestAllowsPerItemValidation(t *testing.T) {
	prev := readAllStdinFn
	defer func() { readAllStdinFn = prev }()
	readAllStdinFn = func() ([]byte, error) {
		return []byte(`[{"draft_id":"d_1"}]`), nil
	}
	items, err := loadSendManyManifest("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected item count: %d", len(items))
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
	uid, createPath, err := saveDraftWithFallback(primary, cfg, &model.State{Auth: model.AuthState{Username: "u@example.com"}}, "u@example.com", []string{"a@example.com"}, "s", "b", "raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != "99" {
		t.Fatalf("expected uid 99 got %s", uid)
	}
	if createPath != "smtp_move_fallback" {
		t.Fatalf("expected fallback path, got %s", createPath)
	}
	if !smtpCalled {
		t.Fatalf("expected smtp fallback to be used")
	}
}

func TestSaveDraftWithFallbackReturnsAppendPathOnSuccess(t *testing.T) {
	primary := &fakeIMAPDraftClient{appendUID: "77"}
	uid, createPath, err := saveDraftWithFallback(primary, config.Default(), &model.State{}, "u@example.com", []string{"a@example.com"}, "s", "b", "raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != "77" {
		t.Fatalf("expected uid 77 got %s", uid)
	}
	if createPath != "imap_append" {
		t.Fatalf("expected imap_append path, got %s", createPath)
	}
}

func TestJSONErrorEnvelopeHasCodeAndRetryable(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Bridge.Username = "me@example.com"
	cfgPath := tmp + "/config.toml"
	statePath := tmp + "/state.json"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	var stdout bytes.Buffer
	exit := Run([]string{"--json", "--config", cfgPath, "--state", statePath, "message", "send", "--draft-id", "missing"}, bytes.NewBuffer(nil), &stdout, io.Discard)
	if exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	out := stdout.String()
	if !strings.Contains(out, `"code":"not_found"`) {
		t.Fatalf("expected error code in json: %s", out)
	}
	if !strings.Contains(out, `"retryable":false`) {
		t.Fatalf("expected retryable marker in json: %s", out)
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
