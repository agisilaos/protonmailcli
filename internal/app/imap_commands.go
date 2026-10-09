package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

type imapDraftClient interface {
	AppendDraft(raw string) (string, error)
	DraftMailboxName() (string, error)
	SearchUIDs(mailbox, criteria string) ([]string, error)
	MoveUID(srcMailbox, uid, dstMailbox string) error
	Close() error
}

type draftCreateItem struct {
	To             []string `json:"to"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body,omitempty"`
	BodyFile       string   `json:"body_file,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type sendManyItem struct {
	DraftID        string `json:"draft_id"`
	ConfirmSend    string `json:"confirm_send"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

var smtpSendFn = bridge.Send
var openBridgeClientFn = func(cfg config.Config, st *model.State, passwordFile string) (imapDraftClient, string, string, error) {
	return bridgeClient(cfg, st, passwordFile)
}

func cmdMailboxIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	if action != "list" && action != "resolve" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown mailbox action: " + action}
	}
	if action == "resolve" {
		fs, _ := newMailboxResolveFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "mailbox resolve", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
	}
	c, _, _, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()
	boxes, err := c.ListMailboxes()
	if err != nil {
		return nil, false, cliError{exit: 4, code: "imap_list_failed", msg: err.Error()}
	}
	res := make([]mailboxInfo, 0, len(boxes))
	for _, b := range boxes {
		id, kind := classifyMailbox(b)
		res = append(res, mailboxInfo{ID: id, Name: b, Kind: kind})
	}
	return mailboxAction(action, args, res, "imap")
}

func cmdSearchIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	fs, opts := newIMAPSearchFlags()
	if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "search "+action, runtimeStdout); err != nil {
		return nil, false, err
	} else if handled {
		return helpData, false, nil
	}
	c, _, _, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()
	criteria, err := buildIMAPCriteria(opts.query, opts.subject, opts.from, opts.to, opts.hasTag, opts.unread, opts.sinceID, opts.after, opts.before)
	if err != nil {
		return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
	}
	if action == "drafts" {
		if strings.TrimSpace(opts.mailbox) != "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--mailbox is only supported for search messages"}
		}
		mailbox, err := c.DraftMailboxName()
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_search_failed", msg: err.Error()}
		}
		items, err := c.ListMessages(mailbox, criteria)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_search_failed", msg: err.Error()}
		}
		items = filterSinceUID(items, opts.sinceID)
		sortByUIDDesc(items)
		start, lim := parsePage(opts.cursor, opts.limit)
		paged, next := paginateMessages(items, start, lim)
		out := make([]draftRecord, 0, len(paged))
		for _, m := range paged {
			out = append(out, draftRecord{ID: imapDraftID(m.UID), UID: m.UID, To: m.To, From: m.From, Subject: m.Subject, Date: m.Date.UTC().Format(time.RFC3339)})
		}
		return draftListResponse{Drafts: out, Count: len(out), Total: len(items), NextCursor: next, Source: "imap"}, false, nil
	}
	if action != "messages" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "search supports messages|drafts"}
	}
	targetMailbox := "INBOX"
	if strings.TrimSpace(opts.mailbox) != "" {
		targetMailbox = strings.TrimSpace(opts.mailbox)
	}
	items, err := c.ListMessages(targetMailbox, criteria)
	if err != nil {
		return nil, false, cliError{exit: 4, code: "imap_search_failed", msg: err.Error()}
	}
	items = filterSinceUID(items, opts.sinceID)
	sortByUIDDesc(items)
	start, lim := parsePage(opts.cursor, opts.limit)
	paged, next := paginateMessages(items, start, lim)
	out := make([]messageRecord, 0, len(paged))
	for _, m := range paged {
		out = append(out, messageRecord{ID: imapMessageIDForMailbox(targetMailbox, m.UID), UID: m.UID, From: m.From, To: m.To, Subject: m.Subject, Date: m.Date.UTC().Format(time.RFC3339)})
	}
	return messageListResponse{Messages: out, Count: len(out), Total: len(items), NextCursor: next, Mailbox: targetMailbox, Source: "imap"}, false, nil
}

func cmdTagIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	var c *bridge.IMAPClient
	ensureClient := func() error {
		if c != nil {
			return nil
		}
		client, _, _, err := bridgeClient(cfg, st, "")
		if err != nil {
			return err
		}
		c = client
		return nil
	}
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()
	switch action {
	case "list":
		if len(args) > 0 {
			fs := emptyHelpFlags("tag list")
			if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag list", runtimeStdout); err != nil {
				return nil, false, err
			} else if handled {
				return helpData, false, nil
			}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		msgs, err := c.ListMessages("INBOX", "ALL")
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_tag_list_failed", msg: err.Error()}
		}
		out := sortedUserKeywords(msgs)
		return tagListResponse{Tags: out, Count: len(out), Source: "imap"}, false, nil
	case "create":
		fs, opts := newIMAPTagCreateFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag create", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if strings.TrimSpace(opts.name) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name required"}
		}
		return tagCreateResponse{Tag: tagInfo{Name: opts.name}, Changed: false, Source: "imap"}, false, nil
	case "add", "remove":
		fs, opts := newIMAPTagAddRemoveFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag "+action, runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		mailbox, uid, err := parseMailboxUID(opts.msgID, "INBOX")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if strings.TrimSpace(opts.tag) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--tag required"}
		}
		if err := bridge.ValidateKeyword(opts.tag); err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if g.dryRun {
			return tagUpdateResponse{MessageID: imapMessageIDForMailbox(mailbox, uid), Tag: opts.tag, Changed: false, Source: "imap"}, false, nil
		}
		if err := c.SetKeyword(mailbox, uid, opts.tag, action == "add"); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_tag_update_failed", msg: err.Error()}
		}
		return tagUpdateResponse{MessageID: imapMessageIDForMailbox(mailbox, uid), Tag: opts.tag, Changed: true, Source: "imap"}, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown tag action: " + action}
	}
}

