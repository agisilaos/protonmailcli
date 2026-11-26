package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func buildWorkflowCLI(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(work, "protonmailcli"), "../../cmd/protonmailcli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	return work
}

func TestFindAndSendRecipePreservesReviewedAccount(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("the README recipe requires Bash")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the README recipe requires Python 3")
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(readme), "Recommended agent sequence for “find and send draft”")
	if !found {
		t.Fatal("missing find-and-send recipe")
	}
	section, _, _ = strings.Cut(section, "## Agent Playbooks")
	steps := regexp.MustCompile("(?s)```bash\n(.*?)\n```").FindAllStringSubmatch(section, -1)
	if len(steps) != 2 {
		t.Fatalf("expected review and send blocks, got %d", len(steps))
	}
	work := buildWorkflowCLI(t)

	for _, test := range []struct {
		name          string
		switchAccount bool
	}{
		{"same account", false},
		{"switched account with same mailbox and UID", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, base)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("BRIDGE_ACCOUNT", "synthetic@example.invalid")
			cfg, err := config.Load(base[2])
			if err != nil {
				t.Fatal(err)
			}
			if err := config.Save(config.DefaultConfigPath(), cfg); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BRIDGE_CONFIG", config.DefaultConfigPath())
			t.Setenv("BRIDGE_STATE", config.DefaultStatePath())
			t.Setenv("BRIDGE_PROFILE", cfg.Profile)
			script := steps[0][1] + "\n" + ": \"${draft_id:?review failed}\"\n"
			if test.switchAccount {
				script += "./protonmailcli bridge account use --username other@example.invalid || exit 1\n"
			}
			// An unrelated shell context after preview must not change the chosen instance.
			script += "export XDG_CONFIG_HOME=\"$PWD/unused config\" XDG_DATA_HOME=\"$PWD/unused state\"\n"
			cmd := exec.Command(bash, "-c", script+steps[1][1])
			cmd.Dir = work
			out, err := cmd.CombinedOutput()
			commands, _ := s.snapshot()
			if !strings.Contains(string(out), `"wouldSend":true`) {
				t.Fatalf("review failed before send: %s", out)
			}
			if test.switchAccount {
				if err == nil || cmd.ProcessState.ExitCode() != 2 || !strings.Contains(string(out), `"code":"validation_error"`) ||
					len(sent()) != 0 || strings.Contains(strings.Join(commands, "\n"), "other@example.invalid") {
					t.Fatalf("switched account: exit=%d SMTP messages=%d output=%s", cmd.ProcessState.ExitCode(), len(sent()), out)
				}
			} else if err != nil || len(sent()) != 1 || !strings.Contains(sent()[0], "From: synthetic@example.invalid") {
				t.Fatalf("reviewed account: err=%v SMTP=%v output=%s", err, sent(), out)
			}
		})
	}
}

func TestExpectedAccountPrecedesBatchReplayAndRejectsLocalMode(t *testing.T) {
	base, manifest, messages := sendRecoveryArgs(t, "batch")
	code, out := runWorkflow(t, base, manifest)
	if code != 0 || len(messages()) != 1 {
		t.Fatalf("seed command receipt: exit=%d output=%s", code, out)
	}
	args := append([]string{"--expect-account", "other@example.invalid"}, base...)
	code, out = runWorkflow(t, args, manifest)
	if code != 2 || !strings.Contains(out, "does not match --expect-account") || len(messages()) != 1 {
		t.Fatalf("switched account replay: exit=%d messages=%d output=%s", code, len(messages()), out)
	}

	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	args = append(base[:5:5], "--expect-account", "synthetic@example.invalid", "draft", "list")
	code, out = runWorkflow(t, args, "")
	if code != 2 || !strings.Contains(out, "--expect-account requires Bridge mode") {
		t.Fatalf("account guard in local mode: exit=%d output=%s", code, out)
	}
}
