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

func sharedStateGroupFixture(t *testing.T, mode os.FileMode) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	directoryGroup := int(info.Sys().(*syscall.Stat_t).Gid)
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group == directoryGroup {
			continue
		}
		if err := os.Chown(path, -1, group); err != nil {
			continue
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path, group
	}
	t.Skip("fixture needs another group the current user may assign")
	return "", 0
}

func TestWriterLockPreservesSharedStateGroup(t *testing.T) {
	for _, mode := range []os.FileMode{0660, 0604} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%04o/existing=%v", mode, existing), func(t *testing.T) {
				path, group := sharedStateGroupFixture(t, mode)
				if existing {
					if err := os.WriteFile(path+".lock", nil, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(path+".lock", mode); err != nil {
						t.Fatal(err)
					}
				}
				link := linkWriterLock
				t.Cleanup(func() { linkWriterLock = link })
				linkWriterLock = func(source, destination string) error {
					info, err := os.Stat(source)
					if err != nil {
						return err
					}
					if int(info.Sys().(*syscall.Stat_t).Gid) != group || info.Mode().Perm() != mode {
						t.Errorf("published permissions before matching shared group: gid=%d mode=%04o want gid=%d mode=%04o", info.Sys().(*syscall.Stat_t).Gid, info.Mode().Perm(), group, mode)
					}
					return link(source, destination)
				}
				lock, err := New(path).LockWriter()
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				info, err := os.Stat(path + ".lock")
				if err != nil || int(info.Sys().(*syscall.Stat_t).Gid) != group || info.Mode().Perm() != mode {
					t.Fatalf("sidecar did not retain shared access: info=%v err=%v want group=%d", info, err, group)
				}
			})
		}
	}
}

func TestWriterGroupUpdateRejectsHardlinkedSidecar(t *testing.T) {
	path, _ := sharedStateGroupFixture(t, 0660)
	alias := filepath.Join(filepath.Dir(path), "retained-alias")
	if err := os.WriteFile(alias, nil, 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(alias, 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(alias, path+".lock"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := New(path).LockWriter(); err == nil {
		lock.Close()
		t.Fatal("accepted a sidecar alias needing a group change")
	}
	after, err := os.Stat(alias)
	if err != nil || after.Sys().(*syscall.Stat_t).Gid != before.Sys().(*syscall.Stat_t).Gid || after.Mode() != before.Mode() {
		t.Fatalf("group update changed an alias: before=%v after=%v error=%v", before, after, err)
	}
}

func TestEqualGroupAndOtherAccessDoesNotRequireGroupReassignment(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644, 0666} {
		t.Run(fmt.Sprintf("mode=%04o", mode), func(t *testing.T) {
			path, _ := sharedStateGroupFixture(t, mode)
			chown := chownWriterLock
			t.Cleanup(func() { chownWriterLock = chown })
			chownWriterLock = func(*os.File, int, int) error {
				t.Error("equal group and other permissions required a group change")
				return os.ErrPermission
			}
			lock, err := New(path).LockWriter()
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			info, err := os.Stat(path + ".lock")
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("sidecar permissions changed: %v, %v", info, err)
			}
		})
	}
}

func TestSharedGroupAssignmentFailureDoesNotPublishLock(t *testing.T) {
	path, _ := sharedStateGroupFixture(t, 0660)
	chown := chownWriterLock
	t.Cleanup(func() { chownWriterLock = chown })
	chownWriterLock = func(*os.File, int, int) error { return os.ErrPermission }
	if lock, err := New(path).LockWriter(); !errors.Is(err, os.ErrPermission) {
		if lock != nil {
			lock.Close()
		}
		t.Fatalf("did not retain failed group assignment: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("failed group assignment left a sidecar or staging file: %v, %v", entries, err)
	}
}
