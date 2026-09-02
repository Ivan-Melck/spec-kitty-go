---
affected_files: []
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T15:59:48Z'
reviewer_agent: reviewer-renata-wp05
wp_id: WP05
---

# WP05 review feedback — cycle 1

Verdict: **REJECT**

Reviewer identity: `reviewer-renata-wp05` (distinct from implementer `codex-wp05`).

## Blocking finding 1 — accepted checkpoint bytes are not proven or contract-conformant

`validateCheckpoint` in `internal/controljournal/application/batch.go:380` validates only the exported wrapper fields and byte lengths. It never decodes the checkpoint, checks `checkpoint-v1.schema.json`, compares the embedded `checkpoint_id`, `project_id`, `mission_id`, `through_record_revision`, or `through_event_id` with the wrapper, or verifies that the checkpoint-head value hashes and selects those exact canonical bytes. Consequently a caller can seal an arbitrary or contradictory checkpoint into the atomic publication batch.

The current passing fixture demonstrates the problem: `internal/controljournal/application/batch_test.go:153-172` admits `{"through_revision":7}` as the checkpoint value and `{"checkpoint_id":"checkpoint-1"}` as the selector. The former omits nearly every required field in `contracts/checkpoint-v1.schema.json`; the latter does not prove the record digest or through-position required by `data-model.md` and `contracts/control-journal-v1.md:215-224`.

Remediation:

- Make prior-prefix checkpoint input a constructor-produced, content-bound capability, or fully decode and validate the exact canonical checkpoint and selector bytes before sealing the batch.
- Bind the wrapper, checkpoint bytes, selector digest, project/mission identity, through revision/EventID, schema/reducer digests, projection digest, and prior-prefix proof to one another.
- Add mutants for malformed schema, mismatched wrapper/embedded IDs and through-position, selector digest mismatch, cross-project/mission bytes, and a current-batch prefix. Each must refuse before any adapter call.

## Blocking finding 2 — aggregate CAS proves a provider revision but not the claimed logical predecessor

`NewValidatedBatchV1` validates `next.LogicalVersion == expected.LogicalVersion + 1`, then emits only `ConditionRevision(expected.ObservedHeadRevision)` (`batch.go:230-276`). `Service.Apply` does not read and decode existing aggregate heads before publication (`service.go:343-380`). Therefore an existing record at revision R with logical version 5 can be presented as `{LogicalVersion: 4, ObservedHeadRevision: R}`; the GapDB CAS succeeds and publishes another logical version 5. The current test at `batch_test.go:174-198` inspects the generated condition but never seeds a real stored head or proves that the stored logical version/coordinate matches the claimed precondition.

This violates T026's requirement to compare every expected head, FR-012, and the journal's monotonic aggregate history.

Remediation:

- Before the one mutation call, ordinary-read every existing touched head and decode it; require the record key/revision, aggregate coordinate, and logical version to match the sealed expected precondition. Creation must prove absence. The existing revision CAS then protects the read-to-commit race.
- Fail closed on corrupt/malformed heads and on a correct revision paired with a dishonest logical version, without calling `AtomicBatch`.
- Add production-path tests for mismatched logical version at the exact provider revision, mismatched coordinate, corrupt head bytes, an appearance race after an absence read, and a change race after an existing-head read.

## Blocking finding 3 — epoch admission can fabricate predecessor lineage and is not enforceably startup-only

`AdmitRuntimeEpochV1` checks that `PreviousRuntimeEpochID` is syntactically valid and that a positive current revision accompanies it (`service.go:178-195`), but it never reads/decodes `runtime/current` or the runtime aggregate head. The only pre-publication read is the new operation key (`service.go:269`). A caller can supply the real current/head revisions with a different valid predecessor epoch string; both CAS conditions pass, and the new immutable epoch/current records persist false predecessor lineage.

In addition, the exported function accepts an ordinary adapter plus caller fields and has no constructor-only owner-restart/lifecycle proof. Nothing in this boundary prevents lease expiry or a normal reconnect path from calling it. The test at `service_test.go:270-319` comments that lease expiry has no epoch API, but does not execute a lease-expiry path or kill a mutant that calls admission outside owner restart.

Remediation:

