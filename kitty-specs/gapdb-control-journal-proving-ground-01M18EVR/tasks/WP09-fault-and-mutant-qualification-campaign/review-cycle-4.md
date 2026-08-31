---
affected_files:
- internal/testcampaign/m1/invariants.go
- internal/testcampaign/m1/invariants_test.go
cycle_number: 4
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: SPEC_KITTY_M1_GAPDBD=/tmp/go-kitty3-gapdb-admission.cOGcrB/gapdbd go test ./internal/testcampaign/m1/faults -run TestProductionFaultRecordsAreMintedByExactExecutionReceipts -count=1
reviewed_at: '2026-08-31T09:24:12Z'
reviewer_agent: codex-wp09-arbiter-final
wp_id: WP09
---

# WP09 arbiter review — cycle 4

**Verdict: approved.** The single cycle-3 blocker is closed at implementation
commit `3d57bfc05871291cc3fa3e974818033b5a2acde4`. Qualification now rejects the
complete Git ignored-untracked census rather than guessing which filename
extensions may affect compilation. An ignored cgo `payload.cc`, an ignored
`//go:embed` payload, or any arbitrary ignored file prevents `SourceBinding`
from being minted.

## Independent adversarial evidence

- Added an ignored `.cc` file to the real qualification checkout. The
  production fault attempt terminated `incomplete`, minted neither a source
  binding nor a production receipt, and reported all eight universal
  invariants false. Removing the file restored the passing exact-source
  receipt.
- `TestObserveGitSourceRejects*` passed 20 repeated runs and five race-enabled
  runs, covering tracked, ordinary untracked, arbitrary ignored, cgo C++, and
  Go embed inputs.
- Exact production fault receipts, unbound-source negatives, and
  literal-fixture negatives passed against admitted GapDB SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- All nineteen executable architectural mutants passed under the race
  detector. The architecture guard, vet, staticcheck, gofmt, and diff checks
  passed.
- The root exact-source focused campaign passed 800/800 attempts in 2921.29s.
  This review used risk-proportional targeted replay rather than repeating that
  complete campaign.

## Arbiter decision

The review-cycle limit has been reached, but there is no remaining substantive
disagreement to escalate: the cycle-3 exploit is reproducibly refused and the
clean source path remains admissible. Approval is therefore the functional
correctness outcome, not a waiver.

## WP anti-pattern checklist

1. Dead code — **PASS**: the source guard is called by the production fault and
   focused campaign executors.
2. Synthetic-fixture test — **PASS**: the decisive mutation crosses the real
   production receipt path.
3. Silent empty return — **PASS**: dirty input returns a typed incomplete
   outcome with diagnostics and false invariants.
4. FR coverage — **PASS**: exact source binding and the WP09 campaign laws are
   covered by production-path and mutation-sensitive tests.
5. Frozen surface — **PASS**: the correction changes only the two WP09-owned
   invariant files.
6. Locked decision — **PASS**: no SQLite fallback, embedded GapDB server, CLI
   prose parsing, or WP10 adoption claim was introduced.
7. Shared-file ownership — **PASS**: prior ownership expansions remain recorded
   in the WP activity log; the final correction touches no shared file.
8. Production fragility — **PASS**: the new guard fails loud before evidence
   publication and adds no transient-race panic path.
