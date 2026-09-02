---
affected_files: []
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T16:08:14Z'
reviewer_agent: reviewer-renata-wp06
wp_id: WP06
---

# WP06 review cycle 1 — changes requested

Reviewer: `reviewer-renata-wp06` (`reviewer-renata`)

Implementation: `928a082`

Handoff: `385907b`

## Verdict

REJECT. The focused implementation, race, static-analysis, architecture, and
formatting gates pass, but the current public seams cannot satisfy the recovery,
zero-I/O, frozen-contract, and cross-WP authority requirements.

## Blocking findings

### 1. A pristine journal cannot restore or admit its first runtime epoch

`RestoreV1` rejects an otherwise valid, bounded empty journal because
`TerminalWatermark.EventCount == 0`, then `deriveAuthority` also requires a
pre-existing epoch and durable revision
(`internal/controljournal/checkpoint/restore.go:77-98`).
`BindRuntimeEpochV1` independently requires `predecessor` to be a valid,
nonempty `RuntimeEpochID` (`restore.go:289-311`). This makes first startup
circular: restore cannot become ready before an epoch exists, while epoch
binding is correctly prohibited until restore is ready. T031 explicitly
requires the empty-prefix path, and `runtime-current-v1.schema.json` makes
`predecessor_runtime_epoch_id` optional.

Remediation:

- Define and implement an explicit pristine-store restore result: ready empty
  projection, zero watermark, no predecessor authority, and closed command
  admission until the first epoch is durably admitted.
- Add a distinct, unambiguous first-epoch binding path (or equivalent optional
  predecessor semantics) while retaining strict predecessor inequality on
  restarts.
- Add tests that exercise `RestoreV1` and first-epoch binding over a truly empty
  page; do not substitute a fabricated checkpoint.

### 2. Invalid checkpoint candidates bypass the required bounded-full-replay fallback

When `CheckpointHeadsV1` returns any candidates, `RestoreV1` tries only those
candidates and returns `invalid_checkpoint` after the loop
(`restore.go:101-141`). It never scans from the zero watermark. This contradicts
FR-020 and the frozen control-journal contract: newest valid checkpoint plus
suffix, then bounded full replay, otherwise closed. The current
`TestRestoreExplicitEarlierFallbackAndAllInvalidRefusal` encodes the wrong
expectation for a bounded, intact full journal.

Remediation:

- After every explicit checkpoint candidate is rejected, attempt exactly one
  bounded full-journal replay from the zero watermark.
- Preserve invalid candidates as evidence and never treat an empty scan as an
  implicit replacement for known checkpoint history.
- Test both outcomes: intact bounded full history succeeds; absent/corrupt or
  over-bound full history remains non-ready.

### 3. Playback's zero-I/O observation is not true by construction

`playback.InputV1` accepts `reducer.RegistryV1`, and that registry stores
arbitrary caller-provided `ReducerFuncV1` closures
(`internal/controljournal/reducer/reducer.go:35-46,58-75`). `RunV1` invokes the
closure and then hard-codes `AdapterCalls: 0` and `DurableWrites: 0`
(`internal/controljournal/playback/playback.go:17-20,59-64`). A reducer can
capture and call a filesystem, network, adapter, or writer capability while the
reported counters remain zero. The AST import test proves only that
`playback.go` has no direct side-effecting import; it does not prove the invoked
reducer is pure. This violates FR-024/NFR-006 and the WP requirement that zero
I/O be backed by construction rather than convention.

Remediation:

- Coordinate with the reducer contract owner to make the playback reducer
  input a closed, version-pinned, structurally side-effect-free capability; an
  exported constructor accepting arbitrary Go functions is not such a
  capability.
- Add a mutant using a reducer closure that attempts an observable external
  call/write. The structural gate must prevent it from being representable or
  executable, rather than merely leaving the counters at zero.

### 4. `BuildV1` does not mint an unforgeable checkpoint-publication capability

`BuildResultV1` exposes mutable `Record`, `Head`, `RecordBytes`, and `HeadBytes`
fields (`internal/controljournal/checkpoint/checkpoint.go:37-42,114`). A caller
can mutate them after `BuildV1`, or bypass `BuildV1` and construct a
self-consistent record/head directly. The 16,001-prefix test does exactly that:
it invents `event_prefix_digest`, projection, authority, record bytes, and head
without reducing a sealed prior prefix, then `RestoreV1` accepts it
(`restore_test.go:122-150`). That test proves suffix-only scanning, but it is a
synthetic fixture for checkpoint provenance and cannot prove T030/T031.

This also leaves WP05 without a safe prior-prefix input: ordinary encode/decode
validation proves self-consistency, not that `BuildV1` reduced the sealed prior
history or excluded the forming batch.

Remediation:

- Have `BuildV1` mint a sealed/copy-on-access `ValidatedCheckpointV1` (name is
  illustrative) whose private seal binds canonical record/head bytes and the
  prior-prefix/current-batch proof.
- Make application publication consume that verified capability and return
  defensive copies; zero/manual/tampered/post-build-mutated values must refuse
  before batch construction.
- Replace the synthetic 16,001-prefix proof with a fixture whose checkpoint is
  produced by the real build path, or narrowly document why an already
  qualified immutable checkpoint capability is the input.

### 5. Reducer/schema digests are labels, not bindings to the supplied registries

