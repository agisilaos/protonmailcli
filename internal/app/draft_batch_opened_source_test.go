package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type syntheticBodyFile struct {
	io.Reader
	info   os.FileInfo
	closed *int
}

func (f *syntheticBodyFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *syntheticBodyFile) Close() error {
	if f.closed != nil {
		*f.closed++
	}
	return nil
}

type sourceModeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (i sourceModeInfo) Mode() os.FileMode { return i.mode }

func TestKeyedDraftBatchCachesOnlyRegularOpenedSources(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create-many", "--stdin", "--idempotency-key", "opened-source")
	path := filepath.Join(t.TempDir(), "literal-path.txt")
	if err := os.WriteFile(path, []byte("literal path is regular"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	openFile := openBodyFileFn
	t.Cleanup(func() { openBodyFileFn = openFile })
	opens, closes := 0, 0
	openBodyFileFn = func(string) (bodyFile, error) {
		opens++
		// The opened object can differ from a pathname stat after resolution or
		// replacement. Model a separately consumed stream behind that path.
		return &syntheticBodyFile{Reader: strings.NewReader(fmt.Sprintf("stream %d", opens)), info: sourceModeInfo{FileInfo: info, mode: os.ModeNamedPipe}, closed: &closes}, nil
	}
	manifest, err := json.Marshal([]map[string]any{
		{"to": []string{"first@example.invalid"}, "body_file": path},
		{"to": []string{"second@example.invalid"}, "body_file": path},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := runWorkflow(t, args, string(manifest))
	_, appended := s.snapshot()
	if code != 0 || opens != 2 || closes != 2 || len(appended) != 2 || !strings.HasSuffix(appended[0], "stream 1") || !strings.HasSuffix(appended[1], "stream 2") {
		t.Fatalf("nonregular opened source was reused: exit=%d opens=%d closes=%d appends=%v out=%s", code, opens, closes, appended, out)
	}
}
