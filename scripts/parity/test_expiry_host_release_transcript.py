import unittest

import expiry_host_transcript as T
from test_expiry_host_transcript import fixture as original_fixture

SHA = "a" * 64
ROOT = "/fixture/r"


def fixture():
    lines = ["=== RUN   " + T.RELEASE_CONTROLS]
    for i, name in enumerate(T.RELEASE_EXPECTED[1:]):
        mode, acks = name.split("/")[-2:]
        lines.extend([
            "=== RUN   " + name,
            f"    fixture.go:1: preserved qualification root: {ROOT}/expiry-host-{i}; source review: {SHA}",
            f"    fixture.go:2: PASSIVE expiry advance: mode={mode}; before=10; after=11; publication={7 if acks == 'actual_ACK' else 0}; private=6; parent_closed_before_advance={'true' if mode == 'EOF' else 'false'}; explicitRA=false",
            "    fixture.go:3: FIRST final close: expected scenario rejection; actual error=scenario_incomplete",
            "    fixture.go:4: POST-RUNDOWN actual full owner/Host/gate error=<nil>; full root error=<nil>",
            "    fixture.go:5: POST-RUNDOWN canonical final closed/quiescent=true; first owner recorded=true error=<nil>; first Host recorded=true error=<nil>; first gate recorded=true error=<nil>",
        ])
        for kind in ("parent", "data", "lock"):
            lines.append(f"    fixture.go:6: POST-RUNDOWN required root {kind} acquired/attempted/completed=[1 1 1]")
        lines.append("--- PASS: " + name + " (0.01s)")
    lines.extend(["--- PASS: " + T.RELEASE_CONTROLS + " (0.01s)", "PASS"])
    return "\n".join(lines) + "\n"


class ReleaseTranscriptTests(unittest.TestCase):
    def check(self, text):
        return T.verify_release_controls(text, "", source_sha256=SHA, root_parent=ROOT)

    def test_separate_contract_matches_with_no_RA_or_whole_goal_claim(self):
        result = self.check(fixture())
        self.assertEqual(result["errors"], [])
        self.assertTrue(result["transcript_matches"])
        self.assertFalse(result["explicit_RA_qualification"])
        self.assertFalse(result["whole_goal_success"])
        self.assertFalse(result["full_negative_matrix"])
        self.assertTrue(all(result["passive_expiry_receipts"].values()))

    def test_old_and_new_contracts_cannot_substitute_for_each_other(self):
        self.assertFalse(self.check(original_fixture())["transcript_matches"])
        result = T.verify(fixture(), "", source_sha256=SHA, root_parent=ROOT)
        self.assertFalse(result["transcript_matches"])
        self.assertEqual(len(T.EXPECTED), 13)
        self.assertEqual(len(T.RELEASE_EXPECTED), 5)

    def test_every_run_and_pass_required_exactly_once(self):
        text = fixture()
        for name in T.RELEASE_EXPECTED:
            for line in ("=== RUN   " + name + "\n", "--- PASS: " + name + " (0.01s)\n"):
                with self.subTest(name=name, line=line, mutation="omit"):
                    self.assertFalse(self.check(text.replace(line, "", 1))["transcript_matches"])
                with self.subTest(name=name, line=line, mutation="duplicate"):
                    self.assertFalse(self.check(text.replace(line, line * 2, 1))["transcript_matches"])

    def test_every_passive_and_cleanup_receipt_is_required(self):
        lines = fixture().splitlines(keepends=True)
        for i, line in enumerate(lines):
            if any(marker in line for marker in ("PASSIVE expiry advance:", "FIRST final close:", "POST-RUNDOWN")):
                with self.subTest(line=i, mutation="omit"):
                    self.assertFalse(self.check("".join(lines[:i] + lines[i + 1:]))["transcript_matches"])
                with self.subTest(line=i, mutation="duplicate"):
                    self.assertFalse(self.check("".join(lines[:i] + [line, line] + lines[i + 1:]))["transcript_matches"])

    def test_passive_control_binding_and_unsigned_revision_mutants(self):
        text = fixture()
        for old, new in (
            ("mode=EOF;", "mode=daemon_timeout;"),
            ("before=10;", "before=0;"),
            ("after=11;", "after=10;"),
            ("after=11;", "after=18446744073709551616;"),
            ("after=11;", "after=" + "9" * 5000 + ";"),
            ("publication=0;", "publication=7;"),
            ("private=6;", "private=9;"),
            ("parent_closed_before_advance=true;", "parent_closed_before_advance=false;"),
            ("parent_closed_before_advance=false;", "parent_closed_before_advance=true;"),
            ("explicitRA=false", "explicitRA=true"),
        ):
            with self.subTest(old=old, new=new[:80]):
                self.assertFalse(self.check(text.replace(old, new, 1))["transcript_matches"])

    def test_four_exact_source_bound_roots_required(self):
        text = fixture()
        for old, new in (("expiry-host-1;", "expiry-host-0;"), (SHA, "b" * 64), (ROOT + "/expiry-host-0;", "/foreign/expiry-host-0;")):
            with self.subTest(old=old):
                self.assertFalse(self.check(text.replace(old, new, 1))["transcript_matches"])


if __name__ == "__main__":
    unittest.main()
