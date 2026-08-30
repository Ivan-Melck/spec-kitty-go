---
work_package_id: WP08
title: Effects Independent Review and Serialized Integration
dependencies:
- WP05
- WP07
requirement_refs:
- C-014
- FR-026
- FR-027
- FR-028
- FR-035
- FR-036
- FR-037
- FR-038
- NFR-004
- NFR-015
planning_base_branch: main
merge_target_branch: main
branch_strategy: Planning artifacts for this mission were generated on main. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into main unless the human explicitly redirects the landing branch.
subtasks:
- T042
- T043
- T044
- T045
- T046
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/provingground/effects.go
create_intent:
- internal/provingground/effects.go
- internal/provingground/effects_test.go
- internal/provingground/review.go
- internal/provingground/review_test.go
- internal/provingground/integration.go
- internal/provingground/integration_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/provingground/effects.go
- internal/provingground/effects_test.go
- internal/provingground/review.go
- internal/provingground/review_test.go
- internal/provingground/integration.go
- internal/provingground/integration_test.go
role: implementer
tags:
- m1
- effects
- review
- integration
tracker_refs: []
---

# Work Package Prompt: WP08 - Effects, Independent Review, and Serialized Integration

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Complete the production-shaped workflow above the journal: record external
effect intent before observation, reconcile dishonest or ambiguous provider
state without unsafe compensation, create immutable candidates, require
ActorID-independent reviews, and serialize Git integration with fact-based
recovery. Tickets remain work facts; Spec Kitty remains authority; Git remains
what happened.

## Product laws

1. Effect intent is durable before provider mutation/observation.
2. Compensation may undo only state still demonstrably attributable to that
   operation by expected revision, value, and marker.
3. No exactly-once provider claim is made.
4. Candidate identity/evidence is immutable after handoff.
5. Reviewer ActorID differs from every candidate contributor ActorID unless a
   mission policy explicitly permits otherwise; M1 provides no such waiver.
6. Integration authority is serialized and validated before Git mutation.
7. Git facts reconcile ambiguous integration; database intent alone is not fact.
8. No force-ready or force-approve path exists.

## Branch and ownership

Begin from WP05/WP07. Edit only the six proving-ground files listed. Use current
tracker, handoff, review, integration, and Git APIs without editing them. Do not
modify WP07's authority/workspace files, SQLite, or provider adapters.

## T042: Journal effect intent before provider observation

**Files**: Create `effects.go` and `effects_test.go`.

**Implementation requirements**:

1. Persist effect intent with OperationID, provider binding/revision, expected
   value, desired value, marker, and bounded diagnostic before provider call.
2. Inject GJ-05 exactly after intent publication and before observation.
3. On restart, enumerate unresolved intents from the journal and observe the
   provider before deciding applied/absent/ambiguous/attribution-lost.
4. Keep provider assignment/comment state out of execution authority.
5. Preserve human edits as external facts, not errors to overwrite.
6. Record all resolution decisions in a later atomic product batch.

**Tests**:

- GJ-05 leaves one discoverable unresolved intent.
- Recovery observes rather than blindly repeats.
- Slow, unavailable, stale, and wrong providers yield honest outcomes.

**Exit evidence**: `go test ./internal/provingground -run Effect`.

## T043: Compare before compensating ambiguous provider effects

**Files**: Extend effects files and tests.

**Implementation requirements**:

1. Require current provider revision, exact current value, and operation marker
   all match the intent's attributable postcondition before compensation.
2. Treat missing guard capability as safe-compensation-unsupported.
3. Treat human reassignment, revision change, or marker loss as attribution loss.
4. Never implement read-then-unconditional-write as a substitute for atomicity.
5. Bound reconciliation attempts and preserve operator-visible recovery state.
6. Keep operation outcome distinct from provider-side effect outcome.

**Tests**:

- Human reassignment between crash/recovery is preserved.
- Stale provider revision refuses compensation.
- Dishonest provider responses cannot fabricate attribution.
- Valid attributable state compensates once through guarded mutation.

**Exit evidence**: `go test ./internal/provingground -run Compensat`.

## T044: Enforce ActorID-independent candidate review

**Files**: Create `review.go` and `review_test.go`.

**Implementation requirements**:

1. Build immutable candidate records from W2 and W3 only after current capsule
   validation; W1 zombie output cannot become a candidate.
2. Record the complete contributor ActorID set for each candidate.
3. Require reviewer ActorID absent from that set, regardless of ProcessID.
4. Use separate reviewer ActorIDs from both A and B in the ordered trial.
5. Bind verdict to candidate digest, work revision, criteria, evidence, and epoch.
6. Refuse self-review, changed candidate, stale capsule, and missing evidence.
7. Provide no waiver or force-ready mechanism in M1.

**Tests**:

- Actor A/Process D cannot review Actor A's candidate.
- Separate reviewer ActorID succeeds with valid evidence.
- same-contributor-review mutant is killed.

**Exit evidence**: `go test ./internal/provingground -run Review`.

## T045: Serialize integration and recover from Git facts

**Files**: Create `integration.go` and `integration_test.go`.

**Implementation requirements**:

1. Acquire one repository integration authority for the exact candidate set,
   target branch, expected target HEAD, WorkRevision, and RuntimeEpochID.
2. Validate current reviews and capsules before repository-fence acquisition and
   again before publication where appropriate.
3. Integrate approved W2/W3 candidates in deterministic serialized order.
4. Record intent before Git mutation and observation after Git fact.
5. On response/crash ambiguity, inspect target HEAD, ancestry, tree/candidate
   digests, and integration metadata before completing or retrying.
6. Refuse manual branch movement and competing integrator safely.
7. Produce exactly two successful candidate publications in the crown trial and
   zero false completions.

**Tests**:

- Competing integrators never publish concurrently.
- Refusal occurs before repository fence for invalid candidate/review.
- Crash after Git commit reconciles from Git fact without duplicate publication.
- Manual branch movement fails closed.

**Exit evidence**: `go test ./internal/provingground -run Integration`.

## T046: Qualify GJ-05, reassignment, review, and integration races

**Validation**:

1. Provide focused campaign hooks for attribution/reassignment and competing
   integration, each later run at least 100 repetitions.
2. Kill same-contributor-review, unsafe-compensation, misattributed-fields, and
   false-integration-completion mutants.
3. Inject provider slow/unavailable/wrong/stale behavior deterministically.
4. Run package tests repeatedly and under the race detector.
5. Run vet, WP01 guards, and `git diff --check`.
6. Assert universal invariants after every terminal path.

## Review guidance

Use ActorID, not ProcessID, when attempting self-review. Change provider state
between intent and compensation. Move the integration target manually. Reject
any code that clears a human reassignment, approves same-actor review, or marks
integration complete from control state without verifying Git.

## Completion criteria

- Provider ambiguity is attribution safe.
- Candidate review is genuinely actor independent.
- Integration is serialized and Git-reconciled.
- GJ-05 and race hooks are ready for the combined trial.
