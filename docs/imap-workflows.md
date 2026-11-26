# IMAP workflow behavior

Message IDs include their mailbox: `imap:Archive:41` addresses UID 41 in
Archive. The last colon separates the UID, so `imap:Project:2026:41` also
round-trips. Bare numeric message IDs use INBOX. Draft commands resolve the
server's special-use Drafts mailbox. A draft receipt includes that mailbox's
exact name. The literal name `Drafts` uses `imap-v2:Drafts:41` so it cannot be
confused with an older alias; other names keep the compatible form, for example
`imap:Entwuerfe:41`. All ID arguments and manifest fields accept this versioned
form. Pass returned IDs unchanged, including confirmation tokens. A first draft
operation fails before access if the receipt's exact mailbox no longer matches the
selected Drafts mailbox, even when the new mailbox contains the same UID.
IDs do not include IMAP UIDVALIDITY: obtain fresh IDs after deleting and
recreating a mailbox under the same name. When an older receipt uses
`imap:Drafts:41`, a draft command accepts the alias if it identifies a single
mailbox. If the server has both `Entwuerfe` marked as Drafts and a distinct
`Drafts` folder, draft commands reject both the legacy alias and a bare UID.
Inspect the UID in each candidate mailbox before choosing a draft. For a
profile named `work` with a config and state under `/mail`, the error includes
these POSIX shell commands.

```sh
protonmailcli --json --config '/mail/config.toml' --state '/mail/state.json' --profile 'work' message get --message-id 'imap-v2:Drafts:41'
protonmailcli --json --config '/mail/config.toml' --state '/mail/state.json' --profile 'work' message get --message-id 'imap:Entwuerfe:41'
```

The ambiguity error lists an exact read command for each candidate, including
multiple special-use Drafts mailboxes and case variants of `Drafts`. Each
command includes the config path, state path, and profile from the original
invocation, including resolved defaults. When the send supplied
`--smtp-password-file`, the inspection commands pass that file path to
`message get --password-file`. Run them while the same Bridge account
is active in that state file. If the effective password file in config or state,
or `bridge.tls_cert_file` in config, uses a relative path, each command runs
in a subshell in the original working directory. You can paste it from another
directory without changing your shell’s working directory. Each read selects
the named folder and fetches the requested UID directly. `draft list` only shows the selected Drafts
mailbox, so it cannot establish which folder an old receipt
referred to. After inspection, use an unambiguous exact ID in the selected
Drafts mailbox. If the intended message belongs elsewhere or its ID remains
ambiguous, resolve the server's Drafts mailbox configuration before retrying.
IDs qualified with an unrelated mailbox are rejected before a draft is accessed.
`mailbox resolve` rejects a generated mailbox ID shared by multiple mailboxes;
use an exact mailbox name to disambiguate.
Discovery counts every distinct special-use Drafts mailbox and every listed
case variant of `Drafts` when resolving a legacy ID. Without a special-use
role, a unique listed spelling such as `DRAFTS` supplies the exact folder
name. Multiple possible fallback spellings require resolving the server
configuration before creating drafts. Creation requires a listed destination
without a `\Noselect` flag. If every advertised Drafts-role mailbox is
unselectable, creation fails even if an ordinary folder is named `Drafts`;
name fallback applies only when no Drafts role is advertised.
An empty listing, a missing Drafts folder, or a
refused mailbox selection stops creation before SMTP. Only an explicit tagged
rejection of the APPEND command enables the fallback. The client waits through status
updates such as `EXISTS` for the command’s reply. IMAP commands recognize
`OK`, `NO`, and `BAD` completion statuses regardless of letter case, requiring
the exact command tag and status token. Fresh create, create-many and follow-up
previews open the resolved destination read-only with IMAP EXAMINE. An unavailable
destination produces a creation error for each fresh item. Keyed replay and pending-receipt checks precede this preview check.
Mailbox listing preserves the server's special-use `\Drafts` flag: a localized
Drafts mailbox receives `id: drafts` and `kind: system`, whether its LIST name is
an atom, a quoted string, or a length-prefixed literal. Repeated rows for the
same exact name contribute their flags to one mailbox in listing and lookup.
Different name spellings remain distinct. If the server sends
a mailbox row the client cannot read, listing and draft discovery report an
error. An exact server name takes precedence over a canonical ID. For example,
if the server lists an ordinary folder named `drafts` and a special-use folder
named `Brouillons`, `mailbox resolve --name drafts` selects the ordinary folder.
Use `mailbox list` to see the server names and `mailbox resolve --name Brouillons`
to select the localized folder. If a canonical ID matches multiple folders and
none has the exact name given, the error lists the candidate names.
Lookup preserves leading and trailing spaces in server names, including during
case-insensitive matching. Extra surrounding whitespace is accepted as a
convenience only when it cannot identify more than one mailbox. For example,
with `Project` and `Project `, `PROJECT ` resolves to `Project `; ` PROJECT  `
is ambiguous. `search messages --mailbox` uses the same lookup and preserves
the resolved server name in its selection, response, and message IDs. Search accepts uppercase and lowercase IMAP SEARCH responses. With no
`--mailbox`, message search uses INBOX. An explicitly empty mailbox returns
exit 2 before connecting.

