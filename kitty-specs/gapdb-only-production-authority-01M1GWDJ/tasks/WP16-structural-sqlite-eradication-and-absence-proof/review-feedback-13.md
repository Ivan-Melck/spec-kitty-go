# WP16 review feedback — cycle 13

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

### Simple-command assignment expansion uses the wrong value

The shell proof mutates the active assignment map before it resolves the
command word in the same simple command. Shell expansion can therefore execute
the value that existed before the command-prefix assignment:

```sh
review_retired_cli=sql\ite3
review_retired_cli=native "$review_retired_cli" /tmp/x
```

The current proof reports no finding. The same escape exists beneath a
recognized wrapper:

```sh
review_retired_cli=sql\ite3
env review_retired_cli=native "$review_retired_cli" /tmp/x
```

Snapshot the pre-command assignment environment for expansion and delay the
effects of simple-command assignment prefixes until the command boundary has
been analyzed. Add both exact mutants plus clean controls that prove the model
does not confuse persistent assignments with command-scoped prefixes.

### Mutant evidence is asserted instead of observed

`scripts/m2-absence/inspect.sh` discards the selected `go test` event stream
and writes fixed `mutants: 47` and `layers: 8` payloads. A missing, renamed, or
skipped named test can still leave `go test -run` green, so the manifest does
not prove its claimed closed census.

Capture `go test -json` receipts for a closed expected absence/mutant census.
Require exact RUN and PASS membership and zero skipped, failed, unmatched,
missing, or duplicate items. Derive every reported count and digest from those
observations. Apply the same observed-evidence rule to layer mutants; fixed
success payloads are not admissible evidence.

## Preserve

- The cycle-13 generalized dispatch rule across interpreter, utility, and
  indirect wrappers, including known and unknown option layouts.
- All word/comment/quote/budget, basename, nesting, case, and ambiguity
  invariants and ordinary non-dispatch data controls.
- Exact sidecar admission, source/tree binding, loaded module graph, binaries,
  install image, SBOM, help/configuration, and 23 observed runtime receipts.
- All prior focused/race/architecture/parity/build/tidy/vet/static evidence.

## Required re-review evidence

- Kill both exact assignment-expansion attacks under direct and wrapped
  execution.
- Demonstrate pre-command versus post-command assignment semantics with clean
  controls.
- Produce closed, machine-observed absence and layer-mutant receipt sets with
  zero skips, failures, omissions, extras, or duplicates.
- Qualify the exact final integrated tree and recompute the complete manifest
  and inventory tuple from that run.
