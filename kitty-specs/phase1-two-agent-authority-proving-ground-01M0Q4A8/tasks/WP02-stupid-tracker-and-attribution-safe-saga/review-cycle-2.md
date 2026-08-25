---
affected_files: []
cycle_number: 2
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T09:32:38Z'
reviewer_agent: unknown
verdict: rejected
wp_id: WP02
---

---
affected_files:
- internal/execution/external_effect.go
- internal/tracker/fake/provider.go
- internal/controlstore/sqlite/external_effect.go
- internal/scenario/tracker_saga_test.go
cycle_number: 1
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/scenario ./internal/tracker/fake -run '^TestCycle1' -count=1 -v
reviewed_at: '2026-08-24T09:31:59Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP02
---

# WP02 Review Cycle 1 — Rejected

Reviewer: `mini-local` using Reviewer Renata

Candidate implementation: `3e19931b9332fd91822b2aa25d603bd6e3a83849`

Implementation base: `1b9c04a`

## Verdict

**REJECT.** The provider store is independently durable and the ordinary ambiguity and compensation cases pass, but the committed implementation loses immutable compensation evidence in the central cross-store crash window. It also lacks the required real fault-boundary proof, emits incomplete command diagnostics, returns an incorrect marker for comment mutations, and adds unversioned lazy SQLite schema.

## Findings

### High — Pending replay overwrites immutable pre-effect evidence after the provider applies

`internal/execution/external_effect.go:108-139` handles confirmed and ambiguous records but does not recover `EffectPending`. If the provider applies an assignment and the subsequent control-store save at `internal/execution/external_effect.go:182` fails or the process crashes, replay reads the pending record, falls through to a fresh provider read, and reconstructs the effect with the already-applied assignment as `PriorValue` and its new revision as `PriorRevision`. The later idempotent provider replay then confirms this corrupted record. Compensation restores the assigned agent rather than the original assignee.

An exact-candidate review probe injected failure on the repository's second save (after provider apply, before confirmed observation), replayed the same operation, and then compensated. It failed with:

```text
recovery corrupted compensation evidence: ... Assignee:agent-a ... Revision:3
```

Reproduce by adding `TestCycle1PostProviderApplyPreObservationRecoveryPreservesCompensationEvidence` beside the scenario tests and running the frontmatter command. The test should wrap the real SQLite repository, fail only its second `SaveExternalEffect`, then reopen/replay/compensate and require the original seeded assignee and original prior revision to remain immutable. Recovery of a durable pending effect must inspect marker/revision and continue from the stored prior evidence, not rebuild that evidence from current provider state.

This violates T007/T008, FR-016, NFR-004, and the definition-of-done requirement that cross-store crash windows be observable and attribution-safe.

### High — The required SR-03/SR-04 crash boundaries and independent-process saga path are absent

No WP02 production or scenario code names or invokes `SR-03` or `SR-04`; `internal/execution/external_effect.go` has no injected fault hook, and the owned `internal/helpercmd/tracker.go` process surface was not created. `internal/scenario/tracker_saga_test.go` invokes `TrackerSaga` in-process. `FailureTimeoutAfterApply` models a returned error, but cannot stop a process after the separately durable provider mutation and before SQLite records its observation—the exact window that exposes the preceding corruption.

The committed suite therefore cannot prove FR-019's named boundaries or restart recovery across independently durable processes. This is mutation-visible: deleting the initial pending `SaveExternalEffect` at `internal/execution/external_effect.go:136` still leaves `go test ./internal/scenario -run TrackerSaga -count=10` passing. Add a helper/process scenario that terminates at both named boundaries, reopens both stores, replays the same operation, and proves the immutable prior evidence and compensation result.

### High — Mutating command envelopes and provider refusal diagnostics are incomplete

`TrackerAssignRequest` at `internal/execution/external_effect.go:62-68` omits the required command schema version, `ActorID`, `ProcessID`, injected command timestamp, and expected aggregate revision. `Compensate` at line 201 accepts only an operation ID. Consequently `effectAggregates` cannot include actor/process attribution. In addition, `providerFailure` at lines 273-302 translates only unavailable and revision-conflict errors; not-found/invalid provider refusals escape as raw `*tracker.Error` without the typed execution diagnostic envelope, retry safety, aggregate identifiers, observed/expected state, or recovery actions.