Tag add/remove respects the mailbox in a message ID. With `--dry-run`, neither
operation issues a flag change. Search treats `--since-id N` as an inclusive
lower bound, even when N is larger than the server's highest UID. Search text
quotes and backslashes are escaped; control characters are rejected.

Draft creation and updates reject CR/LF in header inputs. Body text may contain
newlines. Update changes only supplied fields; explicit `--subject ''` and
`--body ''` clear those fields. Inspection, search, previews and source verification open mailboxes with IMAP
EXAMINE. Message reads use BODY.PEEK so inspection preserves both unread flags
and other clients’ ability to observe recent mail. Follow-up creation and replay
fetch the source UID directly from its mailbox.
Updates and sends preserve `In-Reply-To` and
`References` from the draft.

For `message send-many --idempotency-key`, a matching completed command receipt
replays the entire stored result before the CLI checks any item keys. The
command compares the manifest and returns the recorded exit status without
fetching the drafts again. Use item keys alone when each retry should check the
current content of its draft.

Completed single-send and item-send keys retain the exact source mailbox. A
retry verifies the full request and message content against that source, even
if another folder has since acquired the Drafts role, and returns the saved
receipt without sending again. Changed arguments, body, or thread headers still
conflict. Verification fetches the recorded UID directly from its exact mailbox;
it does not search for an identity that is already known.
Replay, including `--dry-run`, does not rewrite the receipt. Older
receipts naming a non-`Drafts` mailbox also identify their exact source; an older
`imap:Drafts` alias without exact-source metadata still requires unambiguous
mailbox discovery.
Saved source IDs and older raw item receipt IDs accept surrounding whitespace
consistently with command ID parsing. Mailbox-name whitespace inside an ID and
the original request payload used for conflict detection remain unchanged.

Follow-up keys created before mailbox whitespace was preserved can replay their
legacy payload hash when the full request still matches and mailbox listing
identifies one exact source under the old normalization. It trimmed surrounding
whitespace and preserved the case of ordinary mailbox names. Only INBOX is
case-insensitive. Legacy source discovery
runs before fetching, even when the supplied unpadded ID already matches the
saved hash. Missing or conflicting source names return `idempotency_conflict`
(exit 6); discovery or message-fetch failures remain transport errors (exit 4).
New follow-up receipts record the exact source
identity and never use this compatibility lookup. Neither compatibility replay
nor its dry-run rewrites state or creates another draft.

Draft updates append and confirm the replacement before deleting the original.
Deletion requires UIDPLUS or IMAP4rev2 and uses UID EXPUNGE for the selected draft. If an
append is uncertain, the original remains. If replacement creation succeeds
but deleting the original fails, the error includes the confirmed replacement
ID and the original ID. Inspect those drafts before retrying; another update
could create another replacement.

