import hashlib
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import performance_rebind_audit as A


class RebindAuditTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        (self.repo / "internal").mkdir()
        (self.repo / "internal/a.go").write_bytes(b"package a\n")
        (self.repo / "go.mod").write_bytes(b"module fixture\n")
        self.baseline = {"source_members": ["internal/a.go", "go.mod"], "pins": {}}
        for member in self.baseline["source_members"]:
            raw = (self.repo / member).read_bytes()
            self.baseline["pins"][str(self.repo / member)] = {"bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}

    def test_equal_source_is_not_execution_or_semantic_permission(self):
        result = A.audit(self.repo, self.baseline)
        self.assertEqual(result["changed"], [])
        self.assertEqual(result["unchanged"], ["go.mod", "internal/a.go"])
        self.assertTrue(result["rebinding_required"])
        self.assertFalse(result["build_or_timing_authority"])
        self.assertFalse(result["ordinary_build_dependency_closure_proved"])
        self.assertFalse(result["source_semantic_equivalence_proved"])

    def test_changed_missing_added_are_distinct(self):
        (self.repo / "internal/a.go").write_bytes(b"package changed\n")
        (self.repo / "internal/new.go").write_bytes(b"package added\n")
        (self.repo / "go.mod").unlink()
        result = A.audit(self.repo, self.baseline)
        self.assertEqual([r["path"] for r in result["changed"]], ["internal/a.go"])
        self.assertEqual(result["missing"], ["go.mod"])
        self.assertEqual(result["added_go"], ["internal/new.go"])

    def test_external_pins_and_historical_roots_are_not_opened(self):
        self.baseline["pins"]["/historical/ROOTS/report.json"] = {"sha256": "not-read"}
        real = A.read_regular
        opened = []
        def read(path):
            opened.append(path)
            return real(path)
        with patch.object(A, "read_regular", side_effect=read):
            A.audit(self.repo, self.baseline)
        self.assertEqual(set(opened), {self.repo / "internal/a.go", self.repo / "go.mod"})

    def test_non_source_and_noncanonical_members_refused(self):
        for member in ("../ROOTS/report.go", "/absolute/source.go", "internal/../ROOTS.go", "internal//a.go", "ROOTS/report.json", "internal/arbitrary.json"):
            with self.subTest(member=member), self.assertRaises(ValueError):
                A.source_path(self.repo, member)

    def test_symlink_source_and_ancestry_refused(self):
        (self.repo / "internal/a.go").unlink()
        os.symlink(self.repo / "go.mod", self.repo / "internal/a.go")
        with self.assertRaises(ValueError):
            A.audit(self.repo, self.baseline)
        (self.repo / "internal/a.go").unlink()
        os.symlink(self.repo, self.repo / "internal/link")
        with self.assertRaises(ValueError):
            A.source_path(self.repo, "internal/link/a.go")

    def test_read_and_first_close_failures_preserve_both_without_retry(self):
        original_read = OSError("original-read")
        original_close = OSError("original-first-close")
        close = os.close
        calls = []
        def failing_close(fd):
            calls.append(fd)
            close(fd)
            raise original_close
        with patch.object(A.os, "read", side_effect=original_read), patch.object(A.os, "close", side_effect=failing_close):
            with self.assertRaises(BaseExceptionGroup) as caught:
                A.read_regular(self.repo / "go.mod")
        self.assertEqual(caught.exception.exceptions, (original_read, original_close))
        self.assertEqual(len(calls), 1)

    def test_bounded_regular_source_required(self):
        path = self.repo / "oversized.go"
        with path.open("wb") as f:
            f.truncate((16 << 20) + 1)
        with self.assertRaises(BaseExceptionGroup):
            A.read_regular(path)

    def test_duplicate_members_refused(self):
        self.baseline["source_members"].append("go.mod")
        with self.assertRaises(ValueError):
            A.audit(self.repo, self.baseline)


if __name__ == "__main__":
    unittest.main()
