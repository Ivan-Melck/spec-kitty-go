"""Tests for the read-only, commit-bound inventory tool (standard library only)."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import capture_upstream as inventory


class CaptureTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.repo = self.root / "reference"
        self.repo.mkdir()
        self.run_git("init", "-q")
        self.run_git("config", "user.email", "inventory@test.invalid")
        self.run_git("config", "user.name", "Inventory fixture")
        for path in inventory.SOURCE_PATHS:
            file = self.repo / path
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text("# source-bound fixture\n", encoding="utf-8")
        self.envelope = self.repo / "src/specify_cli/orchestrator_api/envelope.py"
        # Upstream code must never execute as a side effect of inventory.
        self.envelope.write_text(
            'raise RuntimeError("must not execute")\n'
            'CONTRACT_VERSION = "1.15.0"\nMIN_PROVIDER_VERSION = "0.1.0"\n',
            encoding="utf-8",
        )
        (self.repo / "src/specify_cli/core/upstream_contract.json").write_text(json.dumps({
            "orchestrator_api": {"allowed_commands": ["specify", "next"],
                                 "forbidden_commands": ["feature-state"],
                                 "allowed_error_codes": ["USAGE_ERROR", "INVALID_MISSION"]},
        }), encoding="utf-8")
        (self.repo / "src/specify_cli/_completion_manifest.json").write_text(json.dumps({
            "commands": {"next": {"hidden": False, "deprecated": False},
                         "merge": {"hidden": True, "deprecated": True}},
        }), encoding="utf-8")
        self.run_git("add", ".")
        self.run_git("commit", "-q", "-m", "immutable inventory fixture")
        self.revision = self.run_git("rev-parse", "HEAD").decode().strip()

    def run_git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args], check=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout

    def test_capture_is_deterministic_and_leaves_reference_unchanged(self):
        first = inventory.capture(self.repo, self.revision)
        second = inventory.capture(self.repo, self.revision)
        self.assertEqual(first, second)
        self.assertEqual(first["orchestrator_api"]["contract_version"], "1.15.0")
        self.assertEqual(first["orchestrator_api"]["allowed_commands"], ["next", "specify"])
        self.assertEqual(len(first["source_sha256"]), len(inventory.SOURCE_PATHS))
        self.assertEqual(self.run_git("status", "--porcelain"), b"")
        self.assertEqual(self.run_git("rev-parse", "HEAD").decode().strip(), self.revision)

    def test_dirty_checkout_cannot_change_committed_baseline(self):
        original = inventory.capture(self.repo, self.revision)
        self.envelope.write_text('CONTRACT_VERSION = "999.0.0"\n', encoding="utf-8")
        self.assertEqual(inventory.capture(self.repo, self.revision), original)
        self.assertIn(b"envelope.py", self.run_git("status", "--porcelain"))

    def test_mutable_and_abbreviated_revisions_are_refused(self):
        for revision in ("main", "HEAD", "latest", self.revision[:8]):
            with self.subTest(revision=revision), self.assertRaises(ValueError):
                inventory.capture(self.repo, revision)

    def test_check_refuses_drift_without_rewriting_evidence(self):
        output = self.root / "snapshot.json"
        args = ["--upstream", str(self.repo), "--revision", self.revision,
                "--output", str(output)]
        self.assertEqual(inventory.main(args), 0)
        original = output.read_bytes()
        self.assertEqual(inventory.main(args + ["--check"]), 0)
        output.write_bytes(original + b" ")
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(inventory.main(args + ["--check"]), 1)
        self.assertEqual(output.read_bytes(), original + b" ")
        self.assertEqual(self.run_git("status", "--porcelain"), b"")

    def test_missing_source_is_not_reported_as_empty_coverage(self):
        self.run_git("rm", "src/specify_cli/orchestrator_api/runtime_next.py")
        self.run_git("commit", "-q", "-m", "remove mandatory source")
        revision = self.run_git("rev-parse", "HEAD").decode().strip()
        with self.assertRaises(subprocess.CalledProcessError):
            inventory.capture(self.repo, revision)

    def test_git_replacements_cannot_change_the_pinned_commit(self):
        original = inventory.capture(self.repo, self.revision)
        self.envelope.write_text('CONTRACT_VERSION = "999.0.0"\nMIN_PROVIDER_VERSION = "0.1.0"\n', encoding="utf-8")
        self.run_git("add", ".")
        self.run_git("commit", "-q", "-m", "replacement fixture")
        replacement = self.run_git("rev-parse", "HEAD").decode().strip()
        self.run_git("replace", self.revision, replacement)
        self.assertEqual(inventory.capture(self.repo, self.revision), original)

    def test_output_cannot_modify_reference_or_git_directories(self):
        source = self.envelope.read_bytes()
        outside_alias = self.root / "source-alias"
        outside_alias.symlink_to(self.envelope)
        for output in (self.envelope, self.repo / ".git" / "config", outside_alias):
            with self.subTest(output=output):
                before = output.read_bytes()
                with contextlib.redirect_stderr(io.StringIO()):
                    code = inventory.main(["--upstream", str(self.repo), "--revision", self.revision,
                                           "--output", str(output)])
                self.assertEqual(code, 2)
                self.assertEqual(output.read_bytes(), before)
        self.assertEqual(self.envelope.read_bytes(), source)
        self.assertEqual(self.run_git("status", "--porcelain"), b"")

    def test_output_hardlink_cannot_truncate_reference_source(self):
        output = self.root / "hardlinked-snapshot"
        output.hardlink_to(self.envelope)
        original = self.envelope.read_bytes()
        self.assertEqual(inventory.main(["--upstream", str(self.repo), "--revision", self.revision,
                                         "--output", str(output)]), 0)
        self.assertEqual(self.envelope.read_bytes(), original)
        self.assertNotEqual(output.read_bytes(), original)
        self.assertEqual(self.run_git("status", "--porcelain"), b"")

    def test_dynamic_version_is_refused_not_executed(self):
        with self.assertRaises(ValueError):
            inventory.literal_constant('CONTRACT_VERSION = compute_version()\n', "CONTRACT_VERSION")


if __name__ == "__main__":
    unittest.main()
