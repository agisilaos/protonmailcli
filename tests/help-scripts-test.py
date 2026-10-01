#!/usr/bin/env python3
"""Prove help capture/isolation and docs failures using disposable fixtures."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
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


if __name__ == "__main__":
    unittest.main(verbosity=2)
