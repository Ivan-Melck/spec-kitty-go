#!/bin/sh
# Rebuild the local parity reference area: a read-only upstream checkout at
# the pinned revision and a wheel-installed oracle binary.
# Usage: scripts/setup-reference.sh /path/to/spec-kitty-checkout [REF dir]
set -eu
UP=${1:?usage: setup-reference.sh <upstream spec-kitty checkout> [ref dir]}
REF=${2:-$HOME/.cache/spec-kitty-parity}
REV=$(cat "$(dirname "$0")/../internal/assets/data/UPSTREAM_REVISION")
mkdir -p "$REF"
if [ ! -d "$REF/upstream" ]; then
  git -C "$UP" worktree add --detach "$REF/upstream" "$REV"
elif [ "$(git -C "$REF/upstream" rev-parse HEAD)" != "$REV" ]; then
  if [ -n "$(git -C "$REF/upstream" status --porcelain)" ]; then
    echo "reference checkout has changes; cannot move it to pinned revision $REV" >&2
    exit 1
  fi
  git -C "$REF/upstream" checkout --detach "$REV"
fi
DIST=$(mktemp -d)
CHECK_HOME=$(mktemp -d)
trap 'rm -rf "$DIST" "$CHECK_HOME"' EXIT HUP INT TERM
(cd "$REF/upstream" && uv build --wheel -o "$DIST")
set -- "$DIST"/*.whl
[ "$#" -eq 1 ] || { echo "expected exactly one upstream wheel" >&2; exit 1; }
uv venv -q --allow-existing "$REF/oracle-venv" --python 3.13
uv pip install -q --python "$REF/oracle-venv/bin/python" --reinstall "$1"
mkdir -p "$REF/dist"
cp "$1" "$REF/dist/"
# The directory entry must be named spec-kitty: click prints argv[0] in usage lines.
mkdir -p "$REF/oracle-bin"
ln -sfn "$REF/oracle-venv/bin/spec-kitty" "$REF/oracle-bin/spec-kitty"
HOME="$CHECK_HOME" SPEC_KITTY_NO_UPGRADE_CHECK=1 "$REF/oracle-bin/spec-kitty" --version
uv pip freeze --python "$REF/oracle-venv/bin/python" > "$REF/oracle-requirements.txt"
printf '%s\n' "$REV" > "$REF/UPSTREAM_REVISION"
