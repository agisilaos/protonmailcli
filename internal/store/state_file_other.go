//go:build !windows

package store

import (
	"fmt"
	"os"
	"path/filepath"
)

var syncStateDirectory = (*os.File).Sync

func openStateSnapshot(path string) (*os.File, error) {
	return os.Open(path)
}

func publishState(staged, target string) error {
	// Establish directory access before replacing the previous checkpoint.
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	// Syncing the staged file does not persist its renamed directory entry.
	// Once published, never roll back: readers may already have seen the intent.
	if err := syncStateDirectory(dir); err != nil {
		return fmt.Errorf("%w: %w", ErrCheckpointUncertain, err)
	}
	return nil
}
