#!/bin/sh
# Copy data from a real upstream wheel, including the built-in pack fetched
# and verified by its build hook. Usage: sync-assets.sh <checkout> [wheel]
set -eu
UP=${1:?usage: sync-assets.sh <upstream checkout> [wheel]}
DST=$(cd "$(dirname "$0")/.." && pwd)/internal/assets/data
WHEEL=${2:-}
SCRATCH=$(mktemp -d)
trap 'rm -rf "$SCRATCH"' EXIT HUP INT TERM
if [ -z "$WHEEL" ]; then
  (cd "$UP" && uv build --wheel -o "$SCRATCH")
  set -- "$SCRATCH"/*.whl
  [ "$#" -eq 1 ] || { echo "expected exactly one upstream wheel" >&2; exit 1; }
  WHEEL=$1
fi
REV=$(git -C "$UP" rev-parse HEAD)
python3 - "$WHEEL" "$DST" "$REV" "$SCRATCH" <<'PY'
from pathlib import Path, PurePosixPath
from zipfile import ZipFile
import shutil
import sys

wheel, dst, revision, scratch = sys.argv[1:]
stage = Path(scratch) / "data"
stage.mkdir()
roots = {"packs", "charter", "specify_cli", "runtime", "mission_runtime", "kernel", "glossary"}
with ZipFile(wheel) as archive:
    for member in archive.infolist():
        path = PurePosixPath(member.filename)
        if member.is_dir() or not path.parts or path.parts[0] not in roots:
            continue
        if path.is_absolute() or ".." in path.parts:
            raise SystemExit(f"unsafe wheel member: {member.filename}")
        if path.suffix in {".py", ".pyc"} and str(path) != "packs/built-in/assets/docs_structural_lint.py":
            continue
        if "tests" in path.parts or ".contextive" in str(path):
            continue
        target = stage.joinpath(*path.parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(archive.read(member))
pack = stage / "packs/built-in"
if not (pack / "pack.yaml").is_file() or not ((pack / "graph.yaml").is_file() or any(pack.glob("*.graph.yaml"))):
    raise SystemExit("upstream wheel has no built-in graph; assets were not changed")
(stage / "UPSTREAM_REVISION").write_text(revision + "\n")
dst = Path(dst)
if dst.exists():
    shutil.rmtree(dst)
shutil.copytree(stage, dst)
print(f"synced {sum(p.is_file() for p in dst.rglob('*'))} files from {revision}")
PY
