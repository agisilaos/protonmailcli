//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriterLockDoesNotPublishBeforeExistingStateIsReadable(t *testing.T) {
	path := physicalStatePath(t, filepath.Join(t.TempDir(), "state.json"))
	original := []byte(`{"tags":{"existing":"retained"}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	previous := openWriterFile
	openWriterFile = func(name string, flags int, mode os.FileMode) (*os.File, error) {
		if name == path {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
		}
		return previous(name, flags, mode)
	}
	t.Cleanup(func() { openWriterFile = previous })
	lock, err := New(path).LockWriter()
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("unreadable existing state must retain its permission error: %v", err)
	}
	if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("failed state access published a persistent lock: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
		t.Errorf("failed lock changed existing state: %q, %v", data, err)
	}
}

func TestWriterLockRetainsPermissionCauseWhenReadOnlyLockingFails(t *testing.T) {
	path := physicalStatePath(t, filepath.Join(t.TempDir(), "state.json"))
	for _, name := range []string{path, path + ".lock"} {
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	previousOpen, previousLock := openWriterFile, tryWriterFileLockFn
	t.Cleanup(func() { openWriterFile, tryWriterFileLockFn = previousOpen, previousLock })
	readOnlyOpened := false
	openWriterFile = func(name string, flags int, mode os.FileMode) (*os.File, error) {
		if name == path+".lock" {
			if flags&os.O_RDWR != 0 {
				return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
			}
			readOnlyOpened = true
		}
		return previousOpen(name, flags, mode)
	}
	tryWriterFileLockFn = func(*os.File) error { return syscall.EBADF }
	lock, err := New(path).LockWriter()
	if lock != nil {
		lock.Close()
	}
	if !readOnlyOpened || !errors.Is(err, os.ErrPermission) || !errors.Is(err, syscall.EBADF) {
		t.Fatalf("read-only exclusive-lock failure lost its permission cause: opened=%t err=%v", readOnlyOpened, err)
	}

	// Local filesystems may support flock on the same read-only descriptor.
	// Preserve that successful fallback when the operating system permits it.
	tryWriterFileLockFn = previousLock
	lock, err = New(path).LockWriter()
	if err != nil {
		t.Fatalf("working read-only locking fallback was rejected: %v", err)
	}
	lock.Close()
}
