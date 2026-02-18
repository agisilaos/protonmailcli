//go:build integration

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBridgeE2EDraftCreateSearchSend(t *testing.T) {
	if strings.TrimSpace(os.Getenv("PMAIL_E2E_BRIDGE")) != "1" {
		t.Skip("set PMAIL_E2E_BRIDGE=1 to run bridge integration tests")
	}

	username := strings.TrimSpace(os.Getenv("PMAIL_E2E_USERNAME"))
	password := strings.TrimSpace(os.Getenv("PMAIL_E2E_PASSWORD"))
	host := envOr("PMAIL_E2E_HOST", "127.0.0.1")
	imapPort := envIntOr("PMAIL_E2E_IMAP_PORT", 1143)
	smtpPort := envIntOr("PMAIL_E2E_SMTP_PORT", 1025)
	if username == "" || password == "" {
		t.Fatal("PMAIL_E2E_USERNAME and PMAIL_E2E_PASSWORD are required")
	}

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	passPath := filepath.Join(tmp, "bridge.pass")
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

	sendArgs := []string{
		"--json", "--no-input",
		"--config", cfgPath, "--state", statePath,
		"message", "send",
		"--draft-id", draftID,
		"--confirm-send", draftID,
		"--smtp-password-file", passPath,
	}
	if strings.TrimSpace(os.Getenv("PMAIL_E2E_REAL_SEND")) != "1" {
		sendArgs = append(sendArgs, "--dry-run")
	}
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
