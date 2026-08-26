---
affected_files: []
cycle_number: 3
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T13:44:35Z'
reviewer_agent: user
wp_id: WP06
---

# WP06 Review Feedback — Cycle 3

**Verdict: changes requested.** The three mutation branches now have permanent recording-double tests for exact request translation, returned mutation, sentinel error propagation, and correct-call deletion sensitivity. One assertion gap remains: the tests do not prove zero calls to every wrong port method, and a temporary wrong-call mutant passed.

## Sole blocking finding

### Mutation routing tests do not assert all wrong-port call counts

**Severity:** high  
**Affected:** `internal/helpercmd/tracker_test.go:193-372`  
**Contracts:** cycle-2 finding; deletion-test requirement; FR-010; FR-011

The new tests correctly require one selected call, but some doubles are passed as unretained literals or expose counters that are never checked:

- dependency put/remove do not retain or inspect the `recordingCoordinationDiscussion` double;
- discussion append does not retain or inspect the `recordingCoordinationGraph` double;
- the error table checks only the selected method's counter and request.

Consequently, an extra wrong method can execute without failing the test. The review temporarily inserted this production mutant in the `dependency-put` branch:

```go
_, _ = discussion.AppendDiscussion(ctx, tracker.AppendDiscussionRequest{})
```

Both permanent mutation tests still passed:

```text
PASS TestTrackerCoordinateBeadsMutationBranchesCallProviderNeutralPorts
PASS TestTrackerCoordinateBeadsMutationBranchesPropagatePortErrors
```

The mutant was removed immediately and the lane is clean. Retain both Graph and Discussion doubles for every case and assert exactly one call to the selected method plus zero calls to all five non-selected methods, in both success and error cases. A shared call-census helper is sufficient. This makes wrong-method addition/rerouting fail while preserving the existing exact request/result/error assertions.

## Earlier blockers remain closed

- Complete-catalog conflicting readiness refusal: PASS.
- Healthy capability consistency plus incompatible/read-only/unavailable/server fail-closed reports: PASS.
- All six shipped production callers exist: PASS.
- Approved command/catalog/inspect/helper ownership expansion remains explicit and bounded: PASS.
- Dependency direction, non-authoritative readiness, discussion replay/ambiguity, human comment preservation, and diagnostic redaction remain intact: PASS.

## Evidence

```text
PASS go test ./internal/helpercmd -run 'TestTrackerCoordinateBeadsMutationBranches(CallProviderNeutralPorts|PropagatePortErrors)' -count=100
PASS go test ./internal/tracker/beads -run 'TestReadyProjectionRefusesConflictingStableMapping|TestPreflightAndInspectHealthyEmbeddedCapabilities|TestDiscussionAmbiguousAppendObservesWithoutBlindRetry|TestGraphExactDirectionRoundTripReplayAndRemoval' -count=20
PASS go vet ./internal/helpercmd
PASS git diff 81f2c91^..81f2c91 --check
FAIL temporary wrong-port-call mutant: the focused helper mutation tests remained green
```

No full race or package-wide repetition was run, per the explicit bounded-review instruction. The temporary mutant was fully restored before verdict capture.

## Anti-pattern checklist

1. **Dead code: PASS** — all six adapter methods have shipped non-test callers.
2. **Synthetic fixture: FAIL** — mutation tests invoke the production seam, but their doubles currently permit an unasserted wrong production call; the test can pass for an incorrect routing shape.
3. **Silent empty return: PASS** — no silent success fallback was introduced.
4. **FR coverage: FAIL** — selected mutation behavior is asserted, but zero-wrong-call routing for FR-010/FR-011 is not deletion-sensitive.
5. **Frozen surface: PASS** — cycle 3 changes only the explicitly authorized helper test file.
6. **Locked decision: PASS** — no provider state gains execution/review/integration authority.
7. **Shared-file ownership: PASS** — helper test ownership expansion remains explicitly rationale-logged.
8. **Production fragility: N/A** — production code is unchanged in cycle 3; the review mutant was temporary and removed.
