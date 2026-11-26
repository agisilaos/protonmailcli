//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupStopsAtPartialTerminalAnswer(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "protonmailcli")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	cfg := filepath.Join(dir, "config.toml")
	const before = "[defaults]\nprofile = 'existing'\n"
	if err := os.WriteFile(cfg, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	// On a canonical terminal the first Ctrl-D delivers the typed characters.
	// The second delivers EOF once. The master stays open, as in a user's shell.
	const script = `
import os, pty, subprocess, sys
master, slave = pty.openpty()
proc = subprocess.Popen([sys.argv[1], "--json", "--config", sys.argv[2], "setup", "--interactive"],
                        stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
os.close(slave)
try:
    os.write(master, b"unfinished profile\x04\x04")
    try:
        out, prompts = proc.communicate(timeout=3)
    except subprocess.TimeoutExpired:
        proc.kill()
        out, prompts = proc.communicate()
        sys.exit("setup waited for more input after EOF: " + prompts.decode())
    if proc.returncode != 1 or b"read setup input" not in out:
        sys.exit("unexpected exit %d: %r" % (proc.returncode, out))
    sys.stdout.buffer.write(prompts)
finally:
    os.close(master)
`
	prompts, err := exec.CommandContext(t.Context(), "python3", "-c", script, binary, cfg).CombinedOutput()
	if err != nil || !strings.Contains(string(prompts), "Profile [default]") || strings.Contains(string(prompts), "Bridge host") {
		t.Fatalf("incomplete terminal answer: %v: %s", err, prompts)
	}
	after, err := os.ReadFile(cfg)
	if err != nil || string(after) != before {
		t.Fatalf("setup changed existing config: %v: %q", err, after)
	}
}
