---
affected_files: []
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T06:32:09Z'
reviewer_agent: codex
wp_id: WP06
---

---
affected_files:
- internal/handoff/service.go
- internal/handoff/handoff_test.go
- internal/review/service.go
- internal/review/review_test.go
- internal/integration/service.go
- internal/integration/service_test.go
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command: go test ./internal/handoff -run TestReviewerProbeCompleteAuthorityCannotBeSilentlyDisabled -count=1 -v
reviewed_at: '2026-08-28T06:31:14Z'
reviewer_agent: codex
wp_id: WP06
---

# WP06 Review Feedback — Cycle 2

Verdict: changes requested.

## Full reconciliation authority can still be silently downgraded (blocker)

The repair adds the correct complete `EligibilityFactStore` path and exact fact
comparison, but service configuration can demote that authority back to the
permissive frozen legacy query.

All three services have the same shape:

1. `NewService` auto-detects `EligibilityFactStore` and installs it.
2. `WithEligibilityFacts` unconditionally assigns its argument, including nil.
3. `requireEligibility` selects full versus legacy authority solely from whether
   that field is nil.
4. A zero caller-supplied `ExpectedEligibility` permits the legacy branch.

Thus `ExpectedEligibility` is not merely comparison input: together with service
configuration, its absence helps select the weaker authority source. That is the
same class of authority leakage T033 is intended to prevent.

An independent temporary production-path probe used a handoff store that
implemented both the legacy and full ports. Its full fact was a valid
`invalidate` decision and therefore denied handoff. `NewService` auto-detected
that full reader. The probe then called `WithEligibilityFacts(nil)` and submitted
a request with zero expectation. The service returned success:

```text
err=<nil> full_reads=0 legacy_reads=true
```

The authoritative full fact was never read; the permissive legacy tuple was
accepted and verifier/store work proceeded. The probe failed as intended and
was removed.

The same assignment and branch logic is present in handoff, review, and
integration. Their permanent tests cover missing readers and omitted
expectations only when full mode is already selected; none attacks clearing an
auto-detected/installed reader or silently choosing legacy mode.

### Required repair

Make eligibility mode an explicit fail-closed service configuration, independent
of caller request content:

- service configuration alone selects complete versus historical legacy mode;
- `WithEligibilityFacts(nil)` must reject, ignore nil, or otherwise preserve an
  already auto-detected/installed full reader; it must never clear/downgrade it;
- once complete mode is selected, zero `ExpectedEligibility` returns typed
  `revision_ineligible` and can never select the legacy query;
- `ExpectedEligibility` remains comparison-only: it cannot enable, disable, or
  choose the authority source;
- historical frozen SQLite/helper-command wiring may remain an explicit
  `LegacyV1` compatibility assembly until WP08, but the compatibility choice
  must be explicit at assembly/service construction rather than inferred from a
  missing caller fact;
- a full/mixed configured assembly must have no automatic legacy fallback after
  reader absence, nil, masking, or read failure.

Add permanent downgrade mutants for all three consumers:

- clear an auto-detected reader with nil;
- clear an explicitly installed reader with nil;
- omit `ExpectedEligibility` while full mode remains selected;
- attempt to mask a full-capable store behind the legacy interface in a full
  assembly;
- make the full reader fail and ensure the legacy query is never consulted;
- retain the exact committed-replay control proving later eligibility is not
  read.

Keep the existing asymmetric actual-versus-expectation substitution matrix,
including returned `LegacyV1`, predecessor/successor, reconciliation, WorkID,
status, disposition, operation, permission, and reason corruption. Permission
and reason should remain derived and `Complete` must reject tampering.

## Cycle-1 addition-only blocker is closed

An independent new work item now produces `changed` with an exact empty
affected-old/decision/status set and a non-empty affected-new set. The mixed plan
publishes generation 2, emits zero authority actions, and replays exactly without
a second store mutation.

Permanent production-path controls reject invented status/decision authority,
unknown affected-new identity, empty changed sets, and clean/observation-only
reports. Mission publication now compares plan and drift old/new sets exactly
and requires decision count to equal affected-old count. This repair is sound.

## Other cycle-1 T033 repair evidence

When complete mode remains configured, handoff, review, and integration:

- validate exact old/new revision, reconciliation, WorkID, status, disposition,
  operation, derived permission, and derived bounded reason;
- reject returned legacy facts and asymmetric reader/expectation substitutions;
- refuse before verifier, review inspection, fence acquisition, Git, or store
  mutation;
- return exact committed operation replay before consulting later eligibility.

The full path itself is correct; the blocker is that mode selection remains
downgradable.

## Gate and scope evidence

- focused workgraph, mission, handoff, review, and integration suites pass at
  `-count=10`;
- the same five packages pass under the race detector;
- architecture suite passes, including the exact `workgraph -> tracker` denial
  and legal `mission -> tracker` edge;
- `go vet` and `staticcheck` pass for all five affected packages;
- frozen SQLite tests pass, and the controlstore tree is byte-identical to
  cycle-1 product (`54a0fd570c77682b863e25e7bd149a5d7f3ebc7f`);
- the scenario tree is also unchanged
  (`cf52671e1bbbead61b728bf110d77847424e4e12`), so the known census/10-minute
  baseline remains non-attributable;
- repair commit `dc157ba` changes exactly thirteen of the existing fifteen
  governed product files and no controlstore, scenario, provider, planning, Git,
  filesystem, workspace, or lease product code;
- topology cleanup `7d14b07` changes only Spec Kitty status/review artifacts.

All temporary reviewer probes were removed.

## Anti-pattern checklist

1. Dead code: **FAIL** — `WithEligibilityFacts` has no production caller, and
   the only current production helper assemblies remain on the implicit legacy
   path; complete authority is not yet an unavoidable live assembly law.
2. Synthetic-fixture test: **FAIL** — the full-fact tests invoke production
   services, but no permanent test kills reader-clearing or mode-downgrade.
3. Silent empty return: **PASS** — service failures are typed and bounded.
4. FR coverage: **FAIL** — T033 remains bypassable through authority-mode
   downgrade.
5. Frozen surface: **PASS** — SQLite/controlstore and scenario sources are
   unchanged.
6. Locked decision: **PASS** — complete fact equality, provider neutrality,
   immutable history, and no direct authority side effects remain intact.
7. Shared-file ownership: **PASS** — the repair touches exactly thirteen
   governed files and keeps topology artifacts separate.
8. Production fragility: **PASS** — no panic, secret leak, provider coupling,
   or new filesystem/Git/lease side effect was found.
