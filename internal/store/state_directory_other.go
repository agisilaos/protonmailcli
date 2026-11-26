//go:build !windows && !darwin && !linux

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// makeStateDirectory persists only directory entries created for this state
// path. Existing components are assumed durably provisioned; this helper does
// not coordinate first-use directory creation across processes. Established
// shared directories do not require access to their parents.
func makeStateDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err := makeStateDirectory(parent); err != nil {
		return err
	}
	dir, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	created := true
	if err := os.Mkdir(path, 0755); err != nil {
		// Another initializer may have created this component after our first stat.
		info, statErr := os.Stat(path)
		if !os.IsExist(err) || statErr != nil || !info.IsDir() {
			return err
		}
		created = false
	}
	if err := syncStateDirectory(dir); err != nil {
		failure := fmt.Errorf("state directory created but parent directory durability could not be confirmed: %w", err)
		if created {
			// Do not leave an empty unconfirmed component for a retry to treat as an
			// established directory. Remove never deletes another caller's contents.
			if cleanupErr := os.Remove(path); cleanupErr != nil {
				return errors.Join(failure, fmt.Errorf("remove unconfirmed state directory %s: %w", path, cleanupErr))
			}
		}
		return failure
	}
	return nil
}
