"""Pure, bounded snapshot checker for the separate five-case outer contract.

No I/O or execution. Matching synthetic or supplied text does not establish an
actual native run, independent approval, precise refusing nested outer check,
OS/descendant containment, heap/performance qualification, or whole-area success.
Pins are caller-supplied expectations, not source files authenticated here.
"""
from pathlib import PurePosixPath
import re

from expiry_host_transcript import _verify

OUTER = "TestExpiryHostNativeOuterTerminalControls"
MODES = ("unchanged", "directHostClose", "directLifecycleStop", "callerCancel", "endpointSubstitution")
EXPECTED = [OUTER] + [f"{OUTER}/{mode}" for mode in MODES]
FUNCTION = "github.com/spec-kitty/go-kitty3/internal/productionruntime.(*expiryHostRun).observeS1Refusal"
SOURCE_SUFFIX = "/internal/productionruntime/expiry_qualification_observation.go"
NUMBER = r"[0-9]{1,20}"
DURATION = r"(?=[^\s]*[1-9])(?:[0-9]+(?:\.[0-9]+)?(?:ns|µs|μs|us|ms|s|m|h))+"
LOG = re.compile(r"^[ \t]*\S+\.go:[0-9]+: (.*)$")
# Raw (unprefixed) messages are also accepted; continuation lines are NOT new
# records merely because they contain 'context canceled' or another error.
STARTS = (
    "preserved qualification root:", "conservative held cutoff:", "frontierreached:",
    "FIRST direct ", "actual endpoint substitution:", "actioncompleted:", "workerjoined:",
    "originalalive after workerjoined:", "endpoint fixture restoration:",
    "original socket restored before FIRST teardown:", "actual outer unchanged expiry:",
    "FIRST final close:", "POST-RUNDOWN ",
)


def _records(block):
    records = []
    for line in block.splitlines():
        prefixed = LOG.fullmatch(line)
        raw = line.strip()
        if prefixed or raw.startswith(STARTS):
            records.append(prefixed.group(1) if prefixed else raw)
        elif raw:
            if records:
                records[-1] += "\n" + raw
            else:
                records.append(raw)
    return records


def _path(value):
    path = PurePosixPath(value)
    return path.is_absolute() and not value.startswith("//") and str(path) == value and ".." not in path.parts


def _uint(value, positive=False, bits=64):
    return bool(re.fullmatch(NUMBER, value)) and (1 if positive else 0) <= int(value) < (1 << bits)


def _error(value):
    return value.strip() not in ("", "nil", "<nil>") and "<nil>" not in value


