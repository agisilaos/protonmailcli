package app

import (
	"strings"
	"testing"
)

func TestLegacyTerminalDraftFailuresUseCurrentRecoveryGuidance(t *testing.T) {
	const payload = `{"body":"one","subject":"","to":["synthetic@example.invalid"]}`
	const manifest = `[{"to":["synthetic@example.invalid"],"subject":"","body":"one"}]`
	const failure = `{"exit":4,"code":"imap_draft_create_uncertain","message":"fallback move failed","hint":"Inspect Drafts before retrying. Keep receipt old."}`
	for _, mode := range []string{"single", "item", "command"} {
		t.Run(mode, func(t *testing.T) {
			peer := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, peer))
			operation, request, response, savedFailure := "draft.create", payload, "null", failure
			args := []string{"draft", "create", "--to", "synthetic@example.invalid", "--body", "one", "--idempotency-key", "old"}
			input := ""
			switch mode {
			case "item":
				operation = "imap.draft.create-item"
				args = []string{"draft", "create-many", "--stdin"}
				input = `[{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"old"}]`
			case "command":
				operation, request = "draft.create-many", manifest
				args = []string{"draft", "create-many", "--stdin", "--idempotency-key", "old"}
				input, savedFailure = manifest, ""
				response = `{"results":[{"index":0,"ok":false,"errorCode":"imap_draft_create_uncertain","error":"fallback move failed. Inspect Drafts before retrying."}],"count":1,"success":0,"failed":1,"source":"imap"}`
			}
			receipt := terminalReceiptFixture(operation, request, response, savedFailure)
			saved := readonlyReceiptFixture(t, base, `{"old":`+receipt+`}`)
			code, out := runWorkflow(t, append(base, args...), input)
			if code != 4 || !strings.Contains(out, "imap_draft_create_uncertain") || !strings.Contains(out, "fallback move failed") || !strings.Contains(out, "Inspect INBOX and Drafts before retrying") {
				t.Fatalf("legacy failure omitted fallback location: exit=%d out=%s", code, out)
			}
			if mode == "single" && !strings.Contains(out, "Keep receipt old") {
				t.Fatalf("lost receipt detail: %s", out)
			}
			assertReadonlyReceiptUnchanged(t, base[4], saved, peer)
		})
	}
}
