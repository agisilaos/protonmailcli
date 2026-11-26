//go:build unix

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

// Model a directory swap after Save's initial check, before its staging work.
func TestPinnedCheckpointUsesHeldDirectoryAfterReplacement(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	disk := New(filepath.Join(parent, "state.json"))
	writer, err := disk.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("unrelated profile")
	if err := os.WriteFile(disk.path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writer.Store.replacePinnedState([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(disk.path); err != nil || string(actual) != string(original) {
		t.Fatalf("checkpoint followed replacement: %q %v", actual, err)
	}
	if actual, err := os.ReadFile(filepath.Join(parent+"-original", "state.json")); err != nil || string(actual) != "{}" {
		t.Fatalf("original directory missing checkpoint: %q %v", actual, err)
	}
}

func TestPinnedLoadRejectsChangedFinalEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	disk := New(path)
	if err := disk.Save(emptyState()); err != nil {
		t.Fatal(err)
	}
	writer, err := disk.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state.json.original", path); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Load(); err == nil {
		t.Fatal("pinned writer followed a replaced final symlink")
	}
}
