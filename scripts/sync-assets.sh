#!/bin/sh
# Copy the data assets shipped in the upstream spec-kitty wheel into
# internal/assets/data, preserving in-wheel paths (src/ prefix removed;
# packs/built-in kept). Usage: scripts/sync-assets.sh /path/to/spec-kitty
set -eu
UP=${1:?usage: sync-assets.sh <upstream checkout>}
DST=$(cd "$(dirname "$0")/.." && pwd)/internal/assets/data
rm -rf "$DST"
mkdir -p "$DST"
cd "$UP"
git ls-files -- packs/built-in src/charter src/specify_cli src/runtime src/mission_runtime src/kernel src/glossary \
  | grep -v '\.py$' \
  | grep -v '/tests/' \
  | grep -v '\.contextive' \
  | grep -v '^src/kernel/pyproject.toml$' \
  | while IFS= read -r f; do
      case "$f" in
        src/*) out=${f#src/} ;;
        *) out=$f ;;
      esac
      mkdir -p "$DST/$(dirname "$out")"
      cp "$f" "$DST/$out"
    done
# keep python-implemented pack assets that ship as data
for f in packs/built-in/assets/docs_structural_lint.py; do
  [ -f "$f" ] && cp "$f" "$DST/$f"
done
git rev-parse HEAD > "$DST/UPSTREAM_REVISION"
echo "synced $(find "$DST" -type f | wc -l) files from $(cat "$DST/UPSTREAM_REVISION")"
