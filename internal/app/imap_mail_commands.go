package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func (a App) cmdDraftIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
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
		mailbox, err := c.DraftMailboxName()
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_list_failed", msg: err.Error()}
		}
		drafts, err := c.ListMessages(mailbox, criteria)
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_list_failed", msg: err.Error()}
		}
		sortByUIDDesc(drafts)
		start, lim := parsePage(opts.cursor, opts.limit)
		paged, next := paginateMessages(drafts, start, lim)
		out := make([]draftRecord, 0, len(drafts))
		for _, d := range paged {
			out = append(out, draftRecord{
				ID:      imapMessageIDForMailbox(d.Mailbox, d.UID),
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
		mailbox, err := resolveDraftIdentity(c, newMessageInspection(g, cfg, st, ""), opts.id)
		if err != nil {
			return nil, false, err
		}
		d, err := c.GetMessage(mailbox, uid)
		if err != nil {
			return nil, false, draftFetchError(err)
		}
		return draftResponse{
			Draft:  draftRecord{ID: imapMessageIDForMailbox(d.Mailbox, d.UID), UID: d.UID, To: d.To, Subject: d.Subject, Body: d.Body, Flags: d.Flags},
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
		if err := a.loadState(opts.idempotencyKey != "", "draft.create", opts.idempotencyKey); err != nil {
			return nil, false, err
		}
		if err := receiptOperationConflict(st, opts.idempotencyKey, "draft.create"); err != nil {
			return nil, false, err
		}
		if err := pendingIdempotencyError(st, opts.idempotencyKey, "draft.create"); err != nil {
			return nil, false, err
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
		if err := a.checkStateWrite(); err != nil {
			return nil, false, err
		}
		if err := ensureClient(); err != nil {
			return nil, false, err
		}
		if err := bridge.ValidateMessageHeaders(username, opts.to, opts.subject, nil); err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if g.dryRun {
			if err := c.ExamineDraftDestination(); err != nil {
				return nil, false, draftCreationError(err)
			}
			return map[string]any{"action": "draft.create", "wouldCreate": true, "source": "imap"}, true, nil
		}
		raw := bridge.BuildRawMessage(username, opts.to, opts.subject, b)
		reserve := func(destination string) error {
			return reserveDraft(st, opts.idempotencyKey, "draft.create", payload, destination, a.checkpoint)
		}
		saved, createPath, err := saveDraftWithFallback(c, cfg, st, username, opts.to, opts.subject, b, raw, nil, reserve)
		var intentErr *draftIntentError
		if errors.As(err, &intentErr) {
			return nil, false, intentErr.cause
		}
		if err != nil {
			failure := draftCreationError(err)
			if saveErr := completeDraft(st, opts.idempotencyKey, "draft.create", payload, nil, &failure, "", a.checkpoint); saveErr != nil {
				return nil, false, saveErr
			}
			return nil, false, failure
		}
		resp := draftResponse{
			Draft:      draftRecord{ID: saved.ID, UID: saved.UID, To: opts.to, Subject: opts.subject, Body: b},
			CreatePath: createPath,
			Source:     "imap",
		}
		if err := completeDraft(st, opts.idempotencyKey, "draft.create", payload, resp, nil, "", a.checkpoint); err != nil {
			return nil, false, err
		}
		return resp, false, nil
	case "create-many":
		fs, opts := newIMAPDraftCreateManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "draft create-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if opts.idempotencyKey != "" {
			if err := a.loadState(true, "draft.create-many", opts.idempotencyKey); err != nil {
				return nil, false, err
			}
			if rec, ok := st.Idempotency[opts.idempotencyKey]; ok && rec.Operation != "draft.create-many" {
				return nil, false, cliError{exit: 6, code: "idempotency_conflict", msg: "idempotency key already used with different operation"}
			}
			if err := pendingIdempotencyError(st, opts.idempotencyKey, "draft.create-many"); err != nil {
				return nil, false, err
			}
		}
		items, err := parseDraftCreateManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		var itemKeys []string
		if opts.idempotencyKey == "" {
			keyed := false
			for _, item := range items {
				itemKeys = append(itemKeys, item.IdempotencyKey)
				keyed = keyed || item.IdempotencyKey != ""
			}
			if err := a.loadState(keyed, "imap.draft.create-item", itemKeys...); err != nil {
				return nil, false, err
			}
		}
		_, replay := st.Idempotency[opts.idempotencyKey]
		// A completed command receipt takes precedence over its item records.
		// Without one, report unfinished items before consuming any body source.
		if opts.idempotencyKey == "" || !replay {
			for _, item := range items {
				if err := pendingIdempotencyError(st, item.IdempotencyKey, "imap.draft.create-item"); err != nil {
					return nil, false, err
				}
			}
		}
		var dispatchWriteErr error
		if opts.idempotencyKey == "" {
			dispatchWriteErr = a.checkBatchStateWrite(st, g.dryRun, "imap.draft.create-item", itemKeys...)
		} else {
			dispatchWriteErr = a.checkStateWrite()
		}
		var payload []draftCreateBatchPayloadItem
		var batch *preparedDraftBatch
		if opts.idempotencyKey != "" {
			batch, err = prepareDraftBatch(items, st, opts.idempotencyKey)
			if err != nil {
				return nil, false, cliError{exit: 1, code: "batch_snapshot_failed", msg: err.Error()}
			}
			defer func() {
				if err := batch.close(); err != nil {
					fmt.Fprintf(runtimeStderr, "warning: draft snapshot cleanup failed: %v\n", err)
				}
			}()
			payload = batch.payload
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "draft.create-many", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		batchReserved := false
		dispatched := false
		examineDestination := sync.OnceValue(func() error { return c.ExamineDraftDestination() })
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		started := false
		for i, it := range items {
			if len(it.To) == 0 {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: "missing to"})
				continue
			}
			if err := draftItemKeyConflict(st, it.IdempotencyKey, opts.idempotencyKey); err != nil {
				results = append(results, batchItemResponse{Index: i, ErrorCode: "idempotency_conflict", Error: err.Error()})
				continue
			}
			var b string
			var err error
			switch {
			case batch == nil || it.BodyFile == "":
				b, err = loadDraftItemBody(it)
			case batch.payload[i].bodyError != nil:
				err = batch.payload[i].bodyError
			default:
				b, err = batch.readSnapshotBody(i)
				if err != nil {
					hint := "Restore temporary storage and retry with the same batch key."
					if batchReserved {
						hint = "The batch key remains pending. Keep the key and state, and reconcile the preceding rows in INBOX and Drafts before retrying missing drafts."
					}
					return draftCreationBatchResult(results, success), false, cliError{
						exit: 1, code: "batch_snapshot_failed", msg: fmt.Sprintf("read private draft snapshot for item %d: %v", i, err), hint: hint,
					}
				}
			}
			if err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: err.Error()})
				continue
			}
			from := effectiveBridgeUsername(cfg, st)
			if err := bridge.ValidateMessageHeaders(from, it.To, it.Subject, nil); err != nil {
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: "validation_error", Error: err.Error()})
				continue
			}
			payload := map[string]any{"to": it.To, "subject": it.Subject, "body": b}
			if found, cached, err := idempotencyLookup(st, it.IdempotencyKey, "imap.draft.create-item", payload); err != nil {
				results = append(results, batchItemResponse{Index: i, ErrorCode: errorCodeFromErr(err, "idempotency_conflict"), Error: err.Error()})
				continue
			} else if found {
				item, err := replayBatchItem(cached, i)
				if err != nil {
					return draftCreationBatchResult(results, success), false, err
				}
				results = append(results, item)
				if item.OK {
					success++
				}
				continue
			}
			if !started {
				if dispatchWriteErr != nil {
					return draftCreationBatchResult(results, success), false, dispatchWriteErr
				}
				if err := ensureClient(); err != nil {
					return draftCreationBatchResult(results, success), false, err
				}
				started = true
			}
			if g.dryRun {
				if err := examineDestination(); err != nil {
					failure := draftCreationError(err)
					results = append(results, batchItemResponse{Index: i, ErrorCode: failure.code, Error: failure.msg})
					continue
				}
				results = append(results, batchItemResponse{Index: i, OK: true, DryRun: true, To: it.To, Subject: it.Subject})
				success++
				continue
			}
			raw := bridge.BuildRawMessage(username, it.To, it.Subject, b)
			reserve := func(destination string) error {
				if batch != nil && !batchReserved {
					if err := reserveDraft(st, opts.idempotencyKey, "draft.create-many", batch.payload, destination, a.checkpoint); err != nil {
						return err
					}
					batchReserved = true
				}
				if err := reserveDraft(st, it.IdempotencyKey, "imap.draft.create-item", payload, destination, a.checkpoint); err != nil {
					return err
				}
				dispatched = true
				return nil
			}
			saved, createPath, err := saveDraftWithFallback(c, cfg, st, username, it.To, it.Subject, b, raw, nil, reserve)
			var intentErr *draftIntentError
			if errors.As(err, &intentErr) {
				message := fmt.Sprintf("could not save draft intent for batch row %d. This row was not dispatched", i)
				if batchReserved {
					message += ". The batch key remains pending"
				}
				return draftCreationBatchResult(results, success), false, draftBatchCheckpointError(intentErr.cause, message)
			}
			if err != nil {
				failure := draftCreationError(err)
				results = append(results, batchItemResponse{Index: i, ErrorCode: failure.code, Error: failure.msg})
				if saveErr := completeDraft(st, it.IdempotencyKey, "imap.draft.create-item", payload, nil, &failure, "", a.checkpoint); saveErr != nil {
					message := fmt.Sprintf("could not save the %s outcome for draft batch row %d", failure.code, i)
					return draftCreationBatchResult(results, success), false, draftBatchCheckpointError(saveErr, message)
				}
				continue
			}
			result := batchItemResponse{Index: i, OK: true, DraftID: saved.ID, UID: saved.UID, CreatePath: createPath}
			results = append(results, result)
			success++
			if err := completeDraft(st, it.IdempotencyKey, "imap.draft.create-item", payload, result, nil, "", a.checkpoint); err != nil {
				message := fmt.Sprintf("created draft %s for batch row %d, but its receipt could not be saved", saved.ID, i)
				return draftCreationBatchResult(results, success), false, draftBatchCheckpointError(err, message)
			}
		}
		resp := draftCreationBatchResult(results, success)
		if !g.dryRun {
			if err := completeDraft(st, opts.idempotencyKey, "draft.create-many", payload, resp, nil, "", a.checkpoint); err != nil {
				if !dispatched {
					if errors.Is(err, store.ErrCheckpointUncertain) {
						failure := cliError{hint: err.Error()}
						errors.As(err, &failure)
						failure.exit, failure.code = 1, "state_save_failed"
						failure.msg = "batch receipt was published, but its durability could not be confirmed"
						failure.hint = "Recovered item outcomes are shown. " + failure.hint
						return resp, false, failure
					}
					return resp, false, cliError{exit: 1, code: "state_save_failed", msg: "batch idempotency key was not recorded", hint: "Recovered item outcomes are shown; restore state write access before using this command key"}
				}
				return resp, false, draftBatchCheckpointError(err, "draft batch finished, but its final receipt could not be durably saved")
			}
		}
		return resp, false, nil
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
		mailbox, err := resolveDraftIdentity(c, newMessageInspection(g, cfg, st, ""), opts.id)
		if err != nil {
			return nil, false, err
		}
		d, err := c.GetMessage(mailbox, uid)
		if err != nil {
			return nil, false, draftFetchError(err)
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
		if err := bridge.ValidateMessageHeaders(username, d.To, d.Subject, draftThreadHeaders(d)); err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if g.dryRun {
			return map[string]any{"action": "draft.update", "draftId": imapMessageIDForMailbox(mailbox, uid), "wouldUpdate": true, "source": "imap"}, true, nil
		}
		newUID, err := c.AppendDraft(bridge.BuildRawMessageWithHeaders(username, d.To, d.Subject, d.Body, draftThreadHeaders(d)))
		if err != nil {
			if errors.Is(err, bridge.ErrAppendUncertain) {
				return nil, false, cliError{exit: 4, code: "imap_draft_update_uncertain", msg: err.Error(), hint: "Original draft " + imapMessageIDForMailbox(mailbox, uid) + " was retained. Inspect Drafts before retrying; a replacement may exist."}
			}
			return nil, false, cliError{exit: 4, code: "imap_draft_update_failed", msg: err.Error()}
		}
		if err := c.DeleteDraft(uid); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_update_uncertain", msg: "replacement was created but original deletion failed: " + err.Error(), hint: "Replacement " + imapMessageIDForMailbox(mailbox, newUID) + " is confirmed. Inspect original " + imapMessageIDForMailbox(mailbox, uid) + " before retrying; do not create another replacement."}
		}
		return draftResponse{
			Draft:  draftRecord{ID: imapMessageIDForMailbox(mailbox, newUID), UID: newUID, To: d.To, Subject: d.Subject, Body: d.Body},
			Source: "imap",
		}, false, nil
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
		mailbox, err := resolveDraftIdentity(c, newMessageInspection(g, cfg, st, ""), opts.id)
		if err != nil {
			return nil, false, err
		}
		if g.dryRun {
			return map[string]any{"action": "draft.delete", "draftId": imapMessageIDForMailbox(mailbox, uid), "wouldDelete": true, "source": "imap"}, true, nil
		}
		if err := c.DeleteDraft(uid); err != nil {
			return nil, false, cliError{exit: 4, code: "imap_draft_delete_failed", msg: err.Error()}
		}
		return struct {
			Deleted bool   `json:"deleted"`
			DraftID string `json:"draftId"`
			Source  string `json:"source"`
		}{Deleted: true, DraftID: imapMessageIDForMailbox(mailbox, uid), Source: "imap"}, false, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown draft action: " + action}
	}
}

