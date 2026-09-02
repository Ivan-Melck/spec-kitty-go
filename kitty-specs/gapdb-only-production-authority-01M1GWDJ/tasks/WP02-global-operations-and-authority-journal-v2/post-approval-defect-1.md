# WP02 Post-Approval Integration Defect 1

Status: confirmed consumer-contract defect; bounded repair required before WP03.

## Discovery

WP03 attempted to consume the terminally approved WP02 `SealedCommitPlanV2`
using only its public contract. The adapter could not construct its required
single exact AtomicBatch without inventing a second persistence model.

The sealed plan exposed canonical binding, OperationState, and event values,
but `HeadTransitionV2` did not contain the epoch and authority summary needed
for canonical head bytes. Effect and eligibility members contained only opaque
digests. Checkpoints contained an immutable record but no slot target, value,
or condition. Provider conditions addressed only aggregate heads. The
normative keyspace also omitted the effect-intent key that both the publication
and ambiguity contracts require.

This contradicts WP03 T014/T015, which require translation and reconciliation
from the sealed plan alone while preserving exact membership and values.

## Architectural correction

WP02 will derive a closed immutable provider-neutral record projection during
sealing. Its roles and targets are finite and typed. Its members contain the
canonical value, value class, and absent-or-positive-revision condition needed
by a storage adapter. There is no public member constructor and no raw key,
provider type, delete, expiry, or condition-any surface.

The repair also makes typed input persistence-complete:

- head transitions provide committed epoch and canonical authority summary;
- effect intents provide their complete canonical value and use a host-global
  EffectID target;
- eligibility transitions provide mission, shard, canonical value, and exact
  observed revision;
- checkpoint transitions pair immutable record and mutable slot targets,
  values, and slot condition; and
- exact condition bijection covers every mutable projected record.

WP03 remains the sole owner of canonical GapDB key construction and provider
translation. Capability packages remain the owners of effect, eligibility,
checkpoint, and aggregate semantics.

## Bounded repair and review law

The implementation repair is confined to WP02's five owned files. It must keep
all previously approved WP02 evidence green and add consumer-contract mutants
for every newly complete member. Because this defect was found after terminal
approval, the repaired surface receives a fresh independent terminal review;
it does not reopen the exhausted ordinary review loop or erase its evidence.
