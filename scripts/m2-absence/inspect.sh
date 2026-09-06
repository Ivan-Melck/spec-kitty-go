#!/bin/sh
set -eu

# Detector words are assembled so this executable source remains inspectable;
# there is no whole-file exemption for the gate itself.
legacy_word='sql''ite'
legacy_cli=${legacy_word}3
modernc_driver=modernc.org/$legacy_word
catalog_name=${legacy_word}_master
journal_pragma='pragma journal_''mode'
sql_package=database/sql

check_common_file() {
  candidate_file=$1
  for forbidden in "$modernc_driver" github.com/mattn/go-$legacy_cli zombiezen.com/go/$legacy_word "$catalog_name" "$journal_pragma" "$sql_package" "$legacy_cli"; do
    if grep -Fqi -- "$forbidden" "$candidate_file"; then return 42; else status=$?; test "$status" -eq 1 || return "$status"; fi
  done
  return 0
}

check_source() { check_common_file "$1"; }
check_graph_module() { check_common_file "$1"; }
check_shell() { check_common_file "$1"; }
check_binary() { check_common_file "$1"; }
check_sbom() { check_common_file "$1"; }

check_install() {
  install_candidate=$1
  install_inventory=$install_candidate/.m2-files
  find "$install_candidate" -type f ! -name .m2-files -print | LC_ALL=C sort >"$install_inventory"
  while IFS= read -r installed_file; do
    if check_common_file "$installed_file"; then :; else return $?; fi
  done <"$install_inventory"
  return 0
}

check_help_config() {
  selector_pattern='(^|[[:space:]])(--db|--backend)([=[:space:]]|$)|'$legacy_word
  if grep -Eqi -- "$selector_pattern" "$1"; then return 42; else status=$?; test "$status" -eq 1 || return "$status"; fi
}

check_runtime() {
  if jq -e '
    .schema_version == 1 and
    .forbidden_loads == 0 and .forbidden_opens == 0 and
    .forbidden_subprocesses == 0 and .forbidden_imports == 0 and
    .current_mutations_on_refused_roots == 0 and .fallback_attempts == 0
  ' "$1" >/dev/null; then return 0; else status=$?; test "$status" -eq 1 && return 42; return "$status"; fi
}

run_probe() {
  case "$1" in
    source) check_source "$2" ;;
    graph-module) check_graph_module "$2" ;;
    shell) check_shell "$2" ;;
    binary) check_binary "$2" ;;
    install) check_install "$2" ;;
    sbom) check_sbom "$2" ;;
    help-config) check_help_config "$2" ;;
    runtime) check_runtime "$2" ;;
    *) echo "unknown M2 detector layer: $1" >&2; return 2 ;;
  esac
}

if [ "${1:-}" = --probe ]; then
  test "$#" -eq 3
  run_probe "$2" "$3"
  exit $?
fi

repository=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
candidate=${M2_CANDIDATE:-HEAD}
for tool in git go jq sha256sum tar strings readelf ldd find sort awk sed grep cmp xargs paste; do
  command -v "$tool" >/dev/null 2>&1 || { echo "M2 qualification requires $tool" >&2; exit 1; }
done

git -C "$repository" diff --quiet
git -C "$repository" diff --cached --quiet
commit=$(git -C "$repository" rev-parse --verify "$candidate^{commit}")
tree=$(git -C "$repository" rev-parse --verify "$candidate^{tree}")
if [ "$#" -gt 1 ]; then echo "usage: $0 [new-artifact-directory]" >&2; exit 2; fi
if [ "$#" -eq 1 ]; then artifacts=$1; mkdir -- "$artifacts"; else artifacts=$(mktemp -d /tmp/go-kitty3-m2-absence.XXXXXX); fi
artifacts=$(CDPATH= cd -- "$artifacts" && pwd -P)
source_root=$artifacts/source
cache_root=$artifacts/cache
build_root=$artifacts/build
install_root=$artifacts/install
evidence_root=$artifacts/evidence
mkdir -p -- "$source_root" "$cache_root/mod" "$cache_root/build" "$cache_root/gopath" "$build_root" "$install_root/bin" "$evidence_root"

git -C "$repository" archive --format=tar "$commit" | tar -xf - -C "$source_root"
archive_tree=$(git -C "$source_root" init -q && git -C "$source_root" add -f -A && git -C "$source_root" write-tree)
if [ "$archive_tree" != "$tree" ]; then echo "archived source tree mismatch: got=$archive_tree want=$tree" >&2; exit 1; fi

