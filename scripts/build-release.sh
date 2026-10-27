#!/bin/sh
# Cross-compile release archives and SHA256SUMS into a directory.
# Usage: scripts/build-release.sh <version> [out dir]
# Archives are named spec-kitty_<version>_<os>_<arch>.tar.gz (.zip on Windows)
# and contain the executable, LICENSE and README.md.
set -eu
VERSION=${1:?usage: build-release.sh <version> [out dir]}
OUT=${2:-dist}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TARGETS=${TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}
# Reproducible archive timestamps: the commit time unless the caller sets one.
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct)}
export SOURCE_DATE_EPOCH
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
for target in $TARGETS; do
  os=${target%/*}
  arch=${target#*/}
  name="spec-kitty_${VERSION}_${os}_${arch}"
  exe=spec-kitty
  [ "$os" = windows ] && exe=spec-kitty.exe
  mkdir -p "$STAGE/$name"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
    go build -trimpath -ldflags "-s -w -buildid=" -o "$STAGE/$name/$exe" ./cmd/spec-kitty)
  cp "$ROOT/LICENSE" "$ROOT/README.md" "$STAGE/$name/"
  find "$STAGE/$name" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
  if [ "$os" = windows ]; then
    (cd "$STAGE" && rm -f "$OUT/$name.zip" && zip -qrX "$OUT/$name.zip" "$name")
  else
    (cd "$STAGE" && tar --sort=name --owner=0 --group=0 --numeric-owner \
      --mtime="@$SOURCE_DATE_EPOCH" -cf - "$name" | gzip -n > "$OUT/$name.tar.gz")
  fi
  echo "built $name"
done
(cd "$OUT" && sha256sum spec-kitty_"${VERSION}"_* > SHA256SUMS)