After a definite APPEND rejection, the client reopens the original destination
read-only. If it is still selectable, the SMTP fallback delivers only to the
account itself, retaining the intended recipients in the stored To header. The
subsequent MOVE targets that same exact mailbox even if the Drafts role changes. It reports a draft ID only after locating exactly one matching message
in the destination Drafts mailbox. A source INBOX UID is never a destination
ID. An explicit final SMTP 4xx or 5xx reply reports that the server rejected
DATA. If the final acknowledgement is lost or unreadable, the server may have
accepted the fallback copy. This returns `imap_draft_create_uncertain`, as
does a failure to confirm the subsequent move. Inspect INBOX and Drafts after an
uncertain result or an interrupted creation, since the copy can remain in INBOX.
If APPEND or MOVE completion, or the destination identity, is unconfirmed, the error and
saved recovery hint also name the exact original destination. Inspect that
mailbox even if a different mailbox now advertises the Drafts role; replaying a
saved failure retains the original name and does not create another copy.
New pending draft keys record the exact destination before dispatch. A retry reports
that mailbox even after a Drafts role change or a missing final receipt. Older
pending keys have only the general INBOX and Drafts guidance.
Keep the state and keys until you reconcile every item. A fresh key is appropriate
only after confirming absence from those mailboxes.

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
`draft create-many` rejects a command key already owned by another operation
before reading the manifest or any body input, including stdin. A row whose item
key belongs to another operation, or equals the command key, leaves its body
file or stdin unread. The command receipt remembers which rows skipped their
bodies. On replay the next eligible row receives the same stdin bytes.

A command-level `draft create-many` key covers the manifest and the contents of
readable `body_file` inputs on rows with recipients and a single body source. Rows
without recipients skip their irrelevant bodies and retain the per-item
validation error. Repeated references to the same literal regular-file path share
one body snapshot and fingerprint per invocation. Distinct path spellings keep
separate snapshots. The special `-` input consumes stdin separately for each row.
Conflicting body sources remain per-item validation errors.
Snapshot eligibility comes from the opened source's metadata, so resolving a
path to a stream never makes later rows reuse its first read merely because a
separate pathname check described a regular file.
Body files are fingerprinted with a bounded read buffer. A completed receipt
needs only these fingerprints and does not create temporary body storage or
contact IMAP. New keyed requests, including previews, capture the same bytes in
one private temporary file and load one body at a time for dispatch. Preparation
therefore does not hold the entire batch's file contents in memory. Temporary
disk space grows with the captured bytes, with one stored copy for repeated
references to each regular-file path.

If a receipt write fails during `draft create-many`, `data.results` and the
counts cover the processed rows. A reservation failure (`state_save_failed`,
exit 1) identifies the zero-based row that was never dispatched. A previously
reserved command key remains pending if the item reservation fails.

If the draft was created before its item receipt failed to save, that row
reports `ok: true`, its exact `draftId`, and its `uid`. A failed or uncertain
creation retains its original row error. The command returns
`imap_draft_create_uncertain` (exit 4) if the receipt checkpoint fails after
dispatch. A failure before publication leaves the durable receipt pending; a
failure syncing the directory after publication can leave the completed receipt
visible. The error identifies that publication and retains the checkpoint cause.
A failed final receipt after dispatch returns the full batch results.
Keep confirmed draft IDs and avoid rerunning the entire manifest. Reconcile
uncertain rows in INBOX and Drafts before creating any missing drafts.

When a batch only recovers saved item outcomes, a failed command-key checkpoint
returns `state_save_failed` (exit 1) with those outcomes. A post-publication sync
failure reports that the batch receipt is visible but not confirmed durable;
keep the original command key and manifest, since a matching retry can replay it
and changed input can conflict. A write failure before publication reports that
the new command key was not recorded.

On Unix the temporary file has permissions `0600`, and its name must be removed
before any draft contents are written; a failed unlink stops preparation with
`batch_snapshot_failed`. On Windows the file is created with a protected ACL
granting access only to the current user, including in a shared temporary
directory. The filesystem must preserve and enforce ACLs; otherwise preparation
fails before writing body bytes. Windows retains the private name until the file
is closed and then removes it on normal and error returns. Abrupt termination on
Windows can leave it in the temporary directory with the same private ACL. A failed close or deferred
removal prints a cleanup warning to stderr with the path for inspection and
removal. This warning preserves the original command result, exit status, and
saved receipts, including partial outcomes; it is not a reason to dispatch again.

