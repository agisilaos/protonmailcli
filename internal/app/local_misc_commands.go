package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"protonmailcli/internal/model"
)

func cmdMailbox(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	boxes := []mailboxInfo{
		{ID: "inbox", Name: "INBOX", Kind: "system", Count: len(st.Messages)},
		{ID: "drafts", Name: "Drafts", Kind: "system", Count: len(st.Drafts)},
		{ID: "sent", Name: "Sent", Kind: "system", Count: countSent(st.Messages)},
	}
	if action == "resolve" {
		fs, _ := newMailboxResolveFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "mailbox resolve", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
	}
	return mailboxAction(action, args, boxes, "local")
}

func countSent(msgs map[string]model.Message) int {
	return len(msgs)
}

func cmdSearch(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	if action != "messages" && action != "drafts" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "search supports messages|drafts"}
	}
	fs, opts := newLocalSearchFlags()
	if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "search "+action, runtimeStdout); err != nil {
		return nil, false, err
	} else if handled {
		return helpData, false, nil
	}
	q := strings.ToLower(opts.query)
	if action == "drafts" {
		out := []model.Draft{}
		for _, d := range st.Drafts {
			if q == "" || strings.Contains(strings.ToLower(d.Subject+" "+d.Body+" "+strings.Join(d.To, " ")), q) {
				out = append(out, d)
			}
		}
		return localSearchDraftsResponse{Drafts: out, Count: len(out)}, false, nil
	}
	out := []model.Message{}
	for _, m := range st.Messages {
		if q == "" || strings.Contains(strings.ToLower(m.Subject+" "+m.Body+" "+strings.Join(m.To, " ")), q) {
			out = append(out, m)
		}
	}
	return localSearchMessagesResponse{Messages: out, Count: len(out)}, false, nil
}

func cmdTag(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "list":
		list := make([]string, 0, len(st.Tags))
		for name := range st.Tags {
			list = append(list, name)
		}
		sort.Strings(list)
		return tagListResponse{Tags: list, Count: len(list)}, false, nil
	case "create":
		fs, opts := newIMAPTagCreateFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag create", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if opts.name == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name required"}
		}
		id := st.Tags[opts.name]
		changed := false
		if id == "" {
			id = fmt.Sprintf("t_%d", time.Now().UnixNano())
			st.Tags[opts.name] = id
			changed = true
		}
		return tagCreateResponse{Tag: tagInfo{ID: id, Name: opts.name}, Changed: changed}, changed, nil
	case "add", "remove":
		fs, opts := newIMAPTagAddRemoveFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag "+action, runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(opts.msgID, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		m, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		if opts.tag == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--tag required"}
		}
		changed := false
		if action == "add" {
			if !contains(m.Tags, opts.tag) {
				m.Tags = append(m.Tags, opts.tag)
				changed = true
			}
		} else {
			next := make([]string, 0, len(m.Tags))
			for _, t := range m.Tags {
				if t != opts.tag {
					next = append(next, t)
				} else {
					changed = true
				}
			}
			m.Tags = next
		}
		st.Messages[uid] = m
		return tagUpdateResponse{MessageID: uid, Tag: opts.tag, Changed: changed}, changed, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown tag action: " + action}
	}
}

func cmdFilter(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "list":
		filters := make([]model.Filter, 0, len(st.Filters))
		for _, f := range st.Filters {
			filters = append(filters, f)
		}
		return filterListResponse{Filters: filters, Count: len(filters)}, false, nil
	case "create":
		fs, opts := newLocalFilterCreateFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter create", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if opts.name == "" || opts.containsQ == "" || opts.addTag == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name, --contains and --add-tag are required"}
		}
		id := fmt.Sprintf("f_%d", time.Now().UnixNano())
		f := model.Filter{ID: id, Name: opts.name, Contains: opts.containsQ, AddTag: opts.addTag, CreatedAt: time.Now().UTC()}
		if !g.dryRun {
			st.Filters[id] = f
		}
		return filterCreateResponse{Filter: f}, true, nil
	case "delete":
		fs, opts := newLocalFilterDeleteFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter delete", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if _, ok := st.Filters[opts.id]; !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "filter not found"}
		}
		if !g.dryRun {
			delete(st.Filters, opts.id)
		}
		return filterDeleteResponse{Deleted: true, FilterID: opts.id}, true, nil
	case "test", "apply":
		fs, opts := newLocalFilterTestApplyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter "+action, runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		f, ok := st.Filters[opts.id]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "filter not found"}
		}
		matches := 0
		changes := 0
		for id, m := range st.Messages {
			hay := strings.ToLower(m.Subject + " " + m.Body)
			if strings.Contains(hay, strings.ToLower(f.Contains)) {
				matches++
				if action == "apply" && !contains(m.Tags, f.AddTag) {
					m.Tags = append(m.Tags, f.AddTag)
					st.Messages[id] = m
					changes++
				}
			}
		}
		changed := action == "apply" && changes > 0
		return filterApplyResponse{FilterID: f.ID, Mode: action, Matched: matches, Changed: changes}, changed, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown filter action: " + action}
	}
}
