#!/bin/sh
set -eu

repository=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
candidate=${M2_CANDIDATE:-HEAD}

for tool in git go jq sha256sum tar strings readelf ldd find sort awk sed grep; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "M2 qualification requires $tool" >&2
    exit 1
  }
done

git -C "$repository" diff --quiet
git -C "$repository" diff --cached --quiet
commit=$(git -C "$repository" rev-parse --verify "$candidate^{commit}")
tree=$(git -C "$repository" rev-parse --verify "$candidate^{tree}")

if [ "$#" -gt 1 ]; then
  echo "usage: $0 [new-artifact-directory]" >&2
  exit 2
fi
if [ "$#" -eq 1 ]; then
  artifacts=$1
  mkdir -- "$artifacts"
else
  artifacts=$(mktemp -d /tmp/go-kitty3-m2-absence.XXXXXX)
fi
artifacts=$(CDPATH= cd -- "$artifacts" && pwd -P)
source_root=$artifacts/source
cache_root=$artifacts/cache
build_root=$artifacts/build
install_root=$artifacts/install
evidence_root=$artifacts/evidence
mkdir -p -- "$source_root" "$cache_root/mod" "$cache_root/build" "$cache_root/gopath" "$build_root" "$install_root/bin" "$evidence_root"

git -C "$repository" archive --format=tar "$commit" | tar -xf - -C "$source_root"
archive_tree=$(git -C "$source_root" init -q && git -C "$source_root" add -A && git -C "$source_root" write-tree)
if [ "$archive_tree" != "$tree" ]; then
  echo "archived source tree mismatch: got=$archive_tree want=$tree" >&2
  exit 1
fi

export GOMODCACHE=$cache_root/mod
export GOCACHE=$cache_root/build
export GOPATH=$cache_root/gopath
export GOTOOLCHAIN=local
export GOFLAGS=-buildvcs=false
cd -- "$source_root"

go_version=$(go version)
go_env=$(go env GOOS GOARCH GOVERSION | paste -sd / -)
go_mod_before=$(sha256sum go.mod | awk '{print $1}')
go_sum_before=$(sha256sum go.sum | awk '{print $1}')

go mod download
go mod verify >"$evidence_root/module-verify.txt"
go mod tidy
test "$go_mod_before" = "$(sha256sum go.mod | awk '{print $1}')"
test "$go_sum_before" = "$(sha256sum go.sum | awk '{print $1}')"

go list -deps -test ./... | LC_ALL=C sort -u >"$evidence_root/package-graph.txt"
go list -m -json all | jq -s 'sort_by(.Path)' >"$evidence_root/modules.json"
go mod graph | LC_ALL=C sort -u >"$evidence_root/module-graph.txt"

for forbidden in \
  modernc.org/sqlite modernc.org/libc modernc.org/mathutil modernc.org/memory \
  modernc.org/cc/v4 modernc.org/ccgo/v4 modernc.org/fileutil modernc.org/gc/v2 \
  modernc.org/gc/v3 modernc.org/goabi0 modernc.org/opt modernc.org/sortutil \
  modernc.org/strutil modernc.org/token github.com/ncruces/go-strftime \
  github.com/remyoudompheng/bigfft database/sql; do
  if grep -Fqi -- "$forbidden" "$evidence_root/package-graph.txt" "$evidence_root/modules.json" "$evidence_root/module-graph.txt"; then
    echo "forbidden dependency in resolved graph: $forbidden" >&2
    exit 1
  fi
done

go test ./internal/architecture -count=1 \
  -run '^(TestM2SQLiteAbsence|TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$' \
  -v >"$evidence_root/absence-tests.txt"
go test ./cmd/spec-kitty ./internal/controlroot -count=1 >"$evidence_root/runtime-tests.txt"