// APPEND or fallback MOVE can have completed even when its reply is lost. Keep the
// original destination with the error so recovery does not follow a new role.
type draftDestinationUncertainError struct {
	destination string
	cause       error
}

func (e *draftDestinationUncertainError) Error() string { return e.cause.Error() }
func (e *draftDestinationUncertainError) Unwrap() error { return e.cause }

func draftCreationError(err error) cliError {
	code := "imap_draft_create_failed"
	hint := ""
	if errors.Is(err, bridge.ErrAppendUncertain) || errors.Is(err, bridge.ErrSMTPDataUncertain) {
		code = "imap_draft_create_uncertain"
		hint = draftRecoveryHint + " The draft may already exist."
		var destinationFailure *draftDestinationUncertainError
		if errors.As(err, &destinationFailure) {
			hint += fmt.Sprintf(" Inspect the original destination mailbox %q even if the Drafts role has moved.", destinationFailure.destination)
		}
	}
	return cliError{exit: 4, code: code, msg: err.Error(), hint: hint}
}

func sendCheckpointError(err error, message string) error {
	if errors.Is(err, store.ErrCheckpointUncertain) {
		return err
	}
	return pendingSendError(message + ": " + err.Error())
}

func draftBatchCheckpointError(err error, message string) error {
	var failure cliError
	if !errors.As(err, &failure) {
		return fmt.Errorf("%s: %w", message, err)
	}
	failure.msg = message
	if errors.Is(err, store.ErrCheckpointUncertain) {
		failure.msg += ". The receipt was published, but its durability could not be confirmed"
	}
	failure.hint = "Keep the reported results and skip confirmed drafts when recovering the batch. " + failure.hint
	return failure
}

