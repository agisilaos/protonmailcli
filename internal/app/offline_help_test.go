package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeafHelpBeforeConfigurationAndState(t *testing.T) {
	paths := []string{"setup", "completion", "draft list", "draft get", "draft create", "draft create-many", "draft update", "draft delete", "message get", "message send", "message send-many", "message follow-up", "mailbox list", "mailbox resolve", "search messages", "search drafts", "tag list", "tag create", "tag add", "tag remove", "filter list", "filter create", "filter delete", "filter test", "filter apply", "auth login", "auth status", "auth logout", "bridge account list", "bridge account use", "doctor"}
	for _, local := range []string{"", "1"} {
		t.Setenv("PMAIL_USE_LOCAL_STATE", local)
		dir := t.TempDir()
		cfg := filepath.Join(dir, "config.toml")
		state := filepath.Join(dir, "state.json")
		for _, present := range []bool{false, true} {
			if present {
				for _, p := range []string{cfg, state} {
					if err := os.WriteFile(p, []byte("invalid fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, path := range paths {
				for _, flag := range []string{"--help", "-h"} {
					var out, stderr bytes.Buffer
					args := append([]string{"--config", cfg, "--state", state, "--json"}, strings.Fields(path)...)
					args = append(args, flag)
					code := Run(args, strings.NewReader(""), &out, &stderr)
					var result struct {
						OK   bool `json:"ok"`
						Data struct {
							Help  string `json:"help"`
							Usage string `json:"usage"`
						} `json:"data"`
					}
					if code != 0 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &result) != nil || !result.OK || result.Data.Help != path || !strings.Contains(result.Data.Usage, "Example:") {
						t.Fatalf("local=%s present=%v %v code=%d out=%s err=%s", local, present, args, code, &out, &stderr)
					}
				}
			}
			for _, p := range []string{cfg, state} {
				b, err := os.ReadFile(p)
				if (!present && !os.IsNotExist(err)) || (present && (err != nil || string(b) != "invalid fixture")) {
					t.Fatalf("help changed %s: %q %v", p, b, err)
				}
			}
		}
	}
}

func TestOfflineHelpRespectsValuesAndModeFlags(t *testing.T) {
	for _, local := range []string{"", "1"} {
		t.Setenv("PMAIL_USE_LOCAL_STATE", local)
		for _, args := range [][]string{{"draft", "create", "--subject", "--help"}, {"draft", "create", "--subject=--help"}, {"draft", "create", "--", "--help"}, {"draft", "create", "value", "--help"}, {"draft", "create", "--unknown", "--help"}} {
			_, handled, err := offlineHelp(args, globalOptions{})
			if handled || err != nil {
				t.Fatalf("%v unexpectedly handled help: %v", args, err)
			}
		}
		fs := offlineHelpFlags("draft create", local == "1")
		if (fs.Lookup("tag") != nil) != (local == "1") || (fs.Lookup("idempotency-key") != nil) != (local != "1") {
			t.Fatalf("mode flag drift: %s", local)
		}
	}
}
