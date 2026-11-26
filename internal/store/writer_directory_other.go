//go:build !unix

package store

import "os"

func openWriterStore(path string) (*Store, error)     { return New(path), nil }
func (s *Store) closeDirectory() error                { return nil }
func (s *Store) replacePinnedState(data []byte) error { return replaceState(s.path, data) }

func (s *Store) openPinnedSnapshot() (*os.File, error) { return openStateSnapshot(s.path) }
