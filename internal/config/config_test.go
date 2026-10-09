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
			TLSCertFile:  "~/bridge-cert.pem",
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

func TestLoadPreservesCommentedSafetyPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[defaults] # defaults comment
profile = "agent#1\"quoted" # not part of value
[bridge]
tls = true # keep transport secure
[safety]
require_confirm_send_non_tty = true # keep explicit confirmation
allow_force_send = false # forbid bypass
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Safety.RequireConfirmSendNonTTY || cfg.Safety.AllowForceSend || !cfg.Bridge.TLS {
		t.Fatalf("comments changed policy: %+v, TLS=%v", cfg.Safety, cfg.Bridge.TLS)
	}
	if cfg.Profile != "agent#1\"quoted" {
		t.Fatalf("incorrect quoted value: %q", cfg.Profile)
	}
}

func TestLoadRejectsInvalidSafetyBooleans(t *testing.T) {
	for _, value := range []string{"TRUE", `"true"`, "tru", "1"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[safety]\nrequire_confirm_send_non_tty = "+value+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("invalid safety value %q was accepted", value)
			}
		})
	}
}

func TestSaveLoadEscapedStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Profile = "agent \"quoted\" # profile"
	cfg.Bridge.PasswordFile = "C:\\secrets\\pass\nword"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != cfg {
		t.Fatalf("round trip altered configuration: got=%+v want=%+v", loaded, cfg)
	}
}

func TestLoadEnvironmentOverridesFileDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Profile, cfg.Output, cfg.Timeout = "file-profile", "plain", "12s"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PMAIL_PROFILE", "environment-profile")
	t.Setenv("PMAIL_OUTPUT", "json")
	t.Setenv("PMAIL_TIMEOUT", "100ms")
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile != "environment-profile" || loaded.Output != "json" || loaded.Timeout != "100ms" {
		t.Fatalf("environment overrides ignored: %+v", loaded)
	}
	if Default().Profile != "default" {
		t.Fatal("environment affected persisted setup defaults")
	}
}

func TestLoadRejectsInvalidEnvironmentOverrides(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"PMAIL_OUTPUT", "xml"}, {"PMAIL_TIMEOUT", "not-a-duration"}, {"PMAIL_TIMEOUT", "0s"}, {"PMAIL_TIMEOUT", "-1s"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := Save(path, Default()); err != nil {
				t.Fatal(err)
			}
			t.Setenv(tc.key, tc.value)
			if _, err := Load(path); err == nil {
				t.Fatalf("invalid %s value accepted: %q", tc.key, tc.value)
			}
		})
	}
}
