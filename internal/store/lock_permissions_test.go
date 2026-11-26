//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestWriterLockPreservesSharedStateAccessUnderPrivateUmask(t *testing.T) {
	if path := os.Getenv("PMAIL_TEST_SHARED_LOCK_STATE"); path != "" {
		syscall.Umask(0077)
		if _, err := New(path).LockWriter(); err != nil {
			t.Fatal(err)
		}
		// An exited owner must not leave the persistent sidecar locked.
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"tags":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWriterLockPreservesSharedStateAccessUnderPrivateUmask$")
	child.Env = append(os.Environ(), "PMAIL_TEST_SHARED_LOCK_STATE="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("lock under private umask: %v\n%s", err, out)
	}
	info, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("shared-state writer lock excludes group access: permissions=%04o, want 0660", info.Mode().Perm())
	}
	lock, err := New(path).LockWriter()
	if err != nil {
		t.Fatalf("exited writer left a stale lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriterLockKeepsPrivateDefaultsAndExistingAccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stateMode os.FileMode
		lockMode  os.FileMode
		want      os.FileMode
	}{
		{"new private state", 0, 0, 0600},
		{"existing private state", 0600, 0, 0600},
		{"group-readable state", 0640, 0, 0640},
		{"legacy private lock for shared state", 0660, 0600, 0660},
		{"state access tightened", 0600, 0660, 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			for name, mode := range map[string]os.FileMode{path: tc.stateMode, path + ".lock": tc.lockMode} {
				if mode != 0 {
					if err := os.WriteFile(name, nil, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(name, mode); err != nil {
						t.Fatal(err)
					}
				}
			}
			first, err := New(path).LockWriter()
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			before, err := os.Stat(path + ".lock")
			if err != nil {
				t.Fatal(err)
			}
			if before.Mode().Perm() != tc.want {
				t.Fatalf("lock permissions=%04o, want %04o", before.Mode().Perm(), tc.want)
			}
			if second, err := New(path).LockWriter(); !errors.Is(err, ErrBusy) {
				if second != nil {
					second.Close()
				}
				t.Fatalf("concurrent writer must be busy: %v", err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := New(path).LockWriter()
			if err != nil {
				t.Fatalf("lock remained busy after release: %v", err)
			}
			defer next.Close()
			after, err := os.Stat(path + ".lock")
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("writer replaced persistent lock inode: %v", err)
			}
			if state, err := os.Stat(path); tc.stateMode == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("lock created state: %v", err)
				}
			} else if err != nil || state.Mode().Perm() != tc.stateMode {
				t.Fatalf("lock altered state permissions: %v, %v", state, err)
			}
		})
	}
}

func TestWriterLockRejectsAliasesWithoutChangingTarget(t *testing.T) {
	for _, link := range []struct {
		name   string
		create func(string, string) error
	}{
		{"symbolic link", os.Symlink},
		{"hard link", os.Link},
	} {
		t.Run(link.name, func(t *testing.T) {
			dir := t.TempDir()
			state, target := filepath.Join(dir, "state.json"), filepath.Join(dir, "private-target")
			if err := os.WriteFile(state, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(state, 0660); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := link.create(target, state+".lock"); err != nil {
				t.Fatal(err)
			}
			if lock, err := New(state).LockWriter(); err == nil {
				lock.Close()
				t.Fatal("aliased sidecar accepted")
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0600 || info.Size() != 0 {
				t.Fatalf("lock setup changed private target: %v, %v", info, err)
			}
		})
	}
}

func TestWriterLockRejectsHardLinkedStateTargets(t *testing.T) {
	dir := t.TempDir()
	state, alias := filepath.Join(dir, "state.json"), filepath.Join(dir, "alias.json")
	original := []byte(`{"idempotency":{"receipt":{"operation":"message.send"}}}`)
	if err := os.WriteFile(state, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(state, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, alias} {
		if lock, err := New(path).LockWriter(); err == nil {
			lock.Close()
			t.Errorf("hardlinked state %q received an independent writer lock", path)
		}
		if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
			t.Errorf("rejected state created a lock file: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(original) {
			t.Errorf("state alias changed: %q, %v", data, err)
		}
	}
}

func TestWriterLockCreationInterruptedBeforePermissions(t *testing.T) {
	if path := os.Getenv("PMAIL_TEST_INTERRUPTED_LOCK_STATE"); path != "" {
		syscall.Umask(0077)
		chmodWriterLock = func(*os.File, os.FileMode) error {
			os.Exit(23)
			return nil
		}
		if _, err := New(path).LockWriter(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("expected interruption at the filesystem permission boundary")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWriterLockCreationInterruptedBeforePermissions$")
	child.Env = append(os.Environ(), "PMAIL_TEST_INTERRUPTED_LOCK_STATE="+path)
	if out, err := child.CombinedOutput(); err == nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 23 {
		t.Fatalf("unexpected child result: %v\n%s", err, out)
	}
	if info, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("interrupted creator published an unfinished lock: %v, %v", info, err)
	}
	lock, err := New(path).LockWriter()
	if err != nil {
		t.Fatalf("interrupted staging prevented the next writer: %v", err)
	}
	defer lock.Close()
	info, err := os.Stat(path + ".lock")
	if err != nil || info.Mode().Perm() != 0660 {
		t.Fatalf("replacement creator did not publish shared access: %v, %v", info, err)
	}
}

func TestWriterLockAcceptsPublishedStagingAliasWithoutChmod(t *testing.T) {
	dir := t.TempDir()
	state, staging := filepath.Join(dir, "state.json"), filepath.Join(dir, ".state-lock-interrupted")
	for _, path := range []string{state, staging} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0660); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(staging, state+".lock"); err != nil {
		t.Fatal(err)
	}
	previous := chmodWriterLock
	chmodWriterLock = func(*os.File, os.FileMode) error { return errors.New("must not chmod a published alias") }
	t.Cleanup(func() { chmodWriterLock = previous })
	lock, err := New(state).LockWriter()
	if err != nil {
		t.Fatalf("published lock with abandoned staging alias must remain usable: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staging); err != nil {
		t.Fatal(err)
	}
	lock, err = New(state).LockWriter()
	if err != nil {
		t.Fatalf("removing staging alias invalidated published lock: %v", err)
	}
	lock.Close()
}

func TestWriterLockConcurrentPublicationKeepsWinner(t *testing.T) {
	assertWriterLockConcurrentPublicationKeepsWinner(t)
}

func assertWriterLockConcurrentPublicationKeepsWinner(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	prepared, resume := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	previous := chmodWriterLock
	var calls atomic.Int32
	chmodWriterLock = func(file *os.File, mode os.FileMode) error {
		if calls.Add(1) == 1 {
			close(prepared)
			<-resume
		}
		return file.Chmod(mode)
	}
	t.Cleanup(func() { chmodWriterLock = previous })
	result := make(chan error, 1)
	go func() {
		lock, err := New(path).LockWriter()
		if lock != nil {
			lock.Close()
		}
		result <- err
	}()
	select {
	case <-prepared:
	case <-time.After(5 * time.Second):
		t.Fatal("first creator did not reach permission setup")
	}
	winner, err := New(path).LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer winner.Close()
	before, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case err := <-result:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("competing creator bypassed the published lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("competing creator did not finish")
	}
	after, err := os.Stat(path + ".lock")
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0660 {
		t.Fatalf("competing creator replaced the winner or changed access: %v, %v", after, err)
	}
}
