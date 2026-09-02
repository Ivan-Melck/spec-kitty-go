# WP01 Post-Approval Defect 2 Review — Changes Requested

Reviewed commit: `4919caf6e216e07cf0f6a4986178a861d7c938ec`

## Blocker: foundation-ownership enforcement is still callable-name based

The new ownership inspection limits adapter signature analysis to five exact
method names. Its renamed-adapter mutant changes the DTO name but leaves the
method named `ReadV2`, so it does not prove the semantic rename-resistance law.

An independent overlay probe retained the valid foundation-typed `ReadV2` seam
and added a semantically equivalent side seam named `Fetch` returning an
adapter-owned `Parcel`. `inspectM2FoundationContractOwnership` returned success.
The adapter can therefore expose a second recovery/publication contract merely
by renaming both the callable and the DTO.

Evidence: `internal/architecture/m2_registry_test.go`, callable filter near
lines 583–587 and renamed fixture near lines 684–693 in the reviewed commit.

## Required bounded repair

1. Replace the exact callable-name whitelist with structural inspection of
   adapter-facing recovery/publication signatures.
2. Reject adapter/GapDB-owned parameter or result types whenever a callable
   participates in foundation recovery/publication vocabulary, regardless of
   callable or DTO spelling.
3. Preserve legitimate internal adapter helpers and unrelated public adapter
   operations; avoid a blanket exported-method ban.
4. Add the exact stronger mutant: keep a valid foundation-owned `ReadV2` and
   introduce renamed `Fetch`/`Parcel` beside it. Bind that mutant into the
   executed census and update the exact count/digest/deletion controls.
5. Preserve the global head key, operation 2/2 registry identity, dependency
   direction, capability reachability, and all prior mutant controls.

## Passing scope

The global aggregate-head spelling and scope-first rejection, exact operation
2/2 tuple and complete registry/keyspace identities, foundation and playback
GapDB-import denials, direct/wrapped capability reachability guard, and the
existing 102 executed mutant cases all passed. Focused x3, race, complete
architecture, helper architecture-check, repository compile, vet, staticcheck,
formatting, and diff gates passed. Temporary probes were removed.