Failure to create or write the temporary snapshot returns `batch_snapshot_failed`
(exit 1) before reservation or dispatch. A readback failure stops the batch at
that row with the same code. The error data lists preceding row results and
confirmed draft IDs. If no fresh draft was attempted, restore temporary storage
and retry with the same key. After a fresh attempt the batch key remains pending.
Keep the state and inspect the preceding rows in INBOX and Drafts before retrying
missing items. Completed item receipts remain available. A batch containing only
validation errors or item replays still saves its final result under the batch key.
Batches without a command-level key read each body immediately before its item.
Changing a file while reusing the batch key returns `idempotency_conflict`
instead of replaying a receipt for old content.
Conflicting body sources and unavailable files have distinct retry identities.
File read failures remain per-item errors and have a distinct retry payload,
so an unavailable file cannot replay a legacy success receipt. A pending single draft reports its recovery record before opening the body file or reading stdin. A pending batch
returns the recovery warning before the CLI reads the manifest or its body files. Keys recorded
by older versions for file-backed batches may conflict; inspect the existing
drafts before choosing a new key. Inline-only batch keys retain their prior
payload representation.

For corrected local-input rows, use item-level keys alone and omit command-level
`--idempotency-key`. A command-level key replays the whole batch result, including
partial failure, when the manifest and body contents still match. It does not resume
unfinished rows. Reconcile dispatched, pending, or uncertain items before
changing keys or retrying them.

Draft items and keyed follow-ups persist intent before dispatch and preserve
completed or uncertain outcomes. Keyed single sends, send batches, and batch
items also persist intent before SMTP. An unavailable state store therefore
prevents dispatch, including on the first run. Successful sends checkpoint their
receipts; successful batch items do so before the next item is sent.