go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./cmd/... | sed '/^$/d' | LC_ALL=C sort >"$evidence_root/commands.txt"
while IFS= read -r package; do
  name=${package##*/}
  go build -trimpath -o "$build_root/$name" "$package"
done <"$evidence_root/commands.txt"

GOBIN=$install_root/bin go install -trimpath ./cmd/...
sed 's#.*/##' "$evidence_root/commands.txt" | LC_ALL=C sort >"$evidence_root/expected-install.txt"
find "$install_root" -type f -printf '%P\n' | sed 's#^bin/##' | LC_ALL=C sort >"$evidence_root/observed-install.txt"
cmp "$evidence_root/expected-install.txt" "$evidence_root/observed-install.txt"

: >"$evidence_root/binary-inspection.txt"
find "$build_root" -maxdepth 1 -type f -print | LC_ALL=C sort >"$evidence_root/binaries.txt"
while IFS= read -r binary; do
  name=${binary##*/}
  go version -m "$binary" >"$evidence_root/$name.version.txt"
  go tool nm "$binary" >"$evidence_root/$name.symbols.txt"
  strings -a "$binary" >"$evidence_root/$name.strings.txt"
  readelf -d -W "$binary" >"$evidence_root/$name.dynamic.txt"
  ldd "$binary" >"$evidence_root/$name.libraries.txt" 2>&1 || true
  "$binary" --help >"$evidence_root/$name.help.txt" 2>&1 || true
  for forbidden in modernc.org/sqlite github.com/mattn/go-sqlite3 zombiezen.com/go/sqlite sqlite_master 'pragma journal_mode' 'database/sql'; do
    if grep -Fqi -- "$forbidden" \
      "$evidence_root/$name.version.txt" "$evidence_root/$name.symbols.txt" \
      "$evidence_root/$name.strings.txt" "$evidence_root/$name.dynamic.txt" \
      "$evidence_root/$name.libraries.txt"; then
      echo "forbidden implementation signature in $name: $forbidden" >&2
      exit 1
    fi
  done
  if grep -Eqi -- '(^|[[:space:]])(--db|--backend)([=[:space:]]|$)|sqlite' "$evidence_root/$name.help.txt"; then
    echo "forbidden authority selector in $name help" >&2
    exit 1
  fi
  printf '%s\t%s\n' "$name" "$(sha256sum "$binary" | awk '{print $1}')" >>"$evidence_root/binary-inspection.txt"
done <"$evidence_root/binaries.txt"

awk '{print $1; print $2}' "$evidence_root/module-graph.txt" | LC_ALL=C sort -u >"$evidence_root/sbom-nodes.txt"
jq -Rn \
  --rawfile nodes "$evidence_root/sbom-nodes.txt" \
  --rawfile graph "$evidence_root/module-graph.txt" \
  --arg root "github.com/spec-kitty/go-kitty3" '
  def rows($s): $s | split("\n") | map(select(length > 0));
  (rows($nodes)) as $nodes |
  (rows($graph) | map(split(" ") | {from: .[0], to: .[1]})) as $edges |
  {bomFormat:"CycloneDX", specVersion:"1.5", serialNumber:null, version:1,
   metadata:{component:{type:"application", name:$root, "bom-ref":$root}},
   components:($nodes | map({type:"library", name:., "bom-ref":.})),
   dependencies:($nodes | map(. as $node | {ref:$node, dependsOn:([$edges[] | select(.from == $node) | .to] | sort)}))}
  ' | jq -S . >"$evidence_root/sbom.cdx.json"

if grep -Eqi -- 'sqlite|modernc\.org/(libc|mathutil|memory|cc|ccgo|fileutil|gc|goabi0|opt|sortutil|strutil|token)' "$evidence_root/sbom.cdx.json"; then
  echo "forbidden component or edge in SBOM" >&2
  exit 1
fi

go mod vendor
GOPROXY=off GOSUMDB=off GOFLAGS='-buildvcs=false -mod=vendor' go list -deps -test ./... \
  | LC_ALL=C sort -u >"$evidence_root/vendor-package-graph.txt"
if grep -Eqi -- 'sqlite|modernc\.org/(libc|mathutil|memory|cc|ccgo|fileutil|gc|goabi0|opt|sortutil|strutil|token)' "$evidence_root/vendor-package-graph.txt"; then
  echo "forbidden dependency in vendor-like offline graph" >&2
  exit 1
fi

find "$install_root" -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum >"$evidence_root/install-sha256.txt"
find "$evidence_root" -type f ! -name manifest.json -print0 | LC_ALL=C sort -z | xargs -0 sha256sum >"$evidence_root/evidence-sha256.txt"

jq -n -S \
  --arg commit "$commit" --arg tree "$tree" --arg go_version "$go_version" --arg go_env "$go_env" \
  --arg go_mod_sha256 "$go_mod_before" --arg go_sum_sha256 "$go_sum_before" \
  --arg sbom_sha256 "$(sha256sum "$evidence_root/sbom.cdx.json" | awk '{print $1}')" \
  --arg artifact_root "$artifacts" \
  '{schema_version:1, verdict:"pass", source:{commit:$commit,tree:$tree,go_mod_sha256:$go_mod_sha256,go_sum_sha256:$go_sum_sha256},toolchain:{version:$go_version,environment:$go_env},qualification:{source:true,module:true,vendor_like:true,mutants:30,binaries:true,sbom:true,install:true,help:true,runtime:true,historical_exemptions:"closed-token-and-path-map"},sbom_sha256:$sbom_sha256,artifact_root:$artifact_root}' \
  >"$evidence_root/manifest.json"

printf 'M2 absence qualification passed\ncommit=%s\ntree=%s\nevidence=%s\n' "$commit" "$tree" "$evidence_root/manifest.json"
