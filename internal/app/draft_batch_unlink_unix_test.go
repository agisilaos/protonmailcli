//go:build !windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotUnlinkFailureStopsBeforeWritingPlaintext(t *testing.T) {
	server := &workflowServer{}
	base := imapCommandArgs(t, newWorkflowServer(t, server))
	bodyPath := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyPath, []byte("synthetic private body"), 0600); err != nil {
		t.Fatal(err)
	}
	createFile, removeFile := createDraftSnapshotFileFn, removeDraftSnapshotFileFn
	t.Cleanup(func() {
		createDraftSnapshotFileFn, removeDraftSnapshotFileFn = createFile, removeFile
	})
	var snapshotPath string
	createDraftSnapshotFileFn = func(_, pattern string) (*os.File, error) {
		file, err := createFile(t.TempDir(), pattern)
		if err == nil {
			snapshotPath = file.Name()
		}
		return file, err
	}
	removeDraftSnapshotFileFn = func(string) error { return os.ErrPermission }
	manifest := fmt.Sprintf(`[{"to":["user@example.invalid"],"body_file":%q}]`, bodyPath)
	code, out := runWorkflow(t, append(base, "--dry-run", "draft", "create-many", "--stdin", "--idempotency-key", "preview"), manifest)
	commands, _ := server.snapshot()
	if code != 1 || !strings.Contains(out, "batch_snapshot_failed") || len(commands) != 0 {
		t.Errorf("unlink failure reached preview: exit=%d commands=%v output=%s", code, commands, out)
	}
	contents, err := os.ReadFile(snapshotPath)
	if err != nil || len(contents) != 0 {
		t.Fatalf("failed unlink retained plaintext: bytes=%d err=%v", len(contents), err)
	}
	if _, err := os.Stat(base[4]); !os.IsNotExist(err) {
		t.Fatalf("failed preview reserved state: %v", err)
	}
}
