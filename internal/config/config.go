package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Profile string
	Output  string
	Timeout string
	Bridge  Bridge
	Safety  Safety
}

type Bridge struct {
	Host         string
	IMAPPort     int
	SMTPPort     int
	TLS          bool
	Username     string
	PasswordFile string
}

type Safety struct {
	RequireConfirmSendNonTTY bool
	AllowForceSend           bool
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

	section := ""
	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		switch section {
		case "defaults":
			switch k {
			case "profile":
				cfg.Profile = v
			case "output":
				cfg.Output = v
			case "timeout":
				cfg.Timeout = v
			}
		case "bridge":
			switch k {
			case "host":
				cfg.Bridge.Host = v
			case "imap_port":
				n, _ := strconv.Atoi(v)
				cfg.Bridge.IMAPPort = n
			case "smtp_port":
				n, _ := strconv.Atoi(v)
				cfg.Bridge.SMTPPort = n
			case "tls":
				cfg.Bridge.TLS = (v == "true")
			case "username":
				cfg.Bridge.Username = v
			case "password_file":
				cfg.Bridge.PasswordFile = v
			}
		case "safety":
			switch k {
			case "require_confirm_send_non_tty":
				cfg.Safety.RequireConfirmSendNonTTY = (v == "true")
			case "allow_force_send":
				cfg.Safety.AllowForceSend = (v == "true")
			}
		}
	}
	return cfg, s.Err()
}

func Save(path string, cfg Config) error {
	path = Expand(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`[defaults]
profile = "%s"
output = "%s"
timeout = "%s"

[bridge]
host = "%s"
imap_port = %d
smtp_port = %d
tls = %t
username = "%s"
password_file = "%s"

[safety]
require_confirm_send_non_tty = %t
allow_force_send = %t
`, cfg.Profile, cfg.Output, cfg.Timeout, cfg.Bridge.Host, cfg.Bridge.IMAPPort, cfg.Bridge.SMTPPort, cfg.Bridge.TLS, cfg.Bridge.Username, cfg.Bridge.PasswordFile, cfg.Safety.RequireConfirmSendNonTTY, cfg.Safety.AllowForceSend)
	return os.WriteFile(path, []byte(content), 0o600)
}
