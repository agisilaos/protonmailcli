package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func sendRecoveryArgs(t *testing.T, mode string) ([]string, string, func() []string) {
	t.Helper()
	s := &workflowServer{appendFailure: mode == "follow-up"}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	sent := captureWorkflowSMTP(t, args)
	switch mode {
	case "single":
		return append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "send-key"), "", sent
	case "batch":
		return append(args, "message", "send-many", "--stdin", "--idempotency-key", "send-key"), `[{"draft_id":"41","confirm_send":"41"}]`, sent
	case "item":
		return append(args, "message", "send-many", "--stdin"), `[{"draft_id":"41","confirm_send":"41","idempotency_key":"send-key"}]`, sent
	case "follow-up":
		return append(args, "message", "follow-up", "--message-id", "41", "--body", "reply", "--idempotency-key", "send-key"), "", sent
	}
	t.Fatalf("invalid mode %q", mode)
	return nil, "", nil
}
func TestKeyedSMTPRequiresDurableIntentBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"single", "batch", "item", "follow-up"} {
		t.Run(mode, func(t *testing.T) {
			args, input, sent := sendRecoveryArgs(t, mode)
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(model.State) error { return fmt.Errorf("synthetic checkpoint unavailable") }}
			code := a.run(args)
			if code == 0 || len(sent()) != 0 || !strings.Contains(out.String(), "state_save_failed") {
				t.Fatalf("exit=%d sends=%d out=%s", code, len(sent()), out.String())
			}
		})
	}
}
func TestKeyedSMTPWithUnwritableInitialStateDoesNotDispatch(t *testing.T) {
	args, input, sent := sendRecoveryArgs(t, "single")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	args[4] = filepath.Join(dir, "state.json")
	for i := 0; i < 2; i++ {
		code, out := runWorkflow(t, args, input)
		if code == 0 || len(sent()) != 0 {
			t.Fatalf("run=%d exit=%d sends=%d out=%s", i, code, len(sent()), out)
		}
	}
}

func TestAcceptedKeyedSMTPKeepsPendingIntentWhenReceiptSaveFails(t *testing.T) {
	for _, mode := range []string{"single", "batch", "item", "follow-up"} {
		t.Run(mode, func(t *testing.T) {
			args, input, sent := sendRecoveryArgs(t, mode)
			disk := store.New(args[4])
			saves := 0
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(st model.State) error {
				saves++
				if saves == 2 {
					return fmt.Errorf("synthetic receipt save failure")
				}
				return disk.Save(st)
			}}
			code := a.run(args)
			wantCode := "imap_send_uncertain"
			if mode == "follow-up" {
				wantCode = "imap_draft_create_uncertain"
			}
			if code != 4 || len(sent()) != 1 || !strings.Contains(out.String(), wantCode) || !strings.Contains(out.String(), `"retryable":false`) {
				t.Fatalf("first exit=%d sends=%d saves=%d out=%s", code, len(sent()), saves, out.String())
			}
			st, err := disk.Load()
			if err != nil || st.Idempotency["send-key"].Status != "pending" {
				t.Fatalf("intent not durable: %+v %v", st.Idempotency, err)
			}
			t.Setenv("PMAIL_SMTP_PASSWORD", "")
			code, replay := runWorkflow(t, args, input)
			if code != 4 || len(sent()) != 1 || !strings.Contains(replay, wantCode) || !strings.Contains(replay, `"retryable":false`) {
				t.Fatalf("replay exit=%d sends=%d out=%s", code, len(sent()), replay)
			}
		})
	}
}

func TestUnacknowledgedKeyedSMTPIsNotReplayed(t *testing.T) {
	for _, mode := range []string{"single", "batch", "item"} {
		t.Run(mode, func(t *testing.T) {
			args, input, _ := sendRecoveryArgs(t, mode)
			sent := captureWorkflowSMTPWithCompletion(t, args, false)
			for i := 0; i < 2; i++ {
				code, out := runWorkflow(t, args, input)
				if code != 4 || len(sent()) != 1 || !strings.Contains(out, "imap_send_uncertain") || !strings.Contains(out, `"retryable":false`) {
					t.Fatalf("run=%d exit=%d sends=%d out=%s", i, code, len(sent()), out)
				}
			}
		})
	}
}
func TestKeyedSMTPDryRunDoesNotPersistIntent(t *testing.T) {
	for _, mode := range []string{"single", "batch", "item", "follow-up"} {
		t.Run(mode, func(t *testing.T) {
			args, input, sent := sendRecoveryArgs(t, mode)
			args = append(args[:5], append([]string{"--dry-run"}, args[5:]...)...)
			code, out := runWorkflow(t, args, input)
			_, err := os.Stat(args[4])
			if code != 0 || len(sent()) != 0 || !os.IsNotExist(err) {
				t.Fatalf("exit=%d sends=%d stateErr=%v out=%s", code, len(sent()), err, out)
			}
		})
	}
}
