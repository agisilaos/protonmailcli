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

func TestResolveMailboxQueryByIDAndName(t *testing.T) {
	boxes := []mailboxInfo{
		{ID: "inbox", Name: "INBOX", Kind: "system"},
		{ID: "all_mail", Name: "All Mail", Kind: "system"},
	}
	got, matchedBy, ambiguous, err := resolveMailboxQuery(boxes, "all_mail")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ambiguous) > 0 {
		t.Fatalf("unexpected ambiguous matches: %#v", ambiguous)
	}
	if got.Name != "All Mail" || matchedBy != "id_exact" {
		t.Fatalf("unexpected resolve result: %#v matchedBy=%s", got, matchedBy)
	}

	got, matchedBy, _, err = resolveMailboxQuery(boxes, "INBOX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "inbox" || matchedBy != "name_exact" {
		t.Fatalf("unexpected resolve result: %#v matchedBy=%s", got, matchedBy)
	}
}

func TestResolveMailboxQueryAmbiguousCasefold(t *testing.T) {
	boxes := []mailboxInfo{
		{ID: "sales", Name: "Sales", Kind: "custom"},
		{ID: "sales_team", Name: "SALES", Kind: "custom"},
	}
	_, _, ambiguous, err := resolveMailboxQuery(boxes, "SaLeS")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	if len(ambiguous) != 2 {
		t.Fatalf("expected 2 ambiguous matches got %d", len(ambiguous))
	}
}
