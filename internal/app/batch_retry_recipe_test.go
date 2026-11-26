package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPublishedSendBatchRetryPinsTheAccount(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("the manifest recipe requires Bash")
	}
	guide, err := os.ReadFile("../../docs/agent-manifest-schemas.md")
	if err != nil {
		t.Fatal(err)
	}
	recipe := regexp.MustCompile("`(\\./protonmailcli [^`\n]*message send-many --file sends-to-retry.json)`").FindAllStringSubmatch(string(guide), -1)
	if len(recipe) != 2 || !strings.Contains(recipe[0][1], "--dry-run") || strings.Contains(recipe[1][1], "--dry-run") {
		t.Fatal("expected a preview followed by a send retry command")
	}

	work := buildWorkflowCLI(t)
	base := imapCommandArgs(t, newWorkflowServer(t, &workflowServer{}))
	sent := captureWorkflowSMTP(t, base)
	const account = "reviewed@example.invalid"
	t.Setenv("BATCH_CONFIG", base[2])
	t.Setenv("BATCH_STATE", base[4])
	t.Setenv("BATCH_PROFILE", "batch profile")
	t.Setenv("BATCH_ACCOUNT", account)
	const row = `[{"draft_id":"imap-v2:Drafts:41","confirm_send":"imap-v2:Drafts:41","idempotency_key":"reviewed-send"}]`
	if err := os.WriteFile(filepath.Join(work, "sends-to-retry.json"), []byte(row), 0600); err != nil {
		t.Fatal(err)
	}
	for _, active := range []string{"switched@example.invalid", account} {
		if code, out := runWorkflow(t, append(base, "bridge", "account", "use", "--username", active), ""); code != 0 {
			t.Fatal(out)
		}
		for index, step := range recipe {
			cmd := exec.Command(bash, "-c", step[1])
			cmd.Dir = work
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil {
				t.Fatalf("run documented command: %v", err)
			}
			if active != account {
				if cmd.ProcessState.ExitCode() != 2 || !strings.Contains(string(out), "does not match --expect-account") || len(sent()) != 0 {
					t.Fatalf("switched account: status=%d SMTP submissions=%d output=%s", cmd.ProcessState.ExitCode(), len(sent()), out)
				}
			} else if err != nil || len(sent()) != index || (index == 1 && !strings.Contains(sent()[0], "From: "+account)) {
				t.Fatalf("reviewed account: err=%v SMTP=%v output=%s", err, sent(), out)
			}
		}
	}
}

func TestSendBatchRetryAfterConfirmedRejection(t *testing.T) {
	base := imapCommandArgs(t, newWorkflowServer(t, &workflowServer{}))
	// The fixture explicitly rejects the first DATA and accepts the next two.
	attempts := captureWorkflowSMTPWithReplies(t, base, "550 not delivered", "250 accepted", "250 accepted")
	args := append(base, "--expect-account", "synthetic@example.invalid", "message", "send-many", "--stdin")
	const original = `[
		{"draft_id":"imap-v2:Drafts:41","confirm_send":"imap-v2:Drafts:41","idempotency_key":"first-send"},
		{"draft_id":"imap-v2:Drafts:42","confirm_send":"imap-v2:Drafts:42","idempotency_key":"later-send"}
	]`
	code, out := runWorkflow(t, args, original)
	if code != 4 || !strings.Contains(out, "550") || !strings.Contains(out, "imap_send_uncertain") || len(attempts()) != 1 {
		t.Fatalf("expected first row rejection before later dispatch: exit=%d attempts=%d output=%s", code, len(attempts()), out)
	}
	code, out = runWorkflow(t, args, original)
	if code != 4 || !strings.Contains(out, "imap_send_uncertain") || len(attempts()) != 1 {
		t.Fatalf("pending item key must stop the entire batch: exit=%d attempts=%d output=%s", code, len(attempts()), out)
	}
	// Keep the key of the undispatched row. Only the independently rejected row gets a new intent.
	retry := strings.Replace(original, "first-send", "replacement-send", 1)
	code, out = runWorkflow(t, args, retry)
	if code != 0 || !strings.Contains(out, `"success":2`) || len(attempts()) != 3 {
		t.Fatalf("retry of confirmed unsent rows: exit=%d attempts=%d output=%s", code, len(attempts()), out)
	}
}
