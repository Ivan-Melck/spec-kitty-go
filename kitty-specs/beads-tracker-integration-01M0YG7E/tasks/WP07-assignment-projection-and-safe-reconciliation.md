---
work_package_id: WP07
title: Assignment Projection and Safe Reconciliation
dependencies:
- WP05
requirement_refs:
- C-004
- C-012
- FR-012
- FR-014
- FR-015
- FR-016
- FR-017
- FR-022
- NFR-004
- NFR-005
- NFR-009
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
subtasks:
- T032
- T033
- T034
- T035
- T036
- T037
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/assignment.go
- internal/tracker/beads/assignment_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/assignment.go
- internal/tracker/beads/assignment_test.go
- internal/execution/external_effect.go
- internal/execution/external_effect_test.go
- internal/controlstore/sqlite/external_effect.go
- internal/controlstore/sqlite/external_effect_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP07 - Assignment Projection and Safe Reconciliation

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Project human-visible assignment into Beads without transferring execution
authority, overstating provider guarantees, or overwriting a human during
recovery. Adapt the existing tracker external-effect saga to capability-aware
assignment, observation-first replay, explicit ambiguity, and durable safe
reconciliation when Beads v1.2.2 cannot conditionally compensate.

The shipped behavior must prefer a stale visible assignment plus an actionable
reconciliation record over an unsafe cleanup. A compensation may be marked done
only when one provider primitive enforces expected observation, expected value,
and the operation marker at the mutation boundary.

Implementation command:

```bash
spec-kitty agent action implement WP07 --agent codex
```

## Context

The Phase 1 external-effect saga already records intent before provider calls,
observes ambiguous results, replays by operation marker, and protects a human
assignment through compare-before-compensate in the strong durable fake. Preserve
those gains. This WP generalizes the saga so a weaker production adapter can
report what it actually guarantees without weakening the fake provider.

WP01 supplies provider-neutral assignment, observation, capabilities, errors,
and narrow interfaces. WP03 supplies the bounded schema-1 command protocol. WP04
supplies canonical binding, preflight, and embedded-writer serialization. WP05
supplies stable work mapping, metadata, catalog reads, and content-derived
observations. Use those accepted seams; do not create parallel IDs, commands,
bindings, revisions, diagnostics, or metadata codecs.

Beads v1.2.2 exposes atomic `bd update --claim` for its documented claim
semantics, but it does not expose arbitrary expected-revision update or
conditional clear. Adapter serialization coordinates Spec Kitty processes only
and cannot fence a human or direct `bd` writer. Consequently:

- observation revision detects drift but is not CAS;
- an atomic claim is used only when its actor/status semantics exactly match the
  requested projection;
- unguarded assignment is used only under an explicit application policy and is
  returned as unguarded;
- marker/value observation can prove replay, but a partially visible result
  remains ambiguous rather than being guessed;
- arbitrary automatic compensation is unsupported for Beads v1.2.2 even when a
  preceding read appears to match.

The local execution lease, fencing token, workspace generation, reviewer
independence, pinned `WorkRevision`, and serialized Git integration remain
authoritative regardless of tracker availability or assignment state.

### Required state distinctions

Keep these outcomes distinct in typed results and durable effect evidence:

1. confirmed assignment with actual post-observation;
2. same-operation replay proven by assignee plus operation marker;
3. unchanged prior state eligible for the exact safe retry policy;
4. ambiguous apply status requiring observation;
5. provider unavailable with local authority unchanged;
6. revision drift or wrong value requiring reconciliation;
7. attribution lost because a human/provider changed the state;
8. safe compensation unsupported because no conditional primitive exists;
9. compensation confirmed by an actual provider observation;
10. idempotency conflict caused by operation-ID or marker reuse.

Do not collapse unsupported capability into attribution loss: the former says
the provider cannot enforce a guard; the latter says observed state is no longer
attributable. Both preserve provider state, but recovery guidance differs.

## Branch Strategy

Work only in the Spec Kitty WP07 lane created from the accepted WP05 result.
Verify the dependency base contains WP01-WP05 contracts before editing. Adapt to
their accepted APIs rather than duplicating or replacing them.

The planning and merge target is `feat/beads-tracker-integration`. Do not edit
the primary checkout, create worktrees manually, merge another active lane,
push to `main`, or change files outside the six exact owned paths.

## T032: Implement capability-aware assignment observation and contract

**Purpose**: Expose exactly which assignment operation is available and return
a complete known observation before policy chooses whether to mutate.

**Files**:

- `internal/tracker/beads/assignment.go`
- `internal/tracker/beads/assignment_test.go`

**Implementation requirements**:

1. Implement the accepted provider-neutral `AssignmentProjection` port using
   WP05 catalog/mapping and revision code. Do not expose Beads payload structs
   or CLI status strings outside `internal/tracker/beads`.
