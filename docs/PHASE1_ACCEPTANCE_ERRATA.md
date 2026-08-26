# Phase 1 acceptance erratum: no waiver exists

Status: living correction

Applies to: Phase 1 authority proving-ground acceptance language

Correction date: 2026-08-25

## Correction

The shipped Phase 1 review gate has no waiver mechanism, bypass flag, or
fallback that permits an implementation actor to approve its own candidate.
Review independence is unconditional in this release: the reviewer `ActorID`
must differ from every implementation contributor `ActorID`. A different
`ProcessID` for the same actor is not independent.

This strict refusal is the implemented and accepted behavior.

## Historical clause

The completed mission artifact
[`phase1-two-agent-authority-proving-ground-01M0Q4A8/spec.md`](../kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/spec.md)
states in User Story 4, acceptance scenario 3, that a same-actor review is
rejected “absent an explicit visible waiver.” The artifact is an immutable
record of the mission as accepted, so this closeout does not rewrite it.

The final phrase was aspirational and is superseded for current-product claims
by this erratum. It must not be interpreted as evidence that a waiver exists.

The living architecture references in `TECHNICAL_PROJECT_PLAN.md`,
`CONCURRENCY_SCENARIOS.md`, `MINI_REVIEW_RESOLUTION.md`, the behavioral
inventory, and README now state the unconditional Phase 1 rule. This erratum
keeps the immutable mission wording readable without allowing it to become a
false runtime promise.

## Operational consequence

- Operators cannot override actor-level review independence in Phase 1.
- Agents and integrations must treat a same-actor refusal as final for that
  reviewer and use a distinct reviewing actor.
- A future exception can be discussed only as a separately specified and
  ratified feature; such discussion does not authorize a current exception.
- No general `force`, hidden configuration, or undocumented recovery path may
  be used to evade this gate.

A future waiver capability would be a new governed feature. It would require an
explicit policy owner, typed scope, attributable evidence, audit semantics,
tests proving that independent-only gates remain fail-closed, and new human
acceptance. None of those conditions is satisfied merely by the historical
wording.

## Evidence

This correction is required by the closeout specification's FR-009 and C-008.
The living behavioral inventory now states the implemented rule directly, and
the README links this erratum at the public compatibility boundary.
