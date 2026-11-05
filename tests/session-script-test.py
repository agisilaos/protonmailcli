#!/usr/bin/env python3
"""Exercise the session workflow with local remotes and controlled gates."""

import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
TRANSPORT = runpy.run_path(str(ROOT / "scripts/session_transport.py"))
SUPPORTS_CONFIG_HOOKS = TRANSPORT["supports_config_hooks"](
    subprocess.check_output(["git", "version"], text=True),
)
REVIEWER = """#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess
import sys

assert not any(key.startswith("PMAIL_") for key in os.environ)
assert not {"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_COUNT", "GIT_ATTR_SOURCE"} & os.environ.keys()
assert sys.argv[sys.argv.index("--sandbox") + 1] == "read-only"
assert Path.cwd() != Path(os.environ["SESSION_TEST_WORKTREE"])
assert not subprocess.check_output(["git", "remote"])
if os.environ.get("SESSION_TEST_EXPECT_CONDITIONAL_HOOK"):
    events = subprocess.run(
        ["git", "config", "--get-all", "hook.fixture-reference.event"],
        text=True, stdout=subprocess.PIPE,
    )
    assert events.returncode == 1 and not events.stdout
    assert subprocess.check_output(
        ["git", "config", "--get", "core.attributesFile"], text=True,
    ).strip() == os.devnull
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
assert not {"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_COUNT", "GIT_ATTR_SOURCE"} & os.environ.keys()
assert not {"GNUMAKEFLAGS", "MAKEFILES", "MAKEFLAGS", "MAKELEVEL", "MAKEOVERRIDES", "MFLAGS"} & os.environ.keys()
assert not {"BASH_ENV", "BASHOPTS", "CDPATH", "ENV", "SHELLOPTS", "GOFLAGS"} & os.environ.keys()
assert os.environ["GOENV"] == "off"
assert os.environ["GOWORK"] == "off"
assert os.environ["GIT_CONFIG_GLOBAL"] == os.devnull
assert os.environ["GIT_ATTR_NOSYSTEM"] == "1"
assert Path.cwd() != Path(os.environ["SESSION_TEST_WORKTREE"])
assert not subprocess.check_output(["git", "remote"])
assert not subprocess.check_output(["git", "status", "--porcelain"])
out = Path(os.environ["PROTONMAILCLI_RELEASE_CHECK_DIR"])
assert not out.is_relative_to(Path.cwd())
out.mkdir()
if not os.environ.get("SESSION_TEST_SKIP_BUILD"):
    binary = out / "protonmailcli"
    binary.touch()
    binary.chmod(0o755)
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
if os.environ.get("SESSION_TEST_REWRITE_PUSHURL"):
    fork = os.environ["SESSION_TEST_REWRITE_PUSHURL"]
    subprocess.run(
        ["git", "-C", os.environ["SESSION_TEST_PRIMARY"], "config",
         f"url.{fork}.pushInsteadOf", os.environ["SESSION_TEST_REMOTE"]],
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
            BASH_ENV="fixture.sh", ENV="fixture.sh", GOENV="fixture.goenv", GOFLAGS="-skip=.",
            GOWORK="fixture.work",
        )
        self.git(self.root, "init", "--quiet", "--bare", "--initial-branch=master", self.remote)
        self.git(self.root, "init", "--quiet", "--initial-branch=master", self.repo)
        scripts = self.repo / "scripts"
        scripts.mkdir()
        shutil.copy2(ROOT / "scripts/session", scripts / "session")
        shutil.copy2(ROOT / "scripts/session_transport.py", scripts / "session_transport.py")
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
            ["scripts/session", *args], cwd=cwd, env={
                **self.env, "SHELLOPTS": "noexec", "BASHOPTS": "extglob",
                "CDPATH": "/fixture", "GIT_ATTR_SOURCE": "HEAD^", **env,
            },
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
        hook = self.repo / ".git/hooks/pre-push"
        hook.write_text("""#!/bin/sh
