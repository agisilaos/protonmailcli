package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"protonmailcli/internal/config"
)

func TestRecoveryCommandsPreserveAccountContext(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell required to verify copy-and-paste commands")
	}
	binDir := buildWorkflowCLI(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, mode := range []string{"explicit", "defaults", "send credential", "batch credential", "config password", "state password", "config certificate"} {
		t.Run(mode, func(t *testing.T) {
			s := &workflowServer{listRows: []string{`* LIST (\Drafts) "/" "Projet d'été"`, `* LIST () "/" "Drafts"`}}
			fixture := imapCommandArgs(t, newWorkflowServer(t, s))
			root := filepath.Join(t.TempDir(), "original directory's $files")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "account's config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(root, "account's data"))
			profile := "team's $profile"
			var args []string
			configPath, statePath := config.DefaultConfigPath(), config.DefaultStatePath()
			if mode != "defaults" {
				configPath, statePath = "account's $cfg.toml", "account's $state.json"
				if runtime.GOOS != "windows" {
					// On Unix this spelling resolves inside nested. Lexical cleaning
					// would silently read a different account state.
					if err := os.MkdirAll("nested/child", 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("nested/child", "route"); err != nil {
						t.Fatal(err)
					}
					statePath = "route/../" + statePath
				}
				args = []string{"--config", configPath, "--state", statePath, "--profile", profile}
			} else {
				t.Setenv("PMAIL_PROFILE", profile)
			}
			for _, path := range []string{configPath, statePath} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(fixture[2], configPath); err != nil {
				t.Fatal(err)
			}
			state := `{"bridge":{"activeUsername":"selected@example.invalid"}}`
			switch mode {
			case "config password", "state password", "config certificate":
				cfg, err := config.Load(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "config certificate" {
					name := "Bridge's $trust.pem"
					if err := os.Rename(cfg.Bridge.TLSCertFile, name); err != nil {
						t.Fatal(err)
					}
					cfg.Bridge.TLSCertFile = name
				} else {
					t.Setenv("PMAIL_SMTP_PASSWORD", "")
					name := "Bridge's $credential"
					if err := os.WriteFile(name, []byte("fixture-file-only-password"), 0600); err != nil {
						t.Fatal(err)
					}
					if mode == "config password" {
						cfg.Bridge.PasswordFile = name
					} else {
						state = `{"bridge":{"activeUsername":"selected@example.invalid"},"auth":{"passwordFile":"Bridge's $credential"}}`
					}
				}
				if err := config.Save(configPath, cfg); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(statePath, []byte(state), 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--json", "--dry-run")
			input := ""
			if mode == "send credential" || mode == "batch credential" {
				t.Setenv("PMAIL_SMTP_PASSWORD", "")
				const keyFile = "Bridge's $credential"
				if err := os.WriteFile(keyFile, []byte("fixture-file-only-password"), 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "send credential" {
					args = append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41", "--smtp-password-file", keyFile)
				} else {
					args = append(args, "message", "send-many", "--stdin", "--smtp-password-file", keyFile)
					input = `[{"draft_id":"41","confirm_send":"41"}]`
				}
			} else {
				args = append(args, "draft", "get", "--draft-id", "41")
			}
			code, out := runWorkflow(t, args, input)
			var failure struct {
				Error struct{ Message, Hint string }
				Data  batchResultResponse
			}
			if err := json.Unmarshal([]byte(out), &failure); err != nil {
				t.Fatal(err)
			}
			if mode == "batch credential" && code == 1 && len(failure.Data.Results) == 1 &&
				failure.Data.Results[0].ErrorCode == "validation_error" {
				failure.Error.Message = failure.Data.Results[0].Error
			} else if code != 2 {
				t.Fatalf("expected ambiguous legacy ID: exit=%d output=%s", code, out)
			}
			guidance := failure.Error.Message + "\n" + failure.Error.Hint
			if strings.Contains(guidance, "fixture-file-only-password") || strings.Contains(guidance, "synthetic-only") || strings.Contains(guidance, "selected@example.invalid") {
				t.Fatalf("recovery disclosed credentials: %s", guidance)
			}
			// Replay from another directory with unrelated defaults. The command
			// must select the original config, active account state, and profile.
			t.Chdir(t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("PMAIL_PROFILE", "other")
			seen := map[string]bool{}
			for _, line := range strings.Split(guidance, "\n") {
				if !strings.Contains(line, "protonmailcli --json ") {
					continue
				}
				// Execute the suggested command exactly as pasted in the new shell.
				out, err := exec.Command(shell, "-c", line).CombinedOutput()
				if err != nil {
					t.Fatalf("suggested command %q: %v: %s", line, err, out)
				}
				var read struct {
					Data struct{ Message struct{ ID string } }
					Meta struct{ Profile string }
				}
				if json.Unmarshal(out, &read) != nil || read.Meta.Profile != profile {
					t.Fatalf("suggested command %q: output=%s", line, out)
				}
				seen[read.Data.Message.ID] = true
			}
			commands, _ := s.snapshot()
			if !seen["imap-v2:Drafts:41"] || !seen["imap:Projet d'été:41"] || len(seen) != 2 ||
				strings.Count(strings.Join(commands, "\n"), `LOGIN "selected@example.invalid"`) != 3 {
				t.Fatalf("exact account inspections: ids=%v guidance=%s", seen, guidance)
			}
		})
	}
}
