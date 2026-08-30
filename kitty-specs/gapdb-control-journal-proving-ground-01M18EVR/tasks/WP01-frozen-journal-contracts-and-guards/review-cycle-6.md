---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 6
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T09:17:26Z'
reviewer_agent: reviewer-renata-capability-2
verdict: rejected
wp_id: WP01
---

# WP01 capability-boundary review — cycle 6

Verdict: changes requested.

The cycle-5 repair is effective. Out-of-tree probes confirmed that raw slice
aliases, a qualified alias from a child package, variadics, defined-slice value
and pointer receivers, anonymous `RecordRevision` owners, and the mattn SQLite
driver are now rejected. Honest event-version slices and the historical
comparison-only SQLite package remain allowed. Exact root symbols, direct
fields, qualified returns and parameters, alias chains, nested anonymous
`RecordRevision` fields, and recursive activation also behave correctly.

The remaining blockers are structural holes in the declared capability and
purity census. They do not require heuristic discovery of semantic function
names.

## Blocking issue 1 — callable census excludes function literals and interface methods

`inspectM1JournalCompetitors` visits only top-level `*ast.FuncDecl` values and
type declarations. These direct competitors therefore pass with no violation:

```go
var SortRaw = func(events []StoredEventV1) {}

type RawSorter interface {
    Sort([]StoredEventV1)
}

var Manufacture = func() ValidatedJournalV1 {
    return ValidatedJournalV1{}
}
```

The first two expose unvalidated event authority without
`ValidatedJournalV1`; the third is a second capability producer. They violate
the frozen rule that only `ValidateJournalV1` constructs the capability and
that no other function or method consumes a raw stored-event slice.

Close the guard over resolved function-typed declarations, function literals,
and interface methods as well as `FuncDecl`. Compare resolved parameter and
result types with the exact frozen types. Add the three examples as permanent
mutants and retain clean function-value/interface controls whose signatures do
not contain either authority type.

## Blocking issue 2 — raw-event containment and generic constraints evade the boundary

The resolved check unwraps one pointer and asks whether the immediate
underlying type is a slice. It does not inspect type-parameter constraints or
containers. Each declaration below passes with no
`journal-raw-slice-authority` violation:

```go
func SortRaw[S ~[]StoredEventV1](events S) {}

type RawInput struct {
    Events []StoredEventV1
}

func SortWrapped(input RawInput) {}

func SortNested(input struct {
    Batch struct {
        Events []StoredEventV1
    }
}) {}
```

These remain unvalidated inputs to an alternate ordering/reduction path. Walk
resolved input types recursively through aliases, named underlyings, pointers,
struct fields, anonymous structs, and type-parameter terms. Stop at the exact
`ValidatedJournalV1` named capability so its deliberately private storage does
not become a false positive. Add generic, named-wrapper, and nested-anonymous
mutants plus an honest wrapper over `[]EventVersionV1`.

## Blocking issue 3 — recursive activation is broader than recursive purity

Any production file recursively below `internal/controljournal` activates the
complete contract, but deny-by-default imports cover only the root directory
and the `reducer/` subtree. This production file activates the journal contract
and still passes with no purity violation:

```go
// internal/controljournal/playback/io.go
package playback

import _ "os"
```

That contradicts the control-journal contract's pure-kernel scope and permits
playback or another future child package to regain filesystem, clock, network,
Git, tracker, or storage access. Apply the closed import surface to every
non-test Go file recursively below `internal/controljournal`, preserving the
root-package import already allowed for child packages. Add a non-`reducer`
child-package mutant and clean recursive control.

## Blocking issue 4 — the SQLite provider boundary still has an admitted common binding

The provider list plus `sqlite` path-component heuristic does not classify the
SQLite-compatible SQLCipher binding:

```go
package provingground

import _ "github.com/mutecomm/go-sqlcipher/v4"
```

It passes with no `sqlite-import` violation inside an M1 production root. Add
SQLCipher providers to the explicit prohibited storage-provider inventory and
add this fixture as a killed mutant. Prefer an explicit admitted external
storage dependency policy for M1 roots over an ever-growing semantic substring
guess; historical comparison packages must remain outside that policy.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1.*(Mutant|Capability|Dependency|Budget|Schemas|Evidence)'` — PASS.
- `go test ./internal/architecture -count=1` — PASS (`13.220s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git diff --check kitty/mission-gapdb-control-journal-proving-ground-01M18EVR..HEAD` — PASS.
- Out-of-tree replay of all cycle-5 counterexamples — PASS; every former gap
  is now rejected and both clean-control classes remain accepted.
- Out-of-tree adjacent mutant campaign — FAIL as expected; all counterexamples
  above survived with an empty relevant violation list.
- The known repository-wide ten-minute `internal/scenario` timeout was not run,
  as directed, and is not treated as a WP01 regression.

No reviewer probe or implementation file was left in the lane worktree.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — the architecture fixture matrix omits
   valid Go callable and containment forms that directly bypass the guard.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — FR-015/FR-016/NFR-013 exclusivity and C-009/FR-039
   no-SQLite/pure-playback enforcement remain bypassable.
5. Frozen surface: **PASS** — implementation commits change only the four
   WP01-owned architecture tests; no frozen contract or module file changed.
6. Locked decision: **FAIL** — a second capability producer, raw-event
   consumers, an impure journal child package, and a SQLite-compatible provider
   can all cross frozen `MUST NOT` boundaries.
7. Shared-file ownership: **PASS** — the lane is exclusive to WP01 and the code
   diff stays within its four owned files.
8. Production fragility: **N/A** — no production panic/fail-loud path was added.
