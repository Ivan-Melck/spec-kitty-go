#!/bin/sh
# Install the spec-kitty executable from a GitHub release (Linux and macOS).
#   curl -fsSL https://raw.githubusercontent.com/<repo>/<ref>/scripts/install.sh | sh
# Environment:
#   SPEC_KITTY_REPO         GitHub owner/name hosting releases (default below)
#   SPEC_KITTY_VERSION      release version without "v" (default: newest release,
#                           including release candidates)
#   SPEC_KITTY_INSTALL_DIR  destination directory (default: $HOME/.local/bin)
#   SPEC_KITTY_DOWNLOAD_URL base URL holding the archives and SHA256SUMS, for
#                           mirrors (requires SPEC_KITTY_VERSION)
set -eu
REPO=${SPEC_KITTY_REPO:-Ivan-Melck/spec-kitty-go}
DEST=${SPEC_KITTY_INSTALL_DIR:-$HOME/.local/bin}
VERSION=${SPEC_KITTY_VERSION:-}

fail() { echo "spec-kitty install: $*" >&2; exit 1; }
fetch() {
  if [ -n "${SPEC_KITTY_DOWNLOAD_URL:-}" ]; then curl -fsSL "$@"; else curl -fsSL --proto '=https' --tlsv1.2 "$@"; fi
}
command -v curl >/dev/null 2>&1 || fail "curl is required"

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported operating system $(uname -s); on Windows use scripts/install.ps1" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported CPU architecture $(uname -m)" ;;
esac

if [ -z "$VERSION" ]; then
  [ -z "${SPEC_KITTY_DOWNLOAD_URL:-}" ] || fail "SPEC_KITTY_DOWNLOAD_URL requires SPEC_KITTY_VERSION"
  VERSION=$(fetch "https://api.github.com/repos/$REPO/releases?per_page=1" |
    sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$VERSION" ] || fail "no release found in $REPO"
fi

name="spec-kitty_${VERSION}_${os}_${arch}"
base=${SPEC_KITTY_DOWNLOAD_URL:-"https://github.com/$REPO/releases/download/v$VERSION"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
fetch -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "could not download $base/$name.tar.gz"
fetch -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "could not download $base/SHA256SUMS"
expected=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$expected" ] || fail "SHA256SUMS has no entry for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
  actual=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $name.tar.gz"
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$DEST"
install -m 0755 "$tmp/$name/spec-kitty" "$DEST/spec-kitty.new"
mv -f "$DEST/spec-kitty.new" "$DEST/spec-kitty"
echo "Installed spec-kitty $VERSION to $DEST/spec-kitty"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "Add $DEST to PATH to run spec-kitty from any directory." ;;
esac
