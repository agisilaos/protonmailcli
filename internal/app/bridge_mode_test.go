package app

import (
	"testing"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func TestResolveBridgeCredentialsPrefersActiveBridgeAccount(t *testing.T) {
	t.Setenv("PMAIL_SMTP_PASSWORD", "secret")
	cfg := config.Default()
	cfg.Bridge.Username = "cfg@example.com"
	st := &model.State{
		Auth:   model.AuthState{Username: "auth@example.com"},
		Bridge: model.BridgeState{ActiveUsername: "active@example.com"},
	}
	user, _, err := resolveBridgeCredentials(cfg, st, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "active@example.com" {
		t.Fatalf("expected active username, got %s", user)
	}
}
