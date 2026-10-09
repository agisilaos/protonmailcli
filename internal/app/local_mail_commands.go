package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func cmdDraft(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "create":
		fs, opts := newLocalDraftCreateFlags()
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
		now := time.Now().UTC()
		id := fmt.Sprintf("d_%d", now.UnixNano())
		d := model.Draft{ID: id, To: opts.to, Subject: opts.subject, Body: b, Tags: opts.tags, CreatedAt: now, UpdatedAt: now}
		if !g.dryRun {
			st.Drafts[id] = d
		}
		return localDraftResponse{Draft: d, CreatePath: "local_state", Source: "local"}, true, nil
	case "update":
		fs, opts := newIMAPDraftUpdateFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		d, ok := st.Drafts[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		if flagWasSet(fs, "subject") {
			d.Subject = opts.subject
		}
		if flagWasSet(fs, "body") || opts.bodyFile != "" || opts.stdinBody {
			nextBody, err := loadDraftUpdateBody(fs, opts)
			if err != nil {
				return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
			}
			d.Body = nextBody
		}
		d.UpdatedAt = time.Now().UTC()
		if !g.dryRun {
			st.Drafts[uid] = d
		}
		return localDraftResponse{Draft: d}, true, nil
	case "get":
		fs, opts := newIMAPDraftGetFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		d, ok := st.Drafts[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		return localDraftResponse{Draft: d}, false, nil
	case "list":
		ids := make([]string, 0, len(st.Drafts))
		for id := range st.Drafts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out := make([]model.Draft, 0, len(ids))
		for _, id := range ids {
			out = append(out, st.Drafts[id])
		}
		return localDraftListResponse{Drafts: out, Count: len(out)}, false, nil
	case "delete":
		fs, opts := newIMAPDraftDeleteFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if _, ok := st.Drafts[uid]; !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		if !g.dryRun {
			delete(st.Drafts, uid)
		}
		return draftDeleteResponse{Deleted: true, DraftID: uid}, true, nil
	case "create-many":
		fs, opts := newLocalDraftCreateManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "draft create-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseDraftCreateManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
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
			payload := map[string]any{"to": it.To, "subject": it.Subject, "body": b}
			if found, cached, err := idempotencyLookup(st, it.IdempotencyKey, "local.draft.create-item", payload); err != nil {
				results = append(results, batchItemResponse{Index: i, ErrorCode: errorCodeFromErr(err, "idempotency_conflict"), Error: err.Error()})
				continue
			} else if found {
				item, err := replayBatchItem(cached, i)
				if err != nil {
					return nil, false, err
				}
				results = append(results, item)
				if item.OK {
					success++
				}
				continue
			}
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DryRun: true, To: it.To, Subject: it.Subject})
				success++
				continue
			}
			now := time.Now().UTC()
			id := fmt.Sprintf("d_%d", now.UnixNano())
			d := model.Draft{ID: id, To: it.To, Subject: it.Subject, Body: b, CreatedAt: now, UpdatedAt: now}
			st.Drafts[id] = d
			result := batchItemResponse{Index: i, OK: true, DraftID: id, CreatePath: "local_state"}
			if err := idempotencyStore(st, it.IdempotencyKey, "local.draft.create-item", payload, result); err != nil {
				return nil, false, err
			}
			results = append(results, result)
			success++
		}
		resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "local"}
		if success == 0 && len(results) > 0 {
			resp.exitCode = 1
		} else if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
		return resp, success > 0, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown draft action: " + action}
	}
}

