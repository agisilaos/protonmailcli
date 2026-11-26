//go:build darwin || linux

package bridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTLSCertificateRejectsNamedPipePromptly(t *testing.T) {
	const helperPath = "PMAIL_TEST_TLS_CERT_FIFO"
	if path := os.Getenv(helperPath); path != "" {
		_, err := bridgeTLSConfig("IMAP", "127.0.0.1", path)
		if !errors.Is(err, ErrTLSConfig) || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("named pipe must be a configuration error, got %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "trust.pem")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	// A subprocess deadline keeps a blocking-open regression from hanging the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTLSCertificateRejectsNamedPipePromptly$")
	cmd.Env = append(os.Environ(), helperPath+"="+path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("certificate loading blocked on a named pipe with no writer: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("certificate loader: %v\n%s", err, out)
	}
}
