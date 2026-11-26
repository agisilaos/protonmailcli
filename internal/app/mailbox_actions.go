package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func mailboxAction(action string, args []string, boxes []mailboxInfo, source string) (any, bool, error) {
	switch action {
	case "list":
		return mailboxListResponse{Mailboxes: boxes, Count: len(boxes)}, false, nil
	case "resolve":
		fs, opts := newMailboxResolveFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		mailbox, matchedBy, ambiguous, err := resolveMailboxQuery(boxes, opts.name)
		if err != nil {
			return nil, false, mailboxResolutionError(err, ambiguous, "--name")
		}
		return mailboxResolveResponse{Mailbox: mailbox, MatchedBy: matchedBy, Source: source}, false, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown mailbox action: " + action}
	}
}

func mailboxResolutionError(err error, ambiguous []mailboxInfo, flag string) cliError {
	if len(ambiguous) > 0 {
		names := make([]string, 0, len(ambiguous))
		for _, m := range ambiguous {
			names = append(names, shellQuoteArgument(m.Name))
		}
		return cliError{exit: 2, code: "validation_error", msg: err.Error(), hint: "Use " + flag + " with one of these exact mailbox names: " + strings.Join(names, ", ")}
	}
	return cliError{exit: 5, code: "not_found", msg: err.Error()}
}

func shellQuoteArgument(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

type messageInspection struct {
	globalOptions
	passwordFile           string
	needsOriginalDirectory bool
}

func newMessageInspection(g globalOptions, cfg config.Config, st *model.State, passwordOverride string) messageInspection {
	relative := func(path string) bool {
		return path != "" && !filepath.IsAbs(config.Expand(path))
	}
	storedPassword := firstNonEmpty(st.Auth.PasswordFile, cfg.Bridge.PasswordFile)
	return messageInspection{
		globalOptions: g, passwordFile: passwordOverride,
		needsOriginalDirectory: relative(cfg.Bridge.TLSCertFile) || (strings.TrimSpace(passwordOverride) == "" && relative(storedPassword)),
	}
}

func (g messageInspection) command(messageID string) string {
	parts := []string{"protonmailcli", "--json", "--config", shellQuoteArgument(recoveryPath(config.Expand(g.config))),
		"--state", shellQuoteArgument(recoveryPath(g.statePath))}
	if g.profile != "" {
		parts = append(parts, "--profile", shellQuoteArgument(g.profile))
	}
	if g.expectAccount != "" {
		parts = append(parts, "--expect-account", shellQuoteArgument(g.expectAccount))
	}
	parts = append(parts, "message", "get")
	if strings.TrimSpace(g.passwordFile) != "" {
		parts = append(parts, "--password-file", shellQuoteArgument(recoveryPath(config.Expand(g.passwordFile))))
	}
	parts = append(parts, "--message-id", shellQuoteArgument(messageID))
	command := strings.Join(parts, " ")
	if !g.needsOriginalDirectory {
		return command
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "# Run from the original working directory: " + command
	}
	return "(cd -P " + shellQuoteArgument(cwd) + " && " + command + ")"
}

func recoveryPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if runtime.GOOS == "windows" {
		if absolute, err := filepath.Abs(path); err == nil {
			return absolute
		}
	} else if cwd, err := os.Getwd(); err == nil {
		// Preserve relative link/../ components. Cleaning changes their target on Unix.
		return strings.TrimSuffix(cwd, "/") + "/" + path
	}
	return path
}
