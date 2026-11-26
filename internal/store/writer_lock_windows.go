package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var readOnlyVolume error = windows.ERROR_WRITE_PROTECT

func lockWriter(statePath string) (io.Closer, error) {
	if err := validateWriterState(statePath); err != nil {
		return nil, err
	}
	path := statePath + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|windows.O_FILE_FLAG_OPEN_REPARSE_POINT, 0600)
	if err != nil {
		return nil, err
	}
	_, err = statWriterLock(file)
	if err == nil {
		// Lock the validated handle using the same one-byte range as older clients.
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	}
	if err != nil {
		file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return windowsWriterLock{file}, nil
}

// Check the existing state before publishing a persistent sidecar with this
// caller's ACLs. Short aliases can change on replacement and name a second lock.
func validateWriterState(path string) error {
	file, err := openWindowsState(path, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state target must be a regular file: %s", path)
	}
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &identity); err != nil {
		return err
	}
	if identity.NumberOfLinks > 1 {
		return fmt.Errorf("state target must be a regular file without hard links: %s", path)
	}
	name, err := normalizedStateName(file)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(path), name) {
		return fmt.Errorf("use the registered state filename %q for writes: %s", name, path)
	}
	return nil
}

type windowsWriterLock struct {
	file *os.File
}

func (lock windowsWriterLock) Close() error {
	err := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &windows.Overlapped{})
	return errors.Join(err, lock.file.Close())
}
