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
assert "GIT_EXTERNAL_DIFF" not in os.environ
assert not {"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_COUNT", "GIT_ATTR_SOURCE"} & os.environ.keys()
assert sys.argv[sys.argv.index("--sandbox") + 1] == "read-only"
assert "--no-ext-diff --no-textconv" in sys.argv[-1]
assert Path.cwd() != Path(os.environ["SESSION_TEST_WORKTREE"])
assert not subprocess.check_output(["git", "remote"])
assert Path("scripts/session_transport.py").stat().st_mode & 0o777 == 0o644
assert Path("scripts/release-check.sh").stat().st_mode & 0o777 == 0o755
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
if os.environ.get("SESSION_TEST_CHANGE_TRANSPORT"):
    subprocess.run(
        ["git", "-C", os.environ["SESSION_TEST_PRIMARY"], "config", "core.sshCommand", "ssh -F fixture"],
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

    def session(self, cwd, *args, status=0, umask=-1, **env):
        result = subprocess.run(
            ["scripts/session", *args], cwd=cwd, env={
                **self.env, "SHELLOPTS": "noexec", "BASHOPTS": "extglob",
                "CDPATH": "/fixture", "GIT_ATTR_SOURCE": "HEAD^", "GIT_EXTERNAL_DIFF": "/usr/bin/true", **env,
            },
            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, umask=umask,
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
        self.assertEqual(hook_refs.read_text().splitlines(), [
            f"refs/heads/fix/mailbox-preview {rebased} refs/heads/master {new_base}",
        ])
        if SUPPORTS_CONFIG_HOOKS:
            self.assertEqual(Path(hook_env["SESSION_TEST_CONFIG_HOOK"]).read_text(), hook_refs.read_text())
        self.assertEqual(self.remote_tip(), rebased)
        result = self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())
        self.assertEqual(self.git(self.repo, "rev-parse", "fix/mailbox-preview"), rebased)
        archives = list(self.root.glob(".repo-mailbox-preview-end-*"))
        self.assertEqual(len(archives), 1)
        self.assertIn(f"Checkout archive retained at {archives[0]}", result)
        self.assertTrue((archives[0] / "files/session-change").is_file())

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

    def test_end_holds_the_index_lock_until_unregistration(self):
        metadata = self.git(self.worktree, "rev-parse", "--absolute-git-dir")
        blob = self.git(self.worktree, "rev-parse", "HEAD:scripts/session")
        marker = self.root / "late-stage-blocked"
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    archive = Path(sys.argv[5]).with_name("files")
    metadata = os.environ["SESSION_TEST_METADATA"]
    blob = os.environ["SESSION_TEST_BLOB"]
    result = subprocess.run(
        [real_git, f"--git-dir={metadata}", f"--work-tree={archive}", "update-index",
         "--add", "--cacheinfo", f"100644,{blob},staged-only"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    assert result.returncode and "index.lock" in result.stderr
    Path(os.environ["SESSION_TEST_STAGE_MARKER"]).touch()
""")
        self.session(
            self.worktree, "end", SESSION_TEST_METADATA=metadata, SESSION_TEST_BLOB=blob,
            SESSION_TEST_STAGE_MARKER=str(marker), **env,
        )
        self.assertTrue(marker.is_file())
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

    def test_end_restores_an_interrupted_move_before_git_updates_the_registration(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "move"] and sys.argv[5] == os.environ["SESSION_TEST_WORKTREE"]:
    Path(sys.argv[5]).rename(sys.argv[6])
    sys.exit(17)
""")
        self.session(self.worktree, "end", status=1, **env)
        self.assertTrue((self.worktree / "scripts/session").is_file())
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")
        self.session(self.worktree, "end")
        self.assertFalse(self.worktree.exists())

    def test_end_reports_files_already_returned_to_the_original_path(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    sys.exit(17)
if sys.argv[3:5] == ["worktree", "move"] and sys.argv[6] == os.environ["SESSION_TEST_WORKTREE"]:
    Path(sys.argv[5]).rename(sys.argv[6])
    sys.exit(17)
""")
        self.assertIn(f"files at {self.worktree}, Git pointer at {self.worktree / '.git'}", self.session(
            self.worktree, "end", status=1, **env,
        ))
        self.assertTrue((self.worktree / "scripts/session").is_file())
        self.git(self.repo, "worktree", "repair", self.worktree)
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
import signal
import sys

rename = Path.rename
def interrupt_archive(path, target):
    if Path(target).name == "files":
        timing = os.environ["SESSION_TEST_INTERRUPT"]
        if timing == "after":
            rename(path, target)
        if timing == "term":
            os.kill(os.getpid(), signal.SIGTERM)
        raise KeyboardInterrupt
    return rename(path, target)

Path.rename = interrupt_archive
sys.path.insert(0, str(Path("scripts").resolve()))
sys.argv = ["scripts/session", "end"]
runpy.run_path("scripts/session", run_name="__main__")
"""
        for timing, status in (("before", 130), ("after", 130), ("term", 143)):
            with self.subTest(timing=timing):
                result = subprocess.run(
                    ["python3", "-c", script], cwd=self.worktree,
                    env={**self.env, "SESSION_TEST_INTERRUPT": timing},
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
                )
                self.assertEqual(result.returncode, status, result.stdout)
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

    def test_end_preserves_the_branch_if_another_worktree_advances_it(self):
        env = self.git_wrapper("""
if sys.argv[3:5] == ["worktree", "remove"]:
    subprocess.run([real_git, *sys.argv[1:]], check=True)
    repo = Path(sys.argv[2])
    other = repo.parent / "other"
    subprocess.run(
        [real_git, "-C", str(repo), "worktree", "add", str(other), "fix/mailbox-preview"],
        check=True,
    )
    subprocess.run(
        [real_git, "-C", str(other), "commit", "--quiet", "--allow-empty", "-m", "Concurrent commit"],
        check=True,
    )
    sys.exit(0)
""")
        self.session(self.worktree, "end", **env)
        self.assertEqual(self.git(self.root / "other", "branch", "--show-current"), "fix/mailbox-preview")
        self.assertEqual(self.git(self.root / "other", "log", "-1", "--format=%s"), "Concurrent commit")
        self.assertTrue(list(self.root.glob(".repo-mailbox-preview-end-*/files/scripts/session")))

    def test_end_can_archive_a_landed_worktree_in_a_single_branch_clone(self):
        head = self.change(self.worktree, "session-change")
        self.git(self.worktree, "push", self.remote, "HEAD:refs/heads/main")
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/main")
        self.git(self.repo, "branch", "protected-local", self.base)
        self.git(self.repo, "config", "--replace-all", "remote.origin.fetch",
                 "+refs/heads/main:refs/heads/protected-local")
        self.git(self.repo, "config", "branch.fix/mailbox-preview.remote", "origin")
        self.git(self.repo, "config", "branch.fix/mailbox-preview.merge", "refs/heads/fix/mailbox-preview")
        self.session(self.worktree, "end")
        self.assertEqual(self.git(self.repo, "rev-parse", "fix/mailbox-preview"), head)
        self.assertEqual(self.git(self.repo, "rev-parse", "protected-local"), self.base)

    def test_end_keeps_a_relative_origin_resolvable_after_moving_the_checkout(self):
        head = self.change(self.worktree, "session-change")
        self.git(self.worktree, "push", self.remote, "HEAD:refs/heads/master")
        self.git(self.repo, "remote", "set-url", "origin", "../remote.git")
        env = self.git_wrapper("""
if sys.argv[1:2] == ["-C"] and "ls-remote" in sys.argv[3:]:
    assert sys.argv[2] == os.environ["SESSION_TEST_WORKTREE"]
""")
        self.session(self.worktree, "end", **env)
        self.assertEqual(self.git(self.repo, "rev-parse", "fix/mailbox-preview"), head)

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

    def test_transport_change_during_qualification_refuses_push(self):
        self.change(self.worktree, "session-change")
        self.assertIn("origin's transport settings changed during qualification", self.session(
            self.worktree, "land", status=1, SESSION_TEST_CHANGE_TRANSPORT="1",
        ))
        self.assertEqual(self.remote_tip(), self.base)

    def test_landing_pins_the_original_remote_when_push_url_changes(self):
        head = self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        self.session(self.worktree, "land", SESSION_TEST_SWAP_PUSHURL=str(fork))
        self.assertEqual(self.remote_tip(), head)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_push_ignores_a_new_url_rewrite(self):
        head = self.change(self.worktree, "session-change")
        fork = self.root / "fork.git"
        self.git(self.repo, "clone", "--quiet", "--bare", self.remote, fork)
        config_hook = self.root / "configured-hook"
        config_hook.write_text('#!/bin/sh\ntouch "$SESSION_TEST_CONFIG_HOOK"\n')
        config_hook.chmod(0o755)
        self.git(self.repo, "config", "hook.fixture.event", "pre-push")
        self.git(self.repo, "config", "hook.fixture.command", config_hook)
        self.git(self.repo, "config", "hook.pre-push.enabled", "false")
        marker = self.root / "configured-hook-ran"
        self.session(
            self.worktree, "land", SESSION_TEST_REWRITE_PUSHURL=str(fork),
            SESSION_TEST_CONFIG_HOOK=str(marker),
        )
        self.assertFalse(marker.exists())
        self.assertEqual(self.remote_tip(), head)
        self.assertEqual(self.git(fork, "rev-parse", "refs/heads/master"), self.base)

    def test_rehearsal_skips_hooks_enabled_by_the_temporary_push_remote(self):
        self.change(self.worktree, "session-change")
        alias = self.root / "alias.git"
        self.git(self.repo, "remote", "set-url", "origin", alias)
        self.git(self.repo, "config", f"url.{self.remote}.insteadOf", alias)
        hook = self.root / "conditional-pre-push"
        hook.write_text('#!/bin/sh\ntouch "$SESSION_TEST_CONDITIONAL_HOOK"\nexit 17\n')
        hook.chmod(0o755)
        config = self.root / "gitconfig"
        included = self.root / "conditional-gitconfig"
        self.git(self.repo, "config", "--file", included, "hook.fixture-conditional.event", "pre-push")
        self.git(self.repo, "config", "--file", included, "hook.fixture-conditional.command", hook)
        self.git(self.repo, "config", "--file", config,
                 f"includeIf.hasconfig:remote.*.url:{self.remote}.path", included)
        marker = self.root / "conditional-pre-push-ran"
        env = self.git_wrapper("""
if "push" in sys.argv[3:] and any(arg.startswith("core.hooksPath=") for arg in sys.argv[3:]):
    git_dir = next(arg for arg in sys.argv if arg.startswith("--git-dir="))
    result = subprocess.run(
        [real_git, "-C", sys.argv[2], git_dir, "config", "--get", "hook.fixture-conditional.event"],
        text=True, stdout=subprocess.PIPE,
    )
    assert result.returncode == 1 and not result.stdout
""")
        self.session(
            self.worktree, "land", "--dry-run", GIT_CONFIG_GLOBAL=str(config),
            SESSION_TEST_CONDITIONAL_HOOK=str(marker), **env,
        )
        self.assertFalse(marker.exists())

    def test_push_does_not_load_transport_changes_enabled_by_the_temporary_remote(self):
        head = self.change(self.worktree, "session-change")
        alias = self.root / "alias.git"
        self.git(self.repo, "remote", "set-url", "origin", alias)
        self.git(self.repo, "config", f"url.{self.remote}.insteadOf", alias)
        config = self.root / "gitconfig"
        included = self.root / "conditional-gitconfig"
        self.git(self.repo, "config", "--file", included, "core.sshCommand", "ssh -F fixture")
        self.git(self.repo, "config", "--file", config,
                 f"includeIf.hasconfig:remote.*.url:{self.remote}.path", included)
        env = self.git_wrapper("""
if "push" in sys.argv[3:] and any(arg.startswith("core.hooksPath=") for arg in sys.argv[3:]):
    git_dir = next(arg for arg in sys.argv if arg.startswith("--git-dir="))
    result = subprocess.run(
        [real_git, "-C", sys.argv[2], git_dir, "config", "--get", "core.sshCommand"],
        text=True, stdout=subprocess.PIPE,
    )
    assert result.returncode == 1 and not result.stdout
""")
        self.session(self.worktree, "land", GIT_CONFIG_GLOBAL=str(config), **env)
        self.assertEqual(self.remote_tip(), head)

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

        self.session(self.worktree, "land")
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), expected_base)
        self.assertEqual(self.remote_tip(), self.git(self.worktree, "rev-parse", "HEAD"))
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

    def test_isolated_push_preserves_push_option_order_and_resets(self):
        head = self.change(self.worktree, "session-change")
        self.git(self.remote, "config", "receive.advertisePushOptions", "true")
        hook = self.remote / "hooks/pre-receive"
        hook.write_text("""#!/bin/sh
printf '%s\\n' "$GIT_PUSH_OPTION_COUNT" "$GIT_PUSH_OPTION_0" "$GIT_PUSH_OPTION_1" > "$SESSION_TEST_PUSH_OPTIONS"
""")
        hook.chmod(0o755)
        for option in ("discard", "", "workflow=fixture", "order=second"):
            self.git(self.repo, "config", "--add", "push.pushOption", option)
        options = self.root / "push-options"
        self.session(self.worktree, "land", SESSION_TEST_PUSH_OPTIONS=str(options))
        self.assertEqual(options.read_text().splitlines(), ["2", "workflow=fixture", "order=second"])
        self.assertEqual(self.remote_tip(), head)

    def test_isolated_push_preserves_required_push_signing(self):
        self.change(self.worktree, "session-change")
        self.git(self.repo, "config", "push.gpgSign", "true")
        self.assertIn("does not support --signed push", self.session(self.worktree, "land", status=1))
        self.assertEqual(self.remote_tip(), self.base)

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

    def test_rebase_install_does_not_overwrite_an_ignored_file_created_late(self):
        original = self.change(self.worktree, "session-change")
        (self.repo / "fixture.pass").write_text("tracked upstream")
        self.git(self.repo, "add", "-f", "fixture.pass")
        self.git(self.repo, "commit", "--quiet", "-m", "Add fixture upstream")
        advance = self.git(self.repo, "rev-parse", "HEAD")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "checkout" in sys.argv[3:] and "--no-overwrite-ignore" in sys.argv[3:]:
    Path(os.environ["SESSION_TEST_WORKTREE"], "fixture.pass").write_text("preserve")
""")
        self.assertIn("would be overwritten", self.session(
            self.worktree, "land", status=1, TMPDIR=str(self.root), **env,
        ))
        self.assertEqual((self.worktree / "fixture.pass").read_text(), "preserve")
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertEqual(self.git(self.worktree, "rev-parse", "refs/worktree/protonmailcli/session-base"),
                         self.base)
        self.assertEqual(self.remote_tip(), advance)
        self.git(self.remote, "update-ref", "refs/heads/master", self.base)
        (self.worktree / "fixture.pass").unlink()
        self.session(self.worktree, "land")
        self.assertEqual(self.remote_tip(), original)

    def test_rebase_install_preserves_a_commit_made_just_before_checkout(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if sys.argv[3:] == ["rev-parse", "--path-format=absolute", "--git-path", "index"]:
    worktree = Path(os.environ["SESSION_TEST_WORKTREE"])
    (worktree / "concurrent-change").write_text("preserve")
    subprocess.run([real_git, "-C", str(worktree), "add", "concurrent-change"], check=True)
    subprocess.run(
        [real_git, "-C", str(worktree), "commit", "--quiet", "-m", "Concurrent commit"], check=True,
    )
""")
        self.assertIn("session changed before applying the candidate", self.session(
            self.worktree, "land", status=1, **env,
        ))
        self.assertEqual(self.git(self.worktree, "branch", "--show-current"), "fix/mailbox-preview")
        self.assertEqual(self.git(self.worktree, "log", "-1", "--format=%s"), "Concurrent commit")
        self.assertEqual((self.worktree / "concurrent-change").read_text(), "preserve")
        self.assertEqual(self.git(self.worktree, "rev-parse", "refs/worktree/protonmailcli/session-base"),
                         self.base)
        self.assertEqual(self.remote_tip(), advance)

    def test_rebase_install_preserves_an_edit_staged_just_before_the_index_lock(self):
        original = self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if sys.argv[3:] == ["rev-parse", "--path-format=absolute", "--git-path", "index"]:
    worktree = Path(os.environ["SESSION_TEST_WORKTREE"])
    (worktree / "remote-change").write_text("remote-change")
    subprocess.run([real_git, "-C", str(worktree), "add", "remote-change"], check=True)
