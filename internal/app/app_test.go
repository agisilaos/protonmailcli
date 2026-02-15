package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
