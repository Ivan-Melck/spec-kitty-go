# WP03 Review Cycle 1

**Reviewer:** reviewer-renata  
**Candidate:** `7e6d356`  
**Verdict:** REJECT

## Blocking findings

### 1. Reconciliation consequences are queryable labels, not enforced authority

`internal/workgraph/reconcile.go:192` computes consequence booleans and
`internal/controlstore/sqlite/workgraph.go:281` reads them, but no production
authorization path consumes them. The only callers of
`ReconciliationConsequence` are the helper inspection adapter and tests.
`internal/controlstore/sqlite/authority.go:89` still grants authority solely
from the mutable `work_items.state` row.

An independent public-API probe saved a material `invalidate` reconciliation
and then called the existing `execution.Service.Start` against the old
WorkRevision. The command succeeded:

```text
invalidated_old_revision_start_error=<nil> run_id=run-after-invalidate
```

This contradicts FR-017, the Story 7 invalidate outcome, the data-model rule
that active old authority expires, and the WP reviewer guidance to reject
status names without enforced consequences. A newly started run is even less
permissible than allowing an already-active old run to finish.

**Required remediation:** Put reconciliation eligibility on the production
command path. Refuse new authority for superseded/invalidated generations;
make invalidate expire existing active authority and publish quarantine intent
atomically where the current schema permits; ensure handoff/integration gates
can consume the same canonical decision. Add black-box tests proving each
disposition's behavior for active, handed-off, approved, and integrated state,
including negative assertions that forbidden commands cannot advance.

### 2. A finalized graph can be changed outside the protected payload row

The triggers at `internal/controlstore/sqlite/workgraph.go:57` protect only
`work_graph_revisions`. The pre-existing public seed API at
`internal/controlstore/sqlite/authority.go:27` can append any ready WorkID to
the same finalized revision, and `Claim` trusts that appended row. An
independent probe observed:

```text
seed_after_finalize_error=<nil> contains_invented=false
invented_work_start_error=<nil> run_id=run-invented work_id=work-invented
parent_revision_update_error=<nil>
graph_payload_update_error=constraint failed: finalized WorkRevision is immutable (1811)
```

Thus execution authority can be granted for work absent from the canonical
digest, and the numbered `work_revisions.revision_number` can be rewritten
directly. The reconciliation child tables also block UPDATE/DELETE but permit
post-commit INSERTs, so extra fork edges or eligibility consequences can be
appended to an allegedly immutable audit aggregate. The work-graph DDL is
created lazily by `ensureWorkGraphSchema`, rather than by the forward-only
versioned migration surface.

**Required remediation:** Make graph membership and numbered identity
immutable across every authoritative table, close or constrain the seed path
after graph finalization, and prevent post-finalization child-row insertion.
Move this schema into a versioned migration. Add direct-SQL UPDATE, DELETE, and
INSERT mutation tests for parent, payload, membership, edge, and consequence
rows, plus a public-API test proving no command can claim a WorkID absent from
the decoded canonical revision.

### 3. Reconciliation is not replay-safe after a committed/lost response

`SaveReconciliation` checks for an existing identity at
`internal/controlstore/sqlite/workgraph.go:194` and unconditionally returns
`reconciliation_conflict`, even for a byte-identical request. The candidate's
own test at `internal/controlstore/sqlite/workgraph_test.go:137` encodes this as
expected behavior. An independent probe confirmed:

```text
identical_reconciliation_replay_error=reconciliation_conflict: reconciliation identity is immutable
```

This violates NFR-004 and the command contract's idempotency law: the
reconciliation ID is validated as an OperationID, so a crash or dropped
response after commit must return the recorded result for the same semantic
request, while only a changed request may conflict.

**Required remediation:** Persist a canonical request digest/result under the
operation identity and distinguish identical replay from ID reuse with changed
content. Test response loss after commit, exact replay, changed-request
conflict, concurrent identical writers, and restart/reopen replay.

### 4. The helper surface is dead and its diagnostics do not satisfy the contract

`internal/helpercmd/workgraph.go:30` introduces `RunWorkGraph`, but
`internal/helpercmd/root.go:26` never dispatches to it and
`cmd/spk-phase1-helper/main.go:15` invokes only that root dispatcher. Repository
call-site search finds no production caller; the scenario calls
`RunWorkGraph` directly in-process. Finalize and reconcile command handlers are
also absent from the helper surface.

Additionally, `WorkGraphResponse`/`workgraph.Error` expose only
`ok`, `kind`, `message`, and ad-hoc fields. They omit the contract-required
`schema_version`, correlation/operation IDs, aggregate IDs, observed/expected
state, retry safety, and recovery actions. This fails FR-018 and NFR-006.

**Required remediation:** Wire the helper dispatcher to the same capability
commands used in process, without duplicating domain decisions in CLI code.
Return the shared typed result envelope for every refusal and success. Add an
OS-process test against the built helper for finalize/reconcile/inspection,
including exact contract-key assertions and stable invalid-ID, not-found,
revision-conflict, and reconciliation-conflict diagnostics.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — `FinalizeWorkRevision` and
  `ReconcileRevision` are not exposed as command handlers; the common result
  shape and idempotency law do not round-trip.
- `contracts/fault-boundaries.md`: orthogonal to WP03's graph operations (it
  defines SR/HR/IG boundaries), but the WP's NFR-004 replay obligation still
  fails as described above.
- `contracts/provider-contract.md`: **PASS / orthogonal** — no tracker
  implementation or forbidden tracker authority was imported.

## Anti-pattern checklist

1. **Dead code: FAIL.** `RunWorkGraph` has no production caller and cannot be
   reached through the helper binary.
2. **Synthetic-fixture test: PASS.** The graph tests call the new domain/store
   code, though they omit the blocking cross-capability behaviors above.
3. **Silent empty return: PASS.** No silent empty failure path was found in the
   changed production code.
4. **FR coverage: FAIL.** FR-017 enforcement and FR-018/NFR-006 complete typed
   diagnostics are not behaviorally covered because they are not implemented.
5. **Frozen surface: N/A.** No frozen file was modified by candidate
   `7e6d356`.
6. **Locked decision: FAIL.** Exact OperationID replay contradicts the
   command contract's idempotency law.
7. **Shared-file ownership: PASS.** Candidate `7e6d356` modifies only WP03's
   declared owned/create-intent surfaces.
8. **Production fragility: N/A.** No new panic/raise path was added to the
   reviewed production files.

## Verification evidence

Green baseline evidence (necessary but insufficient):

```text
go test ./internal/workgraph ./internal/controlstore/sqlite
go test ./internal/scenario -run Reconciliation
go test ./...
go test -race ./...
go test ./internal/scenario -run Reconciliation -count=100
go test ./internal/workgraph -count=100
go vet ./...
git diff --check 7e6d356^..7e6d356
```

All completed successfully. A broader repeated SQLite run was stopped after
293 seconds because real-WAL setup made it disproportionate; the targeted
100-iteration domain and reconciliation scenario runs completed green. The
blocking probes above used only exported production APIs plus a direct SQLite
connection, were removed after execution, and made no production-code changes.

WP04, WP06, and WP08 depend on WP03 and must rebase after remediation.
