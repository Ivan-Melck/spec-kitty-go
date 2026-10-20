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
fi
(cd "$REF/upstream" && uv build --wheel -o "$REF/dist")
uv venv -q --allow-existing "$REF/oracle-venv" --python 3.13
VIRTUAL_ENV="$REF/oracle-venv" uv pip install -q --reinstall "$REF"/dist/spec_kitty_cli-*.whl
# The directory entry must be named spec-kitty: click prints argv[0] in usage lines.
mkdir -p "$REF/oracle-bin"
ln -sfn "$REF/oracle-venv/bin/spec-kitty" "$REF/oracle-bin/spec-kitty"
HOME=$(mktemp -d) SPEC_KITTY_NO_UPGRADE_CHECK=1 "$REF/oracle-bin/spec-kitty" --version
