---
affected_files: []
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T22:31:37Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 review feedback — cycle 1

Verdict: **changes requested**. The ordinary suites pass, but adversarial tests
reproduced authority-mixing, attribution, and pre-fence durability failures.

## 1. Blocker — candidate authority can cross work and can be mixed by a same-HandoffID race

Locations: `internal/provingground/review.go:116-130`, `:168-176`, `:254-304`.

`SubmitCandidate` compares the request with capsule coordinates that exist in
the capsule, but `AuthorityCapsuleV1` has no `WorkID`. It never requires
`request.WorkID` to equal the durable `WorkspaceBinding.WorkID`. A deterministic
attack changed a valid W2 request from `work-b` to `work-a`, supplied the
corresponding complete `work-a` eligibility fact and criteria, and the call
succeeded. The durable record was `work-a` + `workspace-b`.

There is also a TOCTOU in `bindCandidateAuthority`. Two valid submissions with
the same `HandoffID` can overwrite the in-memory placeholder between
`InspectHandoffAuthority` and `CommitHandoff`. A two-goroutine test paused both
requests at `HR-01`, let Actor A overwrite Actor B's placeholder, then released
Actor B first. This reproduced 10/10 times and durably published:

```text
candidate record: work-b / workspace-b
stored authority: actor-a / workspace-c
```

The other request correctly received an idempotency conflict, but that does not
repair the already-published mixed record. This breaks immutable handoff,
ActorID-independent review, and later integration authority.

Required correction:

- Resolve the exact durable binding before handoff and require the request's
  `WorkID`, workspace, repository, lane, actor/process, lease, revision, epoch,
  and capsule fields to describe one object.
- Do not use a mutable, unowned HandoffID placeholder as an out-of-band
  authority side channel. Make candidate admission request-scoped or atomically
  reserve an exact immutable `(HandoffID, request digest, authority)` tuple;
  changed/concurrent binds must fail before verification and publication.
- At commit, re-check that the exact authority inspected is the authority being
  journaled.
- Add the cross-work substitution and deterministic `HR-01` race as mutant
  controls.

## 2. Blocker — stale/dishonest provider facts can fabricate attribution, and concurrent exact publication is contradictory

Locations: `internal/provingground/effects.go:146-180`, `:284-349`.

`coherentProviderIdentity` checks only provider, external ID, work ID, the
observation external ID, nonzero revision, and time. It does not validate the
full provider binding or the relation between `ExternalRef.LastObservation` and
`Observation.Revision`. More importantly, the `EffectApplied` branch accepts
the intended value and marker at the **unchanged expected precondition
revision**. Reconciliation of an unresolved GJ-05 intent with revision `1`,
expected revision `1`, desired assignee, and operation marker was classified
`applied`. A stale or dishonest provider can therefore fabricate attribution.

The local exact-operation path also has an unsafe race. One call was paused in
`ProjectAssignment` after publishing intent. A concurrent exact call observed
the still-absent provider state and durably returned terminal `absent`; after
release, the first call applied the assignment and returned terminal `applied`.
The same exact effect request thus produced contradictory terminal results. If
the first process dies after provider application, the durable terminal
`absent` result suppresses later reconciliation of the applied fact.

Required correction:

- Validate the complete provider identity and internal observation coherence.
  Revisions are opaque: use equality/inequality only, but the attributable
  postcondition cannot reuse the precondition revision.
- Make one effect lifecycle linearizable per exact effect/operation across
  concurrent callers. A second caller must not convert an in-flight initial
  dispatch into terminal absence. Preserve the observe-before-retry and
  no-exactly-once laws while making the initial-dispatch/recovery boundary
  explicit.
- Verify compensation response identity and guard guarantee as well as the
  pre-compensation observation.
- Add stale-revision, misbound-project/ref, and concurrent-exact-operation
  mutant controls.

## 3. High — invalid repository coordinates publish an active plan before Git rejects them

Locations: `internal/provingground/integration.go:119-204`, `:320-338`.

`prevalidate` does not require `Request.RepositoryID` and `Request.TargetRef` to
equal the bound `gitrepo.Repository` (or the candidate repository). Separate
tests changed only the repository ID and only the target ref. In both cases the
request failed later in the Git fence adapter, but the workflow had already
published one durable `integration.plan` and retained one active plan authority:

