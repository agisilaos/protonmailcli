package app

import "protonmailcli/internal/model"

func (a App) checkStateWrite() error {
	if a.stateWriteError != nil {
		return a.stateWriteError()
	}
	return nil
}

// Without a writer, unkeyed rows can run and terminal keys can be checked.
// A missing reservation still requires write access before dispatch.
func (a App) checkStateWriteForReplay(st *model.State, operation string, keys ...string) error {
	err := a.checkStateWrite()
	if err == nil || len(keys) == 0 {
		return err
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		record, exists := st.Idempotency[key]
		// The caller can reject another operation's key from this snapshot.
		if exists && record.Operation != operation {
			continue
		}
		if !exists || (record.Status != "" && record.Status != "complete") {
			return err
		}
	}
	return nil
}

// A lockable sidecar does not imply that its directory can publish a receipt.
// Mixed item-only batches checkpoint the unchanged snapshot before dispatch,
// because an earlier unkeyed row cannot be recovered through a later item's key.
func (a App) checkBatchStateWrite(st *model.State, dryRun bool, operation string, keys ...string) error {
	if err := a.checkStateWriteForReplay(st, operation, keys...); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	unkeyed, fresh := false, false
	for _, key := range keys {
		if key == "" {
			unkeyed = true
		} else if _, exists := st.Idempotency[key]; !exists {
			fresh = true
		}
	}
	if unkeyed && fresh {
		if err := a.checkpoint(*st); err != nil {
			return cliError{exit: 1, code: "state_save_failed", msg: "cannot checkpoint mixed batch; no rows dispatched", hint: err.Error()}
		}
	}
	return nil
}
