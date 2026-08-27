---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T15:43:09Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 Review Feedback — Round 1

Verdict: changes requested.

## Issue 1 — Projection is not structurally read-only (blocker)

`DefaultPolicy` permits `TierProjection` to import the complete
`TierAuthority`, `TierArtifactAuthority`, and `TierMissionAuthority` tiers.
`CheckReachability` then forbids projection callers for only two registered
symbols, `mission.NewDefinition` and `mission.EvaluateTransition`. Any other
exported mission/artifact/authority mutation or constructor remains callable
from `internal/projection` without a violation.

This contradicts the required direction model, AD-09/AD-10, FR-003, NFR-008,
C-001, and the reviewer requirement that projection cannot call mission
mutation or authority-construction seams. The accepting
`projection-to-mission-read-model` fixture proves only an import edge; the
guard cannot distinguish that edge from a call to an unregistered mutation.

Remediation evidence required:

- structurally constrain projection dependencies to typed read-model surfaces,
  without granting the whole authority packages; or provide an equivalently
  complete enforceable boundary;
- add a production-shaped mutant containing a third, otherwise unregistered
  mission mutation/authority constructor and prove projection cannot call it;
- retain positive controls for legitimate typed read-model rendering.

## Issue 2 — The authoritative architecture campaign fails (blocker)

The implementation adds seven top-level tests but does not update or preserve
the exact architecture campaign census. Running

```text
go run ./internal/testcampaign/cmd/campaign -profile architecture \
  -artifact-dir /tmp/wp01-architecture-campaign-clean -timeout 8m
```

returns `verdict=fail` with these unexpected tests:

- `TestDefaultPolicyClassifiesPhase3CapabilitiesExactly`
- `TestDefaultPolicyEnforcesPhase3DirectionsWithExactMutants`
- `TestDefaultPolicyPreservesDeferredPhase4Through6Surfaces`
- `TestPolicyValidationRejectsMalformedSpecificDirections`
- `TestPhase3ReachabilityRegistrationActivatesWithoutPrematureFailure`
- `TestPhase3TransitionGuardRejectsProjectionCaller`
- `TestDeferredRuntimeSurfaceRejectsPhase3TasksAuthorityButAllowsProjectionWrites`

WP01 explicitly requires preserving top-level census assumptions and prefers
new cases as subtests. Keep the new evidence inside already-censused top-level
tests (the owned-file solution), or otherwise coordinate explicit ownership of
the campaign manifest rather than leaving the quality gate red. Re-run the
real campaign and provide a passing exact-census report.

## Issue 3 — The lane's review diff is not confined to owned files (blocker)

The authoritative review diff against
`kitty/mission-software-development-mission-method-01M11EX2` contains 37 files,
not only the six owned architecture files. It includes 33 mission-artifact
changes and deletes the coordination branch's
`acceptance-matrix.json`. Commit `dff5220` is itself a WP01 commit that changes
mission artifacts outside the declared ownership surface.

Repair the lane so the net review diff against the coordination branch is
confined to WP01-owned architecture files. Preserve the coordination branch's
acceptance matrix and do not land planning/status artifact churn through this
lane.

## Validation evidence

The following implementation checks passed and should remain green after the
repair:

- focused policy tests;
- focused reachability and deferred-runtime tests;
- `go test ./internal/architecture -count=10` (130.096 s);
- `go test -race ./internal/architecture -count=1`;
- repository compile-only test;
- `go vet ./internal/architecture`;
- `go mod verify`;
- `gofmt -d` over all six owned files;
- implementation-commit diff hygiene.

The first direct campaign attempt was refused because Spec Kitty's active
`.spec-kitty/review-lock.json` made the tree appear dirty. The lock was parked
only for the bounded campaign execution and restored immediately; the clean
campaign then produced the exact-census failure above.

## Anti-pattern checklist

1. Dead code: PASS — new exported policy/reachability fields and tiers have
   production callers.
2. Synthetic-fixture test: FAIL — the projection/read-model acceptance fixture
   cannot prove that mutation calls are excluded; the guard allows them except
   for two named symbols.
3. Silent empty return: PASS — no new silent empty-return path was found.
4. FR coverage: FAIL — NFR-008/FR-003 projection non-authority is not proven by
   a complete mutation-negative control.
5. Frozen surface: PASS — the implementation commit does not modify a
   specifically frozen source file.
6. Locked decision: FAIL — the projection allowance contradicts the locked
   one-way read boundary and non-authority requirement.
7. Shared-file ownership: FAIL — the lane net diff changes shared mission
   artifacts without an ownership/coordination basis and deletes the acceptance
   matrix.
8. Production fragility: PASS — no new panic/fail-fast production path was
   introduced.
