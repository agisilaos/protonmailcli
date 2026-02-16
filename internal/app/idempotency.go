package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"protonmailcli/internal/model"
)

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
	if len(rec.Response) == 0 {
		return true, map[string]any{"ok": true, "replayed": true}, nil
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
	st.Idempotency[key] = model.IdempotencyRecord{Operation: op, PayloadHash: h, Response: b, CreatedAt: time.Now().UTC()}
	return nil
}

func payloadHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