`SourceIdentityV1` accepts arbitrary nonzero `ReducerSetDigest` and
`EventSchemaSetDigest`. Neither `BuildV1` nor `RestoreV1` compares those values
with the actual `reducer.RegistryV1` or `EventRegistryV1`. On a checkpoint-only
path, a different valid registry can therefore be supplied while the checkpoint
is accepted under the caller-provided original labels. This violates the
version-set and compatibility checks required by T030/T031, FR-020, and the
checkpoint contract.

Remediation:

- Give the sealed event and reducer registries canonical, validated set digests
  and compare them with `SourceIdentityV1` before checkpoint build or restore.
- Add checkpoint-only wrong-event-registry and wrong-reducer-registry mutants;
  both must refuse before readiness.

### 6. The ratified 8 MiB per-page budget is not enforced

The product budget is 500 records and 8 MiB per scan page, with 48 MiB total.
`scanJournal` enforces the record count and only the 48 MiB cumulative byte
limit (`restore.go:177-206`). `TestRestoreBoundExactAndPlusOneBeforeAppend`
explicitly declares one page with `RawBytes = MaxRestoreBytes` (48 MiB) and
expects it to pass (`restore_test.go:239-252`). This contradicts the pinned
budget and NFR-012.

Remediation:

- Add and enforce the 8 MiB per-page limit before accepting/copying page
  records, independently of the 48 MiB total limit.
- Test exact 8 MiB and 8 MiB plus one per page, plus exact/plus-one total.

### 7. `AuthorityCapsuleV1` conflicts with the frozen authority-capsule contract

The new `checkpoint.AuthorityCapsuleV1` contains only `RunID`,
`RuntimeEpochID`, and `DurableRevision` (`model.go:97-101`). The in-scope frozen
`contracts/authority-capsule-v1.schema.json` requires thirteen fields, including
project, mission, work revision, actor, process, lease, workspace, generation,
fence, and `authority_head_record_revision`. The local type neither round-trips
that schema nor clearly identifies itself as a narrow epoch coordinate.

Remediation:

- Consume the canonical authority capsule contract/type when it exists, or
  rename this value to an explicitly narrow epoch-comparison coordinate and
  prevent it from being mistaken for the frozen capsule.
- Add a contract mirror/round-trip assertion for the canonical capsule's closed
  field set at its owning boundary.

### 8. Required quantitative recovery evidence is missing

The suite has three restore/playback comparisons, one epoch replacement, and no
successful 2,001-record multi-page `RestoreV1` run. The playback test's 100
iterations compare playback with playback, not restore with playback. The
16,000-record scan-bound test uses duplicate records and never validates or
reduces them through `RestoreV1`. Consequently NFR-005, NFR-007, and NFR-010 are
not established by the named WP tests.

Remediation:

- Add at least 100 genuine restore-versus-playback comparisons over valid
  deterministic histories.
- Add 100 clean/unclean restart bindings proving unique new epochs and old
  capsule refusal, including first startup separately.
- Add a valid 2,001+ record multi-page restore with no missing/duplicate events,
  plus a stale-cursor restart that repeats the complete traversal.

## Contract round-trip disposition

- `checkpoint-v1.schema.json`: wire field names and basic canonical encoding
  are exercised, but semantic provenance/version bindings fail findings 4 and
  5.
- `control-journal-v1.md`: in scope; bounded-full-replay precedence and
  structurally zero-I/O playback fail findings 2 and 3.
- `authority-capsule-v1.schema.json`: in scope; closed-set shape conflicts with
  the new identically named type (finding 7).
- `runtime-current-v1.schema.json`: in scope for epoch predecessor semantics;
  its optional predecessor confirms the missing first-start case (finding 1).
- Adoption, evidence, sidecar, operation, event, and ordered-trial contract
  files are orthogonal to WP06's owned symbols in this cycle.

## Anti-pattern checklist

1. **Dead code — N/A (staged foundation), with a downstream closure condition.**
   There are currently no non-test production callers of the new packages.
   WP07/WP09 are declared dependents, so this is treated as staged foundation,
   but the sealed checkpoint capability must gain a real application/composition
   caller before those dependents can pass review.
2. **Synthetic-fixture test — FAIL.** The 16,001-prefix test fabricates the
   checkpoint provenance it claims to exercise.
3. **Silent empty return — PASS.** No silent empty-success branch was found;
   the problem is the inverse, a required empty-state success is refused.
4. **FR/NFR coverage — FAIL.** Findings 1, 2, 3, 5, 6, and 8 leave named
   requirements unproved or contradicted.
5. **Frozen surface — PASS.** The implementation commit adds only WP06-owned
   checkpoint/playback files.
6. **Locked decision — FAIL.** Bounded full replay and zero-I/O laws are
   contradicted.
7. **Shared-file ownership — PASS.** No production file overlap was found in
   this implementation commit.
8. **Production fragility — PASS.** No panic/bare-raise equivalent was added.

## Verification evidence

Passed:

- `go test ./internal/controljournal/checkpoint ./internal/controljournal/playback -count=5`
- `go test -race ./internal/controljournal/checkpoint ./internal/controljournal/playback -count=2`
- `go test ./internal/architecture -run 'M1Dependency|M1Budget|M1Contract' -count=1`
- `go vet ./internal/controljournal/checkpoint ./internal/controljournal/playback`
- `staticcheck ./internal/controljournal/checkpoint ./internal/controljournal/playback`
- `git diff --check 4e2c5e3..928a082`

Downstream impact: WP07 and WP09 depend on WP06. They must not consume the
current checkpoint/epoch/playback seams until this review is resolved, and must
refresh from the corrected WP06 lane before implementation.
