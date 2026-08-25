# WP03 Review Cycle 3

**Reviewer:** reviewer-renata  
**Candidate:** `e6950f2` (review lane HEAD `ce0ca0e` adds only coordination-status merge data)  
**Verdict:** REJECT

## Blocking findings

### 1. `FinalizeWorkRevision` violates the command idempotency law

The helper accepts an OperationID at `internal/helpercmd/workgraph.go:67`, but
`runFinalizeWorkRevision` passes only the decoded revision and optimistic
number to `SaveWorkRevision` at line 110. No operation/request digest or
recorded result is persisted by `SaveWorkRevision` at
`internal/controlstore/sqlite/workgraph.go:26`.

An OS-process probe invoked the built helper twice with the same OperationID,
correlation ID, revision JSON, and expected previous revision. The first call
succeeded; the exact replay returned:

```text
ok=false
kind=work_revision_conflict
message="another WorkRevision generation won optimistic creation"
observed_previous_number=1
expected next_revision_number=1
```

The command contract requires every identical OperationID replay to return the
recorded result or resume the same saga; only a changed request may conflict.

**Required remediation:** Persist a canonical finalize request digest and typed
result under the OperationID in the same transaction as revision creation.
Return the recorded result for identical replay, reject changed payload reuse,
and test committed/lost-response replay, reopen replay, and concurrent identical
finalizers through the built helper.

### 2. Superseded lease authority can be reactivated by direct mutation

Migration v5 blocks lease insertion after a non-handoff-eligible reconciliation
at `internal/controlstore/sqlite/migrations.go:279`, but the reactivation guard
at lines 292-296 applies only when a quarantine intent exists. Quarantine
intents are created for invalidate, not supersede.

An adversarial store probe started old-generation work, recorded `supersede`,
changed its lease `active -> expired -> active` with direct SQL, and replayed
the exact Start operation. Both SQL updates succeeded and Claim returned the
run capsule with `Replayed=true`, restoring authority to an expired superseded
generation. This bypasses the insertion guard and makes reconciliation
consequences mutable through lease status.

**Required remediation:** Seal every terminal lease transition against
reactivation whenever current reconciliation policy forbids new authority, not
only invalidation quarantine rows. Add UPDATE mutation tests for supersede and
invalidate plus exact-Start replay assertions after attempted reactivation.

### 3. Canonical work lifecycle and compatibility seed paths remain fail-open

The v5 triggers seal canonical WorkID identity and deletion at
`internal/controlstore/sqlite/migrations.go:221-234`, but do not protect
`work_items.state`. An adversarial probe changed a finalized canonical item
`done -> ready` by direct SQL and `StartRun` granted a new lease. Thus terminal
work can be revived without reconciliation and the executable contract still
mutates outside the immutable graph.

Separately, `SeedAuthority` creates a bare `work_revisions` row and arbitrary
ready membership at `internal/controlstore/sqlite/authority.go:49-79`.
`Claim` enforces canonical membership only when `finalizedGraph == 1` at lines
123-132. A fresh-v5 probe seeded a previously nonexistent revision with an
invented WorkID and Start granted authority. The compatibility seam therefore
remains an alternate authority kernel outside canonical WorkRevision identity.

**Required remediation:** Define and enforce permitted `work_items.state`
transitions in storage, preventing direct terminal-to-ready revival. Restrict
`SeedAuthority` to an explicit migration/fixture mode that cannot be used by
normal v5 authority claims, and make Claim fail closed unless revision and
membership are present in the finalized canonical graph. Mutation-test
`work_items`, base membership, and SeedAuthority/Claim paths, including
invented and cross-generation WorkIDs.

### 4. Mutating helper commands cannot carry the required command envelope

The common helper flags at `internal/helpercmd/workgraph.go:64-69` expose only
database path, OperationID, and correlation ID. Neither `finalize` nor
`reconcile` accepts a schema version or ProcessID. Finalize also has no command
ActorID/injected timestamp fields (those values are only embedded inside a
revision payload), while reconcile has actor/time but still no ProcessID or
schema version.

