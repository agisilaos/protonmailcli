package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadonlyDraftBatchStopsBeforeUnkeyedDispatchWhenLaterKeyIsFresh(t *testing.T) {
	for _, existingLock := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-lock=%v", existingLock), func(t *testing.T) {
			testReadonlyDraftBatchStopsBeforeUnkeyedDispatch(t, existingLock)
		})
	}
}

func testReadonlyDraftBatchStopsBeforeUnkeyedDispatch(t *testing.T, existingLock bool) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	cached := terminalReceiptFixture("imap.draft.create-item", `{"body":"old","subject":"","to":["fixture@example.invalid"]}`,
		`{"ok":true,"draftId":"imap-v2:Drafts:90","uid":"90"}`, "")
	saved := `{"idempotency":{"cached":` + cached + `}}`
	if existingLock {
		readonlyStateWithExistingLock(t, base, saved)
	} else {
		saved = readonlyReceiptFixture(t, base, `{"cached":`+cached+`}`)
	}
	manifest := filepath.Join(t.TempDir(), "drafts.json")
	items := `[
		{"to":["fixture@example.invalid"],"body":"old","idempotency_key":"cached"},
		{"body":"missing recipient"},
		{"to":["fixture@example.invalid"],"body":"unkeyed"},
		{"to":["fixture@example.invalid"],"body_file":"-","idempotency_key":"fresh"}
	]`
	if err := os.WriteFile(manifest, []byte(items), 0600); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("later row body")
	var out bytes.Buffer
	code := Run(append(base, "draft", "create-many", "--file", manifest), input, &out, io.Discard)
	var result struct {
		Error struct{ Code string }
		Data  batchResultResponse
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	_, appended := server.snapshot()
	if code != 1 || (result.Error.Code != "state_error" && result.Error.Code != "state_save_failed") || len(appended) != 0 || input.Len() != len("later row body") {
		t.Fatalf("exit=%d appends=%d unread=%d output=%s", code, len(appended), input.Len(), out.String())
	}
	if result.Data.Count != 2 || result.Data.Success != 1 || result.Data.Failed != 1 ||
		len(result.Data.Results) != 2 || result.Data.Results[0].DraftID != "imap-v2:Drafts:90" ||
		result.Data.Results[1].ErrorCode != "validation_error" {
		t.Fatalf("earlier local results lost: %+v", result.Data)
	}
	assertReadonlyMixedReceiptUnchanged(t, base[4], saved, server, existingLock)
}

func TestReadonlySendBatchStopsBeforeUnkeyedDispatchWhenLaterKeyIsFresh(t *testing.T) {
	for _, existingLock := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-lock=%v", existingLock), func(t *testing.T) {
			testReadonlySendBatchStopsBeforeUnkeyedDispatch(t, existingLock)
		})
	}
}

func testReadonlySendBatchStopsBeforeUnkeyedDispatch(t *testing.T, existingLock bool) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	sent := captureWorkflowSMTP(t, base)
	saved := `{}`
	if existingLock {
		readonlyStateWithExistingLock(t, base, saved)
	} else {
		saved = readonlyReceiptFixture(t, base, `{}`)
	}
	items := `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42","idempotency_key":"fresh"}]`
	code, out := runWorkflow(t, append(base, "message", "send-many", "--stdin"), items)
	if code != 1 || (!strings.Contains(out, `"code":"state_error"`) && !strings.Contains(out, `"code":"state_save_failed"`)) || len(sent()) != 0 {
		t.Fatalf("exit=%d deliveries=%d output=%s", code, len(sent()), out)
	}
	assertReadonlyMixedReceiptUnchanged(t, base[4], saved, server, existingLock)
}

func assertReadonlyMixedReceiptUnchanged(t *testing.T, path, saved string, server *workflowServer, existingLock bool) {
	t.Helper()
	if !existingLock {
		assertReadonlyReceiptUnchanged(t, path, saved, server)
		return
	}
	current, err := os.ReadFile(path)
	commands, appended := server.snapshot()
	if err != nil || string(current) != saved || len(commands) != 0 || len(appended) != 0 {
		t.Fatalf("failed preflight changed state or contacted Bridge: state=%q err=%v commands=%v", current, err, commands)
	}
}

func TestMixedBatchDryRunDoesNotCheckpoint(t *testing.T) {
	for _, mode := range []string{"draft", "message"} {
		t.Run(mode, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			sent := captureWorkflowSMTP(t, base)
			const saved = "{}\n"
			readonlyStateWithExistingLock(t, base, saved)
			args := []string{"--dry-run", "draft", "create-many", "--stdin"}
			items := `[{"to":["fixture@example.invalid"],"body":"unkeyed"},{"to":["fixture@example.invalid"],"body":"fresh","idempotency_key":"fresh"}]`
			if mode == "message" {
				args = []string{"--dry-run", "message", "send-many", "--stdin"}
				items = `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42","idempotency_key":"fresh"}]`
			}
			code, out := runWorkflow(t, append(base, args...), items)
			after, err := os.ReadFile(base[4])
			_, appended := server.snapshot()
			if code != 0 || !strings.Contains(out, `"success":2`) || len(appended) != 0 || len(sent()) != 0 || err != nil || string(after) != saved {
				t.Fatalf("dry-run checkpointed or dispatched: exit=%d out=%s appended=%d sent=%d state=%q err=%v", code, out, len(appended), len(sent()), after, err)
			}
		})
	}
}

func TestPendingDraftItemDoesNotCheckpointMixedWritableManifest(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	saved := `{"future_metadata":{"preserve":true},"idempotency":{"pending":{"operation":"imap.draft.create-item","status":"pending"}}}`
	if err := os.WriteFile(base[4], []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	items := `[{"to":["fixture@example.invalid"]},{"to":["fixture@example.invalid"],"idempotency_key":"fresh"},{"to":["fixture@example.invalid"],"idempotency_key":"pending"}]`
	code, out := runWorkflow(t, append(base, "draft", "create-many", "--stdin"), items)
	current, err := os.ReadFile(base[4])
	commands, appended := server.snapshot()
	if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || err != nil || string(current) != saved || len(commands) != 0 || len(appended) != 0 {
		t.Fatalf("recovery exit=%d out=%s state=%s err=%v commands=%v appends=%d", code, out, current, err, commands, len(appended))
	}
}
