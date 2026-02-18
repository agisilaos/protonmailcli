package app

import "testing"

func TestResolveManifestInput(t *testing.T) {
	if _, err := resolveManifestInput("", false); err == nil {
		t.Fatal("expected missing input error")
	}
	if _, err := resolveManifestInput("a.json", true); err == nil {
		t.Fatal("expected exclusivity error")
	}
	got, err := resolveManifestInput("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "-" {
		t.Fatalf("unexpected manifest input: %s", got)
	}
}

func TestParseRequiredUID(t *testing.T) {
	if _, err := parseRequiredUID("", "--draft-id"); err == nil {
		t.Fatal("expected validation error")
	}
	uid, err := parseRequiredUID("imap:Drafts:123", "--draft-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != "123" {
		t.Fatalf("unexpected uid: %s", uid)
	}
}

func TestParseDateInput(t *testing.T) {
	if _, ok, err := parseDateInput("2026-02-01"); err != nil || !ok {
		t.Fatalf("expected valid YYYY-MM-DD date, got ok=%v err=%v", ok, err)
	}
	if _, ok, err := parseDateInput("2026-02-01T10:00:00Z"); err != nil || !ok {
		t.Fatalf("expected valid RFC3339 date, got ok=%v err=%v", ok, err)
	}
	if _, _, err := parseDateInput("2026-99-99"); err == nil {
		t.Fatal("expected invalid date error")
	}
}
