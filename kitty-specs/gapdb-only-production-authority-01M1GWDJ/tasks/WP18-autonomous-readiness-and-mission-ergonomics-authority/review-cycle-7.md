---
affected_files: []
cycle_number: 7
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T15:50:37Z'
reviewer_agent: codex
wp_id: WP18
---

# WP18 post-approval defect 3

The source-bound WP14 scenario campaign exposed a split-authority defect in
the production composition. The capability census reads the readiness-native
mission aggregate, while final acceptance evidence is still published only by
the legacy phase-shaped mission authority. A scenario can therefore either
create a mission that autonomous progress understands or publish the durable
resolved policy facts acceptance understands, but not do both through one
authoritative mission lifecycle.

Required repair:

1. Add an optional related mission-fact transition to readiness fact-open and
   fact-close commands.
2. Validate that the related fact represents the exact scoped readiness fact
   added or removed by the same transition, including project, mission,
   identity, kind, evidence digest, operation, version, and record revision.
3. Publish the readiness aggregate, durable fact aggregate, operation result,
   events, heads, and compare conditions in one sealed GapDB commit plan.
4. Bind the related fact bytes into the idempotency intent so substitution or
   omission conflicts rather than replaying.
5. Prove crash/retry, competing fact mutation, replay, and tamper refusal, and
   add controlled mutants for omission and substitution.

The repair must not make the legacy phase cursor authoritative, teach progress
to accept legacy mission state, or create a second mission lifecycle. The
readiness aggregate remains the sole mission authority; the related fact is an
atomic evidence projection consumed by acceptance.
