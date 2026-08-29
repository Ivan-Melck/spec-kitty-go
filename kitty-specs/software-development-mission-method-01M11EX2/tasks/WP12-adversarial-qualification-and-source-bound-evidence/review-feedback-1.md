# WP12 review feedback — cycle 1

**Verdict:** rejected

**Reviewer:** Reviewer Renata, independent of the implementation process

## Blocker: Phase 3 publication validation is absent

T067 requires permanent, load-bearing validation and mutant controls for the
Phase 3 evidence publication. The current tree contains the older Phase 2
documentation validator, but no Go validator or closed schema for
`docs/phase3-evidence`.

In a disposable clean clone, the reviewer simultaneously broke a Phase 3 link,
made the Mermaid declaration invalid, corrupted `SHA256SUMS`, changed the
qualification verdict from `pass` to `fail`, and relabeled failed attempt 08 as
`pass`. `go test ./internal/testcampaign ./internal/architecture -count=1`
still passed. Direct checksum verification failed, confirming that the mutation
was real but absent from the claimed Go qualification gates.

This violates T067 steps 9–10 and its validation clause. It also overstates the
SC-012 claim in `docs/phase3-evidence/results.md`.

## Required repair

1. Add a strict, reusable Phase 3 publication validator and closed schemas.
2. Add load-bearing mutants for broken links, malformed Mermaid, nonexistent
   commands, claim/test mismatch, missing limitations, checksum changes,
   removed failure history, and falsified pass/fail/non-admissible states.
3. Exercise the validator from clean payload and attestation checkouts.
4. Re-freeze and rerun qualification because validator or serializer changes
   are campaign-product changes.
5. Preserve the source → payload → attestation separation. Unit-test the
   validator with temporary fixture trees so the source does not predict a
   later evidence commit.

## High: machine evidence is incomplete

- `qualification-evidence.json` omits finalization winner/replay/conflict
  counts.
- The nine fault boundaries are aggregated rather than recording every fault ID
  with expected, started, terminal, and verdict fields.
- Projection trial-kind and mutant-result censuses are absent.
- Supporting profiles in `campaign-census.json` have counts and hashes but not
  the exact test identities required by T067.

## Verified positive evidence

- Qualified source `4a96edd13d135497ef0662e25e78d5394cdb32ca` and payload
  `e3f061660535446b5595b1943d9976c909f8d6cd` match Git.
- All four immutable-payload checksums pass unmodified.
- Historical, mission, and real-provider censuses reconcile.
- Failed and non-admissible attempts remain visible.
- Privacy and local-link scans are clean.
- The ratified 29-path scope is exact.
- Focused campaign, scenario, stale-authority, race, vet, staticcheck, and diff
  checks pass.

The repair must retain these properties and issue a new source-bound evidence
chain rather than retroactively extending the prior verdict.
