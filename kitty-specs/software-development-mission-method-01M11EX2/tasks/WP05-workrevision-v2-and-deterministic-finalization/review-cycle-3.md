---
affected_files:
- internal/mission/finalization.go
- internal/mission/finalization_test.go
cycle_number: 3
mission_slug: software-development-mission-method-01M11EX2
reproduction_command: go test ./internal/mission -run TestReviewerProbeOperationDigestBindsCompleteCommand -count=1 -v
reviewed_at: '2026-08-28T00:30:09Z'
reviewer_agent: codex
wp_id: WP05
---

# WP05 Review Feedback — Round 3

Verdict: changes requested.

## Issue 1 — different authority-bearing requests alias to exact replay (blocker)

The cycle-3 receipt closes the cycle-2 corrupt-replay hole, but the request
commitment still does not cover the complete authority-bearing finalization
command. `finalizationCommandDigest` includes selected identity, definition,
step, generation, actor, governance, and time fields, but omits valid fields of
`ExpectedMission`, including lifecycle, current revision, created/updated time,
terminal outcome, active blockers, and unresolved decisions.

An independent temporary production-path probe demonstrated the resulting
false replay:

1. Finalize a valid command under one `OperationID`.
2. Reissue that operation with the same values except
   `ExpectedMission.UpdatedAt`, changed by one second while remaining valid.
3. The expected mission is passed to `PublishGenerationOne` as the local CAS
   authority, so this is different input to the operation.
4. `Finalize` returns success with `Replayed=true`, zero additional provider
   calls, and zero additional publish calls. It does not return
   `operation_conflict` because both inputs have the same request digest.

This directly violates T027 step 6: same operation plus the same request may
replay, while operation reuse with different input must conflict. Other omitted
mission fields can instead surface as `persistence_contract`, which is also the
wrong classification: the persisted result is not corrupt; the caller reused
an operation identity for a different request.

### Required repair

Bind the request digest to every authority-bearing command value consumed by
the operation, including the complete expected mission snapshot used by the
publication CAS. Add permanent mutants for created/updated time, lifecycle,
terminal outcome, blockers, decisions, and current revision where each field
can form a valid command. Each different valid request under the same operation
must return bounded `operation_conflict` before provider observation or
publication. Exact unchanged replay must remain provider-free.

## Exact snapshot ordering law must be made consistent

This is distinct from the blocker above. The receipt currently sorts
`ActiveBlockers` and `UnresolvedDecisions`, while `sameMissionSnapshot` and the
mission CAS compare those slices in order. A probe showed that two reordered
snapshots receive the same receipt; after substituting the reordered persisted
snapshot and reissuing the correspondingly reordered command, replay succeeds
with the unchanged receipt.

The mission model validates blocker and decision slices as unique sets, and
WP07 requires stable ordering. The clean law is therefore **semantic canonical
set identity**: canonicalize these collections consistently at the mission
boundary, then use that same canonical form for equality, request commitment,
and outcome receipt. Preserving byte/order identity everywhere would also close
the alias, but mixing order-sensitive equality with order-insensitive hashing
does not. Add a permanent permutation control for the chosen law.

## Cycle-2 blocker is otherwise closed

The new domain-separated `FinalizationReceipt` covers exact canonical
`RevisionV2.Encode()` bytes (including opaque provider observations), every
field of the resulting `MissionSnapshot`, and explicit mission, operation, and
request binding. It excludes the transport-only `Replayed` flag and itself.
The commit contract passes `ExpectedReceipt` to the store and explicitly
requires atomic persistence with operation/request identity and the complete
outcome.

Independent and permanent probes confirm that replay refuses, before provider
or publication calls:

- same-ID opaque provider revision substitution;
- same-ID semantic revision substitution;
- mission outcome and timestamp substitution;
- missing, malformed, and altered receipts;
- wrong request and operation binding.

Initial publication, lost-response replay, 100-way same-operation convergence,
and 100-way distinct-operation CAS preserve the exact committed receipt under
the race detector. The source explicitly states the trusted-store boundary:
simultaneous replacement of both the complete outcome and its correctly
recomputed receipt is outside the model, and WP08 must preserve rather than
regenerate the receipt.

## Regression and scope evidence

- focused workgraph and mission suites at `-count=1` and `-count=20` — pass;
- all cycle-1 regression groups and cycle-2 receipt mutants at `-count=20` —
  pass;
- workgraph and mission race suites — pass;
- same- and distinct-operation concurrency tests under race at `-count=10` —
  pass;
- full architecture package and exact Phase-3 direction/mutant campaign at
  `-count=10` — pass;
- repository-wide internal compile-only gate, `go vet`, `staticcheck`,
  `go mod verify`, formatting, and diff hygiene — pass;
- product commit `ddc0100` changes only the two WP05-owned mission files;
  topology cleanup `fe52173` changes only mission projection/review artifacts.

The accepted scenario baseline remains non-attributable: `internal/scenario`
has the identical tree object before and after `ddc0100`
(`cf52671e1bbbead61b728bf110d77847424e4e12`). The known campaign census drift
reproduces, while isolated `IG-02` passes in 14.30 seconds.

All temporary reviewer probes were removed.

## Anti-pattern checklist

1. Dead code: **PASS with planned seam** — the store contract is staged for
   WP08 and exercised through the live finalization service.
2. Synthetic-fixture test: **FAIL** — permanent receipt mutants are strong, but
   no test kills incomplete request-digest coverage.
3. Silent empty return: **PASS** — no silent failure path was found.
4. FR coverage: **FAIL** — T027 exact replay/conflict semantics remain
   incomplete for different valid expected mission inputs.
5. Frozen surface: **PASS** — strict v1 byte compatibility and v2 schema gates
   remain green.
6. Locked decision: **PASS** — provider neutrality, generation-1 authority,
   double observation, and replay-before-provider remain intact.
7. Shared-file ownership: **PASS** — product and topology changes are cleanly
   separated and within scope.
8. Production fragility: **PASS** — no panic, fail-fast transient path, or
   secret leakage was introduced.