func draftCreationBatchResult(results []batchItemResponse, success int) batchResultResponse {
	// Batch rows have no hint field; retain current guidance in their error text,
	// including when these outcomes were restored from older receipts.
	for i := range results {
		if results[i].ErrorCode == "imap_draft_create_uncertain" {
			results[i].Error = withDraftRecoveryHint(results[i].Error)
		}
	}
	resp := batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
	if resp.Failed > 0 {
		resp.exitCode = 4
		if success > 0 {
			resp.exitCode = 10
		}
	}
	return resp
}

type draftIntentError struct{ cause error }

func (e *draftIntentError) Error() string { return e.cause.Error() }
func (e *draftIntentError) Unwrap() error { return e.cause }

func saveDraftWithFallback(c imapDraftClient, cfg config.Config, st *model.State, username string, to []string, subject, body, raw string, extraHeaders map[string]string, reserve func(string) error) (draftRecord, string, error) {
	mailbox, err := c.DraftMailboxName()
	if err != nil {
		return draftRecord{}, "", err
	}
	if reserve != nil {
		if err := reserve(mailbox); err != nil {
			return draftRecord{}, "", &draftIntentError{cause: err}
		}
	}
	uid, err := c.AppendDraft(raw)
	if err == nil {
		return draftRecord{ID: imapMessageIDForMailbox(mailbox, uid), UID: uid}, "imap_append", nil
	}
	if errors.Is(err, bridge.ErrAppendUncertain) {
		return draftRecord{}, "", &draftDestinationUncertainError{destination: mailbox, cause: fmt.Errorf("%w: APPEND destination %q", err, mailbox)}
	}
	if !errors.Is(err, bridge.ErrAppendRejected) {
		return draftRecord{}, "", err
	}
	if err := c.ExamineDraftDestination(); err != nil {
		return draftRecord{}, "", err
	}
	draft, err := createDraftViaMoveFallback(cfg, st, c.TLSPolicy(), mailbox, username, to, subject, body, strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD")), extraHeaders)
	if err != nil {
		return draftRecord{}, "", err
	}
	return draft, "smtp_move_fallback", nil
}

func createDraftViaMoveFallback(cfg config.Config, st *model.State, policy *bridge.TLSPolicy, draftsMailbox, username string, to []string, subject, body, envPassword string, extraHeaders map[string]string) (draftRecord, error) {
	_, password, err := resolveBridgeCredentials(cfg, st, "")
	if err != nil {
		if strings.TrimSpace(envPassword) == "" {
			return draftRecord{}, err
		}
		password = strings.TrimSpace(envPassword)
	}
	token := fmt.Sprintf("pmail-%d", time.Now().UnixNano())
	headers := map[string]string{"X-Pmail-Draft-Token": token}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	if err := smtpSendFn(bridgeSMTPConfig(cfg, username, password, policy), bridge.SendInput{
		From:         username,
		To:           to,
		EnvelopeTo:   []string{username},
		Subject:      subject,
		Body:         body,
		ExtraHeaders: headers,
	}); err != nil {
		return draftRecord{}, err
	}
	c2, _, _, err := openBridgeClientFn(cfg, st, policy)
	if err != nil {
		return draftRecord{}, fmt.Errorf("%w: SMTP accepted fallback; %v", bridge.ErrAppendUncertain, err)
	}
	defer c2.Close()
	var uid string
	for i := 0; i < 10; i++ {
		uids, err := c2.SearchUIDs("INBOX", fmt.Sprintf(`HEADER X-Pmail-Draft-Token "%s"`, escapeSearch(token)))
		if err == nil && len(uids) == 1 {
			uid = uids[len(uids)-1]
			break
		}
		time.Sleep(1 * time.Second)
	}
	if uid == "" {
		return draftRecord{}, fmt.Errorf("%w: SMTP accepted fallback but INBOX identity could not be confirmed", bridge.ErrAppendUncertain)
	}
	if err := c2.MoveUID("INBOX", uid, draftsMailbox); err != nil {
		return draftRecord{}, &draftDestinationUncertainError{destination: draftsMailbox, cause: fmt.Errorf("%w: fallback MOVE from INBOX UID %s to %q did not confirm completion: %v", bridge.ErrAppendUncertain, uid, draftsMailbox, err)}
	}
	draftUIDs, err := c2.SearchUIDs(draftsMailbox, fmt.Sprintf(`HEADER X-Pmail-Draft-Token "%s"`, escapeSearch(token)))
	if err != nil || len(draftUIDs) != 1 {
		return draftRecord{}, &draftDestinationUncertainError{destination: draftsMailbox, cause: fmt.Errorf("%w: fallback moved from INBOX UID %s to %q but destination identity is unconfirmed", bridge.ErrAppendUncertain, uid, draftsMailbox)}
	}
	return draftRecord{ID: imapMessageIDForMailbox(draftsMailbox, draftUIDs[0]), UID: draftUIDs[0]}, nil
}

// A lost final DATA reply can follow acceptance even without a receipt key.
// Keyed attempts also retain their conservative pending-record behavior.
func messageSendFailure(err error, draftID string, keyed bool) cliError {
	if !keyed && !errors.Is(err, bridge.ErrSMTPDataUncertain) {
		return cliError{exit: 4, code: "send_failed", msg: err.Error()}
	}
	failure := pendingSendError("send attempt for " + draftID + " did not confirm delivery: " + err.Error())
	if !keyed {
		failure.hint = "Do not retry automatically. Inspect delivery for this draft before deciding whether another send is needed. Unkeyed sends have no durable replay protection."
	}
	return failure
}

func (a App) cmdMessageIMAP(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	var c *bridge.IMAPClient
	var username string
	var password string
	ensureClient := func(passwordFile string) error {
		if c != nil {
			return nil
		}
		client, user, pass, err := bridgeClient(cfg, st, passwordFile)
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
		if err := a.loadState(false, ""); err != nil {
			return nil, false, err
		}
		if err := ensureClient(opts.passwordFile); err != nil {
			return nil, false, err
		}
		m, err := c.GetMessage(mailbox, uid)
		if errors.Is(err, bridge.ErrMessageNotFound) {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_message_fetch_failed", msg: err.Error()}
		}
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
		if err := a.loadState(opts.idempotencyKey != "", "message.send", opts.idempotencyKey); err != nil {
			return nil, false, err
		}
		if err := receiptOperationConflict(st, opts.idempotencyKey, "message.send"); err != nil {
			return nil, false, err
		}
		if err := pendingIdempotencyError(st, opts.idempotencyKey, "message.send"); err != nil {
			return nil, false, err
		}
		if err := a.checkStateWriteForReplay(st, "message.send", opts.idempotencyKey); err != nil {
			return nil, false, err
		}
		if err := ensureClient(opts.passwordFile); err != nil {
			return nil, false, err
		}
		d, mailbox, err := draftForSend(c, newMessageInspection(g, cfg, st, opts.passwordFile), st, opts.idempotencyKey, "message.send", opts.draftID, uid)
		if err != nil {
			return nil, false, err
		}
		payload := map[string]any{"draftId": opts.draftID, "confirm": opts.confirm, "force": opts.force, "to": d.To, "subject": d.Subject, "body": d.Body, "headers": draftThreadHeaders(d)}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.send", payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := a.checkStateWrite(); err != nil {
			return nil, false, err
		}
		if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), opts.confirm, opts.draftID, uid, opts.force); err != nil {
			return nil, false, err
		}
		if g.dryRun {
			return sendPlanResponse{Action: "send", DraftID: imapMessageIDForMailbox(mailbox, uid), WouldSend: true, DryRun: true, SendPath: "smtp", Source: "imap"}, true, nil
		}
		pass := strings.TrimSpace(password)
		if err := reserveSend(st, opts.idempotencyKey, "message.send", payload, imapMessageIDForMailbox(mailbox, uid), a.checkpoint); err != nil {
			return nil, false, err
		}
		err = bridge.Send(bridgeSMTPConfig(cfg, username, pass, c.TLSPolicy()), bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body, ExtraHeaders: draftThreadHeaders(d)})
		if err != nil {
			return nil, false, messageSendFailure(err, imapMessageIDForMailbox(mailbox, uid), opts.idempotencyKey != "")
		}
		resp := struct {
			Sent     bool   `json:"sent"`
			DraftID  string `json:"draftId"`
			SendPath string `json:"sendPath,omitempty"`
			Source   string `json:"source"`
			SentAt   string `json:"sentAt"`
		}{Sent: true, DraftID: imapMessageIDForMailbox(mailbox, uid), SendPath: "smtp", Source: "imap", SentAt: time.Now().UTC().Format(time.RFC3339)}
		if err := completeSend(st, opts.idempotencyKey, "message.send", payload, resp, resp.DraftID, a.checkpoint); err != nil {
			failure := sendCheckpointError(err, "SMTP accepted "+resp.DraftID+" but its receipt could not be saved")
			return nil, false, failure
		}
		return resp, false, nil
	case "send-many":
		fs, opts := newIMAPMessageSendManyFlags()
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "message send-many", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if opts.idempotencyKey != "" {
			if err := a.loadState(true, "message.send-many", opts.idempotencyKey); err != nil {
				return nil, false, err
			}
			if err := receiptOperationConflict(st, opts.idempotencyKey, "message.send-many"); err != nil {
				return nil, false, err
			}
			if err := pendingIdempotencyError(st, opts.idempotencyKey, "message.send-many"); err != nil {
				return nil, false, err
			}
		}
		items, err := parseSendManyManifestInput(opts.file, opts.fromStdin)
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		if opts.idempotencyKey == "" {
			var keys []string
			for _, item := range items {
				if item.IdempotencyKey != "" {
					keys = append(keys, item.IdempotencyKey)
				}
			}
			if err := a.loadState(len(keys) > 0, "imap.message.send-item", keys...); err != nil {
				return nil, false, err
			}
		}
		if found, cached, err := idempotencyLookup(st, opts.idempotencyKey, "message.send-many", items); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		for _, item := range items {
			if err := pendingIdempotencyError(st, item.IdempotencyKey, "imap.message.send-item"); err != nil {
				return nil, false, err
			}
		}
		replayOperation, replayKeys := "message.send-many", []string{opts.idempotencyKey}
		if opts.idempotencyKey == "" {
			replayOperation = "imap.message.send-item"
			replayKeys = make([]string, 0, len(items))
			for _, item := range items {
				replayKeys = append(replayKeys, item.IdempotencyKey)
			}
		}
		if err := a.checkBatchStateWrite(st, g.dryRun, replayOperation, replayKeys...); err != nil {
			return nil, false, err
		}
		if opts.idempotencyKey != "" {
			if err := ensureClient(opts.passwordFile); err != nil {
				return nil, false, err
			}
			if !g.dryRun {
				if err := reserveSend(st, opts.idempotencyKey, "message.send-many", items, "", a.checkpoint); err != nil {
					return nil, false, err
				}
			}
		}
		results := make([]batchItemResponse, 0, len(items))
		success := 0
		batchResult := func() batchResultResponse {
			return batchResultResponse{Results: results, Count: len(results), Success: success, Failed: len(results) - success, Source: "imap"}
		}
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
			if it.IdempotencyKey != "" && it.IdempotencyKey == opts.idempotencyKey {
				results = append(results, batchItemResponse{Index: i, DraftID: it.DraftID, ErrorCode: "idempotency_conflict", Error: "item and command idempotency keys must be distinct"})
				continue
			}
			if err := receiptOperationConflict(st, it.IdempotencyKey, "imap.message.send-item"); err != nil {
				results = append(results, batchItemResponse{Index: i, DraftID: it.DraftID, ErrorCode: "idempotency_conflict", Error: err.Error()})
				continue
			}
			if err := ensureClient(opts.passwordFile); err != nil {
				return batchResult(), false, err
			}
			d, mailbox, err := draftForSend(c, newMessageInspection(g, cfg, st, opts.passwordFile), st, it.IdempotencyKey, "imap.message.send-item", it.DraftID, uid)
			if err != nil {
				results = append(results, batchItemResponse{Index: i, DraftID: it.DraftID, ErrorCode: errorCodeFromErr(err, "validation_error"), Error: err.Error()})
				continue
			}
			payload := map[string]any{"draftId": it.DraftID, "confirm": it.ConfirmSend, "to": d.To, "subject": d.Subject, "body": d.Body, "headers": draftThreadHeaders(d)}
			if found, cached, err := idempotencyLookup(st, it.IdempotencyKey, "imap.message.send-item", payload); err != nil {
				if errorCodeFromErr(err, "") == "imap_send_uncertain" {
					return batchResult(), false, err
				}
				results = append(results, batchItemResponse{Index: i, DraftID: it.DraftID, ErrorCode: errorCodeFromErr(err, "idempotency_conflict"), Error: err.Error()})
				continue
			} else if found {
				item, err := replayBatchItem(cached, i)
				if err != nil {
					return batchResult(), false, err
				}
				results = append(results, item)
				if item.OK {
					success++
				}
				continue
			}
			if opts.idempotencyKey != "" || it.IdempotencyKey != "" {
				if err := a.checkStateWrite(); err != nil {
					return batchResult(), false, err
				}
			}
			if err := validateSendSafety(cfg, isNonInteractiveSend(g, runtimeStdinIsTTY()), it.ConfirmSend, it.DraftID, uid, false); err != nil {
				code := errorCodeFromErr(err, "confirmation_required")
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: code, Error: code, DraftID: it.DraftID})
				continue
			}
			if g.dryRun {
				results = append(results, batchItemResponse{Index: i, OK: true, DraftID: imapMessageIDForMailbox(mailbox, uid), DryRun: true, SendPath: "smtp"})
				success++
				continue
			}
			if err := reserveSend(st, it.IdempotencyKey, "imap.message.send-item", payload, imapMessageIDForMailbox(mailbox, uid), a.checkpoint); err != nil {
				failure := cliError{exit: 1, code: "state_save_failed", msg: err.Error()}
				_ = errors.As(err, &failure)
				failure.msg = fmt.Sprintf("batch item %d (%s) was not dispatched: %s", i, imapMessageIDForMailbox(mailbox, uid), failure.msg)
				failure.hint = "Do not rerun the whole manifest. Keep the earlier outcomes in data.results and reconcile them before retrying only undispatched rows. " + failure.hint
				return batchResult(), false, failure
			}
			if err := smtpSendFn(bridgeSMTPConfig(cfg, username, strings.TrimSpace(password), c.TLSPolicy()), bridge.SendInput{From: username, To: d.To, Subject: d.Subject, Body: d.Body, ExtraHeaders: draftThreadHeaders(d)}); err != nil {
				keyed := opts.idempotencyKey != "" || it.IdempotencyKey != ""
				failure := messageSendFailure(err, imapMessageIDForMailbox(mailbox, uid), keyed)
				if failure.code == "imap_send_uncertain" {
					results = append(results, batchItemResponse{Index: i, ErrorCode: failure.code, Error: failure.msg, DraftID: imapMessageIDForMailbox(mailbox, uid)})
					resp := batchResult()
					return resp, false, failure
				}
				results = append(results, batchItemResponse{Index: i, OK: false, ErrorCode: failure.code, Error: failure.msg, DraftID: it.DraftID})
				continue
			}
			result := batchItemResponse{Index: i, OK: true, DraftID: imapMessageIDForMailbox(mailbox, uid), SendPath: "smtp", SentAt: time.Now().UTC().Format(time.RFC3339)}
			results = append(results, result)
			success++
			if err := completeSend(st, it.IdempotencyKey, "imap.message.send-item", payload, result, result.DraftID, a.checkpoint); err != nil {
				failure := sendCheckpointError(err, fmt.Sprintf("SMTP accepted batch item %d (%s) but its receipt could not be saved", i, result.DraftID))
				return batchResult(), false, failure
			}
		}
		resp := batchResult()
		if success == 0 && len(results) > 0 {
			resp.exitCode = 1
		} else if success > 0 && (len(results)-success) > 0 {
			resp.exitCode = 10
		}
		if !g.dryRun {
			if err := completeSend(st, opts.idempotencyKey, "message.send-many", items, resp, "", a.checkpoint); err != nil {
				return resp, false, sendCheckpointError(err, "batch result receipt could not be saved")
			}
		}
		return resp, false, nil
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
		if err := a.loadState(opts.idempotencyKey != "", "message.follow-up", opts.idempotencyKey); err != nil {
			return nil, false, err
		}
		if err := receiptOperationConflict(st, opts.idempotencyKey, "message.follow-up"); err != nil {
			return nil, false, err
		}
		if err := pendingIdempotencyError(st, opts.idempotencyKey, "message.follow-up"); err != nil {
			return nil, false, err
		}
		if err := a.checkStateWriteForReplay(st, "message.follow-up", opts.idempotencyKey); err != nil {
			return nil, false, err
		}
		if err := ensureClient(""); err != nil {
			return nil, false, err
		}
		if err := validateLegacyFollowUpSource(c, st, opts.idempotencyKey, mailbox); err != nil {
			return nil, false, err
		}
		orig, err := c.GetMessage(mailbox, uid)
		if errors.Is(err, bridge.ErrMessageNotFound) {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		if err != nil {
			return nil, false, cliError{exit: 4, code: "imap_message_fetch_failed", msg: err.Error()}
		}
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
		if err := bridge.ValidateMessageHeaders(username, recipients, followSubject, map[string]string{"In-Reply-To": inReplyTo, "References": strings.Join(refs, " ")}); err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		payload := map[string]any{
			"messageId":  imapMessagePayloadID(mailbox, uid),
			"to":         recipients,
			"subject":    followSubject,
			"body":       bodyText,
			"inReplyTo":  inReplyTo,
			"references": refs,
		}
		if found, cached, err := followUpIdempotencyLookup(st, opts.idempotencyKey, mailbox, uid, payload); err != nil {
			return nil, false, err
		} else if found {
			return cached, false, nil
		}
		if err := a.checkStateWrite(); err != nil {
			return nil, false, err
		}
		if g.dryRun {
			if err := c.ExamineDraftDestination(); err != nil {
				return nil, false, draftCreationError(err)
			}
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
		reserve := func(destination string) error {
			return reserveDraft(st, opts.idempotencyKey, "message.follow-up", payload, destination, a.checkpoint)
		}
		saved, createPath, err := saveDraftWithFallback(c, cfg, st, username, recipients, followSubject, bodyText, raw, extraHeaders, reserve)
		var intentErr *draftIntentError
		if errors.As(err, &intentErr) {
			return nil, false, intentErr.cause
		}
		if err != nil {
			failure := draftCreationError(err)
			if err := completeDraft(st, opts.idempotencyKey, "message.follow-up", payload, nil, &failure, imapMessageIDForMailbox(mailbox, uid), a.checkpoint); err != nil {
				return nil, false, err
			}
			return nil, false, failure
		}
		resp := messageFollowUpResponse{
			Draft: draftRecord{
				ID:      saved.ID,
				UID:     saved.UID,
				To:      recipients,
				Subject: followSubject,
				Body:    bodyText,
			},
			CreatePath:      createPath,
			Source:          "imap",
			ThreadInReplyTo: inReplyTo,
			References:      refs,
		}
		if err := completeDraft(st, opts.idempotencyKey, "message.follow-up", payload, resp, nil, imapMessageIDForMailbox(mailbox, uid), a.checkpoint); err != nil {
			return nil, false, err
		}
		return resp, false, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown message action: " + action}
	}
}

