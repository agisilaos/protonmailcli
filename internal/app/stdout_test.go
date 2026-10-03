package app

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

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
