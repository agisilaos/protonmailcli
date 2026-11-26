//go:build !unix

package store

import "path/filepath"

func prepareStatePath(path string) (string, error) {
	// Preserve the platform's existing path and Windows handle/alias rules.
	if err := makeStateDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}
