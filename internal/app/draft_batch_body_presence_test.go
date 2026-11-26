package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftBatchRejectsExplicitEmptyBodyAlongsideFile(t *testing.T) {
	for _, mode := range []string{"local", "imap", "imap-keyed"} {
		t.Run(mode, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			if mode == "local" {
				t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
			}
			args = append(args, "draft", "create-many", "--stdin")
			if mode == "imap-keyed" {
				args = append(args, "--idempotency-key", "source-presence")
			}
			path := filepath.Join(t.TempDir(), "body.txt")
			if err := os.WriteFile(path, []byte("must not be used"), 0600); err != nil {
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
				{"to": []string{"recipient@example.invalid"}, "body": "", "body_file": path},
				{"to": []string{"recipient@example.invalid"}, "body": "inline body", "body_file": ""},
			})
			if err != nil {
				t.Fatal(err)
			}
			code, out := runWorkflow(t, args, string(manifest))
			_, appended := s.snapshot()
			if code == 0 || reads != 0 || len(appended) != 0 || !strings.Contains(out, `"errorCode":"validation_error"`) || !strings.Contains(out, "provide only one") {
				t.Fatalf("conflicting empty source accepted: mode=%s exit=%d reads=%d appends=%d out=%s", mode, code, reads, len(appended), out)
			}
		})
	}
}

func TestDraftBatchEmptyBodyMatchesManifestSchema(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "draft-create-many.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifestSchema struct {
		Items struct {
			Properties map[string]struct{ MinLength int }
		}
	}
	if err := json.Unmarshal(schema, &manifestSchema); err != nil {
		t.Fatal(err)
	}
	if minimum := manifestSchema.Items.Properties["body"].MinLength; minimum != 1 {
		t.Errorf("schema allows an empty inline body: minLength=%d", minimum)
	}
	for _, mode := range []string{"local", "imap"} {
		t.Run(mode, func(t *testing.T) {
			server := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, server))
			if mode == "local" {
				t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
			}
			path := filepath.Join(t.TempDir(), "empty")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := json.Marshal([]map[string]any{
				{"to": []string{"recipient@example.invalid"}, "subject": "Inline", "body": ""},
				{"to": []string{"recipient@example.invalid"}, "subject": "File", "body_file": path},
			})
			if err != nil {
				t.Fatal(err)
			}
			code, out := runWorkflow(t, append(args, "--dry-run", "draft", "create-many", "--stdin"), string(rows))
			if code != 10 {
				t.Fatalf("exit=%d output=%s", code, out)
			}
			results := regressionData(t, out)["results"].([]any)
			if len(results) != 2 || results[0].(map[string]any)["errorCode"] != "validation_error" || results[1].(map[string]any)["ok"] != true {
				t.Fatalf("unexpected empty-body results: %s", out)
			}
		})
	}
}
