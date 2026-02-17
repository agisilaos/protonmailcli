package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

func cmdMailboxIMAP(action string, _ []string, cfg config.Config, st *model.State) (any, bool, error) {
	if action != "list" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown mailbox action: " + action}
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
	res := make([]map[string]any, 0, len(boxes))
	for _, b := range boxes {
		res = append(res, map[string]any{"name": b})
	}
	return map[string]any{"mailboxes": res, "count": len(res)}, false, nil
}

func cmdDraftIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	c, username, _, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()

	switch action {
	case "list":
		fs := flag.NewFlagSet("draft list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		query := fs.String("query", "", "text query")
		from := fs.String("from", "", "from filter")
		to := fs.String("to", "", "to filter")
		after := fs.String("after", "", "date filter YYYY-MM-DD")
		before := fs.String("before", "", "date filter YYYY-MM-DD")
		limit := fs.Int("limit", 50, "max results")
		cursor := fs.String("cursor", "", "offset cursor")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		criteria, err := buildIMAPCriteria(*query, "", *from, *to, "", false, "", *after, *before)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		drafts, err := c.ListMessages("Drafts", criteria)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_list_failed", msg: err.Error()}
		}
		sortByUIDDesc(drafts)
		start, lim := parsePage(*cursor, *limit)
		paged, next := paginateMessages(drafts, start, lim)
		out := make([]map[string]any, 0, len(drafts))
		for _, d := range paged {
			out = append(out, map[string]any{
				"id":      imapDraftID(d.UID),
				"uid":     d.UID,
				"to":      d.To,
				"from":    d.From,
				"subject": d.Subject,
				"body":    d.Body,
				"date":    d.Date.UTC().Format(time.RFC3339),
				"flags":   d.Flags,
			})
		}
		return map[string]any{"drafts": out, "count": len(out), "total": len(drafts), "nextCursor": next, "source": "imap"}, false, nil
	case "get":
		fs := flag.NewFlagSet("draft get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*id)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--draft-id required"}
		}
		d, err := c.GetDraft(uid)
		if err != nil {
			return nil, false, cliError{exit: 5, code: "not_found", msg: err.Error()}
		}
		return map[string]any{"draft": map[string]any{"id": imapDraftID(d.UID), "uid": d.UID, "to": d.To, "subject": d.Subject, "body": d.Body, "flags": d.Flags}, "source": "imap"}, false, nil
	case "create":
		fs := flag.NewFlagSet("draft create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var to sliceFlag
		subject := fs.String("subject", "", "subject")
		body := fs.String("body", "", "body")
		bodyFile := fs.String("body-file", "", "body from file or -")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		fs.Var(&to, "to", "recipient (repeat)")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if len(to) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "at least one --to is required"}
		}
		b, err := loadBody(*body, *bodyFile)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		payload := map[string]any{"to": []string(to), "subject": *subject, "body": b}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "draft.create", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		raw := bridge.BuildRawMessage(username, to, *subject, b)
		if g.dryRun {
			return map[string]any{"action": "draft.create", "wouldCreate": true, "source": "imap"}, true, nil
		}
		uid, err := saveDraftWithFallback(c, cfg, st, username, to, *subject, b, raw)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_create_failed", msg: err.Error()}
		}
		resp := map[string]any{"draft": map[string]any{"id": imapDraftID(uid), "uid": uid, "to": to, "subject": *subject, "body": b}, "source": "imap"}
		_ = idempotencyStore(st, *idempotencyKey, "draft.create", payload, resp)
		return resp, true, nil
	case "create-many":
		fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
		fs.SetOutput(os.Stdout)
		file := fs.String("file", "", "manifest json path or -")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return map[string]any{"help": "draft create-many"}, false, nil
			}
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if strings.TrimSpace(*file) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--file is required"}
		}
		items, err := loadDraftCreateManifest(*file)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "draft.create-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		results := make([]map[string]any, 0, len(items))
		success := 0
		for i, it := range items {
			b, err := loadBody(it.Body, it.BodyFile)
			if err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error()})
				continue
			}
			raw := bridge.BuildRawMessage(username, it.To, it.Subject, b)
			if g.dryRun {
				results = append(results, map[string]any{"index": i, "ok": true, "dryRun": true, "to": it.To, "subject": it.Subject})
				success++
				continue
			}
			uid, err := saveDraftWithFallback(c, cfg, st, username, it.To, it.Subject, b, raw)
			if err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"index": i, "ok": true, "draftId": imapDraftID(uid), "uid": uid})
			success++
		}
		resp := map[string]any{"results": results, "count": len(results), "success": success, "failed": len(results) - success, "source": "imap"}
		_ = idempotencyStore(st, *idempotencyKey, "draft.create-many", items, resp)
		return resp, success > 0, nil
	case "update":
		fs := flag.NewFlagSet("draft update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		subject := fs.String("subject", "", "subject")
		body := fs.String("body", "", "body")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*id)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--draft-id required"}
		}
		d, err := c.GetDraft(uid)
		if err != nil {
			return nil, false, cliError{exit: 5, code: "not_found", msg: err.Error()}
		}
		if *subject != "" {
			d.Subject = *subject
		}
		if *body != "" {
			d.Body = *body
		}
		if g.dryRun {
			return map[string]any{"action": "draft.update", "draftId": imapDraftID(uid), "wouldUpdate": true, "source": "imap"}, true, nil
		}
		if err := c.DeleteDraft(uid); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_update_failed", msg: err.Error()}
		}
		newUID, err := c.AppendDraft(bridge.BuildRawMessage(username, d.To, d.Subject, d.Body))
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_update_failed", msg: err.Error()}
		}
		return map[string]any{"draft": map[string]any{"id": imapDraftID(newUID), "uid": newUID, "to": d.To, "subject": d.Subject, "body": d.Body}, "source": "imap"}, true, nil
	case "delete":
		fs := flag.NewFlagSet("draft delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*id)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--draft-id required"}
		}
		if g.dryRun {
			return map[string]any{"action": "draft.delete", "draftId": imapDraftID(uid), "wouldDelete": true, "source": "imap"}, true, nil
		}
		if err := c.DeleteDraft(uid); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_delete_failed", msg: err.Error()}
		}
		return map[string]any{"deleted": true, "draftId": imapDraftID(uid), "source": "imap"}, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown draft action: " + action}
	}
}

