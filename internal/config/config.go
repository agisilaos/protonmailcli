package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Profile string
	Output  string
	Timeout string
	Bridge  Bridge
	Safety  Safety
}

type Bridge struct {
	Host         string `toml:"host"`
	IMAPPort     int    `toml:"imap_port"`
	SMTPPort     int    `toml:"smtp_port"`
	TLS          bool   `toml:"tls"`
	Username     string `toml:"username"`
	PasswordFile string `toml:"password_file"`
	TLSCertFile  string `toml:"tls_cert_file"`
}

type Safety struct {
	RequireConfirmSendNonTTY bool `toml:"require_confirm_send_non_tty"`
	AllowForceSend           bool `toml:"allow_force_send"`
}

type fileDefaults struct {
	Profile string `toml:"profile"`
	Output  string `toml:"output"`
	Timeout string `toml:"timeout"`
}

type configFile struct {
	Defaults fileDefaults `toml:"defaults"`
	Bridge   Bridge       `toml:"bridge"`
	Safety   Safety       `toml:"safety"`
}

func asFile(cfg Config) configFile {
	return configFile{Defaults: fileDefaults{cfg.Profile, cfg.Output, cfg.Timeout}, Bridge: cfg.Bridge, Safety: cfg.Safety}
}

func Default() Config {
	return Config{
		Profile: "default",
		Output:  "human",
		Timeout: "30s",
		Bridge:  Bridge{Host: "127.0.0.1", IMAPPort: 1143, SMTPPort: 1025, TLS: true},
		Safety:  Safety{RequireConfirmSendNonTTY: true, AllowForceSend: true},
	}
}

func Expand(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "protonmailcli", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "protonmailcli", "config.toml")
}

func DefaultStatePath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "protonmailcli", "state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "protonmailcli", "state.json")
}

func Load(path string) (Config, error) {
	cfg := Default()
	path = Expand(path)
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	parsed := asFile(cfg)
	if err := toml.NewDecoder(file).Decode(&parsed); err != nil {
		return cfg, err
	}
	cfg = Config{Profile: parsed.Defaults.Profile, Output: parsed.Defaults.Output, Timeout: parsed.Defaults.Timeout, Bridge: parsed.Bridge, Safety: parsed.Safety}
	if profile := os.Getenv("PMAIL_PROFILE"); profile != "" {
		cfg.Profile = profile
	}
	if output := os.Getenv("PMAIL_OUTPUT"); output != "" {
		cfg.Output = output
	}
	if timeout := os.Getenv("PMAIL_TIMEOUT"); timeout != "" {
		cfg.Timeout = timeout
	}
	if cfg.Output != "" && cfg.Output != "human" && cfg.Output != "json" && cfg.Output != "plain" {
		return cfg, fmt.Errorf("invalid output %q: use human, json, or plain", cfg.Output)
	}
	if timeout, err := time.ParseDuration(cfg.Timeout); cfg.Timeout != "" && (err != nil || timeout <= 0) {
		return cfg, fmt.Errorf("invalid timeout %q: use a positive duration", cfg.Timeout)
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	path = Expand(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := toml.Marshal(asFile(cfg))
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
