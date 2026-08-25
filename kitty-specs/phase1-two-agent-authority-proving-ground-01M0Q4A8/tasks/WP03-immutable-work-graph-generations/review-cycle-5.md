# WP03 Review Cycle 5

**Reviewer:** reviewer-renata  
**Candidate:** `ff6acec` (review lane HEAD `c0bde50` adds only coordination-status merge data)  
**Verdict:** REJECT

## Blocking findings

### 1. Existing v5 databases never receive the remediation schema

`internal/controlstore/sqlite/migrations.go:14` still declares
`currentSchemaVersion = 5`, while candidate `ff6acec` edits the already-shipped
`workGraphMigrationV5` in place to add `work_graph_operations` and the terminal
work/lease guards at lines 149, 249, and 326. The migration loop at lines
354-375 does nothing when a store is already stamped version 5.

An independent black-box upgrade probe built the `e6950f2` helper, used it to
create a real v5 control database, then opened that database with the
`ff6acec` helper/store. The observed schema remained:

```text
user_version=5
work_graph_operations=0
prevent_terminal_lease_reactivation=0
terminal_canonical_work_item_state=0
```

Calling the new finalize command against that upgraded database returned:

```text
work_revision_conflict: inspect finalization operation replay:
SQL logic error: no such table: work_graph_operations (1)
```

Thus every database exercised by the previous review cycle misses the durable
replay table and the guards that close superseded-lease and done-work revival.
The fresh-database tests cannot detect this because they build current v5 from
scratch.

**Required remediation:** Preserve v5 byte-for-byte and append a forward-only
v6 migration that creates the operation table and installs/replaces the new
triggers for existing stores. Add a real `e6950f2`-v5 -> v6 test that verifies
schema objects, preserves populated revisions/reconciliations/leases/work
state, exercises finalize/reconcile afterward, and proves both direct mutation
guards on the upgraded database.

### 2. Exported legacy save paths bypass the required command identity

The new command methods correctly persist schema version, correlation ID,
OperationID, ActorID, ProcessID, timestamp, digest, and immutable result.
However, the exported production methods `SaveWorkRevision` at
`internal/controlstore/sqlite/workgraph.go:59` and `SaveReconciliation` at line
272 remain unrestricted alternate mutation paths. They call the shared
transaction helpers and commit canonical graph/reconciliation state without a
`work_graph_operations` row.

An independent probe called both exported methods to finalize two revisions
and record an invalidation. The authoritative graph and reconciliation changed
successfully while:

```text
SELECT COUNT(*) FROM work_graph_operations = 0
```

Repository call-site search finds these methods only in tests; WP01 fixture
setup and WP03 scenario/store tests use them to avoid the command envelope.
This is a production fixture escape and contradicts the command contract's
rule that every mutating command carries and persists full identity.

**Required remediation:** Remove or unexport the bypass methods, or require the
same complete command request and operation persistence on every callable
mutation path. Migrate test setup through the canonical finalize/reconcile
commands rather than retaining a test-only escape in production code. Add an
assertion that no canonical revision/reconciliation can commit without its
durable command record.

### 3. Finalize ProcessID identity has a surviving mutant

Current code correctly includes ProcessID in `finalizeWorkRevisionDigest`; an
independent changed-ProcessID replay probe returned `revision_conflict` as
required. But replacing the digest's ProcessID with an empty constant survived
all committed finalize store and helper tests, including concurrent and replay
tests. Existing finalize changed-request coverage changes correlation ID, while
the helper changes ProcessID only for reconcile.

This leaves the newly required finalize process attribution unprotected from a
one-line regression despite the mission's mutation-testing quality directive.

**Required remediation:** Add store and built-helper tests that replay the same
finalize OperationID with a different ProcessID and require a typed conflict;
assert the stored operation actor/process/time/schema/correlation fields as
well as the emitted envelope.

## Remediation that passed independent review

- On a freshly created current-schema database, identical finalize replay is
  durable across reopen, concurrent identical writers return the same immutable
  result, and changed correlation/ProcessID reuse conflicts.
- Finalize and reconcile helper commands require schema version, ActorID,
  ProcessID, and injected timestamp; result/error envelopes emit typed identity
  and reconciliation changed-Process reuse conflicts.
- Direct SQL cannot reactivate invalidated or superseded terminal leases or
  change canonical `done` work back to runnable on fresh current-schema stores.
- SeedAuthority is verification-only, and SeedAuthority/Claim fail closed for
  missing, bare, invented, and cross-generation membership. The lease-insert
  trigger provides a second storage guard.
- Manual mutants disabling lease reactivation, done-state immutability,
  SeedAuthority membership, and Claim membership were all killed by focused
  tests.
- Canonical digests, graph validation, optimistic numbering, all four
  dispositions, terminal invalidation precedence, immutable reconciliation
  evidence/children, and queryable consequences remain green.
- The exact WP02 v4 prefix is unchanged by `ff6acec`. A review-only probe
  populated real v4 project/revision/work rows and verified revision number and
  terminal state survived the current v4 -> v5 path.
- `internal/helpercmd/root.go` is unchanged. Authorized WP01 test edits are
  confined to canonical fixture preparation and preserving the existing busy
  contention assertion; they do not loosen WP01 production authority tests.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — existing v5 stores cannot run
  the new commands, and exported save paths bypass the command envelope and
  idempotency record.
- `contracts/fault-boundaries.md`: **PASS / proportionate** — current-schema
  concurrent/reopen replay and atomic authority tests pass; the migration gap
  is the blocking durable-boundary failure.
- `contracts/provider-contract.md`: **PASS / orthogonal** — no tracker or Git
  authority was imported.

## Anti-pattern checklist

1. **Dead code: FAIL.** Exported `SaveWorkRevision` and `SaveReconciliation`
   have no production callers; tests alone use them as envelope bypasses.
2. **Synthetic-fixture test: PASS.** Tests invoke real store/helper paths, but
   some use the blocking production fixture escape above.
3. **Silent empty return: PASS.** No silent empty failure path was found.
4. **FR coverage: FAIL.** Upgrade behavior is absent, and the finalize
   ProcessID digest mutant survives.
5. **Frozen surface: N/A.** No frozen artifact was modified.
6. **Locked decision: FAIL.** Alternate mutators contradict the every-command
   envelope and idempotency laws.
7. **Shared-file ownership: PASS.** WP01 test edits were explicitly authorized
   and retain their prior behavioral assertions.
8. **Production fragility: N/A.** No new panic/raise path was introduced; the
   typed failure on existing v5 is covered by finding 1.

## Verification evidence

Green candidate-schema evidence:

```text
go test ./internal/workgraph ./internal/controlstore/sqlite
go test ./internal/scenario -run 'Reconciliation|WorkGraphHelper'
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
go test ./internal/workgraph -count=200
go test ./internal/controlstore/sqlite -run '<cycle-3 regression set>' -count=100
go test -race ./internal/controlstore/sqlite -run '<finalize/reconcile concurrency>' -count=20
go test ./internal/scenario -run '<helper finalize/reconcile replay>' -count=3
git diff --check kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8..HEAD
```

All completed successfully. A proposed 50-iteration OS-process repetition was
stopped after 90 seconds as disproportionate; the bounded three-iteration run
completed in 44 seconds. Review-only probes and mutant copies were outside the
candidate and removed from the lane; no production code was modified.

WP04, WP06, and WP08 depend on WP03 and should rebase only after the migration
and command-path gaps are closed.
