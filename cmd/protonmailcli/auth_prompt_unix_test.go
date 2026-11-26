//go:build unix

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func TestInteractiveAuthPromptDelivery(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "protonmailcli")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name, username, input string
		closed                bool
	}{
		{name: "closed username prompt", closed: true},
		{name: "closed password-file prompt", username: "fixture@example.invalid", closed: true},
		{name: "normal answers", input: "\n"},
		{name: "final answer without newline", input: "\x04\x04"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.Default()
			cfg.Bridge.Username = tc.username
			cfgPath := filepath.Join(dir, "config.toml")
			if err := config.Save(cfgPath, cfg); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(dir, "state.json")
			const before = "{}\n"
			if err := os.WriteFile(statePath, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			passPath := filepath.Join(dir, "fixture.pass")
			if err := os.WriteFile(passPath, []byte("synthetic-only"), 0600); err != nil {
				t.Fatal(err)
			}
			input := ""
			if !tc.closed {
				input = "fixture@example.invalid\n" + passPath + tc.input
			}
			// A pseudo-terminal keeps stdin interactive and open while the prompt
			// pipe has no reader. A failed prompt must exit without waiting for input.
			const script = `
import json, os, pty, subprocess, sys
master, slave = pty.openpty()
closed = sys.argv[4] == "closed"
stderr = subprocess.PIPE
if closed:
    reader, stderr = os.pipe()
    os.close(reader)
env = {k: v for k, v in os.environ.items() if not k.startswith("PMAIL_")}
env["PMAIL_USE_LOCAL_STATE"] = "1"
process = subprocess.Popen([sys.argv[1], "--json", "--config", sys.argv[2], "--state", sys.argv[3], "auth", "login"], stdin=slave, stdout=subprocess.PIPE, stderr=stderr, env=env)
os.close(slave)
if closed:
    os.close(stderr)
else:
    os.write(master, sys.argv[5].encode())
timed_out = False
try:
    out, _ = process.communicate(timeout=2)
except subprocess.TimeoutExpired:
    timed_out = True
    process.kill()
    out, _ = process.communicate()
finally:
    os.close(master)
print(json.dumps({"exit": process.returncode, "timedOut": timed_out, "output": out.decode()}))
`
			mode := "open"
			if tc.closed {
				mode = "closed"
			}
			out, err := exec.CommandContext(t.Context(), "python3", "-c", script, binary, cfgPath, statePath, mode, input).CombinedOutput()
			if err != nil {
				t.Fatalf("terminal fixture: %v: %s", err, out)
			}
			var result struct {
				Exit     int
				TimedOut bool
				Output   string
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatalf("terminal result: %v: %s", err, out)
			}
			if result.TimedOut {
				t.Fatalf("auth waited for input after prompt delivery failed: %s", out)
			}
			if tc.closed {
				after, err := os.ReadFile(statePath)
				if result.Exit != 1 || !strings.Contains(result.Output, "write auth prompt") || err != nil || string(after) != before {
					t.Fatalf("closed prompt: result=%+v state=%q error=%v", result, after, err)
				}
			} else if result.Exit != 0 || !strings.Contains(result.Output, `"loggedIn":true`) {
				t.Fatalf("successful interactive login: %+v", result)
			}
		})
	}
}