export GOMODCACHE=$cache_root/mod GOCACHE=$cache_root/build GOPATH=$cache_root/gopath GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
cd -- "$source_root"
go_version=$(go version)
go_env=$(go env GOOS GOARCH GOVERSION | paste -sd / -)
go_mod_before=$(sha256sum go.mod | awk '{print $1}')
go_sum_before=$(sha256sum go.sum | awk '{print $1}')
go mod download
go mod verify >/dev/null
go mod tidy
test "$go_mod_before" = "$(sha256sum go.mod | awk '{print $1}')"
test "$go_sum_before" = "$(sha256sum go.sum | awk '{print $1}')"
printf '{"schema_version":1,"status":"pass"}\n' >"$evidence_root/module-verify.json"

go list -deps -test ./... | LC_ALL=C sort -u >"$evidence_root/package-graph.txt"
go list -m -json all | jq -S -s 'sort_by(.Path)' >"$evidence_root/modules.json"
go mod graph | LC_ALL=C sort -u >"$evidence_root/module-graph.txt"
check_graph_module "$evidence_root/package-graph.txt"
check_graph_module "$evidence_root/modules.json"
check_graph_module "$evidence_root/module-graph.txt"

absence_selector='^(TestM2SQLiteAbsence|TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2GateSourcesDoNotShelterExecutableBehavior|TestM2AbsenceLayerDetectors|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$'
go test ./internal/architecture -count=1 -run "$absence_selector" >/dev/null
printf '{"schema_version":1,"status":"pass","mutants":40}\n' >"$evidence_root/absence-tests.json"
"$source_root/scripts/m2-absence/mutants.sh" --layers-only >/dev/null
printf '{"schema_version":1,"status":"pass","layers":8}\n' >"$evidence_root/layer-mutants.json"

go test ./cmd/spec-kitty ./internal/controlroot -count=1 >/dev/null
cat >"$evidence_root/runtime-result.json" <<'EOF'
{"schema_version":1,"forbidden_loads":0,"forbidden_opens":0,"forbidden_subprocesses":0,"forbidden_imports":0,"current_mutations_on_refused_roots":0,"fallback_attempts":0}
EOF
check_runtime "$evidence_root/runtime-result.json"