printf '%s\\n' "$1" "$2" > "$SESSION_TEST_HOOK_ARGS"
cat > "$SESSION_TEST_HOOK_REFS"
""")
        hook.chmod(0o755)
        hook_args = self.root / "hook-args"
        hook_refs = self.root / "hook-refs"
        rebase_hook = self.repo / ".git/hooks/pre-rebase"
        rebase_hook.write_text('#!/bin/sh\ntouch "$SESSION_TEST_REBASE_HOOK"\n')
        rebase_hook.chmod(0o755)
        ref_hook = self.repo / ".git/hooks/reference-transaction"
        ref_hook.write_text('#!/bin/sh\nif [ -n "$SESSION_TEST_REF_HOOK" ]; then touch "$SESSION_TEST_REF_HOOK"; fi\n')
        ref_hook.chmod(0o755)
        monitor = self.root / "fsmonitor"
        monitor.write_text('#!/bin/sh\ntouch "$SESSION_TEST_FSMONITOR"\nexit 1\n')
        monitor.chmod(0o755)
        self.git(self.repo, "config", "core.fsmonitor", monitor)
        config_hook = self.root / "configured-pre-push"
        config_hook.write_text('#!/bin/sh\ncat > "$SESSION_TEST_CONFIG_HOOK"\n')
        config_hook.chmod(0o755)
        self.git(self.repo, "config", "hook.fixture-rebase.event", "pre-rebase")
        self.git(self.repo, "config", "hook.fixture-rebase.command", rebase_hook)
        self.git(self.repo, "config", "hook.fixture-push.event", "pre-push")
        self.git(self.repo, "config", "hook.fixture-push.command", config_hook)
        hook_env = {
            "SESSION_TEST_HOOK_ARGS": str(hook_args), "SESSION_TEST_HOOK_REFS": str(hook_refs),
            "SESSION_TEST_REBASE_HOOK": str(self.root / "rebase-hook-ran"),
            "SESSION_TEST_CONFIG_HOOK": str(self.root / "configured-hook-refs"),
            "SESSION_TEST_REF_HOOK": str(self.root / "reference-hook-ran"),
            "SESSION_TEST_FSMONITOR": str(self.root / "fsmonitor-ran"),
        }

        self.assertIn("Dry run passed", self.session(self.worktree, "land", "--dry-run", **hook_env))
        rebased = self.git(self.worktree, "rev-parse", "HEAD")
        self.assertNotEqual(rebased, old_head)
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), new_base)
        self.assertEqual(self.remote_tip(), new_base)
        self.assertEqual(len(list(self.evidence.glob("*/build/protonmailcli"))), 1)
        self.assertTrue((self.worktree / "remote-change").is_file())
        self.assertFalse(hook_args.exists())
        self.assertFalse(Path(hook_env["SESSION_TEST_REBASE_HOOK"]).exists())
        self.assertFalse(Path(hook_env["SESSION_TEST_CONFIG_HOOK"]).exists())
        self.assertFalse(Path(hook_env["SESSION_TEST_REF_HOOK"]).exists())
        self.assertFalse(Path(hook_env["SESSION_TEST_FSMONITOR"]).exists())

        self.session(self.worktree, "land", **hook_env)
        self.assertEqual(hook_args.read_text().splitlines(), ["origin", str(self.remote)])
        self.assertIn(f"{rebased} refs/heads/master {new_base}", hook_refs.read_text())
        if SUPPORTS_CONFIG_HOOKS:
            self.assertEqual(Path(hook_env["SESSION_TEST_CONFIG_HOOK"]).read_text(), hook_refs.read_text())
        self.assertEqual(self.remote_tip(), rebased)
        self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/mailbox-preview"), "")
        self.assertEqual(len(list(self.root.glob(".repo-mailbox-preview-end-*/files/session-change"))), 1)

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
if sys.argv[1:2] == ["-C"] and "ls-remote" in sys.argv[3:]:
    Path(os.environ["SESSION_TEST_WORKTREE"], "fixture.pass").write_text("fixture")
""")
        self.assertIn("move ignored files", self.session(self.worktree, "end", status=1, **env))
        self.assertEqual((self.worktree / "fixture.pass").read_text(), "fixture")

    def test_end_restores_checkout_with_an_ignored_file_added_just_before_move(self):
        env = self.git_wrapper("""
worktree = Path(os.environ["SESSION_TEST_WORKTREE"])
if sys.argv[3:6] == ["worktree", "move", str(worktree)]:
    (worktree / "fixture.pass").write_text("fixture")
""")
        self.assertIn("move ignored files", self.session(self.worktree, "end", status=1, **env))
        self.assertEqual((self.worktree / "fixture.pass").read_text(), "fixture")
        self.assertIn(str(self.worktree), self.session(self.repo, "list"))

    def test_end_preserves_a_file_written_after_the_final_scan(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    Path(sys.argv[5]).with_name("files").joinpath("late.pass").write_text("fixture")
""")
        self.session(self.worktree, "end", **env)
        files = list(self.root.glob(".repo-mailbox-preview-end-*/files/late.pass"))
        self.assertEqual(len(files), 1)
        self.assertEqual(files[0].read_text(), "fixture")
        self.assertFalse((files[0].parent / ".git").exists())
        self.assertFalse(self.worktree.exists())

    def test_end_restores_the_worktree_after_an_unregister_failure(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    sys.exit(17)
""")
        self.assertIn("could not unregister the checkout; restored", self.session(
            self.worktree, "end", status=1, **env,
        ))
        self.assertTrue((self.worktree / "scripts/session").is_file())
        self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())

    def test_end_does_not_nest_the_worktree_under_a_recreated_original_path(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    sys.exit(17)
if sys.argv[3:5] == ["worktree", "move"] and sys.argv[6] == os.environ["SESSION_TEST_WORKTREE"]:
    original = Path(sys.argv[6])
    original.mkdir()
    (original / "new-file").write_text("preserve")
""")
        self.assertIn("could not restore the checkout; files at", self.session(
            self.worktree, "end", status=1, **env,
        ))
        self.assertEqual(sorted(path.name for path in self.worktree.iterdir()), ["new-file"])
        retained = list(self.root.glob(".repo-mailbox-preview-end-*/checkout/scripts/session"))
        self.assertEqual(len(retained), 1)

    def test_end_leaves_the_archive_detached_when_registration_is_already_removed(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    subprocess.run([real_git, *sys.argv[1:]], check=True)
    sys.exit(17)
""")
        self.assertIn("could not restore the checkout; files at", self.session(
            self.worktree, "end", status=1, **env,
        ))
        retained = list(self.root.glob(".repo-mailbox-preview-end-*/files"))
        self.assertEqual(len(retained), 1)
        self.assertTrue((retained[0] / "scripts/session").is_file())
        self.assertFalse((retained[0] / ".git").exists())
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/mailbox-preview"), "fix/mailbox-preview")

    def test_end_restores_the_gitfile_when_archive_rename_is_interrupted(self):
        script = """
import os
from pathlib import Path
import runpy
import sys

rename = Path.rename
def interrupt_archive(path, target):
    if Path(target).name == "files":
        if os.environ["SESSION_TEST_INTERRUPT"] == "after":
            rename(path, target)
        raise KeyboardInterrupt
    return rename(path, target)

Path.rename = interrupt_archive
sys.path.insert(0, str(Path("scripts").resolve()))
sys.argv = ["scripts/session", "end"]
runpy.run_path("scripts/session", run_name="__main__")
"""
        for timing in ("before", "after"):
            with self.subTest(timing=timing):
                result = subprocess.run(
                    ["python3", "-c", script], cwd=self.worktree,
                    env={**self.env, "SESSION_TEST_INTERRUPT": timing},
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
                )
                self.assertEqual(result.returncode, 130, result.stdout)
                self.assertTrue((self.worktree / ".git").is_file())
                self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")
        self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())

    def test_end_refuses_when_the_default_branch_changes_during_fetch(self):
        self.change(self.worktree, "session-change")
        self.git(self.worktree, "push", self.remote, "HEAD:refs/heads/master")
        self.git(self.remote, "update-ref", "refs/heads/main", self.base)
        env = self.git_wrapper("""
if sys.argv[3:4] == ["fetch"]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_REMOTE"], "symbolic-ref", "HEAD", "refs/heads/main"],
        check=True,
    )
""")
        self.assertIn("default branch changed during fetch", self.session(
            self.worktree, "end", status=1, **env,
        ))
        self.assertTrue((self.worktree / "session-change").is_file())

    def test_land_preserves_the_session_base_if_the_default_changes_during_fetch(self):
        original = self.change(self.worktree, "session-change")
        self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.git(self.remote, "update-ref", "refs/heads/main", self.base)
        env = self.git_wrapper("""
if sys.argv[3:4] == ["fetch"]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_REMOTE"], "symbolic-ref", "HEAD", "refs/heads/main"],
        check=True,
    )
""")
        self.assertIn("default branch changed during fetch", self.session(
            self.worktree, "land", status=1, **env,
        ))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertEqual(self.git(self.worktree, "rev-parse", "refs/worktree/protonmailcli/session-base"),
                         self.base)
        self.session(self.worktree, "land")
        self.assertEqual(self.git(self.remote, "rev-parse", "refs/heads/main"), original)

    def test_end_preserves_the_branch_if_another_worktree_checks_it_out(self):
        env = self.git_wrapper("""
if "branch" in sys.argv[3:] and "--delete" in sys.argv[3:]:
    repo = Path(sys.argv[2])
    subprocess.run(
        [real_git, "-C", str(repo), "worktree", "add", str(repo.parent / "other"), "fix/mailbox-preview"],
        check=True,
    )
""")
        self.assertIn("Git could not delete the session branch", self.session(
            self.worktree, "end", status=1, **env,
        ))
        self.assertEqual(self.git(self.root / "other", "branch", "--show-current"), "fix/mailbox-preview")
        self.assertTrue(list(self.root.glob(".repo-mailbox-preview-end-*/files/scripts/session")))

    def test_end_can_delete_a_landed_branch_in_a_single_branch_clone(self):
        self.change(self.worktree, "session-change")
        self.git(self.worktree, "push", self.remote, "HEAD:refs/heads/main")
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/main")
        self.git(self.repo, "branch", "protected-local", self.base)
        self.git(self.repo, "config", "--replace-all", "remote.origin.fetch",
                 "+refs/heads/main:refs/heads/protected-local")
        self.git(self.repo, "config", "branch.fix/mailbox-preview.remote", "origin")
        self.git(self.repo, "config", "branch.fix/mailbox-preview.merge", "refs/heads/fix/mailbox-preview")
        self.session(self.worktree, "end")
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/mailbox-preview"), "")
        self.assertEqual(self.git(self.repo, "rev-parse", "protected-local"), self.base)

    def test_end_keeps_a_relative_origin_resolvable_after_moving_the_checkout(self):
        self.change(self.worktree, "session-change")
        self.git(self.worktree, "push", self.remote, "HEAD:refs/heads/master")
        self.git(self.repo, "remote", "set-url", "origin", "../remote.git")
        self.session(self.worktree, "end")
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/mailbox-preview"), "")

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

    def test_success_without_the_build_artifact_refuses_landing(self):
        self.change(self.worktree, "gate-without-build")
        self.assertIn("did not produce an executable", self.session(
            self.worktree, "land", status=1, SESSION_TEST_SKIP_BUILD="1",
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

    def test_push_refuses_a_new_url_rewrite(self):
        self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        config_hook = self.root / "configured-hook"
        config_hook.write_text('#!/bin/sh\ntouch "$SESSION_TEST_CONFIG_HOOK"\n')
        config_hook.chmod(0o755)
        self.git(self.repo, "config", "hook.fixture.event", "pre-push")
        self.git(self.repo, "config", "hook.fixture.command", config_hook)
        self.git(self.repo, "config", "hook.pre-push.enabled", "false")
        marker = self.root / "configured-hook-ran"
        self.assertIn("unexpected push destination", self.session(
            self.worktree, "land", status=1, SESSION_TEST_REWRITE_PUSHURL=str(fork),
            SESSION_TEST_CONFIG_HOOK=str(marker),
        ))
        self.assertFalse(marker.exists())
        self.assertEqual(self.remote_tip(), self.base)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_rebase_does_not_follow_a_second_url_rewrite(self):
        self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        expected_base = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        alias = self.root / "alias.git"
        self.git(self.repo, "remote", "set-url", "origin", alias)
        self.git(self.repo, "config", f"url.{self.remote}.insteadOf", alias)
        self.git(self.repo, "config", f"url.{fork}.insteadOf", self.remote)

        self.session(self.worktree, "land", status=1)
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), expected_base)
        self.assertEqual(self.remote_tip(), expected_base)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_push_refuses_a_default_change_in_the_configured_hook(self):
        self.change(self.worktree, "session-change")
        self.git(self.remote, "update-ref", "refs/heads/main", self.base)
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        hook = self.repo / ".git/hooks/pre-push"
        hook.write_text("""#!/bin/sh
set -e
git --git-dir="$SESSION_TEST_REMOTE" symbolic-ref HEAD refs/heads/main
git config "url.$SESSION_TEST_FORK.insteadOf" "$SESSION_TEST_REMOTE"
""")
        hook.chmod(0o755)
        self.assertIn("default branch changed before the push", self.session(
            self.worktree, "land", status=1, SESSION_TEST_FORK=str(fork),
        ))
        self.assertEqual(self.remote_tip(), self.base)
        self.assertEqual(self.git(self.remote, "rev-parse", "refs/heads/main"), self.base)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_final_remote_lookup_preserves_configured_authentication(self):
        head = self.change(self.worktree, "session-change")
        self.git(self.repo, "config", "http.https://fixture.invalid.extraHeader", "Authorization: fixture")
        self.git(self.repo, "config", "core.askPass", "/fixture/git-askpass")
        self.git(self.repo, "config", "remote.origin.proxy", "")
        self.git(self.repo, "config", "remote.origin.proxyAuthMethod", "basic")
        for helper in ("first", "", "last"):
            self.git(self.repo, "config", "--add", "credential.helper", helper)
        hook = self.repo / ".git/hooks/pre-push"
        hook.write_text("""#!/bin/sh
set -e
git config remote.origin.proxyAuthMethod digest
git config core.askPass /fixture/changed-askpass
""")
        hook.chmod(0o755)
        marker = self.root / "transport-checked"
        env = self.git_wrapper("""
if "push" in sys.argv[3:] and any(arg.startswith("core.hooksPath=") for arg in sys.argv[3:]):
    settings = {
        os.environ[f"GIT_CONFIG_KEY_{index}"]: os.environ[f"GIT_CONFIG_VALUE_{index}"]
        for index in range(int(os.environ["GIT_CONFIG_COUNT"]))
    }
    prefix = next(key.rsplit(".", 1)[0] for key in settings if key.endswith(".url"))
    assert settings[f"{prefix}.proxy"] == ""
    assert settings[f"{prefix}.proxyauthmethod"] == "basic"
if sys.argv[1:3] == ["--git-dir=/dev/null", "ls-remote"]:
    def config(*args):
        return subprocess.check_output([real_git, "--git-dir=/dev/null", "config", *args], text=True)
    assert config("--get", "http.https://fixture.invalid.extraHeader") == "Authorization: fixture\\n"
    assert config("--get-all", "credential.helper").splitlines() == ["first", "", "last"]
    assert config("--get", "core.askPass") == "/fixture/git-askpass\\n"
    assert config("--get", "remote.protonmailcli-endpoint.proxy") == "\\n"
    assert config("--get", "remote.protonmailcli-endpoint.proxyAuthMethod") == "basic\\n"
    Path(os.environ["SESSION_TEST_TRANSPORT_CHECK"]).touch()
""")
        self.session(self.worktree, "land", SESSION_TEST_TRANSPORT_CHECK=str(marker), **env)
        self.assertTrue(marker.is_file())
        self.assertEqual(self.git(self.repo, "config", "--get", "remote.origin.proxyAuthMethod"), "digest")
        self.assertEqual(self.remote_tip(), head)

    def test_branch_switch_during_fetch_stops_before_rebase(self):
        original = self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if sys.argv[3:4] == ["fetch"]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_WORKTREE"], "switch", "--quiet", "-c", "fix/other"],
        check=True,
    )
