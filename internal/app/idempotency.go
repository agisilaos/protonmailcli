package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode/utf8"

	"protonmailcli/internal/model"
	"protonmailcli/internal/store"
)

func receiptOperationConflict(st *model.State, key, operation string) error {
	if record, exists := st.Idempotency[key]; key != "" && exists && record.Operation != operation {
		return cliError{exit: 6, code: "idempotency_conflict", msg: "idempotency key already used with different operation"}
	}
	return nil
}

func idempotencyLookup(st *model.State, key, op string, payload any) (bool, any, error) {
	if key == "" {
		return false, nil, nil
	}
	h, err := payloadHash(payload)
	if err != nil {
		return false, nil, err
	}
	rec, ok := st.Idempotency[key]
	if !ok {
		return false, nil, nil
	}
	if rec.Operation != op || rec.PayloadHash != h {
		return false, nil, cliError{exit: 6, code: "idempotency_conflict", msg: "idempotency key already used with different payload"}
	}
	if err := pendingIdempotencyError(st, key, op); err != nil {
		return true, nil, err
	}
	if rec.Failure != nil {
		f := rec.Failure
		hint := f.Hint
		if f.Code == "imap_draft_create_uncertain" {
			hint = withDraftRecoveryHint(hint)
		}
		return true, nil, cliError{exit: f.Exit, code: f.Code, msg: f.Message, hint: hint}
	}
	if len(rec.Response) == 0 {
		return true, map[string]any{"ok": true, "replayed": true}, nil
	}
	if op == "draft.create-many" || op == "message.send-many" {
		var cached batchResultResponse
		if err := json.Unmarshal(rec.Response, &cached); err != nil {
			return false, nil, err
		}
		if op == "draft.create-many" && cached.Source == "imap" {
			return true, draftCreationBatchResult(cached.Results, cached.Success), nil
		}
		if op == "message.send-many" {
			if cached.Failed > 0 {
				cached.exitCode = 1
				if cached.Success > 0 {
					cached.exitCode = 10
				}
			}
			return true, cached, nil
		}
	}
	var out any
	if err := json.Unmarshal(rec.Response, &out); err != nil {
		return false, nil, err
	}
	return true, out, nil
}

func idempotencyStore(st *model.State, key, op string, payload, response any) error {
	if key == "" {
		return nil
	}
	h, err := payloadHash(payload)
	if err != nil {
		return err
	}
	b, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if st.Idempotency == nil {
		st.Idempotency = map[string]model.IdempotencyRecord{}
	}
	record := model.IdempotencyRecord{Operation: op, PayloadHash: h, Response: b, CreatedAt: time.Now().UTC()}
	if rows, ok := payload.([]draftCreateBatchPayloadItem); ok {
		for i, row := range rows {
			if row.BodyFileFingerprint == unusedDraftBodyFingerprint {
				if record.SkippedDraftBodies == nil {
					record.SkippedDraftBodies = make(map[int]bool)
				}
				record.SkippedDraftBodies[i] = true
			}
		}
	}
	st.Idempotency[key] = record
	return nil
}