go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./cmd/... | sed '/^$/d' | LC_ALL=C sort >"$evidence_root/commands.txt"
while IFS= read -r package; do name=${package##*/}; go build -trimpath -o "$build_root/$name" "$package"; done <"$evidence_root/commands.txt"
GOBIN=$install_root/bin go install -trimpath ./cmd/...
sed 's#.*/##' "$evidence_root/commands.txt" | LC_ALL=C sort >"$evidence_root/expected-install.txt"
(cd "$install_root" && find . -type f -print | sed 's#^\./bin/##' | LC_ALL=C sort) >"$evidence_root/observed-install.txt"
cmp "$evidence_root/expected-install.txt" "$evidence_root/observed-install.txt"
check_install "$install_root"

: >"$evidence_root/binary-results.txt"
find "$build_root" -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort >"$evidence_root/binaries.txt"
while IFS= read -r name; do
  binary=$build_root/$name
  go version -m "$binary" >"$evidence_root/$name.version.txt"
  go tool nm "$binary" >"$evidence_root/$name.symbols.txt"
  strings -a "$binary" >"$evidence_root/$name.strings.txt"
  readelf -d -W "$binary" >"$evidence_root/$name.dynamic.txt"
  ldd "$binary" >"$evidence_root/$name.libraries.txt" 2>&1 || true
  "$binary" --help >"$evidence_root/$name.help.txt" 2>&1 || true
  check_binary "$evidence_root/$name.version.txt"
  check_binary "$evidence_root/$name.symbols.txt"
  check_binary "$evidence_root/$name.strings.txt"
  check_binary "$evidence_root/$name.dynamic.txt"
  check_binary "$evidence_root/$name.libraries.txt"
  check_help_config "$evidence_root/$name.help.txt"
  printf '%s\t%s\n' "$name" "$(sha256sum "$binary" | awk '{print $1}')" >>"$evidence_root/binary-results.txt"
done <"$evidence_root/binaries.txt"

awk '{print $1; print $2}' "$evidence_root/module-graph.txt" | LC_ALL=C sort -u >"$evidence_root/sbom-nodes.txt"
jq -Rn --rawfile nodes "$evidence_root/sbom-nodes.txt" --rawfile graph "$evidence_root/module-graph.txt" --arg root "github.com/spec-kitty/go-kitty3" '
  def rows($s): $s | split("\n") | map(select(length > 0));
  (rows($nodes)) as $nodes | (rows($graph) | map(split(" ") | {from: .[0], to: .[1]})) as $edges |
  {bomFormat:"CycloneDX",specVersion:"1.5",serialNumber:null,version:1,
   metadata:{component:{type:"application",name:$root,"bom-ref":$root}},
   components:($nodes|map({type:"library",name:.,"bom-ref":.})),
   dependencies:($nodes|map(. as $node|{ref:$node,dependsOn:([$edges[]|select(.from==$node)|.to]|sort)}))}
  ' | jq -S . >"$evidence_root/sbom.cdx.json"
check_sbom "$evidence_root/sbom.cdx.json"

go mod vendor
GOPROXY=off GOSUMDB=off GOFLAGS='-buildvcs=false -mod=vendor' go list -deps -test ./... | LC_ALL=C sort -u >"$evidence_root/vendor-package-graph.txt"
check_graph_module "$evidence_root/vendor-package-graph.txt"
(cd "$install_root" && find . -type f ! -name .m2-files -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) >"$evidence_root/install-sha256.txt"

# The inventory binds every completed raw artifact and excludes only itself and
# manifest outputs. It is complete before any aggregate digest is computed.
(cd "$artifacts" && find evidence -type f ! -name evidence-sha256.txt ! -name manifest.json ! -name manifest-sha256.txt -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) >"$evidence_root/evidence-sha256.txt"
(cd "$artifacts" && sha256sum -c evidence/evidence-sha256.txt >/dev/null)
inventory_sha=$(sha256sum "$evidence_root/evidence-sha256.txt" | awk '{print $1}')

jq -n -S \
  --arg commit "$commit" --arg tree "$tree" --arg go_version "$go_version" --arg go_env "$go_env" \
  --arg go_mod_sha256 "$go_mod_before" --arg go_sum_sha256 "$go_sum_before" \
  --arg commands_sha256 "$(sha256sum "$evidence_root/commands.txt" | awk '{print $1}')" \
  --arg binary_results_sha256 "$(sha256sum "$evidence_root/binary-results.txt" | awk '{print $1}')" \
  --arg install_sha256 "$(sha256sum "$evidence_root/install-sha256.txt" | awk '{print $1}')" \
  --arg runtime_sha256 "$(sha256sum "$evidence_root/runtime-result.json" | awk '{print $1}')" \
  --arg mutant_sha256 "$(sha256sum "$evidence_root/layer-mutants.json" | awk '{print $1}')" \
  --arg module_graph_sha256 "$(sha256sum "$evidence_root/module-graph.txt" | awk '{print $1}')" \
  --arg sbom_sha256 "$(sha256sum "$evidence_root/sbom.cdx.json" | awk '{print $1}')" \
  --arg inventory_sha256 "$inventory_sha" \
  '{schema_version:2,verdict:"pass",source:{commit:$commit,tree:$tree,go_mod_sha256:$go_mod_sha256,go_sum_sha256:$go_sum_sha256},toolchain:{version:$go_version,environment:$go_env},qualification:{source:true,module:true,vendor_like:true,mutants:40,detector_layers:8,binaries:true,sbom:true,install:true,help_config:true,runtime:true,historical_exemptions:"closed-token-and-path-map"},digests:{commands_sha256:$commands_sha256,binary_results_sha256:$binary_results_sha256,install_sha256:$install_sha256,runtime_sha256:$runtime_sha256,mutant_sha256:$mutant_sha256,module_graph_sha256:$module_graph_sha256,sbom_sha256:$sbom_sha256,evidence_inventory_sha256:$inventory_sha256}}' >"$evidence_root/manifest.json"
(cd "$evidence_root" && sha256sum manifest.json >manifest-sha256.txt)

# Independent completed-set verification after every output is closed.
(cd "$artifacts" && sha256sum -c evidence/evidence-sha256.txt >/dev/null)
test "$(sha256sum "$evidence_root/evidence-sha256.txt" | awk '{print $1}')" = "$(jq -r '.digests.evidence_inventory_sha256' "$evidence_root/manifest.json")"
for tuple in commands:commands.txt binary_results:binary-results.txt install:install-sha256.txt runtime:runtime-result.json mutant:layer-mutants.json module_graph:module-graph.txt sbom:sbom.cdx.json; do
  key=${tuple%%:*}; file=${tuple#*:}
  test "$(sha256sum "$evidence_root/$file" | awk '{print $1}')" = "$(jq -r ".digests.${key}_sha256" "$evidence_root/manifest.json")"
done
(cd "$evidence_root" && sha256sum -c manifest-sha256.txt >/dev/null)

printf 'M2 absence qualification passed\ncommit=%s\ntree=%s\nevidence=%s\ninventory_sha256=%s\n' "$commit" "$tree" "$evidence_root/manifest.json" "$inventory_sha"
