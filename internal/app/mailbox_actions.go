package app

import (
	"flag"
	"io"
	"strings"
)

func mailboxAction(action string, args []string, boxes []mailboxInfo, source string) (any, bool, error) {
	switch action {
	case "list":
		return mailboxListResponse{Mailboxes: boxes, Count: len(boxes)}, false, nil
	case "resolve":
		fs := flag.NewFlagSet("mailbox resolve", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("name", "", "mailbox id or name")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		mailbox, matchedBy, ambiguous, err := resolveMailboxQuery(boxes, *name)
		if err != nil {
			if len(ambiguous) > 0 {
				ids := make([]string, 0, len(ambiguous))
				for _, m := range ambiguous {
					ids = append(ids, m.ID)
				}
				return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error(), hint: "Disambiguate with --name one of: " + strings.Join(ids, ", ")}
			}
			return nil, false, cliError{exit: 5, code: "not_found", msg: err.Error()}
		}
		return mailboxResolveResponse{Mailbox: mailbox, MatchedBy: matchedBy, Source: source}, false, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown mailbox action: " + action}
	}
}
