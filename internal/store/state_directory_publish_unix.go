//go:build darwin || linux

package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const stateDirectoryMarker = ".protonmailcli-directory-init"
const stateDirectoryMarkerContent = "protonmailcli directory provisioning v1\n"

var renameStateDirectoryNoReplace = renameWriterLockNoReplace

// Publish the directory with durable initialization evidence already inside its
// inode. Any observer can finish the parent sync before using that directory.
func makeStateDirectory(path string) (retErr error) {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}
		return finishStateDirectory(path)
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err := makeStateDirectory(parent); err != nil {
		return err
	}
	staged, err := stageStateDirectory(parent)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, removeStateDirectoryStaging(staged)) }()
	if err := renameStateDirectoryNoReplace(staged, path); err != nil {
		if os.IsExist(err) {
			// A conflicting name may be a dangling link or may already have
			// disappeared. Inspect it once instead of recursively publishing.
			current, statErr := os.Stat(path)
			if statErr != nil {
				return fmt.Errorf("state directory publication destination is unavailable: %w", statErr)
			}
			if !current.IsDir() {
				return &os.PathError{Op: "publish state directory", Path: path, Err: syscall.ENOTDIR}
			}
			return finishStateDirectory(path)
		}
		return fmt.Errorf("publish state directory without replacement (pre-provision the directory if the filesystem lacks this operation): %w", err)
	}
	return finishStateDirectory(path)
}

func stageStateDirectory(parent string) (_ string, retErr error) {
	var path string
	created := false
	for attempt := 0; attempt < 10; attempt++ {
		path = filepath.Join(parent, ".state-directory-"+rand.Text())
		if err := os.Mkdir(path, 0755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		created = true
		break
	}
	if !created {
		return "", errors.New("cannot allocate state directory staging name")
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, removeStateDirectoryStaging(path))
		}
	}()
	dir, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	marker, err := os.OpenFile(filepath.Join(path, stateDirectoryMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", err
	}
	defer marker.Close()
	// This marker contains no user data. All directory users must be able to
	// recognize it even when the creating process has a private umask.
	if err := marker.Chmod(0444); err != nil {
		return "", err
	}
	if _, err := io.WriteString(marker, stateDirectoryMarkerContent); err != nil {
		return "", err
	}
	if err := marker.Sync(); err != nil {
		return "", err
	}
	if err := marker.Close(); err != nil {
		return "", err
	}
	if err := syncStateDirectory(dir); err != nil {
		return "", err
	}
	return path, nil
}

func finishStateDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	markerPath := filepath.Join(path, stateDirectoryMarker)
	info, err := root.Lstat(stateDirectoryMarker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Unrelated files, directories and symlinks at this reserved name are left
	// untouched. Only this exact regular-file record denotes initialization.
	if !info.Mode().IsRegular() || info.Size() != int64(len(stateDirectoryMarkerContent)) {
		return nil
	}
	marker, err := root.OpenFile(stateDirectoryMarker, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer marker.Close()
	opened, err := marker.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fmt.Errorf("state directory initialization marker changed: %s", markerPath)
	}
	content, err := io.ReadAll(io.LimitReader(marker, int64(len(stateDirectoryMarkerContent))+1))
	if err != nil {
		return err
	}
	if string(content) != stateDirectoryMarkerContent {
		return nil
	}
	// Resolve '..' through the pinned directory inode, not the lexical caller
	// path: a symlink may name this directory from an entirely different parent.
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: path + "/..", Err: err}
	}
	parent := os.NewFile(uintptr(fd), path+"/..")
	defer parent.Close()
	if err := syncStateDirectory(parent); err != nil {
		return fmt.Errorf("state directory published but parent directory durability could not be confirmed: %w", err)
	}
	current, err := root.Lstat(stateDirectoryMarker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return nil
	}
	if err := root.Remove(stateDirectoryMarker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncStateDirectory(dir)
}

func removeStateDirectoryStaging(path string) (result error) {
	for _, name := range []string{filepath.Join(path, stateDirectoryMarker), path} {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, fmt.Errorf("remove state directory staging %s: %w", name, err))
		}
	}
	return result
}
