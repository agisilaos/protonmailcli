package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type regressionCLI struct {
	t             *testing.T
	config, state string
}

func newRegressionCLI(t *testing.T) regressionCLI {
	t.Helper()
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	t.Setenv("PMAIL_SMTP_PASSWORD", "")
	dir := t.TempDir()
	c := regressionCLI{t: t, config: filepath.Join(dir, "config.toml"), state: filepath.Join(dir, "data", "state.json")}
	code, out := c.run("", "setup", "--non-interactive", "--username", "before@example.invalid", "--bridge-imap-port", "1", "--bridge-smtp-port", "1")
	if code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	return c
}

func (c regressionCLI) run(input string, args ...string) (int, string) {
	c.t.Helper()
	var out, stderr bytes.Buffer
	argv := append([]string{"--json", "--no-input", "--config", c.config, "--state", c.state}, args...)
	code := Run(argv, strings.NewReader(input), &out, &stderr)
	return code, out.String()
}

func regressionData(t *testing.T, out string) map[string]any {
	t.Helper()
	var result struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}

func TestInvalidExistingConfigurationIsNotReportedMissing(t *testing.T) {
	for _, source := range []string{"file", "environment"} {
		t.Run(source, func(t *testing.T) {
			c := newRegressionCLI(t)
			if source == "file" {
				if err := os.WriteFile(c.config, []byte("[safety]\nallow_force_send = invalid\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("PMAIL_TIMEOUT", "invalid")
			}
			code, out := c.run("", "auth", "status")
			if code != 3 || !strings.Contains(out, `"code":"config_error"`) || strings.Contains(out, "setup first") {
				t.Fatalf("invalid configuration was misdiagnosed: %d %s", code, out)
			}
		})
	}
}
