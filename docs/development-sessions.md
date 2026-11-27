# Development sessions

Use one branch and worktree per implementation chat in a standard clone with
trusted Git hooks and a trusted `origin` that uses a standard Git transport.
Landing requires the resolved fetch and push URLs for `origin` to match.
The primary checkout is
the place to start or list sessions. Read-only investigations can use an
existing checkout.

## Start a chat

Give the chat a short task name, an issue or description of the expected
behavior, the scope, relevant evidence, and how to verify completion. State
whether the chat may implement, commit, and land. For example, copy and fill in
this prompt:

> Start a protonmailcli session named `<short-name>`. Implement `<outcome>`.
> The scope is `<files or behavior>`. Use `<issue, reproduction, or evidence>`.
> Verify `<observable result>`. Stop after `<implementation, commit, or landing>`.

1. Run `scripts/session list` to identify existing worktrees and check for
   overlapping ownership. Resume an existing checkout if it owns this work.
2. Run `scripts/session start <short-name>` and switch to the printed sibling
   checkout. It starts at the fetched tip of GitHub's default branch
   (currently `master`) on `fix/<short-name>`. Setup restores tracked files
   with checkout hooks disabled.
3. Make the scoped change and run focused checks as you work. Stage changed
   files by explicit path. Keep another session's changes in its checkout.

## Land and end

1. When authorized, commit the changes as a linear series and confirm the
   worktree is clean. Move any ignored local files, such as credentials or
   build artifacts, out of the checkout before landing. Use a regular Git
   index for the session checkout.
2. Run `scripts/session land --dry-run` to rehearse a landing, or
   `scripts/session land` to publish an authorized landing. Both rebase the
   local branch after checking the fetched commit against the resolved
   remote. Rebase runs in a disposable worktree, and installation refuses
   to overwrite ignored files or replace a newly advanced session branch.
   The session and disposable worktree need matching checkout settings,
   including filters, symlinks, and line endings. Use an absolute path
   for any configured attributes file.
   They use a fresh checkout
   with isolated Git configuration for a read-only Codex review and one
   run of `scripts/release-check.sh --ci`. A real
   landing fast-forwards the same remote default branch from the exact tip used
   during qualification. Landings take turns across worktrees of the same clone.
   The configured pre-push hook runs for a real landing and receives the
   session branch as the local ref.
   The session checks the default branch again inside the pre-push hook
   using the resolved destination and its configured transport settings,
   including protocol options. It records those settings before fetching.
   The push and its final lookup share an isolated transport configuration.
   The push honors configured signing and push options.
   Pinned HTTP connections require a destination that works without redirects.
   Git's ref update guards the branch tip, and a server-side default-branch
   switch can still happen after that last check.
3. If the rebase reports conflicts, the session branch keeps its original
   commits. From the clean session checkout in a shell with `umask 022`,
   replace `<base>` with the exact commit printed by the command and run:

   ```sh
   git rebase --merge --no-autostash --no-update-refs --no-fork-point \
     --no-rebase-merges --reapply-cherry-picks <base>
   ```

   Resolve the conflicts, stage the resolutions, and run
   `git rebase --continue`. After the rebase completes, record the printed base with
   `git update-ref refs/worktree/protonmailcli/session-base <base>`, then retry.
   If installation fails or is interrupted, inspect the session status before retrying.
   The command reports the recovery directory if a partial installation
   needs inspection.
   If review reports findings, address them and commit before
   retrying. If the gate fails or the remote advances, inspect the
   reported result and rerun from the clean worktree. If the remote rewound
   past the recorded base, start a fresh session and bring over only the
   commits you still intend to land.
4. Run `scripts/session end` when finished. It unregisters the worktree
   after confirming the checkout is clean and its commit is on the current
   remote default branch. The local branch is retained. The command
   prints the archive directory. Its `files/` subdirectory contains the
   checkout. Discard the whole archive once any processes using that
   checkout have finished. Move ignored local files, such as
   credentials, out of the checkout before ending.
   Once your local default branch includes the landing, you can remove
   the retained branch with `git branch --delete fix/<name>`.
   If cleanup is interrupted or Git refuses to unregister the worktree,
   the command restores the original path when possible and reports where
   the files are retained. An archive whose registration is already gone
   remains detached from the repository. If files returned to the original
   path before Git updated its registration, use `git worktree repair <path>`
   from the primary checkout.

