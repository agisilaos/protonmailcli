package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftBatchKeyDetectsChangedBodyFile(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "body-file-batch")
	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("original body"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal([]map[string]any{{"to": []string{"recipient@example.invalid"}, "body_file": bodyFile}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		code, out := runWorkflow(t, args, string(manifest))
		if code != 0 {
			t.Fatalf("unchanged body run=%d exit=%d out=%s", i, code, out)
		}
	}
	if err := os.WriteFile(bodyFile, []byte("replacement body"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	if code != 6 || !strings.Contains(out, `"code":"idempotency_conflict"`) {
		t.Fatalf("changed body reused the old receipt: exit=%d out=%s", code, out)
	}
	_, appended := s.snapshot()
	if len(appended) != 1 || !strings.Contains(appended[0], "original body") {
		t.Fatalf("expected only the original draft, got %v", appended)
	}
}

func TestUnkeyedDraftBatchReadsEachBodyBeforeItsDispatch(t *testing.T) {
	bodyFile := filepath.Join(t.TempDir(), "later.txt")
	s := &workflowServer{afterAppend: func() {
		if err := os.WriteFile(bodyFile, []byte("available after first draft"), 0600); err != nil {
			t.Error(err)
		}
	}}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin")
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"recipient@example.invalid"}, "body": "first draft"},
		{"to": []string{"recipient@example.invalid"}, "body_file": bodyFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	_, appended := s.snapshot()
	if code != 0 || len(appended) != 2 || !strings.Contains(appended[1], "available after first draft") {
		t.Fatalf("unkeyed batch loaded later bodies too early: exit=%d out=%s appends=%v", code, out, appended)
	}
}

func TestMissingBodyFileCannotReplayLegacyDraftBatchReceipt(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	bodyFile := filepath.Join(t.TempDir(), "missing.txt")
	// Before file contents were included, the payload encoded the manifest fields
	// in this order. Reproduce that stored receipt without creating another draft.
	manifest := fmt.Sprintf(`[{"to":["recipient@example.invalid"],"subject":"","body_file":%q}]`, bodyFile)
	hash := sha256.Sum256([]byte(manifest))
	state := fmt.Sprintf(`{"idempotency":{"legacy-batch":{"operation":"draft.create-many","payloadHash":"%x","response":{"results":[{"index":0,"ok":true,"draftId":"imap:Drafts:91"}],"count":1,"success":1,"failed":0,"source":"imap"}}}}`, hash)
	if err := os.WriteFile(args[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	args = append(args, "draft", "create-many", "--stdin", "--idempotency-key", "legacy-batch")
	code, out := runWorkflow(t, args, manifest)
	if code != 6 || !strings.Contains(out, `"code":"idempotency_conflict"`) {
		t.Fatalf("unavailable body replayed legacy success: exit=%d out=%s", code, out)
	}
	_, appended := s.snapshot()
	if len(appended) != 0 {
		t.Fatalf("legacy key dispatched a new draft: %v", appended)
	}
}

func TestDraftBatchKeyPreservesBodyFileReadFailuresOnReplay(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "partial-file-batch")
	bodyFile := filepath.Join(t.TempDir(), "missing.txt")
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"recipient@example.invalid"}, "body": "valid item"},
		{"to": []string{"recipient@example.invalid"}, "body_file": bodyFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		code, out := runWorkflow(t, args, string(manifest))
		if code != 10 || !strings.Contains(out, `"errorCode":"validation_error"`) {
			t.Fatalf("read error was not retained per item: run=%d exit=%d out=%s", i, code, out)
		}
	}
	// A newly supplied empty inline body changes the row to a conflicting-source error.
	conflicting := strings.Replace(string(manifest), `"body_file":`, `"body":"","body_file":`, 1)
	if code, out := runWorkflow(t, args, conflicting); code != 6 || !strings.Contains(out, "idempotency_conflict") {
		t.Fatalf("changed validation input replayed a missing-file receipt: exit=%d out=%s", code, out)
	}
	if err := os.WriteFile(bodyFile, []byte("repaired item"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	if code != 6 || !strings.Contains(out, `"code":"idempotency_conflict"`) {
		t.Fatalf("changed file availability reused the receipt: exit=%d out=%s", code, out)
	}
	_, appended := s.snapshot()
	if len(appended) != 1 {
		t.Fatalf("retry dispatched another item: %v", appended)
	}
}