func payloadHash(v any) (string, error) {
	// JSON replaces malformed UTF-8. Preserve its original bytes in a distinct
	// representation while keeping existing text-only receipt hashes stable.
	if payload, ok := v.(map[string]any); ok {
		if body, ok := payload["body"].(string); ok && !utf8.ValidString(body) {
			copy := maps.Clone(payload)
			sum := sha256.Sum256([]byte(body))
			copy["body"] = struct {
				SHA256 string `json:"sha256"`
			}{hex.EncodeToString(sum[:])}
			v = copy
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

const draftRecoveryHint = "Inspect INBOX and Drafts before retrying."

// Receipt text is historical, but the possible fallback location is unchanged.
func withDraftRecoveryHint(text string) string {
	if strings.Contains(text, draftRecoveryHint) {
		return text
	}
	return strings.TrimSpace(text + " " + draftRecoveryHint)
}

func pendingDraftError(destination string) cliError {
	hint := draftRecoveryHint
	if destination != "" {
		hint += fmt.Sprintf(" Inspect the original destination mailbox %q even if the Drafts role has moved.", destination)
	}
	return cliError{exit: 4, code: "imap_draft_create_uncertain", msg: "draft creation has an unfinished recovery record", hint: hint + " Keep this key and state until you reconcile every batch item. Only after confirming absence from both mailboxes, use a new key for missing drafts."}
}

// reserveDraft persists intent before any keyed draft can be dispatched.
func reserveDraft(st *model.State, key, op string, payload any, destination string, checkpoint func(model.State) error) error {
	if key == "" {
		return nil
	}
	if err := idempotencyStore(st, key, op, payload, nil); err != nil {
		return err
	}
	rec := st.Idempotency[key]
	rec.Status = "pending"
	rec.DraftDestination = destination
	st.Idempotency[key] = rec
	if err := checkpoint(*st); err != nil {
		return cliError{exit: 1, code: "state_save_failed", msg: "cannot persist draft intent; no draft dispatched", hint: err.Error()}
	}
	return nil
}

func completeDraft(st *model.State, key, op string, payload, response any, failure *cliError, sourceID string, checkpoint func(model.State) error) error {
	if key == "" {
		return nil
	}
	destination := st.Idempotency[key].DraftDestination
	if err := idempotencyStore(st, key, op, payload, response); err != nil {
		return err
	}
	rec := st.Idempotency[key]
	rec.Status = "complete"
	rec.SourceID = sourceID
	if failure != nil {
		rec.Failure = &model.IdempotencyFailure{Exit: failure.exit, Code: failure.code, Message: failure.msg, Hint: failure.hint}
	}
	st.Idempotency[key] = rec
	if err := checkpoint(*st); err != nil {
		failure := pendingDraftError(destination)
		if errors.Is(err, store.ErrCheckpointUncertain) {
			failure.msg = "draft receipt was published, but its durability could not be confirmed"
			failure.hint = "Keep the original key and payload; a matching retry may replay the visible receipt. Retain confirmed outcomes and do not dispatch them again."
		}
		failure.hint += " Checkpoint error: " + err.Error()
		return errors.Join(failure, err)
	}
	return nil
}

func replayBatchItem(cached any, index int) (batchItemResponse, error) {
	raw, err := json.Marshal(cached)
	if err != nil {
		return batchItemResponse{}, err
	}
	var item batchItemResponse
	if err := json.Unmarshal(raw, &item); err != nil {
		return item, err
	}
	item.Index = index
	return item, nil
}

func pendingSendError(detail string) cliError {
	return cliError{exit: 4, code: "imap_send_uncertain", msg: detail, hint: "Do not retry automatically. Inspect delivery before retrying; keep this key and state and reconcile every batch item."}
}

// A persisted intent prevents replay after interruption or a failed receipt save.
func reserveSend(st *model.State, key, op string, payload any, sourceID string, checkpoint func(model.State) error) error {
	if key == "" {
		return nil
	}
	if err := idempotencyStore(st, key, op, payload, nil); err != nil {
		return err
	}
	rec := st.Idempotency[key]
	rec.Status = "pending"
	rec.SourceID = sourceID
	st.Idempotency[key] = rec
	if err := checkpoint(*st); err != nil {
		return cliError{exit: 1, code: "state_save_failed", msg: "cannot persist send intent; this send was not dispatched", hint: err.Error()}
	}
	return nil
}

func completeSend(st *model.State, key, op string, payload, response any, sourceID string, checkpoint func(model.State) error) error {
	if key == "" {
		return nil
	}
	if err := idempotencyStore(st, key, op, payload, response); err != nil {
		return pendingSendError("send receipt could not be encoded: " + err.Error())
	}
	rec := st.Idempotency[key]
	rec.Status = "complete"
	rec.SourceID = sourceID
	st.Idempotency[key] = rec
	if err := checkpoint(*st); err != nil {
		failure := pendingSendError("send completed but its receipt could not be saved: " + err.Error())
		if errors.Is(err, store.ErrCheckpointUncertain) {
			failure.msg = "send receipt was published, but its durability could not be confirmed"
			failure.hint = "Keep the original key and payload; a matching retry may replay the visible receipt. Do not dispatch confirmed sends again. Checkpoint error: " + err.Error()
		}
		return errors.Join(failure, err)
	}
	return nil
}

// Pending recovery must remain visible even when the backend is unavailable.
func pendingIdempotencyError(st *model.State, key, op string) error {
	if key == "" {
		return nil
	}
	rec, ok := st.Idempotency[key]
	if !ok || rec.Operation != op || rec.Status == "" || rec.Status == "complete" {
		return nil
	}
	if op == "message.send" || op == "message.send-many" || op == "imap.message.send-item" {
		detail := "send has an unfinished recovery record"
		if sourceID := strings.TrimSpace(rec.SourceID); isQualifiedIMAPID(sourceID) {
			if mailbox, uid, err := parseMailboxUID(sourceID, ""); err == nil {
				detail = "send for " + imapMessageIDForMailbox(mailbox, uid) + " has an unfinished recovery record"
			}
		}
		return pendingSendError(detail)
	}
	return pendingDraftError(rec.DraftDestination)
}
