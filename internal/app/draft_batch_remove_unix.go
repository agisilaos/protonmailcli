//go:build !windows

package app

// Unix must unlink the empty file before draft contents can be written.
func prepareDraftSnapshotRemoval(path string) (bool, error) {
	return false, removeDraftSnapshotFileFn(path)
}
