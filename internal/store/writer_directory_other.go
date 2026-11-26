//go:build !unix

package store

func openWriterStore(path string) (*Store, error)     { return New(path), nil }
func (s *Store) closeDirectory() error                { return nil }
func (s *Store) replacePinnedState(data []byte) error { return replaceState(s.path, data) }