func saveDraftWithFallback(c imapDraftClient, cfg config.Config, st *model.State, username string, to []string, subject, body, raw string) (string, error) {
	uid, err := c.AppendDraft(raw)
	if err == nil {
		return uid, nil
	}
	return createDraftViaMoveFallback(cfg, st, username, to, subject, body, strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD")))
}

func createDraftViaMoveFallback(cfg config.Config, st *model.State, username string, to []string, subject, body, envPassword string) (string, error) {
	_, password, err := resolveBridgeCredentials(cfg, st, "")
	if err != nil {
		if strings.TrimSpace(envPassword) == "" {
			return "", err
		}
		password = strings.TrimSpace(envPassword)
	}
	token := fmt.Sprintf("pmail-%d", time.Now().UnixNano())
	if err := smtpSendFn(bridge.SMTPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Username: username, Password: password}, bridge.SendInput{
		From:    username,
		To:      []string{username},
		Subject: subject,
		Body:    body,
		ExtraHeaders: map[string]string{
			"X-Pmail-Draft-Token": token,
		},
	}); err != nil {
		return "", err
	}
	c2, _, _, err := openBridgeClientFn(cfg, st, "")
	if err != nil {
		return "", err
	}
	defer c2.Close()
	draftsMailbox, err := c2.DraftMailboxName()
	if err != nil {
		return "", err
	}
	var uid string
	for i := 0; i < 10; i++ {
		uids, err := c2.SearchUIDs("INBOX", fmt.Sprintf(`HEADER X-Pmail-Draft-Token "%s"`, escapeSearch(token)))
		if err == nil && len(uids) > 0 {
			uid = uids[len(uids)-1]
			break
		}
		time.Sleep(1 * time.Second)
	}
	if uid == "" {
		return "", fmt.Errorf("fallback could not locate created message in INBOX")
	}
	if err := c2.MoveUID("INBOX", uid, draftsMailbox); err != nil {
		return "", err
	}
	draftUIDs, err := c2.SearchUIDs(draftsMailbox, fmt.Sprintf(`HEADER X-Pmail-Draft-Token "%s"`, escapeSearch(token)))
	if err != nil || len(draftUIDs) == 0 {
		return uid, nil
	}
	return draftUIDs[len(draftUIDs)-1], nil
}

func cmdMessageIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	c, username, password, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()

	switch action {
	case "get":
		fs := flag.NewFlagSet("message get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("message-id", "", "message id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*id)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--message-id required"}
		}
		msgs, err := c.ListMessages("INBOX", "UID "+uid)
		if err != nil || len(msgs) == 0 {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		m := msgs[0]
		return map[string]any{"message": map[string]any{"id": imapMessageID(m.UID), "uid": m.UID, "from": m.From, "to": m.To, "subject": m.Subject, "body": m.Body, "flags": m.Flags}, "source": "imap"}, false, nil
	case "send":
		fs := flag.NewFlagSet("message send", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		draftID := fs.String("draft-id", "", "draft id")
		confirm := fs.String("confirm-send", "", "confirmation token")
		force := fs.Bool("force", false, "force send without confirm token")
		passwordFile := fs.String("smtp-password-file", "", "path to smtp password file")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*draftID)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--draft-id required"}
		}
		d, err := c.GetDraft(uid)
		if err != nil {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		payload := map[string]any{"draftId": *draftID, "confirm": *confirm, "force": *force, "to": d.To, "subject": d.Subject, "body": d.Body}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "message.send", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		nonTTY := g.noInput
		if cfg.Safety.RequireConfirmSendNonTTY && nonTTY && *confirm != *draftID && *confirm != uid && !*force {
			return nil, false, cliError{exit: 7, code: "confirmation_required", msg: "--confirm-send is required in non-interactive mode", hint: "Pass --confirm-send <draft-id> or --force"}
		}
		if *force && !cfg.Safety.AllowForceSend {
			return nil, false, cliError{exit: 7, code: "safety_blocked", msg: "--force is disabled by policy"}
		}
		if g.dryRun {
			return map[string]any{"action": "send", "draftId": imapDraftID(uid), "wouldSend": true, "dryRun": true, "source": "imap"}, true, nil
		}
		pass := strings.TrimSpace(password)
		if *passwordFile != "" {
			_, p, err := resolveBridgeCredentials(cfg, st, *passwordFile)
			if err != nil {
				return nil, false, err
			}
			pass = p
		}
		err = bridge.Send(bridge.SMTPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Username: username, Password: pass}, bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body})
		if err != nil {
			return nil, false, cliError{exit: 4, code: "send_failed", msg: err.Error()}
		}
		resp := map[string]any{"sent": true, "draftId": imapDraftID(uid), "source": "imap", "sentAt": time.Now().UTC().Format(time.RFC3339)}
		_ = idempotencyStore(st, *idempotencyKey, "message.send", payload, resp)
		return resp, true, nil
	case "send-many":
		fs := flag.NewFlagSet("message send-many", flag.ContinueOnError)
		fs.SetOutput(os.Stdout)
		file := fs.String("file", "", "manifest json path or -")
		passwordFile := fs.String("smtp-password-file", "", "path to smtp password file")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return map[string]any{"help": "message send-many"}, false, nil
			}
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if strings.TrimSpace(*file) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--file is required"}
		}
		items, err := loadSendManyManifest(*file)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "message.send-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		pass := strings.TrimSpace(password)
		if *passwordFile != "" {
			_, p, err := resolveBridgeCredentials(cfg, st, *passwordFile)
			if err != nil {
				return nil, false, err
			}
			pass = p
		}
		results := make([]map[string]any, 0, len(items))
		success := 0
		for i, it := range items {
			uid, err := parseUID(it.DraftID)
			if err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": "invalid draft_id"})
				continue
			}
			d, err := c.GetDraft(uid)
			if err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": "draft not found", "draftId": it.DraftID})
				continue
			}
			if cfg.Safety.RequireConfirmSendNonTTY && g.noInput && it.ConfirmSend != it.DraftID && it.ConfirmSend != uid {
				results = append(results, map[string]any{"index": i, "ok": false, "error": "confirmation_required", "draftId": it.DraftID})
				continue
			}
			if g.dryRun {
				results = append(results, map[string]any{"index": i, "ok": true, "draftId": it.DraftID, "dryRun": true})
				success++
				continue
			}
			if err := smtpSendFn(bridge.SMTPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Username: username, Password: pass}, bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body}); err != nil {
				results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error(), "draftId": it.DraftID})
				continue
			}
			results = append(results, map[string]any{"index": i, "ok": true, "draftId": it.DraftID, "sentAt": time.Now().UTC().Format(time.RFC3339)})
			success++
		}
		resp := map[string]any{"results": results, "count": len(results), "success": success, "failed": len(results) - success, "source": "imap"}
		_ = idempotencyStore(st, *idempotencyKey, "message.send-many", items, resp)
		return resp, success > 0, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown message action: " + action}
	}
}

