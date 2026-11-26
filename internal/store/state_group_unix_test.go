//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCheckpointPreservesSharedStateGroupForNextWriter(t *testing.T) {
	for _, mode := range []os.FileMode{0660, 0604} {
		t.Run(fmt.Sprintf("mode=%04o", mode), func(t *testing.T) {
			path, group := sharedStateGroupFixture(t, mode)
			disk := New(path)
			lock, err := disk.LockWriter()
			if err != nil {
				t.Fatal(err)
			}
			state := emptyState()
			state.Tags["checkpoint"] = "retained"
			saveErr := disk.Save(state)
			lock.Close()
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode || int(info.Sys().(*syscall.Stat_t).Gid) != group {
				t.Errorf("checkpoint changed existing shared access: mode=%04o group=%d, want mode=%04o group=%d", info.Mode().Perm(), info.Sys().(*syscall.Stat_t).Gid, mode, group)
			}
			previous := chownWriterLock
			t.Cleanup(func() { chownWriterLock = previous })
			// Model a shared sidecar owned by another writer: this caller can lock it
			// but cannot change its group. No secondary account is needed.
			chownWriterLock = func(*os.File, int, int) error { return os.ErrPermission }
			next, err := New(path).LockWriter()
			if next != nil {
				next.Close()
			}
			if err != nil {
				t.Fatalf("checkpoint forced the next writer to reassign another owner's sidecar: %v", err)
			}
		})
	}
}

func TestCheckpointGroupFailurePreservesPreviousState(t *testing.T) {
	path, _ := sharedStateGroupFixture(t, 0660)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := chownStateFile
	t.Cleanup(func() { chownStateFile = previous })
	chownStateFile = func(file *os.File, uid, gid int) error {
		staged, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if staged.Size() != 0 || staged.Mode().Perm() != 0600 {
			t.Errorf("group assignment occurred after contents/shared permissions: %v", staged)
		}
		return os.ErrPermission
	}
	err = New(path).Save(emptyState())
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("group failure did not prevent publication: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(info, after) {
		t.Fatalf("group failure replaced previous state: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(before) {
		t.Fatalf("group failure changed previous contents: %q %v", data, err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("group failure leaked staging: %v %v", files, err)
	}
}

func TestCheckpointSkipsUnnecessaryGroupReassignment(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644, 0666} {
		t.Run(fmt.Sprintf("mode=%04o", mode), func(t *testing.T) {
			path, _ := sharedStateGroupFixture(t, mode)
			previous := chownStateFile
			t.Cleanup(func() { chownStateFile = previous })
			chownStateFile = func(*os.File, int, int) error {
				t.Error("equal group/other access required group reassignment")
				return os.ErrPermission
			}
			if err := New(path).Save(emptyState()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckpointRetainsAlreadyMatchingSharedGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	previous := chownStateFile
	t.Cleanup(func() { chownStateFile = previous })
	chownStateFile = func(*os.File, int, int) error {
		t.Error("matching inherited group required reassignment")
		return os.ErrPermission
	}
	if err := New(path).Save(emptyState()); err != nil {
		t.Fatal(err)
	}
}
