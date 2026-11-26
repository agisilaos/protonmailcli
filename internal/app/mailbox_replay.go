package app

import (
	"encoding/json"
	"maps"
	"strings"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/model"
)

// A completed send is verified against its recorded source, even if another
// mailbox has since acquired the Drafts role. The caller still compares the
// entire request and fetched message with the saved payload before replaying.
func draftForSend(c *bridge.IMAPClient, inspection messageInspection, st *model.State, key, op, id, uid string) (bridge.DraftMessage, string, error) {
	if rec, ok := st.Idempotency[key]; key != "" && ok {
		if rec.Operation != op {
			return bridge.DraftMessage{}, "", cliError{exit: 6, code: "idempotency_conflict", msg: "idempotency key already used with different operation"}
		}
		if rec.Status == "" || rec.Status == "complete" {
			sourceID := strings.TrimSpace(rec.SourceID)
			if sourceID == "" && rec.Failure == nil {
				// A versioned receipt or non-Drafts name is exact even without
				// source metadata. The old imap:Drafts form remains an alias.
				var receipt struct {
					DraftID string `json:"draftId"`
					Sent    bool   `json:"sent"`
					OK      bool   `json:"ok"`
				}
				if json.Unmarshal(rec.Response, &receipt) == nil && (receipt.Sent || receipt.OK) {
					receiptID := strings.TrimSpace(receipt.DraftID)
					_, _, err := parseMailboxUID(receiptID, "Drafts")
					if err == nil && isQualifiedIMAPID(receiptID) && !isLegacyDraftID(receiptID) {
						sourceID = receiptID
					}
				}
			}
			if sourceID != "" {
				mailbox, savedUID, err := parseMailboxUID(sourceID, "Drafts")
				if err != nil || !isQualifiedIMAPID(sourceID) || savedUID != uid {
					return bridge.DraftMessage{}, "", cliError{exit: 6, code: "idempotency_conflict", msg: "idempotency key already used with different draft identity"}
				}
				message, err := c.GetMessage(mailbox, uid)
				if err != nil {
					return bridge.DraftMessage{}, "", draftFetchError(err)
				}
				return message, mailbox, nil
			}
		}
	}
	mailbox, err := resolveDraftIdentity(c, inspection, id)
	if err != nil {
		return bridge.DraftMessage{}, "", err
	}
	draft, err := c.GetMessage(mailbox, uid)
	if err != nil {
		return bridge.DraftMessage{}, "", draftFetchError(err)
	}
	return draft, mailbox, nil
}

// Old hashes cannot distinguish names that differed only by surrounding
// whitespace. Verify their source even when the unpadded hash already matches,
// and before SELECT can turn a missing source into a transport-style failure.
func validateLegacyFollowUpSource(c *bridge.IMAPClient, st *model.State, key, mailbox string) error {
	rec, exists := st.Idempotency[key]
	if key == "" || !exists || rec.Operation != "message.follow-up" || rec.SourceID != "" || (rec.Status != "" && rec.Status != "complete") {
		return nil
	}
	boxes, err := c.ListMailboxes()
	if err != nil {
		return cliError{exit: 4, code: "imap_message_fetch_failed", msg: err.Error()}
	}
	legacyMailbox := legacyFollowUpMailbox(mailbox)
	candidates := make(map[string]bool)
	sourcePresent := false
	for _, box := range boxes {
		legacyName := legacyFollowUpMailbox(box.Name)
		if legacyName == legacyMailbox || (strings.EqualFold(legacyName, "INBOX") && strings.EqualFold(legacyMailbox, "INBOX")) {
			name := box.Name
			if strings.EqualFold(name, "INBOX") {
				name = "INBOX"
			}
			candidates[name] = true
			sourcePresent = sourcePresent || box.Name == mailbox || (name == "INBOX" && strings.EqualFold(mailbox, "INBOX"))
		}
	}
	if len(candidates) != 1 || !sourcePresent {
		return cliError{exit: 6, code: "idempotency_conflict", msg: "legacy follow-up source is missing or ambiguous"}
	}
	return nil
}

// Source discovery is checked before fetching the original message. Accept an
// old normalized hash only for a legacy record with the same complete payload.
func followUpIdempotencyLookup(st *model.State, key, mailbox, uid string, payload map[string]any) (bool, any, error) {
	found, cached, lookupErr := idempotencyLookup(st, key, "message.follow-up", payload)
	if errorCodeFromErr(lookupErr, "") != "idempotency_conflict" {
		return found, cached, lookupErr
	}
	rec := st.Idempotency[key]
	legacyMailbox := legacyFollowUpMailbox(mailbox)
	if rec.Operation != "message.follow-up" || rec.SourceID != "" || (rec.Status != "" && rec.Status != "complete") || legacyMailbox == mailbox {
		return found, cached, lookupErr
	}
	legacyPayload := maps.Clone(payload)
	legacyPayload["messageId"] = imapMessagePayloadID(legacyMailbox, uid)
	hash, err := payloadHash(legacyPayload)
	if err != nil {
		return false, nil, err
	}
	if hash != rec.PayloadHash {
		return found, cached, lookupErr
	}
	return idempotencyLookup(st, key, "message.follow-up", legacyPayload)
}

func legacyFollowUpMailbox(mailbox string) string {
	name := strings.TrimSpace(mailbox)
	if name == "" {
		return "INBOX"
	}
	return name
}
