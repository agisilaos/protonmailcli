//go:build !windows

package app

import "os"

func createDraftSnapshotFile(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}
