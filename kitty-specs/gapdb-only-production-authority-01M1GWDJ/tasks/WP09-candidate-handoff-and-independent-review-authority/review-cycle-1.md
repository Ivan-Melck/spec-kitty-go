---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T21:25:47Z'
reviewer_agent: mill
wp_id: WP09
---

# WP09 review feedback — correction cycle 1

Verdict: **REQUEST CHANGES**

Independent reviewer Mill reviewed candidate
`c4dc9ec817f2f0acab31068b6be542b27b301c58` (tree
`1fa78c812cc6e68b011a611630230f89c6799a53`). The declared eight-file scope,
diff hygiene, ActorID contributor checks, typed failures, basic lost-ack
recovery, and focused/race/static gates are accepted as sound. The candidate
must not be integrated until the following defects are corrected.

## 1. Add explicit immutable candidate replacement

T044.9 requires candidate replacement to be an explicit, versioned transition
with predecessor lineage and a bounded reason. The current handoff state and
reducer terminate at `candidate_recorded` or `candidate_refused`; no
replacement command, event, state, predecessor identity, or reason exists.

Add a CAS-protected replacement path that preserves the prior candidate and
its evidence byte-for-byte. Bind the successor to the predecessor candidate,
handoff/version, reason, actor/process, governance, and OperationID. Exact
replay must be stable, while changed lineage or reason must conflict.

## 2. Anchor same-actor exceptions to current mission policy authority

The current exception is stored as synthetic review-owned state at
`review_slot/policy:<ExceptionID>`. It is not a current mission policy fact,
has no legal production writer through the review reducer, and its positive
test fabricates the observation and assertion directly.

Consume a provider-neutral observed mission policy/decision fact through the
mission authority contract. Validate the exact content-addressed exception,
scope, policy revision, effective bounds, authorizer, reason, candidate,
handoff, work, and reviewer ActorID. Include a CAS assertion against the
mission-fact head in every review stage. Review code may interpret the policy;
the GapDB adapter may only observe, translate, and conditionally publish it.

## 3. Bind criterion completeness to the immutable WorkRevision

Handoff currently accepts caller-supplied criterion IDs and a digest derived
from those same IDs. Because no authoritative WorkRevision criterion
commitment is observed or asserted, a caller can submit a strict subset or an
arbitrary self-consistent set and review will merely echo it.

Observe the current immutable WorkRevision criterion commitment through a
kernel-owned port, require exact canonical membership at handoff and review,
and assert its provider-neutral authority head in the sealed publication.
Missing, stale, incomplete, substituted, or extra criteria must refuse before
publication.

## 4. Accept canonically equivalent criterion order

Request preparation canonicalizes criteria, but authority validation rebuilds
IDs from the raw request order. Compare against the canonical prepared
criterion set and add a regression proving that equivalent reordered input
produces the same binding and result.

## 5. Replace the synthetic adapter-policy mutant

`M-WP09-ADAPTER-POLICY-INTERPRETATION` currently calls a test-only helper that
inspects a sealed plan. It does not mutate or exercise the production GapDB
adapter. Replace it with a type-correct production transport mutation or an
injectable adapter decision whose normal contract tests kill policy
interpretation at the adapter boundary. Keep a passing control and demonstrate
the intended semantic kill.

## 6. Require complete publication membership during ambiguous recovery

On publication failure, both services resolve the OperationID and then observe
only the domain head. The GapDB resolution path reads only the operation
binding/head, so operation and aggregate heads can exist while required event
records or other sealed-plan members are absent or mixed. That topology can be
mistaken for resumable progress.

Return an exact-plan reconciliation result bound to the same PublicationID and
verify complete operation, event, head, assertion/effect, and result membership
before resuming. A coordinator-classified partial or mixed publication must
remain unresolved/refused; services must not second-guess it from selected
heads. Add heads-present/events-absent and mixed-publication fault cases.

## 7. Remove or disable the forbidden legacy eligibility path

The pre-v2 handoff and review services still default to `eligibilityLegacyV1`,
accept zero expected eligibility, and construct legacy facts. The v2 authority
services have no non-test production composition. This violates the locked
GapDB-only complete-eligibility decision and leaves the correct code as dead
parallel structure.

Make complete eligibility mandatory on every callable production path, refuse
absent or zero authority, remove the legacy/default fallback, and provide a
real production composition entry point for the v2 services. Preserve package
ownership; if the correction truly requires files outside WP09's declared
surface, record and govern that scope change before editing them.

## Required rereview evidence

- Add production-path tests for replacement lineage, mission-policy
  currentness, authoritative criterion completeness, canonical reordering,
  exact partial-publication membership, and absent complete eligibility.
- Replace the synthetic mutant and rerun the complete executable mutant census.
- Run focused handoff/review and GapDB tests repeatedly, bounded race coverage,
  checkpoint/suffix playback where affected, architecture tests, compile-only
  repository tests, vet, staticcheck, module verification, formatting, and
  diff/ownership checks.
- Submit a new immutable correction commit and tree for independent rereview;
  do not amend the rejected candidate.
