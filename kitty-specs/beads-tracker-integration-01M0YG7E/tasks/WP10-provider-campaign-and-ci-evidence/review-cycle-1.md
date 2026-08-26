---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T20:49:52Z'
reviewer_agent: user
wp_id: WP10
---

# WP10 Review Feedback — Cycle 1

**Verdict:** Changes requested

Reviewed implementation commit `e3eb9af6f85f541a1619c74a1a9f118460ed5935` on the consolidated source `e7f114ae8b5b87fadb488b38369d37ace38a1298` / tree `7a7a3e614bcf9cc22027293bc37e8a9b80e408f4`. The retained bounded artifacts are internally consistent and the focused, repeated, race, vet, architecture, and Make target checks pass. The following acceptance laws are not yet connected to the executable qualification paths.

## 1. Blocker — the strict evidence law is disconnected from the evidence producer

`internal/testcampaign/beads_evidence.go` defines `BeadsQualificationEvidence`, `EncodeBeadsEvidence`, and `WriteBeadsEvidence`, but repository callers are confined to `beads_evidence_test.go`. The command invoked by `make beads-qualification` still emits its separate `qualificationEvidence` through `validateQualificationEvidence` and `writeEvidence` in `internal/testcampaign/cmd/beads-qualification/runner.go`.

This means the WP10 source-binding, scenario-ID, verdict, timing, and redaction laws—and their mutants—do not govern the artifact used for adoption. The two validators also differ materially: the WP10 validator requires UTC timestamps and ordered p50/p95/max values for every verdict and scans a wider redaction vocabulary, while the command validator does not enforce the same laws on all outcomes. `Proof` is excluded from JSON and never populated by the actual producer.

Required correction: make one strict validator/encoder authoritative for the real qualification command and its adoption artifact. Mutants must cross the actual command-to-artifact path and fail when that emitted evidence violates each law; remove or collapse the disconnected evidence model.

## 2. Blocker — a bounded campaign can pass with dirty source

In `internal/testcampaign/cmd/campaign/main.go`, `sourceIdentity` uses `git status --porcelain --untracked-files=no`, so untracked source/test files are ignored. `runProfile` records `SourceDirty` without refusing it, and `bindCampaignSource` only requires all profiles to report the same value. Consequently, two profiles executed from the same dirty tree can produce a campaign verdict of `pass`.

The dirty-source mutant exercises the disconnected WP10 evidence value rather than the real campaign command, so it does not protect this path. The real-provider runner checks ordinary untracked files before execution but does not re-check source identity after the tests; source drift during qualification can therefore be published under the initial commit/tree binding.

Required correction: refuse dirty source—including relevant untracked files—before bounded and real execution, re-bind/re-check source identity before publishing final evidence, and add mutants/integration tests against the actual runners proving dirty or changed source cannot emit `pass`.

## 3. Blocker — the hosted real workflow cannot produce admissible evidence

`.github/workflows/beads-qualification.yml` neither provisions a shared Dolt server nor supplies the exact server tuple consumed by the real runner (`BEADS_SERVER_ID`, `BEADS_SERVER_ENDPOINT`, `DOLT_SERVER_HOST`, and `DOLT_SERVER_PORT`). Without all four, `qualificationStorageMode` selects `embedded_serialized`, and `TestRealBeadsWorktreeQualification/server_multiwriter_contention` skips.

Passing real evidence requires that scenario plus the exact 8-process/1000-operation contention proof. Therefore the scheduled/manual workflow is structurally limited to `non_admissible`; its final pass enforcement cannot succeed. It is acceptable that the present baseline is `baseline_pending` and that no hosted credentials exist, but the checked-in workflow must be capable of producing admissible evidence when run.

Required correction: provision a disposable shared server inside the offline qualification boundary (or provide an equivalently verified, non-secret tuple reachable within it), bind its exact identity into the runner, and preserve runner-owned lifecycle/cleanup and fail-closed networking.

## 4. High — `provider_real` claims a stale probe that it does not execute

`internal/testcampaign/manifest.go` marks `provider_real` with `RequireStaleProbe: true`, but `TestRealBeadsQualification` and `auditRealAuthorityIsolation` do not exercise a stale lease, fence, or workspace authority probe. `ValidateCampaign` verifies only the declaration and test name; the missing-stale mutant merely flips the declaration. This is declarative coverage, not evidence of the universal invariant.

Required correction: connect the real-provider campaign to an actual stale-authority probe wherever execution authority participates, and make a mutant that removes or disconnects that executable probe fail. If a particular scenario is genuinely provider-only, encode applicability honestly rather than satisfying the census with a boolean.

## Verification evidence

- Owned implementation diff is exactly the six WP10 paths; `git diff --check` passes.
- Retained bounded artifacts bind source `e7f114a` / tree `7a7a3e6`, report clean source and exact portable/adversarial profiles, and independently recompute to semantic SHA-256 `6b9afb918190bf649aec27e2bf0c28942f96a3a588975c5b87a48b5dbf3f2cae` in both runs.
- Focused campaign/evidence/workflow/mutant tests pass.
- `go test ./internal/testcampaign -count=10` passes.
- `go test -race ./internal/testcampaign ./internal/tracker/conformance -count=1` passes.
- Relevant `go vet` and architecture gates pass.
- Make dry-runs preserve bounded, quality, and real-qualification target separation.
- Artifact permission and redaction inspection found no credential or governed payload leakage.

## Anti-pattern checklist

- Dead code: **fail** — the new strict evidence API has test-only callers.
- Synthetic-fixture gate: **fail** — evidence mutants do not reach the artifact-producing command.
- Silent empty return: **pass / not applicable** to the owned Go changes.
- Requirement coverage: **fail** for the executable evidence/adoption law and real stale-probe claim.
- Frozen surface and shared-file ownership: **pass** — the implementation commit changes only the six authorized paths.
- Locked source/qualification decisions: **fail** — dirty source can pass and the hosted real workflow cannot become admissible.
- Production fragility / bare-raise check: **pass / not applicable**.