Although success/failure *result* envelopes now contain the required typed
result keys, the command contract states that every mutating command carries
schema version, correlation/operation identity, ActorID, ProcessID, injected
timestamp, aggregate IDs, and expected aggregate revisions. The helper cannot
round-trip that envelope and cannot attribute a mutation to an actor process.

**Required remediation:** Route a complete shared command value through the Go
handler and helper adapter for finalize/reconcile, validate schema version and
portable ActorID/ProcessID, inject the command timestamp, and add OS-process
tests asserting both accepted envelopes and complete typed refusals.

## Remediation that passed independent review

- Canonical graph and criteria digests are deterministic and
  mutation-sensitive; original and lane-collapse cycles, missing nodes,
  ambiguous IDs, and fork identity/edges are tested.
- Numbered optimistic revision creation has one winner under concurrency.
- Continue, supersede, invalidate, and fork consequences are queryable for all
  four statuses; integrated history is preserved.
- Invalidation expires active authority atomically, records immutable
  quarantine intent, refuses stale exact Start replay, and cannot be revived by
  a later continue reconciliation.
- Finalized graph payload, membership identity, reconciliation evidence,
  consequence children, fork-edge children, and quarantine rows reject the
  candidate's covered direct INSERT/UPDATE/DELETE mutations.
- Reconciliation identical replay is semantic, concurrent, and reopen-safe;
  changed payload reuse conflicts and returns recorded evidence through the
  helper.
- The helper binary reaches finalize, reconcile, evidence, and consequence and
  returns typed result/error envelopes.
- The exact WP02 v4 migration prefix is unchanged. An independent probe seeded
  real v4 project/revision/work rows, opened the candidate store, observed
  `user_version=5`, and verified revision number and terminal work state were
  preserved. Work-graph DDL exists only in migration v5; `ensureWorkGraphSchema`
  now verifies rather than lazily creates it.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — finalize replay is not
  idempotent and mutating helper commands omit required envelope identity.
- `contracts/fault-boundaries.md`: **PASS / proportionate** — atomic invalidate,
  concurrent reconciliation, reopen/replay, and SQLite transaction boundaries
  behaved deterministically in the exercised WP03 seams.
- `contracts/provider-contract.md`: **PASS / orthogonal** — no tracker or Git
  implementation was imported as graph authority.

## Anti-pattern checklist

1. **Dead code: PASS.** The helper root now reaches `RunWorkGraph`; query APIs
   have production consumers where required for Claim/helper behavior.
2. **Synthetic-fixture test: FAIL.** Existing tests miss identical finalize
   replay and preserve an unrestricted SeedAuthority compatibility kernel.
3. **Silent empty return: PASS.** No silent empty failure path was found.
4. **FR coverage: FAIL.** FR-017/NFR-004 authority immutability and replay are
   defeated by state/lease mutation and finalize replay.
5. **Frozen surface: N/A.** No frozen file was changed by `e6950f2`.
6. **Locked decision: FAIL.** The command-envelope and idempotency laws do not
   round-trip.
7. **Shared-file ownership: PASS.** Candidate changes remain within declared
   WP03/shared migration surfaces.
8. **Production fragility: FAIL.** Direct SQLite mutation can revive authority
   that the reconciliation model declares ineligible.

## Verification evidence

Green positive-path evidence:

```text
go test ./internal/workgraph ./internal/controlstore/sqlite
go test ./internal/scenario -run Reconciliation
go test ./...
go test -race ./...
go vet ./...
go test ./internal/scenario -run Reconciliation -count=100
go test ./internal/workgraph -count=100
go test ./internal/workgraph -run '<digest/fork/disposition set>' -count=200
go test ./internal/controlstore/sqlite -run '<revision/reconciliation/mutation set>' -count=50
go test -race ./internal/controlstore/sqlite -run '<optimistic/replay concurrency set>' -count=20
git diff --check kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8..HEAD
```

All completed successfully. Review-only adversarial tests then reproduced the
four blockers and the real-row v4 upgrade described above; those temporary
tests were removed, leaving production and candidate tests unchanged.

WP04, WP06, and WP08 depend on WP03 and should not consume the authority model
until these seams are closed.
