//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBodyFileFollowsSymlinkBeforeParentComponent(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree, "child"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{filepath.Join(dir, "body"): "other", filepath.Join(tree, "body"): "requested"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := loadBody("", dir+"/link/../body", false)
	if err != nil || body != "requested" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}
