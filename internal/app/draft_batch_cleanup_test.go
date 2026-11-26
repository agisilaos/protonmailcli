package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func TestDeferredSnapshotCleanupReportsFailureWithoutReplacingOutcome(t *testing.T) {
	for _, outcome := range []string{"success", "preview", "partial", "credentials"} {
		t.Run(outcome, func(t *testing.T) {
			server := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, server))
			bodyPath := filepath.Join(t.TempDir(), "body.txt")
			if err := os.WriteFile(bodyPath, []byte("synthetic private body"), 0600); err != nil {
				t.Fatal(err)
			}
			createFile, removeFile, prepareRemoval := createDraftSnapshotFileFn, removeDraftSnapshotFileFn, prepareDraftSnapshotRemovalFn
			t.Cleanup(func() {
				createDraftSnapshotFileFn, removeDraftSnapshotFileFn, prepareDraftSnapshotRemovalFn = createFile, removeFile, prepareRemoval
			})
			var snapshot *os.File
			createDraftSnapshotFileFn = func(_, pattern string) (*os.File, error) {
				file, err := createFile(t.TempDir(), pattern)
				snapshot = file
				return file, err
			}
			// Exercise the close-then-remove filesystem contract on every host.
			prepareDraftSnapshotRemovalFn = func(string) (bool, error) { return true, nil }
			removeDraftSnapshotFileFn = func(string) error { return os.ErrPermission }
			args := append(append([]string(nil), base...), "draft", "create-many", "--stdin", "--idempotency-key", "cleanup")
			manifest := fmt.Sprintf(`[{"to":["user@example.invalid"],"body_file":%q}]`, bodyPath)
			wantExit, wantAppends := 0, 1
			switch outcome {
			case "preview":
				args = append([]string{"--dry-run"}, args...)
				wantAppends = 0
			case "partial":
				manifest = strings.TrimSuffix(manifest, "]") + `,{"body":"missing recipient"}]`
				wantExit = 10
			case "credentials":
				cfg, err := config.Load(base[2])
				if err != nil {
					t.Fatal(err)
				}
				cfg.Bridge.Username = ""
				if err := config.Save(base[2], cfg); err != nil {
					t.Fatal(err)
				}
				wantExit, wantAppends = 3, 0
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, strings.NewReader(manifest), &stdout, &stderr)
			_, appended := server.snapshot()
			if code != wantExit || len(appended) != wantAppends {
				t.Fatalf("cleanup replaced the original outcome: exit=%d appends=%d stdout=%s stderr=%s", code, len(appended), stdout.String(), stderr.String())
			}
			if snapshot == nil || !strings.Contains(stderr.String(), "draft snapshot cleanup failed") || !strings.Contains(stderr.String(), snapshot.Name()) {
				t.Fatalf("cleanup failure and retained path were not reported: stderr=%s", stderr.String())
			}
			if _, err := snapshot.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("cleanup retained the descriptor: %v", err)
			}
			if outcome == "credentials" && !strings.Contains(stdout.String(), "config_error") {
				t.Fatalf("lost the original error: %s", stdout.String())
			}
			if outcome == "success" || outcome == "partial" {
				removeDraftSnapshotFileFn = removeFile
				code, replay := runWorkflow(t, args, manifest)
				_, replayAppended := server.snapshot()
				if code != wantExit || len(replayAppended) != wantAppends || !reflect.DeepEqual(regressionData(t, replay), regressionData(t, stdout.String())) {
					t.Fatalf("cleanup changed the recorded outcome: exit=%d appends=%d replay=%s", code, len(replayAppended), replay)
				}
			}
		})
	}
}
