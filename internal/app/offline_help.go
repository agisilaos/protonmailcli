package app

import (
	"errors"
	"flag"
	"io"
	"strings"
)

func offlineHelp(rest []string, g globalOptions) (any, bool, error) {
	for n := len(rest); n > 0; n-- {
		if n > 3 {
			continue
		}
		path := strings.Join(rest[:n], " ")
		fs := offlineHelpFlags(path, useLocalStateMode())
		if fs == nil {
			continue
		}
		if err := fs.Parse(rest[n:]); !errors.Is(err, flag.ErrHelp) {
			return nil, false, nil
		}
		return renderFlagHelp(fs, g, path, runtimeStdout)
	}
	return nil, false, nil
}

func offlineHelpFlags(path string, local bool) *flag.FlagSet {
	switch path {
	case "setup":
		fs, _ := newSetupFlags()
		return fs
	case "message follow-up":
		fs, _ := newIMAPMessageFollowUpFlags()
		return fs
	case "message send-many":
		if local {
			fs, _ := newLocalMessageSendManyFlags()
			return fs
		}
		fs, _ := newIMAPMessageSendManyFlags()
		return fs
	case "message send":
		if local {
			fs, _ := newLocalMessageSendFlags()
			return fs
		}
		fs, _ := newIMAPMessageSendFlags()
		return fs
	case "message get":
		fs, _ := newIMAPMessageGetFlags()
		return fs
	case "draft delete":
		fs, _ := newIMAPDraftDeleteFlags()
		return fs
	case "draft update":
		fs, _ := newIMAPDraftUpdateFlags()
		return fs
	case "draft create-many":
		if local {
			fs, _ := newLocalDraftCreateManyFlags()
			return fs
		}
		fs, _ := newIMAPDraftCreateManyFlags()
		return fs
	case "draft create":
		if local {
			fs, _ := newLocalDraftCreateFlags()
			return fs
		}
		fs, _ := newIMAPDraftCreateFlags()
		return fs
	case "draft get":
		fs, _ := newIMAPDraftGetFlags()
		return fs
	case "draft list":
		if local {
			return emptyHelpFlags(path)
		}
		fs, _ := newIMAPDraftListFlags()
		return fs
	case "tag add", "tag remove":
		fs, _ := newIMAPTagAddRemoveFlags()
		return fs
	case "tag create":
		fs, _ := newIMAPTagCreateFlags()
		return fs
	case "tag list":
		fs := emptyHelpFlags("tag list")
		return fs
	case "search messages", "search drafts":
		if local {
			fs, _ := newLocalSearchFlags()
			return fs
		}
		fs, _ := newIMAPSearchFlags()
		return fs
	case "mailbox resolve":
		fs, _ := newMailboxResolveFlags()
		return fs
	case "filter test", "filter apply":
		fs, _ := newLocalFilterTestApplyFlags()
		return fs
	case "filter delete":
		fs, _ := newLocalFilterDeleteFlags()
		return fs
	case "filter create":
		fs, _ := newLocalFilterCreateFlags()
		return fs
	case "bridge account use":
		fs, _ := newBridgeAccountUseFlags()
		return fs
	case "auth login":
		fs, _ := newAuthLoginFlags()
		return fs
	case "mailbox list", "filter list", "auth status", "auth logout", "bridge account list", "doctor", "completion":
		return emptyHelpFlags(path)
	default:
		return nil
	}
}
func emptyHelpFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func commandHelpNotes(name string) string {
	switch name {
	case "setup":
		return "Noninteractive setup requires --username; optional password-file values are paths, not passwords.\nExample: protonmailcli --no-input setup --username user@example.invalid"
	case "completion":
		return "Usage: protonmailcli completion <bash|zsh|fish>\nExample: protonmailcli completion bash > protonmailcli.bash"
	case "draft create":
		return "Requires at least one --to and exactly one body source: --body, --body-file or --stdin.\nExample: protonmailcli --dry-run draft create --to user@example.invalid --body Example\nIMAP previews may read Bridge; help does not."
	case "draft update":
		return "Requires --draft-id; supply the subject or body fields to update.\nExample: protonmailcli --dry-run draft update --draft-id 123 --subject Example"
	case "draft get", "draft delete":
		return "Requires --draft-id.\nExample: protonmailcli " + name + " --draft-id 123"
	case "draft create-many", "message send-many":
		return "Requires a manifest via --file or --stdin. Review all recipients and confirmation fields before execution.\nExample: protonmailcli --dry-run " + name + " --file manifest.json\nIMAP previews may read Bridge; help does not."
	case "message get":
		return "Requires --message-id.\nExample: protonmailcli message get --message-id 123"
	case "message send":
		return "Requires --draft-id. Noninteractive confirmation policy may require matching --confirm-send or --force.\nExample: protonmailcli --dry-run message send --draft-id 123 --confirm-send 123\nA preview may read Bridge and validates send policy; it does not send mail."
	case "message follow-up":
		return "Requires --message-id and one body source; creates a follow-up draft. Recipients are derived from the original message unless --to overrides them.\nExample: protonmailcli --dry-run message follow-up --message-id 123 --to user@example.invalid --body Example"
	case "mailbox resolve":
		return "Requires --name.\nExample: protonmailcli mailbox resolve --name Inbox"
	case "search":
		return "Select messages or drafts; optional filters narrow results.\nExample: protonmailcli search messages --query Example"
	case "auth login":
		return "Uses --username or the configured username. Noninteractive login also needs --password-file or a configured password file; interactive login can prompt for missing values.\nExample: protonmailcli --no-input auth login --username user@example.invalid --password-file /path/to/bridge-password"
	case "bridge account use":
		return "Requires --username.\nExample: protonmailcli " + name + " --username user@example.invalid"
	case "tag create":
		return "Requires --name.\nExample: protonmailcli --dry-run tag create --name Example"
	case "tag add/remove":
		return "Requires --message-id and --tag.\nExample: protonmailcli --dry-run tag add --message-id 123 --tag Example"
	case "filter create":
		return "Requires --name, --contains and --add-tag.\nExample: protonmailcli --dry-run filter create --name Example --contains Example --add-tag Example"
	case "filter delete", "filter test/apply":
		return "Requires --filter-id.\nExample: protonmailcli filter test --filter-id example"
	default:
		return "Example: protonmailcli " + name
	}
}
