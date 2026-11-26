package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestDraftBatchReportsOutcomesOnCheckpointFailure(t *testing.T) {
	const unkeyed = `{"to":["synthetic@example.invalid"],"body":"first"}`
	const cached = `{"to":["synthetic@example.invalid"],"body":"first","idempotency_key":"cached"}`
	const keyed = `{"to":["synthetic@example.invalid"],"body":"second","idempotency_key":"row"}`
	const later = `{"to":["synthetic@example.invalid"],"body":"later"}`

	for _, tc := range []struct {
		name, peer, key, failStatus string
		first, secondID, rowError   string
		appends, count, success     int
		batch                       bool
	}{
		{"item reservation", "confirmed", "row", "pending", unkeyed, "", "", 1, 1, 1, false},
		{"item reservation inside batch", "confirmed", "row", "pending", unkeyed, "", "", 1, 1, 1, true},
		{"batch reservation", "confirmed", "batch", "pending", cached, "", "", 0, 1, 1, true},
		{"created item receipt", "confirmed", "row", "complete", unkeyed, "imap-v2:Drafts:2", "", 2, 2, 2, false},
		{"uncertain item receipt", "mixed", "row", "complete", unkeyed, "", "imap_draft_create_uncertain", 2, 2, 1, false},
		{"rejected item receipt", "reject", "row", "complete", cached, "", "imap_draft_create_failed", 0, 2, 1, false},
		{"final batch receipt", "confirmed", "batch", "complete", unkeyed, "imap-v2:Drafts:2", "", 2, 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, appends := draftIMAPServer(t, tc.peer)
			args := imapCommandArgs(t, port)
			path := args[4]
			if tc.first == cached {
				record := terminalReceiptFixture("imap.draft.create-item", `{"body":"first","subject":"","to":["synthetic@example.invalid"]}`,
					`{"ok":true,"draftId":"imap-v2:Drafts:80","uid":"80","createPath":"imap_append"}`, "")
				if err := os.WriteFile(path, []byte(`{"idempotency":{"cached":`+record+`}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args = append(args, "draft", "create-many", "--stdin")
			if tc.batch {
				args = append(args, "--idempotency-key", "batch")
			}
			rows := []string{tc.first, keyed}
			if tc.key != "batch" || tc.failStatus != "complete" {
				rows = append(rows, later)
			}
			input := "[" + strings.Join(rows, ",") + "]"
			previous := smtpSendFn
			t.Cleanup(func() { smtpSendFn = previous })
			smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error { return errors.New("synthetic fallback rejection") }
			disk := store.New(path)
			var out bytes.Buffer
			app := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(input), checkpoint: func(st model.State) error {
				if st.Idempotency[tc.key].Status == tc.failStatus {
					return errors.New("checkpoint unavailable")
				}
				return disk.Save(st)
			}}
			code := app.run(args)
			var reply struct {
				Error struct{ Code, Message, Hint string }
				Data  *batchResultResponse
			}
			if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			wantExit, wantCode, durableStatus := 4, "imap_draft_create_uncertain", "pending"
			if tc.failStatus == "pending" {
				wantExit, wantCode, durableStatus = 1, "state_save_failed", ""
			}
			if code != wantExit || reply.Error.Code != wantCode || int(appends.Load()) != tc.appends ||
				reply.Data == nil || reply.Data.Count != tc.count || reply.Data.Success != tc.success ||
				len(reply.Data.Results) != tc.count || reply.Data.Failed != tc.count-tc.success {
				t.Fatalf("exit=%d appends=%d output=%s", code, appends.Load(), &out)
			}
			firstID := "imap-v2:Drafts:1"
			if tc.first == cached {
				firstID = "imap-v2:Drafts:80"
			}
			if first := reply.Data.Results[0]; first.Index != 0 || !first.OK || first.DraftID != firstID {
				t.Fatalf("earlier confirmed draft missing: %+v", first)
			}
			if tc.count == 2 {
				row := reply.Data.Results[1]
				if row.Index != 1 || row.DraftID != tc.secondID || row.ErrorCode != tc.rowError ||
					row.OK != (tc.secondID != "") || (row.OK && (row.UID != "2" || row.CreatePath != "imap_append")) {
					t.Fatalf("current row outcome lost: %+v", row)
				}
				if tc.rowError == "imap_draft_create_uncertain" && !strings.Contains(row.Error, "INBOX and Drafts") {
					t.Fatalf("uncertain result lacks recovery guidance: %+v", row)
				}
			}
			if tc.failStatus == "pending" && (!strings.Contains(reply.Error.Message, "row 1") || !strings.Contains(reply.Error.Message, "not dispatched")) {
				t.Fatalf("cannot distinguish undispatched row: %s", &out)
			}
			if tc.key == "row" && tc.secondID != "" && !strings.Contains(reply.Error.Message, tc.secondID) {
				t.Fatalf("missing confirmed ID in checkpoint error: %s", &out)
			}
			state, err := disk.Load()
			if err != nil || state.Idempotency[tc.key].Status != durableStatus {
				t.Fatalf("durable receipt status=%q want=%q error=%v", state.Idempotency[tc.key].Status, durableStatus, err)
			}
			if tc.batch && tc.key == "row" && (state.Idempotency["batch"].Status != "pending" ||
				!strings.Contains(reply.Error.Message, "batch key remains pending")) {
				t.Fatalf("outer reservation missing during recovery: %s", &out)
			}
			if durableStatus == "pending" || state.Idempotency["batch"].Status == "pending" {
				replayCode, replay := runWorkflow(t, args, input)
				if replayCode != 4 || !strings.Contains(replay, "unfinished recovery record") || int(appends.Load()) != tc.appends {
					t.Fatalf("pending receipt did not stop replay: exit=%d appends=%d output=%s", replayCode, appends.Load(), replay)
				}
			}
		})
	}
}

func TestDraftBatchRetainsEarlierRowsOnInvalidCachedItem(t *testing.T) {
	port, appends := draftIMAPServer(t, "confirmed")
	base := imapCommandArgs(t, port)
	record := terminalReceiptFixture("imap.draft.create-item", `{"body":"cached","subject":"","to":["synthetic@example.invalid"]}`, `"invalid cached item"`, "")
	if err := os.WriteFile(base[4], []byte(`{"idempotency":{"broken":`+record+`}}`), 0600); err != nil {
		t.Fatal(err)
	}
	input := `[{"to":["synthetic@example.invalid"],"body":"first"},{"to":["synthetic@example.invalid"],"body":"cached","idempotency_key":"broken"},{"to":["synthetic@example.invalid"],"body":"later"}]`
	code, out := runWorkflow(t, append(base, "draft", "create-many", "--stdin"), input)
	var reply struct {
		Error struct{ Code string }
		Data  batchResultResponse
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatal(err)
	}
	if code != 1 || reply.Error.Code != "runtime_error" || appends.Load() != 1 || reply.Data.Count != 1 || reply.Data.Success != 1 ||
		len(reply.Data.Results) != 1 || reply.Data.Results[0].DraftID != "imap-v2:Drafts:1" {
		t.Fatalf("exit=%d appends=%d output=%s", code, appends.Load(), out)
	}
}