2. `ObserveAssignment` must resolve the stable external mapping and return exact
   assignee, assignment marker, content-derived revision/full observation,
   provider timestamp when present, and observation time.
3. Treat absent assignee, empty assignee, absent marker, and empty marker
   according to WP01's explicit presence semantics. Never invent a marker from
   the actor, process, timestamp, or current lease.
4. Validate project/work/provider mapping, binding readiness, operation ID,
   actor mapping, expected observation, and requested guarantee before command
   start or embedded-writer lock acquisition.
5. Branch on declared typed capability levels, never on provider name in the
   execution package. The adapter may translate v1.2.2 capabilities internally.
6. Represent atomic claim, explicitly permitted unguarded projection, and
   unsupported guarded projection as different outcomes. Do not call all three
   compare-and-set.
7. Atomic claim is eligible only when the mapped provider actor and documented
   claim/status transition exactly implement the requested projection. Otherwise
   refuse or require the explicit unguarded policy.
8. Keep assignment informational. No method accepts authority sufficient to
   mint/extend a lease or allocate/rebind a workspace.

**Tests**:

- observation returns exact value, marker, mapping, and revision;
- missing/conflicting mapping refuses without command or lock activity;
- atomic-claim eligibility rejects mismatched actor/status semantics;
- guarded-update requests fail before mutation when capability is unguarded;
- capability changes do not alter existing Spec Kitty lease records.

## T033: Project assignment with replay and ambiguity recovery

**Purpose**: Apply one visible projection when allowed and recover lost command
responses without duplicating or falsely confirming external effects.

**Files**:

- `internal/tracker/beads/assignment.go`
- `internal/tracker/beads/assignment_test.go`

**Implementation requirements**:

1. Require an application-authorized request with stable `OperationID`, mapped
   actor, exact external work reference, expected observation, desired assignee,
   and minimum guarantee. The adapter itself does not authorize work.
2. Observe before mutation. Exact assignee plus exact operation marker proves a
   same-semantic replay. Same marker with another value or another marker/value
   at the expected work item is a typed conflict.
3. If prior state remains unchanged, invoke only the command selected by the
   explicit capability/policy. Hold the WP04 embedded writer lock for the whole
   mutating command and immediate post-observation.
4. Use WP03 typed commands with schema envelopes; do not parse human output or
   build raw argv. Preserve WP03 `not_started`, `not_applied`, `applied`, and
   `ambiguous` transport classifications.
5. After success, independently observe and return the actual provider state.
   A successful exit without the requested observable value/marker is not a
   confirmed projection.
6. After timeout, truncation, malformed success, signal, or lost response,
   observe before retry. Confirm only exact value-plus-marker evidence.
7. If claim and marker cannot be attached/queryable as one atomic provider
   effect, report the weaker guarantee. A value without its operation marker is
   partial/ambiguous evidence, not proof that this operation applied.
8. Never generate a replacement operation ID automatically. Same-operation
   retry is allowed only by the typed recovery result.

**Tests**:

- confirmed assignment returns the actual post-observation;
- exact replay is a no-op and starts no second mutation;
- marker reuse with different semantics is rejected;
- timeout-before-apply observes unchanged state and follows safe retry policy;
- timeout-after-apply observes exact effect without duplicate mutation;
- partial assignee/marker outcomes remain ambiguous;
- human/direct change between observation and command is never described as CAS.

## T034: Refuse unsafe compensation and preserve human changes

**Purpose**: Enforce compare-before-compensate as a provider primitive, not a
local read followed by an unguarded update.

**Files**:

- `internal/tracker/beads/assignment.go`
- `internal/tracker/beads/assignment_test.go`

**Implementation requirements**:

1. Begin compensation with a fresh complete assignment observation for
   diagnostic/reconciliation evidence.
2. Require a conditional provider primitive that enforces, in the same mutation:
   expected observation/revision, expected current assignee, and the operation
   marker being compensated.
3. For Beads v1.2.2 arbitrary assignment cleanup, return
   `safe_compensation_unsupported` before any update command and before acquiring
   a writer lock. A matching read does not change this result.
4. If fresh state differs from expected value/marker, return
   `attribution_lost`, preserve the exact human/provider state, and include the
   actual observation in the typed result.
5. If a future accepted provider capability supplies the full conditional
   primitive, call only that primitive and independently observe its actual
   result. Do not silently fall back to an unguarded update.
6. Keep successful fake-provider compensation behavior intact through the
   provider-neutral interface. The fake remains the strong reference.
7. Distinguish unsupported, attribution lost, ambiguous, unavailable, and
   confirmed compensation in tests and diagnostics.
8. Never report compensated unless a coherent provider mutation and observation
   exist. A stale visual projection is acceptable durable debt.

