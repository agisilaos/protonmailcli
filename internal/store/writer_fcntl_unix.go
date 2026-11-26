//go:build aix || (solaris && !illumos)

package store

import (
	"io"
	"os"
)

func init() {
	runRecordLockHelper()
}

func lockWriter(statePath string) (io.Closer, error) {
	return lockRecordWriter(statePath)
}

func tryWriterFileLock(file *os.File) error {
	return tryRecordFileLock(file)
}
