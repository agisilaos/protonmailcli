package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func cmdDraftIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State, checkpoint func(model.State) error) (any, bool, error) {
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
		fs, opts := newIMAPDraftListFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		criteria, err := buildIMAPCriteria(opts.query, "", opts.from, opts.to, "", false, "", opts.after, opts.before)
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
		start, lim := parsePage(opts.cursor, opts.limit)
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
		fs, opts := newIMAPDraftGetFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
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
		fs, opts := newIMAPDraftCreateFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		if len(opts.to) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "at least one --to is required"}
		}
		b, err := loadBody(opts.body, opts.bodyFile, opts.stdinBody)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		payload := map[string]any{"to": []string(opts.to), "subject": opts.subject, "body": b}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "draft.create", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		raw := bridge.BuildRawMessage(username, opts.to, opts.subject, b)
		if g.dryRun {
			return map[string]any{"action": "draft.create", "wouldCreate": true, "source": "imap"}, true, nil
		}
		if err := reserveDraft(st, opts.idempotencyKey, "draft.create", payload, checkpoint); err != nil {
			return nil, false, err
		}
		uid, createPath, err := saveDraftWithFallback(c, cfg, st, username, opts.to, opts.subject, b, raw, nil)
		if err != nil {
			failure := draftCreationError(err)
			if saveErr := completeDraft(st, opts.idempotencyKey, "draft.create", payload, nil, &failure, checkpoint); saveErr != nil {
				return nil, false, saveErr
			}
			return nil, false, failure
		}
		resp := draftResponse{
			Draft:      draftRecord{ID: imapDraftID(uid), UID: uid, To: opts.to, Subject: opts.subject, Body: b},
			CreatePath: createPath,
			Source:     "imap",
		}
		if err := completeDraft(st, opts.idempotencyKey, "draft.create", payload, resp, nil, checkpoint); err != nil {
			return nil, false, err
		}
		return resp, opts.idempotencyKey == "", nil
	case "create-many":
		fs, opts := newIMAPDraftCreateManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "draft create-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseDraftCreateManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "draft.create-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		if !g.dryRun {
			if err := reserveDraft(st, opts.idempotencyKey, "draft.create-many", items, checkpoint); err != nil {
				return nil, false, err
			}
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		for i, it := range items {
			if len(it.To) == 0 {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "missing to"})
				continue
			}
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
			uid, createPath, err := saveDraftWithFallback(c, cfg, st, username, it.To, it.Subject, b, raw, nil)
			if err != nil {
				failure := draftCreationError(err)
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: failure.code, Error: failure.msg})
				continue
			}
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: imapDraftID(uid), UID: uid, CreatePath: createPath})
			success++
		}
		resp := draftCreationBatchResult(results, success)
		if !g.dryRun {
			if err := completeDraft(st, opts.idempotencyKey, "draft.create-many", items, resp, nil, checkpoint); err != nil {
				return nil, false, err
			}
		}
		return resp, opts.idempotencyKey == "" && success > 0, nil
	case "update":
		fs, opts := newIMAPDraftUpdateFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
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
		if opts.subject != "" {
			d.Subject = opts.subject
		}
		if opts.body != "" || opts.bodyFile != "" || opts.stdinBody {
			nextBody, err := loadBody(opts.body, opts.bodyFile, opts.stdinBody)
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
		fs, opts := newIMAPDraftDeleteFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
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

func draftCreationError(err error) cliError {
	code := "imap_draft_create_failed"
	hint := ""
	if errors.Is(err, bridge.ErrAppendUncertain) {
		code = "imap_draft_create_uncertain"
		hint = "Inspect Drafts before retrying; the draft may already exist."
	}
	return cliError{exit: 4, code: code, msg: err.Error(), hint: hint}
}

func draftCreationBatchResult(results []batchItemResponse, success int) batchResultResponse {
	resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
	if resp.Failed > 0 {
		resp.exitCode = 4
		if success > 0 {
			resp.exitCode = 10
		}
	}
	return resp
}

func saveDraftWithFallback(c imapDraftClient, cfg config.Config, st *model.State, username string, to []string, subject, body, raw string, extraHeaders map[string]string) (string, string, error) {
	uid, err := c.AppendDraft(raw)
	if err == nil {
		return uid, "imap_append", nil
	}
	if errors.Is(err, bridge.ErrAppendUncertain) {
		return "", "", err
	}
	uid, err = createDraftViaMoveFallback(cfg, st, username, to, subject, body, strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD")), extraHeaders)
	if err != nil {
		return "", "", err
	}
	return uid, "smtp_move_fallback", nil
}

func createDraftViaMoveFallback(cfg config.Config, st *model.State, username string, to []string, subject, body, envPassword string, extraHeaders map[string]string) (string, error) {
	_, password, err := resolveBridgeCredentials(cfg, st, "")
	if err != nil {
		if strings.TrimSpace(envPassword) == "" {
			return "", err
		}
		password = strings.TrimSpace(envPassword)
	}
	token := fmt.Sprintf("pmail-%d", time.Now().UnixNano())
	headers := map[string]string{"X-Pmail-Draft-Token": token}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	if err := smtpSendFn(bridgeSMTPConfig(cfg, username, password), bridge.SendInput{
		From:         username,
		To:           []string{username},
		Subject:      subject,
		Body:         body,
		ExtraHeaders: headers,
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
		fs, opts := newIMAPMessageGetFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		mailbox, uid, err := parseMailboxUID(opts.id, "INBOX")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		msgs, err := c.ListMessages(mailbox, "UID "+uid)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_message_fetch_failed", msg: err.Error()}
		}
		if len(msgs) == 0 {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		m := msgs[0]
		return messageGetResponse{
			Message: messageRecord{
				ID:      imapMessageIDForMailbox(mailbox, m.UID),
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
		fs, opts := newIMAPMessageSendFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(opts.draftID, "--draft-id")
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
		payload := map[string]any{"draftId": opts.draftID, "confirm": opts.confirm, "force": opts.force, "to": d.To, "subject": d.Subject, "body": d.Body}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.send", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), opts.confirm, opts.draftID, uid, opts.force); err != nil {
			return nil, false, err
		}
		if g.dryRun {
			return sendPlanResponse{Action: "send", DraftID: imapDraftID(uid), WouldSend: true, DryRun: true, SendPath: "smtp", Source: "imap"}, true, nil
		}
		pass := strings.TrimSpace(password)
		if opts.passwordFile != "" {
			_, p, err := resolveBridgeCredentials(cfg, st, opts.passwordFile)
			if err != nil {
				return nil, false, err
			}
			pass = p
		}
		err = bridge.Send(bridgeSMTPConfig(cfg, username, pass), bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body})
		if err != nil {
			return nil, false, cliError{exit: 4, code: "send_failed", msg: err.Error()}
		}
		resp := struct {
			Sent     bool   `json:"sent"`
			DraftID  string `json:"draftId"`
			SendPath string `json:"sendPath,omitempty"`
			Source   string `json:"source"`
			SentAt   string `json:"sentAt"`
		}{Sent: true, DraftID: imapDraftID(uid), SendPath: "smtp", Source: "imap", SentAt: time.Now().UTC().Format(time.RFC3339)}
		_ = idempotencyStore(st, opts.idempotencyKey, "message.send", payload, resp)
		return resp, true, nil
	case "send-many":
		fs, opts := newIMAPMessageSendManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseSendManyManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.send-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		pass := strings.TrimSpace(password)
		if opts.passwordFile != "" {
			_, p, err := resolveBridgeCredentials(cfg, st, opts.passwordFile)
			if err != nil {
				return nil, false, err
			}
			pass = p
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		for i, it := range items {
			if strings.TrimSpace(it.ConfirmSend) == "" {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "missing confirm_send", DraftID: it.DraftID})
				continue
			}
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
			if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), it.ConfirmSend, it.DraftID, uid, false); err != nil {
				code := errorCodeFromErr(err, "confirmation_required")
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: code, Error: code, DraftID: it.DraftID})
				continue
			}
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, DryRun: true, SendPath: "smtp"})
				success++
				continue
			}
			if err := smtpSendFn(bridgeSMTPConfig(cfg, username, pass), bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body}); err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "send_failed", Error: err.Error(), DraftID: it.DraftID})
				continue
			}
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, SendPath: "smtp", SentAt: time.Now().UTC().Format(time.RFC3339)})
			success++
		}
		resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
		if success == 0 && len(results) > 0 {
			resp.exitCode = 1
		} else if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
		_ = idempotencyStore(st, opts.idempotencyKey, "message.send-many", items, resp)
		return resp, success > 0, nil
	case "follow-up":
		fs, opts := newIMAPMessageFollowUpFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message follow-up", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		mailbox, uid, err := parseMailboxUID(opts.msgID, "INBOX")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--message-id required"}
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		msgs, err := c.ListMessages(mailbox, "UID "+uid)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_message_fetch_failed", msg: err.Error()}
		}
		if len(msgs) == 0 {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		orig := msgs[0]
		recipients := []string(opts.to)
		if len(recipients) == 0 {
			recipients = imapFollowUpRecipients(orig.From, orig.To, username)
		}
		if len(recipients) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "could not resolve recipients; pass --to"}
		}
		bodyText, err := loadBody(opts.body, opts.bodyFile, opts.stdinBody)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		followSubject := followUpSubject(opts.subject, orig.Subject)
		inReplyTo, refs := threadHeaders(orig.MessageID, orig.References)
		if inReplyTo == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "message has no Message-ID; cannot create threaded follow-up"}
		}
		payload := map[string]any{
			"messageId":  imapMessageIDForMailbox(mailbox, uid),
			"to":         recipients,
			"subject":    followSubject,
			"body":       bodyText,
			"inReplyTo":  inReplyTo,
			"references": refs,
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.follow-up", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if g.dryRun {
			return messageFollowUpPlanResponse{
				Action:          "follow_up",
				MessageID:       imapMessageIDForMailbox(mailbox, uid),
				To:              recipients,
				Subject:         followSubject,
				WouldCreate:     true,
				DryRun:          true,
				Source:          "imap",
				ThreadInReplyTo: inReplyTo,
				References:      refs,
			}, true, nil
		}
		extraHeaders := map[string]string{
			"In-Reply-To": inReplyTo,
			"References":  strings.Join(refs, " "),
		}
		raw := bridge.BuildRawMessageWithHeaders(username, recipients, followSubject, bodyText, extraHeaders)
		newUID, createPath, err := saveDraftWithFallback(c, cfg, st, username, recipients, followSubject, bodyText, raw, extraHeaders)
		if err != nil {
			return nil, false, draftCreationError(err)
		}
		resp := messageFollowUpResponse{
			Draft: draftRecord{
				ID:      imapDraftID(newUID),
				UID:     newUID,
				To:      recipients,
				Subject: followSubject,
				Body:    bodyText,
			},
			CreatePath:      createPath,
			Source:          "imap",
			ThreadInReplyTo: inReplyTo,
			References:      refs,
		}
		_ = idempotencyStore(st, opts.idempotencyKey, "message.follow-up", payload, resp)
		return resp, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown message action: " + action}
	}
}
