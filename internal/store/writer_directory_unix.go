//go:build unix

package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func openWriterStore(path string) (*Store, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	dir, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	return &Store{path: path, root: root, directory: dir}, nil
}

func (s *Store) closeDirectory() error {
	return errors.Join(s.directory.Close(), s.root.Close())
}

// All names are relative to the directory that owns the lock, including cleanup.
func (s *Store) replacePinnedState(data []byte) error {
	target := filepath.Base(s.path)
	mode := os.FileMode(0600)
	var original os.FileInfo
	if info, err := s.root.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state target is not a regular file: %s", s.path)
		}
		original, mode = info, info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	name := ".state-" + rand.Text()
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(name)
	defer file.Close()
	if err := preserveStateGroup(file, original); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := s.root.Rename(name, target); err != nil {
		return err
	}
	if err := syncStateDirectory(s.directory); err != nil {
		return fmt.Errorf("%w: %w", ErrCheckpointUncertain, err)
	}
	return nil
}
