package app

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var draftSnapshotVolumeFlagsFn = func(handle windows.Handle) (uint32, error) {
	var flags uint32
	err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, nil, 0)
	return flags, err
}

// Windows mode bits do not establish a private ACL. Supply the protected DACL
// during CREATE_NEW, before the file can ever contain draft body bytes.
func createDraftSnapshotFile(dir, pattern string) (*os.File, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if strings.ContainsAny(pattern, `/\`) {
		return nil, &os.PathError{Op: "createtemp", Path: pattern, Err: errors.New("pattern contains path separator")}
	}
	prefix, suffix := pattern, ""
	if wildcard := strings.LastIndexByte(pattern, '*'); wildcard >= 0 {
		prefix, suffix = pattern[:wildcard], pattern[wildcard+1:]
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	for attempt := 0; attempt < 10000; attempt++ {
		path := filepath.Join(dir, prefix+rand.Text()+suffix)
		name, err := draftSnapshotWindowsPath(path)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			&attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_TEMPORARY, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "createtemp", Path: path, Err: err}
		}
		file := os.NewFile(uintptr(handle), path)
		flags, err := draftSnapshotVolumeFlagsFn(handle)
		if err == nil && flags&windows.FILE_PERSISTENT_ACLS == 0 {
			err = errors.New("temporary filesystem does not preserve and enforce ACLs")
		}
		if err != nil {
			return nil, errors.Join(fmt.Errorf("verify private snapshot filesystem: %w", err), file.Close(), os.Remove(path))
		}
		return file, nil
	}
	return nil, &os.PathError{Op: "createtemp", Path: filepath.Join(dir, pattern), Err: os.ErrExist}
}

func draftSnapshotWindowsPath(path string) (*uint16, error) {
	// Retain Win32 handling for short paths and os.CreateTemp's support for
	// long drive/UNC paths when calling the native creation API directly.
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
	return windows.UTF16PtrFromString(name)
}
