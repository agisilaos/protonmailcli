//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package store

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func lockWriter(statePath string) (io.Closer, error) {
	file, err := lockUnixWriterFile(statePath)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func tryWriterFileLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
