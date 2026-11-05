# Development sessions

Use one branch and worktree per implementation chat. The primary checkout is
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
   (currently `master`) on `fix/<short-name>`.
3. Make the scoped change and run focused checks as you work. Stage changed
   files by explicit path. Keep another session's changes in its checkout.

## Land and end

1. When authorized, commit the changes as a linear series and confirm the
   worktree is clean. Move any ignored local files, such as credentials or
   build artifacts, out of the checkout before landing.
2. Run `scripts/session land --dry-run` to rehearse a landing, or
   `scripts/session land` to publish an authorized landing. Both rebase the
   local branch, then use a fresh checkout of the exact commits for a
   read-only Codex review and one run of `scripts/release-check.sh --ci`. A real
   landing fast-forwards the remote default branch from the exact tip used
   during qualification. Landings take turns across worktrees of the same clone.
3. If the rebase stops, resolve the conflict or abort it, then rerun the
   command. If review reports findings, address them and commit before
   retrying. If the gate fails or the remote advances, inspect the
   reported result and rerun from the clean worktree. If the remote rewound
   past the recorded base, start a fresh session and bring over only the
   commits you still intend to land.
4. Run `scripts/session end` when finished. It removes the worktree and its
   branch only if the checkout is clean and its commit is on the current
   remote default branch. Move any ignored local files you want to keep,
   such as credential files, out of the checkout before ending.

Landing requires Git, Python 3, the project's macOS release toolchain and an
authenticated Codex CLI. Set `PROTONMAILCLI_CODEX` to select a different
installed Codex executable. Review output, the release check log and the
version-stamped binary are retained under
`~/Library/Logs/protonmailcli/sessions/`. The landing checks run with the
Proton Mail environment variables cleared.

Use `scripts/session land` for publication so the exact commits pass review
and qualification. Those checks are enforced by the session command. GitHub's
existing release-check workflow also runs after a push to the default branch.
Run `make session-script-test` to exercise changes to the session helper
against disposable local Git remotes.
