# IMAP workflow behavior

Message IDs include their mailbox: `imap:Archive:41` addresses UID 41 in
Archive. The last colon separates the UID, so `imap:Project:2026:41` also
round-trips. Bare numeric message IDs use INBOX. Draft commands resolve the
server's special-use Drafts mailbox, including localized mailbox names. A
draft ID qualified with another mailbox is rejected before reading, updating,
deleting, or sending an unrelated Drafts UID.
`mailbox resolve` rejects a generated mailbox ID shared by multiple mailboxes;
use an exact mailbox name to disambiguate.

Tag add/remove respects the mailbox in a message ID. With `--dry-run`, neither
operation issues a flag change. Search treats `--since-id N` as an inclusive
lower bound, even when N is larger than the server's highest UID. Search text
quotes and backslashes are escaped; control characters are rejected.

Draft creation and updates reject CR/LF in header inputs. Body text may contain
newlines. Update changes only supplied fields; explicit `--subject ''` and
`--body ''` clear those fields. Updates and sends preserve `In-Reply-To` and
`References` from the draft.

Draft updates append and confirm the replacement before deleting the original.
Deletion requires UIDPLUS or IMAP4rev2 and uses UID EXPUNGE for the selected draft. If an
append is uncertain, the original remains. If replacement creation succeeds
but deleting the original fails, the error includes the confirmed replacement
ID and the original ID. Inspect those drafts before retrying; another update
could create another replacement.

When the server definitively rejects APPEND, the SMTP fallback delivers only
to the account itself, retaining the intended recipients in the stored To
header. It reports a draft ID only after locating exactly one matching message
in the destination Drafts mailbox. A source INBOX UID is never a destination
ID. Failures after SMTP acceptance return an uncertain outcome requiring
inspection, rather than advertising a safe automatic retry.

A successful fetch with no matching draft returns `not_found` (exit 5).
Transport, server, and message-decoding failures return
`imap_draft_fetch_failed` (exit 4), so a temporarily unavailable draft is not
reported permanently absent.

## Batch retry keys

`draft create-many` and `message send-many` honor each item's
`idempotency_key`. Repeating an item with the same key and payload reuses its
recorded outcome; changing the payload with that key reports a conflict.
Keep keys distinct across command-level and item-level operations, and retain
the same state file between runs.

Draft items and keyed follow-ups persist intent before dispatch and preserve
completed or uncertain outcomes. Keyed single sends, send batches, and batch
items also persist intent before SMTP. An unavailable state store therefore
prevents dispatch, including on the first run. Successful sends checkpoint their
receipts; successful batch items do so before the next item is sent.

If a send is interrupted, SMTP completion is unconfirmed, or a receipt cannot be
saved, its durable pending key blocks replay with `imap_send_uncertain` and
`retryable: false`. The command stops and names the affected draft when known.
Retain the state and key and inspect delivery before retrying. A command-level
pending batch requires reconciling every item. Keyed follow-up recovery uses
`imap_draft_create_uncertain` because its outcome is a draft.

These local records cannot make SMTP acceptance and filesystem persistence an
atomic transaction; a pending record deliberately leaves that uncertainty for
manual reconciliation. It never authorizes another dispatch automatically.
Unkeyed sends cannot provide replay protection. Reads and dry-runs do not
initialize the state store or reserve keys.

Replaying a completed command-level send batch preserves its failure exit
status: 10 for mixed success and failure, 1 when every item failed. Pending
send recovery uses exit 4 and is checked before backend access. Failed command-level
batches are persisted even when no message was sent. Dry-run never records a
new send receipt.

All SMTP paths, including draft fallback, use the configured operation timeout
and explicit `bridge.tls_cert_file` trust file when provided.