""")
        self.session(self.worktree, "land", status=1, **env)
        self.assertEqual(self.git(self.worktree, "rev-parse", "fix/other"), original)
        self.assertEqual(self.git(self.worktree, "rev-parse", "fix/mailbox-preview"), original)
        self.assertEqual(self.remote_tip(), advance)

    def test_rewind_after_an_attempt_preserves_the_remote_rewrite(self):
        self.change(self.worktree, "session-change")
        prior_tip = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.session(self.worktree, "land", status=1, SESSION_TEST_GATE_EXIT="17")
        self.git(self.remote, "update-ref", "refs/heads/master", self.base)
        self.git(self.repo, "replace", "--graft", self.base, prior_tip)
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

    def test_landing_does_not_recreate_a_branch_deleted_just_before_push(self):
        self.change(self.worktree, "session-change")
        env = self.git_wrapper("""
if "push" in sys.argv[3:] and any(arg.startswith("core.hooksPath=") for arg in sys.argv[3:]):
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_REMOTE"], "update-ref", "-d", "refs/heads/master"],
        check=True,
    )
""")
        self.session(self.worktree, "land", status=1, **env)
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
        self.git(self.repo, "tag", "local-only")
        self.git(self.repo, "config", "fetch.prune", "true")
        self.git(self.repo, "config", "fetch.pruneTags", "true")
        self.session(self.repo, "start", "from-main")
        self.assertEqual(self.git(self.root / "repo-from-main", "rev-parse", "HEAD"), alternate)
        self.assertEqual(self.git(self.repo, "branch", "--show-current"), "master")
        self.assertEqual(self.git(self.repo, "rev-parse", "refs/tags/local-only"), self.base)

    def test_fetch_refmap_does_not_update_other_local_branches(self):
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.git(self.repo, "branch", "protected-local", self.base)
        self.git(self.repo, "symbolic-ref", "refs/remotes/origin/master", "refs/heads/protected-local")
        self.git(self.repo, "config", "--replace-all", "remote.origin.fetch",
                 "+refs/heads/master:refs/heads/protected-local")
        self.session(self.repo, "start", "refmap")
        self.assertEqual(self.git(self.root / "repo-refmap", "rev-parse", "HEAD"), advance)
        self.assertEqual(self.git(self.repo, "rev-parse", "protected-local"), self.base)

    def test_pinned_http_connections_override_url_specific_redirect_settings(self):
        url = "https://fixture.invalid/repo.git"
        self.git(self.repo, "config", "http.followRedirects", "true")
        self.git(self.repo, "config", f"http.{url}.followRedirects", "true")
        config = self.git(self.repo, "config", "--null", "--list", "--includes")
        push = TRANSPORT["with_config"](self.env, list(TRANSPORT["push_settings"](config, "pinned", url)))
        lookup = TRANSPORT["transport_environment"](config, self.env, url)
        for env in (push, lookup):
            result = subprocess.check_output(
                ["git", "config", "--get-urlmatch", "http.followRedirects", url],
                cwd=self.repo, env=env, text=True,
            )
            self.assertEqual(result.strip(), "false")

    def test_fetch_from_a_changed_origin_preserves_the_shared_tracking_ref(self):
        self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.git(self.repo, "update-ref", "refs/remotes/origin/master", advance)
        env = self.git_wrapper("""
