---
affected_files: []
cycle_number: 17
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-03T22:58:25Z'
reviewer_agent: renata-gapdb-codec-guard-review
wp_id: WP01
---

# WP01 review cycle 17 — changes requested

Reviewed commit: `aaf6abf1fca34bd7d76608f32ca1df127497a8c8`

## Blocker 1: reversible activation makes the real guard a no-op

The production consumer-path test returns whenever the private helper spelling
`marshalCanonicalObject` is absent. The helper is currently absent, so the
test runs no codec subtests. A harmless helper rename or later deletion can
silently disable the permanent guard. The synthetic boolean activation test
does not bind this switch to repository semantics.

Replace this with an always-live, contract-owned semantic validator. Because
the pre-repair production tree is intentionally red, stage the guard candidate
with the WP02 repair candidate and independently approve WP01 against that
combined tree before WP02 approval. Do not use a reversible private-name gate.

## Blocker 2: mutants exercise a parallel model, not production paths

The seven new mutants switch test-local generic encode/recover/decode closures
rather than the actual foundation APIs. They therefore do not prove that real
production mutations are killed. In particular, actual decoders receive only
already-canonical RecoveryRecord bytes, the real codec table is not reconciled
to the declared census, and removing production decoder equality remains
green.

Bind every mutant to the same real repository validator, or to type-correct
production-shaped isolated source/overlay. Reconcile the declared six-codec
census with the actual consumer table. Directly feed semantically valid
noncanonical ordering into every strict decoder. Prove raw struct order,
top-level-only normalization, discarded normalization, length-only recovery
admission, removed decoder equality, omitted classification, and partial repair
fail for their intended production-facing law.

Preserve all prior 177 mutant IDs and the existing census/digest controls.
