# WP06 Remediation Review — Changes Requested

Candidate: `586f10c03bf0ec94c51f5d5949eb4ef24e974218`

## Verdict

Reject on one remaining NFR-006/helper-contract defect. The durable replay
ordering and successful judgment-envelope findings from review cycle 2 are
fixed.

## Blocking issue — malformed criteria bypasses the complete judgment context

`runReview` constructs a fully identified `review.Request`, but a
`criteria-json` decode failure calls the legacy `encodeJudgmentInvalid` helper.
That helper retains only OperationID, ActorID, and ProcessID. Valid ReviewID,
HandoffID, WorkRevisionID, WorkID, LeaseID/generation, and candidate values are
dropped even though they were parsed and are applicable to the refusal.

Black-box reproduction against the candidate helper used a complete review
command with `--criteria-json not-json`. The response was:

```json
{"ok":false,"error":{"schema_version":1,"kind":"invalid_command","message":"invalid judgment command","correlation_id":"correlation-malformed","operation_id":"operation-malformed","aggregate_ids":{"actor_id":"actor-reviewer","operation_id":"operation-malformed","process_id":"process-reviewer"},"observed_state":{"command":"rejected","error_kind":"invalid_command"},"expected_state":{"command":"accepted under current immutable authority"},"retry_safety":"never","recovery_actions":["correct the command or state before retrying"],"occurred_at":"2026-08-24T12:00:40Z"}}
```

Expected: the diagnostic preserves every applicable identity from the parsed
command, including `review_id`, `handoff_id`, `work_revision_id`, `work_id`,
`lease_id`, `lease_generation`, and `candidate`, while retaining the typed
observed/expected state, retry policy, recovery action, and submitted time.

Route decode failures through `reviewJudgmentContext(request)` (and preserve
the corresponding raw/constructed handoff context for parse failures where
identities are available). Strengthen
`TestReviewFlagFailureEmitsTypedEnvelope` with a fully populated malformed
command and assertions for all applicable aggregate IDs, not only actor and
operation.

## Prior finding closure

- PASS: Handoff and review operation lookup plus semantic request-digest
  comparison now precede live Git, lease, revision, criterion, and HR probes.
- PASS: Identical handoff replay after branch/workspace movement, HR-02 lost
  response, and lease expiry returns the byte-identical immutable record.
- PASS: Identical review replay after verdict commit and lease expiry returns
  the byte-identical immutable judgment.
- PASS: Changed candidate, ActorID, and ProcessID requests using an occupied
  OperationID return typed `idempotency_conflict`.
- PASS: Successful handoff/review records appear only inside the complete
  shared `judgment` result envelope.

## Acceptance assessment

- T024: PASS — exact candidate, ancestry, lane/base, authority, contributors,
  revision identity, and criteria/evidence digests are immutable and replayable.
- T025: PASS — ActorID-level independence and actor/process attempt attribution
  remain enforced.
- T026: PASS — complete canonical criteria and terminal verdict commit
  atomically with slot conflicts, replay, and eligibility rechecks.
- T027: PASS — HR-00 through HR-07, movement, expiry, same-actor refusal,
  distinct approval/rejection, and competing writers are exercised.
- NFR-006/helper contract: FAIL on the malformed-criteria path described above.
- PASS: approval creates neither integration nor `done` state.
- PASS: direct SQL immutable/sealed triggers remain effective.
- PASS: shipped v7 remains frozen, local v8 remains the documented
  deterministic reservation, and WP06 remains independent v9.

## Anti-pattern checklist

1. Dead code: PASS — new lookup and judgment paths have production callers.
2. Synthetic-fixture test: PASS — replay/HR scenarios invoke production helper,
   SQLite, and real Git paths.
3. Silent empty return: PASS — no undocumented silent-empty path found.
4. FR coverage: PASS — every referenced functional requirement has behavioral
   coverage; the blocker is the NFR-006 diagnostic contract.
5. Frozen surface: PASS — no migration change in the remediation; v7 remains
   digest-frozen.
6. Locked decision: PASS — no moving-workspace review, Git verdict authority,
   partial criterion commit, or approval-to-done path was introduced.
7. Shared-file ownership: PASS — the small helper root change is required by
   the shared judgment envelope and remains within the documented WP06 crossing.
8. Production fragility: PASS — no undocumented fail-loud panic path added.

## Validation evidence

- focused handoff/review/SQLite/helper tests — PASS
- `go test ./internal/scenario -run Review -count=5` — PASS
- HR-00 through HR-07 replay matrix, `-count=2` — PASS
- `go test ./... -count=1` — PASS
- focused `go test -race` over handoff/review/SQLite/helper — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` — PASS
- `git diff --check 586f10c^..586f10c` — PASS
- candidate/ActorID/ProcessID request mutants — PASS with explicit conflicts
- fully identified malformed-criteria command — FAIL: applicable aggregate IDs
  omitted as reproduced above

The WP frontmatter still names `implementer-ivan`; it is already correct for
the requested rework cycle.