**Required mutant**: replace safe refusal with read-compare-`update`; the test
must fail because the mutation spy records a command even though the read matched.

## T035: Adapt the execution saga without authority leakage

**Purpose**: Consume capability-aware assignment outcomes while retaining stable
command status, retry guidance, idempotency, and local execution authority.

**Files**:

- `internal/execution/external_effect.go`
- `internal/execution/external_effect_test.go`

**Implementation requirements**:

1. Depend on the narrow provider-neutral assignment/observation capabilities,
   not the Beads adapter and not a provider-name switch. Existing fake-provider
   saga callers must remain source-compatible where practical.
2. Extend effect/compensation statuses for unsupported safe compensation and
   reconciliation-required outcomes without reusing `compensated`.
3. Persist effect intent before the provider call. Preserve immutable operation,
   correlation, actor, process, work mapping, prior observation, desired value,
   and marker across every replay.
4. Use actual observations returned by the adapter; do not synthesize provider
   revisions or splice historical mutation fields with current human values.
5. Map unsupported compensation to a stable decision-required diagnostic with
   `RetryNever` for automatic mutation and recovery actions that preserve the
   provider state and request explicit reconciliation.
6. Keep unavailable and ambiguous assignment from invalidating a valid local
   lease. They create visible provider debt while execution authority remains
   governed by existing run/lease policy.
7. Preserve same-operation replay and reject different semantic input under one
   operation ID before provider mutation.
8. Keep diagnostic output bounded and free of comment/work bodies, raw command
   output, environment secrets, or provider-specific prose.

**Tests**:

- every new provider error maps to one stable execution status/retry policy;
- unsupported compensation never becomes `CompensationDone`;
- replay returns the original coherent provider observation;
- provider failure leaves lease/fence/workspace authority unchanged;
- no import or type reference to `internal/tracker/beads` enters execution.

## T036: Persist actual observations and reconciliation state safely

**Purpose**: Make restart recovery depend on durable provider reality rather
than process memory while preserving immutable intent evidence.

**Files**:

- `internal/controlstore/sqlite/external_effect.go`
- `internal/controlstore/sqlite/external_effect_test.go`

**Implementation requirements**:

1. Extend the existing external-effect persistence path to retain the new
   response/compensation statuses and append the exact revision, value, marker,
   and observation time returned by the provider.
2. Preserve immutable intent fields in the `ON CONFLICT` predicate. A replay
   may advance status/evidence only for the same semantic operation.
3. Unknown/ambiguous/unavailable outcomes append zero/absent observation values
   rather than fabricating the state the provider probably has.
4. Safe-compensation-unsupported and attribution-lost outcomes append the fresh
   actual observation and remain decision/reconciliation states. Neither may
   write a compensation revision or `compensated` status.
5. On read/reopen, reconstruct the latest coherent observation tuple from the
   append-only observation ledger so saga recovery does not lose it. Do not
   combine revision from one row with value/marker from another.
6. Repeated identical terminal replay must not erase better evidence or append
   contradictory fabricated state. Preserve the strongest last proven
   observation according to explicit state rules.
7. Use the existing external-effect tables and observation ledger. Do not edit
   `migrations.go`, overload WorkRevision graph reconciliations, or introduce an
   unowned schema change in this WP.
8. Keep all updates transactional and map busy/error paths to existing stable
   control-store diagnostics.

**Tests**:

- close/reopen retains unsupported status plus exact actual observation;
- immutable intent cannot be overwritten by changed assignee/marker input;
- ambiguous and unavailable rows contain no invented provider tuple;
- attribution-lost stores the human value and never a compensation revision;
- concurrent same-operation saves converge or return typed idempotency conflict;
- a torn/spliced observation tuple can never be returned.

## T037: Prove race, restart, human-change, and universal invariants

**Purpose**: Demonstrate that provider time, process lifetime, and local
authority may disagree without causing human overwrite or authority corruption.

**Files**:

- all six owned implementation/test files

**Implementation requirements**:

1. Run at least 100 repetitions where a human/direct writer changes assignment
   between Spec Kitty observation and compensation. Assert zero provider writes
   by recovery and exact preservation of the human value.
2. Inject timeout-before-apply, timeout-after-apply, unavailable, malformed
   result, stale revision, wrong marker, partial marker/value, control-store busy,
   and crash/lost-save boundaries.
3. Restart the saga from durable SQLite state after every relevant boundary.
   Recovery must produce the same classification as an uninterrupted run from
   the same proven observations.
4. Include a stale process resuming after lease expiry. Its provider assignment
   or marker must not renew authority, transfer the quarantined workspace, or
   authorize handoff/integration.
