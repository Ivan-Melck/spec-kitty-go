---
affected_files: []
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T07:53:50Z'
reviewer_agent: reviewer-renata
wp_id: WP01
---

# WP01 review feedback — cycle 2 — reviewer-renata

Verdict: changes requested.

## Accepted cycle-1 remediations

- Contributor-list falsification/self-review is now rejected by an explicit
  candidate-to-contributor binding. The exact prior probe is schema-valid but
  `validateEvidencePass` rejects it, and deletion of the binding makes the
  swapped-contributor mutant fail.
- Reducer purity is now deny-by-default for production reducer imports. The
  exact `os` probe and additional `time`, `math/rand`, `net/http`, Git, tracker,
  artifact, and GapDB imports are rejected. Deleting the purity classifier makes
  the `os` mutant fail.

## Blocking issue — the production journal seam guards pass behaviorally wrong implementations

The new FR-015/FR-016/NFR-013 guard checks only that selected field names occur
in comparisons and that suitably named verifier functions are called. It does
not prove that the comparisons implement the required order, that verifier
results control refusal, that digests are recomputed/compared, or that a group
validator observes a group.

An independent temporary probe supplied production-shaped `model.go` and
`validation.go` fixtures with all expected names but deliberately wrong behavior:

1. The comparator checks only `<` for `RecordRevision`, then continues to
   `EventIndex` even when the left revision is greater. For left revision 8/right
   revision 7 and left index 0/right index 1 it incorrectly returns `-1`.
2. `ValidateCommittedEventGroup` accepts one `CommittedEvent`, so it cannot detect
   a missing index, duplicate tuple, noncontiguous group, mixed operation, or
   nonmonotonic record sequence. Merely checking `EventIndex < EventCount` is not
   group validation.
3. `CanonicalEventDigestMatches` returns `digest != ""`, admitting every nonempty
   mismatched digest.
4. An always-true `SupportedEventVersion` call is assigned to `_`; tautological
   comparisons mention all required version fields while unknown versions remain
   admissible.

`inspectM1Sources` returned zero violations for all four defects. Consequently the
new clean fixture in `TestM1DependencyJournalContractMutants` is itself not a
correct behavioral control, and the named mutants only prove the AST recognizer
can spot missing syntax. This leaves the cycle-1 production-ordering and
FR-016/NFR-013 blockers open and violates the anti-vacuity/deletion standard.

Affected code:

- `internal/architecture/m1_dependency_guard_test.go`
  - `TestM1DependencyJournalContractMutants`
  - `inspectM1JournalContractSeams`
  - `usesM1OrderCoordinates`
  - `comparedSelectorNames`
  - `callsM1VersionVerifier` / `callsAnyName`

Required outcome: use a behaviorally executable production contract seam or a
classifier that proves control/data flow, with clean controls that actually
implement three-way `(RecordRevision, EventIndex, EventID)` ordering, whole-group
contiguity/duplicate/nonmonotonic refusal, registry result enforcement, and
canonical digest mismatch refusal. Add decorated/ignored-call mutants matching the
four reproductions above.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1Budget|M1Schemas|M1Evidence|M1Dependency'` — PASS (0.073s).
- `go test ./internal/architecture -count=1` — PASS (12.604s).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- Named contributor, reducer-purity, and journal-contract mutant suites — PASS.
- Contributor-binding deletion test — expected FAIL, proving reachability.
- Reducer-purity deletion test — expected FAIL, proving reachability.
- Journal-seam dispatch deletion test — expected FAIL, proving reachability.
- Decorated wrong-journal-seam probe — expected FAIL with zero production guard
  violations, reproducing the remaining blocker.
- `git diff --check` — PASS; only the four WP01-owned files differ from the mission
  branch; frozen contracts and `go.mod`/`go.sum` are unchanged.

The documented repository-wide `go test ./... -short` 10-minute
`internal/scenario` timeout remains existing package-wide behavior; the prior
handoff records the isolated legacy matrix passing in 362.720s. It is not treated
as a WP01 regression.

## WP anti-pattern checklist

1. Dead code: N/A — no production API or module was added.
2. Synthetic-fixture test: FAIL — journal source fixtures pass on decorative
   syntax while violating the asserted behavior.
3. Silent empty return: N/A — no production code was added.
4. FR coverage: FAIL — FR-015 ordering and FR-016/NFR-013 refusal remain
   structurally mentioned but behaviorally unenforced.
5. Frozen surface: PASS — no contract, `go.mod`, or `go.sum` change.
6. Locked decision: PASS for GapDB allowlisting, global `internal/server`, SQLite,
   current-batch revision, global CAS, and reducer-purity boundaries.
7. Shared-file ownership: PASS — only WP01-owned files changed; lane-a is exclusive.
8. Production fragility: N/A — no production raise/panic path was added.

All temporary probes and deletion edits were restored; lane-a was clean before the
verdict.
