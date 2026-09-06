#!/bin/sh
set -eu

script_directory=${0%/*}
if [ "$script_directory" = "$0" ]; then script_directory=.; fi
CDPATH= cd -- "$script_directory/../.."
repository=$PWD
cd -- "$repository"
receipt_mode=human
capture_root=${TMPDIR:-/tmp}/go-kitty3-m2-mutants-capture.$$
(umask 077 && mkdir -- "$capture_root")
capture_file=$capture_root/value
trap 'rm -f -- "$capture_file"; rmdir -- "$capture_root" 2>/dev/null || true' EXIT HUP INT TERM

emit_receipt() {
  action=$1
  layer=$2
  if [ "$receipt_mode" = json ]; then
    printf '{"Action":"%s","Test":"M2LayerMutant/%s"}\n' "$action" "$layer"
  fi
}

run_layers() {
  mktemp -d /tmp/go-kitty3-m2-mutants.XXXXXX >"$capture_file"
  IFS= read -r fixture_root <"$capture_file"
  legacy_word='sql''ite'
  driver=modernc.org/$legacy_word
  cli=${legacy_word}3
  printf 'package mutant\nimport _ "%s"\n' "$driver" >"$fixture_root/source.go"
  printf 'example.test/mutant %s@v1.0.0\n' "$driver" >"$fixture_root/module.graph"
  {
    printf '#!/bin/sh\nreview_retired_cli=sql\\ite3\n'
    line=0
    while [ "$line" -lt 65 ]; do printf '# intervening line\n'; line=$((line + 1)); done
    printf '/usr/bin/env "$review_retired_cli" /tmp/x\n'
  } >"$fixture_root/command.sh"
  printf 'ELF\\000%s\\000' "$driver" >"$fixture_root/binary"
  mkdir "$fixture_root/install"
  printf '%s' "$driver" >"$fixture_root/install/provider.bin"
  printf '{"bomFormat":"CycloneDX","components":[{"name":"%s"}]}' "$driver" >"$fixture_root/sbom.json"
  printf 'Usage: tool --backend %s\n' "$legacy_word" >"$fixture_root/help.txt"
  printf '{"schema_version":2,"expected":["T"],"run":["T"],"passed":[],"skipped":["T"],"failed":[],"unmatched":[],"verdict":"fail"}\n' >"$fixture_root/runtime.json"

  for layer in source graph-module shell binary install sbom help-config runtime; do
    emit_receipt run "$layer"
    case "$layer" in
      source) target=$fixture_root/source.go ;;
      graph-module) target=$fixture_root/module.graph ;;
      shell) target=$fixture_root/command.sh ;;
      binary) target=$fixture_root/binary ;;
      install) target=$fixture_root/install ;;
      sbom) target=$fixture_root/sbom.json ;;
      help-config) target=$fixture_root/help.txt ;;
      runtime) target=$fixture_root/runtime.json ;;
    esac
    set +e
    scripts/m2-absence/inspect.sh --probe "$layer" "$target"
    status=$?
    set -e
    if [ "$status" -eq 0 ]; then emit_receipt fail "$layer"; echo "M2 detector mutant survived: $layer" >&2; exit 1; fi
    if [ "$status" -ne 42 ]; then emit_receipt fail "$layer"; echo "M2 detector failed instead of rejecting $layer: status=$status" >&2; exit 1; fi
    emit_receipt pass "$layer"
  done

  printf 'package native\nconst Store = "gapdb"\n' >"$fixture_root/clean.go"
  printf '{"schema_version":2,"expected":["T"],"run":["T"],"passed":["T"],"skipped":[],"failed":[],"unmatched":[],"verdict":"pass"}\n' >"$fixture_root/runtime-clean.json"
  scripts/m2-absence/inspect.sh --probe source "$fixture_root/clean.go"
  scripts/m2-absence/inspect.sh --probe runtime "$fixture_root/runtime-clean.json"
  if [ "$receipt_mode" = human ]; then printf 'M2 layer mutants passed\n'; fi
}

if [ "${1:-}" = --layers-only ]; then run_layers; exit 0; fi
if [ "${1:-}" = --layers-json ]; then receipt_mode=json; run_layers; exit 0; fi
cd -- "$repository"
run_layers
exec go test ./internal/architecture -count=1 \
  -run '^(TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2GateSourcesDoNotShelterExecutableBehavior|TestM2SplitExecutableTokensFailClosedWithoutOverNormalization|TestM2QualificationRequiresAdmittedSidecar|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$'
