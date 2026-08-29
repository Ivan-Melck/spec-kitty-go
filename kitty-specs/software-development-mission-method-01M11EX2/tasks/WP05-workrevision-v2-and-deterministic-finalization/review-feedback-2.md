# WP05 Review Feedback — Round 2

Verdict: changes requested.

## Issue 1 — Replay still cannot prove that it returned the originally committed complete revision (blocker)

Cycle 2 correctly validates command-bound revision identity, the complete
mission outcome, and request digest on replay. However, the found-replay path
at `internal/mission/finalization.go:114-122` passes `replay.Revision` as both
the expected candidate and the result. The byte comparison at
`internal/mission/finalization.go:223-232` therefore compares the replayed
revision to itself.

An independent production-path probe demonstrated the remaining hole:

1. Finalize a normal two-item generation and retain the committed operation
   record.
2. Encode its `RevisionV2` and change only one pinned provider observation's
   opaque revision from `revision-1` to `revision-substituted`.
3. Decode the changed bytes as a valid `RevisionV2`. This is intentionally
   possible because opaque provider revision noise does not alter semantic
   graph or criteria digests.
4. Substitute that revision in the same operation record while preserving the
   WorkRevision ID, every command-bound identity, mission outcome/current
   pointer, state version, operation/request digest, and all other fields.
5. Replay the command.

`Finalize` returned success without provider calls. The replay-before-provider
ordering is correct, but the returned audit evidence is not the complete
revision originally committed. The permanent "wrong revision" mutant changes
the WorkRevision ID, so it does not kill this same-ID complete-content
substitution.

This leaves T027 step 6, T028 steps 3/6, and the definition-of-done requirement
that exact replay return the original complete revision unenforced. It also
means a future WP08 adapter has no explicit value with which to atomically bind
the operation record to the exact result bytes.

### Narrow contract shape that satisfies the law

Add a typed, domain-separated full-outcome commitment/receipt, distinct from
the semantic graph and criteria digests. Its canonical hash material should
include at least:

- exact `RevisionV2.Encode()` bytes, including opaque provider observations;
- the complete resulting `MissionSnapshot`;
- mission ID, operation ID, and request digest.

Exclude transport-only `Replayed` and the receipt field itself. One suitable
framing is a versioned domain prefix plus canonical JSON for those values.

The service should compute the expected receipt when it has the candidate and
deterministic mission outcome, pass it through `FinalizationCommit`, and require
the publication result to match it. `FinalizationStore` must atomically persist
the operation identity, request digest, complete outcome, and receipt. A replay
returns the persisted receipt; the service recomputes it solely from the
replayed result and compares before returning success.

This keeps replay before all provider observation, detects mutation of pinned
revision evidence (or any other outcome field) alone, and gives WP08 an explicit
atomic persistence contract. Arbitrary simultaneous corruption of both outcome
and receipt is outside the trusted-store model; the adapter must not regenerate
the receipt from possibly changed state during replay.

Required regression evidence:

- same-ID opaque provider-revision substitution is rejected as bounded
  `persistence_contract` with zero provider and publish calls on replay;
- same-ID semantic revision substitution, mission-outcome substitution, missing
  receipt, altered receipt, and request/operation binding mutants are rejected;
- exact committed replay still succeeds before provider observation;
- lost-response replay returns the original receipt and complete outcome;
- concurrent same-operation callers converge on one atomically bound outcome
  and receipt under the race detector.

## Confirmed cycle-1 repairs

The other four cycle-1 findings are closed:

- v2 decoding rejects missing/`null`/wrong-type scalar fields independently of
  digest mismatch, including false-valued `required`, optional design context,
  and predecessor controls;
- v1 dispatch rejects recursively incomplete nested values, noncanonical bytes,
  and explicit empty `parent_id`; every accepted canonical value re-encodes
  byte-for-byte;
- unsupported mission-definition versions 0, 2, and 99 refuse before replay,
  provider observation, or publication;
- duplicate `(provider, external_id)` mappings are rejected by both raw
  `FinalizationObservation` and `RevisionV2`, while equal opaque revisions on
  distinct mappings remain legal.

## Validation evidence

- focused workgraph and mission suites at `-count=1` and `-count=20` — pass;
- all five permanent cycle-1 regression groups at `-count=20` — pass as written;
- workgraph and mission race suites — pass;
- same- and distinct-operation 100-way concurrency tests under race at
  `-count=10` — pass;
- full architecture package — pass;
- exact Phase-3 direction and deletion-mutant campaign at `-count=10` — pass;
- repository-wide internal compile-only test — pass;
- `go vet`, `staticcheck`, `go mod verify`, formatting, and diff hygiene — pass.

The scenario exception remains non-attributable. Product commit `a9e9760`
changes no `internal/scenario` source, and that directory has the same tree
object before and after the commit
(`cf52671e1bbbead61b728bf110d77847424e4e12`). The known exact census failure
reproduces; isolated
`TestPhase1NamedFaultBoundaryMatrix/integration/IG-02` passes in 14.27 seconds.

Product scope is clean: `a9e9760` changes eight WP05-owned files only, atop the
original ten-file product commit `16b938a`. `c01a479` is a separate status
projection cleanup. All temporary reviewer probes were removed.

## Anti-pattern checklist

1. Dead code: **PASS with planned seam** — workgraph finalization has a live
   mission caller; the finalization store/service surface is explicitly staged
   for WP08 by this WP's contract.
2. Synthetic-fixture test: **FAIL** — the replay tests exercise production code,
   but their "wrong revision" fixture changes only the outer WorkRevision ID and
   does not prove original complete-content replay.
3. Silent empty return: **PASS** — no undocumented silent empty failure path was
   found.
4. FR coverage: **FAIL** — exact complete replay under FR-014/FR-015 remains
   unproven for same-ID revision-content substitution.
5. Frozen surface: **PASS** — v1 behavior is preserved and its accepted encoding
   is now byte-canonical.
6. Locked decision: **PASS** — provider-neutrality, generation-1 authority,
   opaque-revision separation, and replay-before-provider remain intact.
7. Shared-file ownership: **PASS** — product changes are confined to WP05-owned
   files and topology cleanup is separate.
8. Production fragility: **PASS** — no panic/fail-fast transient path or secret
   leakage was introduced.
