package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type regressionCLI struct {
	t             *testing.T
	config, state string
}

func newRegressionCLI(t *testing.T) regressionCLI {
	t.Helper()
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	t.Setenv("PMAIL_SMTP_PASSWORD", "")
	dir := t.TempDir()
	c := regressionCLI{t: t, config: filepath.Join(dir, "config.toml"), state: filepath.Join(dir, "data", "state.json")}
	code, out := c.run("", "setup", "--non-interactive", "--username", "before@example.invalid", "--bridge-imap-port", "1", "--bridge-smtp-port", "1")
	if code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	return c
}

func (c regressionCLI) run(input string, args ...string) (int, string) {
	c.t.Helper()
	var out, stderr bytes.Buffer
	argv := append([]string{"--json", "--no-input", "--config", c.config, "--state", c.state}, args...)
	code := Run(argv, strings.NewReader(input), &out, &stderr)
	return code, out.String()
}

func regressionData(t *testing.T, out string) map[string]any {
	t.Helper()
	var result struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}

func TestFailedDoctorIncludesDiagnosticDetails(t *testing.T) {
	c := newRegressionCLI(t)
	code, out := c.run("", "doctor")
	if code != 3 {
		t.Fatalf("doctor prerequisite result: %d %s", code, out)
	}
	data := regressionData(t, out)
	if data == nil || data["summary"] == nil || data["checks"] == nil || data["doctor"] == nil {
		t.Fatalf("failed doctor lost diagnostics: %s", out)
	}
}

func TestLoginRejectsUnreadablePasswordContent(t *testing.T) {
	for _, kind := range []string{"directory", "empty file"} {
		t.Run(kind, func(t *testing.T) {
			c := newRegressionCLI(t)
			path := t.TempDir()
			if kind == "empty file" {
				path = filepath.Join(path, "password")
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			code, out := c.run("", "auth", "login", "--username", "user@example.invalid", "--password-file", path)
			if code != 2 {
				t.Fatalf("invalid password accepted: %d %s", code, out)
			}
			code, out = c.run("", "auth", "status")
			if code != 0 || regressionData(t, out)["loggedIn"] != false {
				t.Fatalf("failed login changed session: %d %s", code, out)
			}
		})
	}
}

func TestInvalidExistingConfigurationIsNotReportedMissing(t *testing.T) {
	for _, source := range []string{"file", "environment"} {
		t.Run(source, func(t *testing.T) {
			c := newRegressionCLI(t)
			if source == "file" {
				if err := os.WriteFile(c.config, []byte("[safety]\nallow_force_send = invalid\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("PMAIL_TIMEOUT", "invalid")
			}
			code, out := c.run("", "auth", "status")
			if code != 3 || !strings.Contains(out, `"code":"config_error"`) || strings.Contains(out, "setup first") {
				t.Fatalf("invalid configuration was misdiagnosed: %d %s", code, out)
			}
		})
	}
}

func TestCredentialChecksRejectNonRegularPasswordFiles(t *testing.T) {
	for _, command := range []string{"login", "doctor"} {
		t.Run(command, func(t *testing.T) {
			c := newRegressionCLI(t)
			path := filepath.Join(t.TempDir(), "password-fifo")
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"auth", "login", "--password-file", path}
			if command == "doctor" {
				if code, out := c.run("", "setup", "--username", "user@example.invalid", "--smtp-password-file", path, "--bridge-imap-port", "1", "--bridge-smtp-port", "1"); code != 0 {
					t.Fatalf("setup: %d %s", code, out)
				}
				args = []string{"doctor"}
			}
			done := make(chan int, 1)
			go func() { code, _ := c.run("", args...); done <- code }()
			select {
			case code := <-done:
				want := 2
				if command == "doctor" {
					want = 3
				}
				if code != want {
					t.Fatalf("FIFO password file: exit %d, want %d", code, want)
				}
			case <-time.After(500 * time.Millisecond):
				// Release the old blocking reader so a regression cannot hang tests.
				f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("synthetic-password")
				_ = f.Close()
				<-done
				t.Fatal("credential check blocked on a named pipe")
			}
		})
	}
}

func TestDoctorUsesSelectedBridgeAccount(t *testing.T) {
	c := newRegressionCLI(t)
	if err := os.WriteFile(c.config, []byte("[bridge]\nhost = '127.0.0.1'\nimap_port = 1\nsmtp_port = 1\nusername = ''\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PMAIL_SMTP_PASSWORD", "synthetic-password")
	if code, out := c.run("", "bridge", "account", "use", "--username", "selected@example.invalid"); code != 0 {
		t.Fatalf("select account: %d %s", code, out)
	}
	code, out := c.run("", "doctor")
	if code != 4 || regressionData(t, out)["summary"].(map[string]any)["authPrereqs"] != true {
		t.Fatalf("doctor ignored selected credentials: %d %s", code, out)
	}
}
