package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriterRejectsReplacedDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "active")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	disk := New(filepath.Join(parent, "state.json"))
	if err := disk.Save(emptyState()); err != nil {
		t.Fatal(err)
	}
	writer, err := disk.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Skipf("platform prevents renaming locked directory: %v", err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"tags\":{\"second\":\"profile\"}}")
	if err := os.WriteFile(disk.path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Load(); err == nil {
		t.Error("loaded replacement directory")
	}
	if err := writer.Save(emptyState()); err == nil {
		t.Error("wrote replacement directory")
	}
	actual, err := os.ReadFile(disk.path)
	if err != nil || string(actual) != string(original) {
		t.Errorf("replacement state overwritten: %s %v", actual, err)
	}
}
