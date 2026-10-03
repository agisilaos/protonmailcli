//go:build darwin || linux

package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFailedSavePreservesPreviousFile(t *testing.T) {
	path := os.Getenv("AUDIT_STATE_PATH")
	if path != "" {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 128, Max: 128}); err != nil {
			t.Fatal(err)
		}
		s := New(path)
		state := emptyState()
		state.Tags["audit"] = strings.Repeat("synthetic", 100)
		err := s.Save(state)
		if err == nil {
			t.Fatal("expected file-size failure")
		}
		fmt.Println("injected save error:", err)
		return
	}
	t.Setenv("HOME", t.TempDir())
	path = filepath.Join(t.TempDir(), "state.json")

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"tags":{"existing":"retained"}}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFailedSavePreservesPreviousFile$", "-test.v")
	cmd.Env = append(os.Environ(), "AUDIT_STATE_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("previous file destroyed after failed save: before=%d bytes after=%d bytes validJSON=%v; %s", len(before), len(after), json.Valid(after), out)
	}
}
