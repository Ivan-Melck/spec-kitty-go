---
affected_files: []
cycle_number: 1
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-26T02:30:12Z'
reviewer_agent: reviewer-renata
wp_id: WP05
---

# WP05 Review Feedback — Reviewer Renata

**Verdict:** Changes requested for product commit `4d2a6f1abf8c0262824a433ca2c04b2967b2a25f`.

## Blocking findings

### 1. The universal-invariant campaign manifest is not load-bearing

`internal/testcampaign/manifest.go:155-202` declares `quality` and `full_campaign`, but the runtime command only calls `ProfileByName`; repository-wide non-test call-site searches find no caller of `CampaignByName` or `ValidateCampaign`. The actual campaign graph is separately encoded in `Makefile:63` and `Makefile:75`. `TestCampaignWorkflowContracts` checks only that target names occur, not that `quality` depends on the acceptance profile or that `full-campaign` depends on the full scenario profile.

Consequently, removing `acceptance` from the real `quality` prerequisites or removing `scenario-full` from `full-campaign` can leave the detached manifest and its mutants green. `TestCampaignProfileMutantControls` mutates shadow metadata rather than the configuration that schedules the campaign. This fails T025/NFR-004 and anti-pattern checks 1 and 2.

Make the campaign declaration authoritative for execution, or add a semantic guard that proves the Make dependency/recipe graph exactly matches the manifest. The rejecting mutants must alter the execution-defining surface and fail because the real short/full campaign lost the required universal/stale-probe path. Remove or internalize public functions that remain test-only.

### 2. CI does not retain the promised architecture/mutant evidence

`Makefile:45-47` runs `go test -count=1 ./internal/architecture` only to console. Both workflows upload only `ARTIFACT_DIR`. An independent `make full-campaign ARTIFACT_DIR=/tmp/wp05-renata.snHVAs` produced acceptance, scope, and full-scenario transcripts/reports plus a 15.8 MB helper binary, but no architecture or mutant result file. A passing non-verbose `go test` line also does not identify the mutants or the rule each killed.

This contradicts T023 step 5, T026 steps 3-4, and the validation-campaign contract requiring retained architecture-mutant results. `qualification-reference.json` lists ten elapsed times and a pass verdict, but contains no per-run census/mutant results or commit/tree binding that independently substantiates the 10/10 mutant claim. The local-only and pre-repair qualification caveat is honest; the durable proof is still insufficient.

Emit compact machine-readable architecture/mutant evidence into `ARTIFACT_DIR` on success and failure, and retain auditable evidence for each qualification sample. Do not put the built helper binary in the compact evidence bundle unless it is explicitly required evidence.

### 3. Census verification does not bind events to the selected package

`internal/testcampaign/census.go:15-20` parses `event.Package`, and `VerifyRequest` carries a package, but `Verify` never compares them. It copies the requested package into the report while accepting matching test names from any package. A structurally valid transcript for a different package can therefore receive `verdict=pass`, which is not exact evidence for the selected invocation and leaves FR-013/T022 incomplete.

Resolve or capture the canonical package identity, require relevant start/terminal/package events to belong to it, and add a rejecting wrong-package or mixed-package transcript test.

### 4. The commit includes unowned WP06 work

The WP05 lane owns the workflows, Makefile, `internal/testcampaign/**`, and `internal/scenario/campaign_test.go`. Commit `4d2a6f1` also changes `.gitignore`, which `lanes.json` assigns to WP06. The build already writes to `ARTIFACT_DIR/bin`, so the added repository-root `bin/` ignore is not needed for WP05 and prematurely consumes WP06 T027/FR-009 scope. Remove that hunk from WP05 and leave it to WP06.

The other cross-WP edits were reviewed separately: the `internal/architecture` change is necessary to classify `internal/testcampaign` as non-production quality tooling and correctly rejects authority-to-quality imports; the three governance-context additions in `repetition_test.go`/`zombie_test.go` repair fixtures made strict by WP04 without weakening their assertions. The full campaign exercised `WR-04`, `WR-06`, and both zombie tests successfully. This feedback explicitly records those shared-file reasons, but the unrelated `.gitignore` overlap remains a locality/ownership violation.

## Independent verification

- `git diff --check 4d2a6f1^ 4d2a6f1`: passed.
- `make full-campaign ARTIFACT_DIR=/tmp/wp05-renata.snHVAs`: passed.
  - bounded quality: passed, including focused race, vet, staticcheck, module verification, and govulncheck;
  - full scenario: 71 expected, 71 started, 71 terminal, process exit 0, verdict pass, 747.302 seconds;
  - cross-WP governance-provenance fixtures: exercised and passed.
- Action pin resolution against official repositories: `actions/checkout` v4.4.0 = `11d5960a326750d5838078e36cf38b85af677262`; `actions/setup-go` v6.5.0 = `924ae3a1cded613372ab5595356fb5720e22ba16`; `actions/upload-artifact` v4.6.2 = `ea165f8d65b6e75b540449e92b4886f43607fa02`.
- CI semantics: read-only `contents`, SHA pins, PR cancellation, explicit 12/90-minute timeouts, run-scoped mutable paths, nightly/manual full triggers, and 14-day artifact retention all pass inspection.
- Qualification honesty: PASS for explicitly limiting authority to the local reference host, declining a hosted p95 claim, and disclosing that the ten quality samples preceded the full-only fixture repair; FAIL for the missing durable per-sample census/mutant proof described above.

## Anti-pattern checklist

1. **Dead code — FAIL.** `CampaignByName` and `ValidateCampaign` have no non-test caller; the `Campaign` execution declaration is not consumed by Make or the command.
2. **Synthetic-fixture test — FAIL.** The campaign mutant test mutates detached `Campaign` metadata, not the Make scheduling surface whose removal would omit the invariant.
3. **Silent empty return — PASS.** No new swallowed exception/empty-return path was found; validation success returns are explicit and error paths fail loud.
4. **FR coverage — PASS.** FR-006 has workflow/Make assertions and FR-013 invokes the production `Verify` path, though the blocking semantic weaknesses above must be corrected.
5. **Frozen surface — N/A.** No WP05-relevant file is marked frozen or untouchable in the spec, plan, or contracts.
6. **Locked decision — PASS.** No new path violates the reviewed `MUST NOT` constraints; no provider/runtime surface, waiver, remote-store access, or direct-main delivery was introduced.
7. **Shared-file ownership — FAIL.** `.gitignore` is WP06-owned and unrelated to WP05; the architecture and governance-fixture overlaps are now explicitly dispositioned above.
8. **Production fragility — N/A.** No new production `raise`/panic path was introduced; the campaign CLI's nonzero exit is an intentional fail-loud gate boundary.

WP06 depends on WP05 and should remain blocked/rebase after the corrected WP05 product commit is ready.