if sys.argv[3:4] == ["fetch"]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_PRIMARY"], "remote", "set-url", "origin",
         os.environ["SESSION_TEST_FORK"]],
        check=True,
    )
""")
        self.assertIn("default branch changed during fetch", self.session(
            self.worktree, "land", status=1, SESSION_TEST_FORK=str(fork), **env,
        ))
        self.assertEqual(self.git(self.repo, "rev-parse", "refs/remotes/origin/master"), advance)

    def test_git_repository_environment_cannot_redirect_qualification(self):
        head = self.change(self.worktree, "session-change")
        self.session(
            self.worktree, "land", GIT_DIR=str(self.repo / ".git"), GIT_WORK_TREE=str(self.repo),
            GIT_INDEX_FILE=str(self.repo / ".git/index"), GIT_CONFIG_COUNT="0",
        )
        self.assertEqual(self.git(self.repo, "remote", "get-url", "origin"), str(self.remote))
        self.assertEqual(self.remote_tip(), head)

    def test_start_and_qualification_do_not_run_checkout_hooks(self):
        hooks = self.root / "hooks"
        hooks.mkdir()
        hook = hooks / "post-checkout"
        hook.write_text("""#!/bin/sh
if [ "$PWD" != "$SESSION_TEST_WORKTREE" ]; then
    printf '%s\\n' "$PWD" > "$SESSION_TEST_CHECKOUT_HOOK"
    exit 17
