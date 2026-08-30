---
affected_files: []
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T07:33:00Z'
reviewer_agent: reviewer-renata
wp_id: WP01
---

# WP01 review feedback — reviewer-renata

Verdict: changes requested.

## Blocking issue 1 — reviewer independence can be bypassed by falsifying the contributor census

`validateOrderedTrial` verifies reviewer IDs only against the self-reported
`reviews[*].contributor_actor_ids` union. It separately proves the ordered trial
participants are Actor A / Process A / W1, Actor B / Process B / W2, and Actor A /
Process C / W3, but never binds either candidate's contributor set to those
participants. A schema-valid fixture can therefore replace candidate A's contributor
set with `not-actor-a`, set `reviewer_actor_id` to `A`, retain
`actor_independent: true`, and pass both JSON Schema and `validateEvidencePass`.

Independent reproduction:

- clone `passingEvidenceFixture()`;
- set the first review's `contributor_actor_ids` to `["not-actor-a"]`;
- set its `reviewer_actor_id` to `"A"`;
- schema validation succeeds and `validateEvidencePass` returns nil.

This violates T002 requirement 5, FR-036/SC-007 semantics consumed by the WP's
complete evidence predicate, and the mandatory incomplete-signature anti-vacuity
boundary. Add a deterministic candidate-to-contributor binding and a mutant proving
that falsified/omitted candidate contributors cannot turn self-review into a pass.
Apply the same binding to the independent campaign reviewer check.

Affected code: `internal/architecture/m1_evidence_semantics_test.go`, especially
the independent-review check around lines 387-391 and ordered-trial review check
around lines 411-438.

## Blocking issue 2 — FR-017's pure-reducer boundary is not executable

WP01 lists FR-017 and promises an executable architecture boundary, but
`inspectM1Sources` restricts only GapDB, SQLite, `internal/server`, and a small
identifier blacklist. It accepts this source without a violation:

```go
package reducer
import _ "os"
```

at `internal/controljournal/reducer/reduce.go`. The same gap permits `time`, Git,
filesystem, tracker, agent, artifact, and other adapter dependencies expressly
forbidden by FR-017 and `control-journal-v1.md`. Add a path-specific import policy
for `internal/controljournal/reducer` with clean and impure mutants. Also add the
missing FR-016/NFR-013 negative contract controls for unknown event/reducer
versions, malformed groups, ordering, and digest refusal, or narrow the WP's
declared requirement coverage through the mission planning authority.

Affected code: `internal/architecture/m1_dependency_guard_test.go`, especially
`m1SourceRoots` and import classification around lines 24-31 and 181-208.

## Blocking issue 3 — the event-order test is synthetic and cannot guard downstream production code

`TestM1SchemasEventOrderingContract` sorts a test-only `m1EventPosition` using a
test-only `compareM1EventPosition`. No production caller or contract parser uses
that comparator. A later runtime can implement EventID-only or otherwise incorrect
ordering and this test remains green, so the test fails the deletion/synthetic-
fixture gate and does not provide WP02 a red/green boundary. Replace the self-test
with an executable guard tied to the production seam when that seam exists, or a
source/contract rule that necessarily fails for a forbidden ordering construction.

Affected code: `internal/architecture/m1_contracts_test.go` lines 116-154.

## Verification evidence

- `go test ./internal/architecture -count=1 -run 'M1Budget|M1Schemas|M1Evidence|M1Dependency'` — PASS (0.077s).
- `go test ./internal/architecture -count=1` — PASS (14.108s).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `git diff --check kitty/mission-gapdb-control-journal-proving-ground-01M18EVR..HEAD` — PASS.
- Focused forbidden-import and evidence mutants — PASS.
- Deletion test: removing the `internal/server` classifier makes its mutant test fail — reachable.
- Deletion test: removing reviewer-independence logic makes the existing self-review mutant test fail — reachable.
- Temporary falsified-contributor probe — FAIL as expected, demonstrating issue 1.
- Temporary impure-reducer probe — FAIL as expected, demonstrating issue 2.
- The documented repository-wide `go test ./... -short` 10-minute `internal/scenario` timeout was not treated as a WP regression; the implementation handoff records the isolated legacy matrix passing in 362.720s.

## WP anti-pattern checklist

1. Dead code: N/A — no production APIs/modules added.
2. Synthetic-fixture test: FAIL — test-local event comparator is not linked to a production or contract-enforcement path.
3. Silent empty return: N/A — no production code added.
4. FR coverage: FAIL — FR-017 is not enforced; FR-016/NFR-013 negative controls are absent.
5. Frozen surface: PASS — no contract, `go.mod`, or `go.sum` changes.
6. Locked decision: PASS for the implemented GapDB allowlist, global `internal/server`, and M1 SQLite boundaries.
7. Shared-file ownership: PASS — only the four WP01-owned files changed; lane-a is exclusive to WP01.
8. Production fragility: N/A — no production `raise`/panic path added.

The lane was clean after all temporary probes and deletion-test edits were restored.