func cmdSearchIMAP(action string, args []string, cfg config.Config, st *model.State) (any, bool, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	query := fs.String("query", "", "query")
	mailbox := fs.String("mailbox", "", "mailbox name (messages only)")
	from := fs.String("from", "", "from filter")
	to := fs.String("to", "", "to filter")
	subject := fs.String("subject", "", "subject filter")
	hasTag := fs.String("has-tag", "", "imap keyword/tag")
	unread := fs.Bool("unread", false, "only unread messages")
	sinceID := fs.String("since-id", "", "minimum UID (inclusive)")
	after := fs.String("after", "", "date filter YYYY-MM-DD")
	before := fs.String("before", "", "date filter YYYY-MM-DD")
	limit := fs.Int("limit", 50, "max results")
	cursor := fs.String("cursor", "", "offset cursor")
	if err := fs.Parse(args); err != nil {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
	}
	c, _, _, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()
	criteria, err := buildIMAPCriteria(*query, *subject, *from, *to, *hasTag, *unread, *sinceID, *after, *before)
	if err != nil {
		return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
	}
	if action == "drafts" {
		if strings.TrimSpace(*mailbox) != "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--mailbox is only supported for search messages"}
		}
		items, err := c.ListMessages("Drafts", criteria)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_search_failed", msg: err.Error()}
		}
		sortByUIDDesc(items)
		start, lim := parsePage(*cursor, *limit)
		paged, next := paginateMessages(items, start, lim)
		out := make([]map[string]any, 0, len(paged))
		for _, m := range paged {
			out = append(out, map[string]any{"id": imapDraftID(m.UID), "uid": m.UID, "to": m.To, "from": m.From, "subject": m.Subject, "date": m.Date.UTC().Format(time.RFC3339)})
		}
		return map[string]any{"drafts": out, "count": len(out), "total": len(items), "nextCursor": next, "source": "imap"}, false, nil
	}
	if action != "messages" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "search supports messages|drafts"}
	}
	targetMailbox := "INBOX"
	if strings.TrimSpace(*mailbox) != "" {
		targetMailbox = strings.TrimSpace(*mailbox)
	}
	items, err := c.ListMessages(targetMailbox, criteria)
	if err != nil {
		return nil, false, cliError{exit: 4, code: "imap_search_failed", msg: err.Error()}
	}
	sortByUIDDesc(items)
	start, lim := parsePage(*cursor, *limit)
	paged, next := paginateMessages(items, start, lim)
	out := make([]map[string]any, 0, len(paged))
	for _, m := range paged {
		out = append(out, map[string]any{"id": imapMessageID(m.UID), "uid": m.UID, "from": m.From, "to": m.To, "subject": m.Subject, "date": m.Date.UTC().Format(time.RFC3339)})
	}
	return map[string]any{"messages": out, "count": len(out), "total": len(items), "nextCursor": next, "mailbox": targetMailbox, "source": "imap"}, false, nil
}