fi
""")
        hook.chmod(0o755)
        config = self.root / "gitconfig"
        self.git(self.repo, "config", "--file", config, "core.hooksPath", hooks)
        self.git(self.repo, "config", "--file", config, "hook.fixture.event", "post-checkout")
        self.git(self.repo, "config", "--file", config, "hook.fixture.command", hook)
        included = self.root / "conditional-gitconfig"
        self.git(self.repo, "config", "--file", included, "hook.fixture-reference.event",
                 "reference-transaction")
        self.git(self.repo, "config", "--file", included, "hook.fixture-reference.command", hook)
        attributes = self.root / "attributes"
        attributes.write_text("scripts/release-check.sh filter=fixture\n")
        self.git(self.repo, "config", "--file", included, "core.attributesFile", attributes)
        self.git(self.repo, "config", "--file", included, "filter.fixture.smudge",
                 "sed 's/protonmailcli/altered/g'")
        self.git(self.repo, "config", "--file", included, "filter.fixture.clean",
                 "sed 's/altered/protonmailcli/g'")
        self.git(self.repo, "config", "--file", config,
                 "includeIf.gitdir:**/protonmailcli-qualification-*/checkout/.git.path", included)
        marker = self.root / "checkout-hook"
        env = {
            "GIT_CONFIG_GLOBAL": str(config), "SESSION_TEST_CHECKOUT_HOOK": str(marker),
            "SESSION_TEST_EXPECT_CONDITIONAL_HOOK": "1",
        }
        self.session(self.repo, "start", "no-hook", **env)
        self.assertTrue((self.root / "repo-no-hook/scripts/session").is_file())
        head = self.change(self.worktree, "session-change")
        self.session(self.worktree, "land", **env)
        self.assertEqual(self.remote_tip(), head)
        self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
