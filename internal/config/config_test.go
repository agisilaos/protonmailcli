package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigValues(t *testing.T) {
	cfg := Default()
	if cfg.Profile != "default" {
		t.Fatalf("unexpected profile: %q", cfg.Profile)
	}
	if cfg.Output != "human" {
		t.Fatalf("unexpected output: %q", cfg.Output)
	}
	if cfg.Bridge.Host != "127.0.0.1" || cfg.Bridge.IMAPPort != 1143 || cfg.Bridge.SMTPPort != 1025 {
		t.Fatalf("unexpected bridge defaults: %+v", cfg.Bridge)
	}
	if !cfg.Safety.RequireConfirmSendNonTTY || !cfg.Safety.AllowForceSend {
		t.Fatalf("unexpected safety defaults: %+v", cfg.Safety)
	}
}

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	got := Expand("~/test/protonmailcli")
	want := filepath.Join(home, "test", "protonmailcli")
	if got != want {
		t.Fatalf("unexpected expanded path: got=%q want=%q", got, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	cfg := Config{
		Profile: "agent",
		Output:  "json",
		Timeout: "15s",
		Bridge: Bridge{
			Host:         "localhost",
			IMAPPort:     2993,
			SMTPPort:     2025,
			TLS:          false,
			Username:     "me@example.com",
			PasswordFile: "~/secret.pass",
		},
		Safety: Safety{
			RequireConfirmSendNonTTY: false,
			AllowForceSend:           true,
		},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Profile != cfg.Profile || loaded.Output != cfg.Output || loaded.Timeout != cfg.Timeout {
		t.Fatalf("unexpected defaults section: %+v", loaded)
	}
	if loaded.Bridge != cfg.Bridge {
		t.Fatalf("unexpected bridge section: got=%+v want=%+v", loaded.Bridge, cfg.Bridge)
	}
	if loaded.Safety != cfg.Safety {
		t.Fatalf("unexpected safety section: got=%+v want=%+v", loaded.Safety, cfg.Safety)
	}
}

func TestDefaultPathsRespectXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))

	cfgPath := DefaultConfigPath()
	statePath := DefaultStatePath()

	if !strings.HasPrefix(cfgPath, filepath.Join(tmp, "config")) {
		t.Fatalf("unexpected config path: %s", cfgPath)
	}
	if !strings.HasPrefix(statePath, filepath.Join(tmp, "data")) {
		t.Fatalf("unexpected state path: %s", statePath)
	}
}
