//go:build integration

package app

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
)

func TestBridgeE2EDraftCreateSearchSend(t *testing.T) {
	if strings.TrimSpace(os.Getenv("PMAIL_E2E_BRIDGE")) != "1" {
		t.Skip("set PMAIL_E2E_BRIDGE=1 to run bridge integration tests")
	}

	username := strings.TrimSpace(os.Getenv("PMAIL_E2E_USERNAME"))
	password := strings.TrimSpace(os.Getenv("PMAIL_E2E_PASSWORD"))
	if username == "" || password == "" {
		t.Fatal("PMAIL_E2E_USERNAME and PMAIL_E2E_PASSWORD are required")
	}

	cfgPath, statePath, passPath := prepareBridgeE2E(t, username, password)
	mustRunJSON(t, []string{
		"--json", "--no-input",
		"--config", cfgPath, "--state", statePath,
		"auth", "login",
		"--username", username,
		"--password-file", passPath,
	})

	token := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	subject := "pmailcli integration " + token
	create := mustRunJSON(t, []string{
		"--json", "--no-input",
		"--config", cfgPath, "--state", statePath,
		"draft", "create",
		"--to", username,
		"--subject", subject,
		"--body", "integration body",
	})
	draftID := digString(create, "data", "draft", "id")
	if draftID == "" {
		t.Fatalf("missing draft id in response: %#v", create)
	}

	deadline := time.Now().Add(15 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		search := mustRunJSON(t, []string{
			"--json", "--no-input",
			"--config", cfgPath, "--state", statePath,
			"search", "drafts",
			"--query", token,
			"--limit", "50",
		})
		if hasDraftID(search, draftID) {
			found = true
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if !found {
		t.Fatalf("draft %s not found via search", draftID)
	}

	sendArgs := bridgeE2ESendArgs(cfgPath, statePath, draftID, passPath, strings.TrimSpace(os.Getenv("PMAIL_E2E_REAL_SEND")) == "1")
	send := mustRunJSON(t, sendArgs)
	if strings.TrimSpace(os.Getenv("PMAIL_E2E_REAL_SEND")) == "1" {
		if sent, _ := dig(send, "data", "sent").(bool); !sent {
			t.Fatalf("expected sent response: %#v", send)
		}
	} else {
		if wouldSend, _ := dig(send, "data", "wouldSend").(bool); !wouldSend {
			t.Fatalf("expected dry-run send response: %#v", send)
		}
	}
}

func prepareBridgeE2E(t *testing.T, username, password string) (cfgPath, statePath, passPath string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("PMAIL_E2E_REAL_SEND")) != "1" {
		previous := smtpSendFn
		t.Cleanup(func() { smtpSendFn = previous })
		smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
			return errors.New("Bridge rejected draft creation by IMAP; SMTP fallback requires PMAIL_E2E_REAL_SEND=1")
		}
	}
	host := envOr("PMAIL_E2E_HOST", "127.0.0.1")
	imapPort := envIntOr("PMAIL_E2E_IMAP_PORT", 1143)
	smtpPort := envIntOr("PMAIL_E2E_SMTP_PORT", 1025)
	tmp := t.TempDir()
	cfgPath = filepath.Join(tmp, "config.toml")
	statePath = filepath.Join(tmp, "state.json")
	passPath = filepath.Join(tmp, "bridge.pass")
	if err := os.WriteFile(passPath, []byte(password), 0o600); err != nil {
		t.Fatalf("write pass file: %v", err)
	}

	mustRunJSON(t, []string{
		"--json", "--no-input",
		"--config", cfgPath, "--state", statePath,
		"setup", "--non-interactive",
		"--username", username,
		"--smtp-password-file", passPath,
		"--bridge-host", host,
		"--bridge-imap-port", strconv.Itoa(imapPort),
		"--bridge-smtp-port", strconv.Itoa(smtpPort),
	})
	if certificateFile := strings.TrimSpace(os.Getenv("PMAIL_E2E_TLS_CERT_FILE")); certificateFile != "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("load E2E configuration: %v", err)
		}
		cfg.Bridge.TLSCertFile = config.Expand(certificateFile)
		if err := config.Save(cfgPath, cfg); err != nil {
			t.Fatalf("save E2E certificate trust: %v", err)
		}
	}
	return cfgPath, statePath, passPath
}

