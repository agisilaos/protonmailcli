package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestLostAppendKeepsOriginalDestinationInRecovery(t *testing.T) {
	for _, command := range []string{"create", "create-many", "follow-up"} {
		t.Run(command, func(t *testing.T) {
			const original = "Former Drafts"
			peer := &workflowServer{draftBox: original, appendCompletionLost: true}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			sent := captureWorkflowSMTP(t, base)
			args := append(base, "draft", "create", "--to", "recipient@example.invalid", "--body", "fixture", "--idempotency-key", "receipt")
			input := ""
			if command == "create-many" {
				args = append(base, "draft", "create-many", "--stdin")
				input = `[{"to":["recipient@example.invalid"],"body":"fixture","idempotency_key":"receipt"}]`
			} else if command == "follow-up" {
				args = append(base, "message", "follow-up", "--message-id", "41", "--body", "fixture", "--idempotency-key", "receipt")
			}
			code, out := runWorkflow(t, args, input)
			state, err := store.New(base[4]).Load()
			if err != nil {
				t.Fatal(err)
			}
			failure := state.Idempotency["receipt"].Failure
			_, appended := peer.snapshot()
			if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || !strings.Contains(out, original) || len(appended) != 1 || len(sent()) != 0 ||
				failure == nil || !strings.Contains(failure.Message, original) || !strings.Contains(failure.Hint, original) {
				t.Fatalf("APPEND outcome lost its destination: code=%d appended=%d sent=%d failure=%+v out=%s", code, len(appended), len(sent()), failure, out)
			}
			before, err := os.ReadFile(base[4])
			if err != nil {
				t.Fatal(err)
			}
			peer.mu.Lock()
			peer.draftBox = "New Drafts"
			peer.mu.Unlock()
			code, out = runWorkflow(t, args, input)
			after, err := os.ReadFile(base[4])
			_, appended = peer.snapshot()
			if code != 4 || !strings.Contains(out, original) || len(appended) != 1 || len(sent()) != 0 || err != nil || string(before) != string(after) {
				t.Fatalf("replay lost original destination or dispatched again: code=%d appended=%d sent=%d err=%v out=%s", code, len(appended), len(sent()), err, out)
			}
		})
	}
}

func TestPendingDraftRemembersDestinationBeforeDispatch(t *testing.T) {
	for _, command := range []string{"create", "follow-up", "batch", "row"} {
		t.Run(command, func(t *testing.T) {
			const original = "Draft Archive 2026"
			peer := &workflowServer{draftBox: original}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			sent := captureWorkflowSMTP(t, base)
			args := append(base, "draft", "create", "--to", "recipient@example.invalid", "--body", "fixture", "--idempotency-key", "receipt")
			input := ""
			switch command {
			case "follow-up":
				args = append(base, "message", "follow-up", "--message-id", "41", "--body", "fixture", "--idempotency-key", "receipt")
			case "row", "batch":
				args = append(base, "draft", "create-many", "--stdin")
				input = `[{"to":["recipient@example.invalid"],"body":"fixture","idempotency_key":"receipt"}]`
				if command == "batch" {
					args = append(args, "--idempotency-key", "receipt")
					input = `[{"to":["recipient@example.invalid"],"body":"fixture"}]`
				}
			}
			disk := store.New(base[4])
			var out bytes.Buffer
			a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(state model.State) error {
				if state.Idempotency["receipt"].Status == "complete" {
					return errors.New("lost terminal checkpoint")
				}
				return disk.Save(state)
			}}
			a.run(args)
			state, err := disk.Load()
			_, appended := peer.snapshot()
			if err != nil || state.Idempotency["receipt"].Status != "pending" || len(appended) != 1 {
				t.Fatalf("fixture did not interrupt after APPEND: %v %d %s", err, len(appended), out.String())
			}
			peer.mu.Lock()
			peer.draftBox = "Replacement Drafts"
			peer.mu.Unlock()
			code, recovery := runWorkflow(t, args, input)
			_, appended = peer.snapshot()
			if code != 4 || !strings.Contains(recovery, original) || len(appended) != 1 || len(sent()) != 0 {
				t.Fatalf("pending replay lost its destination: exit=%d appends=%d recovery=%s", code, len(appended), recovery)
			}
		})
	}
}
