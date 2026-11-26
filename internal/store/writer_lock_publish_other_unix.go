//go:build unix && !darwin && !linux

package store

import "syscall"

func renameWriterLockNoReplace(source, destination string) error {
	return syscall.ENOTSUP
}
