#!/usr/bin/env python3
"""Install the pre-commit and commit-msg launchers for this clone.

    python3 -B scripts/install-hooks.py

Hooks live in the common Git directory, which every worktree of this clone
shares, so one run covers the main checkout and every session worktree, present
and future.

`core.hooksPath` is deliberately left unset. `scripts/session land` resolves the
repository's own pre-push hook through `git rev-parse --git-path`, and rehearses
it against a disposable transport before the real push; repointing the hooks
directory would change what that resolves to. Installing files in the directory
Git already uses keeps the landing gate reading exactly what it read before.

Each launcher execs the tracked hook under .githooks/ in whichever worktree the
commit is made from, and exits cleanly when that file is absent, so a branch cut
before these hooks existed still commits.
"""

from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys

LAUNCHER = """#!/bin/sh
# Installed by scripts/install-hooks.py. Runs the tracked hook from the worktree
# the commit is made from; absent there, the commit proceeds unchecked.
set -eu
root=$(git rev-parse --show-toplevel)
hook="$root/.githooks/{name}"
[ -x "$hook" ] || exit 0
exec "$hook" "$@"
"""

NAMES = ("pre-commit", "commit-msg")


def git(*arguments: str) -> str:
    return subprocess.check_output(["git", *arguments], text=True).strip()


def main() -> int:
    root = Path(git("rev-parse", "--show-toplevel")).resolve()
    if root != Path(__file__).resolve().parent.parent:
        raise ValueError("run the installer that belongs to this checkout")

    configured = subprocess.run(
        ["git", "config", "--get", "core.hooksPath"], capture_output=True, text=True
    ).stdout.strip()
    if configured:
        raise ValueError(
            f"core.hooksPath is set to {configured!r}; this installer expects it unset so that "
            "the landing gate resolves the pre-push hook as it does today. Review that setting first."
        )

    for name in NAMES:
        tracked = root / ".githooks" / name
        if not tracked.is_file():
            raise ValueError(f"missing tracked hook: .githooks/{name}")
        if not tracked.stat().st_mode & 0o111:
            raise ValueError(f"tracked hook is not executable: .githooks/{name}")

    destination = Path(git("rev-parse", "--path-format=absolute", "--git-path", "hooks"))
    destination.mkdir(parents=True, exist_ok=True)

    for name in NAMES:
        launcher = destination / name
        expected = LAUNCHER.format(name=name)
        if launcher.exists() and launcher.read_text() != expected:
            raise ValueError(
                f"{launcher} already exists and is not this launcher; inspect it before replacing"
            )

    for name in NAMES:
        launcher = destination / name
        launcher.write_text(LAUNCHER.format(name=name))
        launcher.chmod(0o755)

    print(f"Installed {', '.join(NAMES)} in {destination}")
    print("Shared by every worktree of this clone. core.hooksPath left unset.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f"FAIL: {error}")
