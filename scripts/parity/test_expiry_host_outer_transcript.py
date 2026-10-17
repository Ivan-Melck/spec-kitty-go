"""Synthetic fixtures ONLY: not actual native/provider execution evidence."""
import unittest

import expiry_host_outer_transcript as T
import expiry_host_transcript as OLD
from test_expiry_host_transcript import fixture as old_fixture
from test_expiry_host_release_transcript import fixture as release_fixture

SHA, OUTER_SHA, HELD_SHA = "a" * 64, "b" * 64, "c" * 64
ROOT = "/synthetic-only/r"
SOURCE = "/synthetic-only/repo" + T.SOURCE_SUFFIX


def case_rows(mode, index):
    """Invented scalar/log values in actual source format; never native data."""
    directory = f"{ROOT}/expiry-host-{index}/runtime/owned-synthetic"
    original, parked = directory + "/gapdb.sock", directory + "/expiry-outer-original.sock"
    rows = [
        f"preserved qualification root: {ROOT}/expiry-host-{index}; source review: {SHA}",
        f"conservative held cutoff: existing_budget=12s sidecar_source_sha256={HELD_SHA} start_before_WaitHeld=true",
        f"frontierreached: mode={mode} function={T.FUNCTION} source={SOURCE} line=195 sha256={OUTER_SHA}",
    ]
    if mode in ("directHostClose", "directLifecycleStop"):
        method = "Host.Close" if mode == "directHostClose" else "Lifecycle.Stop"
        rows.append(f"FIRST direct {method} return: <nil>")
    if mode == "endpointSubstitution":
        rows.append(f"actual endpoint substitution: original={original} parked={parked} "
                    "original_identity={device:4 inode:101 mode:49600 uid:1000 gid:1000} "
                    "synthetic_identity={device:4 inode:102 mode:49600 uid:1000 gid:1000} "
                    "PID=42 start_ticks=77 error=<nil>; no Accept/respond/RPC/signals")
    rows.append(f"actioncompleted: mode={mode} error=<nil> before_cutoff=true remaining=8.9s (before resume)")
    worker_error = {"unchanged": "<nil>", "callerCancel": "outer refused\ncontext canceled"}.get(mode, "outer refused")
    rows.append(f"workerjoined: mode={mode} error={worker_error} before_cutoff=true remaining=8.8s")
    if mode == "unchanged":
        rows.append("actual outer unchanged expiry: held_revision=7; expired_revision=8; private=6; publication=0; explicitRA=true")
    if mode == "endpointSubstitution":
        rows.extend([
            "originalalive after workerjoined: PID=42 start_ticks=77 caller_alive=true",
            f"endpoint fixture restoration: original={original} parked={parked} restored=true "
            "FIRST listener.Close=<nil> unlink=<nil> rename=<nil> directory.Close=<nil> "
            "outcome=<nil>; residual paths preserved on failure",
            "original socket restored before FIRST teardown: stickyflags=true no_prior_primary_Close=true caller_alive=true",
        ])
    rows.extend([
        "FIRST final close: expected scenario success=" + ("true; actual error=<nil>" if mode == "unchanged" else
                                                         "false; actual error=expiry_scenario_incomplete"),
        "POST-RUNDOWN actual full owner/Host/gate error=<nil>; full root error=<nil>",
        "POST-RUNDOWN canonical final closed/quiescent=true; first owner recorded=true error=<nil>; "
        "first Host recorded=true error=<nil>; first gate recorded=true error=<nil>",
    ])
    rows.extend(f"POST-RUNDOWN required root {kind} acquired/attempted/completed=[1 1 1]"
                for kind in ("parent", "data", "lock"))
    return rows


def fixture(change=None):
    lines = ["=== RUN   " + T.OUTER]
    for index, mode in enumerate(T.MODES, 1):
        rows = case_rows(mode, index)
        if change:
            rows = change(mode, rows)
        lines.append(f"=== RUN   {T.OUTER}/{mode}")
        for row in rows:
            # Go t.Logf prefixes only the first line of a multiline joined error.
            lines.append("    synthetic_fixture.go:1: " + row.replace("\n", "\n        "))
    # Synthetic, nonnative reproduction of testing.Main's buffered PASS order.
    lines.append(f"--- PASS: {T.OUTER} (0.05s)")
    lines.extend(f"    --- PASS: {T.OUTER}/{mode} (0.01s)" for mode in T.MODES)
    lines.append("PASS")
    return "\n".join(lines) + "\n"


