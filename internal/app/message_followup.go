package app

import (
	"net/mail"
	"slices"
	"strings"

	"protonmailcli/internal/model"
)

func followUpSubject(override, original string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	subject := strings.TrimSpace(original)
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	if subject == "" {
		return "Re:"
	}
	return "Re: " + subject
}

func localFollowUpRecipients(msg model.Message, self string) []string {
	if strings.EqualFold(strings.TrimSpace(msg.From), strings.TrimSpace(self)) {
		return append([]string{}, msg.To...)
	}
	if strings.TrimSpace(msg.From) == "" {
		return nil
	}
	return []string{strings.TrimSpace(msg.From)}
}

func normalizeMessageID(v string) string {
	id := strings.TrimSpace(v)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		return id
	}
	return "<" + strings.Trim(id, "<>") + ">"
}

func parseReferenceIDs(v string) []string {
	parts := strings.Fields(strings.TrimSpace(v))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		id := normalizeMessageID(p)
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func threadHeaders(messageID, references string) (string, []string) {
	id := normalizeMessageID(messageID)
	if id == "" {
		return "", nil
	}
	refs := parseReferenceIDs(references)
	if !slices.Contains(refs, id) {
		refs = append(refs, id)
	}
	return id, refs
}

func imapFollowUpRecipients(originalFrom string, originalTo []string, self string) []string {
	if len(originalTo) > 0 {
		if addrs, err := mail.ParseAddressList(originalFrom); err == nil {
			for _, a := range addrs {
				if strings.EqualFold(strings.TrimSpace(a.Address), strings.TrimSpace(self)) {
					return append([]string{}, originalTo...)
				}
			}
		}
	}
	if addrs, err := mail.ParseAddressList(originalFrom); err == nil {
		out := make([]string, 0, len(addrs))
		for _, a := range addrs {
			if strings.TrimSpace(a.Address) != "" {
				out = append(out, strings.TrimSpace(a.Address))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if strings.TrimSpace(originalFrom) == "" {
		return nil
	}
	return []string{strings.TrimSpace(originalFrom)}
}
