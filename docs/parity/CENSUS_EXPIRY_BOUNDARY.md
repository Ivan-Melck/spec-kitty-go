# Aggregate-head census expiry boundary

Status: **evidence/contract blocker, not a diagnosed GapDB defect**. The proposed
stronger physically-persisted census is unproved. The admitted provider's intended
live/expiry semantics must not be rewritten under old qualification.

## Independently verified source evidence

The current pinned module is GapDB
`v0.0.0-20260907114832-006e9d469d40`, matching the adopted M2 candidate's source
revision. `GOTOOLCHAIN=go1.26.7 go mod verify` reports all modules verified.
Parent read the exact pinned source:

- `internal/engine/scan.go`: `ScanPrefix` skips `recordExpired(record, asOf)`;
  continuation cursors pin both revision and AsOf.
- `internal/engine/state.go:233–270`: `ReadRecoverySnapshot` likewise skips
  expired records at its AsOf while reporting the current revision.
- Existing provider tests explicitly expect expired values to be absent.

A once-only **provider-source semantic diagnostic**, not admitted-executable
qualification, ran with Go 1.26.7 and btrfs TMPDIR:

```text
go test -count=1 -v -timeout=90s ./internal/engine
  -run '^(TestScanExcludesExpiryAtExactBoundary|TestReadRecoverySnapshotIsStableSortedBoundedAndCopySafe)$'
```

Both tests pass, package 0.002 s, zero skips. The scan test uses a manual clock/
manual expiry timer; the snapshot test supplies an expired record and asserts
its omission. These are existing upstream unit contracts, not a newly executed
expired-product-head injection or live sidecar/raw-storage census witness.
No module/provider source, executable bytes or repository authority changed.

Evidence:
- `/home/user/.cache/spec-kitty-parity-resume-followup/census-provider-module-verify.log`
- `/home/user/.cache/spec-kitty-parity-resume-followup/census-provider-source.sha256`
- `/home/user/.cache/spec-kitty-parity-resume-followup/census-provider-expiry-semantics.log`
- `/home/user/.cache/spec-kitty-parity-resume-followup/aggregate-head-census-design-review.md`

## What cannot honestly be claimed

Stable capture/final revisions and expiry-free **returned** head records cannot
prove absence of an unrelated expired extra physical head. Both available census
reads filter expiry; switching from paged scan to recovery snapshot does not
close that gap. Current product publications are expiry-free, but their write
invariant alone is not independent absence evidence for out-of-band expired
records. Do not silently replace physical completeness with live visibility.

A narrower proof could establish the complete live head set at pinned scan AsOf
and exact matching sealed ACK record bytes. It would still not prove physical
persisted completeness, original producer-database provenance (opaque ACK has no
DatabaseID), omitted operation-only history, orphan journal records, private
runtime admission, capability semantics, projection digest or progress multiset.
Each has a distinct proof boundary.

## Defensible next paths

1. Obtain a separately justified owner-scoped physical-record observation that
   includes expired records; any provider/API extension needs its own exact
   source/artifact contract, independent review and fresh qualification. Do not
   mutate the admitted provider or retarget its receipts.
2. If a narrower live-only proof is pursued, name its actual contract and keep
   the physical-completeness blocker explicit. It cannot be credited as the
   missing stronger release proof or used to hide unknown history.
3. Follow-up synthesis found a conditional **product-only** alternative using the
   existing GLOBAL retained count plus independently readback of a complete
   closed expected record set. Inclusion + equal cardinality can close current
   whole-owner membership without arbitrary/global scans or a new provider API.
   Independent design review found it viable under authenticated per-RPC ownership,
   the S0/read/S1-count/S2 revision bracket, exact encoded keys, complete fixed
   private record readback and bounded nonleaking refusals. These product seams
   are not implemented, so the physical proof is still absent. See
   [conditional whole-owner alternative](GLOBAL_CARDINALITY_CENSUS_ALTERNATIVE.md).
   Do not infer that a new provider image is necessary merely because product
   Inspection currently drops an existing count. Changed product proof still
   needs fresh source-bound review/checks; adopted receipts are not transferred.

No production census seam or real restart oracle was changed. The scoped-reviewed
logical workspace/execution closure and separate configured-origin work can
proceed independently; neither repairs this gap or authorizes path tokens by
itself. See the corrected
[proposal](AGGREGATE_HEAD_CENSUS_PROPOSAL.md) and
[graph handoff](TYPED_RESTART_GRAPH_NEXT.md).
