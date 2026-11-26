//go:build unix

package store

import (
	"os"
	"path/filepath"
	"strings"
)

// Resolve the raw parent before joining or cleaning it. On Unix, link/.. follows
// the link before visiting its parent; lexical filepath.Dir would erase that.
// Keep the final basename unresolved so the regular-file policy still applies.
func prepareStatePath(path string) (string, error) {
	parent, name := filepath.Split(path)
	if parent == "" {
		parent = "."
	}
	resolved, err := resolveStateDirectory(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, name), nil
}

func resolveStateDirectory(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		if err := makeStateDirectory(resolved); err != nil {
			return "", err
		}
		return filepath.Abs(resolved)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// Resolve an existing prefix first, then create missing components beneath
	// that physical directory. Split preserves the raw symlink/.. prefix.
	trimmed := strings.TrimRight(path, string(os.PathSeparator))
	if trimmed == "" {
		return "", err
	}
	parent, name := filepath.Split(trimmed)
	if parent == "" {
		parent = "."
	}
	resolvedParent, err := resolveStateDirectory(parent)
	if err != nil {
		return "", err
	}
	target := filepath.Join(resolvedParent, name)
	if err := makeStateDirectory(target); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(target)
}
