---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T13:23:00Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 review cycle 1 — changes requested

## Blocking findings

### 1. Confirmed replay accepts changed immutable intent

`assignProjection` checks provider, external ID, desired assignee, prior revision,
and marker, then returns a confirmed replay without comparing the recorded
correlation, actor, or process. `compensateProjection` has the same hole. Because
the early terminal return does not call `SaveExternalEffect`, SQLite's stronger
`ON CONFLICT` predicate never runs. A caller can therefore reuse a confirmed
operation with a different `CorrelationID`, `ActorID`, or `ProcessID` and receive
a successful result attributed to the substituted envelope. The portable path
also reduces `ExternalRef` to provider plus external ID and does not preserve or
compare the requested guarantee, so project/work/provenance mapping and guarantee
changes are not rejected as semantic reuse.

Fix both projection replay paths so every immutable intent field is compared
before any terminal return or provider call. Persist a canonical identity for the
complete work mapping and any other semantic fields needed after restart without
weakening the existing SQLite predicate. Add table-driven tests that mutate each
correlation, actor, process, mapping component, desired value, prior observation,
marker, and guarantee independently for pending and terminal effects; every case
must return `KindIdempotencyConflict` and perform zero provider mutations.

Affected code: `internal/execution/external_effect.go:150-158` and
`internal/execution/external_effect.go:221-239`.

### 2. FR-022 / T037 authority evidence is absent

No WP07 test invokes the universal invariant surface or places a provider
failure around real lease, fence, workspace, review, and integration state. The
new 100-iteration test changes one in-memory provider record and repeatedly calls
the adapter; it does not perform 100 independent human-change races, restart the
saga from durable SQLite state, exercise a stale process after lease expiry, or
assert the five authority laws named by T037. The strong fake and Beads refusal
also are not exercised through the same saga contract.

Add the required provider-failure scenarios (timeout before/after apply,
unavailable, malformed, stale revision, wrong marker, partial value/marker,
control-store busy, and lost save). For each relevant boundary, reopen SQLite and
assert the same classification, then assert lease uniqueness, stale-fence
refusal, workspace non-transfer, actor-level reviewer independence, and serialized
integration. Run at least 100 independent human/direct changes between observation
and compensation with zero provider writes. Exercise both the strong fake's
successful guarded compensation and Beads' safe refusal through `TrackerSaga`.
If the existing universal assertion helper is not reachable within the six-file
map, request and document a narrow ownership expansion instead of deferring an
explicit WP07 exit condition to WP08.

### 3. Terminal compensation can persist fabricated provider evidence

`compensateProjection` accepts `safe_compensation_unsupported` or
`attribution_lost` even when the provider returned no coherent observation.
`SaveExternalEffect` then defaults the ledger tuple to the previously applied
revision/value/marker, so a missing fresh observation can be stored as current
provider reality. This contradicts T036's requirement that these terminal states
append the fresh actual observation and that absent/unknown observations remain
zero.

Require a complete, nonzero, internally coherent observation for unsupported and
attribution-lost terminal classifications. Otherwise persist an unknown tuple
under an ambiguity/reconciliation status. Add dishonest-provider tests for both
terminal errors with an empty or partial observation and prove close/reopen never
returns the historical applied tuple as a fresh observation.

Affected code: `internal/execution/external_effect.go:245-273` and
`internal/controlstore/sqlite/external_effect.go:61-75`.

## Additional required corrections

### 4. The adapter path has no live production caller

All production `NewTrackerSaga` call sites still inject the durable fake.
`BoundAdapter` assignment is reached only from tests/interface assertions. Under
the WP anti-pattern gate, this is dead production integration. Wire one bounded
production/helper scenario that constructs the Beads adapter and calls the
provider-neutral saga path, or obtain a documented scope decision that identifies
the exact later WP responsible and changes this WP's acceptance contract.

### 5. Shared files were changed outside the owned-file map, and discovery disagrees

Commit `ff8b7e6` changes `internal/tracker/beads/catalog.go`,
`internal/tracker/beads/inspect.go`, and `internal/tracker/beads/inspect_test.go`,
although WP07 owns exactly six other paths. The commit has no coordination
rationale, and these are WP05/WP04 surfaces. Record an explicit ownership
expansion or move the changes to the owning WP.

The changes also leave two healthy capability-discovery paths contradictory:
`BoundAdapter.Capabilities` reports the accepted catalog read/create/update ports,
while `BoundAdapter.Inspect` reports all catalog ports unsupported and only adds
assignment. Make healthy inspection report the same implemented port set and add
an equality/consistency assertion, while preserving fail-closed behavior for an
incompatible binding.

### 6. Complete the explicit compensation mutant proof

The safe-refusal test checks provider mutation count, but it does not install the
writer-lock spy for compensation and no mutant control replaces refusal with
read-compare-update. Add the required mutant/deletion evidence so the test proves
both zero command starts and zero writer-lock acquisitions when the fresh read
matches.

## Anti-pattern checklist

1. Dead code: **FAIL** — no production caller constructs the Beads assignment saga.
2. Synthetic-fixture test: **PASS** for adapter behavior; saga tests invoke production saga code through a controlled provider.
3. Silent empty return: **PASS** — no unexplained silent-empty path found in the WP production diff.
4. FR coverage: **FAIL** — FR-022, C-012, and NFR-004 are not exercised around provider failure; NFR-005's 100-race requirement is not met by one change plus 100 reads.
5. Frozen surface: **PASS** — no frozen file identified in the mission contracts was changed.
6. Locked decision: **PASS** in the adapter implementation — no unguarded Beads compensation path was found.
7. Shared-file ownership: **FAIL** — three WP04/WP05 files changed without an ownership note.
8. Production fragility: **N/A** — no Go equivalent of an unexplained bare `raise` was introduced.

## Evidence run

- `go test ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite -run 'Assignment|Compensat|ExternalEffect|Human|Invariant' -count=10 -timeout=15m` — pass.
- `go test -race ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite -timeout=15m` — pass (Beads 248.995s; execution 1.042s; SQLite 30.321s).
- `go vet ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite` — pass.
- `staticcheck ./internal/tracker/beads ./internal/execution ./internal/controlstore/sqlite` — pass.
- `go test ./internal/architecture ./internal/tracker/fake -count=1` — pass.
- `git diff --check ff8b7e6^..ff8b7e6` — pass.

The adapter-level impossible invariant itself passes inspection: matching-read
compensation returns `safe_compensation_unsupported`, and changed state returns
`attribution_lost`; neither path calls the writer mutation helper. Approval is
blocked by the replay substitution defect and missing end-to-end authority
evidence above.
