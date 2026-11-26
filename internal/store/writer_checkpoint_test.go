//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriterCheckpointDuringReadProbeRemainsBusy(t *testing.T) {
	for _, timing := range []string{"before-open", "after-open"} {
		t.Run(timing, func(t *testing.T) {
			path := physicalStatePath(t, filepath.Join(t.TempDir(), "state.json"))
			store := New(path)
			if err := store.Save(emptyState()); err != nil {
				t.Fatal(err)
			}
			writer, err := store.LockWriter()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { writer.Close() })
			openFile := openWriterFile
			t.Cleanup(func() { openWriterFile = openFile })
			checkpointed := false
			openWriterFile = func(name string, flags int, mode os.FileMode) (*os.File, error) {
				if name != path || checkpointed {
					return openFile(name, flags, mode)
				}
				checkpointed = true
				if timing == "before-open" {
					if err := store.Save(emptyState()); err != nil {
						return nil, err
					}
					return openFile(name, flags, mode)
				}
				file, err := openFile(name, flags, mode)
				if err == nil {
					err = store.Save(emptyState())
				}
				return file, err
			}
			contender, err := store.LockWriter()
			if contender != nil {
				contender.Close()
			}
			if !checkpointed || !errors.Is(err, ErrBusy) {
				t.Fatalf("checkpoint became a permanent state error: checkpointed=%t err=%v", checkpointed, err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := store.LockWriter()
			if err != nil {
				t.Fatalf("retry after writer completion: %v", err)
			}
			next.Close()
		})
	}
}
