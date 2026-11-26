package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"protonmailcli/internal/model"
)

// File-backed requests retain only fingerprints in their retry payload.
// Inline-only requests preserve their existing representation for saved keys.
type draftCreateBatchPayloadItem struct {
	draftCreateItem
	BodyFileFingerprint string `json:"body_file_fingerprint,omitempty"`
	bodyError           error
	snapshot            *draftBodySnapshot
}

type draftBodySnapshot struct {
	offset      int64
	size        int64
	fingerprint string
	readError   error
}

type preparedDraftBatch struct {
	payload       []draftCreateBatchPayloadItem
	file          *os.File
	storedBytes   int64
	capture       bool
	removeOnClose bool
}

var createDraftSnapshotFileFn = createDraftSnapshotFile
var removeDraftSnapshotFileFn = os.Remove
var prepareDraftSnapshotRemovalFn = prepareDraftSnapshotRemoval

const unusedDraftBodyFingerprint = "unused_item_key_conflict"

func draftItemKeyConflict(st *model.State, key, commandKey string) error {
	if key != "" && key == commandKey {
		return cliError{exit: 6, code: "idempotency_conflict", msg: "item and command idempotency keys must be distinct"}
	}
	return receiptOperationConflict(st, key, "imap.draft.create-item")
}

func prepareDraftBatch(items []draftCreateItem, st *model.State, commandKey string) (*preparedDraftBatch, error) {
	record, replay := st.Idempotency[commandKey]
	batch := &preparedDraftBatch{payload: make([]draftCreateBatchPayloadItem, len(items)), capture: !replay}
	snapshots := make(map[string]*draftBodySnapshot)
	buffer := make([]byte, 32*1024)
	for i, it := range items {
		item := &batch.payload[i]
		item.draftCreateItem = it
		if len(it.To) == 0 {
			continue
		}
		// A completed batch must verify the same inputs it originally consumed.
		// Older receipts without skipped rows retain their original fingerprints.
		if it.BodyFile != "" && (record.SkippedDraftBodies[i] || (!replay && draftItemKeyConflict(st, it.IdempotencyKey, commandKey) != nil)) {
			item.BodyFileFingerprint = unusedDraftBodyFingerprint
			continue
		}
		if err := it.bodySourceError(); err != nil {
			item.bodyError = err
			item.BodyFileFingerprint = "conflicting_sources"
			continue
		}
		if it.BodyFile == "" {
			continue
		}
		var snapshot *draftBodySnapshot
		var err error
		if it.BodyFile == "-" {
			snapshot, err = batch.readSource(runtimeStdinReader, buffer)
		} else if cached, ok := snapshots[it.BodyFile]; ok {
			snapshot = cached
		} else {
			source, openErr := openBodyFileFn(it.BodyFile)
			if openErr != nil {
				item.bodyError = openErr
				item.BodyFileFingerprint = "unavailable"
				continue
			}
			info, statErr := source.Stat()
			snapshot, err = batch.readSource(source, buffer)
			_ = source.Close()
			if err == nil && statErr == nil && info.Mode().IsRegular() {
				snapshots[it.BodyFile] = snapshot
			}
		}
		if err != nil {
			return nil, errors.Join(err, batch.close())
		}
		item.snapshot = snapshot
		item.bodyError = snapshot.readError
		item.BodyFileFingerprint = snapshot.fingerprint
	}
	return batch, nil
}

// readSource uses one bounded buffer for the entire batch. Existing receipts
// need only a digest; new requests also spool bytes for later one-item dispatch.
func (b *preparedDraftBatch) readSource(source io.Reader, buffer []byte) (*draftBodySnapshot, error) {
	snapshot := &draftBodySnapshot{offset: b.storedBytes, fingerprint: "unavailable"}
	digest := sha256.New()
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			if b.capture {
				if b.file == nil {
					file, err := createDraftSnapshotFileFn("", "protonmailcli-draft-batch-*")
					if err != nil {
						return nil, fmt.Errorf("create private draft snapshot: %w", err)
					}
					b.file = file
					// Keep cleanup responsible for the empty file if unlinking fails.
					b.removeOnClose = true
					removeOnClose, err := prepareDraftSnapshotRemovalFn(file.Name())
					if err != nil {
						return nil, fmt.Errorf("unlink private draft snapshot %q before writing: %w", file.Name(), err)
					}
					b.removeOnClose = removeOnClose
				}
				written, err := b.file.Write(buffer[:n])
				if err != nil {
					return nil, fmt.Errorf("write draft snapshot: %w", err)
				}
				if written != n {
					return nil, fmt.Errorf("write draft snapshot: %w", io.ErrShortWrite)
				}
				b.storedBytes += int64(n)
			}
			snapshot.size += int64(n)
		}
		if readErr == io.EOF {
			snapshot.fingerprint = hex.EncodeToString(digest.Sum(nil))
			return snapshot, nil
		}
		if readErr != nil {
			snapshot.readError = readErr
			if b.capture && b.storedBytes > snapshot.offset {
				if err := b.file.Truncate(snapshot.offset); err != nil {
					return nil, fmt.Errorf("discard incomplete draft snapshot: %w", err)
				}
				if _, err := b.file.Seek(snapshot.offset, io.SeekStart); err != nil {
					return nil, fmt.Errorf("restore draft snapshot position: %w", err)
				}
				b.storedBytes = snapshot.offset
			}
			return snapshot, nil
		}
	}
}

func (b *preparedDraftBatch) readSnapshotBody(index int) (string, error) {
	item := b.payload[index]
	if item.snapshot == nil {
		return "", fmt.Errorf("draft body snapshot is missing")
	}
	if item.snapshot.size == 0 {
		return "", nil
	}
	if b.file == nil {
		return "", fmt.Errorf("draft body snapshot storage is missing")
	}
	data, err := io.ReadAll(io.NewSectionReader(b.file, item.snapshot.offset, item.snapshot.size))
	if err != nil {
		return "", err
	}
	if int64(len(data)) != item.snapshot.size {
		return "", io.ErrUnexpectedEOF
	}
	return string(data), nil
}

func (b *preparedDraftBatch) close() error {
	if b.file == nil {
		return nil
	}
	file := b.file
	b.file = nil
	var cleanupErr error
	if err := file.Close(); err != nil {
		cleanupErr = fmt.Errorf("close private draft snapshot %q: %w", file.Name(), err)
	}
	if b.removeOnClose {
		if err := removeDraftSnapshotFileFn(file.Name()); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove private draft snapshot %q: %w", file.Name(), err))
		}
	}
	return cleanupErr
}
