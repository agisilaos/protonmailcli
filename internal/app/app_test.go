package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
