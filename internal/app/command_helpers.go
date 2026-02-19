package app

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"protonmailcli/internal/output"
)

func parseFlagSetWithHelp(fs *flag.FlagSet, args []string, g globalOptions, helpName string, stdout io.Writer) (any, bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage := usageForFlagSet(fs)
			if g.mode == output.ModeJSON || g.mode == output.ModePlain {
				return map[string]any{"help": helpName, "usage": usage}, true, nil
			}
			fmt.Fprintln(stdout, usage)
			return map[string]any{"help": helpName}, true, nil
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

func parseSendManyManifestInput(file string, fromStdin bool) ([]sendManyItem, error) {
	manifestPath, err := resolveManifestInput(file, fromStdin)
	if err != nil {
		return nil, err
	}
	return loadSendManyManifest(manifestPath, fromStdin)
}
