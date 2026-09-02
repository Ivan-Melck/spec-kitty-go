---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T12:53:46Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 Review Feedback — reviewer-renata

Verdict: **Reject / return to planned**. The baseline suite is green, but the
permanent guard remains fail-open under several semantic mutations required by
T003–T005. These are blocking because WP02, WP03, and WP12 depend on WP01 as
their architecture boundary.

## Issue 1 — incremental activation suppresses violations in later capability packages

`m2EvaluateIncrementalRepository` in
`internal/architecture/m2_dependency_guard_test.go:250` discards every
violation whose source is not in a six-prefix allowlist whenever the legacy
SQLite package exists. That condition remains true until WP16, so an already
present or newly implemented capability such as `internal/workgraph` can import
SQLite or a concrete provider and the repository guard reports no violation.
This contradicts the required first-file activation rule.

Reproduced with an isolated graph containing legacy
`internal/controlstore/sqlite` and an
`internal/workgraph -> modernc.org/sqlite` edge: the closed evaluator found the
edge, but the incremental evaluator returned zero violations.

Required correction: replace the broad source suppression with exact,
reviewable exemptions for pre-existing legacy edges (or equivalent provenance-
bound handling). Every new M2 package and every non-exempt edge in any existing
capability must activate its complete classification immediately. Add a mutant
for a later capability violation while SQLite still exists.

## Issue 2 — the eligibility-shard contract is self-referential and trivially evadable

The frozen values in `m2_registry_test.go:17-22` are also the source of the
test's expectations at lines 103-154 and 331-337. Temporarily changing shard
count `64` to `128` leaves all `TestM2Eligibility*` tests green, so the alleged
frozen tuple is not independently anchored.

Activation is also lexical and incomplete. `inspectM2EligibilityPolicy` scans
only non-test Go files under `internal/workgraph` and considers persistence
present only when one string literal contains `spk/v2/workgraphs/`
(`m2_registry_test.go:304`). A semantically identical key assembled from two
constants, placed in the GapDB adapter that actually owns key encoding, or
renamed behind a function causes `persistencePresent=false` and the test returns
success at lines 124-125 without checking any tuple field.

Required correction: establish an independent versioned contract source for
policy version, shard count, hash/escaping algorithm, per-shard budget, and
contention evidence. Detect the production seam by typed ownership/reachability
across workgraph and adapter surfaces, not a single literal/name. Fail closed
when a production eligibility seam exists but cannot be classified. Add value-
change, arbitrary rename, composed-key, wrapper, and adapter-owned mutants.

## Issue 3 — promised dependency boundaries are not encoded

The M2 policy inherits broad permissions and does not implement several explicit
T003 laws:

- Standard-library imports are ignored, so `internal/controljournal/v2 -> os`
  produces zero violations. The same hole permits clock, randomness, filesystem,
  network, and other ambient I/O in journal/playback code.
- The inherited quality tier permits GapDB for all quality packages. An
  `internal/scenario -> github.com/spec-kitty/gapdb/gapdb` edge is accepted even
  though provider access is restricted to the concrete adapter, sidecar, and
  qualification-only external runner.
- Tracker adapters are denied only artifact/mission authority by the inherited
  policy. A fixture with `internal/tracker/beads -> internal/execution` and
  `internal/tracker/beads -> internal/gitrepo` is accepted, allowing the tracker
  to become execution or Git authority.

Required correction: encode pure journal/playback ambient-capability denials,
narrow GapDB permission to the three named packages rather than their whole
tier, and explicitly deny tracker adapters from execution, workspace, handoff,
review, acceptance, integration, and Git authority as governed. Add positive
fixtures for provider-neutral standard-library use and the legal three provider
callers, plus one mutant per forbidden edge.

## Issue 4 — the event-family validator is not a closed owner/family validator

`validateM2EventRegistry` at `m2_contracts_test.go:366` checks only family
non-emptiness, name uniqueness, positive versions, owner path syntax, and count.
Changing a registered family's owner to another normalized `internal/...` path
is accepted by the same validator used by the mutant loop. Renaming a family
while preserving count/uniqueness has the same weakness. The exact expected
census is checked separately only for the unmodified repository payload, so the
mutants do not exercise a production-equivalent closed model.

Required correction: make the shared validator compare the exact closed tuples
`(family, owner, event_schema, reducer)` and use it for both repository input and
all in-memory mutants. Add wrong-owner, renamed-family, missing-family,
unexpected-family, and version-change cases.

## Issue 5 — mutant census is disconnected from executed mutants

`m2MutantRegistry` is a hard-coded list independent of the actual test tables.
Temporarily deleting the executed `M-REACH-GENERIC` case while leaving its name
in the registry still makes both `TestM2SemanticReachabilityMutants` and
`TestM2MutantCensusIsExact` pass. Therefore the census cannot detect silent
removal of a mutant, contrary to T004/T005.

Required correction: drive execution and census from one closed table of mutant
cases/results, require every declared case to reach its intended semantic rule,
and derive terminal evidence only from those recorded executions. Add a census
deletion control that removes an executed case rather than editing one of two
duplicate literal lists.

## Validation evidence

Baseline commands passed:

- `go test ./internal/architecture -run 'TestM2' -count=3`
- `go test -race ./internal/architecture -run 'TestM2' -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/architecture`
- `staticcheck ./internal/architecture`
- `git diff --check`

Adversarial probes failed as described for incremental activation, composed
eligibility keys, ambient `os`, broad GapDB quality access, tracker execution/Git
access, and event-owner mutation. The frozen-shard and mutant-deletion probes
unexpectedly passed, demonstrating their tests are insensitive. All temporary
probe files and source mutations were fully restored; the WP implementation
diff again contains only its four declared files.

## Anti-pattern checklist

1. Dead code: **N/A** — WP01 intentionally creates architecture test modules,
   not production APIs.
2. Synthetic-fixture test: **FAIL** — several fixtures validate a permissive or
   self-referential helper rather than the complete promised repository rule.
3. Silent empty return: **PASS** — no unexplained Go equivalent found in the WP
   diff; the explicit early return for absent eligibility is instead covered by
   Issue 2 because its activation predicate is unsound.
4. FR coverage: **FAIL** — FR-031/FR-033 and NFR-014/NFR-015 are not proven under
   the reproduced reachability, ownership, activation, and census bypasses.
5. Frozen surface: **PASS** — only the four declared WP01 files changed; frozen
   contracts and M1 evidence were not modified.
6. Locked decision: **FAIL** — pure playback/provider/tracker constraints and
   fail-closed package activation can be violated while the gate remains green.
7. Shared-file ownership: **PASS** — the implementation diff is confined to the
   dedicated WP01 ownership surface.
8. Production fragility: **N/A** — WP01 adds no production request/worker/CLI
   raises or panics.

The M1 adoption tuple itself is correctly pinned and the default tests perform
no network access; this does not offset the permanent-boundary failures above.
