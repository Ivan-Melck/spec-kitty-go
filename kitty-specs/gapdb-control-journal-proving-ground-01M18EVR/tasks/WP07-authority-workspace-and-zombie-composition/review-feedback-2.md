# WP07 review feedback — cycle 2

Verdict: **changes requested**.

Reviewed exact implementation commit `54e2befed184e3d817fadba5ee1a3b7d07978dd7`. The lane later merged mission-status commits; they do not change the reviewed production implementation.

Cycle 2 materially improves the prior submission: expiry and owner restart now publish their complete authority/workspace transitions in one batch; restore consumes a sealed `checkpoint.RestoreResultV1` and performs durable authority/workspace/journal censuses; GJ-03 survives process-memory loss; and an admitted external GapDB plus real Git test round-trips the exact `AuthorityCapsuleV1`. The following blockers remain.

## Blockers

### 1. The retained execution adapter permits actor/process/token impersonation

Locations: `internal/provingground/workspace.go:194-217`.

`executionAuthorityRepository.Heartbeat` and `.Expire` call `find`, but `find` selects a current authority using only `LeaseID` and `Generation`. The adapter ignores the caller's `ActorID`, `ProcessID`, and fencing `Token`, then passes the fully valid *stored* `Authority` into the core. That replaces hostile caller coordinates before `authorizeLocked` can validate them.

A reviewer-only test created Actor A/Process A authority, then submitted syntactically valid retained `execution.HeartbeatRequest` and `execution.ExpireRequest` values using Actor B, Process B, and `wrong-token`, while retaining A's lease ID and generation. Both operations succeeded and durably mutated A's authority.

This violates the WP law that all governed operations validate the complete authority coordinate, FR-033's stale/zombie refusal, and the cycle-1 requirement to compose safely through the retained execution surface. It also means core-level coordinate tests are misleading: the production adapter removes the bad coordinates before reaching the core.

Remediation: resolve and validate by the complete request coordinate (`ActorID`, `ProcessID`, `LeaseID`, generation, token/fence, epoch and applicable run/work identity), never by lease/generation alone. Pass caller evidence through to a core operation that compares it to current authority. Add retained-service mutants for wrong actor, process, token, lease, generation, epoch, and authority-head revision for both heartbeat and expiry.

### 2. Same-operation shortcuts accept changed request content

Locations: `internal/provingground/authority.go:178-185, 692-705, 768-782`; `internal/provingground/workspace.go:259-279`.

The control-journal contract and FR-008 through FR-010 require an operation ID to bind permanently to one request digest. The new in-memory/restored invalidation receipts retain only a subset of the request:

- after a committed expiry, the same operation ID returns success when ActorID, ProcessID, FenceToken, authority-head revision, WorkRevisionID, or authoritative time is changed;
- after a committed owner restart, the same operation ID returns success when the evidentiary `OccurredAt` input is changed; and
- after a committed workspace binding, the same operation ID, lease, and workspace ID returns the old binding while silently accepting changed `RepositoryID`, `LaneID`, `OwnedBranch`, or `PinnedBase`.

Reviewer-only mutants failed for every coordinate above. The workspace case can also produce an internally contradictory `Delivery`: `Runtime.StartWorkspace` writes the changed repository/lane from the retry request into the returned `RunCapsule`, while the durable binding still names the original path/branch/base.

Remediation: persist or reconstruct the canonical request/command digest in every replay receipt and compare it before returning a recorded result. Do not maintain a parallel, weaker definition of idempotency beside the application service. Add changed-digest mutants for Expire, ReplaceRuntime, and GJ-04 replay, including after sealed process reconstruction. Equal operation ID plus unequal digest must fail closed with zero local mutation.

### 3. Concurrent retained claim and owner restart has a data race

Locations: `internal/provingground/workspace.go:158-176`; `internal/provingground/authority.go:857`.

`executionAuthorityRepository.Claim` reads `r.core.epoch` without `epochMu`, while `ReplaceRuntime` writes that field under the lock. A reviewer-only concurrent claim/restart test produced a deterministic race report:

```text
WARNING: DATA RACE
Write: (*service).ReplaceRuntime authority.go:857
Read:  (*executionAuthorityRepository).Claim workspace.go:173
```

This is in the crown-jewel concurrency path and blocks T036/T041 and NFR-003/NFR-007.

Remediation: obtain the epoch through a synchronized snapshot or make the core claim API own the current-epoch selection atomically. Add this exact retained-claim versus owner-restart probe to the package race suite.

