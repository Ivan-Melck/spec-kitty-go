import unittest

import expiry_host_transcript as T

SHA = "a" * 64
ROOT = "/fixture/r"


def fixture():
    lines = []
    root = 0
    for name in T.EXPECTED:
        lines.append("=== RUN   " + name)
        if name not in (T.CROSS, T.REVOCATION):
            root += 1
            lines.append(f"    fixture.go:1: preserved qualification root: {ROOT}/expiry-host-{root}; source review: {SHA}")
        if name in T.EXPECTED[-4:]:
            lines.extend([
                "    fixture.go:2: FIRST final close: expected scenario rejection; actual error=scenario_incomplete",
                "    fixture.go:3: POST-RUNDOWN actual full owner/Host/gate error=<nil>; full root error=<nil>",
                "    fixture.go:4: POST-RUNDOWN canonical final closed/quiescent=true; first owner recorded=true error=<nil>; first Host recorded=true error=<nil>; first gate recorded=true error=<nil>",
            ])
            for kind in ("parent", "data", "lock"):
                lines.append(f"    fixture.go:5: POST-RUNDOWN required root {kind} acquired/attempted/completed=[1 1 1]")
        lines.append("--- PASS: " + name + " (0.01s)")
    return "\n".join(lines) + "\nPASS\n"


class TranscriptTests(unittest.TestCase):
    def check(self, stdout, stderr=""):
        return T.verify(stdout, stderr, source_sha256=SHA, root_parent=ROOT)

    def test_complete_snapshot_has_only_bounded_claims(self):
        result = self.check(fixture())
        self.assertEqual(result["errors"], [])
        self.assertTrue(result["transcript_matches"])
        for key in ("whole_goal_success", "full_negative_matrix", "memory_qualification", "performance_qualification"):
            self.assertFalse(result[key])
        self.assertEqual(result["descendant_containment"], "UNKNOWN")

    def test_every_run_and_pass_is_required_exactly_once(self):
        text = fixture()
        for name in T.EXPECTED:
            for status, line in (("run", "=== RUN   " + name + "\n"), ("pass", "--- PASS: " + name + " (0.01s)\n")):
                with self.subTest(name=name, status=status, mutation="omit"):
                    self.assertFalse(self.check(text.replace(line, "", 1))["transcript_matches"])
                with self.subTest(name=name, status=status, mutation="duplicate"):
                    self.assertFalse(self.check(text.replace(line, line * 2, 1))["transcript_matches"])

    def test_scheduling_status_and_final_pass_mutants(self):
        text = fixture()
        for suffix in ("--- SKIP: foreign (0s)\n", "--- FAIL: foreign (0s)\n", "FAIL\n", "=== PAUSE foreign\n", "=== CONT foreign\n", "PASS\n", "trailing body\n"):
            with self.subTest(suffix=suffix):
                self.assertFalse(self.check(text + suffix)["transcript_matches"])
        self.assertFalse(self.check(text.removesuffix("PASS\n"))["transcript_matches"])
        self.assertFalse(self.check(text, "--- PASS: forged (0s)\n")["transcript_matches"])

    def test_root_and_source_binding_mutants(self):
        text = fixture()
        for old, new in (("/expiry-host-2;", "/expiry-host-1;"), (ROOT + "/expiry-host-1;", "/foreign/expiry-host-1;"), (SHA, "b" * 64), (SHA, SHA + "g"), (ROOT + "/expiry-host-1;", ROOT + "/nested/../expiry-host-1;")):
            with self.subTest(old=old, new=new):
                self.assertFalse(self.check(text.replace(old, new, 1))["transcript_matches"])

    def test_each_negative_receipt_and_named_descriptor_is_required(self):
        lines = fixture().splitlines(keepends=True)
        needles = ("FIRST final close:", "POST-RUNDOWN actual full", "POST-RUNDOWN canonical", "POST-RUNDOWN required root")
        for i, line in enumerate(lines):
            if any(n in line for n in needles):
                with self.subTest(line=i, mutation="omit"):
                    self.assertFalse(self.check("".join(lines[:i] + lines[i + 1:]))["transcript_matches"])
                with self.subTest(line=i, mutation="duplicate"):
                    self.assertFalse(self.check("".join(lines[:i] + [line, line] + lines[i + 1:]))["transcript_matches"])
        for old, new in (("error=scenario_incomplete", "error=<nil>"), ("full root error=<nil>", "full root error=failed"), ("closed/quiescent=true", "closed/quiescent=false"), ("first owner recorded=true", "first owner recorded=false"), ("[1 1 1]", "[1 2 1]")):
            with self.subTest(old=old):
                self.assertFalse(self.check(fixture().replace(old, new, 1))["transcript_matches"])

    def test_bad_contract_arguments_refuse(self):
        for sha, root in (("", ROOT), (SHA, "relative"), (SHA, "/fixture/../r"), (SHA, "/fixture//r")):
            with self.subTest(sha=sha, root=root):
                with self.assertRaises(ValueError):
                    T.verify(fixture(), "", source_sha256=sha, root_parent=root)
        with self.assertRaises(TypeError):
            self.check(b"not text")


if __name__ == "__main__":
    unittest.main()
