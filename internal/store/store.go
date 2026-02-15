package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"protonmailcli/internal/model"
)

type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func emptyState() model.State {
	return model.State{
		Drafts:   map[string]model.Draft{},
		Messages: map[string]model.Message{},
		Tags:     map[string]string{},
		Filters:  map[string]model.Filter{},
	}
}

func (s *Store) Load() (model.State, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return model.State{}, err
	}
	_, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		st := emptyState()
		if saveErr := s.Save(st); saveErr != nil {
			return model.State{}, saveErr
		}
		return st, nil
	}
	if err != nil {
		return model.State{}, err
	}
	b, err := os.ReadFile(s.path)
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
	return st, nil
}

func (s *Store) Save(st model.State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
