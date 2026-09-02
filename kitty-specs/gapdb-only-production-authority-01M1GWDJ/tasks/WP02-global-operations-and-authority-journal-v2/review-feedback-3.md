# WP02 Review Feedback — Cycle 3

## Verdict

Reject to `planned`. Commit `bbe3dd64` closes all three cycle-two findings in
their literal and adjacent event-envelope forms, and every required baseline
gate is green. Independent equivalent-shape probes found five remaining
contract violations in condition membership, standalone operation heads,
publication identity validation, reducer determinism, and total-plan budget
preflight.

## Issue 1 — Provider-condition membership is not exact or attributable

**Severity:** blocker

**Location:** `internal/controljournal/v2/commit_plan.go:187-238` and
`internal/controljournal/v2/commit_plan.go:342-366`

`validatePlanMembers` proves exact event/head and operation event/effect
membership, but `ProviderCondition` is only checked for a unique caller-chosen
string, a valid kind, and a revision shape. Nothing binds a condition to the
head, binding, guard, checkpoint, or other record it protects. Removing
`mission-head` from the otherwise valid first-publication fixture still seals
successfully; replacing it with an unrelated valid condition also succeeds.

This violates T009.6, the Refusal law requiring every mutable fact used by a
command to be represented in the same batch, and the mandatory T011 mutant
"plan omits one ... condition." The current type does not carry enough
provider-independent target information to prove exact membership.

Required correction: give every condition a canonical typed target/purpose and
validate exact condition membership against all planned conditional records.
Keep provider keys out of the kernel. Add omission, substitution, duplicate
target, extra target, absent/create, and observed-revision/update mutants.

## Issue 2 — Standalone operation heads bypass host-global authority

**Severity:** blocker

**Location:** `internal/controljournal/v2/model.go:632-645` and
`internal/controljournal/v2/model.go:674-690`

The repaired sealed-plan path validates the closed family↔aggregate-kind
relationship and requires the operation event/head pair to use the host scope,
current OperationID, and current operation logical version. The independently
public head constructor/decoder does not apply that law. This production probe
is accepted:

```text
kind=operation
scope=mission(project, mission)
id=operation-shadow
logical_version=7
```

Consequently restore can decode an operation head that could never be emitted
by a valid sealed plan. That is the same authority alias cycle two fixed for
events, now crossing through the persisted-head surface.

Required correction: make operation-head validation contextual and
unconditional. A decoded operation head must be proven to come from the global
operation coordinate and agree with the expected OperationID/version; generic
aggregate heads must not admit `kind=operation` without that proof. Add direct
constructor, decoder, and sealed-plan mutants for wrong scope, ID, and version.

## Issue 3 — Persisted records accept non-derived PublicationIDs

**Severity:** high

**Location:** `internal/controljournal/v2/operation.go:302-306`,
`internal/controljournal/v2/model.go:459-462`, and
`internal/controljournal/v2/model.go:636-638`

`PublicationID` is a SHA-256-derived identity and the package already exposes
`DecodePublicationID`, but OperationState, EventEnvelope, and AggregateHead
validate it only with the generic identity grammar. Each constructor/decoder
accepts `not-a-derived-publication-id`. A corrupted prior state, event, or head
therefore survives strict decoding even though no valid sealed plan can derive
that identity.

This violates deterministic publication identity and fail-closed restore.

Required correction: validate every non-empty PublicationID at every ingress
with one exact 64-character lowercase-hex parser (including operation-state
`last_publication_id`, event `publication_id`, and head
`last_publication_id`). Add short, overlong, uppercase, non-hex, empty-where-
required, and valid derived-ID round trips.

## Issue 4 — Pairwise reducer checking does not guarantee repeated determinism

**Severity:** high

**Location:** `internal/controljournal/v2/model.go:726-759`

`Reduce` invokes the registered reducer twice and compares those two outputs.
A stateful reducer that returns one value for calls 1–2 and another value for
calls 3–4 passes both checks. Two successive `Reduce` calls with byte-identical
prior state and event then return different successful results and digests.

