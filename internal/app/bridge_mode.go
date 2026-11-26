package app

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func useLocalStateMode() bool {
	return strings.TrimSpace(os.Getenv("PMAIL_USE_LOCAL_STATE")) == "1"
}

func effectiveBridgeUsername(cfg config.Config, st *model.State) string {
	return firstNonEmpty(st.Bridge.ActiveUsername, st.Auth.Username, cfg.Bridge.Username)
}

func (g globalOptions) checkExpectedAccount(cfg config.Config, st *model.State) error {
	if g.expectAccount != "" && effectiveBridgeUsername(cfg, st) != g.expectAccount {
		return cliError{exit: 2, code: "validation_error", msg: "the selected Bridge username does not match --expect-account", hint: "Check the active Bridge account in the selected config and state"}
	}
	return nil
}

func resolveBridgeCredentials(cfg config.Config, st *model.State, passwordFileOverride string) (string, string, error) {
	username := effectiveBridgeUsername(cfg, st)
	if username == "" {
		return "", "", cliError{exit: 3, code: "config_error", msg: "bridge username is missing", hint: "Run setup or auth login"}
	}
	password := strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD"))
	passwordFile := firstNonEmpty(passwordFileOverride, st.Auth.PasswordFile, cfg.Bridge.PasswordFile)
	if password == "" && passwordFile != "" {
		filePassword, err := readPasswordFile(passwordFile)
		if err != nil {
			return "", "", cliError{exit: 2, code: "validation_error", msg: "cannot read smtp password file"}
		}
		password = filePassword
	}
	if password == "" {
		return "", "", cliError{exit: 3, code: "auth_missing", msg: "bridge password is missing", hint: "Set PMAIL_SMTP_PASSWORD or auth login --password-file"}
	}
	return username, password, nil
}

func bridgeClient(cfg config.Config, st *model.State, passwordFileOverride string) (*bridge.IMAPClient, string, string, error) {
	return bridgeClientWithPolicy(cfg, st, passwordFileOverride, nil)
}

func bridgeClientWithPolicy(cfg config.Config, st *model.State, passwordFileOverride string, policy *bridge.TLSPolicy) (*bridge.IMAPClient, string, string, error) {
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
	c, err := bridge.DialIMAP(bridge.IMAPConfig{
		Host: cfg.Bridge.Host, Port: cfg.Bridge.IMAPPort,
		Username: username, Password: password,
		TLSCertFile: config.Expand(cfg.Bridge.TLSCertFile), TLSPolicy: policy,
	}, timeout)
	if err != nil {
		if errors.Is(err, bridge.ErrTLSConfig) {
			return nil, "", "", cliError{exit: 3, code: "config_error", msg: err.Error(), hint: "Check bridge.tls_cert_file and local certificate trust"}
		}
		var certificateError *tls.CertificateVerificationError
		if errors.As(err, &certificateError) {
			return nil, "", "", cliError{exit: 3, code: "config_error", msg: err.Error(), hint: "Check the Bridge certificate's validity, bridge.host hostname, and bridge.tls_cert_file trust"}
		}
		return nil, "", "", cliError{exit: 4, code: "imap_connect_failed", msg: err.Error()}
	}
	return c, username, password, nil
}

func parseUID(id string) (string, error) {
	v := strings.TrimSpace(id)
	if v == "" {
		return "", fmt.Errorf("empty id")
	}
	if isQualifiedIMAPID(v) {
		_, uid, err := parseMailboxUID(v, "Drafts")
		return uid, err
	}
	return v, nil
}

func imapMessageID(uid string) string {
	return imapMessageIDForMailbox("INBOX", uid)
}

func imapMessageIDForMailbox(mailbox, uid string) string {
	// The literal Drafts name must be distinguishable from older receipts that
	// used imap:Drafts as an alias for whichever mailbox currently has the role.
	if mailbox == "Drafts" {
		return "imap-v2:" + mailbox + ":" + uid
	}
	return imapMessagePayloadID(mailbox, uid)
}

// Keep canonical request hashes compatible with existing follow-up receipts.
func imapMessagePayloadID(mailbox, uid string) string {
	if mailbox == "" {
		mailbox = "INBOX"
	}
	return "imap:" + mailbox + ":" + uid
}

func isQualifiedIMAPID(id string) bool {
	v := strings.TrimSpace(id)
	return strings.HasPrefix(v, "imap:") || strings.HasPrefix(v, "imap-v2:")
}

func isLegacyDraftID(id string) bool {
	if strings.HasPrefix(strings.TrimSpace(id), "imap-v2:") {
		return false
	}
	mailbox, _, err := parseMailboxUID(id, "Drafts")
	return err == nil && mailbox == "Drafts"
}

func parseMailboxUID(id, defaultMailbox string) (string, string, error) {
	v := strings.TrimSpace(id)
	if v == "" {
		return "", "", fmt.Errorf("empty id")
	}
	if isQualifiedIMAPID(v) {
		prefix := "imap:"
		if strings.HasPrefix(v, "imap-v2:") {
			prefix = "imap-v2:"
		}
		split := strings.LastIndex(v, ":")
		if split <= len(prefix) || split == len(v)-1 {
			return "", "", fmt.Errorf("invalid imap id")
		}
		box, uid := v[len(prefix):split], v[split+1:]
		if err := validMessageUID(uid); err != nil {
			return "", "", err
		}
		return box, uid, nil
	}
	if err := validMessageUID(v); err != nil {
		return "", "", err
	}
	box := strings.TrimSpace(defaultMailbox)
	if box == "" {
		box = "INBOX"
	}
	return box, v, nil
}

func validMessageUID(uid string) error {
	n, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || n == 0 {
		return fmt.Errorf("invalid IMAP UID %q (expected positive integer)", uid)
	}
	return nil
}

func bridgeSMTPConfig(cfg config.Config, username, password string, policy *bridge.TLSPolicy) bridge.SMTPConfig {
	timeout := 30 * time.Second
	if parsed, err := time.ParseDuration(cfg.Timeout); err == nil && parsed > 0 {
		timeout = parsed
	}
	return bridge.SMTPConfig{
		Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Timeout: timeout,
		Username: username, Password: password,
		TLSCertFile: config.Expand(cfg.Bridge.TLSCertFile), TLSPolicy: policy,
	}
}
