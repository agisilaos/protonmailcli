package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

type interruptedPrompts struct {
	completed int
	short     bool
}

func (w *interruptedPrompts) Write(p []byte) (int, error) {
	if w.completed > 0 {
		w.completed--
		return len(p), nil
	}
	if w.short {
		return len(p) - 1, nil
	}
	return 0, io.ErrClosedPipe
}

type setupBlankAnswers struct{ reads int }

func (r *setupBlankAnswers) Read(p []byte) (int, error) {
	r.reads++
	p[0] = '\n'
	return 1, nil
}

func TestSetupPreservesConfigWhenPromptFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		completed int
		short     bool
	}{
		{"closed first prompt", 0, false},
		{"short final prompt", 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.toml")
			const before = "[defaults]\nprofile = 'existing'\n"
			if err := os.WriteFile(cfg, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			var input setupBlankAnswers
			code := Run([]string{"--config", cfg, "setup", "--interactive"}, &input, &stdout, &interruptedPrompts{completed: tc.completed, short: tc.short})
			if input.reads != tc.completed {
				t.Errorf("input reads=%d after %d delivered prompts", input.reads, tc.completed)
			}
			after, err := os.ReadFile(cfg)
			if err != nil || string(after) != before || code != 1 || !strings.Contains(stdout.String(), "setup prompt") {
				t.Fatalf("exit=%d stdout=%s config preserved=%v read error=%v", code, &stdout, string(after) == before, err)
			}
		})
	}
}

type setupInputError struct{}

func (setupInputError) Read(p []byte) (int, error) {
	return copy(p, "partial answer"), io.ErrUnexpectedEOF
}

func TestSetupPreservesConfigWhenInputFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input io.Reader
	}{
		{"first answer EOF", strings.NewReader("")},
		{"final answer EOF", strings.NewReader("replacement\nlocalhost\n1025\n1143\nfixture@example.invalid\n")},
		{"whitespace at EOF", strings.NewReader(" \t")},
		{"partial answer with input error", setupInputError{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			const before = "[defaults]\nprofile = 'existing'\n"
			if err := os.WriteFile(cfgPath, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			var out, prompts bytes.Buffer
			code := Run([]string{"--config", cfgPath, "setup", "--interactive"}, tc.input, &out, &prompts)
			after, err := os.ReadFile(cfgPath)
			if code != 1 || err != nil || string(after) != before || !strings.Contains(out.String(), "read setup input") {
				t.Fatalf("input failure must preserve config: exit=%d output=%s config=%q error=%v", code, &out, after, err)
			}
		})
	}
}

func TestSetupAcceptsBlankAnswersAndFinalAnswerWithoutNewline(t *testing.T) {
	for _, tc := range []struct {
		name, input, profile, passwordFile string
	}{
		{"blank defaults", "\n\n\n\n\n\n", "default", ""},
		{"last line without newline", "chosen\n\n\n\nfixture@example.invalid\n/path/to/fixture.pass", "chosen", "/path/to/fixture.pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			var out bytes.Buffer
			code := Run([]string{"--config", cfgPath, "setup", "--interactive"}, strings.NewReader(tc.input), &out, io.Discard)
			cfg, err := config.Load(cfgPath)
			if code != 0 || err != nil || cfg.Profile != tc.profile || cfg.Bridge.PasswordFile != tc.passwordFile {
				t.Fatalf("valid interactive input: exit=%d output=%s config=%+v error=%v", code, &out, cfg, err)
			}
		})
	}
}