func cmdMessage(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	switch action {
	case "get":
		fs, opts := newIMAPMessageGetFlags()
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		uid, err := parseRequiredUID(opts.id, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		m, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		return localMessageGetResponse{Message: m}, false, nil
	case "send":
		fs, opts := newLocalMessageSendFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(opts.draftID, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		d, ok := st.Drafts[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), opts.confirm, d.ID, "", opts.force); err != nil {
			return nil, false, err
		}
		if opts.force {
			fmt.Fprintln(runtimeStderr, "warning: forcing send by policy override")
		}
		if g.dryRun {
			return sendPlanResponse{Action: "send", DraftID: d.ID, WouldSend: true, DryRun: true, SendPath: "local_state", Source: "local"}, false, nil
		}
		from := firstNonEmpty(st.Auth.Username, cfg.Bridge.Username, "local@example.invalid")
		now := time.Now().UTC()
		d.SentAt = &now
		msgID := fmt.Sprintf("m_%d", now.UnixNano())
		m := model.Message{ID: msgID, DraftID: d.ID, From: from, To: d.To, Subject: d.Subject, Body: d.Body, Tags: d.Tags, SentAt: now}
		st.Messages[msgID] = m
		st.Drafts[d.ID] = d
		return messageSendResponse{Sent: true, Message: m, SendPath: "local_state", Source: "local"}, true, nil
	case "send-many":
		fs, opts := newLocalMessageSendManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseSendManyManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		for i, it := range items {
			if strings.TrimSpace(it.ConfirmSend) == "" {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "missing confirm_send", DraftID: it.DraftID})
				continue
			}
			uid, err := parseRequiredUID(it.DraftID, "--draft-id")
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "invalid draft_id"})
				continue
			}
			d, ok := st.Drafts[uid]
			if !ok {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "not_found", Error: "draft not found", DraftID: it.DraftID})
				continue
			}
			if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), it.ConfirmSend, it.DraftID, uid, false); err != nil {
				code := errorCodeFromErr(err, "confirmation_required")
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: code, Error: code, DraftID: it.DraftID})
				continue
			}
			payload := map[string]any{"draftId": d.ID, "to": d.To, "subject": d.Subject, "body": d.Body}
			if found, cached, err := idempotencyLookup(st, it.IdempotencyKey, "local.message.send-item", payload); err != nil {
				results = append(results, batchItemResponse{Index: i, DraftID: it.DraftID, ErrorCode: errorCodeFromErr(err, "idempotency_conflict"), Error: err.Error()})
				continue
			} else if found {
				item, err := replayBatchItem(cached, i)
				if err != nil {
					return nil, false, err
				}
				results = append(results, item)
				if item.OK {
					success++
				}
				continue
			}
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, DryRun: true, SendPath: "local_state"})
				success++
				continue
			}
			now := time.Now().UTC()
			d.SentAt = &now
			msgID := fmt.Sprintf("m_%d", now.UnixNano())
			from := firstNonEmpty(st.Auth.Username, cfg.Bridge.Username)
			if from == "" {
				from = "local@example.com"
			}
			m := model.Message{ID: msgID, DraftID: d.ID, From: from, To: d.To, Subject: d.Subject, Body: d.Body, Tags: d.Tags, SentAt: now}
			st.Messages[msgID] = m
			st.Drafts[d.ID] = d
			result := batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, SendPath: "local_state", SentAt: now.Format(time.RFC3339)}
			if err := idempotencyStore(st, it.IdempotencyKey, "local.message.send-item", payload, result); err != nil {
				return nil, false, err
			}
			results = append(results, result)
			success++
		}
		resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "local"}
		if success == 0 && len(results) > 0 {
			resp.exitCode = 1
		} else if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
		return resp, success > 0, nil
	case "follow-up":
		fs, opts := newIMAPMessageFollowUpFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message follow-up", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(opts.msgID, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		orig, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		recipients := []string(opts.to)
		if len(recipients) == 0 {
			recipients = localFollowUpRecipients(orig, firstNonEmpty(st.Auth.Username, cfg.Bridge.Username))
		}
		if len(recipients) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "could not resolve recipients; pass --to"}
		}
		bodyText, err := loadBody(opts.body, opts.bodyFile, opts.stdinBody)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		followUpSubject := followUpSubject(opts.subject, orig.Subject)
		payload := map[string]any{
			"messageId": opts.msgID,
			"to":        recipients,
			"subject":   followUpSubject,
			"body":      bodyText,
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.follow-up", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if g.dryRun {
			return messageFollowUpPlanResponse{
				Action:      "follow_up",
				MessageID:   orig.ID,
				To:          recipients,
				Subject:     followUpSubject,
				WouldCreate: true,
				DryRun:      true,
				Source:      "local",
			}, true, nil
		}
		now := time.Now().UTC()
		id := fmt.Sprintf("d_%d", now.UnixNano())
		d := model.Draft{
			ID:        id,
			To:        recipients,
			Subject:   followUpSubject,
			Body:      bodyText,
			CreatedAt: now,
			UpdatedAt: now,
		}
		st.Drafts[id] = d
		resp := localMessageFollowUpResponse{Draft: d, CreatePath: "local_state", Source: "local"}
		_ = idempotencyStore(st, opts.idempotencyKey, "message.follow-up", payload, resp)
		return resp, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown message action: " + action}
	}
}
