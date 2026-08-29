---
affected_files: []
cycle_number: 3
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T09:13:49Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 review — cycle 3

Verdict: **changes requested**. Repair commit `ff0fe184` closes the four cycle-2 reproductions under insertion, concurrency, restart, temporal, and one-sided-pointer attacks. One narrow mutation path still violates the global OperationID invariant.

## Blocker — mutable legacy OperationID bypasses every cross-namespace insertion guard

The six pairwise insertion directions now fail correctly, and the two-Store race has one winner. However, the new namespace triggers run only `BEFORE INSERT`. The legacy `operations` table must remain mutable for legal status/result updates, and it has no guard making `operation_id` itself immutable.

An independent direct-SQL probe performed this sequence:

1. Insert `operation-phase3-target` into `phase3_operations`.
2. Insert `operation-legacy-source` into legacy `operations`.
3. Run `UPDATE operations SET operation_id='operation-phase3-target' WHERE operation_id='operation-legacy-source'`.

The update succeeded. The database then contained the same OperationID with two durable meanings in different replay namespaces. This is the same defect class as the cycle-1 collision, reached after creation instead of during creation.

### Surgical remediation

Make legacy operation identity immutable while preserving the status/result transitions used by `authority.go`. Prefer a `BEFORE UPDATE OF operation_id ON operations` trigger that rejects every identity change; equivalently, a complete pairwise update-exclusion trigger may be used if identity changes are an intentional legacy contract. Do not make the entire row immutable because legal status/result updates are load-bearing.

Add permanent mutants proving:

- legacy `operations.operation_id` cannot change to a phase3 ID;
- it cannot change to a work-graph ID;
- it cannot change to any different ID even when no collision exists, if identity is declared immutable;
- ordinary started→succeeded and started→failed status/result updates remain legal;
- the failed identity update leaves both original rows and all child/result/event/outbox evidence unchanged after reopen.

## Evidence retained

- All four cycle-2 controls pass repeatedly: expired outbox reclaim is exactly-once after restart; temporal/claim/lease mutants fail; all insertion directions and the two-Store operation race pass; mission snapshot and current-pointer row enforce the exact mission/revision/generation iff law with rollback.
- `go test ./internal/controlstore/sqlite -run 'Test(Phase3OutboxExpired|Phase3OutboxTemporal|OperationIdentityCannot|LegacyOperationNamespaces|CurrentWorkRevisionPointer|GenerationOneHundred)' -count=20` — pass.
- Five cycle-1 repair families, including artifact expected-prior CAS and eligibility reconstruction, pass at `-count=5`.
- Targeted `-race` campaign passes at `-count=2`.
- Architecture tests, compile-only tests, `go vet`, and `staticcheck` pass.
- Historical migration literals v1–v12 remain untouched; repair commit `ff0fe184` changes nine files within the governed WP08 ownership set.
- The temporary adversarial probe and review lock were removed before verdict capture.

Anti-pattern checklist: dead code PASS; synthetic-fixture coverage PASS; silent empty return PASS; FR coverage FAIL only for post-insert global OperationID immutability; frozen surface PASS; locked decision FAIL because two durable meanings remain constructible; shared-file ownership PASS; production fragility PASS.
