---
affected_files: []
cycle_number: 14
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T00:38:40Z'
reviewer_agent: renata-gapdb-codec-production-review
wp_id: WP02
---

# WP02 review cycle 14 — changes requested

Reviewed production commit `03ac7098fdbadf9a8434ec2726ef54768caaf76c`
with the approved WP01 guard patch
`2d9d50893f4a60e3a457cd3094ff31e55879762c`.

## Blocking finding: recovery canonicalization uses the wrong value ceiling

`NewRecoveryRecordV2` prechecks against `MaxCheckpointValueBytes` (4 MiB),
then canonicalizes every address through `CanonicalObject`, whose fixed limit
is `MaxCanonicalPayloadBytes` (256 KiB). A valid canonical checkpoint record
between those sizes is therefore rejected as `ErrorMalformed`, including the
size class reserved for the structured operation summary and its enclosing
checkpoint.

Select the canonicalization ceiling from the semantic `RecordAddressV2` kind
and use the bound-aware canonical helper. `AddressCheckpointRecord` must admit
canonical values through `MaxCheckpointValueBytes`; ordinary records retain
their ordinary limit, and journal events must retain their declared envelope
limit. Preserve exact `bytes.Equal` refusal without rewriting provider bytes.

Add positive byte-identity tests above 256 KiB and exactly at each expanded
address-specific maximum, together with noncanonical and maximum-plus-one
refusals. Ensure the checkpoint test crosses the real
`NewRecoveryRecordV2` boundary rather than testing only nested summary JSON.

All other defect-6 behavior passed: recursive final bytes, event digest
preimage and golden digest, unchanged State/Witness semantics, exact typed
round trips, nested Summary decoding/order refusal, typed noncanonical
provider refusal, 10,000-boundary size budget, seven-file ownership, and all
repeated/race/architecture/static gates.