func sortedUserKeywords(msgs []bridge.DraftMessage) []string {
	set := map[string]struct{}{}
	for _, m := range msgs {
		for _, f := range m.Flags {
			if strings.HasPrefix(f, "\\") {
				continue
			}
			set[f] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func buildIMAPCriteria(query, subject, from, to, hasTag string, unread bool, sinceID, after, before string) (string, error) {
	for _, value := range []string{query, subject, from, to, hasTag, sinceID, after, before} {
		for _, c := range value {
			if c < 32 || c == 127 {
				return "", fmt.Errorf("search fields must not contain control characters")
			}
		}
	}
	parts := []string{}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, fmt.Sprintf(`TEXT "%s"`, escapeSearch(query)))
	}
	if strings.TrimSpace(subject) != "" {
		parts = append(parts, fmt.Sprintf(`SUBJECT "%s"`, escapeSearch(subject)))
	}
	if strings.TrimSpace(from) != "" {
		parts = append(parts, fmt.Sprintf(`FROM "%s"`, escapeSearch(from)))
	}
	if strings.TrimSpace(to) != "" {
		parts = append(parts, fmt.Sprintf(`TO "%s"`, escapeSearch(to)))
	}
	if strings.TrimSpace(hasTag) != "" {
		if err := bridge.ValidateKeyword(strings.TrimSpace(hasTag)); err != nil {
			return "", err
		}
		parts = append(parts, "KEYWORD "+strings.TrimSpace(hasTag))
	}
	if unread {
		parts = append(parts, "UNSEEN")
	}
	if strings.TrimSpace(sinceID) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(sinceID))
		if err != nil || n <= 0 {
			return "", fmt.Errorf("invalid since-id %q (expected positive integer)", sinceID)
		}
		parts = append(parts, fmt.Sprintf("UID %d:*", n))
	}
	if d, ok, err := parseDateInput(after); err != nil {
		return "", err
	} else if ok {
		parts = append(parts, "SINCE "+d.Format("02-Jan-2006"))
	}
	if d, ok, err := parseDateInput(before); err != nil {
		return "", err
	} else if ok {
		parts = append(parts, "BEFORE "+d.Format("02-Jan-2006"))
	}
	if len(parts) == 0 {
		return "ALL", nil
	}
	return strings.Join(parts, " "), nil
}

func escapeSearch(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(strings.TrimSpace(s))
}

func sortByUIDDesc(msgs []bridge.DraftMessage) {
	sort.Slice(msgs, func(i, j int) bool {
		return uidAsInt(msgs[i].UID) > uidAsInt(msgs[j].UID)
	})
}

func uidAsInt(uid string) int {
	n, err := strconv.Atoi(strings.TrimSpace(uid))
	if err != nil {
		return 0
	}
	return n
}

func parsePage(cursor string, limit int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	start := 0
	if strings.TrimSpace(cursor) != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n >= 0 {
			start = n
		}
	}
	return start, limit
}

func paginateMessages(all []bridge.DraftMessage, start, limit int) ([]bridge.DraftMessage, string) {
	if start >= len(all) {
		return []bridge.DraftMessage{}, ""
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	next := ""
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	return all[start:end], next
}

func loadDraftCreateManifest(path string, fromStdin bool) ([]draftCreateItem, error) {
	b, err := readManifest(path, fromStdin)
	if err != nil {
		return nil, err
	}
	var items []draftCreateItem
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func loadSendManyManifest(path string, fromStdin bool) ([]sendManyItem, error) {
	b, err := readManifest(path, fromStdin)
	if err != nil {
		return nil, err
	}
	var items []sendManyItem
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func readManifest(path string, fromStdin bool) ([]byte, error) {
	if fromStdin || path == "-" {
		return readAllStdinFn()
	}
	return os.ReadFile(path)
}

func usageForFlagSet(fs *flag.FlagSet) string {
	var b bytes.Buffer
	prev := fs.Output()
	fs.SetOutput(&b)
	fs.PrintDefaults()
	fs.SetOutput(prev)
	return strings.TrimRight("Usage of "+fs.Name()+":\n"+b.String(), "\n") + "\n\n" + commandHelpNotes(fs.Name())
}

// IMAP n:* includes the current maximum UID when n exceeds it. Enforce
// the inclusive lower bound before pagination and total calculations.
func filterSinceUID(items []bridge.DraftMessage, since string) []bridge.DraftMessage {
	if strings.TrimSpace(since) == "" {
		return items
	}
	lower := uidAsInt(since)
	out := make([]bridge.DraftMessage, 0, len(items))
	for _, item := range items {
		if uidAsInt(item.UID) >= lower {
			out = append(out, item)
		}
	}
	return out
}
