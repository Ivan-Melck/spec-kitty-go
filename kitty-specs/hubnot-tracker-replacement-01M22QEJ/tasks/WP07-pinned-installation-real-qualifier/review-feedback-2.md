# WP07 review feedback 2 — separate query child-process census

Reviewer: `codex-independent-qualification-review` / reviewer-renata.
Reviewed source: `6fd7b5a5a4a2cc56980752e08f9e2722729ace1c`.
Disposition: request changes for one newly identified dependency requirement gap. Preserve the earlier approval and all historical evidence; do not rewrite their recorded outcomes.

## Finding

`performanceScenario` in `fixture.go` records only `consumer_seconds`, host and load for `performance.known-reference` and `performance.original-recovery`. Only the inventory branch captures an execution trace and child-process census. `validateHubnotRow` likewise requires the process-trace/count measurements only for inventory. The retained 6aaf performance receipts faithfully reflect this implementation; their separate query rows do not contain child counts.

NFR-002 requires separate known-reference/recovery measurement and recorded commands/elapsed time; it does not literally say child-process counts. The general process-count requirement is `contracts/qualification.md` line 22, and WP10 T029 step 6 explicitly requires the separate operations' duration **and child process counts**. WP07 owns the shared runner that must supply those observations for WP10's independent acceptance audit. A recorded single consumer invocation does not count its provider/Git children.

The existing runner deletes its disposable fixture on close. A receipt-only WP10 audit therefore cannot reconstruct missing process observations after the run. A second fixture engine, another complete setup or relabeling the one consumer command as a child census would not faithfully close this gap.

## Required correction and ownership

Change only these four existing WP07-owned files:

- `internal/testcampaign/cmd/hubnot-qualification/fixture.go`
- `internal/testcampaign/cmd/hubnot-qualification/fixture_test.go`
- `internal/testcampaign/hubnot_evidence.go`
- `internal/testcampaign/hubnot_evidence_test.go`

After each existing untraced known-reference/recovery measurement, perform a separately labeled process-filtered trace of the same request while the already prepared fixture remains alive. Reuse the admitted binaries, bounded command capture, existing execution-trace parser and before/after ref guards. Record the trace artifact, actual relevant child census and separately measured traced duration. Check stable returned semantics/proof against the corresponding untraced result, permitting observation timestamps to differ. The recovery trace must remain verified historical replay with unchanged refs.

Keep fixture setup outside measurement, keep untraced latency distinct from trace overhead, and preserve existing adapter budgets. No new manifest rows, exported API, additional fixture setup or duplicated engine is needed. Strengthen evidence validation so these separate rows cannot pass without the required typed trace/count/timing data and artifact reference.

## Required evidence

Capture red regressions for missing/invalid known-reference and recovery trace/count evidence before the fix. Exercise both traced paths through the existing small real fixture with the admitted consumer/provider and verify counted execution classes, stable result comparison and unchanged refs. Retain parser refusal coverage for malformed/incomplete traces. Use the existing exact source-bound quality/review process after bounded independent correction review; final integrated WP10 will run the full scenario set on the integrated source.

No historical receipt may be rebound or supplemented with invented measurements. The earlier 1,000-item results remain historical and overall precutover non-admissible. This finding does not invalidate their actual recorded semantics or timing; it identifies a missing measurement needed for final acceptance. WP09 may continue disjoint implementation but must refresh the independently reapproved dependency before final integration qualification.

This review was read-only: no tests/provider calls or application changes were made to establish the gap. Runtime status is changed only through the subsequent normal rejection command.
