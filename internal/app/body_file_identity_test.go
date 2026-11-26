package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftItemKeyDistinguishesInvalidUTF8BodyBytes(t *testing.T) {
	server := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, server)), "draft", "create-many", "--stdin")
	file := filepath.Join(t.TempDir(), "body")
	data, err := json.Marshal([]draftCreateItem{{To: []string{"fixture@example.invalid"}, BodyFile: file, IdempotencyKey: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		b        byte
		conflict bool
	}{{0xff, false}, {0xff, false}, {0xfe, true}} {
		if err := os.WriteFile(file, []byte{step.b}, 0600); err != nil {
			t.Fatal(err)
		}
		code, out := runWorkflow(t, args, string(data))
		if step.conflict != strings.Contains(out, "idempotency_conflict") || (code == 0) == step.conflict {
			t.Fatalf("byte=%x exit=%d out=%s", step.b, code, out)
		}
	}
	_, appended := server.snapshot()
	if len(appended) != 1 {
		t.Fatalf("appends=%d", len(appended))
	}
}
