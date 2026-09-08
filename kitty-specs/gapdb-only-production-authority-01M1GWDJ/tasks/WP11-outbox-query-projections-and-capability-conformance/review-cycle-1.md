---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T08:20:25Z'
reviewer_agent: mill-wp11-independent-reviewer
wp_id: WP11
---

# WP11 independent review — cycle 1

**Verdict:** Request changes

**Reviewed product commit:** `1e036307191cbef5cbbd5a9aaf4c2cf239f44fd1`

The projection/GapDB boundary is correct, but the candidate is not yet safe to
approve. Repair the following without importing projection into the GapDB
adapter:

1. Compose actual WP05–WP10 reducer registrations and executable accepted and
   refused capability evidence. Replace the name-only census and synthetic
   pass-through conformance claim; add controlled mutants for T058's laws.
2. Bound every list filter before allocation, reject unsupported families, and
   attach database, projection revision/digest, and runtime epoch attribution
   to every Get result.
3. Deep-copy nested outbox observations in every defensive result and prove
   bidirectional mutation isolation.
4. Make watch cancellation idempotent and safe under concurrent callers.
5. Enforce safe-retry and compare-before-compensate predicates again inside the
   pure durable reducer, not only in the live command builder. Add reducer
   mutants for revision, value, attribution, and absence checks.
6. Remove the inherited `Current step` field from the production tasks
   projection; readiness/action census replaces phase ceremony at composition.
7. Remove provider-specific revision vocabulary from the consumer-owned outbox
   acknowledgement.
8. Add GapDB faults for pre-commit failure, persistent ambiguity, definitive
   absence/replanning, malformed acknowledgement, and proof that unresolved
   ambiguity cannot mint retry or compensation permission.

Focused tests, targeted race tests, vet, staticcheck, compile-only, formatting,
and import audits passed. The rejection concerns missing executable safety and
conformance evidence, not the overall architecture.
