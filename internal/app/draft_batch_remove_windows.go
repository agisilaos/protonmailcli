//go:build windows

package app

// Windows may not unlink an open file. Remove its private name after closing.
func prepareDraftSnapshotRemoval(string) (bool, error) {
	return true, nil
}
