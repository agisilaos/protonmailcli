package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestKeyedDraftRecovery(t *testing.T) {
	for _, behavior := range []string{"missing-uid", "lost-completion", "confirmed", "mixed"} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/batch=%v", behavior, batch), func(t *testing.T) {
				port, count := draftIMAPServer(t, behavior)
				args := imapCommandArgs(t, port)
				args = append(args, "draft")
				input := ""
				expectedCount := int32(1)
				if batch {
					args = append(args, "create-many", "--stdin")
					input = `[{"to":["synthetic@example.invalid"],"body":"one"},{"to":["synthetic@example.invalid"],"body":"two"}]`
					if behavior != "lost-completion" {
						expectedCount = 2
					} else {
						input = `[{"to":["synthetic@example.invalid"],"body":"one"}]`
					}
				} else {
					args = append(args, "create", "--to", "synthetic@example.invalid", "--body", "one")
				}
				args = append(args, "--idempotency-key", "durable-key")
				previous := smtpSendFn
				t.Cleanup(func() { smtpSendFn = previous })
				smtpCalls := 0
				smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error { smtpCalls++; return fmt.Errorf("unexpected fallback") }
				var out bytes.Buffer
				first := Run(args, strings.NewReader(input), &out, io.Discard)
				expectedExit := 4
				if behavior == "confirmed" {
					expectedExit = 0
				}
				if behavior == "mixed" {
					if batch {
						expectedExit = 10
					} else {
						expectedExit = 0
					}
				}
				if first != expectedExit || count.Load() != expectedCount {
					t.Fatalf("first exit=%d appends=%d output=%s", first, count.Load(), out.String())
				}
				st, err := store.New(args[4]).Load()
				if err != nil {
					t.Fatal(err)
				}
				if st.Idempotency["durable-key"].Status != "complete" {
					t.Fatalf("missing terminal record: %+v", st.Idempotency)
				}
				// An unusable server after the first run proves replay precedes connection.
				t.Setenv("PMAIL_SMTP_PASSWORD", "")
				out.Reset()
				second := Run(args, strings.NewReader(input), &out, io.Discard)
				if second != first || count.Load() != expectedCount || smtpCalls != 0 {
					t.Fatalf("replay exit=%d want=%d appends=%d smtp=%d output=%s", second, first, count.Load(), smtpCalls, out.String())
				}
			})
		}
	}
}

func TestDraftCheckpointFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		for _, behavior := range []string{"missing-uid", "confirmed"} {
			t.Run(fmt.Sprintf("%d/%s", failAt, behavior), func(t *testing.T) {
				port, count := draftIMAPServer(t, behavior)
				args := imapCommandArgs(t, port)
				disk := store.New(args[4])
				args = append(args, "draft", "create", "--to", "synthetic@example.invalid", "--body", "one", "--idempotency-key", "key")
				saves := 0
				var out bytes.Buffer
				a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(""), checkpoint: func(st model.State) error {
					saves++
					if saves == failAt {
						return fmt.Errorf("injected checkpoint failure")
					}
					return disk.Save(st)
				}}
				exit := a.run(args)
				if failAt == 1 {
					if exit != 1 || count.Load() != 0 {
						t.Fatalf("pre-dispatch failure exit=%d appends=%d", exit, count.Load())
					}
					return
				}
				if exit != 4 || count.Load() != 1 {
					t.Fatalf("post-dispatch failure exit=%d appends=%d", exit, count.Load())
				}
				st, err := disk.Load()
				if err != nil || st.Idempotency["key"].Status != "pending" {
					t.Fatalf("pending on disk: %+v %v", st, err)
				}
				out.Reset()
				if next := Run(args, strings.NewReader(""), &out, io.Discard); next != 4 || count.Load() != 1 || !strings.Contains(out.String(), "unfinished recovery record") {
					t.Fatalf("unsafe replay: exit=%d appends=%d output=%s", next, count.Load(), out.String())
				}
			})
		}
	}
}

func TestInterruptedDraftKeepsPendingIntent(t *testing.T) {
	if raw := os.Getenv("PMAIL_RECOVERY_CHILD"); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatal(err)
		}
		Run(args, strings.NewReader(""), io.Discard, io.Discard)
		return
	}
	port, count := draftIMAPServer(t, "hold")
	args := append(imapCommandArgs(t, port), "draft", "create", "--to", "synthetic@example.invalid", "--body", "one", "--idempotency-key", "interrupted")
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestInterruptedDraftKeepsPendingIntent$")
	cmd.Env = append(os.Environ(), "PMAIL_RECOVERY_CHILD="+string(raw))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(2 * time.Second)
	for count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond * 10)
	}
	if count.Load() != 1 {
		t.Fatal("child did not reach accepted APPEND")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	var out bytes.Buffer
	if exit := Run(args, strings.NewReader(""), &out, io.Discard); exit != 4 || count.Load() != 1 || !strings.Contains(out.String(), "unfinished recovery record") {
		t.Fatalf("interrupted replay exit=%d appends=%d output=%s", exit, count.Load(), out.String())
	}
}

func TestLegacyIdempotencyAndUnknownStatus(t *testing.T) {
	st := model.State{}
	payload := map[string]string{"body": "one"}
	if err := idempotencyStore(&st, "key", "draft.create", payload, map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if found, _, err := idempotencyLookup(&st, "key", "draft.create", payload); !found || err != nil {
		t.Fatalf("legacy replay %v %v", found, err)
	}
	if _, _, err := idempotencyLookup(&st, "key", "draft.create", map[string]string{"body": "two"}); err == nil {
		t.Fatal("conflict allowed")
	}
	rec := st.Idempotency["key"]
	rec.Status = "future-state"
	st.Idempotency["key"] = rec
	if _, _, err := idempotencyLookup(&st, "key", "draft.create", payload); err == nil {
		t.Fatal("unknown status allowed")
	}
}
