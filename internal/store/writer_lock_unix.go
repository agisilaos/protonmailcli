//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var readOnlyVolume error = syscall.EROFS

var chmodWriterLock = (*os.File).Chmod
var chownWriterLock = (*os.File).Chown
var openWriterFile = os.OpenFile
var linkWriterLock = os.Link
var tryWriterFileLockFn = tryWriterFileLock

type openedWriterLock struct {
	file           *os.File
	writeAccessErr error
}

func lockUnixWriterFile(statePath string) (*os.File, error) {
	return lockUnixWriterFileWith(statePath, tryWriterFileLockFn)
}

func lockUnixWriterFileWith(statePath string, tryLock func(*os.File) error) (*os.File, error) {
	mode, group, err := readableWriterStateAccess(statePath)
	if err != nil {
		return nil, err
	}
	path := statePath + ".lock"
	opened, err := openOrPublishWriterLock(path, mode, group)
	if err != nil {
		return nil, err
	}
	file := opened.file
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = file.Close()
		}
	}()
	info, err := statWriterLock(file)
	if err != nil {
		return nil, err
	}
	// Match shared access without chmod/chown on already matching sidecars.
	// Never change an alias's permissions or group through the lock path.
	changeGroup := group >= 0 && int(info.Sys().(*syscall.Stat_t).Gid) != group
	if info.Mode().Perm() != mode || changeGroup {
		if info.Sys().(*syscall.Stat_t).Nlink != 1 {
			return nil, fmt.Errorf("cannot change permissions on a hardlinked state lock: %s", path)
		}
		if changeGroup {
			if err := chownWriterLock(file, -1, group); err != nil {
				return nil, fmt.Errorf("match state lock group (the lock owner may need to update it): %w", err)
			}
		}
		if info.Mode().Perm() != mode {
			if err := chmodWriterLock(file, mode); err != nil {
				return nil, fmt.Errorf("match state lock permissions (the lock owner may need to update them): %w", err)
			}
		}
	}
	if err := tryLock(file); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		if errors.Is(err, syscall.EBADF) && opened.writeAccessErr != nil {
			return nil, fmt.Errorf("exclusive state lock requires write access: %w", errors.Join(opened.writeAccessErr, err))
		}
		return nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return nil, fmt.Errorf("state lock file changed while locking: %s", path)
	}
	// Closing this same descriptor releases its advisory lock.
	keepOpen = true
	return file, nil
}

// Check existing receipts before publishing a new sidecar. A cooperating writer
// may atomically replace the state while this pre-lock probe is in progress.
func readableWriterStateAccess(statePath string) (os.FileMode, int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		state, err := os.Lstat(statePath)
		if os.IsNotExist(err) {
			return 0600, -1, nil
		}
		if err != nil {
			return 0, -1, err
		}
		if !state.Mode().IsRegular() || state.Sys().(*syscall.Stat_t).Nlink != 1 {
			return 0, -1, fmt.Errorf("state target must be a regular file without hard links: %s", statePath)
		}
		// Do not follow a changed symlink or wait on a changed special file.
		readable, err := openWriterFile(statePath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return 0, -1, err
		}
		opened, err := readable.Stat()
		closeErr := readable.Close()
		if err != nil {
			return 0, -1, err
		}
		if closeErr != nil {
			return 0, -1, closeErr
		}
		links := opened.Sys().(*syscall.Stat_t).Nlink
		if !opened.Mode().IsRegular() || (links != 0 && links != 1) {
			return 0, -1, fmt.Errorf("state target must be a regular file without hard links: %s", statePath)
		}
		if links == 0 || !os.SameFile(state, opened) {
			continue
		}
		mode, group := opened.Mode().Perm()&0666, -1
		// Group identity matters only when its access differs from other users.
		// Equal masks need no reassignment, including private state and callers
		// authorized by world access but unable to assign the state's group.
		if (mode>>3)&6 != mode&6 {
			group = int(opened.Sys().(*syscall.Stat_t).Gid)
		}
		return mode, group, nil
	}
	// Repeated checkpoint replacement is contention, not a permanent state error.
	return 0, -1, ErrBusy
}

func openPublishedWriterLock(path string) (*openedWriterLock, error) {
	flags := syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	// NFS-backed exclusive locks can require a writable descriptor. Keep the
	// read-only option for callers whose shared access grants only read permission.
	file, err := openWriterFile(path, flags|os.O_RDWR, 0)
	if errors.Is(err, os.ErrPermission) {
		readOnly, readErr := openWriterFile(path, flags|os.O_RDONLY, 0)
		if readErr != nil {
			return nil, readErr
		}
		return &openedWriterLock{file: readOnly, writeAccessErr: err}, nil
	}
	if err != nil {
		return nil, err
	}
	return &openedWriterLock{file: file}, nil
}

func openOrPublishWriterLock(path string, mode os.FileMode, group int) (*openedWriterLock, error) {
	file, err := openPublishedWriterLock(path)
	if !os.IsNotExist(err) {
		return file, err
	}
	staged, err := os.CreateTemp(filepath.Dir(path), ".state-lock-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged.Name())
	if group >= 0 {
		if err := chownWriterLock(staged, -1, group); err != nil {
			staged.Close()
			return nil, fmt.Errorf("match state lock group before publication: %w", err)
		}
	}
	if err := chmodWriterLock(staged, mode); err != nil {
		staged.Close()
		return nil, err
	}
	// Publish final permissions without replacing a competing writer's inode.
	// A successful hard link may leave a harmless staging alias on interruption.
	if err := publishWriterLock(staged.Name(), path); err != nil {
		staged.Close()
		if os.IsExist(err) {
			return openPublishedWriterLock(path)
		}
		return nil, err
	}
	if err := os.Remove(staged.Name()); err != nil && !os.IsNotExist(err) {
		staged.Close()
		return nil, err
	}
	return &openedWriterLock{file: staged}, nil
}

func publishWriterLock(source, destination string) error {
	err := linkWriterLock(source, destination)
	if err == nil || !(errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EXDEV)) {
		return err
	}
	// Some filesystems support atomic no-replace rename but not hard links.
	// Never substitute ordinary Rename, which could overwrite an active lock.
	if renameErr := renameWriterLockNoReplace(source, destination); renameErr != nil {
		if errors.Is(renameErr, os.ErrExist) {
			return &os.LinkError{Op: "rename", Old: source, New: destination, Err: renameErr}
		}
		return fmt.Errorf("publish state lock without hard links: %w", errors.Join(err, renameErr))
	}
	return nil
}
