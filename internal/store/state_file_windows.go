package store

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openStateSnapshot(path string) (*os.File, error) {
	return openWindowsState(path, 0)
}

func openWindowsState(path string, flags uint32) (*os.File, error) {
	// Preserve Win32 name handling for ordinary paths and use extended-length
	// syntax for long drive and UNC paths, as os.Open does.
	name, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if len(name) < 248 {
		name = path
	} else if !strings.HasPrefix(name, `\\?\`) && !strings.HasPrefix(name, `\\.\`) {
		if tail, ok := strings.CutPrefix(name, `\\`); ok {
			name = `\\?\UNC\` + tail
		} else {
			name = `\\?\` + name
		}
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func normalizedStateName(file *os.File) (string, error) {
	const volumeNameNone = 4 // FILE_NAME_NORMALIZED is the default.
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buf[0], uint32(len(buf)), volumeNameNone)
		if err != nil {
			return "", &os.PathError{Op: "get normalized name", Path: file.Name(), Err: err}
		}
		if n < uint32(len(buf)) {
			return filepath.Base(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n)
	}
}

func publishState(staged, target string) error {
	// On Windows Root.Rename requests POSIX replacement so existing readers
	// can finish on their old file. Go handles the filesystem-specific fallback.
	dir, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		// A directory ACL can grant file creation and replacement without listing.
		return os.Rename(staged, target)
	}
	defer dir.Close()
	return dir.Rename(filepath.Base(staged), filepath.Base(target))
}
