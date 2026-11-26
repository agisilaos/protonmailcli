package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWriterRequiresReadableStateBeforeCreatingSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if lock, err := New(path).LockWriter(); err == nil {
		lock.Close()
		t.Fatal("published a writer lock before opening existing state for reading")
	} else if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("expected the state open error, got %v", err)
	}
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected sidecar after unreadable state: %v", err)
	}
}

func TestWriterRejectsAlternateShortStateName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long registered state filename.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetShortPathName(p, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, n)
	if _, err := windows.GetShortPathName(p, &buf[0], n); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, filepath.Base(windows.UTF16ToString(buf)))
	if strings.EqualFold(alias, path) {
		t.Skip("volume did not assign an alternate short filename")
	}
	if lock, err := New(alias).LockWriter(); err == nil {
		lock.Close()
		t.Fatal("accepted an alternate short filename for state writes")
	} else if !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Fatalf("missing registered state name in error: %v", err)
	}
	if _, err := os.Stat(alias + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alternate sidecar: %v", err)
	}
	lock, err := New(path).LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
}

func TestWriterChecksStateBelowLongDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) <= windows.MAX_PATH {
		dir = filepath.Join(dir, strings.Repeat("d", 60))
	}
	path := filepath.Join(dir, "state.json")
	s := New(path)
	st := emptyState()
	st.Tags["fixture"] = "long path"
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	lock, err := s.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	loaded, err := s.Load()
	if err != nil || loaded.Tags["fixture"] != "long path" {
		t.Fatalf("loaded tags %v, error %v", loaded.Tags, err)
	}
}

func TestStateCheckpointWithOpenSnapshot(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(windows.Handle(f.Fd()), nil, 0, nil, nil, &flags, nil, 0); err != nil {
		t.Fatal(err)
	}
	const supportsPOSIXUnlinkRename = 0x400
	if flags&supportsPOSIXUnlinkRename == 0 {
		t.Skip("filesystem does not advertise POSIX replacement support")
	}
	checkStateCheckpointWithOpenSnapshot(t, dir)
}