""")
        self.assertIn("session index or files changed before applying the candidate", self.session(
            self.worktree, "land", status=1, **env,
        ))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "A  remote-change")
        self.assertEqual((self.worktree / "remote-change").read_text(), "remote-change")
        self.assertEqual(self.remote_tip(), advance)

    def test_rebase_rollback_preserves_the_pre_installation_files_after_a_soft_reset(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_WORKTREE"], "reset", "--soft",
         os.environ["SESSION_TEST_SOFT_RESET"]],
        check=True,
    )
""")
        self.session(
            self.worktree, "land", status=1, TMPDIR=str(self.root),
            SESSION_TEST_SOFT_RESET=self.base, **env,
        )
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), self.base)
        self.assertEqual((self.worktree / "session-change").read_text(), "session-change")
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "A  session-change")
        self.assertFalse((self.worktree / "remote-change").exists())
        self.assertEqual(self.remote_tip(), advance)

    def test_matching_head_without_a_transaction_receipt_does_not_publish_the_candidate_index(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    updates = sys.stdin.read()
    candidate = updates.splitlines()[0].split()[2]
    subprocess.run(
        [real_git, "-C", os.environ["SESSION_TEST_WORKTREE"], "reset", "--soft", candidate],
        check=True,
    )
    sys.exit(subprocess.run([real_git, *sys.argv[1:]], input=updates, text=True).returncode)
""")
        self.assertIn("recovery files retained", self.session(
            self.worktree, "land", status=1, TMPDIR=str(self.root), **env,
        ))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), advance)
        self.assertFalse((self.worktree / "remote-change").exists())
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "D  remote-change")
        self.assertEqual(self.remote_tip(), advance)

    def test_partial_ref_transaction_retains_the_candidate_recovery_files(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    updates = sys.stdin.read().splitlines()
    worktree = os.environ["SESSION_TEST_WORKTREE"]
    for update in updates[:2]:
        action, ref, new, old = update.split()
        assert action == "update"
        subprocess.run([real_git, "-C", worktree, "update-ref", ref, new, old], check=True)
    sys.exit(17)
""")
        self.assertIn("recovery files retained", self.session(
            self.worktree, "land", status=1, TMPDIR=str(self.root), **env,
        ))
        self.assertTrue(list(self.root.glob("protonmailcli-rebase-*/installation-index")))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), advance)
        self.assertFalse((self.worktree / "remote-change").exists())
        self.assertEqual(self.remote_tip(), advance)

    def test_partial_installation_retains_recovery_files_after_a_signal_or_checkout_error(self):
        original = self.change(self.worktree, "session-change")
        self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "checkout" in sys.argv[3:] and "--no-overwrite-ignore" in sys.argv[3:]:
    import signal
    Path(os.environ["SESSION_TEST_WORKTREE"], "partial-candidate").write_text("partial")
    if os.environ["SESSION_TEST_INSTALL_FAILURE"] == "signal":
        os.kill(os.getppid(), signal.SIGTERM)
    sys.exit(17)
