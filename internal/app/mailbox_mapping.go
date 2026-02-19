package app

import (
	"fmt"
	"regexp"
	"sort"
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

func resolveMailboxQuery(mailboxes []mailboxInfo, query string) (mailboxInfo, string, []mailboxInfo, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return mailboxInfo{}, "", nil, fmt.Errorf("--name is required")
	}
	for _, m := range mailboxes {
		if m.Name == q {
			return m, "name_exact", nil, nil
		}
	}
	for _, m := range mailboxes {
		if m.ID == q {
			return m, "id_exact", nil, nil
		}
	}
	var matches []mailboxInfo
	for _, m := range mailboxes {
		if strings.EqualFold(m.Name, q) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], "name_casefold", nil, nil
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool {
			return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
		})
		return mailboxInfo{}, "", matches, fmt.Errorf("ambiguous mailbox name: %q matches %d mailboxes", q, len(matches))
	}
	return mailboxInfo{}, "", nil, fmt.Errorf("mailbox not found: %q", q)
}
