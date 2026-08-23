# Mini Architecture Review Resolution

Review date: 2026-08-23

Plan reviewed: `docs/TECHNICAL_PROJECT_PLAN.md`

## Outcome

The review endorsed the central authority separation:

```text
Beads:       what work exists
Spec Kitty:  whether it may run and how it is accepted
Git:         what actually happened
```

It found one blocker, four additional pre-implementation corrections, and one
delivery-order concern. All are accepted in the revised plan.

## Resolved findings

### Blocker: a database token cannot fence POSIX writes

Accepted.

The original wording incorrectly implied that a fencing token could stop a
living shell, IDE, or agent from continuing to write a worktree after lease
expiry.

Resolution:

- Fencing tokens protect Spec Kitty control-plane mutations only.
- A workspace belongs permanently to the lease generation that created it.
- Expired or revoked workspaces are quarantined, not transferred.
- A replacement lease receives a distinct branch and worktree.
- Old-token handoff, review, acceptance, and integration are rejected.
- Output can be harvested only through explicit reconciliation after the writer
  is proven quiescent.

### High: characterize Python behavior before ratifying architecture

Accepted.

Phase 0 now builds and applies the behavior inventory before foundational ADRs
are frozen. Product laws are provisional until obscure legacy behavior has been
used to attack their boundaries.

### High: define WorkRevision generations and reconciliation

Accepted.

Every finalized executable graph is a numbered generation. Reconciliation
creates a new generation and explicitly classifies affected work as:

- `continue`;
- `supersede`;
- `invalidate`;
- `fork`.

Schedulers, reviews, acceptance, and integration reject work whose disposition
does not authorize the action.

### High: reviewer independence must be actor-level

Accepted.

A different process belonging to the same actor is not independent review.
Default policy requires the review ActorID to differ from implementation
ActorIDs. Explicit same-actor waivers are visible and cannot satisfy a gate that
requires independent review.

### Medium-high: compensation must compare before undoing

Accepted.

External compensation is now governed by an attribution-safe compensation law.
Recovery records and checks the provider revision, expected value, actor, and/or
operation marker. If later human or system activity changed the external state,
the old operation does not overwrite it.

### Medium: declare the local concurrency boundary

Accepted.

The first release has one machine/filesystem authority per SQLite control store.
Remote agents use the Orchestrator API and host-owned execution tools. NFS, SMB,
multi-host shared volumes, and independently mounted database copies are not
supported control-store configurations.

### Delivery concern: prove concurrency before completing the architecture

Accepted.

The old Phase 1 minimal mission skeleton was replaced by a two-agent authority
proving ground using:

- two work items;
- two actors;
- a deliberately stupid fake tracker;
- one real SQLite control database;
- one real temporary Git repository;
- real branches and worktrees;
- independent review;
- serialized integration;
- crash and stale-writer injection at every durable boundary.

Beads, complete mission methodology, MCP, Team Kitty, migration, projections,
and multiple mission types do not precede this experiment.

## Ratification effect

This review does not make the foundational ADRs final. The revised Phase 0
behavior inventory and the Phase 1 proving experiment remain explicit
ratification gates. Failure in either reopens the affected authority,
persistence, or workspace decision before broader implementation proceeds.

Follow-through completed on 2026-08-23:

- Python behavior was characterized at
  `d060cff9a5c9f8cf369c8786e5bf9b4f89931d0a` in
  `docs/PYTHON_BEHAVIOR_INVENTORY.md`.
- The characterization produced seven concrete plan amendments, including the
  separation of durable lane lineage from per-lease workspace lifetime.
- The two-agent proving experiment is specified as executable fault scenarios
  in `docs/CONCURRENCY_SCENARIOS.md`.
- The empirical Phase 1 result remains pending and can still reopen the
  concurrency mechanism ADRs.
