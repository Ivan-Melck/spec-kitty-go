---
affected_files: []
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T10:37:50Z'
reviewer_agent: user
wp_id: WP10
---

# WP10 Review Feedback — Cycle 2

**Verdict**: Rejected

Implementation `cd87e18` passes its focused, repetition, race, architecture,
static, build, migration-freeze, and exact-scope gates, but it does not yet
satisfy the ratified WP10 authority contract.

## Blockers

1. **Creation provenance remains optional below the helper seam.**
   `mission.Service.Create` and SQLite `CreateMission` accept the zero
   `ProcessID` plus zero `GovernanceContext` path. Every new create must require
   both and fail before store calls/writes. Historical v13 rows remain readable.
   Add the zero/zero mutant and update legacy test fixtures explicitly.

2. **Drift accepts caller-authored semantic truth.** The helper verifies only
   provider mapping/revision before comparing a caller-supplied candidate. It
   must derive candidate semantics from exact provider observations or compare
   every executable/canonical definition field before `DetectDrift`. Add
   semantic-substitution mutants and a provider-revision-only noise control that
   yields `observation_only`.

## High findings

3. **Pre-open validation ordering is incomplete.** Transition, decompose,
   finalize, and artifact approve can open/create SQLite or fake-provider files
   before rejecting locally malformed IDs, times, or closed values. Construct
   and validate command-local typed requests first; prove nonexistent paths stay
   nonexistent for each malformed-input mutant.

4. **T056 qualification is incomplete.** Add the required table-driven matrix
   for provider ambiguity, provider-revision-only noise, stale generation,
   tracker status/assignment non-authority, corrupt snapshots, and typed
   refusals. Build a fresh-reader summary only from public results that explains
   every work item, dependency, criterion, current generation, source
   plan/revision, and next action. Delete each required field in turn and prove
   omission mutants fail.

5. **Refusal envelopes lack observed/expected state and stable adapter/query
   classification.** Add bounded typed observed/expected fields where safe;
   classify corrupt durable observations and adapter failures without collapsing
   them all to `invalid_request`; retain secret/provider-output leakage controls.

## Passing evidence to preserve

- Exact product scope: 17 of 18 governed paths; the existing binary entry point
  required no edit.
- SQLite migrations v1–v13 remain byte-identical to accepted WP08; v14 is the
  only append.
- Existing focused x10, scenario x3, helper/scenario race, dependency,
  architecture, compile, vet, staticcheck, formatting, module, and build gates
  pass.

Repair these findings without weakening the accepted evidence or broadening the
18-file boundary.
