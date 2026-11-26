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
			if code != 4 || len(sent()) != 1 || !strings.Contains(out.String(), "published") || !strings.Contains(out.String(), "matching retry") {
				t.Fatalf("published receipt misreported: exit=%d sent=%d output=%s", code, len(sent()), out.String())
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
