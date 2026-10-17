# Saved checkpoint resumption checks

The saved `docs/parity/RESUME.md` was located and read before work resumed.
No mission/admission/release status or historical qualification receipt changed.
No active goal was created and the architect Op remains open.

## Independently checked saved state

- Main HEAD: `1f460f6c8fcdfa4b7f8fca2d244d92fcc5c25538`.
- Clean qualification HEAD/tree: `41c20b14a31a9241833c39b0e3c4b6d652d7e136` /
  `f81c9201998c7a96fdac4dbd5809fc2a3de523ee`; status empty. Left untouched.
- All 109 files listed in the persistent checkpoint's source inventory matched
  their saved hashes **before this continuation's changes**.
- All 58 copied artifacts, manifested archives/bundles, saved M4 coordination
  status and saved resume note matched their checkpoint hashes.
- Existing main/M4 coordination worktree topology was inspected, not replaced.
- Explicit `GOTOOLCHAIN=go1.26.7` resolves to Go 1.26.7; admitted runtime TMPDIR
  `/home/user/.cache/spktmp` remains btrfs.

## Exact executable recovery after `/tmp` disappearance

All three original executable paths were absent. Read-only independent research
identified the saved admission/provenance chain. Parent separately verified
saved bytes/sizes, original M1 admission, WP13 extension admission/base-adoption
hash links, and the M2 adoption's manifest/producer/review/release hashes.
Parent also read the release's exact producer member and that producer's exact
manifest, binary and candidate members **without extraction**; their hashes,
M2 producer commit/tree and byte-identical repository candidate binding match.

Verified bytes were restored only to the three recorded original paths in
fresh owner-only directories, with exclusive/no-follow file creation and mode
0500. SHA-256 and size were rechecked after writing. `go version -m` confirms
original/extension module versions and adopted M2's exact embedded VCS revision;
it does not launch the binaries. No environment binding was retargeted to a
backup path and no old inode/process authority is inferred.

Restoration receipt:
`/home/user/.cache/spec-kitty-parity-resume-followup/executable-restoration.json`.
It preserves two preflight refusals before destination writes (wrong archive
level for manifest, wrong JSON level for candidate source revision). These were
administrative lookup errors, not tests or qualification attempts.

## Fresh live smoke, not release qualification

A once-only dirty-main development check ran after restoration:

```text
TMPDIR=/home/user/.cache/spktmp GOTOOLCHAIN=go1.26.7
SPEC_KITTY_WP13_GAPDBD=/tmp/spec-kitty-admitted-reproduction/bin/gapdbd
 timeout 180s go test -count=1 -v -timeout=150s ./internal/productionruntime
 -run '^TestAcknowledgedPublicationProofProductionOwner$'
```

PASS, 0.206 seconds, zero skips. This exercises fresh admitted startup and
persisted ACK verification across restart using the restored WP13 image.
Log: `/home/user/.cache/spec-kitty-parity-resume-followup/restored-image-live-proof.log`.
All three image hashes and the clean candidate were rechecked afterward.
This is not a full suite, crown-equivalence run, immutable-source attestation,
new admission or transfer of historical qualification to dirty main.

## Configured-origin and finite-contract follow-up

- Corrected repository source reviewed against actual bound8c529 coordinates;
  older unboundd5ab comparison remains explicitly tagged, not substituted baseline.
- Parent full focused unit/race once each: 36 top-level passes, zero skips/failures,
  4.517s/41.006s; vet passes. Scenario hashes unchanged during checks. Independent
  review found no attributable blocking defects and ran all seven repository tests.
- Original failed unit/race logs and corrected fixture/source/hash/log records are
  preserved. Earlier-gate controls aren't credited as configured proof.
- Finite whole-owner count/readback design independently reviewed viable under
  explicit actual-RPC attribution, coherent S1 count, complete fixed metadata and
  pre-copy budgets. No provider API/image inherently required; product seams absent.
- Read-only planner's stage proposal/import-cycle risk assessed in product handoff.
  No arbitrary keys/ports, suggested ceiling changes, implementation or qualification
  accepted merely from planning. No remaining background agents.
- Prior exclusive checkpoint `configured-repository-checkpoint/manifest.json`
  preserves current sources/designs, failed/corrected checks and both exact reviews.
  Verify it and ALL earlier manifests/archives; backups aren't approved patches.
- Main/clean/coordination identities, historical evidence and all three executable
  hashes were rechecked. No source commit, candidate/image/receipt/admission/mission
  mutation, real oracle integration or Op closure.

## Active-goal scalar prerequisite follow-up

- User requested active-goal continuation; goal remains NOT complete. No replacement
  goal or complete-goal status was created.
- Latest scoped source addition: exactly record_footprint.go/test.go; independent
  review verifies1587 prior inventory files unchanged. Eight scalar fields only;
  successful all-role/largecheckpoint calls zeroheap, ZERO failure outputs and
  existing limits preserved. Not census/fullbudget/ACK/seal authentication.
- Parent full core unit/race once84 top /375 results each, focused scenario36 /276
  each, zero fails/skips; vet0. Source hashes unchanged. Five selected architecture
  guards pass once14 outcomes, not fullsuite/qualification. Source reviewed fully.
- Conditional closed attributed-read design independently reviewed viable; exact
  owned wiring/lifetime/revoke/cancel/hash/stricttransport still absent, proxy
  requiresrefusal pending two-hop evidence. Metadata chain and Host count absent.
- Latest `record-footprint-checkpoint/manifest.json` preserves actual code/design /
  independent results/checks, original four-checkpoint chain, images/candidate /
  coordination identities. No source promotion, image/receipt/mission/admission /
  Op mutation. Next minimal kernel/constructor slice requires concrete review.
