package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestSendBatchCheckpointFailurePreservesOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		failAt                            int
		commandKey                        bool
		wantExit, wantCount               int
		wantCode, wantMessage, pendingKey string
	}{
		{"item reservation", 2, false, 1, 1, "state_save_failed", "item 1 (imap:Brouillons:42) was not dispatched", ""},
		{"item completion", 3, false, 4, 2, "imap_send_uncertain", "SMTP accepted batch item 1 (imap:Brouillons:42) but its receipt could not be saved", "item-key"},
		{"command completion", 4, true, 4, 3, "imap_send_uncertain", "batch result receipt could not be saved", "batch-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &workflowServer{draftBox: "Brouillons"}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			delivered := captureWorkflowSMTP(t, base)
			args := append(base, "message", "send-many", "--stdin")
			if tc.commandKey {
				args = append(args, "--idempotency-key", "batch-key")
			}
			const input = `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42","idempotency_key":"item-key"},{"draft_id":"43","confirm_send":"43"}]`
			disk, checkpoints := store.New(base[4]), 0
			var out bytes.Buffer
			app := App{Stdin: strings.NewReader(input), Stdout: &out, Stderr: io.Discard, checkpoint: func(st model.State) error {
				checkpoints++
				if checkpoints == tc.failAt {
					return fmt.Errorf("synthetic checkpoint unavailable")
				}
				return disk.Save(st)
			}}
			code := app.run(args)
			var result struct {
				Error struct {
					Code, Message, Hint string
					Retryable           bool
				}
				Data batchResultResponse
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if code != tc.wantExit || result.Error.Code != tc.wantCode || result.Error.Retryable || !strings.Contains(result.Error.Message, tc.wantMessage) ||
				result.Data.Count != tc.wantCount || result.Data.Success != tc.wantCount || result.Data.Failed != 0 || len(result.Data.Results) != tc.wantCount || len(delivered()) != tc.wantCount || checkpoints != tc.failAt {
				t.Fatalf("checkpoint failure lost dispatch outcomes: exit=%d checkpoints=%d sent=%d out=%s", code, checkpoints, len(delivered()), out.String())
			}
			for i, item := range result.Data.Results {
				wantID := fmt.Sprintf("imap:Brouillons:%d", 41+i)
				if item.Index != i || !item.OK || item.DraftID != wantID || item.SentAt == "" {
					t.Fatalf("lost confirmed row %d: %s", i, out.String())
				}
			}
			state, err := disk.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.pendingKey == "" {
				if len(state.Idempotency) != 0 || !strings.Contains(result.Error.Hint, "Do not rerun the whole manifest") {
					t.Fatalf("undispatched row lacks safe recovery: receipts=%v out=%s", state.Idempotency, out.String())
				}
			} else {
				if state.Idempotency[tc.pendingKey].Status != "pending" {
					t.Fatalf("lost pending recovery: %+v", state.Idempotency)
				}
				code, replay := runWorkflow(t, args, input)
				if code != 4 || !strings.Contains(replay, "imap_send_uncertain") || len(delivered()) != tc.wantCount {
					t.Fatalf("unsafe checkpoint retry: exit=%d sent=%d out=%s", code, len(delivered()), replay)
				}
			}
			commands, _ := peer.snapshot()
			if tc.wantCount < 3 && strings.Contains(strings.Join(commands, "\n"), "UID FETCH 43 ") {
				t.Fatalf("later row was attempted: %v", commands)
			}
		})
	}
}

func TestSendBatchMalformedCachedItemPreservesEarlierOutcomes(t *testing.T) {
	peer := &workflowServer{draftBox: "Brouillons"}
	base := imapCommandArgs(t, newWorkflowServer(t, peer))
	delivered := captureWorkflowSMTP(t, base)
	const payload = `{"body":"original body","confirm":"42","draftId":"42","headers":{"In-Reply-To":"\u003cparent@example.invalid\u003e","References":"\u003croot@example.invalid\u003e \u003cparent@example.invalid\u003e"},"subject":"original","to":["recipient@example.invalid"]}`
	receipt := terminalReceiptFixture("imap.message.send-item", payload, `1`, "")
	saved := `{"idempotency":{"malformed":` + receipt + `}}`
	if err := os.WriteFile(base[4], []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	input := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42","idempotency_key":"malformed"},{"draft_id":"43","confirm_send":"43"}]`
	code, out := runWorkflow(t, append(base, "message", "send-many", "--stdin"), input)
	var result struct {
		Error struct{ Code, Message string }
		Data  batchResultResponse
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "runtime_error" || !strings.Contains(result.Error.Message, "cannot unmarshal number") ||
		result.Data.Count != 1 || result.Data.Success != 1 || len(result.Data.Results) != 1 || !result.Data.Results[0].OK || result.Data.Results[0].DraftID != "imap:Brouillons:41" || len(delivered()) != 1 {
		t.Fatalf("cached-item read failure lost earlier send: exit=%d sent=%d out=%s", code, len(delivered()), out)
	}
	if after, err := os.ReadFile(base[4]); err != nil || string(after) != saved {
		t.Fatalf("corrupt receipt was rewritten: %v", err)
	}
	commands, _ := peer.snapshot()
	if strings.Contains(strings.Join(commands, "\n"), "UID FETCH 43 ") {
		t.Fatalf("continued past unusable receipt: %v", commands)
	}
}
