package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"protonmailcli/internal/output"
)

// Validate the complete command before config, state, or remote access. flag.Parse
// intentionally stops at a positional argument; none of our leaf commands accepts
// positional data except completion's one shell name.
func validateLeafArguments(rest []string) error {
	if rest[0] == "completion" {
		if len(rest) > 2 {
			return cliError{exit: 2, code: "usage_error", msg: "completion accepts one shell name"}
		}
		return nil
	}
	for n := min(3, len(rest)); n > 0; n-- {
		fs := offlineHelpFlags(strings.Join(rest[:n], " "), useLocalStateMode())
		if fs == nil {
			continue
		}
		if err := fs.Parse(rest[n:]); err != nil {
			unknown := strings.TrimPrefix(err.Error(), "flag provided but not defined: ")
			switch unknown {
			case "-json", "-plain", "-no-input", "-dry-run", "-n", "-config", "-state", "-profile":
				name := "-" + unknown
				if unknown == "-n" {
					name = "-n"
				}
				return cliError{exit: 2, code: "usage_error", msg: fmt.Sprintf("global flag %s must appear before the resource", name), hint: "Example: protonmailcli --json draft list"}
			}
			return cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if fs.NArg() != 0 {
			return cliError{exit: 2, code: "usage_error", msg: "unexpected arguments: " + strings.Join(fs.Args(), " ")}
		}
		return nil
	}
	return nil
}

func parseFlagSetWithHelp(fs *flag.FlagSet, args []string, g globalOptions, helpName string, stdout io.Writer) (any, bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return renderFlagHelp(fs, g, helpName, stdout)
		}
		return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
	}
	return nil, false, nil
}

func parseDraftCreateManifestInput(file string, fromStdin bool) ([]draftCreateItem, error) {
	manifestPath, err := resolveManifestInput(file, fromStdin)
	if err != nil {
		return nil, err
	}
	return loadDraftCreateManifest(manifestPath, fromStdin)
}

func loadDraftUpdateBody(fs *flag.FlagSet, opts *imapDraftUpdateFlags) (string, error) {
	if flagWasSet(fs, "body") {
		if opts.bodyFile != "" || opts.stdinBody {
			return "", fmt.Errorf("provide only one of --body, --body-file, or --stdin")
		}
		return opts.body, nil
	}
	return loadBody(opts.body, opts.bodyFile, opts.stdinBody)
}

func parseSendManyManifestInput(file string, fromStdin bool) ([]sendManyItem, error) {
	manifestPath, err := resolveManifestInput(file, fromStdin)
	if err != nil {
		return nil, err
	}
	return loadSendManyManifest(manifestPath, fromStdin)
}

func renderFlagHelp(fs *flag.FlagSet, g globalOptions, name string, stdout io.Writer) (any, bool, error) {
	usage := usageForFlagSet(fs)
	if g.mode == output.ModeJSON || g.mode == output.ModePlain {
		return map[string]any{"help": name, "usage": usage}, true, nil
	}
	if _, err := fmt.Fprintln(stdout, usage); err != nil {
		return nil, true, err
	}
	return map[string]any{"help": name}, true, nil
}
