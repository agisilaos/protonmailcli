package app

import "testing"

func TestClassifyMailboxSystem(t *testing.T) {
	cases := map[string]string{
		"INBOX":     "inbox",
		"Drafts":    "drafts",
		"Sent Mail": "sent",
		"All Mail":  "all_mail",
		"Junk":      "spam",
	}
	for input, wantID := range cases {
		gotID, gotKind := classifyMailbox(input)
		if gotID != wantID {
			t.Fatalf("%s: id got=%s want=%s", input, gotID, wantID)
		}
		if gotKind != "system" {
			t.Fatalf("%s: expected system kind got=%s", input, gotKind)
		}
	}
}

func TestClassifyMailboxCustom(t *testing.T) {
	gotID, gotKind := classifyMailbox("Receipts 2026")
	if gotID != "receipts_2026" {
		t.Fatalf("id got=%s", gotID)
	}
	if gotKind != "custom" {
		t.Fatalf("kind got=%s", gotKind)
	}
}
