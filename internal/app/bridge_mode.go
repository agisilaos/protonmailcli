package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func useLocalStateMode() bool {
	return strings.TrimSpace(os.Getenv("PMAIL_USE_LOCAL_STATE")) == "1"
}

func resolveBridgeCredentials(cfg config.Config, st *model.State, passwordFileOverride string) (string, string, error) {
	username := firstNonEmpty(st.Auth.Username, cfg.Bridge.Username)
	if username == "" {
		return "", "", cliError{exit: 3, code: "config_error", msg: "bridge username is missing", hint: "Run setup or auth login"}
	}
	password := strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD"))
	passwordFile := firstNonEmpty(passwordFileOverride, st.Auth.PasswordFile, cfg.Bridge.PasswordFile)
	if password == "" && passwordFile != "" {
		b, err := os.ReadFile(filepath.Clean(config.Expand(passwordFile)))
		if err != nil {
			return "", "", cliError{exit: 2, code: "validation_error", msg: "cannot read smtp password file"}
		}
		password = strings.TrimSpace(string(b))
	}
	if password == "" {
		return "", "", cliError{exit: 3, code: "auth_missing", msg: "bridge password is missing", hint: "Set PMAIL_SMTP_PASSWORD or auth login --password-file"}
	}
	return username, password, nil
}

func bridgeClient(cfg config.Config, st *model.State, passwordFileOverride string) (*bridge.IMAPClient, string, string, error) {
	username, password, err := resolveBridgeCredentials(cfg, st, passwordFileOverride)
	if err != nil {
		return nil, "", "", err
	}
	timeout := 30 * time.Second
	if strings.TrimSpace(cfg.Timeout) != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	c, err := bridge.DialIMAP(bridge.IMAPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.IMAPPort, Username: username, Password: password}, timeout)
	if err != nil {
		return nil, "", "", cliError{exit: 4, code: "imap_connect_failed", msg: err.Error()}
	}
	return c, username, password, nil
}

func parseUID(id string) (string, error) {
	v := strings.TrimSpace(id)
	if v == "" {
		return "", fmt.Errorf("empty id")
	}
	parts := strings.Split(v, ":")
	if len(parts) == 3 && parts[0] == "imap" {
		return parts[2], nil
	}
	return v, nil
}

func imapDraftID(uid string) string {
	return "imap:Drafts:" + uid
}

func imapMessageID(uid string) string {
	return "imap:INBOX:" + uid
}
