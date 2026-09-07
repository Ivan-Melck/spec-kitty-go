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
qualification_sidecar_sha=947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad
script_directory=${0%/*}
if [ "$script_directory" = "$0" ]; then script_directory=.; fi
CDPATH= cd -- "$script_directory/../.."
repository=$PWD
capture_root=${TMPDIR:-/tmp}/go-kitty3-m2-absence-capture.$$
(umask 077 && mkdir -- "$capture_root")
capture_file=$capture_root/value
trap 'rm -f -- "$capture_file"; rmdir -- "$capture_root" 2>/dev/null || true' EXIT HUP INT TERM

check_admitted_sidecar() {
  sidecar=${SPEC_KITTY_WP15_GAPDBD:-}
  case "$sidecar" in /*) ;; *) return 64 ;; esac
  test -f "$sidecar" && test -x "$sidecar" || return 64
  sha256sum "$sidecar" >"$capture_file" || return 64
  IFS=' ' read -r observed_sidecar_sha ignored_sidecar_path <"$capture_file" || return 64
  test "$observed_sidecar_sha" = "$qualification_sidecar_sha" || return 64
}

check_common_file() {
  candidate_file=$1
  for forbidden in "$modernc_driver" github.com/mattn/go-$legacy_cli zombiezen.com/go/$legacy_word "$catalog_name" "$journal_pragma" "$sql_package" "$legacy_cli"; do
    if grep -Fqi -- "$forbidden" "$candidate_file"; then return 42; else status=$?; test "$status" -eq 1 || return "$status"; fi
  done
  return 0
}

check_source() { check_common_file "$1"; }
check_graph_module() { check_common_file "$1"; }
check_shell() {
  if (cd "$repository" && M2_SHELL_PROBE="$1" go test ./internal/architecture -run '^TestM2ShellProbe$' -count=1 >/dev/null); then
    return 0
  fi
  return 42
}
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
    .schema_version == 2 and .verdict == "pass" and
    (.expected|length) > 0 and .run == .expected and .passed == .expected and
    (.skipped|length) == 0 and (.failed|length) == 0 and (.unmatched|length) == 0
  ' "$1" >/dev/null; then return 0; else status=$?; test "$status" -eq 1 && return 42; return "$status"; fi
}

derive_receipts() {
  expected_file=$1
  raw_file=$2
  result_file=$3
  receipt_kind=$4
  sha256sum "$raw_file" >"$capture_file"
  IFS=' ' read -r raw_sha ignored_raw_path <"$capture_file"
  jq -s -S --rawfile expected_text "$expected_file" --arg receipt_kind "$receipt_kind" --arg raw_sha "$raw_sha" \
    -f "$source_root/scripts/m2-absence/receipt-result.jq" "$raw_file" >"$result_file"
  jq -e '.verdict == "pass"' "$result_file" >/dev/null
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
if [ "${1:-}" = --check-sidecar ]; then
  test "$#" -eq 1
  check_admitted_sidecar
  exit $?
fi

candidate=${M2_CANDIDATE:-HEAD}
for tool in git go jq sha256sum tar strings readelf ldd find sort awk sed grep cmp xargs paste; do
  command -v "$tool" >/dev/null 2>&1 || { echo "M2 qualification requires $tool" >&2; exit 1; }
done
check_admitted_sidecar

git -C "$repository" diff --quiet
git -C "$repository" diff --cached --quiet
git -C "$repository" rev-parse --verify "$candidate^{commit}" >"$capture_file"
IFS= read -r commit <"$capture_file"
git -C "$repository" rev-parse --verify "$candidate^{tree}" >"$capture_file"
IFS= read -r tree <"$capture_file"
if [ "$#" -gt 1 ]; then echo "usage: $0 [new-artifact-directory]" >&2; exit 2; fi
if [ "$#" -eq 1 ]; then
  artifacts=$1
  mkdir -- "$artifacts"
else
  mktemp -d /tmp/go-kitty3-m2-absence.XXXXXX >"$capture_file"
  IFS= read -r artifacts <"$capture_file"
fi
(CDPATH= cd -- "$artifacts" && pwd -P) >"$capture_file"
IFS= read -r artifacts <"$capture_file"
source_root=$artifacts/source
cache_root=$artifacts/cache
build_root=$artifacts/build
install_root=$artifacts/install
evidence_root=$artifacts/evidence
mkdir -p -- "$source_root" "$cache_root/mod" "$cache_root/build" "$cache_root/gopath" "$build_root" "$install_root/bin" "$evidence_root"

git -C "$repository" archive --format=tar "$commit" | tar -xf - -C "$source_root"
git -C "$source_root" init -q
git -C "$source_root" add -f -A
git -C "$source_root" write-tree >"$capture_file"
IFS= read -r archive_tree <"$capture_file"
if [ "$archive_tree" != "$tree" ]; then echo "archived source tree mismatch: got=$archive_tree want=$tree" >&2; exit 1; fi

export GOMODCACHE=$cache_root/mod GOCACHE=$cache_root/build GOPATH=$cache_root/gopath GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
cd -- "$source_root"
go version >"$capture_file"
IFS= read -r go_version <"$capture_file"
go env GOOS GOARCH GOVERSION | paste -sd / - >"$capture_file"
IFS= read -r go_env <"$capture_file"
sha256sum go.mod >"$capture_file"
IFS=' ' read -r go_mod_before ignored_mod_path <"$capture_file"
sha256sum go.sum >"$capture_file"
IFS=' ' read -r go_sum_before ignored_sum_path <"$capture_file"
go mod download
go mod verify >/dev/null
go mod tidy
sha256sum go.mod >"$capture_file"
IFS=' ' read -r go_mod_after ignored_mod_path <"$capture_file"
test "$go_mod_before" = "$go_mod_after"
sha256sum go.sum >"$capture_file"
IFS=' ' read -r go_sum_after ignored_sum_path <"$capture_file"
test "$go_sum_before" = "$go_sum_after"
printf '{"schema_version":1,"status":"pass"}\n' >"$evidence_root/module-verify.json"

go list -deps -test ./... | LC_ALL=C sort -u >"$evidence_root/package-graph.txt"
go list -m -json all | jq -S -s 'sort_by(.Path)' >"$evidence_root/modules.json"
go mod graph | LC_ALL=C sort -u >"$evidence_root/module-graph.txt"
check_graph_module "$evidence_root/package-graph.txt"
check_graph_module "$evidence_root/modules.json"
check_graph_module "$evidence_root/module-graph.txt"

absence_selector='^(TestM2SQLiteAbsence|TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2GateSourcesDoNotShelterExecutableBehavior|TestM2SplitExecutableTokensFailClosedWithoutOverNormalization|TestM2AbsenceLayerDetectors|TestM2ObservedReceiptVerifier|TestM2QualificationRequiresAdmittedSidecar|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$'
if ! go test -json ./internal/architecture -count=1 -run "$absence_selector" >"$evidence_root/absence-events.jsonl"; then
  echo "required absence tests failed" >&2
  exit 1
fi
derive_receipts "$source_root/scripts/m2-absence/expected-absence-tests.txt" "$evidence_root/absence-events.jsonl" "$evidence_root/absence-tests.json" absence
if ! (cd -- "$source_root" && scripts/m2-absence/mutants.sh --layers-json) >"$evidence_root/layer-mutant-events.jsonl"; then
  echo "required detector-layer mutants failed" >&2
  exit 1
fi
derive_receipts "$source_root/scripts/m2-absence/expected-layer-mutants.txt" "$evidence_root/layer-mutant-events.jsonl" "$evidence_root/layer-mutants.json" detector-layer

runtime_selector='^(TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation|TestHistoricalAndUnsafeRootsRefuseBeforeInstallationOrMutation|TestRealGapDBMutationInitializesAndQueriesRemainPureAcrossRestart|TestRealGapDBLostAcknowledgementRestartResumeAndConflict|TestCurrentRootAcceptsIdentityReservationGenerationAheadOfManifest|TestHistoricalSQLiteSignatureAndCompanions|TestClassificationIsReadOnly)$'
jq -n '[
  "TestClassificationIsReadOnly",
  "TestCurrentRootAcceptsIdentityReservationGenerationAheadOfManifest",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/acceptance",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/effect",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/execution",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/handoff",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/integration",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/integration_cleanup",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/mission",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/readiness",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/review",
  "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/workspace_rebind",
  "TestHistoricalAndUnsafeRootsRefuseBeforeInstallationOrMutation",
  "TestHistoricalSQLiteSignatureAndCompanions",
  "TestHistoricalSQLiteSignatureAndCompanions/exact",
  "TestHistoricalSQLiteSignatureAndCompanions/offset-one",
  "TestHistoricalSQLiteSignatureAndCompanions/one-byte-short",
  "TestHistoricalSQLiteSignatureAndCompanions/phrase",
  "TestHistoricalSQLiteSignatureAndCompanions/random-near-prefix",
  "TestHistoricalSQLiteSignatureAndCompanions/wrong-nul",
  "TestRealGapDBLostAcknowledgementRestartResumeAndConflict",
  "TestRealGapDBMutationInitializesAndQueriesRemainPureAcrossRestart"
]' >"$evidence_root/runtime-expected.json"
if ! SPEC_KITTY_WP15_GAPDBD="$sidecar" go test -json -count=1 -run "$runtime_selector" \
  ./cmd/spec-kitty ./internal/helpercmd ./internal/controlroot >"$evidence_root/runtime-events.jsonl"; then
  echo "required production runtime tests failed" >&2
  exit 1
fi
sha256sum "$evidence_root/runtime-events.jsonl" >"$capture_file"
IFS=' ' read -r runtime_raw_sha ignored_runtime_path <"$capture_file"
jq -s -S --slurpfile expected_file "$evidence_root/runtime-expected.json" --arg sidecar_sha "$qualification_sidecar_sha" --arg raw_sha "$runtime_raw_sha" '
  ($expected_file[0]) as $expected |
  [.[] | select(.Test != null)] as $events |
  ([$events[] | select(.Action == "run") | .Test] | sort) as $run |
  ([$events[] | select(.Action == "pass") | .Test] | sort) as $pass |
  ([$events[] | select(.Action == "skip") | .Test] | sort) as $skipped |
  ([$events[] | select(.Action == "fail") | .Test] | sort) as $failed |
  (($run + $pass + $skipped + $failed) | map(select(. as $name | $expected | index($name) | not)) | unique) as $unmatched |
  {schema_version:2, expected:$expected, run:$run, passed:$pass, skipped:$skipped, failed:$failed,
   unmatched:$unmatched, sidecar_sha256:$sidecar_sha, raw_events_sha256:$raw_sha,
   verdict:(if $run == $expected and $pass == $expected and ($skipped|length)==0 and ($failed|length)==0 and ($unmatched|length)==0 then "pass" else "fail" end)}
' "$evidence_root/runtime-events.jsonl" >"$evidence_root/runtime-result.json"
jq -e '.verdict == "pass" and (.run|length)==23 and (.passed|length)==23 and (.skipped|length)==0 and (.failed|length)==0 and (.unmatched|length)==0' "$evidence_root/runtime-result.json" >/dev/null
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
	  case "$name" in
	    spec-kitty) (cd -- "$build_root" && ./spec-kitty --help) >"$evidence_root/$name.help.txt" 2>&1 || true ;;
	    spk-m1-proving-ground) (cd -- "$build_root" && ./spk-m1-proving-ground --help) >"$evidence_root/$name.help.txt" 2>&1 || true ;;
	    spk-m2-qualification) (cd -- "$build_root" && ./spk-m2-qualification --help) >"$evidence_root/$name.help.txt" 2>&1 || true ;;
	    spk-phase1-helper) (cd -- "$build_root" && ./spk-phase1-helper --help) >"$evidence_root/$name.help.txt" 2>&1 || true ;;
    *) echo "unexpected command binary: $name" >&2; exit 1 ;;
  esac
  check_binary "$evidence_root/$name.version.txt"
  check_binary "$evidence_root/$name.symbols.txt"
  check_binary "$evidence_root/$name.strings.txt"
  check_binary "$evidence_root/$name.dynamic.txt"
  check_binary "$evidence_root/$name.libraries.txt"
  check_help_config "$evidence_root/$name.help.txt"
  sha256sum "$binary" >"$capture_file"
  IFS=' ' read -r binary_sha ignored_binary_path <"$capture_file"
  printf '%s\t%s\n' "$name" "$binary_sha" >>"$evidence_root/binary-results.txt"
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
sha256sum "$evidence_root/evidence-sha256.txt" >"$capture_file"
IFS=' ' read -r inventory_sha ignored_inventory_path <"$capture_file"

sha256sum "$evidence_root/commands.txt" >"$capture_file"
IFS=' ' read -r commands_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/binary-results.txt" >"$capture_file"
IFS=' ' read -r binary_results_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/install-sha256.txt" >"$capture_file"
IFS=' ' read -r install_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/runtime-result.json" >"$capture_file"
IFS=' ' read -r runtime_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/absence-tests.json" >"$capture_file"
IFS=' ' read -r absence_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/absence-events.jsonl" >"$capture_file"
IFS=' ' read -r absence_raw_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/layer-mutants.json" >"$capture_file"
IFS=' ' read -r layer_mutant_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/layer-mutant-events.jsonl" >"$capture_file"
IFS=' ' read -r layer_mutant_raw_sha ignored_digest_path <"$capture_file"
jq '.counts.expected' "$evidence_root/absence-tests.json" >"$capture_file"
IFS= read -r absence_receipts <"$capture_file"
jq '.counts.expected' "$evidence_root/layer-mutants.json" >"$capture_file"
IFS= read -r detector_layers <"$capture_file"
sha256sum "$evidence_root/module-graph.txt" >"$capture_file"
IFS=' ' read -r module_graph_sha ignored_digest_path <"$capture_file"
sha256sum "$evidence_root/sbom.cdx.json" >"$capture_file"
IFS=' ' read -r sbom_sha ignored_digest_path <"$capture_file"

jq -n -S \
  --arg commit "$commit" --arg tree "$tree" --arg go_version "$go_version" --arg go_env "$go_env" \
  --arg go_mod_sha256 "$go_mod_before" --arg go_sum_sha256 "$go_sum_before" \
  --arg commands_sha256 "$commands_sha" \
  --arg binary_results_sha256 "$binary_results_sha" \
  --arg install_sha256 "$install_sha" \
  --arg runtime_sha256 "$runtime_sha" \
  --arg runtime_raw_sha256 "$runtime_raw_sha" \
  --arg absence_sha256 "$absence_sha" \
  --arg absence_raw_sha256 "$absence_raw_sha" \
  --arg layer_mutant_sha256 "$layer_mutant_sha" \
  --arg layer_mutant_raw_sha256 "$layer_mutant_raw_sha" \
  --argjson absence_receipts "$absence_receipts" \
  --argjson detector_layers "$detector_layers" \
  --arg module_graph_sha256 "$module_graph_sha" \
  --arg sbom_sha256 "$sbom_sha" \
  --arg inventory_sha256 "$inventory_sha" \
  '{schema_version:3,verdict:"pass",source:{commit:$commit,tree:$tree,go_mod_sha256:$go_mod_sha256,go_sum_sha256:$go_sum_sha256},toolchain:{version:$go_version,environment:$go_env},qualification:{source:true,module:true,vendor_like:true,absence_receipts:$absence_receipts,detector_layers:$detector_layers,binaries:true,sbom:true,install:true,help_config:true,runtime:"observed",runtime_tests:23,historical_exemptions:"closed-token-and-path-map"},digests:{commands_sha256:$commands_sha256,binary_results_sha256:$binary_results_sha256,install_sha256:$install_sha256,runtime_sha256:$runtime_sha256,runtime_raw_sha256:$runtime_raw_sha256,absence_sha256:$absence_sha256,absence_raw_sha256:$absence_raw_sha256,layer_mutant_sha256:$layer_mutant_sha256,layer_mutant_raw_sha256:$layer_mutant_raw_sha256,module_graph_sha256:$module_graph_sha256,sbom_sha256:$sbom_sha256,evidence_inventory_sha256:$inventory_sha256}}' >"$evidence_root/manifest.json"
(cd "$evidence_root" && sha256sum manifest.json >manifest-sha256.txt)

# Independent completed-set verification after every output is closed.
(cd "$artifacts" && sha256sum -c evidence/evidence-sha256.txt >/dev/null)
sha256sum "$evidence_root/evidence-sha256.txt" >"$capture_file"
IFS=' ' read -r observed_inventory_sha ignored_inventory_path <"$capture_file"
jq -r '.digests.evidence_inventory_sha256' "$evidence_root/manifest.json" >"$capture_file"
IFS= read -r manifest_inventory_sha <"$capture_file"
test "$observed_inventory_sha" = "$manifest_inventory_sha"
for tuple in commands:commands.txt binary_results:binary-results.txt install:install-sha256.txt runtime:runtime-result.json runtime_raw:runtime-events.jsonl absence:absence-tests.json absence_raw:absence-events.jsonl layer_mutant:layer-mutants.json layer_mutant_raw:layer-mutant-events.jsonl module_graph:module-graph.txt sbom:sbom.cdx.json; do
  key=${tuple%%:*}; file=${tuple#*:}
  sha256sum "$evidence_root/$file" >"$capture_file"
  IFS=' ' read -r observed_file_sha ignored_file_path <"$capture_file"
  jq -r ".digests.${key}_sha256" "$evidence_root/manifest.json" >"$capture_file"
  IFS= read -r manifest_file_sha <"$capture_file"
  test "$observed_file_sha" = "$manifest_file_sha"
done
(cd "$evidence_root" && sha256sum -c manifest-sha256.txt >/dev/null)

printf 'M2 absence qualification passed\ncommit=%s\ntree=%s\nevidence=%s\ninventory_sha256=%s\n' "$commit" "$tree" "$evidence_root/manifest.json" "$inventory_sha"
