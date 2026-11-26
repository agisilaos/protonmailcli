package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"protonmailcli/internal/model"
)

type Store struct {
	path      string
	directory *os.File
	root      *os.Root
}

func New(path string) *Store {
	return &Store{path: path}
}

var ErrBusy = errors.New("another command is updating this state file")

// ErrCheckpointUncertain means the replacement is visible, but its directory
// entry could not be confirmed durable. A caller must not assume it is absent.
var ErrCheckpointUncertain = errors.New("state checkpoint published but parent directory durability could not be confirmed")

// IsWritePermissionError identifies denied writes for which reading a saved
// receipt may still succeed.
func IsWritePermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, readOnlyVolume)
}

// Writer exposes the state path associated with the held lock.
type Writer struct {
	*Store
	io.Closer
	directory *os.File
}

func (w *Writer) Close() error {
	return errors.Join(w.Closer.Close(), w.Store.closeDirectory())
}

// LockWriter covers the entire read-modify-write cycle, including checkpoints.
// The lock file stays in place because the state file is atomically replaced.
func (s *Store) LockWriter() (*Writer, error) {
	path, err := prepareStatePath(s.path)
	if err != nil {
		return nil, err
	}
	pinned, err := openWriterStore(path)
	if err != nil {
		return nil, err
	}
	dir := pinned.directory
	lock, err := lockWriter(path)
	if err != nil {
		pinned.closeDirectory()
		return nil, err
	}
	if err := pinned.checkDirectory(); err != nil {
		lock.Close()
		pinned.closeDirectory()
		return nil, err
	}
	return &Writer{Store: pinned, Closer: lock, directory: dir}, nil
}

func statWriterLock(file *os.File) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return nil, fmt.Errorf("state lock must be an empty regular file: %s", file.Name())
	}
	return info, nil
}

func emptyState() model.State {
	return model.State{
		Drafts:      map[string]model.Draft{},
		Messages:    map[string]model.Message{},
		Tags:        map[string]string{},
		Filters:     map[string]model.Filter{},
		Bridge:      model.BridgeState{},
		Idempotency: map[string]model.IdempotencyRecord{},
	}
}

func (s *Store) checkDirectory() error {
	if s.directory == nil {
		return nil
	}
	expected, err := s.directory.Stat()
	if err != nil {
		return err
	}
	actual, err := os.Stat(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	if !os.SameFile(expected, actual) {
		return errors.New("state directory changed while its writer lock was held")
	}
	return nil
}

func (s *Store) Load() (model.State, error) {
	if err := s.checkDirectory(); err != nil {
		return model.State{}, err
	}
	var file *os.File
	var err error
	if s.root != nil {
		file, err = s.openPinnedSnapshot()
	} else {
		file, err = openStateSnapshot(s.path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return emptyState(), nil
	}
	if err != nil {
		return model.State{}, err
	}
	b, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return model.State{}, err
	}
	var st model.State
	if err := json.Unmarshal(b, &st); err != nil {
		return model.State{}, fmt.Errorf("decode state: %w", err)
	}
	if st.Drafts == nil {
		st.Drafts = map[string]model.Draft{}
	}
	if st.Messages == nil {
		st.Messages = map[string]model.Message{}
	}
	if st.Tags == nil {
		st.Tags = map[string]string{}
	}
	if st.Filters == nil {
		st.Filters = map[string]model.Filter{}
	}
	if st.Idempotency == nil {
		st.Idempotency = map[string]model.IdempotencyRecord{}
	}
	return st, nil
}

func (s *Store) Save(st model.State) error {
	if err := s.checkDirectory(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if s.root != nil {
		return s.replacePinnedState(b)
	}
	path, err := prepareStatePath(s.path)
	if err != nil {
		return err
	}
	return replaceState(path, b)
}
