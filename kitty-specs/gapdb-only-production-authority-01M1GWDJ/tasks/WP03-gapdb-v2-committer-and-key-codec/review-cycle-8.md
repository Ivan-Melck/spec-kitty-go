---
affected_files:
- path: go.mod
- path: go.sum
- path: internal/controlstore/gapdb/admission_test.go
- path: internal/controlstore/gapdb/external_scenarios_test.go
- path: internal/controlstore/gapdb/port.go
- path: internal/controlstore/gapdb/port_test.go
- path: internal/controlstore/gapdb/v2_adapter_test.go
- path: internal/controlstore/gapdb/v2_ambiguity.go
- path: internal/controlstore/gapdb/v2_codec.go
- path: internal/controlstore/gapdb/v2_committer.go
cycle_number: 8
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command: go test ./internal/controlstore/gapdb -count=1
reviewed_at: '2026-09-04T16:01:22Z'
reviewer_agent: codex
wp_id: WP03
---

# WP03 post-approval dependency repair: GapDB read-only assertions

WP08 proved that the approved adapter cannot atomically bind a new workspace
against an unchanged execution head. `post-approval-defect-9.md` ratifies the
missing primitive and records the released upstream implementation.

Reopen WP03 after the corresponding WP02 sealed-plan repair is approved. Apply
these bounded changes without altering capability policy:

1. Pin `github.com/spec-kitty/gapdb` to
   `v0.0.0-20260904151017-4d88094749e0` with module sum
   `h1:x/zAH/n0M/nA1RQpi1ubRETo8OLcEyRBsaDUsd1TuU4=` and unchanged go.mod sum
   `h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=`. No replace directive,
   local shim, private provider import, or copied protocol type is permitted.
2. Keep the original M1 `Admitted*` runtime tuple immutable until WP13 owns the
   human adoption cutover. Add separately named assertion-candidate constants
   for source commit `4d88094749e0f6fdb38a4786708d920fb51966a1`, source tree
   `a109220e3dae287e321fb37e904d3bea0802b911`, module version above, Linux/amd64
   sidecar SHA-256
   `947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad`,
   and size `5327126`. Admission tests must distinguish "compiled client
   capability" from "production-admitted executable" explicitly.
3. Translate every WP02 sealed authority assertion into exactly one public
   `gapdb.Assertion` on the role-specific key with only `absent` or exact
   record-revision conditions. Assertions are not mutations and must never gain
   TTL, value bytes, or a write result.
4. Translate the permanent workspace-path claim record through one closed key
   spelling derived only from its sealed semantic claim digest:
   `spk/v2/workspace-path-claims/<64-lowercase-claim-digest>`. Require absent,
   ordinary value, no expiry, and exact witness membership. The codec must
   expose a closed workspace-path-claim kind/accessor/parser; no raw path or
   caller-chosen provider key participates in the spelling.
5. Enforce the product's combined assertion-plus-mutation count and byte budget
   before delivery. Require exact `MutationCount` and `AssertionCount` in a
   successful provider response.
6. On response-loss reconciliation, first prove all written publication
   records at one provider revision, then re-read every assertion target.
   Return committed only while its revision and canonical value digest still
   equal the sealed assertion evidence. Changed or unprovable assertion state
   remains typed ambiguous; it is never inferred from successful writes.
7. Extend the deliberately dishonest fake provider and exact Unix-sidecar
   campaign for assertion success, stale revision, absent conflict, result-count
   mismatch, response loss followed by stable evidence, response loss followed
   by drift, overlapping key refusal, and path-claim contention. The external
   campaign must execute the exact candidate binary named above and must not
   relabel it production-admitted before WP13.
8. Preserve all old v2 codec, mutation, witness, ambiguity, diagnostic, and
   architecture controls. Kernel packages remain unable to import GapDB.

Controlled mutants must kill assertion omission, assertion-as-put, condition
downgrade to `any`, missing value-digest reconciliation, trusted result counts,
path-claim expiry/deletion, and accidental replacement of the frozen M1 tuple.
