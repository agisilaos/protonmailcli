//go:build unix

package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"protonmailcli/internal/store"
)

func TestRetargetingStateAliasKeepsLockedDraft(t *testing.T) {
	c := newRegressionCLI(t)
	original, other := t.TempDir(), t.TempDir()
	route := filepath.Join(t.TempDir(), "active")
	if err := os.Symlink(original, route); err != nil {
		t.Fatal(err)
	}
	stateName := "state.json"
	unrelated := []byte("{\"tags\":{\"other\":\"untouched\"}}\n")
	if err := os.WriteFile(filepath.Join(other, stateName), unrelated, 0600); err != nil {
		t.Fatal(err)
	}
	body := draftBodyReader(func(p []byte) (int, error) {
		next := route + "-next"
		if err := os.Symlink(other, next); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, route); err != nil {
			t.Fatal(err)
		}
		return copy(p, "locked draft"), io.EOF
	})
	var out bytes.Buffer
	args := []string{"--json", "--config", c.config, "--state", filepath.Join(route, stateName), "draft", "create", "--stdin", "--to", "test@example.invalid"}
	if code := Run(args, body, &out, io.Discard); code != 0 {
		t.Fatalf("create: %d %s", code, out.String())
	}
	saved, err := store.New(filepath.Join(original, stateName)).Load()
	if err != nil || len(saved.Drafts) != 1 {
		t.Errorf("locked directory has %d drafts: %v", len(saved.Drafts), err)
	}
	actual, err := os.ReadFile(filepath.Join(other, stateName))
	if err != nil || !bytes.Equal(actual, unrelated) {
		t.Errorf("other directory changed: %s %v", actual, err)
	}
}
