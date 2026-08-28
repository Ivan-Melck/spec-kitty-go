# WP08 review — cycle 2

Verdict: **changes requested**. Repair commit `005ddabf` materially improves all five cycle-1 areas. Artifact expected-prior CAS and eligibility canonical reconstruction now pass their adversarial controls. Four executable failures remain across outbox recovery, global operation identity, and current-pointer all-or-none integrity.

## 1. Blocker — a claimed outbox envelope can be stranded permanently after process death

`ClaimOutbox` selects only `pending` and `retryable` rows. The public `OutboxStore` has no lookup or expired-claim recovery operation. A probe claimed an envelope, closed the store to model process death, reopened it, advanced time by one year, and attempted a replacement claim. It returned `found=false` with no error. The only recovery methods require both the lost sequence and the dead process's claim ID, so durable `claimed` state is not promised delivery state.

Remediation: give claims a bounded durable expiry/lease and provide a CAS recovery path that a replacement process can discover and execute without volatile state from the dead process. A legal design may reclaim an expired `claimed` row directly with a new claim ID, or atomically make it retryable and then claim it. In either case, bind the prior status/claim/expiry and increment attempts exactly once. Add crash-after-successful-claim + reopen tests, not only rollback-during-claim tests; prove pre-expiry refusal, post-expiry one-winner recovery across two independently opened Stores, wrong/dead claim refusal, and no mutation of immutable event identity.

## 2. Blocker — the outbox schema admits temporally impossible legal-status transitions

The v13 `claimed` check validates non-null timestamps but does not require `claimed_at >= available_at >= created_at`. A direct SQL probe transitioned a pending event to `claimed` with `claimed_at` one day before event creation and availability; SQLite accepted it. This is a legal status edge under the trigger, so the trigger cannot compensate for the missing temporal predicate.

Remediation: close the transition class structurally. At minimum enforce:

- pending: `available_at >= created_at`;
- claimed: `claimed_at >= available_at >= created_at`, and on a reclaimed retry `claimed_at >= last_attempt_at`;
- retryable: `last_attempt_at >= claimed_at` for the completed claim, `available_at >= last_attempt_at`, and no live claim;
- delivered: `last_attempt_at >= claimed_at` for the completed claim and `delivered_at >= last_attempt_at`, with no live claim.

If clearing `claimed_at` on retry/delivery prevents durable validation of those relations, retain a separate immutable/current-attempt timestamp sufficient to prove them. Add direct-SQL mutants for pre-creation claim, pre-availability claim, backward retry availability, backward attempt, backward delivery, empty/invalid claim ID, skipped statuses, same-status mutation, and attempt-count jumps. Every mutant must fail without changing the envelope.

## 3. Blocker — OperationID remains non-global between the two legacy namespaces

The new triggers correctly exclude `phase3_operations` from both legacy tables in both insertion orders. They do not exclude `operations` from `work_graph_operations`. A probe inserted the same OperationID into both legacy tables successfully. The result is still two durable meanings for one supposedly global operation identity depending on the replay port used.

Remediation: enforce pairwise, bidirectional exclusion across all three namespaces: phase3↔operations, phase3↔work_graph_operations, and operations↔work_graph_operations. Preserve exact same-table replay behavior. Add both insertion orders for every pair, two-Store concurrent attempts, reopen evidence, and zero-write assertions for result/child/event/outbox rows after refusal.

## 4. Blocker — the current-pointer relation is not all-or-none in the mission-to-pointer direction

The composite `(work_revision_id, mission_id, generation)` foreign key correctly rejects foreign mission, false generation, missing revision, and substituted revision pointers. However, because `phase3_missions.current_work_revision_id` is nullable, its composite FK is vacuously satisfied when NULL. A probe created a valid mission with no current revision, inserted its revision row, then successfully inserted `phase3_current_work_revisions`. The durable pointer said generation 1 was current while the mission authority still said no revision was current.

Remediation: enforce the bidirectional invariant: a current-pointer row exists if and only if the mission's current revision is non-null, and both name the same revision/mission/generation. One compatible ordering is to update the mission to the new revision first under the deferred FK, then insert/update the pointer under a trigger that requires the mission already names that exact revision; forbid clearing the mission pointer while a pointer row remains. Apply the same ordering to finalization and successor publication. Add pointer-with-null-mission, mission-without-pointer, clear-one-side, foreign/missing/false-generation, late-fault, reopen, and 100-way two-Store mutants. Each aggregate must leave complete prior or complete successor authority only.

## Bounded validation evidence

- Temporary adversarial probes reproduced all four failures above and were removed before verdict capture.
- `go test ./internal/controlstore/sqlite -run 'Test(Phase3OutboxDelivery|Phase3OutboxClaimBoundary|OperationIdentityCannot|CurrentWorkRevisionPointer|SuccessorPublication|ArtifactPublication|GenerationOne|Phase3Migration|GovernanceContext)' -count=3` — pass.
- `go test -race ./internal/controlstore/sqlite -run 'Test(Phase3OutboxDelivery|ArtifactPublicationHundred|MissionCreateHundred|SuccessorPublication|GenerationOne)' -count=1` — pass.
- `go test ./internal/architecture -count=1` — pass.
- `staticcheck ./internal/controlstore/... ./internal/artifact` — pass.
- Repair commit `005ddabf` changes 11 governed files; the complete product surface remains within the 15-file WP08 ownership set. Historical migration text v1–v12 remains unchanged.

Anti-pattern checklist: dead code PASS; synthetic-fixture coverage PASS for the exercised repair paths; silent empty return PASS; FR coverage FAIL because promised outbox recovery and global identity/all-or-none pointer invariants remain unproved and false; frozen surface PASS; locked decisions FAIL for the same durable-authority contradictions; shared-file ownership PASS under the recorded artifact-contract ownership expansion; production fragility PASS.
