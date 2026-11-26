package app

import (
	"strings"
	"testing"
)

func TestMailboxActionListTypedResponse(t *testing.T) {
	boxes := []mailboxInfo{{ID: "inbox", Name: "INBOX", Kind: "system"}}
	data, changed, err := mailboxAction("list", nil, boxes, "local")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatalf("list should not mutate state")
	}
	resp, ok := data.(mailboxListResponse)
	if !ok {
		t.Fatalf("expected mailboxListResponse, got %T", data)
	}
	if resp.Count != 1 || resp.Mailboxes[0].ID != "inbox" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestMailboxActionResolveTypedResponse(t *testing.T) {
	boxes := []mailboxInfo{{ID: "all_mail", Name: "All Mail", Kind: "system"}}
	data, changed, err := mailboxAction("resolve", []string{"--name", "all_mail"}, boxes, "imap")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatalf("resolve should not mutate state")
	}
	resp, ok := data.(mailboxResolveResponse)
	if !ok {
		t.Fatalf("expected mailboxResolveResponse, got %T", data)
	}
	if resp.MatchedBy != "id_exact" || resp.Source != "imap" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestMailboxAmbiguityHintQuotesExactShellArguments(t *testing.T) {
	boxes := []mailboxInfo{{ID: "cost_usd", Name: "Cost $USD's"}, {ID: "cost_usd", Name: "Cost-USD"}}
	_, _, err := mailboxAction("resolve", []string{"--name", "cost_usd"}, boxes, "imap")
	failure, ok := err.(cliError)
	if !ok || !strings.Contains(failure.hint, `'Cost $USD'\''s'`) || !strings.Contains(failure.hint, "'Cost-USD'") {
		t.Fatalf("hint must preserve each name when pasted into a shell: %v (%v)", failure.hint, err)
	}
}
