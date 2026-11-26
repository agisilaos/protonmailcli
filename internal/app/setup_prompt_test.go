package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

type setupEOF struct{ reads int }

func (r *setupEOF) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
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
			var input setupEOF
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