class OuterTranscriptTests(unittest.TestCase):
    def check(self, text=None, stderr="", **overrides):
        arguments = dict(source_sha256=SHA, root_parent=ROOT, outer_source_sha256=OUTER_SHA,
                         held_source_sha256=HELD_SHA)
        arguments.update(overrides)
        return T.verify_outer_controls(fixture() if text is None else text, stderr, **arguments)

    def reject(self, text):
        self.assertFalse(self.check(text)["transcript_matches"])

    def test_synthetic_match_is_bounded_not_execution(self):
        result = self.check()
        self.assertEqual(result["errors"], [])
        self.assertTrue(result["transcript_matches"])
        self.assertEqual(len(result["outer_first_outcome_receipts"]), 5)
        self.assertTrue(all(result["outer_first_outcome_receipts"].values()))
        self.assertTrue(result["unchanged_explicit_RA_receipt"])
        self.assertTrue(result["explicit_RA_qualification"])
        self.assertEqual(result["negative_first_outcome_receipts"], {})
        for key in ("whole_goal_success", "whole_area_success", "full_negative_matrix",
                    "memory_qualification", "performance_qualification"):
            self.assertFalse(result[key])
        for key in ("actual_execution", "stream_finality", "descendant_containment",
                    "OS_containment", "precise_refusing_nested_outer_check"):
            self.assertIn("UNKNOWN", result[key])

    def test_every_receipt_omission_duplicate_and_adjacent_order(self):
        for index, mode in enumerate(T.MODES, 1):
            rows = case_rows(mode, index)
            for i in range(len(rows)):
                for mutation in ("omit", "duplicate", "swap"):
                    if mutation == "swap" and i + 1 == len(rows):
                        continue
                    changed = rows[:]
                    if mutation == "omit":
                        del changed[i]
                    elif mutation == "duplicate":
                        changed.insert(i, changed[i])
                    else:
                        changed[i], changed[i + 1] = changed[i + 1], changed[i]
                    with self.subTest(mode=mode, row=i, mutation=mutation):
                        self.reject(fixture(lambda m, r: changed if m == mode else r))

    def test_run_pass_census_and_topology(self):
        text = fixture()
        for name in T.EXPECTED:
            for line in ("=== RUN   " + name + "\n",
                         ("" if name == T.OUTER else "    ") + "--- PASS: " + name
                         + (" (0.05s)\n" if name == T.OUTER else " (0.01s)\n")):
                with self.subTest(name=name, line=line):
                    self.reject(text.replace(line, "", 1))
                    self.reject(text.replace(line, line * 2, 1))
        parent_pass = f"--- PASS: {T.OUTER} (0.05s)\n"
        self.reject(parent_pass + text.replace(parent_pass, ""))
        for suffix in ("PASS\n", "trailing\n", "--- FAIL: foreign (0s)\n", "--- SKIP: foreign (0s)\n",
                       "FAIL\n", "=== PAUSE foreign\n", "=== CONT foreign\n", "=== RUN   foreign\n"):
            self.reject(text + suffix)
        self.reject(text.removesuffix("PASS\n"))
        self.assertFalse(self.check(text, "--- PASS: forged (0s)\n")["transcript_matches"])

    def test_synthetic_old_interleaved_topology_rejected(self):
        parent_pass = f"--- PASS: {T.OUTER} (0.05s)\n"
        text = fixture().split(parent_pass)[0]
        for name, following in zip(T.EXPECTED[1:], T.EXPECTED[2:]):
            marker = "=== RUN   " + following + "\n"
            text = text.replace(marker, f"    --- PASS: {name} (0.01s)\n" + marker)
        text += f"    --- PASS: {T.EXPECTED[-1]} (0.01s)\n" + parent_pass + "PASS\n"
        result = self.check(text)
        self.assertEqual(result["errors"], ["outer sequential RUN/PASS topology mismatch"])
        self.assertTrue(all(result["outer_first_outcome_receipts"].values()))
        self.assertFalse(result["transcript_matches"])
        self.assertFalse(result["explicit_RA_qualification"])

    def test_synthetic_run_and_pass_order_mutations(self):
        text = fixture()
        for index in range(len(T.EXPECTED) - 1):
            for kind in ("RUN", "PASS"):
                first, second = T.EXPECTED[index:index + 2]
                if kind == "RUN":
                    a, b = "=== RUN   " + first + "\n", "=== RUN   " + second + "\n"
                else:
                    a = ("" if first == T.OUTER else "    ") + "--- PASS: " + first + (
                        " (0.05s)\n" if first == T.OUTER else " (0.01s)\n")
                    b = "    --- PASS: " + second + " (0.01s)\n"
                with self.subTest(kind=kind, index=index):
                    changed = text.replace(a, "SYNTHETIC_SWAP_MARKER\n").replace(b, a).replace(
                        "SYNTHETIC_SWAP_MARKER\n", b)
                    self.assertIn("outer sequential RUN/PASS topology mismatch", self.check(changed)["errors"])
                    self.reject(changed)
        # A child PASS cannot appear within an otherwise valid set of RUN blocks.
        child_pass = f"    --- PASS: {T.EXPECTED[1]} (0.01s)\n"
        marker = "=== RUN   " + T.EXPECTED[2] + "\n"
        self.reject(text.replace(child_pass, "").replace(marker, child_pass + marker))

    def test_synthetic_receipts_cannot_move_or_be_borrowed_across_cases(self):
        for source_index, source in enumerate(T.MODES, 1):
            for target in T.MODES:
                if source == target:
                    continue
                for row_index, row in enumerate(case_rows(source, source_index)):
                    for duplicate in (False, True):
                        def change(mode, rows):
                            if mode == source and not duplicate:
                                return rows[:row_index] + rows[row_index + 1:]
                            if mode == target:
                                return rows + [row]
                            return rows
                        with self.subTest(source=source, target=target, row=row_index, duplicate=duplicate):
                            result = self.check(fixture(change))
                            self.assertFalse(result["transcript_matches"])
                            self.assertFalse(result["outer_first_outcome_receipts"][f"{T.OUTER}/{target}"])
                            if not duplicate:
                                self.assertFalse(result["outer_first_outcome_receipts"][f"{T.OUTER}/{source}"])

    def test_receipts_cannot_escape_blocks_or_duplicate_into_stderr(self):
        text = fixture()
        for mode_index, mode in enumerate(T.MODES, 1):
            for receipt in case_rows(mode, mode_index):
                row = "    synthetic_fixture.go:1: " + receipt.replace("\n", "\n        ") + "\n"
                with self.subTest(mode=mode, receipt=receipt):
                    self.reject(row + text)
                    self.reject(text.replace("PASS\n", row + "PASS\n"))
                    self.assertFalse(self.check(text, row)["transcript_matches"])

    def test_roots_and_all_source_bindings(self):
        text = fixture()
        for old, new in (
            ("expiry-host-2;", "expiry-host-1;"), (ROOT + "/expiry-host-1;", "/foreign/expiry-host-1;"),
            (ROOT + "/expiry-host-1;", ROOT + "/nested/../expiry-host-1;"),
            (SHA, "d" * 64), (OUTER_SHA, "d" * 64), (HELD_SHA, "d" * 64),
            (SHA, SHA + "f"), ("line=195", "line=194"), (T.FUNCTION, T.FUNCTION + "Other"),
            (SOURCE, "relative" + T.SOURCE_SUFFIX), (SOURCE, SOURCE.replace("/repo/", "/repo/../repo/")),
            (SOURCE, SOURCE + ".wrong"), (SOURCE, SOURCE.replace("/repo/", "/other-repo/")),
            ("existing_budget=12s", "existing_budget=13s"),
            ("start_before_WaitHeld=true", "start_before_WaitHeld=false"),
        ):
            with self.subTest(old=old, new=new):
                self.reject(text.replace(old, new, 1))
        # One missing case root cannot be compensated by two observations elsewhere.
        self.reject(fixture(lambda m, r: r[:1] * 2 + r[1:] if m == "unchanged" else
                            r[1:] if m == "callerCancel" else r))

    def test_mode_cutoff_errors_and_direct_returns(self):
        for mode in T.MODES:
            for old, new in (
                ("frontierreached: mode=" + mode, "frontierreached: mode=wrong"),
                ("actioncompleted: mode=" + mode, "actioncompleted: mode=wrong"),
                ("workerjoined: mode=" + mode, "workerjoined: mode=wrong"),
                ("error=<nil> before_cutoff", "error=failed before_cutoff"),
                ("before_cutoff=true", "before_cutoff=false"), ("remaining=8.9s", "remaining=-1s"),
                ("remaining=8.8s", "remaining=0s"), (" (before resume)", " (after resume)"),
            ):
                with self.subTest(mode=mode, old=old):
                    self.reject(fixture(lambda m, r: [v.replace(old, new) for v in r] if m == mode else r))
        for old, new in (("Host.Close return: <nil>", "Host.Close return: broken"),
                         ("Lifecycle.Stop return: <nil>", "Lifecycle.Stop return: broken"),
                         ("error=<nil> before_cutoff=true remaining=8.8s", "error=failed before_cutoff=true remaining=8.8s"),
                         ("error=outer refused", "error=<nil>"), ("error=outer refused", "error="),
                         ("context canceled", "cancellation identity lost"),
                         ("outer refused", "context deadline exceeded")):
            with self.subTest(old=old, new=new):
                self.reject(fixture().replace(old, new, 1))
        for mode in T.MODES:
            extra = "FIRST direct Lifecycle.Stop return: <nil>"
            self.reject(fixture(lambda m, r: r[:3] + [extra] + r[3:] if m == mode else r))

    def test_go_multiline_joined_error_is_preserved(self):
        result = self.check(fixture(lambda m, r: [v.replace("expiry_scenario_incomplete",
                                                          "expiry_scenario_incomplete\ncontext canceled") for v in r]))
        self.assertTrue(result["transcript_matches"])
        # Cancellation may be the entire worker error or any joined component.
        self.assertTrue(self.check(fixture().replace("error=outer refused\n        context canceled",
                                                    "error=context canceled"))["transcript_matches"])

    def test_final_scenario_and_every_cleanup_scalar(self):
        changes = [
            ("success=true", "success=false"), ("success=false", "success=true"),
            ("actual error=expiry_scenario_incomplete", "actual error=<nil>"),
            ("actual error=expiry_scenario_incomplete", "actual error="),
            ("actual error=expiry_scenario_incomplete", "actual error=other_incomplete"),
            ("success=true; actual error=<nil>", "success=true; actual error=expiry_scenario_incomplete"),
            ("full owner/Host/gate error=<nil>", "full owner/Host/gate error=broken"),
            ("full root error=<nil>", "full root error=broken"), ("closed/quiescent=true", "closed/quiescent=false"),
        ]
        changes += [(f"first {kind} recorded=true", f"first {kind} recorded=false") for kind in ("owner", "Host", "gate")]
        changes += [(f"first {kind} recorded=true error=<nil>", f"first {kind} recorded=true error=broken")
                    for kind in ("owner", "Host", "gate")]
        changes += [(f"root {kind} acquired/attempted/completed=[1 1 1]",
                     f"root {kind} acquired/attempted/completed=[1 2 1]") for kind in ("parent", "data", "lock")]
        for mode in T.MODES:
            for old, new in changes:
                text = fixture(lambda m, r: [v.replace(old, new) for v in r] if m == mode else r)
                if text != fixture():
                    with self.subTest(mode=mode, old=old):
                        self.reject(text)

    def test_endpoint_identity_substitution_restoration_safety(self):
        changes = [
            ("PID=42", "PID=0"), ("start_ticks=77", "start_ticks=0"),
            ("PID=42 start_ticks=77 caller_alive", "PID=43 start_ticks=77 caller_alive"),
            ("start_ticks=77 caller_alive", "start_ticks=78 caller_alive"),
            ("inode:102", "inode:101"), ("inode:102", "inode:0"),
            ("inode:102", "inode:18446744073709551616"),
            ("device:4 inode:102", "device:5 inode:102"), ("mode:49600", "mode:16832"),
            ("uid:1000", "uid:4294967296"), ("gid:1000", "gid:4294967296"),
            ("start_ticks=77", "start_ticks=18446744073709551616"),
            ("error=<nil>; no Accept", "error=broken; no Accept"),
            ("caller_alive=true", "caller_alive=false"), ("restored=true", "restored=false"),
            ("stickyflags=true", "stickyflags=false"), ("no_prior_primary_Close=true", "no_prior_primary_Close=false"),
            ("/expiry-outer-original.sock", "/foreign.sock"), ("/gapdb.sock", "/foreign.sock"),
        ]
        changes += [(f"{key}=<nil>", f"{key}=unsafe")
                    for key in ("listener.Close", "unlink", "rename", "directory.Close", "outcome")]
        for old, new in changes:
            with self.subTest(old=old, new=new):
                self.reject(fixture().replace(old, new, 1))
        # Global mutation moves both logged original/restored paths outside the owned root.
        self.reject(fixture().replace("expiry-host-5/runtime/", "expiry-host-4/runtime/"))
        for marker in ("originalalive", "endpoint fixture restoration", "original socket restored"):
            for mode in T.MODES[:-1]:
                extra = next(v for v in case_rows("endpointSubstitution", 5) if v.startswith(marker))
                self.reject(fixture(lambda m, r: r[:6] + [extra] + r[6:] if m == mode else r))

    def test_positive_uint64_counts_and_no_negative_RA_credit(self):
        for old, new in (
            ("held_revision=7", "held_revision=0"), ("held_revision=7", "held_revision=-1"),
            ("held_revision=7", "held_revision=18446744073709551616"),
            ("expired_revision=8", "expired_revision=7"), ("expired_revision=8", "expired_revision=6"),
            ("expired_revision=8", "expired_revision=18446744073709551616"),
            ("private=6", "private=5"), ("publication=0", "publication=7"),
            ("explicitRA=true", "explicitRA=false"),
        ):
            with self.subTest(old=old, new=new):
                self.reject(fixture().replace(old, new, 1))
        self.assertTrue(self.check(fixture().replace("expired_revision=8", "expired_revision=18446744073709551615"))["transcript_matches"])
        positive = next(v for v in case_rows("unchanged", 1) if v.startswith("actual outer unchanged"))
        for mode in T.MODES[1:]:
            self.reject(fixture(lambda m, r: r[:6] + [positive] + r[6:] if m == mode else r))
            moved = fixture(lambda m, r: [v for v in r if v != positive] if m == "unchanged" else
                            r[:6] + [positive] + r[6:] if m == mode else r)
            result = self.check(moved)
            self.assertFalse(result["transcript_matches"])
            self.assertFalse(result["explicit_RA_qualification"])

    def test_old_contracts_are_not_interchangeable(self):
        self.reject(old_fixture())
        self.reject(release_fixture())
        for checker in (OLD.verify, OLD.verify_release_controls):
            self.assertFalse(checker(fixture(), "", source_sha256=SHA, root_parent=ROOT)["transcript_matches"])

    def test_explicit_parameter_guards_and_text_only(self):
        for key in ("source_sha256", "outer_source_sha256", "held_source_sha256"):
            for value in (None, 12, b"a" * 64, "", "a" * 63, "a" * 65, "A" * 64, "g" * 64):
                with self.subTest(key=key, value=value):
                    with self.assertRaises(ValueError):
                        self.check(**{key: value})
        for root in (None, 12, "relative", "//synthetic-only/r", "/synthetic-only//r", "/synthetic-only/../r", ROOT + "/"):
            with self.assertRaises(ValueError):
                self.check(root_parent=root)
        for stdout, stderr in ((b"text", ""), ("text", b"text"), (None, "")):
            with self.assertRaises(TypeError):
                self.check(stdout, stderr) if stdout is not None else T.verify_outer_controls(
                    None, "", source_sha256=SHA, root_parent=ROOT,
                    outer_source_sha256=OUTER_SHA, held_source_sha256=HELD_SHA)
        with self.assertRaises(TypeError):
            T.verify_outer_controls(fixture(), "", source_sha256=SHA, root_parent=ROOT)


if __name__ == "__main__":
    unittest.main()
