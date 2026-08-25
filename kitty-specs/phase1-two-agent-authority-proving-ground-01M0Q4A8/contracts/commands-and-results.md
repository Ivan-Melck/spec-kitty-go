# Command and Result Contract

Phase 1 exposes Go command handlers used by tests and by the helper process.
The helper is a transport adapter, not a second implementation.

## Command Envelope

Every mutating command carries:

- schema version;
- correlation ID and OperationID/idempotency key;
- ActorID and ProcessID;
- injected timestamp;
- aggregate IDs and expected aggregate revisions;
- lease token when the command acts under run authority.

## Required Commands

| Command | Success result | Principal refusal modes |
|---|---|---|
| `FinalizeWorkRevision` | immutable revision and graph digest | invalid graph, cycle, revision conflict |
| `ReconcileRevision` | immutable disposition record | invalid transition, integrated-history mutation |
| `StartRun` | run capsule or durable in-progress operation | not ready, already leased, tracker unavailable, recovery required |
| `HeartbeatRun` | new deadline at same generation | stale token, expired lease, revision conflict |
| `ExpireRun` | expired lease and quarantined workspace | generation changed, still live |
| `SubmitHandoff` | immutable candidate boundary | stale authority, candidate mismatch, ineligible revision |
| `SubmitReview` | atomic verdict and criteria | reviewer not independent, slot conflict, candidate mismatch |
| `QueueIntegration` | queued/active attempt | not approved, candidate mismatch, target authority busy |
| `ResumeIntegration` | terminal result or typed repair state | target drift, conflict, ambiguous Git fact |
| `CompensateEffect` | compensated/no-op/conflict | provider unavailable, attribution lost |
| `Inspect` | state and diagnostics | invalid repository binding |

## Run Capsule

A successful `StartRun` returns exactly the authority an agent needs:

- WorkID and WorkRevisionID;
- RunID, LeaseID, lease generation, and opaque token;
- LaneID;
- WorkspaceID, canonical path, owned branch, and verified base SHA;
- lease deadline and heartbeat policy.

It does not return tracker ownership as authority.

## Typed Result Shape

All success and failure results share:

```text
schema_version
kind
correlation_id
operation_id
aggregate_ids
observed_state
expected_state
retry_safety: safe_same_operation | safe_new_operation | decision_required | never
recovery_actions[]
```

Stable failure kinds include `authority_conflict`, `stale_authority`,
`control_store_busy`, `workspace_not_ready`, `tracker_unavailable`,
`external_attribution_lost`, `reviewer_not_independent`, `review_conflict`,
`target_changed`, `integration_conflict`, and `decision_required`.

Errors are machine-matchable by kind; human text is explanatory only.

## Idempotency Law

Replaying the same OperationID and semantically identical request returns the
recorded result or continues the same saga. Reusing an OperationID for a
different request is a conflict. A timeout never authorizes inventing a new
operation before inspecting the old one.

## Authority Law

Every command that can publish or advance work rechecks the current lease
generation/token in the same control-store transaction as its state mutation.
Git and tracker adapters never validate execution authority themselves.
