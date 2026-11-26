package store

import (
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"protonmailcli/internal/model"
)

func checkStateCheckpointWithOpenSnapshot(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "state.json")
	s := New(path)
	st := emptyState()
	st.Tags["version"] = "old"
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openStateSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	st.Tags["version"] = "new"
	if err := s.Save(st); err != nil {
		t.Fatalf("checkpoint with an active reader: %v", err)
	}
	oldBytes, err := io.ReadAll(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var old model.State
	if err := json.Unmarshal(oldBytes, &old); err != nil || old.Tags["version"] != "old" {
		t.Fatalf("active reader got version %q, %v", old.Tags["version"], err)
	}
	current, err := s.Load()
	if err != nil || current.Tags["version"] != "new" {
		t.Fatalf("new reader got version %q, %v", current.Tags["version"], err)
	}
}
