package app

import (
	"bytes"
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
	"protonmailcli/internal/output"
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
	res := make([]mailboxInfo, 0, len(boxes))
	for _, b := range boxes {
		res = append(res, mailboxInfo{Name: b})
	}
	return mailboxListResponse{Mailboxes: res, Count: len(res)}, false, nil
}

func cmdDraftIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	var c *bridge.IMAPClient
	var username string
	ensureClient := func() error {
		if c != nil {
			return nil
		}
		client, user, _, err := bridgeClient(cfg, st, "")
		if err != nil {
			return err
		}
		c = client
		username = user
		return nil
	}
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()

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
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		drafts, err := c.ListMessages("Drafts", criteria)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_list_failed", msg: err.Error()}
		}
		sortByUIDDesc(drafts)
		start, lim := parsePage(*cursor, *limit)
		paged, next := paginateMessages(drafts, start, lim)
		out := make([]draftRecord, 0, len(drafts))
		for _, d := range paged {
			out = append(out, draftRecord{
				ID:      imapDraftID(d.UID),
				UID:     d.UID,
				To:      d.To,
				From:    d.From,
				Subject: d.Subject,
				Body:    d.Body,
				Date:    d.Date.UTC().Format(time.RFC3339),
				Flags:   d.Flags,
			})
		}
		return draftListResponse{Drafts: out, Count: len(out), Total: len(drafts), NextCursor: next, Source: "imap"}, false, nil
	case "get":
		fs := flag.NewFlagSet("draft get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(*id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		d, err := c.GetDraft(uid)
		if err != nil {
			return nil, false, cliError{exit: 5, code: "not_found", msg: err.Error()}
		}
		return draftResponse{
			Draft:  draftRecord{ID: imapDraftID(d.UID), UID: d.UID, To: d.To, Subject: d.Subject, Body: d.Body, Flags: d.Flags},
			Source: "imap",
		}, false, nil
	case "create":
		fs := flag.NewFlagSet("draft create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var to sliceFlag
		subject := fs.String("subject", "", "subject")
		body := fs.String("body", "", "body")
		bodyFile := fs.String("body-file", "", "body from file or -")
		stdinBody := fs.Bool("stdin", false, "read body from stdin")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		fs.Var(&to, "to", "recipient (repeat)")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if len(to) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "at least one --to is required"}
		}
		b, err := loadBody(*body, *bodyFile, *stdinBody)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		payload := map[string]any{"to": []string(to), "subject": *subject, "body": b}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "draft.create", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		raw := bridge.BuildRawMessage(username, to, *subject, b)
		if g.dryRun {
			return map[string]any{"action": "draft.create", "wouldCreate": true, "source": "imap"}, true, nil
		}
		uid, err := saveDraftWithFallback(c, cfg, st, username, to, *subject, b, raw)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_create_failed", msg: err.Error()}
		}
		resp := draftResponse{
			Draft:  draftRecord{ID: imapDraftID(uid), UID: uid, To: to, Subject: *subject, Body: b},
			Source: "imap",
		}
		_ = idempotencyStore(st, *idempotencyKey, "draft.create", payload, resp)
		return resp, true, nil
	case "create-many":
		fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		file := fs.String("file", "", "manifest json path or -")
		fromStdin := fs.Bool("stdin", false, "read manifest json from stdin")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				usage := usageForFlagSet(fs)
				if g.mode == output.ModeJSON || g.mode == output.ModePlain {
					return map[string]any{"help": "draft create-many", "usage": usage}, false, nil
				}
				fmt.Fprintln(os.Stdout, usage)
				return map[string]any{"help": "draft create-many"}, false, nil
			}
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		manifestPath, err := resolveManifestInput(*file, *fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		items, err := loadDraftCreateManifest(manifestPath, *fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "draft.create-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		for i, it := range items {
			b, err := loadBody(it.Body, it.BodyFile, false)
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: err.Error()})
				continue
			}
			raw := bridge.BuildRawMessage(username, it.To, it.Subject, b)
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DryRun: true, To: it.To, Subject: it.Subject})
				success++
				continue
			}
			uid, err := saveDraftWithFallback(c, cfg, st, username, it.To, it.Subject, b, raw)
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "imap_draft_create_failed", Error: err.Error()})
				continue
			}
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: imapDraftID(uid), UID: uid})
			success++
		}
		resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
		if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
		_ = idempotencyStore(st, *idempotencyKey, "draft.create-many", items, resp)
		return resp, success > 0, nil
	case "update":
		fs := flag.NewFlagSet("draft update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		subject := fs.String("subject", "", "subject")
		body := fs.String("body", "", "body")
		bodyFile := fs.String("body-file", "", "body from file or -")
		stdinBody := fs.Bool("stdin", false, "read body from stdin")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(*id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		d, err := c.GetDraft(uid)
		if err != nil {
			return nil, false, cliError{exit: 5, code: "not_found", msg: err.Error()}
		}
		if *subject != "" {
			d.Subject = *subject
		}
		if *body != "" || *bodyFile != "" || *stdinBody {
			nextBody, err := loadBody(*body, *bodyFile, *stdinBody)
			if err != nil {
				return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
			}
			d.Body = nextBody
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
		return draftResponse{
			Draft:  draftRecord{ID: imapDraftID(newUID), UID: newUID, To: d.To, Subject: d.Subject, Body: d.Body},
			Source: "imap",
		}, true, nil
	case "delete":
		fs := flag.NewFlagSet("draft delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("draft-id", "", "draft id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(*id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		if g.dryRun {
			return map[string]any{"action": "draft.delete", "draftId": imapDraftID(uid), "wouldDelete": true, "source": "imap"}, true, nil
		}
		if err := c.DeleteDraft(uid); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_delete_failed", msg: err.Error()}
		}
		return struct {
			Deleted bool   `json:"deleted"`
			DraftID string `json:"draftId"`
			Source  string `json:"source"`
		}{Deleted: true, DraftID: imapDraftID(uid), Source: "imap"}, true, nil
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
	var c *bridge.IMAPClient
	var username string
	var password string
	ensureClient := func() error {
		if c != nil {
			return nil
		}
		client, user, pass, err := bridgeClient(cfg, st, "")
		if err != nil {
			return err
		}
		c = client
		username = user
		password = pass
		return nil
	}
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()

	switch action {
	case "get":
		fs := flag.NewFlagSet("message get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("message-id", "", "message id")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(*id, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		msgs, err := c.ListMessages("INBOX", "UID "+uid)
		if err != nil || len(msgs) == 0 {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		m := msgs[0]
		return messageGetResponse{
			Message: messageRecord{
				ID:      imapMessageID(m.UID),
				UID:     m.UID,
				From:    m.From,
				To:      m.To,
				Subject: m.Subject,
				Body:    m.Body,
				Flags:   m.Flags,
			},
			Source: "imap",
		}, false, nil
	case "send":
		fs := flag.NewFlagSet("message send", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		draftID := fs.String("draft-id", "", "draft id")
		confirm := fs.String("confirm-send", "", "confirmation token")
		force := fs.Bool("force", false, "force send without confirm token")
		passwordFile := fs.String("smtp-password-file", "", "path to smtp password file")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				usage := usageForFlagSet(fs)
				if g.mode == output.ModeJSON || g.mode == output.ModePlain {
					return map[string]any{"help": "message send", "usage": usage}, false, nil
				}
				fmt.Fprintln(os.Stdout, usage)
				return map[string]any{"help": "message send"}, false, nil
			}
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(*draftID, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
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
		if err := validateSendSafety(cfg, g.noInput, *confirm, *draftID, uid, *force); err != nil {
			return nil, false, err
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
		resp := struct {
			Sent    bool   `json:"sent"`
			DraftID string `json:"draftId"`
			Source  string `json:"source"`
			SentAt  string `json:"sentAt"`
		}{Sent: true, DraftID: imapDraftID(uid), Source: "imap", SentAt: time.Now().UTC().Format(time.RFC3339)}
		_ = idempotencyStore(st, *idempotencyKey, "message.send", payload, resp)
		return resp, true, nil
	case "send-many":
		fs := flag.NewFlagSet("message send-many", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		file := fs.String("file", "", "manifest json path or -")
		fromStdin := fs.Bool("stdin", false, "read manifest json from stdin")
		passwordFile := fs.String("smtp-password-file", "", "path to smtp password file")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				usage := usageForFlagSet(fs)
				if g.mode == output.ModeJSON || g.mode == output.ModePlain {
					return map[string]any{"help": "message send-many", "usage": usage}, false, nil
				}
				fmt.Fprintln(os.Stdout, usage)
				return map[string]any{"help": "message send-many"}, false, nil
			}
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		manifestPath, err := resolveManifestInput(*file, *fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		items, err := loadSendManyManifest(manifestPath, *fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "message.send-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		pass := strings.TrimSpace(password)
		if *passwordFile != "" {
			_, p, err := resolveBridgeCredentials(cfg, st, *passwordFile)
			if err != nil {
				return nil, false, err
			}
			pass = p
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		for i, it := range items {
			uid, err := parseUID(it.DraftID)
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "invalid draft_id"})
				continue
			}
			d, err := c.GetDraft(uid)
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "not_found", Error: "draft not found", DraftID: it.DraftID})
				continue
			}
			if err := validateSendSafety(cfg, g.noInput, it.ConfirmSend, it.DraftID, uid, false); err != nil {
				code := errorCodeFromErr(err, "confirmation_required")
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: code, Error: code, DraftID: it.DraftID})
				continue
			}
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, DryRun: true})
				success++
				continue
			}
			if err := smtpSendFn(bridge.SMTPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Username: username, Password: pass}, bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body}); err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "send_failed", Error: err.Error(), DraftID: it.DraftID})
				continue
			}
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, SentAt: time.Now().UTC().Format(time.RFC3339)})
			success++
		}
		resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
		if success == 0 && len(results) > 0 {
			resp.exitCode = 1
		} else if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
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
	out := make([]messageRecord, 0, len(paged))
	for _, m := range paged {
		out = append(out, messageRecord{ID: imapMessageID(m.UID), UID: m.UID, From: m.From, To: m.To, Subject: m.Subject, Date: m.Date.UTC().Format(time.RFC3339)})
	}
	return messageListResponse{Messages: out, Count: len(out), Total: len(items), NextCursor: next, Mailbox: targetMailbox, Source: "imap"}, false, nil
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
		uid, err := parseRequiredUID(*msgID, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
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
	return strings.ReplaceAll(strings.TrimSpace(s), `"`, `\"`)
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

func loadSendManyManifest(path string, fromStdin bool) ([]sendManyItem, error) {
	b, err := readManifest(path, fromStdin)
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
	return strings.TrimRight("Usage of "+fs.Name()+":\n"+b.String(), "\n")
}
