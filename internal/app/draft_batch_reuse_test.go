package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyedDraftBatchReadsRepeatedTemplateOnce(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "shared-template")
	bodyPath := filepath.Join(t.TempDir(), "template.txt")
	if err := os.WriteFile(bodyPath, []byte("original template"), 0600); err != nil {
		t.Fatal(err)
	}
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	reads := 0
	openBodyFileFn = func(path string) (bodyFile, error) {
		reads++
		return openFile(path)
	}
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"first@example.invalid"}, "body_file": bodyPath},
		{"to": []string{"second@example.invalid"}, "body_file": bodyPath},
		{"to": []string{"third@example.invalid"}, "body_file": bodyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		code, out := runWorkflow(t, args, string(manifest))
		if code != 0 || reads != i {
			t.Fatalf("shared template was not read once per invocation: exit=%d reads=%d run=%d out=%s", code, reads, i, out)
		}
	}
	if err := os.WriteFile(bodyPath, []byte("replacement template"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	if code != 6 || reads != 3 || !strings.Contains(out, `"code":"idempotency_conflict"`) {
		t.Fatalf("changed shared template did not conflict: exit=%d reads=%d out=%s", code, reads, out)
	}
	_, appended := s.snapshot()
	if len(appended) != 3 {
		t.Fatalf("batch replay created additional drafts: %v", appended)
	}
	for _, raw := range appended {
		if !strings.HasSuffix(raw, "original template") {
			t.Fatalf("draft did not use the captured template: %q", raw)
		}
	}
}

func TestKeyedDraftBatchKeepsDistinctLiteralFilePathsSeparate(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "literal-paths")
	bodyPath := filepath.Join(t.TempDir(), "template.txt")
	if err := os.WriteFile(bodyPath, []byte("template"), 0600); err != nil {
		t.Fatal(err)
	}
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	reads := 0
	openBodyFileFn = func(path string) (bodyFile, error) {
		reads++
		return openFile(path)
	}
	otherPath := filepath.Dir(bodyPath) + "/./template.txt"
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"first@example.invalid"}, "body_file": bodyPath},
		{"to": []string{"second@example.invalid"}, "body_file": otherPath},
		{"to": []string{"third@example.invalid"}, "body_file": otherPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	if code != 0 || reads != 2 {
		t.Fatalf("distinct literal paths shared a snapshot: exit=%d reads=%d out=%s", code, reads, out)
	}
}

func TestKeyedDraftBatchSnapshotPreservesBodyValidation(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "validated-template")
	bodyPath := filepath.Join(t.TempDir(), "template.txt")
	if err := os.WriteFile(bodyPath, []byte("valid template"), 0600); err != nil {
		t.Fatal(err)
	}
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	reads := 0
	openBodyFileFn = func(path string) (bodyFile, error) {
		reads++
		return openFile(path)
	}
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"recipient@example.invalid"}, "body_file": bodyPath},
		{"to": []string{"recipient@example.invalid"}, "body": "conflicting source", "body_file": bodyPath},
		{"body_file": filepath.Join(t.TempDir(), "irrelevant.txt")},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	_, appended := s.snapshot()
	if code != 10 || reads != 1 || len(appended) != 1 || !strings.Contains(out, "missing to") || !strings.Contains(out, "provide only one") {
		t.Fatalf("snapshot bypassed validation or read an invalid row: exit=%d reads=%d appends=%d out=%s", code, reads, len(appended), out)
	}
}

func TestDraftBatchConsumesStdinOnceWithOrWithoutCommandKey(t *testing.T) {
	for _, key := range []string{"", "stdin-bodies"} {
		name := "unkeyed"
		if key != "" {
			name = "keyed"
		}
		t.Run(name, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			manifestPath := filepath.Join(t.TempDir(), "drafts.json")
			manifest := `[{"to":["first@example.invalid"],"body_file":"-"},{"to":["second@example.invalid"],"body_file":"-"}]`
			if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "draft", "create-many", "--file", manifestPath)
			if key != "" {
				args = append(args, "--idempotency-key", key)
			}
			code, out := runWorkflow(t, args, "stdin body")
			_, appended := s.snapshot()
			if code != 0 || len(appended) != 2 || !strings.HasSuffix(appended[0], "\r\n\r\nstdin body") || !strings.HasSuffix(appended[1], "\r\n\r\n") {
				t.Fatalf("stdin rows did not consume their source separately: exit=%d out=%s appends=%v", code, out, appended)
			}
		})
	}
}
