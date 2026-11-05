#!/usr/bin/env python3
"""Exercise the session workflow with local remotes and controlled gates."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
REVIEWER = """#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess
import sys

assert not any(key.startswith("PMAIL_") for key in os.environ)
assert sys.argv[sys.argv.index("--sandbox") + 1] == "read-only"
assert Path.cwd() != Path(os.environ["SESSION_TEST_WORKTREE"])
assert not subprocess.check_output(["git", "remote"])
for revision in ("HEAD", "HEAD^"):
    sha = subprocess.check_output(["git", "rev-parse", revision], text=True).strip()
    assert sha in sys.argv[-1]
path = Path(sys.argv[sys.argv.index("--output-last-message") + 1])
path.write_text(os.environ.get("SESSION_TEST_REVIEW", '{"findings": []}'))
"""
RELEASE_CHECK = """#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess
import sys

assert sys.argv[1:] == ["--ci"]
assert not any(key.startswith("PMAIL_") for key in os.environ)
assert not {"GNUMAKEFLAGS", "MAKEFILES", "MAKEFLAGS", "MAKELEVEL", "MAKEOVERRIDES", "MFLAGS"} & os.environ.keys()
assert Path.cwd() != Path(os.environ["SESSION_TEST_WORKTREE"])
assert not subprocess.check_output(["git", "remote"])
assert not subprocess.check_output(["git", "status", "--porcelain"])
out = Path(os.environ["PROTONMAILCLI_RELEASE_CHECK_DIR"])
assert not out.is_relative_to(Path.cwd())
out.mkdir()
(out / "protonmailcli").touch()
if os.environ.get("SESSION_TEST_MOVE_REMOTE"):
    subprocess.run(
        ["git", "-C", os.environ["SESSION_TEST_PRIMARY"], "push", os.environ["SESSION_TEST_REMOTE"],
         "refs/heads/advance:refs/heads/master"],
        check=True,
    )
if os.environ.get("SESSION_TEST_SWAP_PUSHURL"):
    subprocess.run(
        ["git", "-C", os.environ["SESSION_TEST_PRIMARY"], "remote", "set-url", "--push", "origin",
         os.environ["SESSION_TEST_SWAP_PUSHURL"]],
        check=True,
    )
sys.exit(int(os.environ.get("SESSION_TEST_GATE_EXIT", "0")))
"""


class SessionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="proton-session-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.repo = self.root / "repo"
        self.remote = self.root / "remote.git"
        self.worktree = self.root / "repo-mailbox-preview"
        self.evidence = self.root / "Library/Logs/protonmailcli/sessions"
        reviewer = self.root / "codex"
        reviewer.write_text(REVIEWER)
        reviewer.chmod(0o755)

        self.env = {
            key: value for key, value in os.environ.items()
            if not key.startswith(("GIT_", "PMAIL_", "SESSION_TEST_"))
        }
        self.env.update(
            HOME=str(self.root), GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
            GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
            GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
            PROTONMAILCLI_CODEX=str(reviewer), PMAIL_E2E_BRIDGE="1",
            PMAIL_E2E_REAL_SEND="1", PMAIL_SMTP_PASSWORD="fixture",
            SESSION_TEST_REMOTE=str(self.remote), SESSION_TEST_WORKTREE=str(self.worktree),
            SESSION_TEST_PRIMARY=str(self.repo),
            GNUMAKEFLAGS="-n", MAKEFILES="fixture.mk", MAKEFLAGS="-n", MAKELEVEL="1",
            MAKEOVERRIDES="fixture=value", MFLAGS="-n",
        )
        self.git(self.root, "init", "--quiet", "--bare", "--initial-branch=master", self.remote)
        self.git(self.root, "init", "--quiet", "--initial-branch=master", self.repo)
        scripts = self.repo / "scripts"
        scripts.mkdir()
        shutil.copy2(ROOT / "scripts/session", scripts / "session")
        gate = scripts / "release-check.sh"
        gate.write_text(RELEASE_CHECK)
        gate.chmod(0o755)
        (self.repo / ".gitignore").write_text("*.pass\n")
        self.git(self.repo, "add", "scripts", ".gitignore")
        self.git(self.repo, "commit", "--quiet", "-m", "Initial")
        self.git(self.repo, "remote", "add", "origin", self.remote)
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.base = self.git(self.repo, "rev-parse", "HEAD")
        self.session(self.repo, "start", "mailbox-preview")

    def git(self, cwd, *args):
        result = subprocess.run(
            ["git", *args], cwd=cwd, env=self.env, text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def session(self, cwd, *args, status=0, **env):
        result = subprocess.run(
            ["scripts/session", *args], cwd=cwd, env={**self.env, **env},
            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        self.assertEqual(result.returncode, status, result.stdout)
        return result.stdout

    def change(self, checkout, name):
        (checkout / name).write_text(name)
        self.git(checkout, "add", name)
        self.git(checkout, "commit", "--quiet", "-m", f"Add {name}")
        return self.git(checkout, "rev-parse", "HEAD")

    def remote_tip(self):
        return self.git(self.remote, "rev-parse", "refs/heads/master")

    def git_wrapper(self, extra_code):
        wrapper = self.root / "git"
        wrapper.write_text("""#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess
import sys
real_git = os.environ["SESSION_TEST_REAL_GIT"]
""" + extra_code + """
os.execv(real_git, [real_git, *sys.argv[1:]])
""")
        wrapper.chmod(0o755)
        return {
            "PATH": os.pathsep.join([str(self.root), self.env["PATH"]]),
            "SESSION_TEST_REAL_GIT": shutil.which("git"),
        }

    def test_start_rebase_dry_run_land_and_end(self):
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), self.base)
        self.assertIn(str(self.worktree), self.session(self.repo, "list"))
        old_head = self.change(self.worktree, "session-change")
        new_base = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")

        self.assertIn("Dry run passed", self.session(self.worktree, "land", "--dry-run"))
        rebased = self.git(self.worktree, "rev-parse", "HEAD")
        self.assertNotEqual(rebased, old_head)
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), new_base)
        self.assertEqual(self.remote_tip(), new_base)
        self.assertEqual(len(list(self.evidence.glob("*/build/protonmailcli"))), 1)
        self.assertTrue((self.worktree / "remote-change").is_file())

        self.session(self.worktree, "land")
        self.assertEqual(self.remote_tip(), rebased)
        self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/mailbox-preview"), "")

    def test_dirty_and_unlanded_sessions_are_preserved(self):
        head = self.change(self.worktree, "unlanded")
        dirty = self.worktree / "in-progress"
        dirty.write_text("preserve")
        for command in ("land", "end"):
            self.assertIn("commit the session", self.session(self.worktree, command, status=1))
        self.assertEqual(dirty.read_text(), "preserve")
        dirty.unlink()
        self.assertIn("not on origin/master", self.session(self.worktree, "end", status=1))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), head)
        self.assertEqual(self.remote_tip(), self.base)

    def test_land_and_end_preserve_ignored_credentials(self):
        credential = self.worktree / "fixture.pass"
        credential.write_text("fixture")
        for command in ("land", "end"):
            self.assertIn("move ignored files", self.session(self.worktree, command, status=1))
        self.assertEqual(credential.read_text(), "fixture")

    def test_unmanaged_branch_cannot_land(self):
        self.git(self.worktree, "switch", "--quiet", "-c", "fix/other/work")
        self.assertIn("checkout made by scripts/session start", self.session(
            self.worktree, "land", status=1,
        ))
        self.assertEqual(self.remote_tip(), self.base)

    def test_end_refuses_a_matching_worktree_created_manually(self):
        manual = self.root / "repo-manual"
        self.git(self.repo, "worktree", "add", "-b", "fix/manual", manual, self.base)
        self.assertIn("no session record", self.session(manual, "end", status=1))
        self.assertTrue(manual.is_dir())

    def test_end_preserves_ignored_files_added_during_fetch(self):
        env = self.git_wrapper("""
if sys.argv[1:2] == ["-C"] and sys.argv[3:4] == ["ls-remote"]:
    Path(os.environ["SESSION_TEST_WORKTREE"], "fixture.pass").write_text("fixture")
