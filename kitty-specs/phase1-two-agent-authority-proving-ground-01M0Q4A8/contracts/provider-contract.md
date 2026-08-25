# Fake Tracker Provider Contract

The fake tracker exists to test an unreliable external ticket boundary. It is
deliberately too weak to run a mission.

## Supported Data

- provider work key, title, description, criteria, and dependency keys;
- assignee and comments;
- monotonically changing provider revision;
- opaque operation marker attached to a mutation;
- durable history sufficient for test assertions.

## Supported Operations

- read one work item and its provider revision;
- compare-and-set assignment;
- compare-and-add a comment/marker;
- compare-before-compensate an attributable assignment;
- inject unavailable, timeout-before-apply, timeout-after-apply, and explicit
  revision-conflict outcomes.

The provider store is durable and separate from the Spec Kitty control store.

## Forbidden Semantics

The provider has no concept of:

- execution eligibility, run leases, or fencing tokens;
- lane or workspace ownership;
- review authority or acceptance;
- integration/merge slots;
- mission state transitions;
- Git refs, commits, or hooks.

Provider assignment is informational coordination. It cannot cause a run to
become active or a candidate to become accepted.

## Compare-Before-Compensate

Compensation may undo an effect only when all of the following still match the
recorded applied effect:

1. provider work key;
2. current provider revision (or provider-supported causal successor);
3. current value;
4. operation marker.

If attribution is missing, compensation records `external_attribution_lost`
and preserves current provider state. Retrying a completed compensation is a
no-op.

## Ambiguous Outcome

After timeout or lost response, Spec Kitty reads current provider state using
the same OperationID marker. It records `confirmed`, `not_applied`, or
`decision_required`; it does not blindly repeat a non-idempotent mutation.

Contract tests run against this provider without importing any fake-specific
types into the execution capability. The same tests define the minimum future
provider boundary, but do not commit Phase 1 to Beads or another tracker.
