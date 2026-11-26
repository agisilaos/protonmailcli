package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nonMailboxIDChars = regexp.MustCompile(`[^a-z0-9]+`)

func classifyMailbox(name string, flags ...string) (string, string) {
	for _, flag := range flags {
		if strings.EqualFold(flag, `\Drafts`) {
			return "drafts", "system"
		}
	}
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
	q := query
	if q == "" {
		return mailboxInfo{}, "", nil, fmt.Errorf("mailbox name is required")
	}
	for _, m := range mailboxes {
		if m.Name == q {
			return m, "name_exact", nil, nil
		}
	}
	var matches []mailboxInfo
	for _, m := range mailboxes {
		if m.ID == q {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], "id_exact", nil, nil
	}
	if len(matches) > 1 {
		return mailboxInfo{}, "", matches, fmt.Errorf("ambiguous mailbox id: %q matches %d mailboxes; use the exact mailbox name", q, len(matches))
	}
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
	// Surrounding whitespace is part of a server name. Only offer trimmed
	// convenience lookup after preserving it yielded no match, and when doing
	// so cannot collapse distinct names onto one another.
	trimmed := strings.TrimSpace(q)
	if trimmed == "" {
		return mailboxInfo{}, "", nil, fmt.Errorf("mailbox name is required")
	}
	if trimmed != q {
		for _, m := range mailboxes {
			if m.ID == trimmed || strings.EqualFold(strings.TrimSpace(m.Name), trimmed) {
				matches = append(matches, m)
			}
		}
		if len(matches) == 1 {
			match := matches[0]
			matchedBy := "name_casefold"
			if match.Name == trimmed {
				matchedBy = "name_exact"
			} else if match.ID == trimmed {
				matchedBy = "id_exact"
			}
			return match, matchedBy, nil, nil
		}
		if len(matches) > 1 {
			return mailboxInfo{}, "", matches, fmt.Errorf("ambiguous mailbox name: %q matches %d mailboxes after trimming whitespace", q, len(matches))
		}
	}
	return mailboxInfo{}, "", nil, fmt.Errorf("mailbox not found: %q", q)
}
