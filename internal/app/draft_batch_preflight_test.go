package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type forbiddenBatchInput struct{ reads int }

func (r *forbiddenBatchInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("input must not be read")
}

func TestDraftBatchOperationConflictPrecedesBodyReads(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	state := `{"idempotency":{"other-operation":{"operation":"message.send","status":"complete","payloadHash":"old"}}}`
	if err := os.WriteFile(args[4], []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "drafts.json")
	if err := os.WriteFile(manifest, []byte(`[{"to":["recipient@example.invalid"],"body_file":"-"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	input := &forbiddenBatchInput{}
	var out, stderr bytes.Buffer
	code := Run(append(args, "draft", "create-many", "--file", manifest, "--idempotency-key", "other-operation"), input, &out, &stderr)
	commands, _ := s.snapshot()
	if code != 6 || !strings.Contains(out.String(), `"code":"idempotency_conflict"`) || input.reads != 0 || len(commands) != 0 {
		t.Fatalf("operation conflict consumed input or contacted server: exit=%d reads=%d commands=%v out=%s", code, input.reads, commands, out.String())
	}
}
