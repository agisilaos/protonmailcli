//go:build unix || windows

package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriterLockRequiresEmptyRegularSidecar(t *testing.T) {
	for _, kind := range []string{"nonempty", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			sidecar, target := path+".lock", path+".other"
			switch kind {
			case "nonempty":
				if err := os.WriteFile(sidecar, []byte("existing data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				if kind == "symlink" {
					if err := os.WriteFile(target, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, sidecar); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("cannot create symlink fixture: %v", err)
					}
					t.Fatal(err)
				}
			}
			lock, err := New(path).LockWriter()
			if err == nil {
				lock.Close()
				t.Fatalf("accepted %s writer lock", kind)
			}
			if kind == "dangling-symlink" {
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dangling target was created: %v", err)
				}
			}
		})
	}
}

func TestWriterLockUsesExistingSharedMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACLs")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	lock, err := New(path).LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	info, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("lock mode=%04o want 0660", info.Mode().Perm())
	}
}

func TestWriterLockRetainsExclusionAfterLocalContention(t *testing.T) {
	const pathEnv = "PMAIL_TEST_CONTENDED_STATE"
	if path := os.Getenv(pathEnv); path != "" {
		if lock, err := New(path).LockWriter(); !errors.Is(err, ErrBusy) {
			if err == nil {
				lock.Close()
			}
			t.Fatalf("separate writer after local contention: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := New(path).LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if other, err := New(path).LockWriter(); !errors.Is(err, ErrBusy) {
		if err == nil {
			other.Close()
		}
		t.Fatalf("second local writer: %v", err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWriterLockRetainsExclusionAfterLocalContention$")
	child.Env = append(os.Environ(), pathEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child writer check: %v\n%s", err, out)
	}
}

func TestWriterRejectsHardlinkedStateAcrossDirectories(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "first", "state.json"), filepath.Join(dir, "second", "state.json")}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	const original = `{"tags":{"fixture":"retained"}}`
	if err := os.WriteFile(paths[0], []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(paths[0], paths[1]); err != nil {
		t.Skipf("filesystem cannot create a hard-link fixture: %v", err)
	}
	for _, path := range paths {
		if lock, err := New(path).LockWriter(); err == nil {
			lock.Close()
			t.Errorf("accepted a multiply linked state file: %s", path)
		}
		if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
			t.Errorf("published a sidecar for a multiply linked state file: %s, %v", path, err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != original {
			t.Errorf("changed a hard-linked state target: %q, %v", data, err)
		}
	}
}
