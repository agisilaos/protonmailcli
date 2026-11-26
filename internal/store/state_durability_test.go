//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/model"
)

func TestSaveSyncsPublishedCheckpointDirectory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "sync failure"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			disk := New(path)
			if err := disk.Save(emptyState()); err != nil {
				t.Fatal(err)
			}
			previous := syncStateDirectory
			t.Cleanup(func() { syncStateDirectory = previous })
			failure := errors.New("synthetic directory sync failure")
			called := false
			syncStateDirectory = func(dir *os.File) error {
				called = true
				info, err := dir.Stat()
				if err != nil || !info.IsDir() || !directoryFileIs(dir, filepath.Dir(path)) {
					t.Fatalf("wrong checkpoint directory: %s info=%v err=%v", dir.Name(), info, err)
				}
				visible, err := disk.Load()
				if err != nil || visible.Idempotency["send-key"].Status != "pending" {
					t.Fatalf("directory sync did not follow publication: state=%+v err=%v", visible, err)
				}
				if fail {
					return failure
				}
				return previous(dir)
			}
			state := emptyState()
			state.Idempotency["send-key"] = model.IdempotencyRecord{Operation: "message.send", Status: "pending"}
			err := disk.Save(state)
			if !called {
				t.Fatal("Save reported success without syncing its checkpoint directory")
			}
			if fail {
				if !errors.Is(err, ErrCheckpointUncertain) || !errors.Is(err, failure) || !strings.Contains(err.Error(), "published") || !strings.Contains(err.Error(), "durability") {
					t.Fatalf("post-publication durability failure was hidden: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			visible, err := disk.Load()
			if err != nil || visible.Idempotency["send-key"].Status != "pending" {
				t.Fatalf("published intent was rolled back: state=%+v err=%v", visible, err)
			}
			names, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state-*"))
			if err != nil || len(names) != 0 {
				t.Fatalf("staging files left behind: %v %v", names, err)
			}
		})
	}
}

func TestSaveCannotSyncUnreadableParentBeforePublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	const original = "{}\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if file, err := os.Open(dir); err == nil {
		file.Close()
		t.Skip("filesystem permits reading this directory")
	}
	err := New(path).Save(emptyState())
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unopenable checkpoint directory accepted: %v", err)
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil || string(current) != original {
		t.Fatalf("state changed before directory access was established: %q %v", current, readErr)
	}
}
