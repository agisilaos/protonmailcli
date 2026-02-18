package app

import "protonmailcli/internal/config"

func validateSendSafety(cfg config.Config, nonTTY bool, confirm, canonicalID, canonicalUID string, force bool) error {
	if cfg.Safety.RequireConfirmSendNonTTY && nonTTY && confirm != canonicalID && (canonicalUID == "" || confirm != canonicalUID) && !force {
		return cliError{exit: 7, code: "confirmation_required", msg: "--confirm-send is required in non-interactive mode", hint: "Pass --confirm-send <draft-id> or --force"}
	}
	if force && !cfg.Safety.AllowForceSend {
		return cliError{exit: 7, code: "safety_blocked", msg: "--force is disabled by policy"}
	}
	return nil
}
