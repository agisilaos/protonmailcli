package app

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestBridgeOperationKeepsValidatedTrustAfterReservation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		input string
	}{
		{"send", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "key"}, ""},
		{"send-many", []string{"message", "send-many", "--stdin", "--idempotency-key", "key"}, `[{"draft_id":"41","confirm_send":"41","idempotency_key":"item"}]`},
		{"draft fallback", []string{"draft", "create", "--to", "recipient@example.invalid", "--body", "test", "--idempotency-key", "key"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &workflowServer{appendFailure: tc.name == "draft fallback"}
			args := imapCommandArgs(t, newWorkflowServer(t, peer))
			delivered := captureWorkflowSMTP(t, args)
			cfg, err := config.Load(args[2])
			if err != nil {
				t.Fatal(err)
			}
			disk := store.New(args[4])
			var out bytes.Buffer
			removed := false
			app := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(tc.input), checkpoint: func(st model.State) error {
				if !removed && st.Idempotency["key"].Status == "pending" {
					if err := os.Remove(cfg.Bridge.TLSCertFile); err != nil {
						return err
					}
					removed = true
				}
				return disk.Save(st)
			}}
			code := app.run(append(args, tc.args...))
			if code != 0 || !removed || len(delivered()) != 1 {
				t.Fatalf("exit=%d removed=%t synthetic deliveries=%d out=%s", code, removed, len(delivered()), out.String())
			}
			st, err := disk.Load()
			if err != nil || st.Idempotency["key"].Status != "complete" {
				t.Fatalf("missing completed receipt: state=%+v err=%v", st.Idempotency["key"], err)
			}
			// A new operation independently validates the now-missing trust file.
			next := append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "next")
			code, result := runWorkflow(t, next, "")
			st, err = disk.Load()
			_, reserved := st.Idempotency["next"]
			if code != 3 || !strings.Contains(result, "config_error") || err != nil || reserved || len(delivered()) != 1 {
				t.Fatalf("next operation: exit=%d reserved=%t err=%v output=%s", code, reserved, err, result)
			}
		})
	}
}
