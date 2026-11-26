//go:build !unix && !windows

package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

var readOnlyVolume error = os.ErrPermission

func lockWriter(statePath string) (io.Closer, error) {
	return nil, fmt.Errorf("state writer locks on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}