If the final SMTP DATA acknowledgement is lost, `message send` and
`message send-many` return `imap_send_uncertain` (exit 4, category `uncertain`,
`retryable: false`) even without an idempotency key. The error identifies the
affected draft by its resolved mailbox-qualified ID, even when the request used
a bare UID or a legacy Drafts alias. Keep that exact ID for reconciliation if the
Drafts role later moves. A batch stops before sending later rows. Every batch,
including batches mixing keyed and unkeyed items, returns processed rows in
`data.results`, including confirmed successes and
the uncertain row with its zero-based `index` and `draftId`. `data.count` counts
those processed rows; later manifest rows are omitted and were never attempted.
Inspect delivery before deciding whether another send is needed; unkeyed
commands have no durable replay protection. An explicit SMTP rejection still reports `send_failed` for an
unkeyed attempt; a definitive temporary failure retains its retryable classification.
The DATA challenge and final reply may contain just the status code, with or
without text or a trailing space, including the last line of a multiline reply.
A bare `354` permits sending the body; a rejection before that point remains a
pre-delivery failure. A bare `250`
confirms acceptance; bare `451` or `550` is a definite rejection. Malformed,
inconsistent, or incomplete replies remain uncertain. Every reply line must end
with CRLF: an EOF fragment or a bare LF cannot confirm acceptance or rejection.
An incomplete challenge stops before the body is sent. This follows
[RFC 5321 section 4.2](https://www.rfc-editor.org/rfc/rfc5321.html#section-4.2).

If an item intent checkpoint fails in `message send-many`, the command returns
`state_save_failed` with earlier processed outcomes in `data.results`. The error
names the zero-based index and exact draft ID of the current row and says it was
not dispatched. That row and later rows are omitted from the returned counts.
Do not rerun the whole manifest: preserve the returned outcomes and reconcile
already accepted sends before retrying only undispatched rows.
If SMTP accepted the current row but its receipt checkpoint fails, that row is
included as `ok: true`, with its exact ID and `sentAt`; the uncertainty error
explicitly identifies the accepted row whose receipt was not saved. Its durable
pending key still blocks automatic replay, and later rows were not attempted.
A failed final command-level receipt likewise returns all processed outcomes and
identifies the batch receipt failure. Returned success counts describe confirmed
SMTP acceptance, even when the corresponding receipt could not be persisted.
Other errors that stop the item loop, including an unreadable cached outcome,
also retain earlier processed results while keeping their original error code.

If a send is interrupted, SMTP completion is unconfirmed, or a receipt cannot be
saved, its durable pending key blocks replay with `imap_send_uncertain` and
`retryable: false`. The command stops and names the affected draft when known.
Single-send and item-level pending receipts save the exact resolved draft ID
before SMTP dispatch. After a process exit, recovery reports that saved ID
without fetching the message or following a newly assigned Drafts role. Older
pending receipts without a usable source ID retain the generic recovery warning;
the CLI does not guess their mailbox.
If the CLI can read a pending key but lacks permission to create the state lock,
it still reports the recovery warning and stops before contacting Bridge.
A fresh command-level batch key does not hide pending item keys: the command
checks its own recovery record first, then the manifest's item records. Without
a saved command receipt, pending items stop the batch before body files or stdin
are consumed or the state file is rewritten, even when the directory is writable.
This also applies during a dry-run. A matching completed command receipt
continues to take precedence over item-level recovery.
When an older terminal draft-uncertainty receipt is replayed, recovery output
includes the current instruction to inspect both INBOX and Drafts. Other stored
details and the receipt file are preserved. Cached batch rows carry that guidance
in their error text.
Matching terminal receipts whose payloads can be verified locally also remain
available from a read-only store, including successful draft IDs, completed
uncertain failures, and partial or failed command-batch outcomes with their
original exit status. An item-keyed draft batch resolves local validation and
compatible receipts before opening a Bridge connection, even when its writer
lock is available. A missing body file remains a per-item validation error beside
recovered IDs. If a fresh item needs an unavailable writer, the command returns
the state error. Changed payloads still conflict.

A new command-level key must be saved even when all item outcomes are already
known. If it cannot be recorded, the command returns `state_save_failed` (exit 1)
with the recovered item results in `data` and says the key was not recorded.
Restore write access and retry the same manifest to bind that command key;
item-only replay does not need a new command receipt. Unkeyed remote draft
creation, draft edits/deletion, and tag changes return their confirmed results
without requesting a state writer or saving unchanged local state. Keyed fresh
operations still persist their recovery intent before any remote mutation.

Locally verified recovery neither writes state nor contacts Bridge. Completed
single-send, follow-up and item-send receipts may read IMAP to verify the
current source message even when
local state is read-only. They return the saved outcome only after the full
payload matches; changed payloads conflict, and no SMTP send or draft creation
is repeated. A failed writer lock still blocks fresh keys before any Bridge
connection.
With no command key, a draft or send batch can replay completed items alongside
fresh unkeyed rows. If any row has an unrecorded item key and state is
read-only, the batch stops before dispatching an unkeyed row. Draft creation
returns any preceding cached results and local validation errors. It checks
the manifest keys without opening later body files or consuming later stdin
bodies. A key owned by a different operation returns
`idempotency_conflict` from the readable state, including per-item conflicts.
Neither operation requires a new receipt for the unkeyed rows.
Retain the state and key and inspect delivery before retrying. A command-level
pending batch requires reconciling every item. The CLI reports a pending send
batch before reading its manifest, even if that file has changed or disappeared. Keyed follow-up recovery uses
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
IMAP connections use the same trust file and verify the server certificate and
configured hostname before login, including draft reads performed by send
previews. Loopback connections require valid certificate trust too; see
[Bridge transport behavior](config-and-smtp-fixes.md) for PEM configuration.


If a completed send receipt is published but its directory sync fails, the CLI
reports the publication and retains the original key. A matching retry can replay
the visible receipt. A draft batch that fails mailbox discovery before dispatch
reports a failed state write if it also cannot save its final result.