5. Reuse existing universal invariant assertions where reachable without
   editing unowned files. At minimum prove one active lease, fence enforcement,
   workspace non-transfer, actor-level review separation, and serialized
   integration remain unchanged around provider failures.
6. Test both strong fake compensation and Beads safe refusal through the same
   saga contract. Capability absence may change the external-effect disposition,
   never the authority laws.
7. Run all involved packages under the race detector; no known race is waived.
8. Verify adapter, execution, and persistence diagnostics contain no payload
   bodies, secrets, or human-output transcripts.

**Exit evidence**:

```bash
go test ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite -run 'Assignment|Compensat|ExternalEffect|Human|Invariant' -count=10
go test -race ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite
```

## Test Strategy

Develop from the safety boundary inward: first make adapter compensation refuse
before mutation, then adapt saga statuses, then persist/reload exact observations,
and finally run restart/race campaigns. Use deterministic clocks, stable
operation IDs, temporary provider/control stores, and named fault boundaries.

Unit tests must assert both returned classification and negative side effects.
For safe refusal, snapshot command history, writer-lock acquisition count,
provider state, SQLite effect state, and relevant authority rows before/after.
An error without a non-mutation assertion is insufficient.

During development:

```bash
go test ./internal/tracker/beads -run 'Assignment|Compensat'
go test ./internal/execution -run 'Tracker|ExternalEffect'
go test ./internal/controlstore/sqlite -run 'ExternalEffect'
```

Before handoff:

```bash
go test ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite -count=10
go test -race ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite
go vet ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite
```

Do not require a network or real Beads installation for the focused tests. WP08
will run the real-provider campaign. This WP uses the dishonest schema-1 command
harness and accepted fake provider to prove all semantic branches offline.

## Definition of Done

- Assignment observation returns exact provider value, marker, and revision.
- Projection branches on typed guarantee levels and confirms only actual
  post-observation evidence.
- Replay requires the same operation, assignee, and marker; partial state is
  explicit ambiguity.
- Beads v1.2.2 safe compensation refuses before lock or update even when a
  preceding read matches.
- Direct human assignment always survives automatic recovery.
- Unsupported, attribution-lost, ambiguous, unavailable, and compensated are
  distinct durable statuses with stable diagnostics.
- SQLite reload returns one coherent actual observation tuple and immutable
  operation intent.
- Existing strong fake-provider compensation remains operational and honest.
- Provider failure never grants, renews, transfers, reviews, accepts, or
  integrates work.
- One hundred human-change repetitions produce zero automated overwrites and
  zero universal authority invariant violations.
- Focused, repeated, race, vet, and diff checks pass with no waiver.
- Only files listed in `owned_files` are changed.
- Record all subtask completion through the event-sourced status command:

```bash
spec-kitty agent tasks mark-status T032 T033 T034 T035 T036 T037 --status done --mission beads-tracker-integration-01M0YG7E
```

## Risks

- **False CAS**: observation plus local serialization can look conditional but
  cannot fence a direct provider writer. Keep guarantee names exact.
- **Partial marker application**: assignee and operation metadata may become
  visible separately. Never treat one half as confirmed replay.
- **Human overwrite**: a matching pre-read can become stale before update. Beads
  compensation must refuse because no mutation-bound condition exists.
- **Status collapse**: mapping unsupported into attribution lost or compensated
  destroys recovery meaning and can trigger unsafe automation.
- **Evidence splicing**: combining historical revision with current value creates
  provider state that never existed. Store and load tuples atomically.
- **Authority leak**: convenient tracker assignment can accidentally become a
  scheduler precondition or lease renewal. Test authority rows around failures.
- **Replay drift**: replacing an operation ID or marker after ambiguity can
  duplicate effects. Preserve immutable intent across restart.
- **SQLite conflict**: a permissive upsert may overwrite immutable prior
  evidence. Keep the full semantic equality predicate.
- **Diagnostic leakage**: provider payloads can contain human work content. Use
  typed summaries and recovery codes only.

## Reviewer Guidance

Start with the impossible invariant: search for any Beads compensation path that
issues an unguarded update after a matching read. Reject it. Run the mutation
spy and verify `safe_compensation_unsupported` occurs before writer-lock
acquisition and command start. Then race a human change through the same path.

Trace one ambiguous assignment through provider apply, lost response, control
save failure, process restart, and observation. Confirm the terminal evidence is
a tuple that actually existed at the provider and that same-operation replay
does not issue a duplicate mutation.

Inspect execution imports and state transitions. Provider assignment must remain
visible coordination only: it cannot create/extend a lease, replace the pinned
graph, transfer a workspace, relax reviewer independence, or bypass serialized
integration. Finally, verify the strong fake still compensates when its primitive
truly enforces revision/value/marker, proving the saga did not reduce all
providers to Beads' weaker capability.
