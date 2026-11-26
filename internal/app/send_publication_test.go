package app

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestPublishedSendReceiptRetainsReplayGuidance(t *testing.T) {
	for _, mode := range []string{"single", "batch", "item"} {
		t.Run(mode, func(t *testing.T) {
			args, input, sent := sendRecoveryArgs(t, mode)
			disk := store.New(args[4])
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(state model.State) error {
				if err := disk.Save(state); err != nil {
					return err
				}
				if state.Idempotency["send-key"].Status == "complete" {
					return fmt.Errorf("%w: simulated sync failure", store.ErrCheckpointUncertain)
				}
				return nil
			}}
			code := a.run(args)
			wantHint := "matching retry"
			if mode == "item" {
				wantHint = "reconcile every batch item"
			}
			if code != 4 || len(sent()) != 1 || !strings.Contains(out.String(), "published") || !strings.Contains(out.String(), wantHint) {
				t.Fatalf("published receipt misreported: exit=%d sent=%d output=%s", code, len(sent()), out.String())
			}
			if mode == "single" && (!strings.Contains(out.String(), "SMTP accepted imap-v2:Drafts:41") || !strings.Contains(out.String(), `"sent":true`)) {
				t.Fatalf("missing accepted send context: %s", out.String())
			}
			code, replay := runWorkflow(t, args, input)
			if code != 0 || len(sent()) != 1 {
				t.Fatalf("replay exit=%d sent=%d out=%s", code, len(sent()), replay)
			}
		})
	}
}

func TestDraftDiscoveryFailureDoesNotClaimDispatch(t *testing.T) {
	peer := &workflowServer{listRows: []string{`* LIST () NIL INBOX`}}
	base := imapCommandArgs(t, newWorkflowServer(t, peer))
	args := append(base, "draft", "create-many", "--stdin", "--idempotency-key", "batch")
	var out bytes.Buffer
	a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(`[{"to":["fixture@example.invalid"],"body":"fixture"}]`), checkpoint: func(model.State) error { return fmt.Errorf("injected save failure") }}
	code := a.run(args)
	_, appended := peer.snapshot()
	if code != 1 || len(appended) != 0 || !strings.Contains(out.String(), "state_save_failed") || strings.Contains(out.String(), "unfinished recovery record") {
		t.Fatalf("undispatched batch misreported: exit=%d appends=%d out=%s", code, len(appended), out.String())
	}
}

func TestPublishedBatchRowRequiresReconciliation(t *testing.T) {
	for _, commandKey := range []bool{false, true} {
		t.Run(fmt.Sprint(commandKey), func(t *testing.T) {
			args, _, sent := sendRecoveryArgs(t, "item")
			input := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"41","confirm_send":"41","idempotency_key":"send-key"}]`
			if commandKey {
				args = append(args, "--idempotency-key", "batch-key")
			}
			disk := store.New(args[4])
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(state model.State) error {
				if err := disk.Save(state); err != nil {
					return err
				}
				if state.Idempotency["send-key"].Status == "complete" {
					return fmt.Errorf("%w: injected sync", store.ErrCheckpointUncertain)
				}
				return nil
			}}
			code := a.run(args)
			if code != 4 || len(sent()) != 2 || !strings.Contains(out.String(), "published") || !strings.Contains(out.String(), "reconcile every batch item") || !strings.Contains(out.String(), "batch item 1") {
				t.Fatalf("unsafe batch guidance: exit=%d sent=%d out=%s", code, len(sent()), out.String())
			}
			if commandKey {
				code, _ = runWorkflow(t, args, input)
				if code != 4 || len(sent()) != 2 {
					t.Fatalf("pending batch replayed: exit=%d sent=%d", code, len(sent()))
				}
			}
		})
	}
}

func TestConfirmedDraftCheckpointFailureKeepsResult(t *testing.T) {
	for _, follow := range []bool{false, true} {
		t.Run(fmt.Sprint(follow), func(t *testing.T) {
			base := imapCommandArgs(t, newWorkflowServer(t, &workflowServer{}))
			args := append(base, "draft", "create", "--to", "fixture@example.invalid", "--body", "fixture", "--idempotency-key", "receipt")
			if follow {
				args = append(base, "message", "follow-up", "--message-id", "41", "--body", "fixture", "--idempotency-key", "receipt")
			}
			disk := store.New(base[4])
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(""), checkpoint: func(state model.State) error {
				if state.Idempotency["receipt"].Status == "complete" {
					return fmt.Errorf("lost final checkpoint")
				}
				return disk.Save(state)
			}}
			code := a.run(args)
			if code != 4 || !strings.Contains(out.String(), `"draft":{`) || !strings.Contains(out.String(), `"createPath":"imap_append"`) {
				t.Fatalf("confirmed draft result lost: exit=%d out=%s", code, out.String())
			}
		})
	}
}
