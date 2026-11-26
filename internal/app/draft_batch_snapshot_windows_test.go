package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func draftSnapshotPrivacyError(file *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !windows.EqualSid(owner, user.User.Sid) {
		return fmt.Errorf("snapshot owner is not the current user: %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("snapshot permits inherited access: control=%x err=%v", control, err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		return fmt.Errorf("snapshot does not have one explicit user access entry: %v", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		return err
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	wantAccess := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE)
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 ||
		!windows.EqualSid(sid, user.User.Sid) || ace.Mask&wantAccess != wantAccess {
		return fmt.Errorf("snapshot access is not restricted to its user: %s", descriptor.String())
	}
	return nil
}

func TestWindowsDraftSnapshotHasPrivateACLBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	// This empty, disposable directory models a shared temporary directory.
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	file, err := createDraftSnapshotFileFn(dir, "snapshot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if filepath.Dir(file.Name()) != dir {
		t.Fatalf("snapshot ignored the selected directory: %q", file.Name())
	}
	if err := draftSnapshotPrivacyError(file); err != nil {
		t.Fatalf("snapshot was not private at creation: %v", err)
	}
	const body = "synthetic private snapshot"
	if _, err := io.WriteString(file, body); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// An interrupted command can leave a closed named file. Its ACL must remain
	// private without depending on the original handle still being open.
	leftover, err := os.Open(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer leftover.Close()
	if err := draftSnapshotPrivacyError(leftover); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(leftover)
	if err != nil || string(data) != body {
		t.Fatalf("snapshot content changed: %q, %v", data, err)
	}
}

func TestWindowsDraftSnapshotRejectsUnverifiedACLFilesystem(t *testing.T) {
	for _, failure := range []error{nil, windows.ERROR_ACCESS_DENIED} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			dir := t.TempDir()
			volumeFlags := draftSnapshotVolumeFlagsFn
			t.Cleanup(func() { draftSnapshotVolumeFlagsFn = volumeFlags })
			draftSnapshotVolumeFlagsFn = func(windows.Handle) (uint32, error) { return 0, failure }
			file, err := createDraftSnapshotFileFn(dir, "snapshot-*")
			if file != nil {
				file.Close()
				t.Error("returned storage without verified ACL support")
			}
			if err == nil || (failure != nil && !errors.Is(err, failure)) {
				t.Fatalf("missing ACL verification error: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("ACL check failure retained temporary storage: %v, %v", entries, err)
			}
		})
	}
}

func TestWindowsDraftSnapshotSupportsLongTemporaryPaths(t *testing.T) {
	dir := t.TempDir()
	for len(dir) <= windows.MAX_PATH {
		dir = filepath.Join(dir, strings.Repeat("d", 60))
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := createDraftSnapshotFileFn(dir, "snapshot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := draftSnapshotPrivacyError(file); err != nil {
		t.Fatal(err)
	}
}
