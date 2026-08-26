#!/bin/sh
set -eu
LC_ALL=C
export LC_ALL

qualified_version='1.2.2'
qualified_source_commit='6c124203e771433a3550c348771a5b5e27fd3c21'
qualified_module_sum='h1:2K5ki3k1GGGKuYdFIuJtqHHziGQcepN+H2f/EYfE0X4='
qualified_go_version='go1.26.4'
qualified_build_method='go-install-cgo-gms_pure_go-trimpath-nobuildid-go1.26.4'

usage() {
    printf '%s\n' 'usage: install-qualified-beads.sh --dest ABSOLUTE_DIRECTORY [--offline]'
}

destination=''
offline='false'
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dest)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            destination=$2
            shift 2
            ;;
        --offline)
            offline='true'
            shift
            ;;
        *)
            usage >&2
            exit 2
            ;;
    esac
done

[ -n "$destination" ] || { usage >&2; exit 2; }
case "$destination" in
    /*) ;;
    *) printf '%s\n' 'destination must be absolute' >&2; exit 2 ;;
esac

destination=$(realpath -m -- "$destination")
case "$destination" in
    /|/home|/tmp|/usr|/var) printf '%s\n' 'destination is too broad' >&2; exit 2 ;;
    *[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./+=,@:%-]*)
        printf '%s\n' 'destination contains bytes unsupported by the JSON result contract' >&2
        exit 2
        ;;
esac

if repository_root=$(git rev-parse --show-toplevel 2>/dev/null); then
    repository_root=$(realpath -m -- "$repository_root")
    case "$destination" in
        "$repository_root"|"$repository_root"/*)
            printf '%s\n' 'destination must be outside the source repository' >&2
            exit 2
            ;;
    esac
fi

goos=$(go env GOOS)
goarch=$(go env GOARCH)
case "$goos/$goarch/$qualified_build_method" in
    linux/amd64/go-install-cgo-gms_pure_go-trimpath-nobuildid-go1.26.4)
        qualified_digest='0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6'
        ;;
    *)
        printf '%s\n' 'no reviewed binary digest exists for this platform and build method' >&2
        exit 1
        ;;
esac

tool_directory="$destination/bd-v${qualified_version}-${goos}-${goarch}-gms_pure_go"
target="$tool_directory/bd"

verify_target() {
    [ -f "$target" ] && [ -x "$target" ] || return 1
    observed_digest=$(sha256sum -- "$target" | awk '{print $1}')
    [ "$observed_digest" = "$qualified_digest" ] || return 1
    identity=$(BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$target" version --json)
    printf '%s' "$identity" | grep -F '"schema_version": 1' >/dev/null
    printf '%s' "$identity" | grep -F '"version": "1.2.2"' >/dev/null
}

emit_result() {
    printf '{"schema_version":1,"version":"%s","digest_sha256":"%s","executable_path":"%s","source_version":"v%s","source_commit":"%s","goos":"%s","goarch":"%s","build_method":"%s"}\n' \
        "$qualified_version" "$qualified_digest" "$target" "$qualified_version" "$qualified_source_commit" \
        "$goos" "$goarch" "$qualified_build_method"
}

if [ -e "$target" ]; then
    if verify_target; then
        emit_result
        exit 0
    fi
    printf '%s\n' 'existing target does not match the reviewed digest and version; choose a new destination' >&2
    exit 1
fi

[ "$offline" = 'false' ] || {
    printf '%s\n' 'offline installation requires an already verified target' >&2
    exit 1
}

observed_go_version=$(go version | awk '{print $3}')
[ "$observed_go_version" = "$qualified_go_version" ] || {
    printf '%s\n' 'the reviewed build requires Go 1.26.4 exactly' >&2
    exit 1
}
command -v gcc >/dev/null 2>&1 || {
    printf '%s\n' 'the reviewed CGO build requires a C compiler' >&2
    exit 1
}

install_scratch=$(mktemp -d "${TMPDIR:-/tmp}/spec-kitty-beads-install.XXXXXX")
cleanup_install_scratch() {
    if [ -d "$install_scratch/modcache" ]; then
        GOMODCACHE="$install_scratch/modcache" go clean -modcache >/dev/null 2>&1 || true
    fi
    chmod -R u+w -- "$install_scratch" 2>/dev/null || true
    rm -rf -- "$install_scratch"
}
trap cleanup_install_scratch EXIT HUP INT TERM

module_record="$install_scratch/module.json"
GOMODCACHE="$install_scratch/modcache" \
GOCACHE="$install_scratch/gocache" \
GOPATH="$install_scratch/gopath" \
go mod download -json "github.com/steveyegge/beads@v${qualified_version}" >"$module_record"
grep -F "\"Sum\": \"$qualified_module_sum\"" "$module_record" >/dev/null || {
    printf '%s\n' 'module checksum does not match the reviewed source archive' >&2
    exit 1
}
grep -F "\"Hash\": \"$qualified_source_commit\"" "$module_record" >/dev/null || {
    printf '%s\n' 'module source commit does not match the reviewed tag identity' >&2
    exit 1
}

mkdir -m 700 -- "$install_scratch/out"
GOBIN="$install_scratch/out" \
GOMODCACHE="$install_scratch/modcache" \
GOCACHE="$install_scratch/gocache" \
GOPATH="$install_scratch/gopath" \
CGO_ENABLED=1 \
GOFLAGS='-tags=gms_pure_go -trimpath -ldflags=-buildid=' \
GOTOOLCHAIN="$qualified_go_version" \
go install "github.com/steveyegge/beads/cmd/bd@v${qualified_version}"

built="$install_scratch/out/bd"
built_digest=$(sha256sum -- "$built" | awk '{print $1}')
[ "$built_digest" = "$qualified_digest" ] || {
    printf '%s\n' 'built binary digest is not allowlisted for this toolchain and platform' >&2
    exit 1
}

mkdir -m 700 -- "$tool_directory"
mv -- "$built" "$target"
chmod 700 -- "$target"
verify_target || {
    printf '%s\n' 'installed binary failed its post-install identity check' >&2
    exit 1
}
emit_result
