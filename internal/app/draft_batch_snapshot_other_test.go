//go:build !windows

package app

import (
	"fmt"
	"os"
)

func draftSnapshotPrivacyError(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("snapshot mode=%04o, want 0600", info.Mode().Perm())
	}
	return nil
}
