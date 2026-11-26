package app

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestPublishedIntentCheckpointErrorPreventsRemoteDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, input  string
		args         []string
		recoveryCode string
	}{
		{"single send", "", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "receipt"}, "imap_send_uncertain"},
		{"item send", `[{"draft_id":"41","confirm_send":"41","idempotency_key":"receipt"}]`, []string{"message", "send-many", "--stdin"}, "imap_send_uncertain"},
		{"batch send", `[{"draft_id":"41","confirm_send":"41"}]`, []string{"message", "send-many", "--stdin", "--idempotency-key", "receipt"}, "imap_send_uncertain"},
		{"single draft", "", []string{"draft", "create", "--to", "fixture@example.invalid", "--body", "synthetic", "--idempotency-key", "receipt"}, "imap_draft_create_uncertain"},
		{"item draft", `[{"to":["fixture@example.invalid"],"body":"synthetic","idempotency_key":"receipt"}]`, []string{"draft", "create-many", "--stdin"}, "imap_draft_create_uncertain"},
		{"batch draft", `[{"to":["fixture@example.invalid"],"body":"synthetic"}]`, []string{"draft", "create-many", "--stdin", "--idempotency-key", "receipt"}, "imap_draft_create_uncertain"},
		{"follow-up", "", []string{"message", "follow-up", "--message-id", "imap:INBOX:41", "--body", "synthetic", "--idempotency-key", "receipt"}, "imap_draft_create_uncertain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			sent := captureWorkflowSMTP(t, base)
			disk := store.New(base[4])
			var output bytes.Buffer
			// Model the store returning a sync error after publication. The store tests
			// separately exercise the real rename/sync ordering and fault boundary.
			app := App{Stdout: &output, Stderr: io.Discard, Stdin: strings.NewReader(tc.input), checkpoint: func(state model.State) error {
				if err := disk.Save(state); err != nil {
					return err
				}
				return errors.New("state checkpoint published but parent directory durability could not be confirmed: synthetic sync failure")
			}}
			args := append(base, tc.args...)
			code := app.run(args)
			_, appended := server.snapshot()
			if code != 1 || !strings.Contains(output.String(), `"code":"state_save_failed"`) || !strings.Contains(output.String(), "published") || len(sent()) != 0 || len(appended) != 0 {
				t.Fatalf("unconfirmed checkpoint dispatched: exit=%d sent=%d appended=%d out=%s", code, len(sent()), len(appended), &output)
			}
			visible, err := disk.Load()
			if err != nil || visible.Idempotency["receipt"].Status != "pending" {
				t.Fatalf("published intent lost: %+v err=%v", visible, err)
			}
			code, retry := runWorkflow(t, args, tc.input)
			_, appended = server.snapshot()
			if code != 4 || !strings.Contains(retry, tc.recoveryCode) || len(sent()) != 0 || len(appended) != 0 {
				t.Fatalf("visible pending intent failed to stop retry: exit=%d sent=%d appended=%d out=%s", code, len(sent()), len(appended), retry)
			}
		})
	}
}
