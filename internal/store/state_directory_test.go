//go:build !windows

package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func firstUseStateEntry(t *testing.T, name string, disk *Store) error {
	t.Helper()
	if name == "Save" {
		return disk.Save(emptyState())
	}
	lock, err := disk.LockWriter()
	if lock != nil {
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func TestFirstUseStateDirectorySyncsCreatedParentEntries(t *testing.T) {
	for _, entry := range []string{"LockWriter", "Save"} {
		t.Run(entry, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "new", "nested")
			path := filepath.Join(dir, "state.json")
			previous := syncStateDirectory
			t.Cleanup(func() { syncStateDirectory = previous })
			var synced []string
			syncStateDirectory = func(parent *os.File) error {
				name := parent.Name()
				for _, candidate := range []string{root, filepath.Join(root, "new"), dir} {
					if directoryFileIs(parent, candidate) {
						name = candidate
						break
					}
				}
				synced = append(synced, name)
				if !directoryFileIs(parent, dir) {
					for _, name := range []string{path, path + ".lock"} {
						if _, err := os.Stat(name); !os.IsNotExist(err) {
							t.Fatalf("state used before parent publication was durable: %s %v", name, err)
						}
					}
				}
				return previous(parent)
			}
			if err := firstUseStateEntry(t, entry, New(path)); err != nil {
				t.Fatal(err)
			}
			want := []string{root, filepath.Join(root, "new")}
			if entry == "Save" {
				want = append(want, dir)
			}
			last := -1
			for _, name := range want {
				next := slices.Index(synced[last+1:], name)
				if next < 0 {
					t.Fatalf("first-use parent entries not synced in order: got %v want %v", synced, want)
				}
				last += next + 1
			}
		})
	}
}

func TestFirstUseStateDirectorySyncFailureStopsAndCanRetry(t *testing.T) {
	for _, entry := range []string{"LockWriter", "Save"} {
		for _, level := range []string{"first", "nested"} {
			t.Run(entry+"/"+level, func(t *testing.T) {
				root := t.TempDir()
				first := filepath.Join(root, "new")
				dir := filepath.Join(first, "nested")
				path := filepath.Join(dir, "state.json")
				failureAt := root
				if level == "nested" {
					failureAt = first
				}
				previous := syncStateDirectory
				t.Cleanup(func() { syncStateDirectory = previous })
				failure := errors.New("synthetic new-directory sync failure")
				syncStateDirectory = func(parent *os.File) error {
					if directoryFileIs(parent, failureAt) {
						return failure
					}
					return previous(parent)
				}
				err := firstUseStateEntry(t, entry, New(path))
				if !errors.Is(err, failure) {
					t.Fatalf("acknowledged first-use state after parent sync failure: %v", err)
				}
				retriedSync := false
				syncStateDirectory = func(parent *os.File) error {
					if directoryFileIs(parent, failureAt) {
						retriedSync = true
					}
					return previous(parent)
				}
				if err := firstUseStateEntry(t, entry, New(path)); err != nil {
					t.Fatalf("retry after failed directory initialization: %v", err)
				}
				if !retriedSync {
					t.Fatal("retry bypassed the unconfirmed parent sync")
				}
			})
		}
	}
}

func TestExistingStateDirectoryDoesNotRequireAncestorSync(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "shared")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Existing shared-directory users need only search access to its ancestors.
	if err := os.Chmod(root, 0100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	previous := syncStateDirectory
	t.Cleanup(func() { syncStateDirectory = previous })
	syncStateDirectory = func(parent *os.File) error {
		if !directoryFileIs(parent, dir) {
			t.Fatalf("preexisting directory required ancestor sync: %s", parent.Name())
		}
		return previous(parent)
	}
	disk := New(filepath.Join(dir, "state.json"))
	var lock io.Closer
	lock, err := disk.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := disk.Save(emptyState()); err != nil {
		t.Fatal(err)
	}
}

func TestFirstUseDirectorySyncFailurePreservesConcurrentContents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "new")
	path := filepath.Join(dir, "state.json")
	other := filepath.Join(dir, "other-caller.txt")
	previous := syncStateDirectory
	t.Cleanup(func() { syncStateDirectory = previous })
	failure := errors.New("synthetic directory sync failure")
	syncStateDirectory = func(parent *os.File) error {
		if !directoryFileIs(parent, root) {
			return previous(parent)
		}
		// A shared initializer can have used the directory while sync was pending.
		if err := os.WriteFile(other, []byte("retain concurrent content"), 0600); err != nil {
			t.Fatal(err)
		}
		return failure
	}
	_, err := New(path).LockWriter()
	if !errors.Is(err, failure) {
		t.Fatalf("lost sync or cleanup failure: %v", err)
	}
	data, readErr := os.ReadFile(other)
	if readErr != nil || string(data) != "retain concurrent content" {
		t.Fatalf("cleanup removed concurrent content: %q %v", data, readErr)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("failed initialization published lock: %v", err)
	}
}

func directoryFileIs(file *os.File, path string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	named, err := os.Stat(path)
	return err == nil && os.SameFile(opened, named)
}
