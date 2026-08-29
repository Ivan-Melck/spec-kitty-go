---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T08:19:59Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 review — cycle 1

Verdict: **rejected**. The transaction-local implementation and typed restore paths are promising, and the bounded focused, race, architecture, compile, and static gates pass. Five storage-contract defects nevertheless violate T041–T047 and allow durable authority to disagree with the domain model.

## 1. Blocker — `phase3_outbox` is an immutable event copy, not a delivery outbox

`internal/controlstore/sqlite/migrations.go:291` stores only sequence, event identity, kind, digest, and creation time, then makes every row immutable. There is no durable delivery state, attempt count, availability/lease time, completion time, or atomic claim/ack contract. An executable schema probe verified that none of `delivery_status`, `attempt_count`, `available_at`, or `delivered_at` exists. A crash cannot distinguish never-attempted, in-flight, retryable, or delivered work, so the table cannot uphold the promised delivery boundary.

Remediation: separate immutable event evidence from mutable, narrowly controlled delivery state. Add a transactional enqueue plus an atomic claim/retry/ack protocol with bounded attempts/timestamps and typed contention outcomes. Event payload/evidence must remain immutable and secret-safe; only delivery metadata may change through the dedicated outbox port. Add fault/reopen mutants after enqueue, claim, failed attempt, and acknowledgement proving no lost or duplicate logical delivery.

## 2. Blocker — operation identity is not global across storage namespaces

`phase3_operations` at `internal/controlstore/sqlite/migrations.go:161` is independent of the legacy `operations` namespace. A direct probe inserted the same valid operation ID into both tables successfully. This permits one caller identity to mean two different requests/results depending on which adapter is asked, violating the explicit v1/v2 namespace and replay law.

Remediation: enforce bidirectional exclusion structurally for every legacy and phase-3 operation namespace (a shared operation-identity registry is preferable; symmetric insert triggers are acceptable if complete). The rule must be order-independent: legacy-then-v2 and v2-then-legacy both fail before any result, event, outbox, child row, or pointer write. Add reopen and concurrent two-Store mutants, plus same-namespace exact replay controls.

## 3. Blocker — the current revision pointer does not bind mission, revision, and generation

`phase3_current_work_revisions` at `internal/controlstore/sqlite/migrations.go:224` references only `work_revision_id`. A direct probe created a valid revision for `mission-01`, generation 1, then successfully inserted a current pointer for `mission-foreign`, generation 99. The schema therefore admits a durable pointer that no `RevisionV2` can justify.

Remediation: give the revision table a composite candidate key for `(work_revision_id, mission_id, generation)` and make the current pointer reference that exact triple (with the mission FK/mission current pointer consistency enforced as appropriate). Exercise wrong mission, wrong generation, substituted revision, pointer-without-revision, and partial late-fault mutants. Each must roll back all operation/result/event/outbox/current/mission writes; exact replay remains unchanged.

## 4. Blocker — eligibility rows are not bound to their reconciliation or revision rows

`phase3_eligibility_facts` at `internal/controlstore/sqlite/migrations.go:245` has no FK for its old/new revisions and no composite relation to the referenced reconciliation header. `WorkEligibilityFact` at `internal/controlstore/sqlite/reconciliation_v2.go:263` reads the row in isolation and merely recomputes `allowed` and `reason`. A probe inserted a row whose successor contradicted the reconciliation header and did not exist in `phase3_work_revisions_v2`; restore accepted it because its locally derived policy fields matched.

Remediation: structurally bind each fact to the exact reconciliation old/new pair and existing v2 revision rows. Restore through `NewEligibilityFact`/`Complete`, but also join and compare the reconciliation header and exact persisted revisions before returning authority. Treat `allowed` and `reason` only as redundant corruption checks, never authority. Add old/new/header/work/status/disposition/operation substitution mutants, direct `allowed`/reason corruption, incomplete/all-or-none publication faults, and reopen checks proving refusal without downstream effects.

## 5. Blocker — artifact publication has no expected-latest CAS

`artifact.AtomicPublishRequest` at `internal/artifact/service.go:152` contains no expected-latest revision identity or generation. `PublishAtomic` at `internal/controlstore/sqlite/artifact.go:123` unconditionally computes `MAX(revision_number)+1`. The permanent race test at `internal/controlstore/sqlite/artifact_test.go:66` consequently requires all 100 distinct concurrent stale callers to succeed as revisions 1–100. T044 requires the opposite authority law: publication under an expected latest value, with exactly one winner among independent Stores and typed stale results for losing requests, while exact same-operation replay converges.

Remediation: extend the consumer-owned artifact publication contract (and WP ownership if required) with an explicit expected-latest value, bind it into the request digest, and compare it inside the same write transaction before any insert. Add a 100-way, two-independently-opened-Store, no-sleep test proving one distinct-operation CAS winner; losers must be typed stale/conflict as appropriate, with zero orphan revision/validation/operation/event/outbox rows. Separately prove same-operation/same-digest convergence and changed-digest conflict before mutable reads.

## Validation evidence

- Adversarial schema/restore probes reproduced all four structural defects above; the temporary probe file was removed after capture.
- `go test ./internal/controlstore/sqlite -run 'Test(Phase3|MissionCreate|ArtifactPublication|GenerationOne|SuccessorPublication|MissionAction|GovernanceContext)' -count=3` — pass.
- `go test -race ./internal/controlstore/sqlite -run 'Test(Phase3MissionCreate|ArtifactPublicationAtomic|GenerationOneAtomic|SuccessorPublication|MissionAction)' -count=1` — pass.
- `go test ./internal/architecture -count=1` — pass.
- `go test ./internal/controlstore/... -run '^$' -count=1` — pass.
- `staticcheck ./internal/controlstore/...` — pass.

These green gates show the existing tests are stable; they do not cover the failed storage invariants. Repair should retain the current transaction-local helper discipline and add zero-write/rollback mutants for every structural refusal above.