func TestBridgeE2EExplicitCertificateConfiguration(t *testing.T) {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "1")
	t.Setenv("PMAIL_E2E_HOST", "127.0.0.1")
	t.Setenv("PMAIL_E2E_IMAP_PORT", "1")
	t.Setenv("PMAIL_E2E_SMTP_PORT", "1")
	t.Setenv("HOME", t.TempDir())
	for _, certificateFile := range []string{"", filepath.Join(t.TempDir(), "bridge.pem"), "~/bridge.pem"} {
		t.Run(certificateFile, func(t *testing.T) {
			t.Setenv("PMAIL_E2E_TLS_CERT_FILE", certificateFile)
			cfgPath, _, _ := prepareBridgeE2E(t, "fixture@example.invalid", "synthetic-only")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Bridge.TLSCertFile != config.Expand(certificateFile) {
				t.Fatalf("prepared E2E configuration trust file = %q, want %q", cfg.Bridge.TLSCertFile, config.Expand(certificateFile))
			}
		})
	}
}

func mustRunJSON(t *testing.T, args []string) map[string]any {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := Run(args, bytes.NewBuffer(nil), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("command failed exit=%d args=%v stdout=%s stderr=%s", exit, args, stdout.String(), stderr.String())
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("invalid json args=%v stdout=%s err=%v", args, stdout.String(), err)
	}
	return env
}

func hasDraftID(env map[string]any, draftID string) bool {
	v := dig(env, "data", "drafts")
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["id"].(string); id == draftID {
			return true
		}
	}
	return false
}

func dig(root map[string]any, keys ...string) any {
	var cur any = root
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func digString(root map[string]any, keys ...string) string {
	v, _ := dig(root, keys...).(string)
	return v
}

func envOr(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func envIntOr(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func bridgeE2ESendArgs(cfgPath, statePath, draftID, passPath string, realSend bool) []string {
	args := []string{
		"--json", "--no-input",
		"--config", cfgPath, "--state", statePath,
		"message", "send",
		"--draft-id", draftID,
		"--confirm-send", draftID,
		"--smtp-password-file", passPath,
	}
	if !realSend {
		args = append([]string{"--dry-run"}, args...)
	}
	return args
}

func TestBridgeE2ESafeSendArguments(t *testing.T) {
	c := newRegressionCLI(t)
	code, out := c.run("", "draft", "create", "--to", "recipient@example.invalid", "--body", "safe preview")
	if code != 0 {
		t.Fatalf("create: %d %s", code, out)
	}
	id := regressionData(t, out)["draft"].(map[string]any)["id"].(string)
	result := mustRunJSON(t, bridgeE2ESendArgs(c.config, c.state, id, "unused-local-password", false))
	if dig(result, "data", "wouldSend") != true {
		t.Fatalf("expected dry-run plan: %#v", result)
	}
	code, out = c.run("", "search", "messages")
	if code != 0 || regressionData(t, out)["count"] != float64(0) {
		t.Fatalf("preview created message: %d %s", code, out)
	}
}

func TestBridgeE2EFallbackRequiresExplicitRealSend(t *testing.T) {
	for _, optIn := range []string{"", "1"} {
		t.Run("opt-in="+optIn, func(t *testing.T) {
			t.Setenv("PMAIL_E2E_REAL_SEND", optIn)
			t.Setenv("PMAIL_USE_LOCAL_STATE", "0")
			t.Setenv("PMAIL_SMTP_PASSWORD", "")
			t.Setenv("PMAIL_E2E_HOST", "127.0.0.1")
			server := &workflowServer{appendFailure: true}
			endpoint := newWorkflowServer(t, server)
			t.Setenv("PMAIL_E2E_IMAP_PORT", strconv.Itoa(endpoint.port))
			t.Setenv("PMAIL_E2E_SMTP_PORT", "1")
			cert := filepath.Join(t.TempDir(), "fixture.pem")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: endpoint.certificate}), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PMAIL_E2E_TLS_CERT_FILE", cert)
			calls := 0
			oldSend := smtpSendFn
			t.Cleanup(func() { smtpSendFn = oldSend })
			smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
				calls++
				return errors.New("synthetic SMTP rejected")
			}
			cfg, state, _ := prepareBridgeE2E(t, "fixture@example.invalid", "synthetic-only")
			code, out := runWorkflow(t, []string{"--json", "--config", cfg, "--state", state, "draft", "create",
				"--to", "fixture@example.invalid", "--body", "fixture"}, "")
			commands, _ := server.snapshot()
			if code != 4 || !strings.Contains(strings.Join(commands, "\n"), " APPEND ") ||
				(optIn == "" && (calls != 0 || !strings.Contains(out, "PMAIL_E2E_REAL_SEND"))) || (optIn == "1" && calls != 1) {
				t.Fatalf("opt-in=%q, exit=%d SMTP calls=%d output=%s", optIn, code, calls, out)
			}
		})
	}
}
