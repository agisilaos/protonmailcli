package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func localPreviewData(t *testing.T, c regressionCLI, mode, input string, args ...string) map[string]any {
	t.Helper()
	argv := []string{"--no-input", "--config", c.config, "--state", c.state}
	if mode != "human" {
		argv = append(argv, "--"+mode)
	}
	var out, stderr bytes.Buffer
	if code := Run(append(argv, args...), strings.NewReader(input), &out, &stderr); code != 0 {
		t.Fatalf("command failed: exit=%d out=%s stderr=%s", code, &out, &stderr)
	}
	raw := out.String()
	if mode == "json" {
		return regressionData(t, raw)
	}
	if mode == "plain" {
		const prefix = "ok\ttrue\tdata\t"
		if !strings.HasPrefix(raw, prefix) || strings.Count(raw, "\n") != 1 {
			t.Fatalf("invalid plain result: %q", raw)
		}
		raw = strings.TrimPrefix(raw, prefix)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("decode %s result: %v: %s", mode, err, raw)
	}
	return data
}

func TestLocalDraftPreviewPreservesNestedInputs(t *testing.T) {
	for _, mode := range []string{"json", "plain", "human"} {
		for _, source := range []string{"body", "body-file", "stdin"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				c := newRegressionCLI(t)
				body := "Preview from " + source + "\nSecond line\twith a tab\n"
				args := []string{"draft", "create", "--to", "first@example.invalid", "--to", "second@example.invalid", "--subject", "Proposed subject", "--tag", "review", "--tag", "finance"}
				input := ""
				switch source {
				case "body":
					args = append(args, "--body", body)
				case "body-file":
					path := filepath.Join(filepath.Dir(c.config), "body.txt")
					if err := os.WriteFile(path, []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--body-file", path)
				case "stdin":
					args, input = append(args, "--stdin"), body
				}
				plan := localPreviewData(t, c, mode, input, append([]string{"--dry-run"}, args...)...)
				// Preserve the established consumer path, e.g. jq '.data.draft.to'.
				proposed, ok := plan["draft"].(map[string]any)
				if plan["action"] != "draft.create" || plan["wouldCreate"] != true || plan["source"] != "local" || plan["createPath"] != "local_state" ||
					!ok || !reflect.DeepEqual(proposed["to"], []any{"first@example.invalid", "second@example.invalid"}) || proposed["subject"] != "Proposed subject" || proposed["body"] != body || !reflect.DeepEqual(proposed["tags"], []any{"review", "finance"}) {
					t.Fatalf("preview lost proposed inputs: %+v", plan)
				}
				for _, key := range []string{"id", "draftId", "createdAt", "updatedAt"} {
					if _, exists := proposed[key]; exists {
						t.Fatalf("preview fabricated receipt field %s: %+v", key, plan)
					}
				}
				for _, key := range []string{"to", "subject", "body", "tags"} {
					if _, exists := plan[key]; exists {
						t.Fatalf("preview duplicated the nested %s input: %+v", key, plan)
					}
				}
				if _, err := os.Stat(c.state); !os.IsNotExist(err) {
					t.Fatalf("preview wrote state: %v", err)
				}
				created := localPreviewData(t, c, mode, input, args...)["draft"].(map[string]any)
				for _, key := range []string{"to", "subject", "body", "tags"} {
					if !reflect.DeepEqual(created[key], proposed[key]) {
						t.Fatalf("creation differs from preview for %s: plan=%+v draft=%+v", key, plan, created)
					}
				}
				if created["id"] == "" || created["id"] == nil {
					t.Fatalf("creation omitted the real receipt: %+v", created)
				}
			})
		}
	}
}
