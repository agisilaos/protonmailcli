package app

import (
	"strings"
	"testing"
)

func TestBuildIMAPCriteriaDatesAndFields(t *testing.T) {
	criteria, err := buildIMAPCriteria("invoice", "billing@example.com", "me@example.com", "2026-01-01", "2026-02-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if criteria == "ALL" {
		t.Fatalf("expected non-ALL criteria")
	}
	for _, want := range []string{"TEXT \"invoice\"", "FROM \"billing@example.com\"", "TO \"me@example.com\"", "SINCE 01-Jan-2026", "BEFORE 01-Feb-2026"} {
		if !strings.Contains(criteria, want) {
			t.Fatalf("criteria missing %q: %s", want, criteria)
		}
	}
}

func TestBuildIMAPCriteriaInvalidDate(t *testing.T) {
	_, err := buildIMAPCriteria("", "", "", "2026-99-99", "")
	if err == nil {
		t.Fatalf("expected error")
	}
}
