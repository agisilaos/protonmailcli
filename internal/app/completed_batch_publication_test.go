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

func TestDraftBatchReportsPublishedCheckpointFailure(t *testing.T) {
	for _, scenario := range []string{"recovered", "recovered partial", "fresh"} {
		t.Run(scenario, func(t *testing.T) {
			fresh := scenario == "fresh"
			peer := &workflowServer{draftBox: "Former Drafts"}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["fixture@example.invalid"]}`, `{"ok":true,"draftId":"imap:Former Drafts:91","uid":"91","createPath":"imap_append"}`, "")
			if err := os.WriteFile(base[4], []byte(`{"idempotency":{"cached":`+receipt+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			input := `[{"to":["fixture@example.invalid"],"body":"one","idempotency_key":"cached"}`
			if fresh {
				input = `[{"to":["fixture@example.invalid"],"body":"one"}`
			}
			count, retryExit := 1, 0
			if scenario == "recovered partial" {
				input += `,{"body":"missing recipient"}`
				count, retryExit = 2, 10
			}
			input += `]`
			disk := store.New(base[4])
			args := append(base, "draft", "create-many", "--stdin", "--idempotency-key", "batch")
			var out bytes.Buffer
			app := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(state model.State) error {
				if err := disk.Save(state); err != nil {
					return err
				}
				if state.Idempotency["batch"].Status == "complete" {
					return fmt.Errorf("%w: synthetic directory sync failure", store.ErrCheckpointUncertain)
				}
				return nil
			}}
			code := app.run(args)
			var reply struct {
				Error struct{ Code, Message, Hint string }
				Data  batchResultResponse
			}
			if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			commands, appended := peer.snapshot()
			wantExit, wantCode, wantAppends := 1, "state_save_failed", 0
			if fresh {
				wantExit, wantCode, wantAppends = 4, "imap_draft_create_uncertain", 1
			}
			if code != wantExit || reply.Error.Code != wantCode || !strings.Contains(reply.Error.Message, "published") ||
				!strings.Contains(reply.Error.Hint, "synthetic directory sync failure") || strings.Contains(out.String(), "not recorded") ||
				reply.Data.Count != count || reply.Data.Success != 1 || reply.Data.Failed != count-1 || len(reply.Data.Results) != count ||
				reply.Data.Results[0].DraftID != "imap:Former Drafts:91" || (!fresh && len(commands) != 0) || len(appended) != wantAppends {
				t.Fatalf("published completed receipt misreported: exit=%d commands=%v out=%s", code, commands, &out)
			}
			state, err := disk.Load()
			if err != nil || state.Idempotency["batch"].Status != "complete" {
				t.Fatalf("published completed receipt missing: %+v %v", state.Idempotency["batch"], err)
			}
			code, replay := runWorkflow(t, args, input)
			if code != retryExit || !strings.Contains(replay, "imap:Former Drafts:91") {
				t.Fatalf("same-key replay did not recover completion: exit=%d out=%s", code, replay)
			}
			code, conflict := runWorkflow(t, args, strings.Replace(input, `"one"`, `"changed"`, 1))
			previousCommands := len(commands)
			commands, appended = peer.snapshot()
			if code != 6 || !strings.Contains(conflict, "idempotency_conflict") || len(commands) != previousCommands || len(appended) != wantAppends {
				t.Fatalf("published key did not bind its original input: exit=%d commands=%v out=%s", code, commands, conflict)
			}
		})
	}
}
