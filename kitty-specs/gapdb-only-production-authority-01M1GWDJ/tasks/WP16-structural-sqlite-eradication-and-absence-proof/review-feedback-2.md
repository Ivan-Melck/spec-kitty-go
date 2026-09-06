# WP16 review feedback — cycle 2

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

1. The final lane tree fails `TestM2SQLiteAbsence` because the required
   `scope-extension-1.md` contains the truthful historical token
   `modernc.org/sqlite` but the closed path/token map does not account for it.
   The final qualification must bind and pass the integrated tree, not an
   earlier candidate that predates required evidence.
2. `m2GateSource` returns before subprocess, dynamic-load, fallback, schema,
   and related checks for both architecture gate files and both shell
   qualification scripts. Independent mutants placed an executable `sqlite3`
   command in `scripts/m2-absence/inspect.sh` and compiled fallback behavior in
   an exempt architecture test; both falsely passed the absence gate. Replace
   blanket file exemptions with a representation that can exempt detector data
   without sheltering executable behavior, and add killing controls for it.
3. The advertised 30 mutants all call the lexical inspector on small fixture
   files with graph resolution disabled. The binary, SBOM, installation, help,
   runtime, module-graph, and shell-script mechanisms are never mutated or
   exercised. Add layer-specific positive controls such that deleting or
   weakening each named detector makes a mutant survive and the suite fail.
4. The clean manifest does not bind the complete evidence tuple: it lacks the
   command inventory, binary/install/runtime/mutant-result digests, and an
   aggregate evidence digest. `evidence-sha256.txt` also includes itself while
   redirection is still writing it, so its recorded self-entry differs from the
   completed file. Exclude checksum/manifest outputs from their own input set,
   bind all claimed artifacts transitively, and independently verify the
   completed set.

## Preserve

- All 33 retired SQLite files remain deleted.
- SQLite, `database/sql`, and the exclusive modernc closure remain absent from
  package, test, module, binary, install, help, runtime, and SBOM surfaces.
- The second `go mod tidy` stays clean; compilation, CGO-free builds, vet,
  staticcheck, representative GapDB parity, and race checks remain green.
- The architecture policy continues to remove SQLite classification and
  external allowances without weakening unrelated directions.
- The frozen 185-mutant census and the resurrected-adapter positive control
  remain meaningful.

## Required re-review evidence

- Qualify the exact final integrated source/tree containing all required WP16
  governance artifacts.
- Reproduce the two independent executable-shelter mutants and prove both are
  killed.
- Demonstrate detector-specific mutants for source, graph, shell, binary,
  install, SBOM, help/configuration, and runtime layers.
- Recompute and verify a self-consistent manifest that transitively binds every
  claimed artifact and never hashes an output while that output is being
  written.