An exact-candidate probe assigning a nonexistent work key failed with:

```text
provider refusal escaped execution diagnostics: *tracker.Error tracker not_found: work does not exist
```

Add table-driven assertions for every rejection/refusal path, including provider read failures before an effect exists and compensation read failures, requiring the complete command/result diagnostic envelope and exact actor/process attribution.

### Medium — Comment mutations report the assignment marker instead of their operation marker

`CompareAndAddComment` calls the shared `mutation(work, ...)` helper at `internal/tracker/fake/provider.go:87` and `:105`. That helper returns `work.AssignmentMarker`, not the comment request's marker. A comment on unassigned work therefore returns an empty marker; a comment on assigned work returns an unrelated assignment marker. Replay has the same defect.

An exact-candidate probe failed with:

```text
comment mutation marker="", want its operation marker
```

Add fresh and replay comment assertions requiring `Mutation.Marker == CommentRequest.Marker`. The existing provider test asserts the revision but not the marker, so it does not pin T006's operation-marker contract.

### Medium — External-effect tables bypass the versioned migration contract

`internal/controlstore/sqlite/external_effect.go:15-43` defines its own DDL, and both read and write paths execute `CREATE TABLE IF NOT EXISTS`. `internal/controlstore/sqlite/migrations.go` remains at schema version 3 and contains no external-effect migration. Thus databases with and without the WP02 tables advertise the same version, a read mutates database schema, and compatibility cannot be validated through the normal migration gate. Move these durable control-state tables into the versioned migration sequence and add exact-prior empty/used migration and reopen tests.

## Validation evidence

- `go test -count=1 ./internal/tracker/... ./internal/execution ./internal/controlstore/sqlite` — PASS (`sqlite` 4.388s).
- `go test ./internal/scenario -run TrackerSaga -count=10` — PASS (1.207s).
- `go test ./internal/scenario -run TrackerSaga -count=100` — PASS (8.348s).
- `go test ./internal/tracker/... ./internal/execution -count=20` — PASS.
- `go test -count=1 ./...` — PASS (scenario 12.166s).
- `go test -race -count=1 ./...` — PASS (scenario 13.338s; no race report).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `git diff --check 1b9c04a..3e19931b` — PASS.
- Exact-candidate first-open probe, 5 rounds × 32 synchronized independent OS processes against one fake-provider path — PASS (0.240s).
- Review probes: post-apply/pre-observation recovery — **FAIL**; missing-work diagnostic completeness — **FAIL**; comment marker fidelity — **FAIL**.

## Mutation sensitivity

- Delete compare-before-compensate attribution guard — KILLED by committed provider and human-reassignment scenario tests.
- Delete fake-provider compensated replay fast path — KILLED by committed repeated-compensation provider test.
- Delete initial durable pending-effect save — **SURVIVED** `go test ./internal/scenario -run TrackerSaga -count=10`; the required separate-durability invariant is not pinned.

## Anti-pattern checklist

1. Dead code: **FAIL** — `TrackerSaga` and its provider path have no non-test production consumer; the planned helper/process surface is absent.
2. Synthetic-fixture test: **FAIL** — saga scenarios remain in-process and cannot exercise the specified process-crash boundaries between durable stores.
3. Silent empty return: **PASS** — reviewed no-op returns are explicit idempotent/compensation states.
4. FR coverage: **FAIL** — FR-019 SR-03/SR-04 and the complete mutating command envelope are not exercised.
5. Frozen surface: **N/A** — no WP02 frozen file is identified.
6. Locked decision: **FAIL** — unversioned lazy DDL conflicts with the versioned control-store model.
7. Shared-file ownership: **PASS** — candidate production changes stay within WP02-owned files.
8. Production fragility: **FAIL** — a single control-store save failure after external apply causes durable replay to corrupt compensation evidence.

## Passing contract areas

The fake tracker persists separately from SQLite, uses process locking and atomic replacement, and exposes no lease/mission/workspace/Git semantics. Timeout-before/after observation, revision CAS, later human reassignment preservation, valid compensation, repeated compensation no-op, and tracker non-authority assertions all pass on their ordinary in-process paths. Those strengths do not make the failed crash-recovery and diagnostic contracts safe to approve.
