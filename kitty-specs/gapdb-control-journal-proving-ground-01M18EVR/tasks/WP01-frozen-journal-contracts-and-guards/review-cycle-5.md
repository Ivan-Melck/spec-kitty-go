---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 5
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T09:00:43Z'
reviewer_agent: reviewer-renata-capability
verdict: rejected
wp_id: WP01
---

# WP01 capability-boundary review — cycle 5

Verdict: changes requested.

The exact `ValidateJournalV1 -> ValidatedJournalV1` boundary is the correct
architectural correction. Recursive activation, exact root symbols, direct
field/type checks, opaque capability state, decorative-decoy resistance, and
deny-by-default root/reducer imports all pass their committed controls. This
review does not request a return to heuristic arbitrary-name semantic discovery;
WP02 still owns ordering, grouping, registry, digest, and reducer behavior.

## Blocking issue 1 — raw-event authority and revision ownership have type-shape escapes

`inspectM1JournalCompetitors` recognizes only a literal `[]StoredEventV1` in a
function's parameter list. It does not follow aliases or defined slice types,
does not treat a variadic parameter as the equivalent slice, and does not inspect
method receivers. Each of these production declarations passes with no
`journal-raw-slice-authority` violation:

```go
type RawEvents = []StoredEventV1
func SortUnvalidated(events RawEvents) { _ = events }

func ValidateUnvalidated(events ...StoredEventV1) { _ = events }

type UnvalidatedEvents []StoredEventV1
func (events UnvalidatedEvents) Sort() { _ = events }
```

They preserve an ordering/validation path that bypasses the frozen validated
capability. The direct `RecordRevision` census has a related syntax-only hole:

```go
var revisionDecoy struct { RecordRevision uint64 }
```

This also passes without `journal-record-revision-owner`, despite directly
owning the field outside `StoredEventV1`.

This violates the frozen contract's rules that reducers/playback receive
`ValidatedJournalV1`, no competing function or method consumes raw stored-event
slices for authority, and only `StoredEventV1` directly owns `RecordRevision`.
Close the census over resolved Go types, including aliases, defined slice
types, variadics, method receivers, and anonymous struct declarations. Add all
four reproductions as permanent mutants with an exact valid control. The guard
may remain stricter than semantic name recognition; it must not reintroduce
heuristic function scoring.

## Blocking issue 2 — the no-SQLite boundary admits alternate SQLite drivers

`isSQLiteImport` rejects only `modernc.org/sqlite` and imports whose path ends
in `/controlstore/sqlite`. This M1 production source passes with zero
violations:

```go
package provingground
import _ "github.com/mattn/go-sqlite3"
```

T003 requires new M1 SQLite package imports to fail, and C-006 forbids a hidden
SQLite authority path. Enforce the backend prohibition independently of the
currently historical driver/package name. Add this alternate-driver fixture as
a killed mutant while retaining the clean control proving the existing
historical SQLite package outside M1 roots remains comparison-only.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1.*(Mutant|Capability|Dependency|Budget|Schemas|Evidence)'` — PASS.
- `go test ./internal/architecture -count=1` — PASS (`13.977s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git diff --check kitty/mission-gapdb-control-journal-proving-ground-01M18EVR..HEAD` — PASS.
- Out-of-tree reviewer mutant suite — FAIL as expected: all four capability/
  ownership bypasses and the alternate SQLite driver survived with an empty
  violation list.
- The known repository-wide ten-minute `internal/scenario` timeout was not
  rerun and is not treated as a WP01 regression.

All reviewer probes were created in an out-of-tree archive. No implementation
or mission-contract file was changed in the lane.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — the committed authority mutants cover
   literal syntax but miss type-equivalent raw-event consumers.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — FR-015/FR-016/NFR-013 capability exclusivity and
   C-009/FR-039 no-SQLite enforcement remain bypassable.
5. Frozen surface: **PASS** — the implementation diff changes only WP01-owned
   tests; contract and module files are unchanged by the WP.
6. Locked decision: **FAIL** — an alternate SQLite authority import can enter
   an M1 production root, contrary to C-006 and the plan's MUST NOT boundary.
7. Shared-file ownership: **PASS** — only the four WP01-owned files differ; the
   lane is exclusive to WP01.
8. Production fragility: **N/A** — no production panic/fail-loud path was added.

