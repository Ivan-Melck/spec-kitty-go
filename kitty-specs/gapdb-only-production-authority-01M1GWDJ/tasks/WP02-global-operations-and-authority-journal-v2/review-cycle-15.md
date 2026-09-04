---
affected_files: []
cycle_number: 15
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T01:09:00Z'
reviewer_agent: renata-gapdb-recovery-bounds-review
wp_id: WP02
---

# WP02 review cycle 15 — changes requested

Reviewed `03ac7098fdbadf9a8434ec2726ef54768caaf76c` and
`e8a5fc8f21203785408883edd2f7286ec9dfe70d` on the current combined lane-b
tree.

## Blocking evidence gap: ordinary exact maximum does not cross production

The successful live-constructor table crosses event and checkpoint records
at their exact maxima, but does not cross an ordinary address at exactly
`MaxCanonicalPayloadBytes` (256 KiB). Ordinary recovery currently has only a
small success, a private-helper mapping assertion, and the maximum-plus-one
refusal. Production is presently correct, but an ordinary-only bound
regression could evade this boundary evidence.

Add an `ordinary_exact_maximum` entry using `ordinaryAddress` and
`MaxCanonicalPayloadBytes` to the existing successful
`NewRecoveryRecordV2` table. Reuse the common assertions for exact byte
identity and defensive copying. Re-run the focused repeated test and the
proportionate affected package/static gates; no production-code change is
required.

All other production behavior and the full v2, race, 184-mutant,
all-architecture, compile, vet, staticcheck, module, format, diff, and
ownership gates passed review.
