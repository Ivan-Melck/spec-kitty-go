---
affected_files: []
cycle_number: 3
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T08:13:09Z'
reviewer_agent: reviewer-renata
wp_id: WP01
---

---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 3
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewer_agent: reviewer-renata
wp_id: WP01
---

# WP01 review feedback — cycle 3 — reviewer-renata

Verdict: changes requested.

## Blocking issue — a valid decoy type masks a broken production journal seam

The cycle-3 executable probe kills the four cycle-2 decorated semantic bypasses
when the broken functions are the only complete signature set, but it does not
bind the selected functions to the production committed-event type. In
`discoverM1ExecutableJournalSeam`, every event-shaped function signature is
grouped by parameter type; the highest score wins, and an equal score is settled
by lexicographic type name. `executeM1JournalSemanticProbe` then executes only
that single selected type.

An independent adversarial fixture retained a deliberately broken
`CommittedEvent` comparator, whole-group validator, version enforcement, and
digest validation, then added an otherwise unused, correctly behaved
`AProbeEvent` seam with arbitrary function names and the same aggregate score.
Because `AProbeEvent` sorts before `CommittedEvent`, the temporary program tested
the decoy only and `inspectM1Sources` returned no violations. Renaming the decoy
after `CommittedEvent` caused the broken production seam to be exercised and the
expected ordering/group/version/digest violations to appear. Thus guard success
depends on an unrelated type name and does not prove the production seam that
later WPs consume.

This is a non-vacuity and production-binding failure for FR-015, FR-016, and
NFR-013. The new temporary compilation/execution path is bounded and cleans its
temporary directory, but executing one heuristic winner is insufficient when
another complete candidate exists.

Affected code:

- `internal/architecture/m1_dependency_guard_test.go:635` —
  `discoverM1ExecutableJournalSeam`
- `internal/architecture/m1_dependency_guard_test.go:669` — lexicographic
  equal-score selection
- `internal/architecture/m1_dependency_guard_test.go:731` — single-seam
  execution

Required outcome: bind the semantic probe to the actual production
committed-event seam, or execute and require compliance from every complete
candidate that can satisfy the production contract. Add the valid-decoy/broken-
production fixture as a permanent mutant, including arbitrary type and function
renaming controls, and prove the new assertion is deletion-reachable.

## Cycle-3 disposition

- Contributor binding remains fixed: falsified contributor lists and reviewer
  self-review are rejected.
- Reducer purity remains fixed: `os` and other non-pure imports are rejected.
- The four cycle-2 semantic bypasses are killed when applied to the selected
  seam.
- Arbitrary function renaming is supported for a single seam, but arbitrary type
  naming exposes the decoy-selection bypass above.
- Frozen contracts, module files, and locked no-SQLite/internal-server/GapDB
  boundaries were not changed by fix commit
  `5467edbadfbf15d1564fa7f5e850b2f5a302b118`.

## Verification

- Independent decoy-seam adversarial probe — FAIL as expected for the review:
  the guard reported zero violations for broken `CommittedEvent` behavior while
  executing the valid lexicographically earlier decoy.
- Control rename of the decoy after `CommittedEvent` — ordering, group, version,
  and digest violations became observable, confirming the winner-selection
  cause.
- `git diff --check` — PASS.
- Exact fix commit scope — one WP01-owned test file; frozen contracts and
  `go.mod`/`go.sum` unchanged.

All temporary adversarial fixtures were removed before this verdict.

## WP anti-pattern checklist

1. Dead/synthetic test seam: FAIL — an unused valid decoy can satisfy the guard
   while the production seam is wrong.
2. Mutant anti-vacuity: FAIL — equal-score candidate selection permits a
   deterministic semantic bypass.
3. Error-path/deletion reachability: FAIL for production binding — deleting or
   breaking the non-selected production seam does not fail the guard.
4. FR coverage: FAIL — FR-015 and FR-016/NFR-013 remain unenforced for the actual
   production seam in multi-candidate source.
5. Frozen surface: PASS.
6. Locked boundaries: PASS.
7. Shared-file ownership: PASS.
8. Production fragility: N/A — WP01 changes tests only.