func cmdTagIMAP(action string, args []string, cfg config.Config, st *model.State) (any, bool, error) {
	c, _, _, err := bridgeClient(cfg, st, "")
	if err != nil {
		return nil, false, err
	}
	defer c.Close()
	switch action {
	case "list":
		msgs, err := c.ListMessages("INBOX", "ALL")
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_tag_list_failed", msg: err.Error()}
		}
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
		return map[string]any{"tags": out, "count": len(out), "source": "imap"}, false, nil
	case "create":
		fs := flag.NewFlagSet("tag create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("name", "", "tag name")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if strings.TrimSpace(*name) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name required"}
		}
		return map[string]any{"tag": map[string]any{"name": *name}, "changed": false, "source": "imap"}, false, nil
	case "add", "remove":
		fs := flag.NewFlagSet("tag add/remove", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		msgID := fs.String("message-id", "", "message id")
		tag := fs.String("tag", "", "tag name")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseUID(*msgID)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--message-id required"}
		}
		if strings.TrimSpace(*tag) == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--tag required"}
		}
		if err := c.SetKeyword("INBOX", uid, *tag, action == "add"); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_tag_update_failed", msg: err.Error()}
		}
		return map[string]any{"messageId": imapMessageID(uid), "tag": *tag, "changed": true, "source": "imap"}, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown tag action: " + action}
	}
}

func buildIMAPCriteria(query, subject, from, to, hasTag string, unread bool, sinceID, after, before string) (string, error) {
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
		parts = append(parts, fmt.Sprintf(`KEYWORD "%s"`, escapeSearch(hasTag)))
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
	if d, ok, err := parseDateArg(after); err != nil {
		return "", err
	} else if ok {
		parts = append(parts, "SINCE "+d.Format("02-Jan-2006"))
	}
	if d, ok, err := parseDateArg(before); err != nil {
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
	return strings.ReplaceAll(strings.TrimSpace(s), `"`, `\"`)
}

func parseDateArg(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("invalid date %q (expected YYYY-MM-DD or RFC3339)", s)
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

func loadDraftCreateManifest(path string) ([]draftCreateItem, error) {
	b, err := readManifest(path)
	if err != nil {
		return nil, err
	}
	var items []draftCreateItem
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	for i := range items {
		if len(items[i].To) == 0 {
			return nil, fmt.Errorf("manifest item %d missing to", i)
		}
		if items[i].Body == "" && items[i].BodyFile == "" {
			return nil, fmt.Errorf("manifest item %d needs body or body_file", i)
		}
	}
	return items, nil
}

func loadSendManyManifest(path string) ([]sendManyItem, error) {
	b, err := readManifest(path)
	if err != nil {
		return nil, err
	}
	var items []sendManyItem
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	for i := range items {
		if strings.TrimSpace(items[i].DraftID) == "" {
			return nil, fmt.Errorf("manifest item %d missing draft_id", i)
		}
		if strings.TrimSpace(items[i].ConfirmSend) == "" {
			return nil, fmt.Errorf("manifest item %d missing confirm_send", i)
		}
	}
	return items, nil
}

func readManifest(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
