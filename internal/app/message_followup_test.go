package app

import "testing"

func TestThreadHeaders(t *testing.T) {
	inReplyTo, refs := threadHeaders("<orig@example.com>", "<a@example.com> <b@example.com>")
	if inReplyTo != "<orig@example.com>" {
		t.Fatalf("unexpected inReplyTo: %q", inReplyTo)
	}
	if len(refs) != 3 {
		t.Fatalf("unexpected refs: %#v", refs)
	}
	if refs[2] != "<orig@example.com>" {
		t.Fatalf("expected original id at end: %#v", refs)
	}
}

func TestIMAPFollowUpRecipientsSentMessage(t *testing.T) {
	recipients := imapFollowUpRecipients("Me <me@example.com>", []string{"a@example.com"}, "me@example.com")
	if len(recipients) != 1 || recipients[0] != "a@example.com" {
		t.Fatalf("unexpected recipients: %#v", recipients)
	}
}

func TestIMAPFollowUpRecipientsInboxMessage(t *testing.T) {
	recipients := imapFollowUpRecipients("Alice <alice@example.com>", []string{"me@example.com"}, "me@example.com")
	if len(recipients) != 1 || recipients[0] != "alice@example.com" {
		t.Fatalf("unexpected recipients: %#v", recipients)
	}
}
