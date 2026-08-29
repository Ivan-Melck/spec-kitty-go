# Control Journal and Recovery Contract

## Status

Proposed normative contract for the production runtime. The current delivered runtime does not implement this contract.

## Purpose

The control journal is Spec Kitty's durable explanation of accepted authority changes. GapDB supplies ordered conditional storage, durability acknowledgement, recovery, snapshots, and watches. This contract supplies the product-level event semantics needed to resume and play back a mission safely.

GapDB recovery answers “which committed bytes survived?” The control journal answers “what did Spec Kitty decide, under which authority and policy, and what may safely happen next?” Neither replaces the other.

## Commit rule

An authority-changing command succeeds only after one atomic durable GapDB mutation has:

1. verified the expected aggregate revision, runtime epoch, lease, and command preconditions;
2. appended the complete versioned event set for the transition;
3. updated the idempotency result for the operation;
4. updated any checkpoint scheduling metadata or outbox intent required by the transition; and
5. received GapDB's durable acknowledgement.

Memory acknowledgement is permitted only for explicitly loss-tolerant telemetry that cannot grant authority, satisfy acceptance, or conceal a required decision.

## Key layout

The concrete encoding is implementation-defined, but it must preserve these logical spaces:

```text
/meta/database-identity
/meta/schema-version
/runtime/current-epoch
/operations/{operation-id}
/events/{storage-revision}/{event-id}
/aggregates/{aggregate-type}/{aggregate-id}
/checkpoints/{mission-id}/{through-revision}
/outbox/{effect-id}
/projections/progress/{mission-id}
```

Keys and values are bounded. Logs, transcripts, patches, test output, and other unbounded bodies live in an artifact store and are referenced by digest.

## Command transaction

For a typed command `C`:

1. Canonicalize the request and compute its digest.
2. Read `OperationID`.
   - Same operation and digest: return the recorded result.
   - Same operation and different digest: reject with `IDEMPOTENCY_CONFLICT`.
3. Verify the active runtime epoch and all authority preconditions.
4. Reduce the command against the current in-memory projection without side effects.
5. Atomically and durably write events, operation result, aggregate heads, and outbox intents with compare-and-swap guards.
6. Publish the committed events to the in-memory projection and revision watchers.
7. Dispatch external-effect intents asynchronously.

No external effect may occur before its intent is durable. An external effect is never replayed merely because its intent is encountered during restore or playback.

## External effects and compensation

External work uses intent/attempt/observation events. Every retry uses the same stable effect identity and provider-specific idempotency or attribution marker where available.

Compensation may undo external state only when a compare-before-compensate observation proves that the current state is still attributable to the operation being compensated. The comparison must use the strongest available combination of provider revision, expected value, assignment identity, and operation marker. Ambiguity becomes a reconciliation decision; it never authorizes a destructive guess.

## Checkpoints

A checkpoint is a versioned, checksummed projection through an inclusive journal revision. It contains no new authority and is valid only when:

- its database and mission identities match;
- its schema and reducer versions are supported;
- its source-prefix digest verifies; and
- its recorded revision exists in recovered GapDB history.

Invalid checkpoints are rejected. Restore may fall back to an older valid checkpoint or full journal reduction, subject to bounded startup policy.

## Restart protocol

```text
GapDB restore
  -> validate database identity and schema
  -> load latest valid mission checkpoints
  -> reduce later journal events without side effects
  -> durably create a new runtime epoch
  -> mark predecessor leases non-current
  -> quarantine predecessor workspaces
  -> reconcile Git, Beads, artifacts, and pending effects
  -> publish recovered progress
  -> admit new commands
```

The runtime does not become ready if journal validation, reducer compatibility, or epoch creation fails. Replacement leases always receive fresh workspaces; an expired lease's mutable workspace is never transferred.

## Playback

Playback is a pure reducer over a selected journal prefix and recorded external observations. It:

- accepts explicit reducer and schema versions;
- performs no network, Git, filesystem, tracker, qualification, or compensation effects;
- produces a deterministic mission projection, decision explanation, and evidence index;
- reports unknown or inconsistent events rather than skipping them; and
- can export a portable terminal record whose digests bind the event prefix, reducer, policy, WorkRevision, candidates, reviews, acceptance, and integration result.

## Reconciliation outcomes

Tracker or external drift affecting a pinned executable generation yields one explicit outcome:

- `continue`: the current generation and active runs remain admissible;
- `supersede`: a new generation replaces the old for future work while already-created evidence remains historical;
- `invalidate`: affected runs or candidates cannot proceed to acceptance;
- `fork`: both generations remain addressable under an explicit policy decision;
- `retry`: an attributable incomplete effect may be attempted again;
- `compensate`: an attributable current external effect may be undone;
- `decision_required`: automated resolution is unsafe or unauthorized.

## Required fault proofs

An implementation is not qualified until executable tests cover:

- crash before and after durable command acknowledgement;
- duplicate and conflicting operation replay;
- torn or corrupt checkpoint with valid journal fallback;
- restart with a zombie predecessor process;
- stale workspace writes after lease expiry;
- tracker outage and stale or human-mutated tracker revisions;
- external-effect crash at every intent/attempt/observation boundary;
- unsafe compensation refusal;
- manual Git ref movement and missing candidate objects; and
- playback proving zero adapter calls.