This directly violates T010.7: the same prior state and event must produce
byte-identical output across repeated runs. The existing alternating-output
mutant kills only one nondeterminism shape.

Required correction: make the enforceable boundary explicit. Either retain a
synchronized canonical input→output observation and reject any later mismatch,
or move determinism proof to a qualification-owned reducer harness that runs
each registered implementation repeatedly across fresh instances/processes.
In either design, add the pair-stable/stateful mutant and concurrent execution
under `-race`; do not present two adjacent calls as a general purity proof.

## Issue 5 — Total serialized plan size is still checked after allocation

**Severity:** high

**Location:** `internal/controljournal/v2/commit_plan.go:241-279` and
`internal/controljournal/v2/commit_plan.go:395-416`

The new preflight counts raw payload bytes only. It omits the encoded size of
the binding, current/prior operation state, heads, effects, guards, conditions,
identifiers, JSON framing, and base64 expansion of `[]byte` fields. A valid
six-event probe with six maximum-size payloads and observations passes
`preflightCommitPlan`, is then cloned, canonicalized, and sorted, and only
returns `BUDGET_EXCEEDED` from `planPreimage` after JSON serialization exceeds
`MaxCommitPlanBytes`.

This violates T006.8, T009.11, and T011.6's requirement to refuse before
copying, sorting, canonicalization, or excessive allocation.

Required correction: preflight a conservative encoded-size bound for every
plan component before cloning/sorting, or impose an ingress byte budget whose
proof guarantees both preimage and final encoding fit. Add an execution-bound
mutant proving a whole-plan maximum passes and maximum-plus-one refuses from
the preflight seam before canonicalization.

## Replayed cycle-one and cycle-two evidence

The repair now passes all previously failing forms:

- every OperationState semantic field changes plan digest and PublicationID;
- first/later event and effect membership is exact and cumulative;
- sorted set extension works before, between, and after existing IDs;
- impossible dates and non-digits in every numeric timestamp position refuse;
- event family and aggregate kind are closed in plan and direct-envelope paths;
- operation-kind events require host scope/current OperationID/version;
- nested governance, event, observation, and checkpoint maximum-plus-one
  inputs return typed `BUDGET_EXCEEDED`; and
- sealed-plan caller mutation does not alter canonical evidence.

All temporary executable probes were removed. The lane is clean apart from
runtime-owned `.spec-kitty/` state.

## Qualification evidence

Passing:

- focused T006–T011 semantic union at `-count=20`;
- complete `internal/controljournal/v2` suite at `-count=20`;
- race suite at `-count=5`;
- ordinary architecture suite;
- helper `architecture-check` gate;
- whole-repository compile-only test;
- `go vet`, `staticcheck`, `gofmt -d`, and `git diff --check`.

Failing independent probes:

1. omit one valid provider condition: plan seals;
2. construct/decode a mission-scoped operation head: accepted;
3. use an arbitrary non-digest PublicationID in state/event/head: accepted;
4. pair-stable stateful reducer: repeated identical reductions disagree; and
5. serialized plan over budget: production preflight returns nil and refusal
   occurs only after clone/canonicalize/sort.

## Anti-pattern checklist

1. Dead code: **PASS with planned consumers** — WP03/WP04 are the declared
   journal and restore consumers.
2. Synthetic-fixture test: **PASS** — existing tests exercise production paths;
   the independent probes demonstrate missing equivalent shapes.
3. Silent empty return: **PASS** — no silent empty production return found.
4. FR coverage: **FAIL** — FR-004/FR-005/FR-006 and NFR-012 remain false-green
   under the five probes above.
5. Frozen surface: **PASS** — the repair modifies only WP02-owned files; the
   approved architecture dependency arrived through its coordination merge.
6. Locked decision: **FAIL** — condition omission, operation-head aliasing,
   malformed publication identity, nondeterministic replay, and late budget
   refusal contradict MUST-level journal laws.
7. Shared-file ownership: **PASS** — no undeclared production file was changed
   by the WP02 repair.
8. Production fragility: **N/A** — no panic/raise path was added by the repair.