func draftFetchError(err error) cliError {
	if errors.Is(err, bridge.ErrMessageNotFound) {
		return cliError{exit: 5, code: "not_found", msg: "draft not found"}
	}
	return cliError{exit: 4, code: "imap_draft_fetch_failed", msg: err.Error()}
}

func draftThreadHeaders(d bridge.DraftMessage) map[string]string {
	headers := map[string]string{}
	if d.InReplyTo != "" {
		headers["In-Reply-To"] = d.InReplyTo
	}
	if d.References != "" {
		headers["References"] = d.References
	}
	return headers
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func resolveDraftIdentity(c *bridge.IMAPClient, inspection messageInspection, id string) (string, error) {
	mailbox, uid, err := parseMailboxUID(id, "Drafts")
	if err != nil {
		return "", cliError{exit: 2, code: "validation_error", msg: err.Error()}
	}
	discovered, err := c.ResolveDraftMailbox()
	legacy := isLegacyDraftID(id)
	if legacy && discovered.AmbiguousLegacyID {
		reads := make([]string, 0, len(discovered.LegacyCandidates))
		for _, candidate := range discovered.LegacyCandidates {
			exactID := imapMessageIDForMailbox(candidate, uid)
			reads = append(reads, inspection.command(exactID))
		}
		return "", cliError{exit: 2, code: "validation_error", msg: fmt.Sprintf("ambiguous draft ID %q: it could refer to any of %q. Compare the messages below. Use an unambiguous exact ID for the selected Drafts mailbox, or resolve the server's Drafts configuration before retrying.\n%s", id, discovered.LegacyCandidates, strings.Join(reads, "\n"))}
	}
	if err != nil {
		return "", cliError{exit: 4, code: "imap_draft_fetch_failed", msg: err.Error()}
	}
	if !legacy && mailbox != discovered.Name {
		return "", cliError{exit: 2, code: "validation_error", msg: fmt.Sprintf("draft ID mailbox %q does not match Drafts mailbox %q", mailbox, discovered.Name)}
	}
	return discovered.Name, nil
}
