---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 7
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T09:35:51Z'
reviewer_agent: reviewer-renata-capability-3
verdict: rejected
wp_id: WP01
---

# WP01 capability-boundary review — cycle 7

Verdict: changes requested.

The cycle-6 counterexamples are closed. Independent out-of-tree probes confirmed
that function-literal inputs and outputs, interface methods, generic constraints
and unions, named and nested raw-event wrappers, validator rebinding, recursive
child-package impurity, and SQLCipher are rejected. The complete cycle-5 set
also remains closed: aliases, variadics, value and pointer receivers, qualified
child-package aliases, anonymous `RecordRevision` owners, and the mattn SQLite
driver all fail for the intended reason.

Four remaining blockers make the guard either fail open or reject architecture
that the ratified plan requires.

## Blocking issue 1 — root and child type-check errors fail open

Both `types.Config.Check` calls discard their errors. The root check proceeds
whenever it returns a partial non-nil package, while every child group ignores
the result unconditionally. These independently reproduced fixtures therefore
return an empty violation set:

```go
// internal/controljournal/broken.go
package controljournal
var _ MissingType
```

```go
// internal/controljournal/playback/broken.go
package playback
func Broken(MissingType) {}
```

That silently drops parts of the resolved authority census and contradicts the
fail-closed contract. Record a stable `journal-type-check` violation for every
root or child package error even when Go returns a partial package. Add both
fixtures as permanent mutants. Do not suppress the type-check callback or
infer success from `pkg != nil`.

## Blocking issue 2 — capability-reference and result traversal are overbroad

The syntax-level recreation test classifies every type declaration that merely
mentions `ValidatedJournalV1` as an alias/embedding recreation. Separately,
`m1TypeContainsValidatedJournal` walks both parameters and results of a nested
signature while it is deciding whether an outer result produces the
capability. This valid consumer surface is rejected twice:

```go
type Sink func(ValidatedJournalV1)

func NewSink() Sink { return nil }
```

It neither aliases nor embeds the capability, and neither callable returns a
`ValidatedJournalV1`. A reducer/playback consumer is precisely what the frozen
boundary is intended to permit. Narrow recreation to resolved exact aliases
and embedded fields. Make callable traversal direction-aware: consumer
parameters are not producer results. Add the example above, a consumer
interface, and a non-embedded holder as clean controls while retaining the
existing producer, alias, embedding, and wrapped-result mutants.

## Blocking issue 3 — the declared sole constructor is not enforced

The census examines callable signatures and validator rebinding, but never
construction expressions. This non-authoritative construction produces no
violation:

```go
func SideEffectConstruction() {
    _ = ValidatedJournalV1{}
}
```

The frozen contract says only `ValidateJournalV1` may construct the capability;
return-signature policing alone cannot enforce that. Resolve exact
`ValidatedJournalV1` composite/allocation construction and reject it outside
the authoritative validator body. Add a side-effect construction mutant and
an exact validator-body clean control.

## Blocking issue 4 — recursive purity forbids the required pure package graph

The new recursive purity check applies one flat import allowlist to every
`internal/controljournal` child. It consequently rejects this pure in-tree
edge:

```go
// internal/controljournal/playback/playback.go
package playback
import _ "github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
```

The ratified plan defines `reducer`, `checkpoint`, `playback`, and
`application` as cooperating pure child packages. Since `reducer` imports the
root journal types, the root cannot import it back; playback/application must
be able to depend on the pure child packages without a cycle. Permit imports
inside the recursively guarded `internal/controljournal` tree (or freeze the
exact planned internal DAG) while continuing to apply the same deny-by-default
external import policy to every child. The in-memory type resolver must also
resolve those sibling source packages and report their errors rather than
falling back to an installed archive.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1.*(Mutant|Capability|Dependency|Budget|Schemas|Evidence)'` — PASS.
- `go test ./internal/architecture -count=1` — PASS (`12.701s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git diff --check` over the WP code diff — PASS.
- Out-of-tree cycle-5 and cycle-6 replay — PASS; every former counterexample
  is rejected and direct root/qualified validator calls remain admitted.
- Out-of-tree adjacent campaign — FAIL as expected: both type-check errors and
  direct side-effect construction survive; the valid consumer callable and
  pure sibling import are false-red.
- The known repository-wide ten-minute `internal/scenario` timeout was not run,
  as directed, and is not treated as a WP01 regression.

No reviewer probe or implementation file remains in the lane worktree.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — committed mutants do not exercise
   type-check failure or construction outside the authoritative validator.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — FR-015/FR-016/NFR-013 sole-constructor and
   fail-closed authority exclusivity remain bypassable.
5. Frozen surface: **PASS** — implementation commits modify only the four
   WP01-owned architecture tests; no frozen contract or module file changed.
6. Locked decision: **FAIL** — the pure child-package graph ratified by the plan
   is rejected, while malformed packages and non-authoritative construction
   cross frozen `MUST`/`MUST NOT` boundaries.
7. Shared-file ownership: **PASS** — the implementation diff remains inside
   WP01's exclusive four-file surface.
8. Production fragility: **N/A** — no production panic/fail-loud path was added.