### 4. Crown-jewel invalidation remains inaccessible through the public composition

Locations: `internal/provingground/workspace.go:28-122`; `internal/provingground/workspace_test.go:224-383`.

The only exported `Runtime` methods are `StartWorkspace` and `Authorize`. External production callers cannot heartbeat, expire, replace the owner/runtime epoch, reconcile a physical workspace, or obtain a refreshed exact authority capsule after heartbeat. The admitted external GapDB/real Git test performs expiry through package-private `restarted.core.Expire`; owner replacement tests likewise exercise the private core with fake appliers. Therefore the public composition proves start and same-epoch restore, but not the WP's expiry -> quarantine -> replacement -> W3 crown-jewel path.

There is also still no non-test production caller of `NewRuntime`, `RestoreRuntimeValidated`, or `StartWorkspace`; repository-wide call-site search finds only their definitions. That keeps the WP-level dead-code gate failed and repeats the cycle-1 production-caller concern.

Remediation: expose the retained execution lifecycle through a narrow production-callable `Runtime` API that consumes/returns exact frozen capsules and performs owner replacement through an admitted application service. Drive the admitted external GapDB/real Git scenario exclusively through that public surface, including expiry, W1 quarantine, Process C replacement, fresh W3 delivery, and old-capsule refusal. Add the intended live production caller (the WP09 campaign command is acceptable when present, but WP07 cannot claim this composition is live before one exists).

## Cycle-1 closure and evidence that passed

- Atomic expiry: **PASS** — one authority-plus-workspace batch; ambiguous and before-projection retries preserve an unsplit memory view.
- Atomic owner restart: **PASS** — one runtime-plus-authority-plus-workspace batch; workspace ordering is explicitly sorted and work ordering does not range over a map.
- Caller-forged raw projection: **PASS** — projection type is private and production restore requires the checkpoint package's constructor-sealed admission.
- Restore census/post-seal refusal: **PASS** — configured authority, durable head, workspace, and admitted journal censuses are cross-checked; post-seal head revisions refuse.
- GJ-03 after process loss: **PASS** — classification derives from exact Git/reservation facts plus durable binding absence, not an in-memory orphan map.
- Exact admitted GapDB and real Git: **PASS with public-surface limitation** — binary SHA-256 `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`; the real sidecar/Git GJ-04 test passes and preserves primary HEAD/index/tree.
- Zombie POSIX fact: **PASS at private-core level** — the test really writes W1 and the core refuses stale actions; blocker 1 means the retained adapter does not yet preserve that refusal law.
- Focused/repeated tests: `go test ./internal/provingground -count=10` passed.
- Existing race suite: `go test -race ./internal/provingground -count=2` passed; it lacks the claim/restart probe that exposes blocker 3.
- Focused requirement selectors repeated five times passed.
- `go test ./internal/architecture -count=1`, `go vet ./internal/provingground`, `staticcheck ./internal/provingground`, and `git diff --check` passed.

## Contract round-trip

- `contracts/control-journal-v1.md`: **FAIL** — same OperationID with changed content is accepted by local receipt shortcuts rather than producing `operation_identity_collision`.
- `contracts/authority-capsule-v1.schema.json`: **PARTIAL** — exact start/GJ-04 capsule round-trip is real, but no public heartbeat/expiry/replacement surface maintains the capsule lifecycle.
- Other WP09 evidence contracts remain outside WP07, unchanged.

## Anti-pattern checklist

1. Dead code: **FAIL** — no non-test production caller uses the new composition, and the public surface cannot perform the complete lifecycle.
2. Synthetic-fixture test: **PASS with limitation** — the admitted test invokes real application/GapDB/Git start and restore paths, but invalidation crosses a private field.
3. Silent empty return: **PASS** — no unexplained silent empty-return path found.
4. FR coverage: **FAIL** — FR-008/FR-010 idempotency semantics, FR-023 public predecessor invalidation, FR-033 retained-adapter refusal, NFR-002, and the retained concurrency race are not proven.
5. Frozen surface: **PASS** — frozen contract files are unchanged and the exact capsule type is now used.
6. Locked decision: **FAIL** — changed request content reuses an operation ID successfully, contrary to the frozen command-acceptance law.
7. Shared-file ownership: **PASS** — WP07 remains within its six owned proving-ground files; the earlier architecture classification is separately owned and already mutant-tested.
8. Production fragility: **N/A** — no panic-style production addition found; the blockers are authority, idempotency, race, and reachability defects.

WP08 and WP09 remain blocked on a corrected and independently approved WP07.
