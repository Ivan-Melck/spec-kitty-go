# WP06 Review Cycle 1 — Changes Requested

Candidate: `f10a09f6b7f77a2d7ad4399fbe9156ce2185ca3a`

## Verdict

Reject. Initial handoff binding, ActorID-level reviewer independence, immutable
SQLite evidence, and atomic terminal verdict persistence are sound, but a
committed handoff cannot be replayed independently of later branch state and
the helper results do not satisfy the mission's shared command-envelope
contract.

## Blocking issue 1 — committed handoff replay rereads moving authority

`handoff.Service.Submit` calls `InspectHandoffAuthority` and `GitVerifier.Verify`
before `Store.CommitHandoff` reaches its `loadHandoffOperation` replay check.
Consequently, an exact replay after `HR-02` still depends on the current lease,
workspace record, and owned branch head instead of returning the immutable
recorded result.

Independent reproduction:

1. Start a real-Git review candidate and submit a handoff successfully.
2. Commit another file in the same workspace, moving the owned branch away from
   the handed-off candidate.
3. Replay the identical handoff request with the same OperationID, HandoffID,
   candidate, and all other inputs.
4. The helper returns `candidate_mismatch: candidate is not the observed head
   of the owned lane branch`.

Expected: the same semantically identical OperationID returns the already
committed handoff. A changed request using that OperationID returns an explicit
idempotency conflict. The recorded result must remain recoverable after branch
movement, workspace mutation, or lease expiry.

Fix by providing a replay lookup before any live authority/Git inspection. The
stored semantic request digest must be comparable without recomputing mutable
Git verification evidence. Add black-box regressions for `HR-02` lost response
followed by branch movement and lease expiry, plus a changed-payload conflict.

## Blocking issue 2 — handoff/review helper results omit the shared envelope

The command contract requires every success and failure result to carry
`schema_version`, `kind`, `correlation_id`, `operation_id`, relevant
`aggregate_ids`, observed/expected state, retry safety, and recovery actions.
`internal/helpercmd/review.go` instead emits the bare domain `handoff.Record` or
`review.Record` under the generic root `Response`; neither success record has
the required envelope. Failure wrapping includes only operation/actor/process
aggregate IDs even though handoff, review, work revision, work, lease, and
candidate identities are available, and typed conflicts/refusals default to a
generic `never` recovery posture.

Introduce a typed judgment-result envelope analogous to `WorkGraphResult`,
populate all relevant IDs and recovery semantics, and add JSON contract tests
for successful handoff/review, same-actor refusal, stale authority, replay, and
terminal-slot conflict.

## Acceptance assessment

- T024: partial pass — exact full candidate, ancestry, lane, pinned base,
  contributors, WorkRevision identity, and criteria/evidence digests persist
  immutably; committed replay is incorrectly coupled to moving state.
- T025: pass — contributor membership is checked by ActorID, independent of a
  new ProcessID, and valid attempts are actor/process attributed.
- T026: pass — every canonical criterion and the verdict commit in one SQLite
  transaction; one terminal slot, idempotent review replay, explicit conflicts,
  and eligibility/fence rechecks are present.
- T027: partial pass — HR-00 through HR-07, later workspace/branch movement,
  same-actor refusal, distinct-actor approval/rejection, and competing writers
  are exercised, but the HR-02 recovery case omits movement before replay.
- Approval creates no integration or `done` record.
- Direct SQL update/delete attacks are blocked by immutable/sealed triggers.
- Migration coordination is forward-only: shipped v7 is digest-frozen, local
  v8 is a deterministic reservation, and handoff/review schema is independent
  v9. The commit records the required shared-file coordination rationale.

## Anti-pattern checklist

1. Dead code: PASS — new public capability services have live helper callers.
2. Synthetic-fixture test: PASS — scenario tests use production services,
   SQLite, helper processes, and real Git; the added adversarial replay test
   fails against production behavior as expected.
3. Silent empty return: PASS — no undocumented silent-empty error path found.
4. FR coverage: FAIL — immutable/idempotent handoff recovery after a committed
   response loss is not behaviorally covered; the missing case exposes issue 1.
5. Frozen surface: PASS — the v7 migration body remains digest-frozen.
6. Locked decision: PASS — no Git/tracker verdict authority, live-workspace
   review, or approval-to-done path was introduced.
7. Shared-file ownership: PASS — migration, ID, helper dispatch, and schema-test
   crossings are explicitly justified and v8/v9 coordination is documented.
8. Production fragility: PASS — no undocumented bare raise/panic path added.

## Validation evidence

- `go test ./internal/handoff ./internal/review ./internal/controlstore/sqlite -count=1` — PASS
- focused HR/independence/contention scenarios, `-count=3` — PASS
- `go test ./... -count=1` — PASS
- `go test -race ./internal/handoff ./internal/review ./internal/controlstore/sqlite -count=1` — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` (`staticcheck 2026.1`) — PASS
- `git diff --check f10a09f^..f10a09f` — PASS
- adversarial committed-handoff replay after branch movement — FAIL with
  `candidate_mismatch` (temporary test removed; production was not edited)

The WP frontmatter still names `implementer-ivan` rather than a reviewer
profile. That review-handoff oversight is noted; it is already the correct
profile for the requested rework cycle.