## Partial checkout recovery

If `start` prints a checkout path and base after setup stops, fix the
underlying Git error and inspect the checkout before restoring files.
Record the printed base with
`git update-ref refs/worktree/protonmailcli/session-base <base>` from the
session checkout. In a still-empty checkout, finish with
`git restore --source=HEAD --staged --worktree -- .`.

Landing requires Git, Python 3.9 or newer, the project's macOS release toolchain and an
authenticated Codex CLI. Git 2.54 or newer supports config-based Git hooks.
Set `PROTONMAILCLI_CODEX` to select a different
installed Codex executable. Review output, the release check log and the
version-stamped binary are retained under
`~/Library/Logs/protonmailcli/sessions/`. The landing checks clear Proton Mail
variables and use the project's Make and Go settings.

Use `scripts/session land` for publication so the exact commits pass review
and qualification. Those checks are enforced by the session command. GitHub's
existing release-check workflow also runs after a push to `master` or `main`.
Run `make session-script-test` to exercise changes to the session helper
against disposable local Git remotes.

## Archived sessions

Abandoned session checkouts are removed with `git worktree remove`, but their
commits are kept under the `refs/archive/` namespace so nothing is lost to
garbage collection. Two prefixes are used:

- `refs/archive/worktree/<checkout-name>` — the HEAD of a removed session
  checkout. Uncommitted work found in the checkout was committed first as
  `archive(session): Capture uncommitted <checkout-name> work`.
- `refs/archive/branch/<branch-name>` — a deleted local session branch.

These refs stay out of `git branch` and `git log` by default. List and inspect
them with:

```bash
git for-each-ref refs/archive
```

To recover one into a working checkout:

```bash
git worktree add /tmp/<name> refs/archive/worktree/<name>
```

Delete an archive ref once its work is confirmed landed or unwanted:

```bash
git update-ref -d refs/archive/worktree/<name>
```

## Hooks

Run the installer once per clone:

```bash
python3 -B scripts/install-hooks.py
```

It writes `pre-commit` and `commit-msg` launchers into the clone's shared hooks
directory, so one run covers the main checkout and every session worktree,
present and future. Each launcher execs the tracked hook under `.githooks/` in
whichever worktree the commit is made from, and exits cleanly when that file is
absent, so a branch cut before the hooks existed still commits.

- `pre-commit` checks staged whitespace, runs `gofmt` over staged Go files and
  runs the docs-reality check over staged Markdown.
- `commit-msg` requires a blank line after the subject.

`core.hooksPath` is deliberately left unset. `scripts/session land` resolves the
repository's own pre-push hook through `git rev-parse --git-path` and rehearses
it against a disposable transport before the real push; repointing the hooks
directory would change what that resolves to. The installer refuses to run if
`core.hooksPath` has been set.

## Pruning sessions

`scripts/session list` shows every session worktree with its short HEAD, its
state, its last commit date and why it is in that state:

```bash
scripts/session list
```

A worktree is `landed` when its HEAD has reached origin's default branch as the
last fetch left it, `unlanded` when it carries commits that have not, `dirty`
when it holds uncommitted paths, and `missing` when its directory is gone.
Because the check is offline, a session that landed after the last fetch reads
as unlanded, which holds it back rather than removing it.

```bash
scripts/session prune           # propose
scripts/session prune --apply   # carry it out
```

`prune` removes only clean, landed worktrees. Before each removal it records
that HEAD under `refs/archive/worktree/<name>`, so the commits survive garbage
collection even though nothing else references them. A dirty or unlanded
worktree is reported and kept: removing it would drop the only ref holding its
commits, and whether that work matters is not a decision a cleanup command
should make. Review those by hand, as [Archived sessions](#archived-sessions)
describes.
