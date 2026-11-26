package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"protonmailcli/internal/store"
)

func readonlyStateWithExistingLock(t *testing.T, args []string, saved string) {
	t.Helper()
	dir := t.TempDir()
	args[4] = filepath.Join(dir, "state.json")
	if err := os.WriteFile(args[4], []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := store.New(args[4]).LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if file, err := os.CreateTemp(dir, "permission-probe-"); err == nil {
		file.Close()
		t.Skip("filesystem permits writes in a read-only directory")
	}
	lock, err = store.New(args[4]).LockWriter()
	if err != nil {
		t.Fatalf("fixture must retain a lockable sidecar: %v", err)
	}
	lock.Close()
}

func TestDraftBatchResolvesLocalOutcomesBeforeTransport(t *testing.T) {
	for _, access := range []string{"writable", "readonly existing lock", "readonly missing lock"} {
		for _, invalid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invalid=%v", access, invalid), func(t *testing.T) {
				server := &workflowServer{}
				base := imapCommandArgs(t, newWorkflowServer(t, server))
				receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["fixture@example.invalid"]}`, `{"index":9,"ok":true,"draftId":"imap:Drafts:91"}`, "")
				records := `{"cached":` + receipt + `}`
				saved := `{"idempotency":` + records + `}`
				switch access {
				case "writable":
					if err := os.WriteFile(base[4], []byte(saved), 0600); err != nil {
						t.Fatal(err)
					}
				case "readonly existing lock":
					readonlyStateWithExistingLock(t, base, saved)
				case "readonly missing lock":
					readonlyReceiptFixture(t, base, records)
				}
				input := `[{"to":["fixture@example.invalid"],"body":"one","idempotency_key":"cached"}`
				wantExit := 0
				if invalid {
					input += fmt.Sprintf(`,{"to":["fixture@example.invalid"],"body_file":%q}`, filepath.Join(t.TempDir(), "missing-body.txt"))
					wantExit = 10
				}
				input += `]`
				code, out := runWorkflow(t, append(base, "draft", "create-many", "--stdin"), input)
				commands, appended := server.snapshot()
				after, err := os.ReadFile(base[4])
				if code != wantExit || !strings.Contains(out, `"draftId":"imap:Drafts:91"`) ||
					(invalid && !strings.Contains(out, `"errorCode":"validation_error"`)) || len(commands) != 0 || len(appended) != 0 || err != nil || string(after) != saved {
					t.Fatalf("locally resolved batch: exit=%d output=%s commands=%v state unchanged=%t error=%v", code, out, commands, string(after) == saved, err)
				}
			})
		}
	}
}

func TestUnkeyedRemoteDraftDoesNotSaveUnchangedState(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			const saved = "{}\n"
			readonlyStateWithExistingLock(t, base, saved)
			args := append(append([]string(nil), base...), "draft", "create", "--to", "fixture@example.invalid", "--body", "one")
			input := ""
			if batch {
				args = append(append([]string(nil), base...), "draft", "create-many", "--stdin")
				input = `[{"to":["fixture@example.invalid"],"body":"one"}]`
			}
			code, out := runWorkflow(t, args, input)
			_, appended := server.snapshot()
			after, err := os.ReadFile(base[4])
			if code != 0 || len(appended) != 1 || !strings.Contains(out, "imap-v2:Drafts:91") || err != nil || string(after) != saved {
				t.Fatalf("confirmed remote draft lost to unrelated save: exit=%d appended=%d output=%s state=%q error=%v", code, len(appended), out, after, err)
			}
		})
	}
}

func TestRemoteEditsDoNotSaveUnchangedState(t *testing.T) {
	for _, args := range [][]string{
		{"draft", "update", "--draft-id", "41", "--subject", "changed"},
		{"draft", "delete", "--draft-id", "41"},
		{"tag", "add", "--message-id", "imap:INBOX:41", "--tag", "reviewed"},
		{"tag", "remove", "--message-id", "imap:INBOX:41", "--tag", "reviewed"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			const saved = "{}\n"
			readonlyStateWithExistingLock(t, base, saved)
			code, out := runWorkflow(t, append(base, args...), "")
			commands, _ := server.snapshot()
			after, err := os.ReadFile(base[4])
			if code != 0 || !strings.Contains(strings.Join(commands, "\n"), "UID STORE") || err != nil || string(after) != saved {
				t.Fatalf("confirmed remote edit lost to unrelated save: exit=%d output=%s commands=%v state=%q error=%v", code, out, commands, after, err)
			}
		})
	}
}

func TestUnkeyedRemoteMutationsDoNotRequireStateWriter(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		input string
	}{
		{[]string{"draft", "create", "--to", "fixture@example.invalid", "--body", "one"}, ""},
		{[]string{"draft", "create-many", "--stdin"}, `[{"to":["fixture@example.invalid"],"body":"one"}]`},
		{[]string{"draft", "update", "--draft-id", "41", "--subject", "changed"}, ""},
		{[]string{"draft", "delete", "--draft-id", "41"}, ""},
		{[]string{"tag", "add", "--message-id", "imap:INBOX:41", "--tag", "reviewed"}, ""},
		{[]string{"tag", "remove", "--message-id", "imap:INBOX:41", "--tag", "reviewed"}, ""},
	} {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			saved := readonlyReceiptFixture(t, base, `{}`)
			code, out := runWorkflow(t, append(base, tc.args...), tc.input)
			commands, _ := server.snapshot()
			after, err := os.ReadFile(base[4])
			if code != 0 || len(commands) == 0 || err != nil || string(after) != saved {
				t.Fatalf("remote-only mutation required state writer: exit=%d output=%s commands=%v state=%q error=%v", code, out, commands, after, err)
			}
			if _, err := os.Stat(base[4] + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("remote-only mutation created a state lock: %v", err)
			}
		})
	}
}

func TestKeyedDraftStillRequiresDurableReceiptWithExistingLock(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
	}{
		{"single", "", []string{"draft", "create", "--to", "fixture@example.invalid", "--body", "one", "--idempotency-key", "fresh"}},
		{"command batch", `[{"to":["fixture@example.invalid"],"body":"one"}]`, []string{"draft", "create-many", "--stdin", "--idempotency-key", "fresh"}},
		{"item batch", `[{"to":["fixture@example.invalid"],"body":"one","idempotency_key":"fresh"}]`, []string{"draft", "create-many", "--stdin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			const saved = "{}\n"
			readonlyStateWithExistingLock(t, base, saved)
			code, out := runWorkflow(t, append(base, tc.args...), tc.input)
			commands, appended := server.snapshot()
			after, err := os.ReadFile(base[4])
			if code != 1 || !strings.Contains(out, "state_save_failed") || len(appended) != 0 || strings.Contains(strings.Join(commands, "\n"), " APPEND ") || err != nil || string(after) != saved {
				t.Fatalf("keyed mutation bypassed durable receipt: exit=%d output=%s commands=%v state=%q error=%v", code, out, commands, after, err)
			}
		})
	}
}

func TestRecoveredDraftItemsReportUnrecordedCommandKey(t *testing.T) {
	for _, existingLock := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-lock=%v", existingLock), func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			receipt := terminalReceiptFixture("imap.draft.create-item", `{"body":"one","subject":"","to":["fixture@example.invalid"]}`, `{"index":0,"ok":true,"draftId":"imap:Drafts:91"}`, "")
			records := `{"cached":` + receipt + `}`
			saved := `{"idempotency":` + records + `}`
			if existingLock {
				readonlyStateWithExistingLock(t, base, saved)
			} else {
				readonlyReceiptFixture(t, base, records)
			}
			args := append(append([]string(nil), base...), "draft", "create-many", "--stdin", "--idempotency-key", "new-command")
			input := `[{"to":["fixture@example.invalid"],"body":"one","idempotency_key":"cached"}]`
			code, out := runWorkflow(t, args, input)
			commands, _ := server.snapshot()
			after, err := os.ReadFile(base[4])
			if code != 1 || !strings.Contains(out, "not recorded") || !strings.Contains(out, `"draftId":"imap:Drafts:91"`) || len(commands) != 0 || err != nil || string(after) != saved {
				t.Fatalf("unrecordable command key: exit=%d output=%s commands=%v state=%q error=%v", code, out, commands, after, err)
			}
			if err := os.Chmod(filepath.Dir(base[4]), 0700); err != nil {
				t.Fatal(err)
			}
			code, out = runWorkflow(t, args, input)
			if code != 0 || !strings.Contains(out, "imap:Drafts:91") {
				t.Fatalf("recording restored command key: exit=%d output=%s", code, out)
			}
			code, out = runWorkflow(t, args, strings.Replace(input, "one", "changed", 1))
			commands, _ = server.snapshot()
			if code != 6 || !strings.Contains(out, "idempotency_conflict") || len(commands) != 0 {
				t.Fatalf("recorded command key did not bind manifest: exit=%d output=%s commands=%v", code, out, commands)
			}
		})
	}
}

func TestLocalMatchingReceiptsReplayFromReadonlyState(t *testing.T) {
	for _, mode := range []string{"follow-up", "draft batch", "send batch"} {
		t.Run(mode, func(t *testing.T) {
			cli := newRegressionCLI(t)
			code, out := cli.run("", "draft", "create", "--to", "recipient@example.invalid", "--body", "original")
			if code != 0 {
				t.Fatal(out)
			}
			draftID := regressionData(t, out)["draft"].(map[string]any)["id"].(string)
			var args []string
			input := ""
			switch mode {
			case "follow-up":
				code, out = cli.run("", "message", "send", "--draft-id", draftID, "--confirm-send", draftID)
				if code != 0 {
					t.Fatal(out)
				}
				messageID := regressionData(t, out)["message"].(map[string]any)["id"].(string)
				args = []string{"message", "follow-up", "--message-id", messageID, "--body", "reply", "--idempotency-key", "receipt"}
			case "draft batch":
				args = []string{"draft", "create-many", "--stdin"}
				input = `[{"to":["recipient@example.invalid"],"body":"reply","idempotency_key":"receipt"}]`
			case "send batch":
				args = []string{"message", "send-many", "--stdin"}
				input = fmt.Sprintf(`[{"draft_id":%q,"confirm_send":%q,"idempotency_key":"receipt"}]`, draftID, draftID)
			}
			code, out = cli.run(input, args...)
			if code != 0 {
				t.Fatalf("initial receipt: exit=%d output=%s", code, out)
			}
			first := regressionData(t, out)
			saved, err := os.ReadFile(cli.state)
			if err != nil {
				t.Fatal(err)
			}
			// The completed command no longer owns its sidecar. Use a separate
			// readable snapshot whose directory cannot publish a writer lock.
			dir := t.TempDir()
			cli.state = filepath.Join(dir, "state.json")
			if err := os.WriteFile(cli.state, saved, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			if file, err := os.CreateTemp(dir, "permission-probe-"); err == nil {
				file.Close()
				t.Skip("filesystem permits writes in a read-only directory")
			}
			code, out = cli.run(input, args...)
			after, err := os.ReadFile(cli.state)
			if code != 0 || !reflect.DeepEqual(regressionData(t, out), first) || err != nil || string(after) != string(saved) {
				t.Fatalf("matching local replay: exit=%d output=%s state unchanged=%t error=%v", code, out, string(after) == string(saved), err)
			}
			if mode == "follow-up" {
				args[len(args)-3] = "changed"
				code, out = cli.run(input, args...)
				if code != 6 || !strings.Contains(out, "idempotency_conflict") {
					t.Fatalf("changed local payload bypassed receipt matching: exit=%d output=%s", code, out)
				}
				args[len(args)-1] = "fresh"
				code, out = cli.run(input, args...)
				if code != 1 || !strings.Contains(out, `"code":"state_error"`) {
					t.Fatalf("fresh local follow-up bypassed write requirement: exit=%d output=%s", code, out)
				}
				args[len(args)-4] = "--body-file"
				args[len(args)-3] = filepath.Join(t.TempDir(), "missing-body.txt")
				code, out = cli.run(input, args...)
				if code != 2 || !strings.Contains(out, "validation_error") {
					t.Fatalf("offline input validation hidden by writer error: exit=%d output=%s", code, out)
				}
			} else {
				partial := strings.TrimSuffix(input, "]") + `,{}]`
				code, out = cli.run(partial, args...)
				if code != 10 || !strings.Contains(out, `"errorCode":"validation_error"`) {
					t.Fatalf("local partial replay exit changed: exit=%d output=%s", code, out)
				}
				code, out = cli.run(`[{}]`, args...)
				if code != 1 || !strings.Contains(out, `"errorCode":"validation_error"`) || strings.Contains(out, "state_error") {
					t.Fatalf("local all-invalid batch exit changed: exit=%d output=%s", code, out)
				}
			}
			if after, err := os.ReadFile(cli.state); err != nil || string(after) != string(saved) {
				t.Fatalf("read-only local checks changed state: %v", err)
			}
			if _, err := os.Stat(cli.state + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("read-only replay published a writer lock: %v", err)
			}
		})
	}
}
