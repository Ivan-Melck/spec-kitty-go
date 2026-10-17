"""Pure checkers for fixed Host expiry transcript contracts.

A matching transcript is not process containment, complete negative coverage,
performance qualification, an admission record, or whole-goal completion. Callers
must separately bind source, executable, first exit/close outcomes and input bytes.
This module does no I/O or provider execution.
"""
import collections
from pathlib import PurePosixPath
import re

CROSS = "TestExpiryHostNativeRequiredInitialCleanUncleanCrossProduct"
RECONCILER = "TestExpiryHostNativeReconcilerActualACKsIncluded"
REVOCATION = "TestExpiryHostNativePriorRevocationRefusesBothRestartModes"
EXPECTED = (
    [CROSS]
    + [f"{CROSS}/{mode}/{acks}" for mode in ("initial", "clean", "unclean") for acks in ("private_only", "actual_ACK")]
    + [RECONCILER, REVOCATION]
    + [f"{REVOCATION}/{cause}/{mode}" for cause in ("gate_close", "failed_live_owner_cleanup_stale") for mode in ("clean", "unclean")]
)


RELEASE_CONTROLS = "TestExpiryHostNativeEOFAndDaemonTimeoutControls"
RELEASE_EXPECTED = [RELEASE_CONTROLS] + [
    f"{RELEASE_CONTROLS}/{mode}/{acks}"
    for mode in ("EOF", "daemon_timeout") for acks in ("private_only", "actual_ACK")
]


def verify(stdout, stderr, *, source_sha256, root_parent):
    """The original thirteen-outcome contract; no cases may be waived."""
    return _verify(stdout, stderr, source_sha256=source_sha256, root_parent=root_parent,
                   expected=EXPECTED, root_count=11, negatives=EXPECTED[-4:])


def verify_release_controls(stdout, stderr, *, source_sha256, root_parent):
    """Separate four-fault contract, never substituted for the original suite."""
    result = _verify(stdout, stderr, source_sha256=source_sha256, root_parent=root_parent,
                     expected=RELEASE_EXPECTED, root_count=4, negatives=RELEASE_EXPECTED[1:])
    result["schema"] = "expiry-host-release-controls-transcript-check/v1"
    result["explicit_RA_qualification"] = False
    passive = {}
    pattern = (r"PASSIVE expiry advance: mode=(EOF|daemon_timeout); before=([0-9]{1,20}); after=([0-9]{1,20}); "
               r"publication=([0-9]{1,20}); private=([0-9]{1,20}); parent_closed_before_advance=(true|false); explicitRA=false$")
    for name in RELEASE_EXPECTED[1:]:
        mode, acks = name.split("/")[-2:]
        block = re.search(r"^=== RUN   " + re.escape(name) + r"\s*\n(.*?)(?=^=== RUN|^\s*--- PASS:|\Z)", stdout, re.M | re.S)
        rows = re.findall(pattern, block.group(1) if block else "", re.M)
        ok = False
        if len(rows) == 1:
            observed_mode, before, after, pubs, private, parent_closed = rows[0]
            before, after, pubs, private = map(int, (before, after, pubs, private))
            ok = (observed_mode == mode and 0 < before < after <= (1 << 64) - 1
                  and pubs == (7 if acks == "actual_ACK" else 0) and private == 6
                  and parent_closed == ("true" if mode == "EOF" else "false"))
        passive[name] = ok
        if not ok:
            result["errors"].append("missing/invalid passive expiry receipt: " + name)
    result["passive_expiry_receipts"] = passive
    result["transcript_matches"] = not result["errors"]
    return result


def _verify(stdout, stderr, *, source_sha256, root_parent, expected, root_count, negatives):
    """Check an observed text snapshot; never assert it is a final stream."""
    if not isinstance(stdout, str) or not isinstance(stderr, str):
        raise TypeError("text snapshots required")
    if not re.fullmatch(r"[0-9a-f]{64}", source_sha256):
        raise ValueError("exact source digest required")
    parent = PurePosixPath(root_parent)
    if not parent.is_absolute() or str(parent) != root_parent or ".." in parent.parts:
        raise ValueError("canonical absolute root parent required")
    errors = []
    runs = re.findall(r"^=== RUN   (\S+)\s*$", stdout, re.M)
    passes = re.findall(r"^\s*--- PASS: (\S+) \([^\n]*\)$", stdout, re.M)
    if runs != expected:
        errors.append("RUN census/order mismatch")
    if collections.Counter(passes) != collections.Counter(expected):
        errors.append("PASS census mismatch")
    if re.search(r"--- (?:SKIP|FAIL):|^FAIL(?:\s|$)|^=== (?:PAUSE|CONT)", stdout + "\n" + stderr, re.M):
        errors.append("skip/fail/unexpected scheduling")
    if re.search(r"^=== RUN|--- (?:PASS|SKIP|FAIL):", stderr, re.M):
        errors.append("test status in stderr")
    if stdout.splitlines()[-1:] != ["PASS"] or stdout.splitlines().count("PASS") != 1:
        errors.append("missing/duplicate/nonterminal final PASS")

    roots = re.findall(r"preserved qualification root: ([^;\n]+); source review: ([0-9a-f]{64})$", stdout, re.M)
    if len(roots) != root_count or len({path for path, _ in roots}) != root_count:
        errors.append("not eleven distinct root observations" if root_count == 11 else "not four distinct root observations")
    for path, sha in roots:
        root = PurePosixPath(path)
        if root.parent != parent or str(root) != path or root.name in (".", "..") or sha != source_sha256:
            errors.append("root/source observation mismatch")

    negative_receipts = {}
    for name in negatives:
        match = re.search(r"^=== RUN   " + re.escape(name) + r"\s*\n(.*?)(?=^=== RUN|^\s*--- PASS:|\Z)", stdout, re.M | re.S)
        block = match.group(1) if match else ""
        scenario = re.findall(r"FIRST final close: expected scenario rejection; actual error=(.*)", block)
        full = re.findall(r"POST-RUNDOWN actual full owner/Host/gate error=(.*); full root error=(.*)", block)
        state = re.findall(r"POST-RUNDOWN canonical final closed/quiescent=(.*)", block)
        counts = {kind: re.findall(r"POST-RUNDOWN required root " + kind + r" acquired/attempted/completed=(.*)", block)
                  for kind in ("parent", "data", "lock")}
        ok = (len(scenario) == 1 and scenario[0] not in ("", "<nil>")
              and full == [("<nil>", "<nil>")]
              and state == ["true; first owner recorded=true error=<nil>; first Host recorded=true error=<nil>; first gate recorded=true error=<nil>"]
              and all(value == ["[1 1 1]"] for value in counts.values()))
        negative_receipts[name] = ok
        if not ok:
            errors.append("missing/invalid first-outcome cleanup receipt: " + name)
    return {
        "schema": "expiry-host-transcript-check/v1",
        "errors": errors,
        "transcript_matches": not errors,
        "actual_run": runs,
        "actual_pass": passes,
        "roots_observed": [path for path, _ in roots],
        "negative_first_outcome_receipts": negative_receipts,
        "stream_finality": "UNKNOWN; observed snapshot only",
        "descendant_containment": "UNKNOWN",
        "whole_goal_success": False,
        "full_negative_matrix": False,
        "memory_qualification": False,
        "performance_qualification": False,
    }
