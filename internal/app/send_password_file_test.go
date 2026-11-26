package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendPasswordFileSuppliesIMAPCredentials(t *testing.T) {
	for _, action := range []string{"send", "send-many"} {
		t.Run(action, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			t.Setenv("PMAIL_SMTP_PASSWORD", "")
			passwordFile := filepath.Join(t.TempDir(), "bridge.pass")
			if err := os.WriteFile(passwordFile, []byte("synthetic-file-password\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--dry-run", "message", action, "--smtp-password-file", passwordFile)
			input := ""
			if action == "send" {
				args = append(args, "--draft-id", "41", "--confirm-send", "41")
			} else {
				args = append(args, "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41"}]`
			}
			code, out := runWorkflow(t, args, input)
			if code != 0 {
				t.Fatalf("password-file-only send preview failed: exit=%d out=%s", code, out)
			}
			commands, _ := s.snapshot()
			if !strings.Contains(strings.Join(commands, "\n"), `LOGIN "synthetic@example.invalid" "synthetic-file-password"`) {
				t.Fatalf("IMAP did not use the supplied password file: %v", commands)
			}
		})
	}
}
