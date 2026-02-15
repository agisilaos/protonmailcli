package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

type contractFixture struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Expected struct {
		ExitCode          int             `json:"exitCode"`
		StdoutJSON        json.RawMessage `json:"stdoutJson"`
		StderrContains    []string        `json:"stderrContains"`
		StderrNotContains []string        `json:"stderrNotContains"`
	} `json:"expected"`
}

func TestContractFixtures(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	files, err := filepath.Glob(filepath.Join(root, "tests", "contracts", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no contract fixtures found")
	}

	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var fx contractFixture
			if err := json.Unmarshal(b, &fx); err != nil {
				t.Fatal(err)
			}

			tmp := t.TempDir()
			cfg := filepath.Join(tmp, "config.toml")
			statePath := filepath.Join(tmp, "state.json")

			setupExit := Run([]string{"--json", "--config", cfg, "--state", statePath, "setup", "--non-interactive", "--username", "agent@example.com"}, bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
			if setupExit != 0 {
				t.Fatalf("setup failed: %d", setupExit)
			}
			seedContractState(t, statePath)

			args := parseFixtureArgs(fx.Command)
			args = normalizeGlobalFlags(args)
			args = append([]string{"--config", cfg, "--state", statePath}, args...)

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			exit := Run(args, bytes.NewBuffer(nil), stdout, stderr)
			if exit != fx.Expected.ExitCode {
				t.Fatalf("exit mismatch: got=%d want=%d stdout=%s stderr=%s", exit, fx.Expected.ExitCode, stdout.String(), stderr.String())
			}

			if len(fx.Expected.StdoutJSON) > 0 {
				var expected any
				var got any
				if err := json.Unmarshal(fx.Expected.StdoutJSON, &expected); err != nil {
					t.Fatalf("invalid expected stdoutJson: %v", err)
				}
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
					t.Fatalf("stdout is not valid json: %v\nstdout=%s", err, stdout.String())
				}
				if ok, path, reason := subsetMatch(expected, got, "$"); !ok {
					t.Fatalf("stdout json mismatch at %s: %s\nstdout=%s", path, reason, stdout.String())
				}
			}

			for _, s := range fx.Expected.StderrContains {
				if !strings.Contains(stderr.String(), s) {
					t.Fatalf("stderr missing %q. stderr=%s", s, stderr.String())
				}
			}
			for _, s := range fx.Expected.StderrNotContains {
				if strings.Contains(stderr.String(), s) {
					t.Fatalf("stderr contains forbidden %q. stderr=%s", s, stderr.String())
				}
			}
		})
	}
}

func seedContractState(t *testing.T, statePath string) {
	t.Helper()
	now := time.Now().UTC()
	st := model.State{
		Drafts: map[string]model.Draft{
			"d_123": {
				ID:        "d_123",
				To:        []string{"a@example.com"},
				Subject:   "fixture",
				Body:      "fixture body",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Messages: map[string]model.Message{
			"m_123": {
				ID:      "m_123",
				From:    "sender@example.com",
				To:      []string{"a@example.com"},
				Subject: "fixture msg",
				Body:    "fixture",
				SentAt:  now,
			},
		},
		Tags:    map[string]string{},
		Filters: map[string]model.Filter{},
	}
	if err := store.New(statePath).Save(st); err != nil {
		t.Fatal(err)
	}
}

func parseFixtureArgs(cmd string) []string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}
	if parts[0] == "protonmailcli" || strings.HasSuffix(parts[0], "/protonmailcli") {
		return parts[1:]
	}
	return parts
}

func normalizeGlobalFlags(args []string) []string {
	globalNames := map[string]bool{
		"--json":     true,
		"--plain":    true,
		"--no-input": true,
		"--dry-run":  true,
		"-n":         true,
		"--profile":  true,
	}
	globalWithValue := map[string]bool{
		"--profile": true,
	}
	var globals []string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if globalNames[a] {
			globals = append(globals, a)
			if globalWithValue[a] && i+1 < len(args) {
				globals = append(globals, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(globals, rest...)
}

func subsetMatch(expected, got any, path string) (bool, string, string) {
	switch e := expected.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false, path, "type mismatch, expected object"
		}
		for k, ev := range e {
			gv, exists := g[k]
			if !exists {
				return false, path + "." + k, "missing key"
			}
			if ok, p, reason := subsetMatch(ev, gv, path+"."+k); !ok {
				return false, p, reason
			}
		}
		return true, "", ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return false, path, "type mismatch, expected array"
		}
		if len(e) > len(g) {
			return false, path, "expected array longer than got"
		}
		for i := range e {
			if ok, p, reason := subsetMatch(e[i], g[i], path); !ok {
				return false, p, reason
			}
		}
		return true, "", ""
	default:
		if expected != got {
			return false, path, "value mismatch"
		}
		return true, "", ""
	}
}
