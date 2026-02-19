package app

import (
	"regexp"
	"strings"
)

var nonMailboxIDChars = regexp.MustCompile(`[^a-z0-9]+`)

func classifyMailbox(name string) (string, string) {
	n := strings.TrimSpace(name)
	l := strings.ToLower(n)
	switch l {
	case "inbox":
		return "inbox", "system"
	case "drafts":
		return "drafts", "system"
	case "sent", "sent mail", "sent messages":
		return "sent", "system"
	case "archive":
		return "archive", "system"
	case "spam", "junk":
		return "spam", "system"
	case "trash", "deleted items":
		return "trash", "system"
	case "all mail", "allmail":
		return "all_mail", "system"
	default:
		id := strings.Trim(nonMailboxIDChars.ReplaceAllString(strings.ToLower(n), "_"), "_")
		if id == "" {
			id = "mailbox"
		}
		return id, "custom"
	}
}
