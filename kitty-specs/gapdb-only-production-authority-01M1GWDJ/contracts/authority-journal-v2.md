# Authority Journal v2 Contract

Status: normative M2 planning contract.

## Purpose

Authority Journal v2 generalizes the accepted M1 event/CAS model to every
production authority capability while repairing three boundaries that M1 did
not claim to solve:

1. OperationID uniqueness is host-global, not mission-local.
2. staged operations have immutable binding plus revisioned progress.
3. checkpoint suffixes are directly addressable without a provider
   revision-range scan or compactable watch history.

## Key law

```text
operation binding:
  spk/v2/operations/<operation-id>/binding

operation head:
  spk/v2/operations/<operation-id>/head

aggregate head:
  spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>

immutable event:
  spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>
```

Every key component uses the canonical bounded codec. Logical version is a
positive 16-character lowercase hexadecimal integer. An event key is inserted
with absence required and is never updated or deleted in M2. Aggregate deletion
is a tombstone transition with a retained head.

## Operation resolution order

1. authenticate and syntactically validate the request;
2. canonicalize stable intent and read the global OperationBinding;
3. on exact binding, return or resume OperationState even if the runtime epoch
   has since changed;
4. on binding mismatch, return `IDEMPOTENCY_CONFLICT` before capability reads;
5. on absence, evaluate current epoch and capability preconditions;
6. publish the first meaningful transition and binding atomically.

The stable intent digest excludes RuntimeEpochID. The binding preserves the
epoch that evaluated the command as provenance. A request proven absent under a
predecessor epoch is not executed in the replacement epoch without explicit
re-evaluation.

## Publication law

One sealed CommitPlan becomes exactly one GapDB AtomicBatch and one positive
GapDB revision. It contains:

- absent OperationBinding on the first publication;
- exactly one OperationState event/head transition;
- zero or more capability event/head transitions;
- zero or more effect intents and eligibility-guard transitions; and
- due checkpoint records/slot update when explicitly planned.

At most one event addresses an aggregate coordinate in a publication. Its
logical version is exactly the prior head version plus one. Every event has the
same OperationID, PublicationID, event count, provider revision, and distinct
consecutive event index. Publication success is not exposed before durable
acknowledgement or same-identity reconciliation.

## Ambiguity law

PublicationID is deterministically derived from OperationID, next operation
logical version, and sealed CommitPlan digest. On EOF, timeout, cancellation,
or malformed acknowledgement, reconciliation reads the global binding,
OperationState, events, and heads:

- complete matching membership returns the exact recorded result/progress;
- proven absence permits retry of the identical sealed plan and preconditions;
- partial or contradictory membership is corruption and closes authority.

No ambiguity path allocates a new OperationID, calls an external adapter, or
publishes a projection optimistically.

## Restore law

Watch is notification only. Restore captures stable provider revision `R`,
selects a compatible complete checkpoint, scans all current aggregate heads at
`R`, and exact-reads every new logical version for each advanced/new aggregate.
It validates operation membership and sorts events by
`(Record.Revision, EventIndex, EventID)` before pure reduction. It rechecks `R`
before sealing the restored projection.

A stale cursor restarts the entire bounded attempt. Missing versions, physical
head deletion, mismatched terminal heads, partial operation membership,
unknown version tuples, or exceeded budgets fail readiness. Only a sealed
restore result may admit the replacement runtime epoch.

## Capability ownership law

The common journal owns publication mechanics and canonical validation only.
Mission, artifact, workgraph, execution, workspace, handoff, review,
acceptance, integration, effect, and outbox packages own their commands,
events, states, decisions, reducers, and narrow repository ports. Neither the
common journal nor the GapDB adapter interprets capability policy.

## Refusal law

Semantic refusal order is capability-defined and behavior-inventory traced.
All mutable facts used by an accepted command are represented by provider
conditions in the same batch. On CAS loss, resolution first checks whether the
same operation committed, then identifies the moved authority/eligibility
coordinate. It never retries a newly decided batch automatically.

## Structural law

No production package, dependency, binary, installation, configuration,
fallback, importer, build tag, generated edge, reflection path, or subprocess
may select or reach SQLite. Historical evidence is permitted only by exact
path-scoped architecture rules and is never executable production input.
