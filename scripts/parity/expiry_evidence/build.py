#!/usr/bin/env python3
"""Prepare/build an isolated TEST artifact. Never edits a provider or product tree."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

VERSION = "v0.0.0-20260907114832-006e9d469d40"
MODULE = "github.com/spec-kitty/gapdb"
MODULE_SUM = "h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM="
EXPIRY_HASH = "da3f45409507ed69118a172ef9e5e80aa842a03087031fe746a2823713795a41"
NEEDLE = "\tsort.Slice(due, func(i, j int) bool { return due[i].key < due[j].key })"
ASSETS = Path(__file__).resolve().parent
REPO = ASSETS.parents[2]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def inventory(root):
    result = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"symlink in source: {path}")
        if path.is_file():
            result[str(path.relative_to(root))] = sha(path.read_bytes())
    return result


def patch_expiry(raw):
    if sha(raw) != EXPIRY_HASH:
        raise ValueError("expiry source differs from exact pinned source")
    text = raw.decode("utf-8")
    if text.count(NEEDLE) != 1:
        raise ValueError("expiry hold placement is not unique")
    # Due candidates have been selected, but no revision/WAL/map change or lock
    # is held. Readers/status remain on the original daemon implementation.
    return text.replace(NEEDLE, "\tspkExpiryEvidenceHold() // isolated qualification only\n" + NEEDLE).encode()


def module_zip_sum(archive):
    # Go dirhash.Hash1/HashZip: bind actual ZIP bytes to the immutable go.sum
    # value, independent of the mutable download-cache .ziphash file.
    total = hashlib.sha256()
    with zipfile.ZipFile(archive) as z:
        names = sorted(info.filename for info in z.infolist())
        if len(names) != len(set(names)) or any("\n" in name for name in names):
            raise ValueError("unsafe/duplicate module ZIP member")
        for name in names:
            total.update((sha(z.read(name)) + "  " + name + "\n").encode())
    return "h1:" + base64.b64encode(total.digest()).decode("ascii")


def prepare(source, output, archive):
    if output.exists() or output.is_symlink():
        raise FileExistsError(output)
    before = inventory(source)
    if module_zip_sum(archive) != MODULE_SUM:
        raise ValueError("actual module ZIP does not match pinned go.sum")
    # Authenticate both the module ZIP bytes and every copied source file.
    with zipfile.ZipFile(archive) as z:
        prefix = f"{MODULE}@{VERSION}/"
        expected = {}
        for info in z.infolist():
            if info.is_dir():
                continue
            if not info.filename.startswith(prefix):
                raise ValueError("unexpected module archive prefix")
            name = info.filename[len(prefix):]
            if name in expected or Path(name).is_absolute() or ".." in Path(name).parts:
                raise ValueError("unsafe/duplicate module archive member")
            expected[name] = sha(z.read(info))
        if before != expected:
            raise ValueError("source differs from authenticated module ZIP")
    patched_expiry = patch_expiry((source / "internal/engine/expiry.go").read_bytes())
    # The output must not exist. Never merge into any checkout or prior run.
    output.mkdir(mode=0o700, parents=False)
    copied = output / "source"
    shutil.copytree(source, copied)
    for path in copied.rglob("*"):
        path.chmod(0o700 if path.is_dir() else 0o600)
    copied.chmod(0o700)
    expiry = copied / "internal/engine/expiry.go"
    expiry.write_bytes(patched_expiry)
    for asset, target in [("gate.go.txt", "spk_expiry_gate.go"),
                          ("gate_disabled.go.txt", "spk_expiry_gate_disabled.go"),
                          ("gate_test.go.txt", "spk_expiry_gate_test.go")]:
        (copied / "internal/engine" / target).write_bytes((ASSETS / asset).read_bytes())
    after = inventory(copied)
    changed = sorted(k for k in before if before[k] != after.get(k))
    added = sorted(after.keys() - before.keys())
    if changed != ["internal/engine/expiry.go"] or len(added) != 3:
        raise ValueError("unexpected source delta")
    if inventory(source) != before:
        raise ValueError("original source changed during preparation")
    record = {"schema_version": 1, "kind": "expiry-test-artifact-not-production-admission",
              "module": MODULE, "version": VERSION, "module_sum": MODULE_SUM,
              "original_source": str(source), "original_inventory": before,
              "test_source_inventory": after, "changed": changed, "added": added,
              "assets": inventory(ASSETS), "release_ready": False, "adopted": False,
              "qualification_complete": False, "original_source_unchanged": True}
    (output / "preparation.json").write_text(json.dumps(record, indent=2) + "\n")
    return copied, record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="fresh directory under an existing parent")
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    env = {**os.environ, "GOTOOLCHAIN": "go1.26.7", "GOFLAGS": "", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64"}
    if f"{MODULE} {VERSION} {MODULE_SUM}" not in (REPO / "go.sum").read_text().splitlines():
        raise SystemExit("pinned dependency sum differs")
    cache = Path(subprocess.check_output(["go", "env", "GOMODCACHE"], env=env, cwd=REPO, text=True).strip())
    subprocess.run(["go", "mod", "verify"], cwd=REPO, env=env, check=True)
    source = cache / f"{MODULE}@{VERSION}"
    archive = cache / "cache/download" / MODULE / "@v" / f"{VERSION}.zip"
    output = args.output.absolute()
    copied, record = prepare(source, output, archive)
    if args.prepare_only:
        print(output / "preparation.json")
        return

    def run(name, command, race=False):
        run_env = {**env, "CGO_ENABLED": "1"} if race else env
        with (output / (name + ".log")).open("x") as log:
            log.write(json.dumps({"command": command, "CGO_ENABLED": run_env["CGO_ENABLED"]}) + "\n")
            log.flush()
            result = subprocess.run(command, cwd=copied, env=run_env, stdout=log, stderr=subprocess.STDOUT)
        if result.returncode:
            raise SystemExit(f"{name} failed ({result.returncode}); preserved {output}")

    run("gate-unit", ["go", "test", "-count=1", "-v", "-tags=spk_expiry_evidence", "./internal/engine", "-run=^TestSPKExpiryEvidenceGate"])
    run("gate-race", ["go", "test", "-race", "-count=1", "-v", "-tags=spk_expiry_evidence", "./internal/engine", "-run=^TestSPKExpiryEvidenceGate"], race=True)
    for name, tags in [("gapdbd.expiry-evidence", "spk_expiry_evidence"),
                       ("gapdbd.expiry-evidence.repeat", "spk_expiry_evidence"),
                       ("gapdbd.control", "")]:
        run(name, ["go", "build", "-trimpath", "-buildvcs=false", "-tags=" + tags, "-o", str(output / name), "./cmd/gapdbd"])
    image = (output / "gapdbd.expiry-evidence").read_bytes()
    if image != (output / "gapdbd.expiry-evidence.repeat").read_bytes():
        raise SystemExit("repeated test builds differ")
    run("gate-vet", ["go", "vet", "-tags=spk_expiry_evidence", "./internal/engine"])
    if inventory(source) != record["original_inventory"] or inventory(copied) != record["test_source_inventory"]:
        raise SystemExit("source changed during build")
    record["executables"] = [{"path": str(output / name), "sha256": sha((output / name).read_bytes()),
                              "bytes": (output / name).stat().st_size}
                             for name in ["gapdbd.expiry-evidence", "gapdbd.control"]]
    record["build_recipe"] = {"go": subprocess.check_output(["go", "version"], env=env, text=True).strip(),
                              "env": {k: env[k] for k in ["GOTOOLCHAIN", "GOFLAGS", "CGO_ENABLED", "GOOS", "GOARCH"]},
                              "tags": "spk_expiry_evidence", "trimpath": True, "buildvcs": False,
                              "identical_test_builds": 2}
    (output / "build.json").write_text(json.dumps(record, indent=2) + "\n")
    print(output / "build.json")


if __name__ == "__main__":
    main()
