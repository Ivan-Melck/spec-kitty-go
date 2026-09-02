# M1 GapDB Adoption Closeout

M1 is technically complete and the exact qualified GapDB tuple is adopted for
the `production-runtime` program. This decision admits M2; it does not claim
that M2 has already removed SQLite or that a production release exists.

## Bound decision

| Field | Value |
|---|---|
| Decision | `adopt` |
| Signer | `github:lynncoleart` |
| Signature mechanism | `approved-local-reference` |
| Local approval reference | `conversation-approval:2026-09-02T10:46:41Z` |
| Canonical adoption-envelope SHA-256 | `fefefdbbebdd390ce6ce48ccaa6d97fc9b271fedbfdd5180dc82d69c9d35dc55` |
| RunID | `m1-crown-e05a5e4-20260901T224500Z` |
| Qualified source commit | `e05a5e46feb3e65ff42c4544c16678c440ef3818` |
| Qualified source tree | `c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73` |
| Evidence manifest SHA-256 | `397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309` |
| Admitted tuple SHA-256 | `4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d` |
| `gapdbd` SHA-256 | `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e` |

The human approval was supplied in the active session as “yes! that would be
awesome. please continue” in direct response to the proposed exact-evidence
GapDB adoption decision followed by M2. The adoption record binds that local
reference to the canonical envelope digest above. It is not represented as a
detached cryptographic signature.

## Durable evidence

The complete 79-file evidence tree is preserved in the GitHub release
[`m1-gapdb-evidence-v1`](https://github.com/spec-kitty/spec-kitty-redesign/releases/tag/m1-gapdb-evidence-v1),
whose tag targets the qualified source commit.

| Asset | SHA-256 |
|---|---|
| `spec-kitty-m1-crown-e05a5e4.tar.zst` | `86b8cd608f5cd9d1dce7ce25dffa5d87e1df25a233847316416f14e7948a18df` |
| `spec-kitty-m1-crown-e05a5e4.tar.zst.sha256` | Contains the checksum above and the canonical asset name. |

The uploaded asset was downloaded into a fresh directory, its checksum was
verified, it was extracted, and the exact qualified proving binary independently
verified the extracted evidence as `pass` with `adoption_eligible: true` and the
manifest digest shown above.

## Race-gate remediation

`make race-m1` is a bounded, exact test census covering every new M1 package
under Go's race detector. It selects concurrency- or mutation-sensitive tests
and deliberately excludes the separate 2,001-record checkpoint stress case.
The ordinary `race` target depends on `race-m1`, so `make quality-core` and the
hosted quality workflow enforce the M1 race census on every change.

## Boundary after adoption

- M1 evidence and the exact GapDB tuple are accepted.
- M2 may now begin.
- SQLite remains only the historical Phase 1–3 implementation until M2 proves
  its total absence from every production surface.
- There is no authorization for dual write, read fallback, automatic legacy
  import, or a runtime backend-selection mode.
