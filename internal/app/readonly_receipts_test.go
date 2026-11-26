package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readonlyReceiptFixture(t *testing.T, args []string, records string) string {
	t.Helper()
	dir := t.TempDir()
	args[4] = filepath.Join(dir, "state.json")
	saved := `{"idempotency":` + records + `}`
	if err := os.WriteFile(args[4], []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if file, err := os.CreateTemp(dir, "permission-probe-"); err == nil {
		_ = file.Close()
		t.Skip("filesystem permits writes in a read-only directory")
	}
	return saved
}

func assertReadonlyReceiptUnchanged(t *testing.T, path, saved string, server *workflowServer) {
	t.Helper()
	if current, err := os.ReadFile(path); err != nil || string(current) != saved {
		t.Fatalf("read-only recovery changed state: %q %v", current, err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("read-only recovery created a writer lock: %v", err)
	}
	commands, _ := server.snapshot()
	if len(commands) != 0 {
		t.Fatalf("read-only recovery contacted Bridge: %v", commands)
	}
}

func terminalReceiptFixture(operation, payload, response, failure string) string {
	hash := sha256.Sum256([]byte(payload))
	if failure == "" {
		failure = "null"
	}
	return fmt.Sprintf(`{"operation":%q,"status":"complete","payloadHash":"%x","response":%s,"failure":%s}`, operation, hash, response, failure)
}

func TestReadonlyFreshBatchKeyStillReportsPendingItem(t *testing.T) {
	for _, tc := range []struct{ resource, action, operation, manifest, code string }{
		{"message", "send-many", "imap.message.send-item", `[{"draft_id":"41","confirm_send":"41","idempotency_key":"pending-item"}]`, "imap_send_uncertain"},
		{"draft", "create-many", "imap.draft.create-item", `[{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"pending-item"}]`, "imap_draft_create_uncertain"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			saved := readonlyReceiptFixture(t, args, fmt.Sprintf(`{"pending-item":{"operation":%q,"status":"pending"}}`, tc.operation))
			code, out := runWorkflow(t, append(args, tc.resource, tc.action, "--stdin", "--idempotency-key", "fresh-batch"), tc.manifest)
			if code != 4 || !strings.Contains(out, tc.code) || !strings.Contains(out, `"retryable":false`) {
				t.Fatalf("fresh command key hid pending item: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, args[4], saved, s)
		})
	}
}

func TestReadonlyTerminalReceiptsReplayBeforeBackendAccess(t *testing.T) {
	const draftPayload = `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`
	const draftManifest = `[{"to":["synthetic@example.invalid"],"subject":"","body":"one"},{"to":["synthetic@example.invalid"],"subject":"","body":"two"}]`
	const sendManifest = `[{"draft_id":"41","confirm_send":"41"},{"draft_id":"42","confirm_send":"42"}]`
	const partialResponse = `{"results":[{"index":0,"ok":true,"draftId":"imap:Drafts:91"},{"index":1,"ok":false,"errorCode":"imap_draft_create_uncertain","error":"Inspect Drafts before retrying"}],"count":2,"success":1,"failed":1,"source":"imap"}`
	const failedResponse = `{"results":[{"index":0,"ok":false,"errorCode":"validation_error","error":"invalid input"},{"index":1,"ok":false,"errorCode":"validation_error","error":"invalid input"}],"count":2,"success":0,"failed":2,"source":"imap"}`
	for _, tc := range []struct {
		name, operation, payload, response, failure, input, want string
		args                                                     []string
		exit                                                     int
	}{
		{"draft-success", "draft.create", draftPayload, `{"draft":{"id":"imap:Drafts:91"},"source":"imap"}`, "", "", "imap:Drafts:91", []string{"draft", "create", "--to", "synthetic@example.invalid", "--body", "one"}, 0},
		{"draft-uncertain", "draft.create", draftPayload, "null", `{"exit":4,"code":"imap_draft_create_uncertain","message":"append completion lost","hint":"Inspect Drafts before retrying"}`, "", "Inspect Drafts before retrying", []string{"draft", "create", "--to", "synthetic@example.invalid", "--body", "one"}, 4},
		{"draft-batch-partial", "draft.create-many", draftManifest, partialResponse, "", draftManifest, "imap:Drafts:91", []string{"draft", "create-many", "--stdin"}, 10},
		{"send-batch-partial", "message.send-many", sendManifest, partialResponse, "", sendManifest, "imap:Drafts:91", []string{"message", "send-many", "--stdin"}, 10},
		{"draft-batch-failed", "draft.create-many", draftManifest, failedResponse, "", draftManifest, "validation_error", []string{"draft", "create-many", "--stdin"}, 4},
		{"send-batch-failed", "message.send-many", sendManifest, failedResponse, "", sendManifest, "validation_error", []string{"message", "send-many", "--stdin"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			record := terminalReceiptFixture(tc.operation, tc.payload, tc.response, tc.failure)
			saved := readonlyReceiptFixture(t, base, `{"receipt":`+record+`}`)
			args := append(append(append([]string(nil), base...), tc.args...), "--idempotency-key", "receipt")
			code, out := runWorkflow(t, args, tc.input)
			if code != tc.exit || !strings.Contains(out, tc.want) {
				t.Fatalf("terminal receipt hidden: exit=%d want=%d out=%s", code, tc.exit, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, s)
			conflictArgs, conflictInput := append([]string(nil), args...), tc.input
			if tc.input != "" {
				conflictInput = strings.Replace(tc.input, "41", "43", 1)
				if tc.operation == "draft.create-many" {
					conflictInput = strings.Replace(tc.input, "one", "changed", 1)
				}
			} else {
				for i, arg := range conflictArgs {
					if arg == "--body" {
						conflictArgs[i+1] = "changed"
					}
				}
			}
			code, out = runWorkflow(t, conflictArgs, conflictInput)
			if code != 6 || !strings.Contains(out, "idempotency_conflict") {
				t.Fatalf("changed payload replayed a receipt: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, s)
		})
	}
}

func TestReadonlyRecoveryNeverStartsTransportForFreshWrites(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
	}{
		{"keyed-draft", "", []string{"draft", "create", "--to", "synthetic@example.invalid", "--body", "one", "--idempotency-key", "fresh"}},
		{"draft-batch", `[{"to":["synthetic@example.invalid"],"body":"one"}]`, []string{"draft", "create-many", "--stdin", "--idempotency-key", "fresh"}},
		{"empty-draft-batch", `[]`, []string{"draft", "create-many", "--stdin", "--idempotency-key", "fresh"}},
		{"single-send", "", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "fresh"}},
		{"send-batch", `[{"draft_id":"41","confirm_send":"41"}]`, []string{"message", "send-many", "--stdin", "--idempotency-key", "fresh"}},
		{"send-item", `[{"draft_id":"41","confirm_send":"41","idempotency_key":"fresh"}]`, []string{"message", "send-many", "--stdin"}},
		{"follow-up", "", []string{"message", "follow-up", "--message-id", "41", "--body", "one", "--idempotency-key", "fresh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			saved := readonlyReceiptFixture(t, args, `{}`)
			code, out := runWorkflow(t, append(args, tc.args...), tc.input)
			wantCode := "state_error"
			if tc.name == "empty-draft-batch" {
				wantCode = "state_save_failed"
			}
			if code != 1 || !strings.Contains(out, `"code":"`+wantCode+`"`) {
				t.Fatalf("fresh write bypassed the unavailable writer: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, args[4], saved, s)
		})
	}
}

func TestReadonlyStatePreservesUnkeyedSendsAndDryRuns(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		dryRun      bool
		sends       int
	}{
		{"single-send", "", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41"}, false, 1},
		{"send-batch", `[{"draft_id":"41","confirm_send":"41"}]`, []string{"message", "send-many", "--stdin"}, false, 1},
		{"keyed-dry-run", "", []string{"message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "preview"}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, args)
			saved := readonlyReceiptFixture(t, args, `{}`)
			statePath := args[4]
			if tc.dryRun {
				args = append([]string{"--dry-run"}, args...)
			}
			code, out := runWorkflow(t, append(args, tc.args...), tc.input)
			if code != 0 || len(sent()) != tc.sends {
				t.Fatalf("command that needs no state write regressed: exit=%d sends=%d out=%s", code, len(sent()), out)
			}
			if current, err := os.ReadFile(statePath); err != nil || string(current) != saved {
				t.Fatalf("command changed read-only state: %q %v", current, err)
			}
			if _, err := os.Stat(statePath + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("command created a writer lock: %v", err)
			}
		})
	}
}

func TestReadonlyDraftItemsReplayTheirCompatibleTerminalOutcomes(t *testing.T) {
	for _, batchKey := range []string{"", "fresh-batch"} {
		t.Run("batch-key="+batchKey, func(t *testing.T) {
			s := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			success := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`, `{"index":9,"ok":true,"draftId":"imap:Drafts:91"}`, "")
			uncertain := terminalReceiptFixture("imap.draft.create-item", `{"body":"two","subject":"","to":["synthetic@example.invalid"]}`, "null", `{"exit":4,"code":"imap_draft_create_uncertain","message":"Inspect Drafts before retrying"}`)
			saved := readonlyReceiptFixture(t, base, `{"success":`+success+`,"uncertain":`+uncertain+`}`)
			args := append(append([]string(nil), base...), "draft", "create-many", "--stdin")
			if batchKey != "" {
				args = append(args, "--idempotency-key", batchKey)
			}
			input := `[{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"success"},{"to":["synthetic@example.invalid"],"body":"two","idempotency_key":"uncertain"}]`
			code, out := runWorkflow(t, args, input)
			wantExit := 10
			if batchKey != "" {
				wantExit = 1
			}
			if code != wantExit || !strings.Contains(out, `"index":0,"ok":true,"draftId":"imap:Drafts:91"`) || !strings.Contains(out, "imap_draft_create_uncertain") || (batchKey != "" && !strings.Contains(out, "not recorded")) {
				t.Fatalf("compatible item receipts were hidden: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, s)
			code, out = runWorkflow(t, args, strings.Replace(input, `"body":"two"`, `"body":"changed"`, 1))
			if code != wantExit || !strings.Contains(out, "idempotency_conflict") || !strings.Contains(out, "imap:Drafts:91") {
				t.Fatalf("item conflict was not retained beside the cached success: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, s)
			code, out = runWorkflow(t, args, strings.Replace(input, `"idempotency_key":"uncertain"`, `"idempotency_key":"new-item"`, 1))
			if code != 1 || !strings.Contains(out, `"code":"state_error"`) {
				t.Fatalf("fresh item bypassed the unavailable writer: exit=%d out=%s", code, out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, s)
		})
	}
}
