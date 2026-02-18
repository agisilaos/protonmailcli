package app

import (
	"testing"

	"protonmailcli/internal/config"
)

func TestValidateSendSafetyRequiresConfirmInNonTTY(t *testing.T) {
	cfg := config.Default()
	err := validateSendSafety(cfg, true, "", "d_1", "", false)
	if err == nil {
		t.Fatal("expected confirmation error")
	}
	ce, ok := err.(cliError)
	if !ok {
		t.Fatalf("unexpected error type: %T", err)
	}
	if ce.code != "confirmation_required" {
		t.Fatalf("unexpected error code: %s", ce.code)
	}
}

func TestValidateSendSafetyAcceptsUIDConfirmation(t *testing.T) {
	cfg := config.Default()
	if err := validateSendSafety(cfg, true, "123", "imap:Drafts:123", "123", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSendSafetyBlocksForceWhenPolicyDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Safety.AllowForceSend = false
	err := validateSendSafety(cfg, true, "", "d_1", "", true)
	if err == nil {
		t.Fatal("expected safety_blocked")
	}
	ce := err.(cliError)
	if ce.code != "safety_blocked" {
		t.Fatalf("unexpected code: %s", ce.code)
	}
}
