//go:build unix

package store

import (
	"fmt"
	"os"
	"syscall"
)

var chownStateFile = (*os.File).Chown

func preserveStateGroup(file *os.File, original os.FileInfo) error {
	if original == nil {
		return nil
	}
	mode := original.Mode().Perm()
	// Match the writer sidecar rule: group identity matters when its read/write
	// access differs from other users. Private/equal-access files need no chown.
	if (mode>>3)&6 == mode&6 {
		return nil
	}
	group := original.Sys().(*syscall.Stat_t).Gid
	staged, err := file.Stat()
	if err != nil {
		return err
	}
	if staged.Sys().(*syscall.Stat_t).Gid == group {
		return nil
	}
	if err := chownStateFile(file, -1, int(group)); err != nil {
		return fmt.Errorf("match existing state group before publication: %w", err)
	}
	return nil
}
