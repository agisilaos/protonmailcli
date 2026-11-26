//go:build unix

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statePathThroughSymlinkParent(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	real := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(real, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	route := filepath.Join(root, "route")
	if err := os.Symlink(filepath.Join("nested", "child"), route); err != nil {
		t.Fatal(err)
	}
	return root, real, route + "/../state.json"
}

func TestStateCheckpointUsesResolvedSymlinkParent(t *testing.T) {
	root, real, path := statePathThroughSymlinkParent(t)
	const original = "unrelated lexical-parent state"
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	previous := syncStateDirectory
	t.Cleanup(func() { syncStateDirectory = previous })
	synced := false
	syncStateDirectory = func(dir *os.File) error {
		synced = true
		if !directoryFileIs(dir, real) {
			t.Errorf("checkpoint synced lexical parent instead of published parent: %s", dir.Name())
		}
		return previous(dir)
	}
	disk := New(path)
	state := emptyState()
	state.Tags["expected"] = "receipt"
	if err := disk.Save(state); err != nil {
		t.Fatal(err)
	}
	if !synced {
		t.Fatal("checkpoint did not sync parent")
	}
	actual, err := New(filepath.Join(real, "state.json")).Load()
	if err != nil || actual.Tags["expected"] != "receipt" {
		t.Fatalf("checkpoint not published at OS-resolved target: %+v %v", actual, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil || string(data) != original {
		t.Fatalf("lexical target changed: %q %v", data, err)
	}
}

func TestStateWriterStagesInResolvedParent(t *testing.T) {
	root, real, path := statePathThroughSymlinkParent(t)
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	if probe, err := os.CreateTemp(root, "permission-probe-"); err == nil {
		probe.Close()
		t.Skip("filesystem permits writes in read-only lexical parent")
	}
	disk := New(path)
	lock, err := disk.LockWriter()
	if err != nil {
		t.Fatalf("writer staged outside writable resolved parent: %v", err)
	}
	defer lock.Close()
	if err := disk.Save(emptyState()); err != nil {
		t.Fatalf("checkpoint staged outside writable resolved parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "state.json.lock")); err != nil {
		t.Fatalf("lock not in resolved parent: %v", err)
	}
}

func TestStateWriterCreatesMissingResolvedParent(t *testing.T) {
	root, real, path := statePathThroughSymlinkParent(t)
	path = strings.TrimSuffix(path, "state.json") + "fresh/state.json"
	disk := New(path)
	lock, err := disk.LockWriter()
	if err != nil {
		t.Fatalf("resolve missing parent after symlink/..: %v", err)
	}
	defer lock.Close()
	if err := disk.Save(emptyState()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(real, "fresh", "state.json")); err != nil {
		t.Fatalf("resolved checkpoint missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh")); !os.IsNotExist(err) {
		t.Fatalf("created lexical parent: %v", err)
	}
}

// Permission and replacement hooks target the actual pathname used by the OS,
// including hosts where the temporary directory itself contains a symlink.
func physicalStatePath(t *testing.T, path string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, filepath.Base(path))
}
