#!/usr/bin/env python3
"""Prove help capture/isolation and docs failures using disposable fixtures."""

from __future__ import annotations

import json
import os
import re
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
from string import Template
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
BIN_STUB = '''#!/usr/bin/env python3
import os
from pathlib import Path
import sys

assert os.environ.get('PMAIL_USE_LOCAL_STATE') == '1'
args = sys.argv[1:]
config = Path(args[args.index('--config') + 1])
state = Path(args[args.index('--state') + 1])
if 'setup' in args:
    config.write_text('fixture configuration')
    state.write_text('{}')
    print('setup completed')
    raise SystemExit(0)
assert config.exists() and state.exists()
print('help on stdout', flush=True)
print('help on stderr', file=sys.stderr)
'''
GO_STUB = '''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

assert sys.argv[1] == 'build', sys.argv
target = Path(sys.argv[sys.argv.index('-o') + 1])
Path(os.environ['HELP_STUB_LOG']).write_text(json.dumps({'binary': str(target), 'cache': os.environ.get('GOCACHE')}))
if os.environ.get('HELP_BUILD_FAILURE') == '1':
    print('fixture build failed', file=sys.stderr)
    raise SystemExit(17)
target.write_text(Path(os.environ['HELP_BIN_STUB']).read_text())
target.chmod(0o755)
'''


class HelpScriptsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="proton-help-contract-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.repo = self.root / "repo"
        for relative in ("scripts", "docs/help", "cmd/protonmailcli", ".tmp", ".gocache"):
            (self.repo / relative).mkdir(parents=True, exist_ok=True)
        for name in ("update-help.sh", "check-help.sh"):
            shutil.copy2(ROOT / "scripts" / name, self.repo / "scripts" / name)
        (self.repo / "scripts/help-snapshots.txt").write_text("root.txt\t--help\n")
        (self.repo / ".tmp/user-artifact").write_text("keep existing binary artifacts\n")
        (self.repo / ".gocache/user-artifact").write_text("keep existing cache artifacts\n")
        (self.repo / "docs/help/root.txt").write_text("help on stdout\nhelp on stderr\n")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "go").write_text(GO_STUB)
        (self.bin / "go").chmod(0o755)
        (self.root / "binary.py").write_text(BIN_STUB)
        self.scratch = self.root / "scratch"
        self.scratch.mkdir()
        self.output = self.root / "snapshots"
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}", TMPDIR=str(self.scratch), GOCACHE=str(self.repo / ".gocache"), HELP_STUB_LOG=str(self.root / "build.json"), HELP_BIN_STUB=str(self.root / "binary.py"))

    def run_script(self, name: str, *args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(["/bin/bash", str(self.repo / "scripts" / name), *args], cwd=self.root, env=env or self.env, capture_output=True, text=True, timeout=15)

    def artifacts(self) -> dict[str, bytes]:
        return {str(path.relative_to(self.repo)): path.read_bytes() for directory in (self.repo / ".tmp", self.repo / ".gocache") for path in directory.rglob("*") if path.is_file()}

    def test_generator_captures_both_streams(self) -> None:
        result = self.run_script("update-help.sh", "--out-dir", str(self.output))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.output / "root.txt").read_text(), "help on stdout\nhelp on stderr\n")

    def test_generator_owns_temp_binary_cache_config_and_state(self) -> None:
        before = self.artifacts()
        result = self.run_script("update-help.sh", "--out-dir", str(self.output))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.artifacts(), before)
        metadata = json.loads((self.root / "build.json").read_text())
        binary = Path(metadata["binary"])
        cache = Path(metadata["cache"])
        self.assertTrue(binary.is_relative_to(self.scratch), binary)
        self.assertTrue(cache.is_relative_to(binary.parent), cache)
        self.assertFalse(binary.parent.exists(), "temporary binary/config/state/cache root leaked")
        self.assertEqual(list(self.scratch.iterdir()), [])

    def test_check_help_accepts_current_combined_output(self) -> None:
        result = self.run_script("check-help.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(list(self.scratch.iterdir()), [])

    def test_stderr_only_snapshot_drift_fails_with_refresh_hint(self) -> None:
        (self.repo / "docs/help/root.txt").write_text("help on stdout\n")
        result = self.run_script("check-help.sh")
        self.assertEqual(result.returncode, 1)
        self.assertIn("run: scripts/update-help.sh", result.stderr)
        self.assertEqual((self.repo / "docs/help/root.txt").read_text(), "help on stdout\n")

    def test_build_failure_preserves_artifacts_and_cleans_owned_work(self) -> None:
        before = self.artifacts()
        result = self.run_script("update-help.sh", "--out-dir", str(self.output), env=dict(self.env, HELP_BUILD_FAILURE="1"))
        self.assertEqual(result.returncode, 17)
        self.assertIn("fixture build failed", result.stderr)
        self.assertEqual(self.artifacts(), before)
        self.assertEqual(list(self.scratch.iterdir()), [])

    def test_unknown_generator_arguments_fail_before_building(self) -> None:
        result = self.run_script("update-help.sh", "--unexpected")
        self.assertEqual(result.returncode, 2)
        self.assertFalse((self.root / "build.json").exists())


class HumanOutputContractTests(unittest.TestCase):
    def test_spec_identifies_batch_error_fields(self) -> None:
        spec = (ROOT / "docs/cli-spec.md").read_text()
        stdout = spec.split("### stdout\n", 1)[1].split("### stderr", 1)[0]
        for field in ("results[].errorCode", "results[].error"):
            self.assertIn(f"`{field}`", stdout)


class DocumentationFaultTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="proton-docs-contract-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.readme = """# Fixture

## Install
Install the CLI.
## Usage
Read the docs.
## Release
make changelog-context VERSION=vX.Y.Z
make release-check VERSION=vX.Y.Z
make release-dry-run VERSION=vX.Y.Z
make release VERSION=vX.Y.Z
scripts/changelog-context.sh
scripts/release-check.sh
scripts/release.sh
RELEASING.md
## Docs
[Guide](guide.md)
"""
        (self.root / "README.md").write_text(self.readme)
        (self.root / "RELEASING.md").write_text("# Releasing\n")
        (self.root / "CHANGELOG.md").write_text("# Changes\n\n## [v1.2.3] - 2026-09-30\n\n- Historical fixture.\n")
        (self.root / "guide.md").write_text("# Guide\n")

    def check(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run([sys.executable, str(ROOT / "scripts/docs-contract-check.py"), "--root", str(self.root)], capture_output=True, text=True, timeout=15)

    def test_valid_publisher_documentation_passes(self) -> None:
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_reversed_headings_are_rejected(self) -> None:
        (self.root / "README.md").write_text(self.readme.replace("## Install", "## Swap").replace("## Usage", "## Install").replace("## Swap", "## Usage"))
        self.assertEqual(self.check().returncode, 1)

    def test_broken_local_link_is_rejected(self) -> None:
        (self.root / "guide.md").unlink()
        self.assertEqual(self.check().returncode, 1)


class MailboxResolutionGuideTests(unittest.TestCase):
    def test_localized_role_example_uses_its_exact_server_name(self) -> None:
        guide = (ROOT / "docs/imap-workflows.md").read_text()
        examples = re.findall(r"`mailbox resolve --name ([^`]+)`", guide)
        names = {"drafts": "ordinary mailbox", "Brouillons": "special-use mailbox"}
        self.assertIn("special-use mailbox", [names.get(example) for example in examples],
                      "an exact folder named drafts takes precedence over the shared role ID")


class SendReceiptDocumentationTests(unittest.TestCase):
    def test_send_many_explains_both_key_scopes(self) -> None:
        workflow = " ".join((ROOT / "docs/imap-workflows.md").read_text().split())
        self.assertIn("`message send-many --idempotency-key`", workflow)
        self.assertRegex(workflow, r"command receipt.{0,110}before.{0,75}item keys")


class ReadmeFindAndSendTests(unittest.TestCase):

    def test_missing_selectors_stop_interactive_shell_commands(self) -> None:
        section = (ROOT / "README.md").read_text().split("Recommended agent sequence for “find and send draft”", 1)[1].split("## Agent Playbooks", 1)[0]
        steps = re.findall(r"```bash\n(.*?)\n```", section, re.DOTALL)
        for block in steps:
            with self.subTest(block=block[:40]), tempfile.TemporaryDirectory() as directory:
                work = Path(directory)
                cli = work / "protonmailcli"
                cli.write_text('#!/bin/sh\nprintf called >> calls\necho \'{"data":{"drafts":[{"id":"41"}],"total":1}}\'\n')
                cli.chmod(0o755)
                setup = "unset BRIDGE_CONFIG BRIDGE_STATE BRIDGE_PROFILE BRIDGE_ACCOUNT; draft_id=stale; reviewed_context=(--config stale)\n"
                if block == steps[1]:
                    setup += "unset reviewed_context\n"
                subprocess.run(["bash", "--noprofile", "--norc", "-i"], input=setup+block+"\nexit\n", cwd=work,
                               capture_output=True, text=True, timeout=10)
                self.assertFalse((work / "calls").exists(), "unguarded command ran after missing selectors")


    def test_send_key_is_scoped_to_the_saved_intent(self) -> None:
        readme = (ROOT / "README.md").read_text()
        recipe = readme.split("Recommended agent sequence for “find and send draft”", 1)[1].split("## Agent Playbooks", 1)[0]
        flag = re.search(r'`--idempotency-key "([^"]+)"`', recipe)
        self.assertIsNotNone(flag)
        template = Template(flag[1])
        first = template.substitute(draft_id="imap:Drafts:41", send_key="intent-1")
        second = template.substitute(draft_id="imap:Drafts:41", send_key="intent-2")
        self.assertNotEqual(first, second, "the same UID can occur in two accounts or two intended sends")

    def test_ambiguous_receipt_guidance_inspects_each_candidate(self) -> None:
        readme = (ROOT / "README.md").read_text()
        instructions = readme.split("Pass the ID from `draft list`", 1)[1].split("Create a threaded follow-up", 1)[0]
        self.assertIn("`message get`", instructions)
        self.assertIn("each candidate", instructions)
        workflow = (ROOT / "docs/imap-workflows.md").read_text()
        examples = [line for line in workflow.splitlines() if "message get --message-id" in line and line.startswith("protonmailcli ")]
        self.assertGreaterEqual(len(examples), 2)
        expected_prefix = ["protonmailcli", "--json", "--config", "/mail/config.toml",
                           "--state", "/mail/state.json", "--profile", "work",
                           "message", "get", "--message-id"]
        commands = [shlex.split(cmd) for cmd in examples]
        self.assertTrue(all(args[:-1] == expected_prefix for args in commands))
        self.assertEqual({args[-1] for args in commands}, {"imap-v2:Drafts:41", "imap:Entwuerfe:41"})


    def test_preview_and_send_confirm_the_selected_search_result(self) -> None:
        readme = (ROOT / "README.md").read_text()
        section = readme.split("Recommended agent sequence for “find and send draft”", 1)[1].split("## Agent Playbooks", 1)[0]
        steps = re.findall(r"```bash\n(.*?)\n```", section, re.DOTALL)
        self.assertEqual(len(steps), 2)
        selected = "imap:Projets terminés:41"
        stub = """#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
assert args[args.index("--expect-account") + 1] == "test@example.invalid"
for flag, value in (("--config", "initial config.toml"), ("--state", "initial state.json"), ("--profile", "reviewed")):
    expected = value if flag == "--profile" else os.path.join(os.getcwd(), value)
    defaults = {"--config": os.path.join(os.environ["XDG_CONFIG_HOME"], "initial config.toml"),
                "--state": os.path.join(os.environ["XDG_DATA_HOME"], "initial state.json"), "--profile": os.environ["PMAIL_PROFILE"]}
    actual = args[args.index(flag) + 1] if flag in args else defaults[flag]
    if flag != "--profile":
        actual = os.path.realpath(actual)
        expected = os.path.realpath(expected)
    assert actual == expected, (flag, actual, expected)
step = "search" if "search" in args else "inspect" if "get" in args else "preview" if "--dry-run" in args else "send"
with open("calls", "a") as log:
    print(step, file=log)
if step == os.environ["FAIL_STEP"] and step != "search":
    sys.exit(4)
if step == "search":
    print(json.dumps({"data": {"drafts": [{"id": os.environ["SELECTED"]}], "total": 1}}))
    sys.exit(4 if step == os.environ["FAIL_STEP"] else 0)
else:
    assert args[args.index("--draft-id") + 1] == os.environ["SELECTED"]
    if step != "inspect":
        assert args[args.index("--confirm-send") + 1] == os.environ["SELECTED"]
"""
        for failure, expected in (("search", ["search"]), ("inspect", ["search", "inspect"]), ("preview", ["search", "inspect", "preview"]),
                                  ("", ["search", "inspect", "preview", "send"])):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                work = Path(directory)
                cli = work / "protonmailcli"
                cli.write_text(stub)
                cli.chmod(0o755)
                environment_change = """export XDG_CONFIG_HOME=/other/config XDG_DATA_HOME=/other/data PMAIL_PROFILE=other
export BRIDGE_CONFIG=/other/config.toml BRIDGE_STATE=/other/state.json BRIDGE_PROFILE=other
export BRIDGE_ACCOUNT=other@example.invalid"""
                result = subprocess.run(["bash", "-c", "draft_id=stale\n" + steps[0] + "\n" + environment_change + "\n" + steps[1]], cwd=work,
                                        env=dict(os.environ, FAIL_STEP=failure, SELECTED=selected, BRIDGE_ACCOUNT="test@example.invalid",
                                                 BRIDGE_CONFIG=str(work / "initial config.toml"), BRIDGE_STATE=str(work / "initial state.json"),
                                                 BRIDGE_PROFILE="reviewed", XDG_CONFIG_HOME=str(work), XDG_DATA_HOME=str(work), PMAIL_PROFILE="reviewed"),
                                        capture_output=True, text=True, timeout=15)
                self.assertEqual((work / "calls").read_text().splitlines(), expected)
                self.assertEqual(result.returncode == 0, not failure, result.stderr)


class ReadmeBatchRecipeTests(unittest.TestCase):
    def test_command_keys_are_documented_for_matching_bridge_batches(self) -> None:
        readme = (ROOT / "README.md").read_text().split("### 7. Native batch operations", 1)[1].split("\n## ", 1)[0]
        for text in (readme, (ROOT / "docs/agent-manifest-schemas.md").read_text()):
            text = " ".join(text.replace("`", "").split())
            self.assertIn("Bridge mode", text)
            self.assertIn("idempotency_conflict", text)
            self.assertIn("Local-state", text)

    def test_batch_retry_guidance_covers_missing_item_receipts(self) -> None:
        readme = (ROOT / "README.md").read_text().split("### 7. Native batch operations", 1)[1].split("\n## ", 1)[0]
        for text in (readme, (ROOT / "docs/agent-manifest-schemas.md").read_text()):
            text = " ".join(text.replace("`", "").split())
            self.assertIn("original item keys", text)
            self.assertIn("confirmed unsent", text)

    def test_send_retry_guides_distinguish_undispatched_and_pending_rows(self) -> None:
        readme = (ROOT / "README.md").read_text().split("### 7. Native batch operations", 1)[1].split("\n### ", 1)[0]
        guide = (ROOT / "docs/agent-manifest-schemas.md").read_text().split("## Recommended agent workflow", 1)[1]
        for name, text in (("README batch operations", readme), ("manifest workflow", guide)):
            with self.subTest(document=name):
                text = " ".join(text.replace("`", "").split())
                self.assertRegex(text, r"(?i)undispatched.{0,110}original item key")
                self.assertRegex(text, r"(?i)pending.{0,250}confirm(?:ing|ed)? non-delivery.{0,75}fresh item key")

    def test_retry_guides_identify_the_original_bridge_account(self) -> None:
        readme = (ROOT / "README.md").read_text().split("### 2. Batch draft ingestion from a manifest", 1)[1].split("\n### ", 1)[0]
        guide = (ROOT / "docs/agent-manifest-schemas.md").read_text().split("## Recommended agent workflow", 1)[1]
        actions = ("bridge account list", "auth status", "draft create-many")
        native = (ROOT / "README.md").read_text().split("### 7. Native batch operations", 1)[1].split("\n### ", 1)[0]
        for text, expected_actions in ((readme, actions), (guide, (*actions, "message send-many")),
                                       (native, ("draft create-many", "message send-many"))):
            commands = re.findall(r"(?:\./)?protonmailcli [^`\n]+", text)
            for action in expected_actions:
                matching = [cmd for cmd in commands if action in cmd]
                self.assertTrue(matching, action)
                for command in matching:
                    globals_ = shlex.split(command.split(action)[0])
                    expected = [("--config", "BATCH_CONFIG"), ("--state", "BATCH_STATE"), ("--profile", "BATCH_PROFILE")]
                    if action.endswith("-many"):
                        expected.append(("--expect-account", "BATCH_ACCOUNT"))
                    for flag, var in expected:
                        self.assertIn(flag, globals_, command)
                        self.assertEqual(globals_[globals_.index(flag) + 1], "${" + var + ":?}", command)

    def test_corrected_row_retry_uses_item_keys_alone(self) -> None:
        readme = (ROOT / "README.md").read_text()
        recipe = readme.split("### 2. Batch draft ingestion from a manifest\n", 1)[1].split("\n### ", 1)[0]
        guide = (ROOT / "docs/agent-manifest-schemas.md").read_text()
        for name, text in (("README batch recipe", recipe), ("manifest guide", guide)):
            with self.subTest(document=name):
                text = " ".join(text.replace("`", "").split())
                self.assertIn("item-level", text)
                self.assertRegex(text, r"(?i)omit (?:the )?command-level --idempotency-key",
                                 "a command-level receipt replays before corrected rows are processed")
                self.assertRegex(text, r"command-level key caches .*partial",
                                 "explain why the complete batch key cannot resume repaired rows")
        for command in re.findall(r"```bash\n(.*?)\n```", recipe, flags=re.DOTALL):
            self.assertNotIn("--idempotency-key", shlex.split(command))

    def test_fallback_recovery_checks_inbox_and_drafts(self) -> None:
        readme = (ROOT / "README.md").read_text()
        recovery = readme.split("### IMAP draft identity and diagnostics\n", 1)[1].split("\nCreate draft:", 1)[0]
        recipe = readme.split("### 2. Batch draft ingestion from a manifest\n", 1)[1].split("\n### ", 1)[0]
        for name, text in (
            ("README recovery", recovery),
            ("README batch recipe", recipe),
            ("manifest guide", (ROOT / "docs/agent-manifest-schemas.md").read_text()),
            ("IMAP workflow guide", (ROOT / "docs/imap-workflows.md").read_text()),
        ):
            with self.subTest(document=name):
                text = " ".join(text.replace("`", "").split())
                self.assertRegex(text, r"(?i)(?:inspect|check) (?:both )?INBOX and Drafts",
                                 "SMTP fallback may remain in INBOX when its move fails")
                self.assertIn("SMTP fallback", text)

    def test_recipe_preserves_recipients_and_body_files(self) -> None:
        readme = (ROOT / "README.md").read_text()
        section = readme.split("### 2. Batch draft ingestion from a manifest\n", 1)[1].split("\n### ", 1)[0]
        self.assertIn("](#imap-draft-identity-and-diagnostics)", section,
                      "partial IMAP batches need the procedure for reconciling dispatched items")
        recipes = re.findall(r"```bash\n(.*?)\n```", section, flags=re.DOTALL)
        self.assertEqual(len(recipes), 2, "expected a preview and an executable batch recipe")
        preview, command = map(shlex.split, recipes)
        self.assertEqual(preview[0], "./protonmailcli")
        self.assertEqual(command[0], "./protonmailcli")
        self.assertIn("--dry-run", preview)
        self.assertNotIn("--dry-run", command)
        manifest = re.search(r"```json\n(.*?)\n```", section, re.DOTALL)
        self.assertIsNotNone(manifest)

        with tempfile.TemporaryDirectory(prefix="proton-readme-batch-") as directory:
            work = Path(directory)
            binary = work / "protonmailcli"
            subprocess.run(
                ["go", "build", "-o", str(binary), "./cmd/protonmailcli"],
                cwd=ROOT, check=True, timeout=120,
            )
            env = {key: value for key, value in os.environ.items() if not key.startswith("PMAIL_")}
            env.update(XDG_CONFIG_HOME=str(work / "config"), XDG_DATA_HOME=str(work / "data"),
                       PMAIL_USE_LOCAL_STATE="1", PATH=str(work) + os.pathsep + env["PATH"],
                       BATCH_CONFIG=str(work / "batch's config.toml"), BATCH_STATE=str(work / "batch state.json"),
                       BATCH_PROFILE="batch profile", BATCH_ACCOUNT="selected@example.invalid")
            context = ["--config", env["BATCH_CONFIG"], "--state", env["BATCH_STATE"], "--profile", env["BATCH_PROFILE"]]

            def recipe(line, expected_exit=0):
                result = subprocess.run(["bash", "-c", line], cwd=work, env=env, capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, expected_exit, result.stdout + result.stderr)
                envelope = json.loads(result.stdout)
                self.assertEqual(envelope["meta"]["profile"], env["BATCH_PROFILE"], line)
                return envelope["data"]

            def cli(*args, expected_exit=0):
                result = subprocess.run([str(binary), *args], cwd=work, env=env,
                                        capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, expected_exit, result.stdout + result.stderr)
                return json.loads(result.stdout)["data"]

            cli("--json", "--no-input", "setup", "--non-interactive", "--username", "other@example.invalid")
            cli("--json", "bridge", "account", "use", "--username", "other@example.invalid")
            cli("--json", *context, "--no-input", "setup", "--non-interactive", "--username", "author@example.invalid")
            cli("--json", *context, "bridge", "account", "use", "--username", "selected@example.invalid")
            guide = (ROOT / "docs/agent-manifest-schemas.md").read_text()
            for source in (section, guide):
                for action, field, expected in (("bridge account list", "active", "selected@example.invalid"),
                                                ("auth status", "username", "author@example.invalid")):
                    line = re.search(r"`((?:\./)?protonmailcli [^`\n]*" + action + r")`", source)
                    self.assertIsNotNone(line, action)
                    self.assertEqual(recipe(line[1])[field], expected, line[1])
            (work / "drafts").mkdir()
            (work / "drafts/a.md").write_text("Body A from a file.\n")
            (work / "drafts/b.md").write_text("Body B from a file.\n")
            (work / "drafts.json").write_text(manifest[1])
            for action in (("draft", "create-many"), ("message", "send-many")):
                result = subprocess.run([str(binary), "--json", "--dry-run", *action, "--file", "drafts.json",
                                         "--idempotency-key", "whole"], cwd=work, env=env,
                                        capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, 2)
                self.assertIn("idempotency-key", result.stdout + result.stderr)
            # The guide's local simulation uses the same manifest with the Bridge-only guard omitted.
            guard = ' --expect-account "${BATCH_ACCOUNT:?}"'
            self.assertTrue(all(guard in line for line in recipes))
            recipes = [line.replace(guard, "") for line in recipes]
            planned = recipe(recipes[0])
            self.assertEqual(planned["success"], 2)
            self.assertEqual(cli("--json", *context, "draft", "list")["count"], 0)
            (work / "drafts/b.md").unlink()
            partial = recipe(recipes[1], expected_exit=10)
            self.assertEqual((partial["success"], partial["failed"]), (1, 1))
            (work / "drafts/b.md").write_text("Body B from a file.\n")
            recipe(recipes[1])
            drafts = cli("--json", *context, "draft", "list")["drafts"]
            self.assertEqual(len(drafts), 2)
            self.assertEqual({draft["subject"]: (draft["to"], draft["body"]) for draft in drafts}, {
                "Hello A": (["a@example.com", "second@example.com"], "Body A from a file.\n"),
                "Hello B": (["b@example.com"], "Body B from a file.\n"),
            })


if __name__ == "__main__":
    unittest.main(verbosity=2)
