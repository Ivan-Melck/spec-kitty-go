---
work_package_id: WP07
title: Run Lease Epoch and External-Effect Authority
dependencies:
- WP04
- WP06
requirement_refs:
- FR-011
- FR-017
- FR-018
- FR-020
- FR-023
- FR-024
- FR-025
- FR-034
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-007
- NFR-016
- C-004
- C-005
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T034
- T035
- T036
- T037
- T038
- T039
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/execution/
create_intent:
- internal/controlstore/gapdb/execution_authority.go
- internal/controlstore/gapdb/execution_authority_test.go
- internal/controlstore/gapdb/effect_authority.go
- internal/controlstore/gapdb/effect_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/execution/**
- internal/governancecontext/**
- internal/controlstore/gapdb/execution_authority.go
- internal/controlstore/gapdb/execution_authority_test.go
- internal/controlstore/gapdb/effect_authority.go
- internal/controlstore/gapdb/effect_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP07 — Run Lease Epoch and External-Effect Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Move run, lease, runtime-epoch, governance provenance, and external-effect
authority onto capability-owned events and one-batch GapDB publication while
preserving Spec Kitty's concurrency laws. Retries must replay committed truth,
stale authority must never mutate, and no external retry or compensation may
proceed unless revision, value, and operation marker still prove attribution.

## Context

WP04 provides sealed restore and fresh-epoch admission inputs. WP06 provides
current WorkRevision and eligibility facts used by execution admission. This WP
owns execution-slot decisions, governance context state, external-effect saga
progression, and their concrete GapDB adapters. WP08 binds workspaces to the
lease produced here; WP09 and WP10 consume its current-authority evidence.

Read the mission `spec.md`, `plan.md`, `research.md` Decisions 2, 4, and 6,
`data-model.md`, and `contracts/authority-journal-v2.md`. Read the landed WP02,
WP03, WP04, and WP06 public seams. Preserve useful behavior from the existing
`internal/execution` services, but replace overwrite-style repository mechanics
with capability commands, events, reducers, and sealed publication plans. Do
not transliterate SQLite methods or construct a broad GapDB store.

Core ordering laws:

1. validate transport/authentication syntax;
2. resolve global OperationBinding and committed OperationState;
3. replay an exact committed result even after runtime epoch rotation;
4. reject a binding mismatch before capability reads or effects;
5. only if the operation is absent, validate current epoch, WorkRevision,
   execution slot, actor/process, lease generation/fence, and governance facts;
6. publish intent/state atomically before any external mutation; and
7. after ambiguity, observe and reconcile rather than assuming absence.

ActorID is durable identity; ProcessID is one execution. RuntimeEpochID is one
admitted owner lifetime. Lease generation and fence are execution-slot
coordinates. They must remain distinct in types, hashes, diagnostics, events,
and tests.

Stay within the declared ownership globs. Other lanes may modify journal,
workgraph, workspace, handoff, or tracker packages concurrently. Depend on
their consumer-facing contracts; do not edit them from this lane.

## Branch Strategy

- Planning branch: `feat/gapdb-only-production-authority`.
- Final mission merge target: `feat/gapdb-only-production-authority`.
- Execution worktrees are allocated per the computed lane recorded in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Use the branch/worktree reported by the runtime capsule; do not infer a lane
  path or merge dependency branches manually.
- Preserve concurrent agents' edits and keep commits scoped to this WP's four
  ownership patterns.

Start or resume implementation with:

```bash
spec-kitty agent action implement WP07 --agent <name> --mission gapdb-only-production-authority-01M1GWDJ
```

## T034 — Execution-slot consumer ports, events, and governance provenance

### Purpose

Define execution authority in product terms and place persistence ports beside
the capability that owns them. Every run decision must carry immutable identity
and governance provenance without exposing GapDB keys or provider revisions to
callers.

### Implementation guidance

1. Inventory current start, replace, heartbeat, expiry, outcome, recovery, and
   effect behaviors. Preserve typed results and refusal precedence, but remove
   SQLite-shaped read/save/transaction assumptions from execution interfaces.
2. Define an execution-slot coordinate `(WorkRevisionID, WorkID)` and versioned
   state for current RunID, ActorID, ProcessID, LeaseID, generation, fence,
   deadline/heartbeat observation, RuntimeEpochID, terminal outcome, and
   governance commitment.
3. Add explicit execution events for run requested/admitted/replaced, lease
   granted/renewed/expired, outcome recorded, and predecessor authority
   invalidated as required by the closed event registry. Events describe
   product facts, not key/value operations.
4. Keep decisions and reducers pure. Accept authoritative current facts and a
   caller-supplied observed time/clock result; do not read the clock, provider,
   filesystem, Git, tracker, or process table inside decision/reducer code.
5. Define narrow consumer-owned repository ports that load an execution
   snapshot with logical/provider versions and publish a sealed CommitPlan.
   Do not expose generic Get/Put, raw keys, GapDB records, SQL rows, or a god
   transaction callback.
6. Extend `internal/governancecontext` only as needed to preserve exact action
   provenance: context ID, aggregate commitment, doctrine revision, profile
   revision, mission-type revision, config/policy revision, and source
   commitments.
7. Bind governance/profile/doctrine revisions to OperationBinding and execution
   events. They are immutable provenance even if current project governance
   later changes; replay returns the originally bound values.
8. Reject missing, duplicated, malformed, over-broad, or digest-inconsistent
   governance entries before run admission. Do not infer profile identity from
   ActorID or agent name.
9. Preserve bounded typed diagnostics with exact operation, runtime epoch,
   work revision, work, run, lease, actor, and process coordinates.
10. Add reducer, codec/round-trip, validation, provenance drift, and refusal
    ordering tests. Include a later governance revision that must not rewrite
    an already committed run.

### Validation

```bash
go test ./internal/governancecontext ./internal/execution -run 'Test.*Governance|Test.*Execution|Test.*Run' -count=1
```

## T035 — One-current lease generation, fence, heartbeat, and expiry CAS

### Purpose

Guarantee at most one current lease per execution slot while allowing unrelated
slots to progress independently. Renewal, expiry, and replacement must be
atomic authority transitions, not read-then-write conventions.

### Implementation guidance

1. Model lease generation as a strictly positive monotonic counter scoped to
   one execution slot. A replacement increments generation and receives a new
   LeaseID and unpredictable fence/token; it never inherits predecessor
   authority.
2. Require every lease-mutating command to present RuntimeEpochID, WorkRevision,
   execution slot, RunID, ActorID, ProcessID, LeaseID, generation, and fence.
   Represent every mutable fact in the same sealed publication's conditions.
3. Create/grant requires the slot's expected logical/provider revision and
   eligibility facts from WP06. Heartbeat CASes the current execution head and
   renews only that exact authority.
4. Expiry must prove the current deadline/heartbeat observation and exact
   authority coordinates. A stale expiry must not revoke a renewed lease.
5. Replacement after expiry publishes predecessor loss and successor grant
   with exact event/head membership. It must provide WP08 enough immutable
   evidence to quarantine the old workspace and allocate a fresh one; this WP
   does not transfer or mutate workspace identity.
6. Terminal outcome is monotonic and bound to current lease authority. Reject
   duplicate-conflicting outcomes, predecessor results, and mismatched actor or
   process before mutation.
7. Do not use a process mutex, PID liveness, timestamp ordering, branch name,
   or filesystem lock as authority. These may be observations only.
8. Test simultaneous initial claims, renew-versus-expire, expire-versus-
   replace, duplicate replacement, stale heartbeat, old fence after restart,
   and independent-slot progress under contention.
9. Require exactly one winning publication on same-slot races and no global
   journal/mission lock for unrelated slots.

### Validation

```bash
go test ./internal/execution -run 'Test.*Lease|Test.*Heartbeat|Test.*Expire|Test.*Replace' -count=3
go test -race ./internal/execution -run 'Test.*Lease|Test.*Heartbeat|Test.*Expire|Test.*Replace' -count=1
```

## T036 — Replay committed results before epoch validation

### Purpose

Resolve the apparent conflict between idempotent recovery and epoch fencing:
an operation that committed under a predecessor epoch remains replayable, but
an absent operation carrying predecessor authority must never execute under the
replacement epoch.

### Implementation guidance

1. Route every execution command through WP02's global operation resolver
   before current RuntimeEpoch validation or execution-slot reads.
2. If OperationBinding matches and OperationState holds a committed terminal
   result, return the exact recorded bytes/typed result with `Replayed=true`.
   Perform no capability mutation, lease renewal, side effect, or new epoch
   evaluation that could hide the recorded truth.
3. If the binding exists but differs in command kind/schema, intent digest,
   scope, actor/process, or governance coordinates, return
   `IDEMPOTENCY_CONFLICT` before capability reads and effects.
4. If the binding matches an in-progress/ambiguous operation, resume only its
   recorded reconciliation protocol. Do not re-decide it as a new command.
5. If no binding exists, validate the current RuntimeEpoch before reading other
   mutable execution facts. A predecessor or unknown epoch returns stale
   authority and requires explicit re-evaluation/new authorization; it does not
   allocate a new OperationID automatically.
6. Keep RuntimeEpochID out of the stable canonical intent digest while retaining
   originating epoch as immutable OperationBinding/event provenance.
7. On CAS loss, resolve whether the same operation committed before describing
   moved execution authority. Preserve deterministic refusal precedence.
8. Test response loss across restart, exact replay after epoch rotation,
   changed request after rotation, absent predecessor request, ambiguous
   in-progress operation, and CAS loss where the same publication won.
9. Add mutants that validate epoch before resolving committed results, include
   epoch in stable intent, or execute an absent predecessor operation; every
   mutant must be rejected.

### Validation

```bash
go test ./internal/execution -run 'Test.*Replay|Test.*Epoch|Test.*Idempot' -count=3
```

## T037 — External-effect intent, attempt, observation, and ambiguity progression

### Purpose

Represent provider work as a durable, monotonic saga so every effect is
attributable and restartable without blind mutation or duplicated commands.

### Implementation guidance

1. Replace overwrite-style `ExternalEffectRepository` behavior with an
   external-effect aggregate, pure decisions/reducer, and a narrow publication
   port. Preserve tracker portability; execution must depend on the tracker
   contract, never a Beads-specific model.
2. Model the progression explicitly:
   `intent_recorded -> attempt_started -> observation_recorded`, with failure
   paths to `ambiguous` or `reconciliation_required`, then an explicit
   `retry_allowed`, `compensation_allowed`, `refused`, or `resolved` outcome.
3. Intent records EffectID, stable OperationID, target mapping, intended value,
   expected provider revision/value/operation marker, required guarantee,
   actor/process/governance provenance, and bounded canonical request digest.
4. Publish effect intent and the corresponding OperationState/capability event
   in one batch before calling the provider. A naked reservation or an external
   call preceding durable intent is forbidden.
5. Publish attempt-start evidence before mutation where the provider protocol
   needs it. Use stable EffectID/operation marker for provider idempotency; do
   not invent a second top-level OperationID.
6. Record every provider outcome as an observation, not authority: confirmed,
   unavailable, stale, conflicting, malformed, or unknown. Response loss moves
   to ambiguity; it never means absent or success.
7. After restart, use OperationState/effect state to select observation or
   decision-required flow. Pure journal playback must dispatch no effects.
8. Keep event/effect membership cumulative and bounded across stages. Terminal
   result bytes are immutable; later observations append progression rather
   than rewrite history.
9. Test crash before intent, after intent, after attempt-start, after provider
   mutation before response, after observation before acknowledgement, and on
   each progression publication.

### Validation

```bash
go test ./internal/execution -run 'Test.*Effect|Test.*Saga|Test.*Ambigu' -count=1
```

## T038 — Compare revision, value, and operation marker before retry or compensation

### Purpose

Enforce the general saga law: recovery may undo or repeat external state only
while that state remains demonstrably attributable to the operation being
recovered.

### Implementation guidance

1. Define the attribution predicate as an exact conjunction of provider
   revision, observed value, and operation marker. All three must match the
   recorded expectation appropriate to the requested action.
2. For retry, observe current provider state first. Permit retry only when the
   recorded state and provider guarantee prove absence/non-application or an
   idempotent same-marker retry. Unknown or stale observations require a human
   decision.
3. For compensation, compare the current provider revision/value/marker with
   the observation created by this effect. A human reassignment or any
   coordinate change blocks compensation; never clear “whatever is there.”
4. Treat missing marker support honestly. If the provider cannot prove the
   required guarantee, return unsupported/decision-required rather than
   weakening attribution to value-only equality.
5. Bind each observation to the external mapping and metadata schema so a
   provider/work remap cannot inherit old effect evidence.
6. Publish retry/compensation permission and OperationState progression before
   invoking the external mutation. Recheck provider conditions if supported;
   otherwise expose the residual race explicitly in the typed result.
7. Never trust warning prose, timestamps, assignee alone, actor identity alone,
   or operation-log ownership as proof of attribution.
8. Test the canonical crash sequence: A assigns; A crashes; a human reassigns
   to B; recovery for A observes changed revision/value/marker and refuses to
   clear B's assignment.
9. Add dishonest-provider cases: stale revision with current value, current
   revision with wrong marker, forged marker with wrong value, inconsistent
   reads, malformed envelope, timeout, and availability recovery.
10. Add blind-retry, value-only compensation, marker-only compensation, and
    warning-as-success mutants; require exact mutant census kills.

### Validation

```bash
go test ./internal/execution -run 'Test.*Attribution|Test.*Compensat|Test.*Dishonest' -count=3
```

## T039 — GapDB adapters and adversarial authority tests

### Purpose

Connect execution/effect capability ports to Authority Journal v2 through the
sole GapDB adapter and prove atomicity, recovery, concurrency, and refusal laws
against realistic failure.

### Implementation guidance

1. Implement execution adapter files matching
   `internal/controlstore/gapdb/execution_*.go` and effect adapter files matching
   `internal/controlstore/gapdb/effect_*.go`. Use only WP03's canonical codec,
   reader, and sealed-plan committer.
2. Translate typed execution/effect loads into capability snapshots with exact
   logical and provider revisions. Translate sealed decisions into one
   AtomicBatch; the adapter must not interpret mission, lease, or tracker
   policy.
3. Require operation binding/state, capability events/heads, effect intents,
   and applicable eligibility conditions in the same batch. No dual write,
   optimistic projection update, legacy repository save, or fallback is legal.
4. Reconcile dropped, timed-out, cancelled, EOF, or malformed acknowledgements
   using PublicationID and exact membership. Complete matching publication
   replays; proven absence permits identical-plan retry; partial evidence
   closes authority as corruption.
5. Add same-slot contention and unrelated-slot progress tests. Run at least 100
   deterministic repetitions in the campaign-facing test path and carry the
   universal stale-authority invariant into every fault case.
6. Restart between intent, attempt, observation, heartbeat, expiry, and
   replacement stages. Verify recovered projections and OperationState equal
   uninterrupted execution and old capsules never regain authority.
7. Exercise a slow, unavailable, and dishonest tracker contract implementation.
   Assert no blind retry/compensation and no provider call during playback.
8. Add mutants for split batch, result-before-durable-ack, epoch-first replay,
   stale lease acceptance, process-only identity, naked intent reservation,
   blind retry, unsafe compensation, optimistic effect completion, and adapter
   policy interpretation.
9. Ensure diagnostics are bounded, typed, stable, and include immutable
   coordinates without provider secrets or unbounded response payloads.
10. Run format, focused race tests, execution/governance tests, and all v2
    journal tests. Confirm edits remain inside the declared ownership globs.

### Validation

```bash
go test ./internal/execution ./internal/governancecontext ./internal/controlstore/gapdb -count=1
go test -race ./internal/execution ./internal/controlstore/gapdb -run 'Test.*Lease|Test.*Effect|Test.*Contention|Test.*Dropped' -count=1
go test ./internal/controljournal/v2/... -count=1
git diff --check
```

## Definition of Done

- Execution slots have capability-owned commands, events, reducer, typed
  snapshot/publication ports, and bounded diagnostics.
- Run evidence binds ActorID, ProcessID, WorkRevisionID, WorkID, RuntimeEpochID,
  RunID, LeaseID, generation, fence, and immutable governance provenance.
- One current lease is enforced by one-batch CAS; heartbeat, expiry,
  replacement, and outcome reject stale coordinates without serializing
  unrelated slots.
- Exact committed operations replay before current-epoch rejection, while an
  absent predecessor-epoch request cannot mutate under the replacement owner.
- External effects progress from durable intent through attempts,
  observations, ambiguity, reconciliation, and terminal resolution without
  overwrite-only receipts.
- Retry and compensation require matching provider revision, value, and
  operation marker; human/provider drift produces decision-required.
- Concrete GapDB adapters translate only typed capability state and sealed
  plans and perform no policy decisions or fallback writes.
- Dropped acknowledgements, restarts, same-slot contention, unrelated progress,
  dishonest providers, stale writers, and declared mutants are covered.
- Focused tests, repeated tests, race tests, v2 regressions, formatting, and
  `git diff --check` pass with changes only in owned paths.
- Completion is recorded in the event log:

```bash
spec-kitty agent tasks mark-status T034 T035 T036 T037 T038 T039 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks

- **Epoch/idempotency inversion**: validating current epoch first hides a
  predecessor commit. Resolve matching OperationBinding/State before epoch.
- **Lease generation conflation**: keep runtime epoch, lease generation, fence,
  ActorID, and ProcessID distinct and mutation-test their equality rules.
- **Process-local correctness**: mutexes and PID liveness cannot enforce durable
  authority; all mutable preconditions belong in one provider batch.
- **Effect before intent**: no external call is allowed until intent and
  OperationState are durably published.
- **Provider semantic gravity**: execution owns portable decisions; concrete
  adapters and Beads details must not enter its events or states.
- **Unsafe compensation**: matching only assignee/value can overwrite human
  action. Require revision, value, and marker together.
- **Replay side effects**: restart reduction is pure; saga dispatch begins only
  after authority is restored/admitted and current progression is read.
- **Global serialization**: global OperationID uniqueness does not justify a
  global journal head or mission mutex.

## Reviewer Guidance

Review refusal order and atomic membership before reviewing convenience APIs.
Prove an exact predecessor result replays after epoch rotation, a changed
binding conflicts before capability reads, and a previously unseen predecessor
request refuses without mutation. Verify current lease authority is a complete
coordinate, not generation/token alone, and that unrelated execution slots can
publish concurrently.

Trace one external effect through every durable stage and every crash cut.
Confirm the provider is never called before intent, response loss becomes
ambiguity, restart does not fabricate a new OperationID, and playback cannot
dispatch effects. For compensation, reproduce the human-reassignment scenario
and verify all three attribution coordinates are checked against a fresh
observation.

Reject a broad GapDB store, direct provider keys in execution, table-shaped
ports, policy decisions in adapters, split writes, legacy saves, optimistic
acknowledgements, ActorID/ProcessID conflation, governance revision rewriting,
blind retry, value-only compensation, or tests whose mutants die only because
of unrelated parse failures.
