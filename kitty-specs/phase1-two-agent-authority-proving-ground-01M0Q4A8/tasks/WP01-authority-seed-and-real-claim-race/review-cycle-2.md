---
affected_files: []
cycle_number: 2
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T03:56:17Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP01
---

# WP01 review cycle 2 — mini-local

Verdict: **REJECT**

Candidate: `70e600509873a1b85f548875f979532ac14ecdf5`

## Blocking findings

1. **HIGH — A current holder can expire a still-live lease.** `internal/controlstore/sqlite/authority.go:223-248` expires any active row matching lease/generation/token without checking `deadline <= request.Now`. This contradicts the `ExpireRun` contract's `still live` refusal and the plan's `heartbeat deadline passes -> expire` ordering. An isolated black-box review test claimed a one-minute lease and called `Expire` one second later; it failed with `live lease was expired before its deadline`. Add the deadline/observed-generation CAS and a test that proves early expiry is refused while due expiry succeeds.

2. **HIGH — Claims can cross project/mission authority scopes.** `internal/controlstore/sqlite/authority.go:71-81` validates only `(work_id, work_revision_id)` state, while `:106-112` persists the caller-supplied ProjectID/MissionID. The schema has no composite relation tying those values to the selected WorkRevision. An isolated test seeded projects A/B, then claimed A's work/revision with B's project/mission; the claim succeeded. Bind the claim lookup/foreign keys to the owning project and mission and reject mismatches with a typed diagnostic.

3. **HIGH — Refusal diagnostics do not implement NFR-006/the command contract.** `internal/execution/model.go:22-29` exposes only kind, message, a boolean `retry_safe`, aggregate IDs, and recovery actions. It omits schema version, correlation ID, top-level OperationID, observed state, expected state, and the required typed retry classification. `internal/scenario/claim_race_test.go:136-140` asserts only kind, one aggregate ID, boolean retry safety, and a non-empty action, so it cannot catch the missing fields. Implement the common result envelope and assert every required field for each loser, idempotency conflict, stale fence, and busy refusal.

4. **HIGH — The migration surface is not versioned or forward-only.** `internal/controlstore/sqlite/migrations.go:3-86` is one unconditional `CREATE IF NOT EXISTS` script ending in `PRAGMA user_version = 1`; `internal/controlstore/sqlite/db.go:39-41` executes it on every open. An isolated test created `user_version=99`; `Open` silently reset it to 1 and succeeded. Implement ordered migrations with current-version inspection, transactional application, and refusal of unknown future versions; test fresh, idempotent reopen, upgrade, and future-version refusal.

5. **HIGH — Bearer fencing tokens are persisted verbatim rather than as the data-model's token digest.** `internal/controlstore/sqlite/migrations.go:41,69` stores raw result/lease tokens and `internal/controlstore/sqlite/authority.go:112-119` writes them directly. An isolated database query recovered the exact `bearer-token-a` from both tables. Persist and compare a one-way digest at the authority boundary while preserving the specified durable replay behavior through an explicitly designed safe result mechanism.

6. **MEDIUM — Typed ID validation is dead and bypassed by the real helper.** All `Parse*ID` functions in `internal/ids/ids.go:49-73` have zero non-test callers. `internal/helpercmd/root.go:54-58` casts flags directly, and `StartRequest.Validate` checks only non-emptiness. An isolated test showed `WorkID("has/slash")` is accepted. Parse at the transport boundary (or centralize equivalent validation), cover all ID kinds, and keep invalid values out of filesystem and persistence paths.

7. **MEDIUM — FR-019/error-path evidence is incomplete.** The only production hook call is `SR-00` at `internal/execution/lease.go:71`; `internal/faults` and `internal/helpercmd` have no tests, including the recognizable crash-exit path. WP01 references FR-019 and T004 requires process-safe block/crash controls. Add real helper-process crash/block tests and either implement the durable boundaries owned by this slice or narrow the WP's requirement reference explicitly. Mutation checks did confirm the existing fencing, replay, and busy tests fail when their corresponding predicates/translations are removed.

8. **MEDIUM — The race does not exercise the winning token as authority.** `internal/scenario/claim_race_test.go:126-150` checks one success, a non-empty token, and one `status='active'` row, but never performs a fenced mutation with the winner capsule or proves non-winner credentials are rejected. The reviewer guidance explicitly requires one valid lease, not merely one success response. Heartbeat through the same service with the winner token and assert a stale/wrong token cannot mutate.

9. **LOW — Production panic has no fail-loud rationale.** `internal/execution/lease.go:87-92` panics on entropy failure. Return a typed initialization/command failure, or document why process termination is the intended contract.

## WP anti-pattern checklist

1. Dead code — **FAIL**: `Parse*ID` has zero production callers; service heartbeat/expiry are not consumed by the helper/scenario command path.
2. Synthetic-fixture tests — **PASS**: authority/race assertions invoke the real store/service/helper paths.
3. Silent empty return — **PASS**: no unexplained silent empty failure return was found in the changed Go paths.
4. FR coverage — **FAIL**: NFR-006 and FR-019 are not behaviorally covered; the real transport bypasses FR-001 parsing.
5. Frozen surface — **N/A**: no frozen/untouchable WP surface was identified or modified by candidate commit `70e6005`.
6. Locked decision — **PASS**: no tracker/Git-worktree behavior or prohibited production integration was added.
7. Shared-file ownership — **N/A**: runtime topology reports WP01 owns lane-a alone; candidate production files remain within `owned_files`.
8. Production fragility — **FAIL**: undocumented production panic at `internal/execution/lease.go:90`.

## Validation evidence

- PASS: `go test ./internal/ids ./internal/controlstore/sqlite ./internal/execution`
- PASS: `go test ./internal/scenario -run ClaimRace -count=100` (`105.975s`)
- PASS: `go test ./internal/scenario -run 'WP01(ContentionCeiling|LostResponseReplay)$' -count=10` (`51.171s`)
- PASS: `go test -race -count=1 ./...` (non-cached)
- PASS: `go test ./...`
- PASS: `go vet ./...`
- PASS: `staticcheck ./...`
- PASS mutation/deletion sensitivity: removing the heartbeat token predicate failed `TestHeartbeatAndExpiryRequireCurrentGenerationAndToken`; bypassing replay failed `TestClaimIsIdempotentAndFenced`; removing busy translation failed `TestWriterContentionReturnsTypedBusyAfterBoundedWait`.
- FAIL as expected in isolated candidate archive: adversarial tests for early expiry, raw token persistence, cross-scope claims, invalid ID lexemes, and unknown future schema versions.

## Governance notes

- WP frontmatter still names `implementer-ivan`; the independent review applied resolved `reviewer-renata` as assigned.
- Generated charter fetches for `section:code-review-checklist` and `section:terminology-canon` returned `No charter section found`; the embedded runtime review checklist and mission contracts were applied.
