---
affected_files:
  - internal/controlstore/sqlite/authority_test.go
  - internal/controlstore/sqlite/migrations.go
  - internal/controlstore/sqlite/workgraph.go
  - internal/controlstore/sqlite/workgraph_test.go
  - internal/scenario/claim_race_test.go
  - internal/scenario/reconciliation_test.go
cycle_number: 7
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./... && go test -race ./... && go vet ./... && staticcheck ./...
reviewed_at: '2026-08-24T11:12:36Z'
reviewer_agent: reviewer-renata
verdict: approved
wp_id: WP03
---

# WP03 Review Cycle 7

**Reviewer:** reviewer-renata  
**Candidate:** `c2468c8` (review-lane HEAD `539a3e1` adds coordination-state merge data only)  
**Verdict:** APPROVE

## Findings

No blocking findings remain. Candidate `c2468c8` closes every blocker from
review cycle 5 without changing the root dispatcher or weakening prior WP01
authority assertions.

## Migration and compatibility evidence

- `currentSchemaVersion` is 6. The operation-result table, result indexes,
  canonical-done guard, finalized-membership lease guard, operation
  immutability guards, and terminal-lease reactivation guard are introduced
  only by the forward v6 migration.
- An independent textual extraction and diff proved that the complete
  `workGraphMigrationV5` declaration is byte-identical between shipped commit
  `e6950f2` and candidate `c2468c8`. Both extracted declarations hash to
  `fd2394ae365103eabef87917126add5c700decc33d40be150cbdc9181c3722d9`.
  The committed runtime-string freeze test also passes with its canonical
  digest `7379fcf39fc07c12c2d4e35995a0cf0d99eb1f4fee60287c0d2b2567efca51f3`.
- The populated shipped-v5 upgrade test installs the exact frozen v1-v5
  sequence, records two graph generations, reconciliation evidence and
  consequences, terminal work state, an active run/lease, and the WP02
  external-effect marker, then opens through the production migration path.
  All data survives v5 -> v6; post-upgrade finalize and reconcile commands
  succeed; direct done-work revival and expired-lease reactivation fail.
- The exact WP02 v4 prefix remains intact and the v4 -> v5 -> v6 ordering test
  preserves the WP02 tables. No lazy DDL was found in the work-graph command
  path.

## Command identity, replay, and mutation seals

- The exported production methods `SaveWorkRevision` and
  `SaveReconciliation` are absent. Repository-wide call-site inspection found
  only the public command-aware `FinalizeWorkRevision` and
  `ReconcileWorkRevision` mutation entry points; their unexported transaction
  helpers are called only from those methods. WP01/WP03 fixtures now use the
  same command-aware APIs and every canonical row asserted by the tests has a
  durable operation record.
- Finalize digests include schema version, correlation, ActorID, ProcessID,
  injected timestamp, optimistic revision number, and canonical revision
  bytes. Reconcile digests include the same command identity plus the immutable
  reconciliation request digest. Persisted operation rows retain the full
  command identity and cannot be updated or deleted.
- Identical finalize/reconcile replay is durable across reopen and concurrent
  writers. A changed request with the same OperationID conflicts. A review-only
  `-race` probe repeated 50 times with simultaneous different ProcessIDs: one
  writer won, the other received the typed revision conflict, reopening
  returned the winner's exact result, and the stored ProcessID remained pinned.
  The probe was removed after execution.
- The built helper reaches finalize, reconcile, evidence, and consequence. It
  requires schema version, ActorID, ProcessID, and injected timestamp for
  mutation, emits complete typed result/error envelopes, returns exact replay
  payloads, and reports changed-ProcessID OperationID reuse as a typed conflict.
- SeedAuthority remains verification-only. SeedAuthority and Claim fail closed
  for missing, bare/non-finalized, invented, and cross-generation work. Direct
  SQL cannot append or move finalized membership, revive canonical done work,
  reactivate invalidated/superseded leases, mutate reconciliation evidence or
  child rows, or alter durable command results. A review-only direct-SQL probe
  repeated 100 times confirmed that a fully populated bare run cannot acquire
  a lease without finalized canonical membership; the probe was removed.

## Work-graph semantics

Canonical encoding/digests remain deterministic. Missing and ambiguous IDs,
original lane cycles, lane-collapse cycles, optimistic numbered-generation
conflicts, and typed complete diagnostics are covered and green. Continue,
supersede, invalidate, and fork preserve their distinct status consequences;
fork identity/edges and reconciliation reasons remain queryable. Invalidation
expires active authority, publishes sealed quarantine intent, and cannot be
revived by later continue. Supersede preserves already-running authority but
blocks new starts. Reconciliation evidence, consequences, fork edges, and
quarantine children are sealed against update, delete, and post-commit append.

## Mutation evidence

Isolated candidate copies were mutated and discarded after testing. Focused
tests killed each of these changes:

- omit ProcessID from both command digests;
- alter one byte of shipped migration v5;
- stop schema advancement at version 5;
- disable canonical done-state immutability;
- disable terminal lease reactivation protection; and
- bypass SeedAuthority/Claim finalized-membership checks.

The committed store and built-helper changed-ProcessID assertions specifically
kill the ProcessID digest regression that blocked cycle 5.

## Verification

All of the following completed successfully on the cumulative candidate:

```text
go test ./internal/workgraph ./internal/controlstore/sqlite ./internal/scenario
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
go test ./internal/workgraph -count=100
go test ./internal/controlstore/sqlite -run '<migration/replay/authority regression set>' -count=20
go test ./internal/scenario -run '<helper finalize/reconcile set>' -count=3
go test -race ./internal/controlstore/sqlite -run '<review changed-ProcessID concurrency probe>' -count=50
go test ./internal/controlstore/sqlite -run '<review direct-lease membership probe>' -count=100
git diff --check ff6acec..c2468c8
```

The review-only probe files and isolated mutant tree were removed. The lane has
no reviewer production edits; `.spec-kitty/` is the runtime review lock.

## Contract round-trip

- `contracts/commands-and-results.md`: **PASS** — every mutation carries and
  persists complete identity; replay is immutable and typed.
- `contracts/fault-boundaries.md`: **PASS / proportionate** — transaction,
  concurrent-writer, reopen, migration, and direct-mutation boundaries pass.
- `contracts/provider-contract.md`: **PASS / orthogonal** — no tracker or Git
  authority was imported.

## Anti-pattern checklist

1. **Dead code:** PASS — legacy exported save bypasses are gone.
2. **Synthetic-fixture escape:** PASS — fixtures use command-aware production
   APIs; the upgrade fixture intentionally constructs the exact frozen schema.
3. **Silent empty return:** PASS.
4. **FR coverage:** PASS.
5. **Frozen surface:** PASS — shipped v5 is byte-identical and digest-pinned.
6. **Locked decisions:** PASS.
7. **Shared-file ownership:** PASS — WP01 edits are setup-only.
8. **Production fragility:** PASS — no new untyped failure or panic path found.

Verdict: **APPROVE** candidate `c2468c8`.
