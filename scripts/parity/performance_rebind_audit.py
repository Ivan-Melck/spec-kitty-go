#!/usr/bin/env python3
"""Read-only source-drift planning; NEVER imports/runs a frozen executor.

Only cmd/internal Go source and the baseline's explicit repository fixtures/module
files are read. Provider images, report roots, native streams, external pins and
historical ROOTS contents are never opened. This is NOT build/timing permission,
semantic equivalence, source-closure attestation or completed qualification.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import stat

NON_GO = {
    "go.mod", "go.sum", "docs/programs/evidence/m1-adoption.json",
    "internal/testcampaign/m1/testdata/evidence/incomplete.json",
    "internal/testcampaign/m1/testdata/evidence/pass.json",
    "internal/testcampaign/testdata/hubnot-qualification-reference.json",
    "internal/testcampaign/testdata/qualification-reference.json",
    "internal/tracker/hubnot/testdata/codec-fixtures.json",
}


def read_regular(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    primary, close_error, body = None, None, bytearray()
    try:
        before = os.fstat(fd)
        if not stat.S_ISREG(before.st_mode) or before.st_size > 16 << 20:
            raise ValueError("not bounded regular source", str(path))
        while len(body) < before.st_size:
            chunk = os.read(fd, min(1 << 20, before.st_size - len(body)))
            if not chunk:
                raise ValueError("short source read", str(path))
            body.extend(chunk)
        identity = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
        if identity(before) != identity(os.fstat(fd)) or identity(before) != identity(os.lstat(path)):
            raise ValueError("source changed during read", str(path))
    except BaseException as exc:
        primary = exc
    try:
        os.close(fd)
    except BaseException as exc:
        close_error = exc
    if primary is not None or close_error is not None:
        raise BaseExceptionGroup("source read/first close", [e for e in (primary, close_error) if e is not None])
    return bytes(body)


def source_path(repo, member):
    if not isinstance(member, str):
        raise ValueError("non-string source member")
    rel = PurePosixPath(member)
    if rel.is_absolute() or any(p in ("..", ".") for p in member.split("/")) or str(rel) != member:
        raise ValueError("noncanonical source member", member)
    if member not in NON_GO and not (rel.suffix == ".go" and rel.parts[0] in ("cmd", "internal")):
        raise ValueError("outside source-only audit domain", member)
    path = repo / member
    for parent in (path, *path.parents):
        if parent == repo.parent:
            break
        if parent.is_symlink():
            raise ValueError("symlink source ancestry", str(parent))
    return path


def audit(repo, baseline):
    repo = Path(repo).absolute()
    if repo.resolve() != repo or not repo.is_dir():
        raise ValueError("real canonical repository required")
    members = baseline["source_members"]
    if not isinstance(members, list) or len(members) != len(set(members)):
        raise ValueError("source members must be unique")
    current_go = set()
    for directory in ("cmd", "internal"):
        for path in (repo / directory).rglob("*.go"):
            member = path.relative_to(repo).as_posix()
            source_path(repo, member)
            if path.is_file():
                current_go.add(member)
    unchanged, changed, missing = [], [], []
    for member in members:
        path = source_path(repo, member)
        pin = baseline["pins"][str(path)]
        if not path.exists():
            missing.append(member)
            continue
        raw = read_regular(path)
        actual = {"bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
        old = {key: pin[key] for key in ("bytes", "sha256")}
        if actual == old:
            unchanged.append(member)
        else:
            changed.append({"path": member, "baseline": old, "current": actual})
    return {
        "schema": "performance-source-rebind-planning/v1",
        "baseline_members": len(members), "unchanged": sorted(unchanged),
        "changed": changed, "missing": sorted(missing),
        "added_go": sorted(current_go - set(members)),
        "rebinding_required": True, "build_or_timing_authority": False,
        "ordinary_build_dependency_closure_proved": False,
        "source_semantic_equivalence_proved": False, "all_four": "OPEN",
        "remaining": ["fresh source/custody bindings and independent exact-source/contingency review",
                      "fresh one-shot build plus same-build ELF/profile review",
                      "explicit timing gate and prior-provider containment decision; finish intentional parallel work",
                      "preserve/audit every actual sample, failure, first close and all evidence roots",
                      "authentic B1-B4 inputs for separate all-ten-role workload; NOT private/hash fixture prerequisites"],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--baseline", required=True)
    args = parser.parse_args()
    raw = read_regular(Path(args.baseline))
    result = audit(args.repo, json.loads(raw))
    result["baseline_sha256"] = hashlib.sha256(raw).hexdigest()
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
