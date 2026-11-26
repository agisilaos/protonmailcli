//go:build unix

package main

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func TestDoctorKeepsExitCodeWhenReaderClosesAfterSummary(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "protonmailcli")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	cfg := config.Default()
	cfg.Bridge.Username = "fixture@example.invalid"
	// The oversized synthetic hostname fails DNS validation locally. Its echo in
	// the report exceeds the pipe buffer, so the writer cannot finish first.
	cfg.Bridge.Host = strings.Repeat("x", 1<<20) + ".invalid"
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), binary, "--config", cfgPath, "--state", filepath.Join(dir, "state.json"), "doctor")
	cmd.Env = []string{"PMAIL_SMTP_PASSWORD=fixture"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	summary, readErr := bufio.NewReader(out).ReadString('\n')
	out.Close()
	err = cmd.Wait()
	if readErr != nil || !strings.Contains(summary, "(bridge_unreachable)") {
		t.Fatalf("doctor error summary=%q read error=%v", summary, readErr)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 || !strings.Contains(stderr.String(), "stdout write failed") {
		t.Fatalf("wait=%v stderr=%q", err, &stderr)
	}
}
