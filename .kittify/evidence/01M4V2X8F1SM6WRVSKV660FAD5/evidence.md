# Complete progress snapshots own a query budget

Status: size-bound correction verified; two newly reached restart terminal
semantic-comparison failures remain; a two-work diagnostic found extra durable
rebind-operation history with all shared terminal facts identical.
Not mission admission, frozen-proof transfer or full workflow/API parity.

## Witness and decision

Requester instruction: `continue`, following the namespace repair and retained
full-suite failures. Governed invocation: `01M4V2X8F1SM6WRVSKV660FAD5`.

The stable `ProgressService.Mission` path refused a real two-work mission at
revision 56 / `integration-work-a`: **273,496 bytes** of marshaled complete
progress, versus the journal's **262,144-byte** canonical event/state payload
budget. Inner diagnostic: `BUDGET_EXCEEDED: canonical_object`; public result:
`PROGRESS_INCOMPLETE: encoding`. See [finding 005](QUALIFICATION_FINDINGS.md).

A complete query joins multiple valid durable states and includes intentionally
repeated family projections. It is not one durable event/reducer state. Keeping
valid operation membership made that mistaken coupling observable; removing
operations would hide the defect and violate completeness.

The progress consumer now owns a **16 MiB serialized snapshot limit**, including
the populated 64-character digest. It retains 10,000-item / 128-page bounds,
full immutable operation membership, ordering, exact revision/epoch checks,
read-only behavior and copy-only cache/watch output. Larger snapshots refuse
wholly with `PROGRESS_INCOMPLETE: snapshot_budget`, never partial success or a
partial cache entry. This is a byte-output/retained-contribution bound, not a
claim of constant RSS across decoding, pages, copies or concurrent readers.

## Boundary and compatibility

[`progress_snapshot.go`](../../internal/productionruntime/progress_snapshot.go)
accepts only the consumer-owned `MissionProgress` DTO. `json.Marshal` produces
its input; there are no raw JSON fields or caller-supplied object keys. Durable
state remains opaque base64 bytes. `UseNumber` then sorted-map remarshal retains
exact uint64 values and the old recursively sorted JSON semantics without
copying a general parser or importing qualification tooling.

The preimage excludes `SnapshotSHA256` itself and deliberately retains the
legacy wire framing:

```text
SHA256("spec-kitty.reducer-state.v2" || NUL || canonical snapshot JSON)
```

This private compatibility domain does **not** make a query authoritative reducer
state. Previously valid snapshots retain identical hashes. JSON schema/envelope,
null/empty distinctions, array ordering and state contents remain unchanged.

The list pass charges serialized retained items. The builder's single
classification selects every actual destination (`Items`, family and possible
pending reconciliation), charges escaped/base64 JSON contributions before their
defensive copies, then appends atomically. Final DTO and canonical-length checks
cover metadata/derived arrays and reserve the digest's extra 64 bytes. Refusals
cannot be cached. Borrowed query bytes never escape the admission/copy boundary.

Rejected alternatives:

- Raise durable payload/codec/admission budgets: unrelated authority relaxation.
- Omit operations, deduplicate away public family arrays, truncate or reduce
  census: schema/completeness break.
- Export an unrestricted kernel canonicalization/commitment API for a query:
  unnecessary foundation boundary expansion.
- Change the digest namespace or hash declaration-order JSON: compatibility break.
- Import the M2 campaign canonicalizer into production: qualification dependency.

No journal parser, reducer digest, durable codec, event size, admission tuple,
provider protocol, frozen source hash or historical receipt was changed.

## Evidence

- Stable-Mission large complete-census and snapshot-budget tests failed pre-fix
  with `PROGRESS_INCOMPLETE: encoding`:
  `/tmp/spec-kitty-progress-budget-red.log`.
- All existing/new `TestProgress*` tests pass, including cache/watch/copy behavior,
  10,000 cached-query performance, unchanged durable oversized-object rejection,
  old digest comparison with uint64 above 2^53 and escaped Unicode DTO strings,
  exact 16 MiB returned boundary including hash, metadata-only overflow,
  dual blocker/decision membership and atomic multi-destination refusal.
- Progress race run passes: `/tmp/spec-kitty-progress-budget-race.log`.
- Six unchanged architecture/durable/absence/documentation guards and changed
  package vet pass: `/tmp/spec-kitty-progress-budget-{guards,vet}.log`.
- Complete runtime/helper/public-CLI packages pass:
  `/tmp/spec-kitty-progress-budget-native-packages.log`.
- Temporary overlays removing exact-number preservation or changing the legacy
  digest domain each fail the compatibility assertion; no working-tree source
  was replaced: `/tmp/spec-kitty-progress-budget-mutant-{uint64-rounding,domain-drift}.log`.
- Independent scoped review found no blocking defects; its initial unnecessary
  copy finding was corrected before final verification.
- One real scenario run with matching exact native-host M2 and distinct
  original/extension bindings completed exit 1 (487.462 seconds):
  `/tmp/spec-kitty-progress-budget-real-green.log`. Publication-cut and durable
  outbox-performance mutation tests **pass**. Two-work restart and real-Git crown
  now reach completion/restoration, then **fail** `canonical terminal item facts
  differ`. No skips reported, no retries. These new comparison failures are
  being isolated; native restart/crown coverage is not claimed green.
- Independent review follow-up found no remaining findings; preflight now
  precedes the initial defensive copy. Exact-limit/derived-metadata, actual
  escaped DTO strings and atomic dual classification coverage were added.

A separate two-work diagnostic found 46 uninterrupted versus 56 restarted items:
all shared facts identical, ten additional explicitly generated rebind operations.
It did not show provider-revision/shared-state divergence. This is consistent
with legitimate recovery history; the qualification equivalence contract must
prove that history separately without deleting operations or weakening terminal
facts. See [finding 006](QUALIFICATION_FINDINGS.md). Real-Git crown has not been
separately diagnosed.

The last complete suite remains failed (47 passing packages, five failing).
Qualification proxy compatibility, distinct nested artifact contracts,
clean source-bound fault/focused/M2 campaigns and M3/M4 entry decisions remain
separate obligations. The uncommitted candidate cannot inherit M2 adoption proof.
