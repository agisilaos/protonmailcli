package store

import (
	"os"
	"path/filepath"
	"testing"

	"protonmailcli/internal/model"
)

func TestLoadCreatesEmptyStateFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "state.json")
	s := New(path)

	st, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if st.Drafts == nil || st.Messages == nil || st.Tags == nil || st.Filters == nil || st.Idempotency == nil {
		t.Fatalf("expected initialized maps: %+v", st)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected state file to be created: %v", err)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "state.json")
	s := New(path)

	in := model.State{
		Drafts: map[string]model.Draft{
			"d_1": {ID: "d_1", Subject: "hello"},
		},
		Messages: map[string]model.Message{
			"m_1": {ID: "m_1", Subject: "world"},
		},
		Tags: map[string]string{
			"finance": "t_1",
		},
		Filters: map[string]model.Filter{
			"f_1": {ID: "f_1", Name: "invoices"},
		},
		Idempotency: map[string]model.IdempotencyRecord{
			"k": {Operation: "draft.create", PayloadHash: "h1"},
		},
	}
	if err := s.Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Drafts["d_1"].Subject != "hello" || got.Messages["m_1"].Subject != "world" {
		t.Fatalf("unexpected state payload: %+v", got)
	}
	if got.Tags["finance"] != "t_1" || got.Filters["f_1"].Name != "invoices" {
		t.Fatalf("unexpected loaded maps: %+v", got)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	_, err := New(path).Load()
	if err == nil {
		t.Fatal("expected decode error")
	}
}
