package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/store"
)

type failingOutput struct {
	partial bool
	calls   int
}

func (w *failingOutput) Write(p []byte) (int, error) {
	w.calls++
	if w.partial {
		return len(p) / 2, nil
	}
	return 0, errors.New("sink closed")
}

func TestOutputFailureStatus(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, args := range [][]string{{"--version"}, {"--help"}, {"--json", "--unknown"}, {"--plain", "--unknown"}} {
			out := &failingOutput{partial: partial}
			var stderr bytes.Buffer
			code := Run(args, strings.NewReader(""), out, &stderr)
			want := 1
			if len(args) > 1 && args[1] == "--unknown" {
				want = 2
			}
			if code != want || !strings.Contains(stderr.String(), "stdout write failed") {
				t.Fatalf("%v partial=%t code=%d stderr=%s", args, partial, code, stderr.String())
			}
			if out.calls != 1 {
				t.Fatalf("retried output %d times", out.calls)
			}
		}
	}
}

func TestFailedReceiptKeepsOneLocalMutation(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	state := filepath.Join(dir, "state.json")
	prefix := []string{"--json", "--config", cfg, "--state", state}
	if code := Run(append(append([]string{}, prefix...), "setup", "--non-interactive", "--username", "user@example.invalid"), strings.NewReader(""), io.Discard, io.Discard); code != 0 {
		t.Fatalf("setup=%d", code)
	}
	out := &failingOutput{partial: true}
	var stderr bytes.Buffer
	args := append(append([]string{}, prefix...), "draft", "create", "--to", "user@example.invalid", "--body", "Example")
	if code := Run(args, strings.NewReader(""), out, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	st, err := store.New(state).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Drafts) != 1 {
		t.Fatalf("drafts=%d", len(st.Drafts))
	}
	if !strings.Contains(stderr.String(), "inspect state before retrying") {
		t.Fatal(stderr.String())
	}
}

func TestCommandOutputLatchesShortWrite(t *testing.T) {
	sink := &failingOutput{partial: true}
	out := &commandOutput{writer: sink}
	if _, err := out.Write([]byte("hello")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("again")); !errors.Is(err, io.ErrShortWrite) || sink.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, sink.calls)
	}
}

func TestDefaultOutputShowsDraftReceiptsAndReads(t *testing.T) {
	t.Setenv("PMAIL_OUTPUT", "")
	c := newRegressionCLI(t)
	run := func(args ...string) string {
		t.Helper()
		fullArgs := append([]string{"--config", c.config, "--state", c.state}, args...)
		var out, stderr bytes.Buffer
		if exit := Run(fullArgs, strings.NewReader(""), &out, &stderr); exit != 0 || stderr.Len() != 0 {
			t.Fatalf("%v exit=%d stdout=%q stderr=%q", args, exit, &out, &stderr)
		}
		return out.String()
	}

	const subject = "Budget review"
	const body = "Please review the budget."
	const recipient = "reviewer@example.invalid"
	created := run("draft", "create", "--to", recipient, "--subject", subject, "--body", body)
	id := latestDraftID(t, c.state)
	for _, tc := range []struct {
		command string
		output  string
		want    []string
	}{
		{"create", created, []string{id}},
		{"get", run("draft", "get", "--draft-id", id), []string{id, recipient, subject, body}},
		{"list", run("draft", "list"), []string{id, subject}},
		{"mailbox list", run("mailbox", "list"), []string{"INBOX", "Drafts", "Sent"}},
	} {
		for _, want := range tc.want {
			if !strings.Contains(tc.output, want) {
				t.Errorf("%s omitted %q from %q", tc.command, want, tc.output)
			}
		}
	}
}

func TestHumanDoctorFailureShowsBothEndpoints(t *testing.T) {
	t.Setenv("PMAIL_OUTPUT", "")
	t.Setenv("PMAIL_SMTP_PASSWORD", "fixture")
	smtp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	// The connected client's port stays reserved and does not accept connections.
	client, err := net.Dial("tcp4", smtp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := config.Default()
	cfg.Bridge.Username = "fixture@example.invalid"
	cfg.Bridge.SMTPPort = smtp.Addr().(*net.TCPAddr).Port
	cfg.Bridge.IMAPPort = client.LocalAddr().(*net.TCPAddr).Port
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", cfgPath, "--state", filepath.Join(dir, "state.json"), "doctor"}

	var stdout, stderr bytes.Buffer
	if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 4 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	summary, details, _ := strings.Cut(stdout.String(), "\n")
	if summary != "error: one or more bridge endpoints are unreachable (bridge_unreachable)" {
		t.Errorf("error summary=%q", summary)
	}
	var data struct{ Checks []bridge.HealthStatus }
	if err := json.Unmarshal([]byte(details), &data); err != nil {
		t.Errorf("missing diagnostic details on stdout: %q (%v)", details, err)
	} else if len(data.Checks) != 2 || data.Checks[0].Name != "smtp" || !data.Checks[0].OK ||
		data.Checks[1].Name != "imap" || data.Checks[1].OK || data.Checks[1].Detail == "" {
		t.Errorf("incorrect endpoint diagnostics: %+v", data.Checks)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(append([]string{"--plain"}, args...), strings.NewReader(""), &stdout, &stderr); code != 4 {
		t.Fatalf("plain doctor exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	record := strings.TrimSuffix(stdout.String(), "\n")
	fields := strings.Split(record, "\t")
	if strings.ContainsAny(record, "\r\n") || len(fields) != 10 || fields[8] != "data" {
		t.Fatalf("plain doctor lost its final data field: %q", record)
	}
	data.Checks = nil
	if err := json.Unmarshal([]byte(fields[9]), &data); err != nil || len(data.Checks) != 2 ||
		data.Checks[0].Name != "smtp" || !data.Checks[0].OK || data.Checks[1].Name != "imap" || data.Checks[1].OK {
		t.Fatalf("plain endpoint diagnostics=%q err=%v", fields[9], err)
	}
	// Buffer the summary so the short write occurs while printing the checks.
	short := &failingOutput{partial: true}
	sink := bufio.NewWriterSize(short, len(summary)+1)
	stderr.Reset()
	if code := Run(args, strings.NewReader(""), sink, &stderr); code != 4 || !strings.Contains(stderr.String(), "stdout write failed") {
		t.Fatalf("short diagnostic write: exit=%d stderr=%s", code, &stderr)
	}
}
