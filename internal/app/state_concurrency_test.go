package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/store"
)

type draftBodyReader func([]byte) (int, error)

func (r draftBodyReader) Read(p []byte) (int, error) { return r(p) }

func TestConcurrentStateWritersPreserveSuccessfulMutations(t *testing.T) {
	if os.Getenv("PMAIL_TEST_CONCURRENT_TAG") == "1" {
		os.Exit(Run([]string{"--json", "--config", os.Getenv("PMAIL_TEST_CONFIG"), "--state", os.Getenv("PMAIL_TEST_STATE"), "tag", "create", "--name", "finance"}, os.Stdin, os.Stdout, os.Stderr))
	}
	c := newRegressionCLI(t)
	var busy bool
	body := draftBodyReader(func(p []byte) (int, error) {
		// The draft command has loaded state and is now collecting its body.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentStateWritersPreserveSuccessfulMutations$")
		child.Env = append(os.Environ(), "PMAIL_TEST_CONCURRENT_TAG=1", "PMAIL_TEST_CONFIG="+c.config, "PMAIL_TEST_STATE="+c.state)
		out, err := child.CombinedOutput()
		if err != nil {
			if child.ProcessState == nil || child.ProcessState.ExitCode() != 6 || !bytes.Contains(out, []byte(`"code":"state_busy"`)) {
				t.Fatalf("concurrent tag create: %v %s", err, out)
			}
			busy = true
		}
		return copy(p, "draft body"), io.EOF
	})
	var out bytes.Buffer
	code := Run([]string{"--json", "--no-input", "--config", c.config, "--state", c.state, "draft", "create", "--stdin", "--to", "tester@example.invalid"}, body, &out, io.Discard)
	if code != 0 {
		t.Fatalf("draft create: %d %s", code, out.String())
	}
	if busy {
		if code, out := c.run("", "tag", "create", "--name", "finance"); code != 0 {
			t.Fatalf("retry after writer finished: %d %s", code, out)
		}
	}
	if code, out := c.run("", "tag", "list"); code != 0 || !strings.Contains(out, `"finance"`) {
		t.Fatalf("successful tag missing: %d %s", code, out)
	}
	if code, out := c.run("", "draft", "list"); code != 0 || !strings.Contains(out, `"draft body"`) {
		t.Fatalf("successful draft missing: %d %s", code, out)
	}
}

type receiptWriter func([]byte) (int, error)

func (w receiptWriter) Write(p []byte) (int, error) { return w(p) }

func TestStateLockReleasedBeforeWritingResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"saved", []string{"tag", "create", "--name", "finance"}, 0},
		{"rejected", []string{"tag", "create"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newRegressionCLI(t)
			var rendered bool
			out := receiptWriter(func(p []byte) (int, error) {
				rendered = true
				lock, err := store.New(c.state).LockWriter()
				if err != nil {
					t.Fatalf("output writer still owns the state lock: %v", err)
				}
				lock.Close()
				return len(p), nil
			})
			args := append([]string{"--json", "--config", c.config, "--state", c.state}, tc.args...)
			code := Run(args, strings.NewReader(""), out, io.Discard)
			if code != tc.want || !rendered {
				t.Fatalf("exit=%d rendered=%t", code, rendered)
			}
		})
	}
}
