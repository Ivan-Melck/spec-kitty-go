---
affected_files: []
cycle_number: 18
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T00:02:14Z'
reviewer_agent: renata-gapdb-codec-combined-review
wp_id: WP01
---

# WP01 review cycle 18 — changes requested

Reviewed WP01 commits `aaf6abf1fca34bd7d76608f32ca1df127497a8c8`
and `6dcae82fed43ba6460fbc81c39e83ce7cf260bb5` on the combined repaired
candidate `03ac7098fdbadf9a8434ec2726ef54768caaf76c`.

## Blocking finding: direct decoder semantics are discarded

`TestM2CurrentDurableCodecsRoundTripExactRecoveryBytes` discards the decoded
value for all five direct durable codecs. It proves canonical input is
accepted and alternate ordering is rejected, but it does not prove the
decoder returns the exact encoded semantics. A decoder that performs strict
input validation and then returns a zero or substituted value with `nil` can
therefore false-green. This violates the ratified consumer-path law:
`Decode succeeds with exact semantics`.

Restore decode-to-canonical-re-encode byte equality for OperationBindingV2,
EventEnvelopeV2, AggregateHeadV2, OperationStateV2, and
PublicationWitnessV2 (or compare every semantic field/accessor). Add a
production-overlay mutant that preserves strict input validation while
substituting the decoded result, and prove the guard rejects it through the
intended exact-semantics diagnostic. OperationSummaryV2's digest comparison
already protects its nested path.

The two cycle-17 findings are otherwise resolved: the guard is always live,
does not depend on a private helper name, uses type-correct overlays over real
production APIs, preserves the prior 177 mutants, and has an exact 184-mutant
census. Re-run the real consumer path, overlay arbiter, repeated M2 suite,
race suite, all-architecture regression, compile, vet, staticcheck, module,
format, diff, and ownership gates after the repair.
