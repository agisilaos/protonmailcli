//go:build windows

package store

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsWriteProtectionAllowsReceiptRecovery(t *testing.T) {
	for _, tc := range []struct {
		cause error
		allow bool
	}{
		{windows.ERROR_WRITE_PROTECT, true},
		{windows.ERROR_ACCESS_DENIED, true},
		{windows.ERROR_SHARING_VIOLATION, false},
		{windows.ERROR_FILE_NOT_FOUND, false},
	} {
		err := &os.PathError{Op: "open", Path: "state.lock", Err: tc.cause}
		if got := IsWritePermissionError(err); got != tc.allow {
			t.Errorf("recovery for %v = %t; want %t", tc.cause, got, tc.allow)
		}
	}
}
