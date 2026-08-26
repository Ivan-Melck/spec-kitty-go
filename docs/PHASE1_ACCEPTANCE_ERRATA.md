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

Other architecture-era references to a possible visible waiver in
`TECHNICAL_PROJECT_PLAN.md`, `CONCURRENCY_SCENARIOS.md`, and
`MINI_REVIEW_RESOLUTION.md` likewise describe a future policy design. They do
not describe an implemented Phase 1 behavior. The behavioral inventory and
README are the living product-status surfaces and point here so that the
historical design discussion remains readable without becoming false runtime
documentation.

## Operational consequence

- Operators cannot override actor-level review independence in Phase 1.
- Agents and integrations must treat a same-actor refusal as final for that
  reviewer and use a distinct reviewing actor.
- Documentation that discusses visible waivers describes a future policy
  design only; it does not authorize a current exception.
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
