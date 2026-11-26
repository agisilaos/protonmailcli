//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCredentialChecksRejectNonRegularPasswordFiles(t *testing.T) {
	for _, command := range []string{"login", "doctor"} {
		t.Run(command, func(t *testing.T) {
			c := newRegressionCLI(t)
			path := filepath.Join(t.TempDir(), "password-fifo")
			if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"auth", "login", "--password-file", path}
			if command == "doctor" {
				if code, out := c.run("", "setup", "--username", "user@example.invalid", "--smtp-password-file", path, "--bridge-imap-port", "1", "--bridge-smtp-port", "1"); code != 0 {
					t.Fatalf("setup: %d %s", code, out)
				}
				args = []string{"doctor"}
			}
			done := make(chan int, 1)
			go func() { code, _ := c.run("", args...); done <- code }()
			select {
			case code := <-done:
				want := 2
				if command == "doctor" {
					want = 3
				}
				if code != want {
					t.Fatalf("FIFO password file: exit %d, want %d", code, want)
				}
			case <-time.After(500 * time.Millisecond):
				// Release the old blocking reader so a regression cannot hang tests.
				f, err := os.OpenFile(path, os.O_WRONLY|unix.O_NONBLOCK, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("synthetic-password")
				_ = f.Close()
				<-done
				t.Fatal("credential check blocked on a named pipe")
			}
		})
	}
}
