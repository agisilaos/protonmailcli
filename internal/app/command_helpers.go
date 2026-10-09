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

func loadDraftUpdateBody(fs *flag.FlagSet, opts *imapDraftUpdateFlags) (string, error) {
	if flagWasSet(fs, "body") {
		if opts.bodyFile != "" || opts.stdinBody {
			return "", fmt.Errorf("provide only one of --body, --body-file, or --stdin")
		}
		return opts.body, nil
	}
	return loadBody(opts.body, opts.bodyFile, opts.stdinBody)
}