- Read and decode the prior `runtime/current` and runtime aggregate head and require their identities, logical coordinate, epoch lineage, and record revisions to match the sealed admission input before the five-record CAS.
- Make the startup-only boundary enforceable with a constructor-produced owner-lifetime/restart proof or an equally strong composition/architecture guard; a comment and separate method name are insufficient.
- Add tests proving a false predecessor with correct revisions refuses without publication, current/head semantic mismatch fails closed, lease expiry and sidecar reconnect cannot admit an epoch, every actual owner restart does, and predecessor-epoch commands/capsules refuse.

## Blocking finding 4 — budget evidence is synthetic and does not prove pre-call refusal

`TestBatchMinimumMaximumAndPermanentBoundaries` invokes `validateRawBudget` and `validateWireBudget` directly (`batch_test.go:42-57`). Deleting the production call to `validateProductBudget(product)` at `batch.go:331` leaves those assertions green. There is no constructed over-budget command/batch passed through `NewValidatedBatchV1`/`Service.Apply` with a spy proving zero adapter calls. This fails the review prompt's synthetic-fixture rule and T024's requirement that raw/wire limits are enforced before the call.

Remediation:

- Exercise exact and plus-one raw/wire boundaries through the production batch constructor and service boundary, and assert the publication port's mutation-call count remains zero on refusal.
- Add a mutant/control demonstrating that removing or bypassing `validateProductBudget` fails the suite.
- Carry the WP's required universal journal/authority assertions into both GJ-01 and GJ-02 terminal paths (the current application tests never invoke an equivalent universal assertion seam).

## Evidence that passed

- `go test ./internal/controljournal/application`
- `go test -race ./internal/controljournal/application`
- Exact admitted-binary path: `SPEC_KITTY_M1_GAPDBD=/tmp/go-kitty3-gapdb-admission.cOGcrB/gapdbd go test -race ./internal/controljournal/application -count=1`
- Focused GapDB prerequisite tests and race tests
- `go vet` on application/journal/GapDB/sidecar packages
- `staticcheck` on application and GapDB packages
- focused M1 architecture tests and `git diff --check`
- 100 repetitions of GJ-01, ambiguity reconciliation, and aggregate-concurrency unit paths

The real WP04 response-drop proxy path is genuine and passed: it consumed the durable daemon response, returned `ambiguous_response`, reconciled the complete batch by ordinary reads, and observed exactly one operation record.

## Anti-pattern checklist

1. Dead code: **N/A for this staged WP** — the public application capability is intentionally consumed by downstream WP07/WP08, which are declared dependents; no out-of-scope caller should be added here.
2. Synthetic-fixture test: **FAIL** — raw/wire budget assertions call validators directly and checkpoint acceptance uses non-contract bytes.
3. Silent empty return: **FAIL** — `batchResultType`/`batchResult` silently return empty values on decode failure (`service.go:454-467`) without a fail-loud rationale; return a typed indeterminate/refusal path or bind/verify those bytes so failure is impossible and documented.
4. FR coverage: **FAIL** — FR-011/FR-012 and NFR-001 evidence does not cover the checkpoint, semantic-head, and pre-call budget cases above.
5. Frozen surface: **PASS** — WP05's six authoritative application files are new; prerequisite changes were explicit, narrowly scoped model/adapter seams and architecture classifications.
6. Locked decision: **FAIL** — accepting nonconformant checkpoint bytes contradicts the frozen checkpoint contract; unproven epoch admission contradicts startup-only epoch semantics.
7. Shared-file ownership: **PASS** — changes outside the six application files are isolated prerequisite commits identified in the review request.
8. Production fragility: **PASS** — no new panic/bare-raise equivalent or silent transient-race panic path was found.

Contracts walked: `control-journal-v1.md`, `operation-record-v1.schema.json`, `event-envelope-v1.schema.json`, `checkpoint-v1.schema.json`, `runtime-current-v1.schema.json`, `m1-product-budgets-v1.json`, and the GJ-02 portions of `sidecar-lifecycle-v1.md`/`evidence-adoption-v1.md`. Other adoption, authority-capsule, trial, and evidence-manifest surfaces are downstream/orthogonal to WP05.
