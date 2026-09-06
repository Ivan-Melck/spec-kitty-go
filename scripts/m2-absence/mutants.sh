#!/bin/sh
set -eu

repository=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
inspector=$repository/scripts/m2-absence/inspect.sh

run_layers() {
  fixture_root=$(mktemp -d /tmp/go-kitty3-m2-mutants.XXXXXX)
  legacy_word='sql''ite'
  driver=modernc.org/$legacy_word
  cli=${legacy_word}3
  printf 'package mutant\nimport _ "%s"\n' "$driver" >"$fixture_root/source.go"
  printf 'example.test/mutant %s@v1.0.0\n' "$driver" >"$fixture_root/module.graph"
  printf '#!/bin/sh\n%s authority.db\n' "$cli" >"$fixture_root/command.sh"
  printf 'ELF\\000%s\\000' "$driver" >"$fixture_root/binary"
  mkdir "$fixture_root/install"
  printf '%s' "$driver" >"$fixture_root/install/provider.bin"
  printf '{"bomFormat":"CycloneDX","components":[{"name":"%s"}]}' "$driver" >"$fixture_root/sbom.json"
  printf 'Usage: tool --backend %s\n' "$legacy_word" >"$fixture_root/help.txt"
  printf '{"schema_version":1,"forbidden_loads":1,"forbidden_opens":0,"forbidden_subprocesses":0,"forbidden_imports":0,"current_mutations_on_refused_roots":0,"fallback_attempts":0}\n' >"$fixture_root/runtime.json"

  for layer in source graph-module shell binary install sbom help-config runtime; do
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
    "$inspector" --probe "$layer" "$target"
    status=$?
    set -e
    if [ "$status" -eq 0 ]; then echo "M2 detector mutant survived: $layer" >&2; exit 1; fi
    if [ "$status" -ne 42 ]; then echo "M2 detector failed instead of rejecting $layer: status=$status" >&2; exit 1; fi
  done

  printf 'package native\nconst Store = "gapdb"\n' >"$fixture_root/clean.go"
  printf '{"schema_version":1,"forbidden_loads":0,"forbidden_opens":0,"forbidden_subprocesses":0,"forbidden_imports":0,"current_mutations_on_refused_roots":0,"fallback_attempts":0}\n' >"$fixture_root/runtime-clean.json"
  "$inspector" --probe source "$fixture_root/clean.go"
  "$inspector" --probe runtime "$fixture_root/runtime-clean.json"
  printf 'M2 layer mutants passed: 8/8\n'
}

if [ "${1:-}" = --layers-only ]; then run_layers; exit 0; fi
cd -- "$repository"
run_layers
exec go test ./internal/architecture -count=1 \
  -run '^(TestM2AbsenceMutants|TestM2AbsenceSymlinkAndHistoricalDiscrimination|TestM2GateSourcesDoNotShelterExecutableBehavior|TestM2RetiredBlobCensusIsClosed|TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed)$'
