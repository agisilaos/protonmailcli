package app

import (
	"encoding/json"
	"fmt"
	"os"
	"protonmailcli/internal/model"
	"strings"
	"testing"
)

func TestReadonlyBatchAllowsTerminalKeyAndUnkeyedRow(t *testing.T) {
	for _, mode := range []string{"draft", "message"} {
		for _, existingLock := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing-lock=%v", mode, existingLock), func(t *testing.T) {
				server := &workflowServer{}
				base := imapCommandArgs(t, newWorkflowServer(t, server))
				sent := captureWorkflowSMTP(t, base)
				args := []string{"draft", "create-many", "--stdin"}
				first := `{"to":["fixture@example.invalid"],"body":"one","idempotency_key":"cached"}`
				second := `{"to":["fixture@example.invalid"],"body":"two"}`
				if mode == "message" {
					args = []string{"message", "send-many", "--stdin"}
					first = `{"draft_id":"41","confirm_send":"41","idempotency_key":"cached"}`
					second = `{"draft_id":"42","confirm_send":"42"}`
				}
				if code, out := runWorkflow(t, append(append([]string{}, base...), args...), "["+first+"]"); code != 0 {
					t.Fatalf("setup: %d %s", code, out)
				}
				raw, err := os.ReadFile(base[4])
				if err != nil {
					t.Fatal(err)
				}
				var state model.State
				if err = json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
				records, err := json.Marshal(state.Idempotency)
				if err != nil {
					t.Fatal(err)
				}
				saved := `{"idempotency":` + string(records) + `}`
				if existingLock {
					readonlyStateWithExistingLock(t, base, saved)
				} else {
					saved = readonlyReceiptFixture(t, base, string(records))
				}
				code, out := runWorkflow(t, append(base, args...), "["+first+","+second+"]")
				_, appends := server.snapshot()
				if code != 0 || !strings.Contains(out, `"success":2`) || len(appends)+len(sent()) != 2 {
					t.Fatalf("exit=%d out=%s appends=%d sends=%d", code, out, len(appends), len(sent()))
				}
				if raw, err = os.ReadFile(base[4]); err != nil || string(raw) != saved {
					t.Fatalf("state changed: %v", err)
				}
			})
		}
	}
}

func TestReadonlyWrongOperationReportsLocalConflict(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		code  int
	}{
		{"single", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "taken"}, "", 6},
		{"item", []string{"message", "send-many", "--stdin"}, `[{"draft_id":"41","confirm_send":"41","idempotency_key":"taken"}]`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			saved := readonlyReceiptFixture(t, base, `{"taken":{"operation":"draft.create","status":"complete"}}`)
			code, out := runWorkflow(t, append(base, tc.args...), tc.input)
			if code != tc.code || !strings.Contains(out, "idempotency_conflict") {
				t.Fatalf("exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, server)
		})
	}
}
