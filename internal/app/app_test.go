package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func TestSetupNonInteractiveAndDraftFlow(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("setup exit=%d stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "draft", "create", "--to", "a@example.com", "--subject", "hello", "--body", "body"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("draft create exit=%d stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true got %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if path, _ := data["createPath"].(string); path != "local_state" {
		t.Fatalf("expected createPath=local_state got %q payload=%v", path, data)
	}
	if source, _ := data["source"].(string); source != "local" {
		t.Fatalf("expected source=local got %q payload=%v", source, data)
	}
}

func TestLocalSendIncludesSendPath(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "draft", "create", "--to", "a@example.com", "--subject", "hello", "--body", "body"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("create failed: %d", exit)
	}
	draftID := latestDraftID(t, state)
	stdout := &bytes.Buffer{}
	exit := Run([]string{"--json", "--no-input", "--dry-run", "--config", cfg, "--state", state, "message", "send", "--draft-id", draftID, "--confirm-send", draftID}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{})
	if exit != 0 {
		t.Fatalf("send failed: %d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"sendPath":"local_state"`) {
		t.Fatalf("missing sendPath in response: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"source":"local"`) {
		t.Fatalf("missing source in response: %s", stdout.String())
	}
}

func TestSendRequiresConfirmWhenNoInput(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	setupExit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if setupExit != 0 {
		t.Fatalf("setup failed: %d", setupExit)
	}
	createExit := Run([]string{"--json", "--config", cfg, "--state", state, "draft", "create", "--to", "a@example.com", "--subject", "hello", "--body", "body"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if createExit != 0 {
		t.Fatalf("create failed: %d", createExit)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--no-input", "--config", cfg, "--state", state, "message", "send", "--draft-id", latestDraftID(t, state)}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 7 {
		t.Fatalf("expected exit 7, got %d; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("confirmation_required")) {
		t.Fatalf("expected confirmation_required in stdout: %s", stdout.String())
	}
}

func TestAuthLoginStatusLogout(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	pass := filepath.Join(tmp, "bridge.pass")
	if err := os.WriteFile(pass, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com", "--smtp-password-file", pass}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "auth", "login", "--username", "me@example.com", "--password-file", pass}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("login failed: %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "auth", "status"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("status failed: %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("\"loggedIn\":true")) {
		t.Fatalf("expected loggedIn true: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "auth", "logout"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("logout failed: %d", exit)
	}
}

func TestDoctorFailureExitCode(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	t.Setenv("PMAIL_SMTP_PASSWORD", "secret")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com", "--bridge-host", "127.0.0.1", "--bridge-smtp-port", "1", "--bridge-imap-port", "1"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "doctor"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 4 {
		t.Fatalf("expected exit 4 got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("bridge_unreachable")) {
		t.Fatalf("expected bridge_unreachable in stdout: %s", stdout.String())
	}
}

func TestDoctorPrereqFailureExitCode(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	// Clear username in config to force prereq failure without credentials.
	if err := os.WriteFile(cfg, []byte(`[defaults]
profile = "default"
output = "human"
timeout = "30s"

[bridge]
host = "127.0.0.1"
imap_port = 1143
smtp_port = 1025
tls = true
username = ""
password_file = ""

[safety]
require_confirm_send_non_tty = true
allow_force_send = true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "doctor"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 3 {
		t.Fatalf("expected exit 3 got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("doctor_prereq_failed")) {
		t.Fatalf("expected doctor_prereq_failed in stdout: %s", stdout.String())
	}
}

func TestDoctorPayloadGroups(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Host = "127.0.0.1"
	cfg.Bridge.IMAPPort = 1
	cfg.Bridge.SMTPPort = 1
	st := &model.State{Auth: model.AuthState{Username: "me@example.com"}}
	t.Setenv("PMAIL_SMTP_PASSWORD", "secret")

	data, _, err := cmdDoctor(cfg, st)
	if err == nil {
		t.Fatalf("expected error due to unreachable ports")
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload")
	}
	if _, ok := m["summary"]; !ok {
		t.Fatalf("missing summary group: %#v", m)
	}
	if _, ok := m["doctor"]; !ok {
		t.Fatalf("missing doctor group: %#v", m)
	}
}

func TestBridgeAccountListAndUse(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "cfg@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "bridge", "account", "list"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("list failed: %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"cfg@example.com"`) {
		t.Fatalf("expected configured account in list: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "bridge", "account", "use", "--username", "active@example.com"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("use failed: %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"active@example.com"`) {
		t.Fatalf("expected active username in output: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "bridge", "account", "list"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("list after use failed: %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"active":"active@example.com"`) && !strings.Contains(stdout.String(), `"active@example.com"`) {
		t.Fatalf("expected active account marker: %s", stdout.String())
	}
}

func TestLocalMailboxListIncludesStableIDAndKind(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "mailbox", "list"}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("mailbox list failed: %d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"id":"inbox"`) || !strings.Contains(stdout.String(), `"kind":"system"`) {
		t.Fatalf("mailbox mapping fields missing: %s", stdout.String())
	}
}

func TestLocalMailboxResolveByID(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "mailbox", "resolve", "--name", "drafts"}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("mailbox resolve failed: %d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"id":"drafts"`) || !strings.Contains(stdout.String(), `"matchedBy":"id_exact"`) {
		t.Fatalf("unexpected mailbox resolve payload: %s", stdout.String())
	}
}

func TestCompletionZsh(t *testing.T) {
	stdout := &bytes.Buffer{}
	exit := Run([]string{"completion", "zsh"}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{})
	if exit != 0 {
		t.Fatalf("completion failed: %d", exit)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("compdef protonmailcli")) {
		t.Fatalf("unexpected completion output: %s", stdout.String())
	}
}

func latestDraftID(t *testing.T, statePath string) string {
	t.Helper()
	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Drafts map[string]any `json:"drafts"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	for id := range s.Drafts {
		return id
	}
	t.Fatal("no draft found")
	return ""
}

func TestNormalizeExitCodeFromTypedResponse(t *testing.T) {
	r := batchResultResponse{Count: 2, Success: 1, Failed: 1, Source: "imap"}
	r.exitCode = 10
	if got := normalizeExitCode(r); got != 10 {
		t.Fatalf("expected 10 got %d", got)
	}
}

func TestDraftCreateOmitsZeroSentAt(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}

	stdout := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "draft", "create", "--to", "a@example.com", "--subject", "s", "--body", "b"}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{})
	if exit != 0 {
		t.Fatalf("draft create failed: %d stdout=%s", exit, stdout.String())
	}
	if strings.Contains(stdout.String(), `"sentAt"`) {
		t.Fatalf("expected sentAt to be omitted from create response: %s", stdout.String())
	}

	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(b), `"sentAt":"0001-01-01T00:00:00Z"`) {
		t.Fatalf("expected zero sentAt to be omitted in state: %s", string(b))
	}
}

func TestBatchHelpDoesNotRequireBridgeAuth(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	// Setup only username; no password file and no PMAIL_SMTP_PASSWORD.
	if exit := Run([]string{"--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "draft", "create-many", "--help"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("draft create-many --help failed: exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"usage\":\"Usage of draft create-many:") {
		t.Fatalf("expected usage field in json stdout: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "auth_missing") || strings.Contains(stderr.String(), "auth_missing") {
		t.Fatalf("help path should not require auth: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--config", cfg, "--state", state, "message", "send-many", "--help"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("message send-many --help failed: exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"usage\":\"Usage of message send-many:") {
		t.Fatalf("expected usage field in json stdout: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "auth_missing") || strings.Contains(stderr.String(), "auth_missing") {
		t.Fatalf("help path should not require auth: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestMessageSendHelpJSON(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "message", "send", "--help"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("message send --help failed: exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"help\":\"message send\"") || !strings.Contains(stdout.String(), "\"usage\":\"Usage of message send:") {
		t.Fatalf("unexpected help payload: %s", stdout.String())
	}
}

func TestMessageSendHelpIMAPWithoutAuth(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--config", cfg, "--state", state, "message", "send", "--help"}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("message send --help failed: exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "auth_missing") || strings.Contains(stderr.String(), "auth_missing") {
		t.Fatalf("help should not require auth: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestLocalBatchDraftCreateAndSendManyParity(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")

	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}

	manifest := filepath.Join(tmp, "drafts.json")
	if err := os.WriteFile(manifest, []byte(`[
{"to":["a@example.com"],"subject":"ok","body":"hello"},
{"to":["b@example.com"],"subject":"bad","body_file":"./missing.txt"}
]`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--no-input", "--config", cfg, "--state", state, "draft", "create-many", "--file", manifest}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 10 {
		t.Fatalf("expected partial success exit 10, got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"success":1`) || !strings.Contains(stdout.String(), `"failed":1`) {
		t.Fatalf("unexpected batch draft response: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"errorCode":"validation_error"`) {
		t.Fatalf("expected validation errorCode: %s", stdout.String())
	}

	draftID := latestDraftID(t, state)
	sendManifest := filepath.Join(tmp, "sends.json")
	if err := os.WriteFile(sendManifest, []byte(`[{"draft_id":"`+draftID+`","confirm_send":"`+draftID+`"}]`), 0o600); err != nil {
		t.Fatalf("write send manifest: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	exit = Run([]string{"--json", "--no-input", "--dry-run", "--config", cfg, "--state", state, "message", "send-many", "--file", sendManifest}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dryRun":true`) || !strings.Contains(stdout.String(), `"success":1`) {
		t.Fatalf("unexpected send-many dry-run response: %s", stdout.String())
	}
}

func TestLocalSendManyAllFailedReturnsNonZero(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	manifest := filepath.Join(tmp, "send-many.json")
	if err := os.WriteFile(manifest, []byte(`[{"draft_id":"missing","confirm_send":"missing"}]`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"--json", "--no-input", "--config", cfg, "--state", state, "message", "send-many", "--file", manifest}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 1 {
		t.Fatalf("expected exit 1 for all-failed batch, got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"success":0`) || !strings.Contains(stdout.String(), `"failed":1`) {
		t.Fatalf("unexpected response: %s", stdout.String())
	}
}

func TestLateGlobalFlagFailsFast(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := Run([]string{"draft", "list", "--json", "--config", cfg, "--state", state}, bytes.NewBuffer(nil), stdout, stderr)
	if exit != 2 {
		t.Fatalf("expected usage exit 2, got %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "global flag --json must appear before the resource") {
		t.Fatalf("unexpected error message: %s", stdout.String())
	}
}

func TestLocalDraftCreateManyPerItemValidation(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	state := filepath.Join(tmp, "state.json")
	if exit := Run([]string{"--json", "--config", cfg, "--state", state, "setup", "--non-interactive", "--username", "me@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); exit != 0 {
		t.Fatalf("setup failed: %d", exit)
	}
	manifest := filepath.Join(tmp, "drafts.json")
	if err := os.WriteFile(manifest, []byte(`[
{"to":["a@example.com"],"subject":"ok","body":"hello"},
{"subject":"bad","body":"missing to"}
]`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	stdout := &bytes.Buffer{}
	exit := Run([]string{"--json", "--no-input", "--config", cfg, "--state", state, "draft", "create-many", "--file", manifest}, bytes.NewBuffer(nil), stdout, &bytes.Buffer{})
	if exit != 10 {
		t.Fatalf("expected partial exit 10, got %d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"errorCode":"validation_error"`) || !strings.Contains(stdout.String(), `"success":1`) {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}
