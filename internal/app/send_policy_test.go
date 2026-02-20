package app

import (
	"errors"
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

func TestErrorCodeFromErr(t *testing.T) {
	if got := errorCodeFromErr(cliError{code: "confirmation_required"}, "fallback"); got != "confirmation_required" {
		t.Fatalf("unexpected code: %s", got)
	}
	if got := errorCodeFromErr(errors.New("x"), "fallback"); got != "fallback" {
		t.Fatalf("unexpected fallback: %s", got)
	}
}

func TestIsNonInteractiveSend(t *testing.T) {
	tests := []struct {
		name     string
		opts     globalOptions
		stdinTTY bool
		want     bool
	}{
		{name: "explicit no-input", opts: globalOptions{noInput: true}, stdinTTY: true, want: true},
		{name: "non-tty stdin", opts: globalOptions{noInput: false}, stdinTTY: false, want: true},
		{name: "interactive tty", opts: globalOptions{noInput: false}, stdinTTY: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonInteractiveSend(tt.opts, tt.stdinTTY); got != tt.want {
				t.Fatalf("expected %v got %v", tt.want, got)
			}
		})
	}
}