def verify_outer_controls(stdout, stderr, *, source_sha256, root_parent,
                          outer_source_sha256, held_source_sha256):
    """Verify only the prepared native outer five-case text contract.

    Text snapshots and all three lowercase hex64 pins must be explicit. This
    never substitutes for accepted thirteen-outcome or five-outcome release
    contracts, never opens files, and never invokes a provider or subprocess.
    """
    for pin in (source_sha256, outer_source_sha256, held_source_sha256):
        if not isinstance(pin, str) or not re.fullmatch(r"[0-9a-f]{64}", pin):
            raise ValueError("explicit lowercase hex64 source digest required")
    if not isinstance(root_parent, str) or not _path(root_parent):
        raise ValueError("canonical absolute root parent required")
    result = _verify(stdout, stderr, source_sha256=source_sha256, root_parent=root_parent,
                     expected=EXPECTED, root_count=5, negatives=[])
    result.update({
        "schema": "expiry-host-outer-controls-transcript-check/v1",
        "actual_execution": "UNKNOWN; supplied text only",
        "precise_refusing_nested_outer_check": "UNKNOWN; not instrumented here",
        "OS_containment": "UNKNOWN",
        "whole_area_success": False,
        "explicit_RA_qualification": False,
    })
    errors = result["errors"]
    statuses = re.findall(r"^=== RUN   (\S+)\s*$|^\s*--- PASS: (\S+) \([^\n]*\)$", stdout, re.M)
    # Pinned Go testing.Main buffers child PASS lines until the parent returns:
    # all six RUN blocks, then parent PASS followed by the five child PASS lines.
    wanted = [(name, "") for name in EXPECTED] + [("", name) for name in EXPECTED]
    if statuses != wanted:
        errors.append("outer sequential RUN/PASS topology mismatch")

    receipts = {}
    case_records = []
    frontier_sources = []
    positive = False
    for mode, name in zip(MODES, EXPECTED[1:]):
        block = re.search(r"^=== RUN   " + re.escape(name) + r"[ \t]*\n(.*?)(?=^=== RUN|^\s*--- PASS:|\Z)",
                          stdout, re.M | re.S)
        records = _records(block.group(1) if block else "")
        case_records.extend(records)
        index = 0
        valid = True

        def take(pattern):
            nonlocal index, valid
            row = records[index] if index < len(records) else ""
            index += 1
            match = re.fullmatch(pattern, row, re.S)
            if not match:
                valid = False
            return match

        root_row = take(r"preserved qualification root: ([^;\n]+); source review: " + source_sha256)
        root = root_row.group(1) if root_row else ""
        if not root or not _path(root) or PurePosixPath(root).parent != PurePosixPath(root_parent):
            valid = False
        take(r"conservative held cutoff: existing_budget=12s sidecar_source_sha256="
             + held_source_sha256 + r" start_before_WaitHeld=true")
        frontier = take(r"frontierreached: mode=" + mode + r" function=" + re.escape(FUNCTION)
                        + r" source=(\S+) line=195 sha256=" + outer_source_sha256)
        if frontier:
            frontier_sources.append(frontier.group(1))
            if not (_path(frontier.group(1)) and frontier.group(1).endswith(SOURCE_SUFFIX)):
                valid = False

        endpoint = None
        if mode in ("directHostClose", "directLifecycleStop"):
            method = "Host.Close" if mode == "directHostClose" else "Lifecycle.Stop"
            take(r"FIRST direct " + re.escape(method) + r" return: <nil>")
        elif mode == "endpointSubstitution":
            identity = (r"\{device:(" + NUMBER + r") inode:(" + NUMBER + r") mode:(" + NUMBER
                        + r") uid:(" + NUMBER + r") gid:(" + NUMBER + r")\}")
            endpoint = take(r"actual endpoint substitution: original=(\S+) parked=(\S+) original_identity="
                            + identity + r" synthetic_identity=" + identity
                            + r" PID=(" + NUMBER + r") start_ticks=(" + NUMBER
                            + r") error=<nil>; no Accept/respond/RPC/signals")
            if endpoint:
                original, parked, *values = endpoint.groups()
                a, b = values[:5], values[5:10]
                pid, ticks = values[10:]
                directory = PurePosixPath(original).parent
                valid = valid and (
                    _path(original) and _path(parked)
                    and directory.parent == PurePosixPath(root) / "runtime"
                    and original == str(directory / "gapdb.sock")
                    and parked == str(directory / "expiry-outer-original.sock")
                    and all(_uint(v, positive=i == 1, bits=64 if i < 2 else 32)
                            for identity_values in (a, b) for i, v in enumerate(identity_values))
                    and a[0] == b[0] and int(a[1]) != int(b[1])
                    and a[3:] == b[3:]
                    and all(int(v[2]) & 0o170000 == 0o140000 for v in (a, b))
                    and _uint(pid, positive=True) and _uint(ticks, positive=True))

        take(r"actioncompleted: mode=" + mode + r" error=<nil> before_cutoff=true remaining="
             + DURATION + r" \(before resume\)")
        worker = take(r"workerjoined: mode=" + mode + r" error=(.+?) before_cutoff=true remaining=" + DURATION)
        if worker:
            error = worker.group(1)
            valid = valid and (error == "<nil>" if mode == "unchanged" else _error(error))
            if "context deadline exceeded" in error:
                valid = False
            if mode == "callerCancel" and "context canceled" not in error:
                valid = False

        if mode == "unchanged":
            expiry = take(r"actual outer unchanged expiry: held_revision=(" + NUMBER
                          + r"); expired_revision=(" + NUMBER + r"); private=6; publication=0; explicitRA=true")
            positive = bool(expiry and all(_uint(v, positive=True) for v in expiry.groups())
                            and int(expiry.group(1)) < int(expiry.group(2)))
            valid = valid and positive
        elif mode == "endpointSubstitution":
            alive = take(r"originalalive after workerjoined: PID=(" + NUMBER + r") start_ticks=("
                         + NUMBER + r") caller_alive=true")
            if not endpoint or not alive or alive.groups() != endpoint.groups()[-2:]:
                valid = False
            restore = take(r"endpoint fixture restoration: original=(\S+) parked=(\S+) restored=true "
                           r"FIRST listener\.Close=<nil> unlink=<nil> rename=<nil> directory\.Close=<nil> "
                           r"outcome=<nil>; residual paths preserved on failure")
            if not endpoint or not restore or restore.groups() != endpoint.groups()[:2]:
                valid = False
            take(r"original socket restored before FIRST teardown: stickyflags=true "
                 r"no_prior_primary_Close=true caller_alive=true")

        final = take(r"FIRST final close: expected scenario success=" + ("true" if mode == "unchanged" else "false")
                     + r"; actual error=(.+)")
        if final:
            error = final.group(1)
            valid = valid and (error == "<nil>" if mode == "unchanged" else
                               _error(error) and "expiry_scenario_incomplete" in error)
        take(r"POST-RUNDOWN actual full owner/Host/gate error=<nil>; full root error=<nil>")
        take(r"POST-RUNDOWN canonical final closed/quiescent=true; first owner recorded=true error=<nil>; "
             r"first Host recorded=true error=<nil>; first gate recorded=true error=<nil>")
        for kind in ("parent", "data", "lock"):
            take(r"POST-RUNDOWN required root " + kind + r" acquired/attempted/completed=\[1 1 1\]")
        valid = valid and index == len(records)
        receipts[name] = bool(valid)
        if not valid:
            errors.append("missing/invalid ordered outer first-outcome receipts: " + name)

    # No orphan/duplicate receipts before, between, or after the five blocks;
    # stderr cannot supply (or duplicate) qualification evidence either.
    body = "\n".join(line for line in stdout.splitlines()
                     if not (line.startswith("=== RUN") or line.lstrip().startswith("--- PASS:") or line == "PASS"))
    if _records(body) != case_records:
        errors.append("outer receipts outside their case blocks")
    if len(frontier_sources) != 5 or len(set(frontier_sources)) != 1:
        errors.append("outer frontier source path census/binding mismatch")
    if any(row.startswith(STARTS) for row in _records(stderr)):
        errors.append("outer qualification receipt in stderr")
    result["outer_first_outcome_receipts"] = receipts
    result["unchanged_explicit_RA_receipt"] = positive and receipts.get(EXPECTED[1], False)
    result["explicit_RA_qualification"] = bool(result["unchanged_explicit_RA_receipt"] and not errors)
    result["transcript_matches"] = not errors
    return result
