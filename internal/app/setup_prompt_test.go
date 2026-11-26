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

type setupInputError string

func (s setupInputError) Read(p []byte) (int, error) {
	return copy(p, s), io.ErrUnexpectedEOF
}

func TestSetupPreservesConfigWhenInputFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input io.Reader
	}{
		{"first answer EOF", strings.NewReader("")},
		{"final answer EOF", strings.NewReader("replacement\nlocalhost\n1025\n1143\nfixture@example.invalid\n")},
		{"whitespace at EOF", strings.NewReader(" \t")},
		{"partial answer with input error", setupInputError("partial answer")},
		{"complete final answer with input error", io.MultiReader(strings.NewReader("\n\n\n\nfixture@example.invalid\n"), setupInputError("/path/to/fixture.pass\n"))},
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
	// config.Load applies environment overrides after reading the saved TOML.
	t.Setenv("PMAIL_PROFILE", "")
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

func TestSetupPortInputs(t *testing.T) {
	for _, tc := range []struct {
		name, smtp, imap, badPort string
		flags                     bool
		wantSMTP, wantIMAP        int
	}{
		{"malformed SMTP", "abc", "31143", "SMTP", false, 0, 0},
		{"malformed IMAP", "31025", "abc", "IMAP", false, 0, 0},
		{"numeric prefix", "1025x", "31143", "SMTP", false, 0, 0},
		{"integer overflow", "31025", "99999999999999999999999999", "IMAP", false, 0, 0},
		{"zero IMAP", "1025", "0", "IMAP", false, 0, 0},
		{"above maximum SMTP", "65536", "1143", "SMTP", false, 0, 0},
		{"blank defaults", "", "", "", false, 1025, 1143},
		{"explicit decimal and upper boundary", "031025", "65535", "", false, 31025, 65535},
		{"flag negative SMTP", "-1", "1143", "SMTP", true, 0, 0},
		{"flag above maximum IMAP", "1025", "65536", "IMAP", true, 0, 0},
		{"flag integer syntax and boundaries", "0xffff", "0o1", "", true, 65535, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			const before = "[bridge]\nhost = 'existing.invalid'\nsmtp_port = 2500\nimap_port = 1400\n"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			input := strings.Join([]string{"", "", tc.smtp, tc.imap, "fixture@example.invalid", "", ""}, "\n")
			var out, prompts bytes.Buffer
			args := []string{"--json", "--config", path, "setup"}
			if tc.flags {
				args = append(args, "--non-interactive", "--username", "fixture@example.invalid", "--bridge-smtp-port", tc.smtp, "--bridge-imap-port", tc.imap)
			} else {
				args = append(args, "--interactive")
			}
			code := Run(args, strings.NewReader(input), &out, &prompts)
			if tc.badPort != "" {
				after, err := os.ReadFile(path)
				if code != 2 || !strings.Contains(out.String(), tc.badPort+" port") || err != nil || string(after) != before {
					t.Fatalf("invalid %s port: exit=%d output=%s config preserved=%v read error=%v", tc.badPort, code, &out, string(after) == before, err)
				}
				if strings.Contains(prompts.String(), "Bridge username") {
					t.Fatal("setup kept prompting after an invalid port")
				}
				return
			}
			cfg, err := config.Load(path)
			if code != 0 || err != nil || cfg.Bridge.SMTPPort != tc.wantSMTP || cfg.Bridge.IMAPPort != tc.wantIMAP {
				t.Fatalf("valid ports: exit=%d config=%+v read error=%v output=%s", code, cfg.Bridge, err, &out)
			}
		})
	}
}

func TestInteractiveSetupRejectsInvalidPortFlagsBeforePrompting(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"--bridge-smtp-port", "65536"},
		{"--bridge-imap-port", "0"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			const before = "[defaults]\nprofile = 'existing'\n"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			var out, prompts bytes.Buffer
			var answers setupBlankAnswers
			code := Run([]string{"--json", "--config", path, "setup", "--interactive", tc.flag, tc.value}, &answers, &out, &prompts)
			after, err := os.ReadFile(path)
			if code != 2 || !strings.Contains(out.String(), `"code":"validation_error"`) || prompts.Len() != 0 || answers.reads != 0 || err != nil || string(after) != before {
				t.Fatalf("invalid flag: exit=%d output=%s prompts=%q reads=%d config preserved=%v read error=%v", code, &out, &prompts, answers.reads, string(after) == before, err)
			}
		})
	}
}

func TestInteractiveSetupShowsAndUsesFlagDefaults(t *testing.T) {
	t.Setenv("PMAIL_PROFILE", "")
	for _, tc := range []struct {
		name, input, wantProfile string
		wantSMTP, wantIMAP       int
	}{
		{"accept defaults", "\n\n\n\n\n\n", "work", 2500, 1400},
		{"override defaults", "team\n\n3500\n3400\n\n\n", "team", 3500, 3400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			var out, prompts bytes.Buffer
			code := Run([]string{"--config", path, "setup", "--interactive", "--profile", "work",
				"--bridge-smtp-port", "2500", "--bridge-imap-port", "1400"}, strings.NewReader(tc.input), &out, &prompts)
			cfg, err := config.Load(path)
			if code != 0 || err != nil || cfg.Profile != tc.wantProfile || cfg.Bridge.SMTPPort != tc.wantSMTP || cfg.Bridge.IMAPPort != tc.wantIMAP {
				t.Errorf("exit=%d output=%s profile=%q SMTP=%d IMAP=%d read error=%v", code, &out, cfg.Profile, cfg.Bridge.SMTPPort, cfg.Bridge.IMAPPort, err)
			}
			for _, prompt := range []string{"Profile [work]", "Bridge SMTP port [2500]", "Bridge IMAP port [1400]"} {
				if !strings.Contains(prompts.String(), prompt) {
					t.Errorf("missing %q in %s", prompt, &prompts)
				}
			}
		})
	}
}

func TestSetupStopsReadingAfterSixCompleteAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	var answers setupBlankAnswers
	var out bytes.Buffer
	if code := Run([]string{"--config", path, "setup", "--interactive"}, &answers, &out, io.Discard); code != 0 || answers.reads != 6 {
		t.Fatalf("setup exit=%d after %d reads: %s", code, answers.reads, &out)
	}
}

func TestSetupRejectsConflictingInputModesBeforePrompting(t *testing.T) {
	for _, tc := range []struct {
		name           string
		globals, setup []string
	}{
		{"global no-input", []string{"--no-input"}, []string{"--interactive"}},
		{"both setup modes", nil, []string{"--non-interactive", "--interactive"}},
		{"both setup modes reversed", nil, []string{"--interactive", "--non-interactive"}},
		{"dry run", []string{"--dry-run", "--no-input"}, []string{"--interactive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.toml")
			const before = "[defaults]\nprofile = 'original'\n"
			if err := os.WriteFile(cfg, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--json", "--config", cfg}, tc.globals...)
			args = append(args, "setup", "--username", "fixture@example.invalid")
			var input setupBlankAnswers
			var out, prompts bytes.Buffer
			code := Run(append(args, tc.setup...), &input, &out, &prompts)
			after, err := os.ReadFile(cfg)
			if err != nil || code != 2 || !strings.Contains(out.String(), "usage_error") || prompts.Len() != 0 || input.reads != 0 || string(after) != before {
				t.Fatalf("exit=%d output=%s prompts=%q reads=%d config=%q err=%v", code, &out, &prompts, input.reads, after, err)
			}
		})
	}
}
