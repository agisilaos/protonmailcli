#!/usr/bin/env python3
"""Check that the docs agents steer by still describe this repository.

    python3 -B scripts/docs-reality-check.py            every tracked Markdown file
    python3 -B scripts/docs-reality-check.py --staged    only what the next commit holds

The shared docs contract already checks README headings and Markdown links. This
checks the claims those rules cannot see: a `backticked` path is as easy to
invent as a sentence, and nothing notices when the directory it names never
existed.

It reads every tracked Markdown file except the records, which may name what has
since gone (CHANGELOG.md, docs/adr/, docs/history/). From each it takes every
`backticked` span outside a fenced code block, and reports with file and line:

  - a repository path that names no file or directory. A path resolves from the
    repository root, or as the ending of any tracked file's path, so
    `app/responses.go` is found at `internal/app/responses.go`.
  - a script under scripts/ that is missing or not executable.
  - a `make <target>` that the Makefile does not define.
  - a `PMAIL_*` environment variable that no Go source reads.
  - AGENTS.md over 900 words, or with a line over 400 characters. Agents load it
    on every turn, so it pays for its length in every request.
  - a dated heading in a rules file. Rules are edited in place; what happened on
    a day belongs in its commit message or its issue.

Exit status is 0 when nothing is reported, 1 otherwise.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

# Records may name what has since been removed; they are not instructions.
RECORD_PREFIXES = ("docs/adr/", "docs/history/", "docs/perf-runs/")
RECORD_FILES = ("CHANGELOG.md",)

# A span is treated as a repository path when its first segment is one of these,
# so that `origin/master` and `imap:drafts:12` are left alone.
TOP_LEVEL = {"cmd", "docs", "internal", "scripts", "tests", ".github", ".githooks"}

# Only a path under one of those roots is checked. A bare filename is far more
# often an example the reader supplies (`drafts.json`) or a release artifact the
# build produces (`NOTES.md`), and guessing between the two reports noise.

# Paths the docs describe on purpose before they exist: `domain-modeling` creates
# them when a term or a decision is first resolved. Remove an entry once it
# exists; the check passes either way.
PLANNED_PATHS = {"docs/adr"}

# Executability is required only of things that are actually run, not of the
# data manifests that also live under scripts/.
EXECUTABLE_SUFFIXES = {".sh", ".py", ""}

# Characters that mean the span is a pattern, a placeholder or prose, not a path.
UNPATHLIKE = set(' \t<>*|"\'$()[]{}!?,;=')

AGENTS_WORD_LIMIT = 900
AGENTS_LINE_LIMIT = 400

CODE_FENCE = re.compile(r"^\s*(`{3,}|~{3,})(.*)$")
BACKTICKED = re.compile(r"`([^`\n]+)`")
HEADING = re.compile(r"^#{1,6}\s+(.*)$")
DATE_IN_HEADING = re.compile(
    r"\b(20\d{2}-\d{2}-\d{2}"
    r"|\d{1,2}\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+20\d{2}"
    r"|(January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},?\s+20\d{2})\b"
)
MAKE_TARGET = re.compile(r"^make\s+([a-z][a-z0-9-]*)")
ENV_VAR = re.compile(r"^(PMAIL_[A-Z0-9_]+)$")


def tracked_files(root: Path) -> list[str]:
    output = subprocess.check_output(["git", "-C", str(root), "ls-files", "-z"], text=True)
    return [name for name in output.split("\0") if name]


def staged_markdown(root: Path) -> list[str]:
    output = subprocess.check_output(
        ["git", "-C", str(root), "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"],
        text=True,
    )
    return [name for name in output.split("\0") if name.endswith(".md")]


def is_record(name: str) -> bool:
    return name in RECORD_FILES or name.startswith(RECORD_PREFIXES)


def visible_lines(text: str) -> list[tuple[int, str]]:
    """Lines outside fenced code blocks, numbered from 1."""
    result: list[tuple[int, str]] = []
    fence: tuple[str, int] | None = None
    for number, line in enumerate(text.splitlines(), 1):
        marker = CODE_FENCE.match(line)
        if fence is not None:
            if marker and marker.group(1)[0] == fence[0] and len(marker.group(1)) >= fence[1] and not marker.group(2).strip():
                fence = None
            continue
        if marker:
            fence = (marker.group(1)[0], len(marker.group(1)))
            continue
        result.append((number, line))
    return result


def path_candidate(span: str) -> str | None:
    """The repository path a span names, or None when it is not one."""
    span = span.strip().rstrip(".:,")
    if not span or UNPATHLIKE & set(span):
        return None
    if span.startswith(("~", "/", "-", "http")):
        return None
    cleaned = span.rstrip("/")
    if not cleaned:
        return None
    if "/" not in cleaned:
        return None
    if cleaned.split("/")[0] not in TOP_LEVEL:
        return None
    return cleaned


def make_targets(root: Path) -> set[str]:
    makefile = root / "Makefile"
    if not makefile.is_file():
        return set()
    found = set()
    for line in makefile.read_text().splitlines():
        match = re.match(r"^([a-z][a-z0-9-]*):", line)
        if match:
            found.add(match.group(1))
    return found


def environment_names(root: Path) -> set[str]:
    found = set()
    for name in tracked_files(root):
        if not name.endswith(".go"):
            continue
        found.update(re.findall(r"PMAIL_[A-Z0-9_]+", (root / name).read_text()))
    return found


def check_file(root: Path, name: str, index: dict[str, set[str]], targets: set[str],
               env: set[str]) -> list[str]:
    path = root / name
    try:
        text = path.read_text()
    except (OSError, UnicodeDecodeError) as error:
        return [f"{name}: could not read: {error}"]

    problems: list[str] = []
    lines = visible_lines(text)

    for number, line in lines:
        heading = HEADING.match(line)
        if heading and DATE_IN_HEADING.search(heading.group(1)):
            problems.append(
                f"{name}:{number}: dated heading in a rules file: {heading.group(1).strip()}"
            )
        for span in BACKTICKED.findall(line):
            target = MAKE_TARGET.match(span.strip())
            if target:
                if targets and target.group(1) not in targets:
                    problems.append(f"{name}:{number}: no such Make target: make {target.group(1)}")
                continue
            variable = ENV_VAR.match(span.strip())
            if variable:
                if variable.group(1) not in env:
                    problems.append(
                        f"{name}:{number}: no Go source reads {variable.group(1)}"
                    )
                continue
            candidate = path_candidate(span)
            if candidate is None or candidate in PLANNED_PATHS:
                continue
            resolved = root / candidate
            if resolved.exists():
                if (candidate.startswith("scripts/") and resolved.is_file()
                        and resolved.suffix in EXECUTABLE_SUFFIXES
                        and not resolved.stat().st_mode & 0o111):
                    problems.append(f"{name}:{number}: script is not executable: {candidate}")
                continue
            if candidate in index["suffixes"]:
                continue
            problems.append(f"{name}:{number}: path names no file or directory: {candidate}")

    if name == "AGENTS.md":
        words = len(text.split())
        if words > AGENTS_WORD_LIMIT:
            problems.append(f"{name}: {words} words, over the {AGENTS_WORD_LIMIT}-word limit")
        for number, line in enumerate(text.splitlines(), 1):
            if len(line) > AGENTS_LINE_LIMIT:
                problems.append(
                    f"{name}:{number}: line is {len(line)} characters, over {AGENTS_LINE_LIMIT}"
                )

    return problems


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--staged", action="store_true",
                        help="check only the Markdown files the next commit holds")
    arguments = parser.parse_args()

    root = Path(subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True).strip())

    everything = tracked_files(root)
    suffixes: set[str] = set()
    for name in everything:
        parts = name.split("/")
        for start in range(len(parts)):
            suffixes.add("/".join(parts[start:]))
    index = {"suffixes": suffixes}

    if arguments.staged:
        names = [name for name in staged_markdown(root) if not is_record(name)]
    else:
        names = [name for name in everything if name.endswith(".md") and not is_record(name)]

    targets = make_targets(root)
    env = environment_names(root)

    problems: list[str] = []
    for name in sorted(names):
        if not (root / name).is_file():
            continue
        problems.extend(check_file(root, name, index, targets, env))

    if problems:
        for problem in problems:
            print(problem, file=sys.stderr)
        print(f"\n{len(problems)} problem(s) in the docs.", file=sys.stderr)
        return 1

    print(f"docs describe the repository ({len(names)} file(s) checked)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
