import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

SPEC = importlib.util.spec_from_file_location("expiry_evidence_build", Path(__file__).with_name("build.py"))
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)


class PreparationTests(unittest.TestCase):
    def fixture(self, root):
        source = root / "original"
        (source / "internal/engine").mkdir(parents=True)
        raw = ("package engine\nfunc f() {\n" + build.NEEDLE + "\n}\n").encode()
        (source / "internal/engine/expiry.go").write_bytes(raw)
        (source / "go.mod").write_text("module " + build.MODULE + "\n")
        archive = root / "module.zip"
        with zipfile.ZipFile(archive, "w") as z:
            for path in source.rglob("*"):
                if path.is_file():
                    z.writestr(f"{build.MODULE}@{build.VERSION}/{path.relative_to(source)}", path.read_bytes())
        return source, archive, raw

    def test_exact_delta_and_original_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, archive, raw = self.fixture(root)
            before = build.inventory(source)
            with patch.object(build, "EXPIRY_HASH", build.sha(raw)), patch.object(build, "MODULE_SUM", build.module_zip_sum(archive)):
                copied, record = build.prepare(source, root / "new", archive)
            self.assertEqual(build.inventory(source), before)
            self.assertEqual(record["changed"], ["internal/engine/expiry.go"])
            self.assertEqual(len(record["added"]), 3)
            self.assertEqual((copied / "internal/engine/expiry.go").read_text().count("spkExpiryEvidenceHold()"), 1)
            self.assertFalse(record["qualification_complete"])
            self.assertFalse(record["adopted"])

    def test_pinned_source_mismatch(self):
        with self.assertRaises(ValueError):
            build.patch_expiry(b"package engine\n")

    def test_existing_output_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, archive, _ = self.fixture(root)
            output = root / "existing"
            output.mkdir()
            sentinel = output / "keep"
            sentinel.write_bytes(b"unchanged")
            with self.assertRaises(FileExistsError):
                build.prepare(source, output, archive)
            self.assertEqual(sentinel.read_bytes(), b"unchanged")

    def test_source_mismatch_refused_before_copy(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, archive, _ = self.fixture(root)
            (source / "go.mod").write_text("changed")
            with patch.object(build, "MODULE_SUM", build.module_zip_sum(archive)), self.assertRaises(ValueError):
                build.prepare(source, root / "new", archive)
            self.assertFalse((root / "new").exists())

    def test_zip_sum_mismatch_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, archive, _ = self.fixture(root)
            with self.assertRaisesRegex(ValueError, "pinned go.sum"):
                build.prepare(source, root / "new", archive)
            self.assertFalse((root / "new").exists())

    def test_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, archive, _ = self.fixture(root)
            (source / "alias").symlink_to(source / "go.mod")
            with self.assertRaises(ValueError):
                build.prepare(source, root / "new", archive)
            self.assertFalse((root / "new").exists())


if __name__ == "__main__":
    unittest.main()
