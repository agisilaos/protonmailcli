package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func cmdDraft(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "create":
		fs := flag.NewFlagSet("draft create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var to sliceFlag
		var tags sliceFlag
		subject := fs.String("subject", "", "subject")
		body := fs.String("body", "", "body")
		bodyFile := fs.String("body-file", "", "body from file or -")
		stdinBody := fs.Bool("stdin", false, "read body from stdin")
		fs.Var(&to, "to", "recipient (repeat)")
		fs.Var(&tags, "tag", "tag (repeat)")
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
		now := time.Now().UTC()
		id := fmt.Sprintf("d_%d", now.UnixNano())
		d := model.Draft{ID: id, To: to, Subject: *subject, Body: b, Tags: tags, CreatedAt: now, UpdatedAt: now}
		if !g.dryRun {
			st.Drafts[id] = d
		}
		return localDraftResponse{Draft: d, CreatePath: "local_state", Source: "local"}, true, nil
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
		d, ok := st.Drafts[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
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
		d.UpdatedAt = time.Now().UTC()
		if !g.dryRun {
			st.Drafts[uid] = d
		}
		return localDraftResponse{Draft: d}, true, nil
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
		if _, ok := st.Drafts[uid]; !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		if !g.dryRun {
			delete(st.Drafts, uid)
		}
		return draftDeleteResponse{Deleted: true, DraftID: uid}, true, nil
	case "create-many":
		fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		file := fs.String("file", "", "manifest json path or -")
		fromStdin := fs.Bool("stdin", false, "read manifest json from stdin")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "draft create-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseDraftCreateManifestInput(*file, *fromStdin)
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
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DryRun: true, To: it.To, Subject: it.Subject})
				success++
				continue
			}
			now := time.Now().UTC()
			id := fmt.Sprintf("d_%d", now.UnixNano())
			d := model.Draft{ID: id, To: it.To, Subject: it.Subject, Body: b, CreatedAt: now, UpdatedAt: now}
			st.Drafts[id] = d
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: id, CreatePath: "local_state"})
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
		m, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		return localMessageGetResponse{Message: m}, false, nil
	case "send":
		fs := flag.NewFlagSet("message send", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		draftID := fs.String("draft-id", "", "draft id")
		confirm := fs.String("confirm-send", "", "confirmation token")
		force := fs.Bool("force", false, "force send without confirm token")
		passwordFile := fs.String("smtp-password-file", "", "path to smtp password file")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(*draftID, "--draft-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		d, ok := st.Drafts[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "draft not found"}
		}
		if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), *confirm, d.ID, "", *force); err != nil {
			return nil, false, err
		}
		if *force {
			fmt.Fprintln(runtimeStderr, "warning: forcing send by policy override")
		}
		password := strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD"))
		candidatePasswordFile := firstNonEmpty(*passwordFile, st.Auth.PasswordFile, cfg.Bridge.PasswordFile)
		if password == "" && candidatePasswordFile != "" {
			b, err := os.ReadFile(filepath.Clean(config.Expand(candidatePasswordFile)))
			if err != nil {
				return nil, false, cliError{exit: 2, code: "validation_error", msg: "cannot read smtp password file"}
			}
			password = strings.TrimSpace(string(b))
		}
		if g.dryRun {
			return sendPlanResponse{Action: "send", DraftID: d.ID, WouldSend: true, DryRun: true, SendPath: "local_state", Source: "local"}, true, nil
		}
		from := firstNonEmpty(st.Auth.Username, cfg.Bridge.Username)
		if from == "" {
			return nil, false, cliError{exit: 3, code: "config_error", msg: "bridge username is missing", hint: "Run setup or auth login and set username"}
		}
		if err := bridge.Send(bridge.SMTPConfig{Host: cfg.Bridge.Host, Port: cfg.Bridge.SMTPPort, Username: from, Password: password}, bridge.SendInput{From: from, To: d.To, Subject: d.Subject, Body: d.Body}); err != nil {
			return nil, false, cliError{exit: 4, code: "send_failed", msg: err.Error()}
		}
		now := time.Now().UTC()
		d.SentAt = &now
		msgID := fmt.Sprintf("m_%d", now.UnixNano())
		m := model.Message{ID: msgID, DraftID: d.ID, From: from, To: d.To, Subject: d.Subject, Body: d.Body, Tags: d.Tags, SentAt: now}
		st.Messages[msgID] = m
		st.Drafts[d.ID] = d
		return messageSendResponse{Sent: true, Message: m, SendPath: "local_state", Source: "local"}, true, nil
	case "send-many":
		fs := flag.NewFlagSet("message send-many", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		file := fs.String("file", "", "manifest json path or -")
		fromStdin := fs.Bool("stdin", false, "read manifest json from stdin")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		items, err := parseSendManyManifestInput(*file, *fromStdin)
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
			results = append(results, batchItemResponse{Index: i, OK: true, DraftID: it.DraftID, SendPath: "local_state", SentAt: now.Format(time.RFC3339)})
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
		fs := flag.NewFlagSet("message follow-up", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		msgID := fs.String("message-id", "", "message id")
		var to sliceFlag
		subject := fs.String("subject", "", "subject override")
		body := fs.String("body", "", "body")
		bodyFile := fs.String("body-file", "", "body from file or -")
		stdinBody := fs.Bool("stdin", false, "read body from stdin")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		fs.Var(&to, "to", "recipient (repeat)")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message follow-up", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(*msgID, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		orig, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		recipients := []string(to)
		if len(recipients) == 0 {
			recipients = localFollowUpRecipients(orig, firstNonEmpty(st.Auth.Username, cfg.Bridge.Username))
		}
		if len(recipients) == 0 {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "could not resolve recipients; pass --to"}
		}
		bodyText, err := loadBody(*body, *bodyFile, *stdinBody)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		followUpSubject := followUpSubject(*subject, orig.Subject)
		payload := map[string]any{
			"messageId": *msgID,
			"to":        recipients,
			"subject":   followUpSubject,
			"body":      bodyText,
		}
		if found, cached, err := idempotencyLookup(st, *idempotencyKey, "message.follow-up", payload); err != nil {
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
		_ = idempotencyStore(st, *idempotencyKey, "message.follow-up", payload, resp)
		return resp, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown message action: " + action}
	}
}