```text
plans=1 active=1
error=invalid_command: integration fence requires an exact repository-first authority intent
```

This violates refusal-before-fence/intent and leaves invalid durable authority
that survives restart.

Required correction: compare the full request against the bound repository,
target ref, candidate repository, WorkRevision, and epoch during prevalidation,
before `integration.plan` publication. Add tests asserting zero plan events,
zero active authority, zero fence refs, and unchanged target for each foreign
coordinate.

## 4. High — restore does not require exact effect-intent membership

Locations: `internal/provingground/effects.go:371-398`, `:500-544`,
`:591-655`.

Live replay validates the complete effect-ID slice, but restore only calls
`containsEffect`. A test reconstructed the accepted intent operation with an
additional valid `effect-extra` member while retaining its revision and all
other bytes. `newEffectCoordinator` restored successfully. Restore therefore
accepts an operation whose durable effect membership is not the command's exact
membership. The same restore surface also validates event membership by
containment rather than a sealed operation/event group and does not enforce the
48 MiB aggregate restore byte budget while scanning up to 32 pages.

Required correction: restore through a sealed exact operation group: request
digest, result, event count/order, exact event IDs, exact effect IDs, intent
bytes/revisions, and aggregate head must agree. Enforce the declared global
event/byte/page budgets rather than only a per-scan page count. Add missing,
extra, reordered, and tampered membership controls.

## 5. High — T046's campaigns and terminal invariant assertions are placeholders

Locations: `internal/provingground/integration.go:485-489`,
`internal/provingground/integration_test.go:372-391`, and WP08 fixtures.

The two named campaign functions are aliases for `RunFocusedCampaign`. Their
only WP08 test increments a counter 100 times. It does not run an attribution /
human-reassignment trial or a competing-integration trial, and proves none of
NFR-004 or NFR-015's zero-violation observations. WP08 tests register only
`assertAuthorityInvariants` cleanup (and `review_test.go` inherits that fixture);
there is no equivalent assertion for effect/journal coherence, candidate /
authority coherence, reviewer independence, singleton integration authority,
or Git publication facts after every terminal path. The attacks above are
exactly the violations those universal assertions should catch.

Required correction: make the focused hooks execute the actual WP08 scenarios
for at least 100 repetitions and assert all applicable universal invariants on
success, refusal, ambiguity, crash, and recovery. Add explicit controls proving
same-contributor review, unsafe compensation, misattributed provider fields,
and false integration completion are killed.

## Anti-pattern checklist

1. **Dead code — FAIL.** `NewEffectCoordinator`, `NewReviewCoordinator`, and
   `NewIntegrationCoordinator` have no non-test callers. If WP10 is intentionally
   the first live composition, this WP's mandatory checklist/exit wording must
   explicitly say so; as reviewed, the new public workflow is test-only.
2. **Synthetic-fixture test — PASS.** Existing behavioral tests invoke the new
   coordinator/service paths rather than asserting literal fixtures.
3. **Silent empty return — PASS.** No silent empty failure return was found.
4. **FR coverage — FAIL.** Functional examples exist, but T046/NFR-004/NFR-015
   are callback-count placeholders rather than the required scenario proofs.
5. **Frozen surface — PASS.** Commit `a9e5f8f` changes only the six WP08-owned
   proving-ground files.
6. **Locked decision — PASS.** No force-ready/force-approve fallback or SQLite
   path was introduced by WP08.
7. **Shared-file ownership — N/A.** No shared production file was changed.
8. **Production fragility — N/A.** No new Go panic/exception path exists in
   production code; the two `panic` calls are test fixtures.

## Passing evidence (not sufficient to override the findings)

- Admitted binary SHA-256:
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- Focused tests including the real external GapDB effect path: pass.
- `go test ./internal/provingground -count=10`: pass.
- `go test -race ./internal/provingground -count=10`: pass.
- Architecture, control-journal, GapDB adapter, Git, handoff, review, and
  integration package tests: pass.
- `go vet`, `staticcheck`, and `git diff --check`: pass.

The adversarial tests were temporary review probes and were removed after
reproduction; the implementation worktree is unchanged except for the existing
`.spec-kitty/` runtime directory.