""")
        partial = self.worktree / "partial-candidate"
        for count, failure in enumerate(("signal", "error"), 1):
            with self.subTest(failure=failure):
                self.assertIn("recovery files retained", self.session(
                    self.worktree, "land", status=1, TMPDIR=str(self.root),
                    SESSION_TEST_INSTALL_FAILURE=failure, **env,
                ))
                self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
                self.assertEqual(len(list(self.root.glob("protonmailcli-rebase-*/installation-index"))), count)
                self.assertEqual(partial.read_text(), "partial")
                partial.unlink()

    def test_rebase_install_locks_the_live_index_against_concurrent_commits(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        marker = self.root / "index-lock"
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    worktree = os.environ["SESSION_TEST_WORKTREE"]
    result = subprocess.run(
        [real_git, "-C", worktree, "commit", "--quiet", "--allow-empty", "-m", "Concurrent commit"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    assert result.returncode and "index.lock" in result.stderr
    Path(os.environ["SESSION_TEST_INDEX_LOCK"]).touch()
""")
        self.session(self.worktree, "land", SESSION_TEST_INDEX_LOCK=str(marker), **env)
        self.assertTrue(marker.is_file())
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), advance)
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")

    def test_rebase_install_keeps_the_session_branch_reserved(self):
        self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        marker = self.root / "branch-reservation"
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    repo = Path(os.environ["SESSION_TEST_PRIMARY"])
    other = repo.parent / "other"
    result = subprocess.run(
        [real_git, "-C", str(repo), "worktree", "add", str(other), "fix/mailbox-preview"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    assert result.returncode and not other.exists()
    Path(os.environ["SESSION_TEST_RESERVATION"]).touch()
""")
        self.session(self.worktree, "land", SESSION_TEST_RESERVATION=str(marker), **env)
        self.assertTrue(marker.is_file())
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD^"), advance)
        self.assertEqual(self.remote_tip(), self.git(self.worktree, "rev-parse", "HEAD"))

    def test_install_transaction_rejects_switching_to_another_branch_at_the_same_commit(self):
        original = self.change(self.worktree, "session-change")
        advance = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        env = self.git_wrapper("""
if "update-ref" in sys.argv[3:] and "--stdin" in sys.argv[3:]:
    worktree = os.environ["SESSION_TEST_WORKTREE"]
    subprocess.run(
        [real_git, "-C", worktree, "branch", "fix/other"],
        check=True,
    )
    subprocess.run(
        [real_git, "-C", worktree, "symbolic-ref", "HEAD", "refs/heads/fix/other"],
        check=True,
    )
""")
        self.assertIn("checked-out branch changed during installation", self.session(
            self.worktree, "land", status=1, **env,
        ))
        self.assertEqual(self.git(self.worktree, "branch", "--show-current"), "fix/other")
        self.assertEqual(self.git(self.worktree, "rev-parse", "fix/mailbox-preview"), original)
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")
        self.assertEqual(self.remote_tip(), advance)

    def test_land_refuses_a_split_index(self):
        self.git(self.worktree, "update-index", "--split-index")
        self.assertIn("disable the split index", self.session(self.worktree, "land", status=1))

    def test_rehearsal_skips_hooks_configured_only_for_candidate_metadata(self):
        self.change(self.worktree, "session-change")
        self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        hook = self.root / "candidate-hook"
        hook.write_text('#!/bin/sh\ntouch "$SESSION_TEST_CANDIDATE_HOOK"\nexit 17\n')
        hook.chmod(0o755)
        config = self.root / "gitconfig"
        included = self.root / "conditional-gitconfig"
        self.git(self.repo, "config", "--file", included, "hook.fixture-candidate.event", "post-checkout")
        self.git(self.repo, "config", "--file", included, "hook.fixture-candidate.command", hook)
        self.git(self.repo, "config", "--file", config,
                 "includeIf.gitdir:**/worktrees/checkout.path", included)
        marker = self.root / "candidate-hook-ran"
        self.session(
            self.worktree, "land", "--dry-run", GIT_CONFIG_GLOBAL=str(config),
            SESSION_TEST_CANDIDATE_HOOK=str(marker),
        )
        self.assertFalse(marker.exists())

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

    def test_rebase_conflict_reports_the_base_and_leaves_the_session_untouched(self):
        original = self.change(self.worktree, "conflict")
        (self.repo / "conflict").write_text("upstream")
        self.git(self.repo, "add", "conflict")
        self.git(self.repo, "commit", "--quiet", "-m", "Add a conflicting upstream file")
        base = self.git(self.repo, "rev-parse", "HEAD")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.assertIn(f"rebase onto {base} did not complete", self.session(
            self.worktree, "land", status=1,
        ))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertEqual(self.git(self.worktree, "status", "--porcelain"), "")
        self.assertEqual((self.worktree / "conflict").read_text(), "conflict")
        self.assertEqual(self.remote_tip(), base)

    def test_rebase_refuses_different_worktree_checkout_conversions(self):
        original = self.change(self.worktree, "session-change")
        base = self.change(self.repo, "remote-change")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        metadata = self.git(self.worktree, "rev-parse", "--absolute-git-dir")
        config = self.root / "gitconfig"
        included = self.root / "conditional-gitconfig"
        self.git(self.repo, "config", "--file", config, f"includeIf.gitdir:{metadata}.path", included)
        for setting in ("autocrlf = true", "symlinks = false"):
            with self.subTest(setting=setting):
                included.write_text(f"[core]\n    {setting}\n")
                self.assertIn("checkout conversion settings differ", self.session(
                    self.worktree, "land", status=1, TMPDIR=str(self.root), GIT_CONFIG_GLOBAL=str(config),
                ))
        self.assertEqual(self.git(self.worktree, "rev-parse", "HEAD"), original)
        self.assertFalse((self.worktree / "remote-change").exists())
        self.assertFalse(list(self.root.glob("protonmailcli-rebase-*")))
        self.assertEqual(self.remote_tip(), base)

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

    def test_start_refuses_a_name_that_matches_the_remote_default_branch(self):
        self.git(self.remote, "update-ref", "refs/heads/fix/collision", self.base)
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/fix/collision")
        self.assertIn("matches the remote default branch", self.session(
            self.repo, "start", "collision", status=1,
        ))
        self.assertEqual(self.git(self.repo, "branch", "--list", "fix/collision"), "")
        self.assertFalse((self.root / "repo-collision").exists())

    def test_start_refuses_an_origin_with_a_custom_remote_helper_setting(self):
        self.git(self.repo, "config", "remote.origin.vcs", "fixture")
        self.assertIn("standard Git transport", self.session(self.repo, "start", "custom", status=1))
        self.assertFalse((self.root / "repo-custom").exists())

    def test_interrupted_start_reports_the_recoverable_checkout_and_base(self):
        path = self.root / "repo-recovery"
        env = self.git_wrapper("""
if sys.argv[2] == os.environ["SESSION_TEST_START_PATH"] and sys.argv[3:5] == [
    "update-ref", "refs/worktree/protonmailcli/session-base",
]:
    import signal
    os.kill(os.getppid(), signal.SIGTERM)
    sys.exit(17)
""")
        result = self.session(self.repo, "start", "recovery", status=1, SESSION_TEST_START_PATH=str(path), **env)
        self.assertIn(f"checkout preserved at {path} and base {self.base}", result)
        self.git(path, "update-ref", "refs/worktree/protonmailcli/session-base", self.base)
        self.git(path, "restore", "--source=HEAD", "--staged", "--worktree", "--", ".")
        self.assertTrue((path / "scripts/session").is_file())

    def test_older_git_runs_an_executable_file_hook_without_a_shebang(self):
        head = self.change(self.worktree, "session-change")
        hook = self.repo / ".git/hooks/pre-push"
        hook.write_text('printf "%s\\n" "$1" > "$SESSION_TEST_HOOK_MARKER"\n')
        hook.chmod(0o755)
        marker = self.root / "legacy-hook"
        env = self.git_wrapper("""
if sys.argv[3:] == ["version"]:
    print("git version 2.53.0")
    sys.exit(0)
""")
        self.session(self.worktree, "land", SESSION_TEST_HOOK_MARKER=str(marker), **env)
        self.assertEqual(marker.read_text().strip(), "origin")
        self.assertEqual(self.remote_tip(), head)

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
        env = TRANSPORT["transport_environment"](config, self.env, url)
        result = subprocess.check_output(
            ["git", "config", "--get-urlmatch", "http.followRedirects", url],
            cwd=self.repo, env=env, text=True,
        )
        self.assertEqual(result.strip(), "false")

    def test_pinned_connections_preserve_server_option_order(self):
        for option in ("first=one", "second=two", "first=three"):
            self.git(self.repo, "config", "--add", "remote.origin.serverOption", option)
        config = self.git(self.repo, "config", "--null", "--list", "--includes")
        url = "https://fixture.invalid/repo.git"
        env = TRANSPORT["transport_environment"](config, self.env, url)
        alias = TRANSPORT["LOOKUP_REMOTE"]
        result = subprocess.check_output(
            ["git", "config", "--get-all", f"remote.{alias}.serverOption"],
            cwd=self.repo, env=env, text=True,
        )
        self.assertEqual(result.splitlines(), ["first=one", "second=two", "first=three"])

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

    def test_restrictive_umask_does_not_change_tracked_checkout_modes(self):
        private = self.root / "repo-private"
        self.session(self.repo, "start", "private", umask=0o077)
        self.assertEqual((private / "scripts/session_transport.py").stat().st_mode & 0o777, 0o644)
        self.assertEqual((private / "scripts/release-check.sh").stat().st_mode & 0o777, 0o755)
        self.change(private, "session-change")
        for filename in ("session_transport.py", "release-check.sh"):
            with (self.repo / "scripts" / filename).open("a") as script:
                script.write("\n# Fixture remote update\n")
        self.git(self.repo, "add", "scripts/session_transport.py", "scripts/release-check.sh")
        self.git(self.repo, "commit", "--quiet", "-m", "Update fixture scripts")
        base = self.git(self.repo, "rev-parse", "HEAD")
        self.git(self.repo, "push", "--quiet", "origin", "master")
        self.session(private, "land", umask=0o077, SESSION_TEST_WORKTREE=str(private))
        self.assertEqual(self.git(private, "rev-parse", "HEAD^"), base)
        self.assertEqual((private / "scripts/session_transport.py").stat().st_mode & 0o777, 0o644)
        self.assertEqual((private / "scripts/release-check.sh").stat().st_mode & 0o777, 0o755)
        self.assertEqual(self.remote_tip(), self.git(private, "rev-parse", "HEAD"))


if __name__ == "__main__":
    unittest.main()
