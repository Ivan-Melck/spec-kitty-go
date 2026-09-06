# Post-approval defect 11: approved scenario retirement was not incorporated

## Finding

WP14 did retire the original SQLite-driven scenario suite in product commit
`fced4f1a`; its approved lane revision `0234a746` contains that commit. The
WP15 lane did not incorporate the approved WP14 lane, however, so its view of
`internal/scenario/**` still imported `internal/controlstore/sqlite`, invoked
the retired helper surface with `--db`, and decoded legacy
`helpercmd.Response` types.

WP15 correctly removes that legacy helper API in favor of the closed native
GapDB command surface. Without the approved dependency commit, `go test ./...`
fails to compile the stale WP14-owned scenario package. Preserving legacy
response types or a test-only SQLite command path would conceal the missing
dependency ancestry and violate the M2 production-authority boundary.

## Required repair

Reopen WP14 only long enough to make the already-delivered retirement a
permanent, mutation-sensitive contract, then incorporate its approved product
history into WP15:

1. retain product commit `fced4f1a` and its accepted portable GapDB behavior
   census and invariant coverage;
2. add an executable guard that fails if any retired scenario filename or
   legacy SQLite/helper dependency returns;
3. merge the repaired WP14 lane into WP15 rather than recreating its types;
4. prove `go test ./internal/scenario` and the repository package graph compile
   against the WP15 native command surface; and
5. record the dependency-incorporation failure so future lane allocation tests
   can reproduce it.

This is a dependency repair, not a compatibility waiver. WP15 must not carry
the removed Python/SQLite-shaped helper protocol forward, and WP16 must not
delete SQLite while a compiled test consumer remains.
