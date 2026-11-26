package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"protonmailcli/internal/model"
)

func TestReadonlyRemoteTerminalReceiptsVerifyWithoutRedispatch(t *testing.T) {
	for _, mode := range []string{"single-send", "send-item", "follow-up"} {
		for _, legacyStatus := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy-status=%v", mode, legacyStatus), func(t *testing.T) {
				server := &workflowServer{}
				base := imapCommandArgs(t, newWorkflowServer(t, server))
				sent := captureWorkflowSMTP(t, base)
				var args []string
				input := ""
				switch mode {
				case "single-send":
					args = []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "receipt"}
				case "send-item":
					args = []string{"message", "send-many", "--stdin"}
					input = `[{"draft_id":"41","confirm_send":"41","idempotency_key":"receipt"}]`
				case "follow-up":
					args = []string{"message", "follow-up", "--message-id", "imap:INBOX:41", "--to", "reply@example.invalid", "--body", "reply", "--idempotency-key", "receipt"}
				}
				code, out := runWorkflow(t, append(append([]string(nil), base...), args...), input)
				if code != 0 {
					t.Fatalf("initial operation: exit=%d output=%s", code, out)
				}
				want := regressionData(t, out)
				original, err := os.ReadFile(base[4])
				if err != nil {
					t.Fatal(err)
				}
				var state model.State
				if err := json.Unmarshal(original, &state); err != nil {
					t.Fatal(err)
				}
				if legacyStatus {
					receipt := state.Idempotency["receipt"]
					receipt.Status = ""
					state.Idempotency["receipt"] = receipt
				}
				records, err := json.Marshal(state.Idempotency)
				if err != nil {
					t.Fatal(err)
				}
				saved := readonlyReceiptFixture(t, base, string(records))
				beforeCommands, beforeAppends := server.snapshot()
				beforeSends := len(sent())
				code, out = runWorkflow(t, append(append([]string(nil), base...), args...), input)
				if code != 0 || !reflect.DeepEqual(regressionData(t, out), want) {
					t.Fatalf("matching terminal receipt hidden by writer failure: exit=%d output=%s", code, out)
				}
				commands, _ := server.snapshot()
				if !strings.Contains(strings.Join(commands[len(beforeCommands):], "\n"), "UID FETCH 41 ") {
					t.Fatalf("remote payload was not verified: commands=%v", commands[len(beforeCommands):])
				}
				conflictArgs, conflictInput := append([]string(nil), args...), input
				wantConflict := 6
				if mode == "send-item" {
					conflictInput = strings.Replace(input, `"confirm_send":"41"`, `"confirm_send":"42"`, 1)
					wantConflict = 1
				} else {
					flag, value := "--confirm-send", "42"
					if mode == "follow-up" {
						flag, value = "--body", "changed"
					}
					for i, arg := range conflictArgs {
						if arg == flag {
							conflictArgs[i+1] = value
						}
					}
				}
				code, out = runWorkflow(t, append(append([]string(nil), base...), conflictArgs...), conflictInput)
				if code != wantConflict || !strings.Contains(out, "idempotency_conflict") {
					t.Fatalf("changed payload bypassed verification: exit=%d output=%s", code, out)
				}
				beforeFresh, _ := server.snapshot()
				freshArgs, freshInput := append([]string(nil), args...), input
				if mode == "send-item" {
					freshInput = strings.TrimSuffix(input, "]") + `,{"draft_id":"42","confirm_send":"42","idempotency_key":"fresh"}]`
				} else {
					freshArgs[len(freshArgs)-1] = "fresh"
				}
				code, out = runWorkflow(t, append(append([]string(nil), base...), freshArgs...), freshInput)
				commands, appended := server.snapshot()
				// Close sends LOGOUT without waiting for its reply, so the peer
				// may record cleanup of the preceding read after that call returns.
				var freshCommands []string
				for _, command := range commands[len(beforeFresh):] {
					if !strings.HasSuffix(command, " LOGOUT") {
						freshCommands = append(freshCommands, command)
					}
				}
				if code != 1 || !strings.Contains(out, `"code":"state_error"`) || len(freshCommands) != 0 {
					t.Fatalf("fresh write bypassed the unavailable writer: exit=%d output=%s commands=%v", code, out, commands[len(beforeFresh):])
				}
				server.mu.Lock()
				server.raw = strings.Replace(server.raw, "Subject: original", "Subject: changed", 1)
				server.mu.Unlock()
				code, out = runWorkflow(t, append(append([]string(nil), base...), args...), input)
				if code != wantConflict || !strings.Contains(out, "idempotency_conflict") {
					t.Fatalf("changed remote payload replayed stale success: exit=%d output=%s", code, out)
				}
				_, appended = server.snapshot()
				if len(appended) != len(beforeAppends) || len(sent()) != beforeSends {
					t.Fatalf("replay dispatched again: appends=%d sends=%d", len(appended), len(sent()))
				}
				if after, err := os.ReadFile(base[4]); err != nil || string(after) != saved {
					t.Fatalf("replay changed state: %v", err)
				}
				if _, err := os.Stat(base[4] + ".lock"); !os.IsNotExist(err) {
					t.Fatalf("replay published a writer lock: %v", err)
				}
			})
		}
	}
}