""")
        self.assertIn("move ignored files", self.session(self.worktree, "end", status=1, **env))
        self.assertEqual((self.worktree / "fixture.pass").read_text(), "fixture")

    def test_hidden_tracked_edits_are_preserved(self):
        path = self.worktree / ".gitignore"
        self.git(self.worktree, "update-index", "--assume-unchanged", ".gitignore")
        path.write_text("hidden edit")
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")
        for command in ("land", "end"):
            self.assertIn("normal index tracking", self.session(self.worktree, command, status=1))
        self.assertEqual(path.read_text(), "hidden edit")

    def test_review_findings_and_invalid_verdict_stop_before_gate(self):
        self.change(self.worktree, "review-me")
        for review, diagnostic in (
            (json.dumps({"findings": ["mail.go:42: unsafe send"]}), "reported 1 finding"),
            ("{}", "no valid verdict"),
        ):
            self.assertIn(diagnostic, self.session(
                self.worktree, "land", status=1, SESSION_TEST_REVIEW=review,
            ))
        self.assertEqual(len(list(self.evidence.glob("*/review.log"))), 2)
        self.assertEqual(list(self.evidence.glob("*/release-check.log")), [])
        self.assertEqual(self.remote_tip(), self.base)

    def test_failed_release_check_keeps_remote_unchanged(self):
        self.change(self.worktree, "gate-failure")
        self.assertIn("exited 17", self.session(
            self.worktree, "land", status=1, SESSION_TEST_GATE_EXIT="17",
        ))
        self.assertEqual(self.remote_tip(), self.base)

    def test_remote_advance_during_qualification_blocks_push(self):
        self.change(self.worktree, "session-change")
        self.git(self.repo, "switch", "--quiet", "-c", "advance")
        advance = self.change(self.repo, "remote-change")
        self.assertIn("changed during qualification", self.session(
            self.worktree, "land", status=1, SESSION_TEST_MOVE_REMOTE="1",
        ))
        self.assertEqual(self.remote_tip(), advance)

    def test_landing_pins_the_original_remote_when_push_url_changes(self):
        head = self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        self.session(self.worktree, "land", SESSION_TEST_SWAP_PUSHURL=str(fork))
        self.assertEqual(self.remote_tip(), head)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_rewind_after_an_attempt_preserves_the_remote_rewrite(self):
        self.change(self.worktree, "session-change")
        self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.session(self.worktree, "land", status=1, SESSION_TEST_GATE_EXIT="17")
        self.git(self.remote, "update-ref", "refs/heads/master", self.base)
        self.assertIn("no longer descends", self.session(self.worktree, "land", status=1))
        self.assertEqual(self.remote_tip(), self.base)

    def test_rebase_reapplies_a_patch_previously_reverted_on_the_remote(self):
        self.change(self.worktree, "session-change")
        (self.repo / "session-change").write_text("session-change")
        self.git(self.repo, "add", "session-change")
        self.git(self.repo, "commit", "--quiet", "-m", "Apply the same patch upstream")
        self.git(self.repo, "revert", "--no-edit", "HEAD")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.session(self.worktree, "land")
        self.assertEqual(self.git(self.remote, "show", "refs/heads/master:session-change"), "session-change")

    def test_merge_commits_are_preserved_on_refusal(self):
        self.change(self.worktree, "session-change")
        self.git(self.repo, "switch", "--quiet", "-c", "side")
        self.change(self.repo, "side-change")
        self.git(self.worktree, "merge", "--quiet", "--no-ff", "-m", "Merge side", "side")
        merge = self.git(self.worktree, "rev-parse", "HEAD")
        self.assertIn("contains merge commits", self.session(self.worktree, "land", status=1))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), merge)
        self.assertEqual(self.remote_tip(), self.base)

    def test_lease_prevents_recreating_a_branch_deleted_just_before_push(self):
        self.change(self.worktree, "session-change")
        env = self.git_wrapper("""
if sys.argv[1:2] == ["-C"] and sys.argv[3:4] == ["push"]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_REMOTE"], "update-ref", "-d", "refs/heads/master"],
        check=True,
    )
""")
        self.assertIn("stale info", self.session(self.worktree, "land", status=1, **env))
        self.assertEqual(self.git(self.remote, "for-each-ref", "refs/heads/master"), "")

    def test_smoke_binary_is_built_outside_the_checkout(self):
        script = self.worktree / "scripts/smoke-agent.sh"
        shutil.copy2(ROOT / "scripts/smoke-agent.sh", script)
        gitless_bin = self.root / "bin"
        gitless_bin.mkdir()
        go = gitless_bin / "go"
        go.write_text("""#!/bin/sh
test "$1" = build && test "$2" = -o || exit 22
printf '%s\\n' "$3" > "$SESSION_TEST_BINARY_PATH"
exit 23
""")
        go.chmod(0o755)
        recorded_path = self.root / "smoke-path"
        result = subprocess.run(
            [script], cwd=self.worktree, env={
                **self.env, "PATH": os.pathsep.join([str(gitless_bin), self.env["PATH"]]),
                "SESSION_TEST_BINARY_PATH": str(recorded_path),
            }, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 23, result.stderr)
        binary = Path(recorded_path.read_text().strip()).resolve()
        self.assertFalse(binary.is_relative_to(self.worktree))
        self.assertFalse(binary.parent.exists())

    def test_start_uses_remote_default_without_switching_primary_checkout(self):
        self.git(self.repo, "switch", "--quiet", "-c", "main")
        alternate = self.change(self.repo, "main-change")
        self.git(self.repo, "push", "--quiet", "origin", "main")
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/main")
        self.git(self.remote, "update-ref", "refs/heads/feature/HEAD", self.base)
        self.git(self.repo, "switch", "--quiet", "master")
        self.session(self.repo, "start", "from-main")
        self.assertEqual(self.git(self.root / "repo-from-main", "rev-parse", "HEAD"), alternate)
        self.assertEqual(self.git(self.repo, "branch", "--show-current"), "master")


if __name__ == "__main__":
    unittest.main()
