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

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func TestPendingSendSourceSurvivesProcessExit(t *testing.T) {
	if raw := os.Getenv("PMAIL_PENDING_SEND_CHILD"); raw != "" {
		var request struct {
			Args  []string
			Input string
		}
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
		disk := store.New(request.Args[4])
		app := App{Stdin: strings.NewReader(request.Input), Stdout: io.Discard, Stderr: io.Discard, checkpoint: func(st model.State) error {
			if st.Idempotency["send-key"].Status == "complete" {
				os.Exit(0)
			}
			return disk.Save(st)
		}}
		code := app.run(request.Args)
		t.Fatalf("child did not exit after SMTP acceptance: code=%d", code)
	}
	for _, command := range []string{"send", "send-many"} {
		for _, inputID := range []string{"41", "imap:Drafts:41"} {
			t.Run(command+"/"+inputID, func(t *testing.T) {
				peer := &workflowServer{draftBox: "Brouillons"}
				base := imapCommandArgs(t, newWorkflowServer(t, peer))
				delivered := captureWorkflowSMTP(t, base)
				args := append(base, "message", command)
				input := ""
				if command == "send" {
					args = append(args, "--draft-id", inputID, "--confirm-send", inputID, "--idempotency-key", "send-key")
				} else {
					args = append(args, "--stdin")
					input = fmt.Sprintf(`[{"draft_id":%q,"confirm_send":%q,"idempotency_key":"send-key"}]`, inputID, inputID)
				}
				request, err := json.Marshal(struct {
					Args  []string
					Input string
				}{args, input})
				if err != nil {
					t.Fatal(err)
				}
				child := exec.Command(os.Args[0], "-test.run=^TestPendingSendSourceSurvivesProcessExit$")
				child.Env = append(os.Environ(), "PMAIL_PENDING_SEND_CHILD="+string(request))
				if out, err := child.CombinedOutput(); err != nil {
					t.Fatalf("child failed: %v %s", err, out)
				}
				state, err := store.New(base[4]).Load()
				if err != nil {
					t.Fatal(err)
				}
				before, _ := peer.snapshot()
				t.Setenv("PMAIL_SMTP_PASSWORD", "")
				code, out := runWorkflow(t, args, input)
				after, _ := peer.snapshot()
				record := state.Idempotency["send-key"]
				if record.Status != "pending" || record.SourceID != "imap:Brouillons:41" || code != 4 || !strings.Contains(out, "imap_send_uncertain") || !strings.Contains(out, "imap:Brouillons:41") || len(delivered()) != 1 || len(after) != len(before) {
					t.Fatalf("interrupted send lost resolved source: record=%+v exit=%d sent=%d commands=%d/%d out=%s", record, code, len(delivered()), len(before), len(after), out)
				}
			})
		}
	}
}

func TestLegacyPendingSendRecoveryDoesNotInventSource(t *testing.T) {
	for _, sourceID := range []string{"", "unqualified-old-value"} {
		t.Run(sourceID, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			saved := readonlyReceiptFixture(t, base, fmt.Sprintf(`{"old":{"operation":"message.send","status":"pending","sourceId":%q}}`, sourceID))
			var out bytes.Buffer
			code := Run(append(base, "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "old"), strings.NewReader(""), &out, io.Discard)
			if code != 4 || !strings.Contains(out.String(), "imap_send_uncertain") || !strings.Contains(out.String(), "unfinished recovery record") || strings.Contains(out.String(), "imap:Drafts") || strings.Contains(out.String(), "unqualified-old-value") {
				t.Fatalf("legacy pending recovery invented a target: exit=%d out=%s", code, out.String())
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, peer)
		})
	}
}
