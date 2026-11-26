//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func rejectHardLinkPublication(t *testing.T) {
	t.Helper()
	previous := linkWriterLock
	linkWriterLock = func(source, destination string) error {
		return &os.LinkError{Op: "link", Old: source, New: destination, Err: syscall.EOPNOTSUPP}
	}
	t.Cleanup(func() { linkWriterLock = previous })
}

func TestWriterLockPublishesWithoutHardLinks(t *testing.T) {
	rejectHardLinkPublication(t)
	for _, mode := range []os.FileMode{0600, 0660} {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		first, err := New(path).LockWriter()
		if err != nil {
			t.Fatalf("filesystem without hard links must publish a writer lock: %v", err)
		}
		defer first.Close()
		info, err := os.Stat(path + ".lock")
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("published permissions=%v, want %04o: %v", info, mode, err)
		}
		if second, err := New(path).LockWriter(); !errors.Is(err, ErrBusy) {
			if second != nil {
				second.Close()
			}
			t.Fatalf("second writer bypassed fallback lock: %v", err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := New(path).LockWriter()
		if err != nil {
			t.Fatalf("released fallback lock did not remain reusable: %v", err)
		}
		next.Close()
		if staging, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state-lock-*")); err != nil || len(staging) != 0 {
			t.Fatalf("successful fallback left staging files: %v, %v", staging, err)
		}
	}
}

func TestWriterLockWithoutHardLinksKeepsCompetingPublisher(t *testing.T) {
	rejectHardLinkPublication(t)
	assertWriterLockConcurrentPublicationKeepsWinner(t)
}
