# WP02 Review Feedback — Cycle 2

## Verdict

Reject to `planned`. Commit `0f1e7d6c` repairs the five cycle-1 findings in
their original forms, the approved WP01 repair `b4a10645` is present through
merge `99fa07b8`, and all baseline qualification gates pass. Three bounded
equivalent-shape probes nevertheless cross locked journal invariants.

## Issue 1 — Operation aggregate authority can evade the host-global check

**Severity:** blocker

**Location:** `internal/controljournal/v2/commit_plan.go:184-206`

`validatePlanMembers` applies the host-scope/operation-ID check only inside
`if event.Family == "operation"`. Aggregate identity is independently caller
controlled, so an event registered as family `mission` can use
`AggregateKind("operation")`, a mission scope, and an arbitrary operation ID.
When a normal host-global `operation` family event is also present, the plan
still has `operationEvents == 1`; the disguised mission-scoped operation event
and its matching head are accepted and sealed.

Reproduction: extend the valid first-publication fixture with:

- family/event type `mission` / `mission.changed`;
- coordinate kind/ID `operation` / `operation-shadow`;
- the normal mission scope;
- a matching version-1 head; and
- `event-shadow` in the exact `OperationState.EventIDs` membership.

`SealCommitPlanV2` returns success. This recreates the host-global authority
hole through a one-field semantic alias and violates T009.4, T009.7, T010.2,
and the review requirement that operation events and heads are host-global.

Required correction: make event-family ownership and aggregate-coordinate
kind a closed validated relationship. Independently require every aggregate
whose kind is `operation` to have host scope, the current OperationID, and the
current operation logical version, regardless of the supplied family. Add a
mutant that changes only the family while retaining a mission-scoped
`operation` coordinate and matching head.

## Issue 2 — Non-digit RFC3339 years are accepted

**Severity:** high

**Location:** `internal/controljournal/v2/operation.go:115-135`

`canonicalUTCSecond` maps a non-digit to `-100` and then uses the result in
arithmetic without first requiring every numeric position to be a digit. Some
invalid years therefore collapse into positive, calendar-valid years. For
example, `202a-02-28T12:00:00Z` is interpreted arithmetically as year 1920 and
is accepted by `NewOperationBindingV2`.

This violates T007.8 and the exact canonical UTC RFC3339-second contract.

Required correction: reject any non-digit in every numeric position before
date arithmetic, or parse and exact-round-trip the supported RFC3339 form.
Add position-wise non-digit mutants in addition to the existing punctuation,
offset, fractional-second, and impossible-calendar cases.

## Issue 3 — Nested membership exceeds its budget after copy/sort and gets the wrong typed refusal

**Severity:** high

**Location:** `internal/controljournal/v2/commit_plan.go:108-131` and
`internal/controljournal/v2/model.go:445-447`

Top-level plan collections are bounded before copying, but each event's
`GovernanceRevisions` is copied and sorted before its `MaxMembers` limit is
checked. `NewEventEnvelopeV2` also copies the slice before testing the limit.
A fixture with exactly `MaxMembers+1` valid, sorted governance revisions
returns `INVALID_CANONICAL_BYTES: governance_revisions`, not the required
typed `BUDGET_EXCEEDED`, and the allocation/sort already happened.

The same preflight rule should be audited for other nested caller-owned bytes,
especially checkpoint canonical bytes, before cloning. This violates T006.8,
T009.11, T011.6, and the risk mitigation requiring validation before
allocation or sorting attacker-controlled collections.

Required correction: preflight every nested count and byte budget before
cloning, canonicalizing, or sorting; return `BUDGET_EXCEEDED` for every
maximum-plus-one case. Add exact-maximum and maximum-plus-one tests for nested
governance membership and checkpoint/event byte payloads, not only top-level
slice counts.

## Replayed cycle-1 evidence

The following repaired controls pass:

- result kind, result schema, canonical result, event membership, and effect
  membership alter both plan digest and PublicationID;
- first-publication ghost membership and the original mission-scoped
  `operation`-family event are rejected;
- later publications require a prior state and reject unproven event/effect
  additions;
- sorted insertion before, between, and after existing event/effect IDs is
  accepted, while loss, replacement, and duplicates refuse;
- ordinary operation binding keys remain host-global;
- impossible calendar days, offsets, fractional seconds, and lowercase zone
  suffixes refuse;
- caller mutation after sealing does not alter canonical sealed bytes; and
- the declared top-level collection maximum-plus-one cases refuse.

## Qualification evidence

Passing:

- repaired semantic tests at `-count=20`;
- complete `internal/controljournal/v2` suite at `-count=20`;
- race suite at `-count=5`;
- full ordinary architecture suite;
- helper architecture-check gate;
- whole-repository compile-only test;
- `go vet`, `staticcheck`, `gofmt -d`, and `git diff --check`;
- direct production imports are only `bytes`, `crypto/sha256`, `encoding/*`,
  `fmt`, `sort`, `strconv`, `strings`, and `unicode/utf8`.

Failing independent temporary probes:

1. disguised mission-family/misson-scoped `operation` aggregate: accepted;
2. `202a-02-28T12:00:00Z`: accepted; and
3. `MaxMembers+1` governance revisions: wrong non-budget error after
   copy/sort.

All temporary probe code was removed. The implementation lane is restored
apart from runtime-owned `.spec-kitty/` review state.

## Anti-pattern checklist

1. Dead code: **PASS with planned consumers** — WP03/WP04 consume the journal
   contracts.
2. Synthetic-fixture test: **PASS** — existing tests exercise production
   constructors and validators; the problem is missing equivalent-shape cases.
3. Silent empty return: **PASS** — no silent empty production return found.
4. FR coverage: **FAIL** — FR-002/FR-004 and bounded-validation obligations are
   false-green under the three reproduced inputs.
5. Frozen surface: **PASS** — the implementation repair stays within WP02's
   owned files; the separately approved WP01 dependency merge is explicit.
6. Locked decision: **FAIL** — mission-scoped operation authority and
   post-allocation budget validation contradict explicit MUST-level laws.
7. Shared-file ownership: **PASS** — WP01 changes arrived only through the
   approved dependency merge.
8. Production fragility: **N/A** — no panic/raise path was introduced by the
   repair.
