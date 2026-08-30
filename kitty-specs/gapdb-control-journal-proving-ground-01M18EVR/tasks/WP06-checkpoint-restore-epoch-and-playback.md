---
work_package_id: WP06
title: Checkpoint Restore Epoch and Playback
dependencies:
- WP02
- WP03
requirement_refs:
- C-010
- C-011
- FR-019
- FR-020
- FR-021
- FR-022
- FR-023
- FR-024
- FR-025
- FR-029
- NFR-005
- NFR-006
- NFR-007
- NFR-010
- NFR-012
planning_base_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
merge_target_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
branch_strategy: Planning artifacts for this mission were generated on kitty/mission-gapdb-control-journal-proving-ground-01M18EVR. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into the mission branch; only the accepted mission may merge to main.
subtasks:
- T030
- T031
- T032
- T033
- T034
- T035
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controljournal/checkpoint/
create_intent:
- internal/controljournal/checkpoint/model.go
- internal/controljournal/checkpoint/checkpoint.go
- internal/controljournal/checkpoint/checkpoint_test.go
- internal/controljournal/checkpoint/restore.go
- internal/controljournal/checkpoint/restore_test.go
- internal/controljournal/playback/playback.go
- internal/controljournal/playback/playback_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controljournal/checkpoint/model.go
- internal/controljournal/checkpoint/checkpoint.go
- internal/controljournal/checkpoint/checkpoint_test.go
- internal/controljournal/checkpoint/restore.go
- internal/controljournal/checkpoint/restore_test.go
- internal/controljournal/playback/playback.go
- internal/controljournal/playback/playback_test.go
role: implementer
tags:
- m1
- checkpoint
- restore
- playback
tracker_refs: []
---

# Work Package Prompt: WP06 - Checkpoint, Restore, Epoch, and Playback

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Implement deterministic product recovery from a versioned checkpoint plus the
committed journal suffix and a separate full-history playback verifier that
performs zero adapter calls and zero durable writes. This WP proves product
state, authority summaries, and digests after bytes return; it must never equate
GapDB byte recovery with Spec Kitty readiness.

## Recovery laws

1. A checkpoint covers only an already committed prior prefix.
2. Its watermark is explicit, stable, and independently validated.
3. Suffix events use the exact committed event order.
4. Restored projection digest must equal full side-effect-free playback digest.
5. Playback receives immutable inputs and no adapter capability.
6. Every owner restart admits a new opaque RuntimeEpochID.
7. Predecessor-epoch capsules are refused after the new binding.
8. Bounds fail closed; no hidden unbounded catch-up exists.

## Branch and ownership

Begin from WP02/WP03. Edit only checkpoint and playback files. Consume adapter
read/scan ports without modifying them. Do not manage the sidecar lifecycle,
mutate application commands, or edit module/SQLite files.

## T030: Build prior-prefix checkpoints with explicit watermarks

**Files**: Create `checkpoint/model.go`, `checkpoint.go`, and tests.

**Implementation requirements**:

1. Define versioned checkpoint envelope, aggregate states/heads, authority
   summary, stable projection digest, and committed prefix watermark.
2. Validate the watermark references a fully committed record boundary.
3. Canonically encode aggregate collections and digests.
4. Enforce four-MiB checkpoint payload and product count limits.
5. Make checkpoint due at 128 committed commands, 512 events, or four MiB of
   suffix history, whichever occurs first.
6. Build only from committed prior history; reject current-batch inclusion.
7. Publish checkpoint blob/head through the later application batch atomically.

**Tests**:

- Canonical bytes/digest are stable across map order.
- Current-batch and mid-record watermarks fail.
- Each due threshold and boundary-minus-one is covered.
- Invalid-checkpoint mutant is killed.

**Exit evidence**: `go test ./internal/controljournal/checkpoint -run Checkpoint`.

## T031: Restore checkpoint plus ordered journal suffix

**Files**: Create `restore.go` and `restore_test.go`.

**Implementation requirements**:

1. Read and validate the latest eligible checkpoint head/blob.
2. Verify schema, digest, watermark, source identity, and aggregate heads before
   applying any suffix event.
3. Scan committed records strictly after the watermark with bounded pages.
4. Decode/sort/apply events by revision, event index, EventID.
5. Verify per-aggregate head continuity throughout restoration.
6. Produce product projection, authority summary, terminal watermark, and digest.
7. Return not-ready with operator diagnostics on any gap or inconsistency.

**Tests**:

- Empty-prefix, checkpoint-only, suffix-only, and checkpoint-plus-suffix paths.
- Duplicate/missing/out-of-order events fail closed.
- Corrupt checkpoint falls back only to an explicitly eligible earlier one
  within the stale-restart bound; it never silently starts empty.

**Exit evidence**: `go test ./internal/controljournal/checkpoint -run Restore`.

## T032: Implement zero-I/O playback and digest comparison

**Files**: Create `playback/playback.go` and `playback_test.go`.

**Implementation requirements**:

1. Accept a complete immutable ordered record slice or bounded iterator wholly
   materialized before playback starts.
2. Expose no adapter, filesystem, clock, random, network, or writer interface.
3. Decode and reduce all events from the beginning.
4. Produce the same projection and stable digest format as restore.
5. Return explicit adapter-call and durable-write counters fixed at zero in
   trial observation, backed by construction rather than convention.
6. Compare restored and playback digests and fail on any mismatch.

**Tests**:

- Random valid histories yield restore/playback digest equality.
- A reducer/order mutation causes mismatch.
- Interface/import guards kill playback-adapter-or-write mutant.

**Exit evidence**: `go test ./internal/controljournal/playback`.

## T033: Enforce restore bounds and stale-restart caps

**Files**: Extend restore/checkpoint tests.

**Implementation requirements**:

1. Cap restore at 16,000 events, 48 MiB decoded input, and 32 scan pages.
2. Permit at most two bounded stale-checkpoint restarts.
3. Check every bound before allocating or applying the excess item.
4. Return stable field-specific not-ready diagnostics and recovery actions.
5. Never truncate and report a partial projection as ready.
6. Keep scan record/byte counts separate from event and retry counts.

**Tests**:

- Exact limits pass; plus-one fails without partial readiness.
- Oversized decoded content cannot bypass wire/raw checks.
- Third stale restart fails closed.

**Exit evidence**: `go test ./internal/controljournal/checkpoint -run Bound`.

## T034: Refuse predecessor-epoch capsules after restart

**Files**: Extend restore model/tests using an epoch-binding input seam.

**Implementation requirements**:

1. Restore durable product state before declaring the new owner ready.
2. Require a newly admitted RuntimeEpochID from the startup coordinator; this
   package validates/binds but does not create ambient global authority.
3. Compare capsule epoch to the current admitted binding on authority use.
4. Refuse the predecessor capsule even if its durable revision remains current.
5. Keep restart inside the same umbrella RunID for the crown trial.
6. Do not rotate epoch for lease expiry alone.

**Tests**:

- New epoch differs, old capsule refuses, new capsule can proceed.
- Durable revision continuity survives epoch change.
- Lease expiration does not change epoch.

**Exit evidence**: `go test ./internal/controljournal/checkpoint -run Epoch`.

## T035: Qualify checkpoint, ordering, and playback mutants

**Validation and mutants**:

1. Kill current-batch-checkpoint, invalid-checkpoint, invalid-event-order,
   revision-fill-in, playback-adapter-or-write, and watch-as-journal mutants.
2. Fuzz checkpoint decode and restore ordering with bounded deterministic seeds.
3. Run tests repeatedly, race detector, vet, and `git diff --check`.
4. Measure representative restore against ratified mission budgets without
   weakening correctness to hit a number.
5. Confirm playback imports expose no side-effecting dependency.

## Review guidance

The reviewer should inspect the function signatures, not merely counters, to
prove playback cannot call storage. Reject silent empty-state recovery, partial
ready projections, checkpoint coverage of the publishing batch, or epoch reuse
after owner restart.

## Completion criteria

- Checkpoint and suffix have explicit validated boundaries.
- Restore is bounded and product-aware.
- Full playback is structurally side-effect free.
- Equality and zero-I/O observations are ready for the ordered trial.
