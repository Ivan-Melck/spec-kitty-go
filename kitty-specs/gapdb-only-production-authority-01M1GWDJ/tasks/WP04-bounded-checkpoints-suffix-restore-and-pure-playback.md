---
work_package_id: WP04
title: Bounded Checkpoints Suffix Restore and Pure Playback
dependencies:
- WP02
- WP03
requirement_refs:
- FR-003
- FR-019
- FR-020
- FR-021
- FR-022
- FR-025
- FR-034
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-009
- NFR-012
- NFR-013
- C-002
- C-013
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T017
- T018
- T019
- T020
- T021
- T022
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controljournal/v2/
create_intent:
- internal/controljournal/v2/checkpoint/model.go
- internal/controljournal/v2/checkpoint/checkpoint.go
- internal/controljournal/v2/checkpoint/checkpoint_test.go
- internal/controljournal/v2/playback/playback.go
- internal/controljournal/v2/playback/playback_test.go
- internal/controljournal/v2/restore/restore.go
- internal/controljournal/v2/restore/restore_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controljournal/v2/checkpoint/model.go
- internal/controljournal/v2/checkpoint/checkpoint.go
- internal/controljournal/v2/checkpoint/checkpoint_test.go
- internal/controljournal/v2/playback/playback.go
- internal/controljournal/v2/playback/playback_test.go
- internal/controljournal/v2/restore/restore.go
- internal/controljournal/v2/restore/restore_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 — Bounded Checkpoints Suffix Restore and Pure Playback

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement a bounded, deterministic recovery pipeline that restores the
memory-primary projection from the newest compatible complete checkpoint plus
immutable per-aggregate event suffixes. Playback must be incapable of external
I/O, recovery must restart rather than mix revisions, and only a sealed restore
result may authorize admission of a fresh runtime epoch.

## Context

WP02 defines Authority Journal v2 records, operation membership, reducers, and
sealed publication plans. WP03 supplies the sole GapDB translation plus a
provider-neutral, revision-aware read port. This WP consumes those contracts;
it must not import GapDB, reach into provider files, or invent a second storage
API. WP05–WP14 depend on this package for restart safety and memory-primary
state.

Read `plan.md`, `spec.md`, `research.md` Decision 3, the checkpoint and restore
sections of `data-model.md`, and `contracts/authority-journal-v2.md`. Also read
the WP02/WP03 implementation and tests as landed in this lane. Treat their
public contracts as authoritative; do not fork equivalent event, head, key, or
operation types inside checkpoint/restore packages.

The governing recovery sequence is:

1. close command admission;
2. capture positive durable `(DatabaseID, R)`;
3. select a compatible checkpoint candidate or origin;
4. scan the complete global aggregate-head and operation-record censuses,
   requiring each initial page to report `R` and every continuation cursor to
   remain provider-pinned;
5. compare checkpoint and current aggregate censuses;
6. exact-read each required logical version with bounded parallelism;
7. require positive record revisions no greater than `R`, reconstruct
   historical OperationState from operation events, and validate exact
   membership and terminal heads;
8. sort by `(Record.Revision, EventIndex, EventID)` and reduce purely;
9. verify the whole-product digest and re-read the same DatabaseID and exactly
   `R`; and
10. seal the result that may request a fresh runtime epoch.

GapDB `Watch` is notification only. It is bounded and compactable and therefore
cannot be the durable suffix. Do not add a hot global journal sequence or scan
all opaque EventIDs after a nonzero checkpoint. Suffix addressability comes
from immutable `(aggregate coordinate, logical version)` event keys.

Stay inside the seven owned files. If a dependency contract is missing, report
the exact gap to WP02/WP03 rather than editing their surface from this lane.
Per `post-approval-defect-2.md`, this WP remains blocked until corrected WP01,
WP02, and WP03 are independently reapproved in that order. Do not implement
against an intermediate dependency state.

`post-approval-defect-4.md` supersedes the prior resume decision. WP04 found
that plan/record-set prefix evidence was process-local and that anonymous
operation-summary leaves could not address operation suffixes. It remains
blocked until WP01, WP02, and WP03 are again independently reapproved. Then
consume the mandatory immutable publication witness, attributable operation
boundaries, shared prefix helper, and canonical reducer-state digest helper;
do not invent equivalents in these packages.

This is an attempt-bound stable-revision protocol, not an as-of-Get contract.
The frozen GapDB API cannot accept `R` on `Get` or on the initial prefix page.
WP04 must use the foundation recovery values repaired in WP02 and the WP03
adapter implementation; it must not import GapDB, inspect adapter-private keys,
or define substitute event/head/operation wire structs.

## Branch Strategy

- Planning branch: `feat/gapdb-only-production-authority`.
- Final mission merge target: `feat/gapdb-only-production-authority`.
- Spec Kitty assigns execution worktrees from the computed lane in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Use the runtime capsule's branch and workspace. Do not assume a particular
  lane number, worktree path, or dependency merge commit.
- Other agents may be implementing capability packages concurrently. Do not
  revert their changes or broaden this WP's ownership.

Start or resume implementation with:

```bash
spec-kitty agent action implement WP04 --agent <name> --mission gapdb-only-production-authority-01M1GWDJ
```

## T017 — Checkpoint v2 complete census, rolling sealed prefix, and candidate ring

### Purpose

Model and build a checkpoint that proves one complete authoritative projection
through one positive provider revision without replaying history from origin.
Retain enough independently verifiable candidates to recover from a corrupt
latest checkpoint.

### Implementation guidance

1. In `checkpoint/model.go`, define closed v2 checkpoint records matching the
   mission model: schema/version identity, checkpoint ID, positive
   `ThroughRecordRevision`, event/reducer registry digests, codec version,
   rolling prefix digest, complete aggregate entries, operation summary,
   canonical projection digest, creating PublicationID, and checkpoint digest.
2. Each aggregate entry must include its exact coordinate, logical version,
   terminal EventID, canonical state bytes/digest, and tombstone state. Sort by
   canonical coordinate before encoding; reject duplicates and ambiguous order.
3. Represent the complete aggregate census explicitly. An omitted aggregate is
   corruption, not an empty/default aggregate. A tombstoned aggregate remains
   in the census because M2 forbids physical event/head deletion.
4. Accept a sealed projection/prefix capability maintained after acknowledged
   publications. Checkpoint creation must be proportional to current projection
   size and may not reduce the full journal again.
5. Bind the rolling prefix digest to prior sealed prefix state plus every
   acknowledged publication through the checkpoint watermark. Do not advance
   it on optimistic projection application or ambiguous acknowledgement.
   Consume WP02's seal; do not invent another acknowledgement or projection
   capability. Its acknowledgement ledger must admit at least 10,000
   publications and order them by provider revision, not response completion.
6. Compute canonical bytes and digests internally. Keep proof seals private,
   make returned byte/state slices defensive copies, and reject zero-value or
   forged sealed capabilities.
7. Model a bounded checkpoint candidate ring: fixed slot count, immutable
   checkpoint records, and slot references updated only by a legitimate
   publication plan. Selection sorts by watermark rather than slot order.
8. Validate candidates independently. Fall back from a corrupt or incompatible
   newest candidate to an older valid candidate; reject two valid candidates
   at the same watermark whose canonical identities disagree.
9. Preserve exact registry and codec compatibility. Unknown schema, event set,
   reducer set, or canonical codec makes that candidate unusable; it must never
   be partially interpreted.
   Event compatibility includes registry identity, publication rule, ordering,
   and the complete family tuple census. Reducer compatibility includes every
   declared implementation digest.
10. Add tests for deterministic encoding, reordered aggregate input, complete
    census, tombstones, defensive copies, forged seals, ring wraparound, corrupt
    newest fallback, contradictory same-watermark candidates, and origin mode.
11. Keep `operation_summary` compact: bounded counts/status counts and sorted
    fixed-size `OperationBoundaryV2` values containing OperationID lookup
    digest, logical version, and a leaf that binds exact identity,
    PublicationID, status/result, and membership digests. Preserve the
    checkpoint logical-version boundary needed for suffix addressing, but do
    not copy variable-length OperationIDs or terminal results into the
    checkpoint. Require the final structured checkpoint encoder to fit 10,000
    maximum-version boundaries and complete summary overhead within
    `MaxOperationSummaryBytesV2 = 2,112,000` bytes.

### Validation

```bash
go test ./internal/controljournal/v2/checkpoint -count=1
```

## T018 — Stable attempt-bound head census and whole-attempt restart

### Purpose

Ensure recovery observes one coherent store revision. A stale cursor or moving
store must restart the entire bounded attempt; recovery may not splice heads
or suffix records from different revisions.

### Implementation guidance

1. In `restore/restore.go`, define the narrow provider-neutral reader required
   from WP03 using WP02's foundation types: capture store identity/current/
   durable revision, scan aggregate heads and operation records under an
   attempt coordinate, exact-read immutable event versions, and surface typed
   stale-cursor/revision conditions. The interface is consumer-owned here, but
   its value types are not duplicated here.
2. Keep the reader outside playback. Restore orchestrates reads; playback only
   receives already materialized validated facts.
3. Capture positive durable `(DatabaseID, R)` before selecting/scanning. Require
   each initial page to report exactly `R`, every continuation page/cursor to
   remain provider-pinned to that identity/revision, and every returned record
   revision to be positive and no greater than `R`.
4. Scan the complete `spk/v2/heads/` aggregate census and host-global
   `spk/v2/operations/` binding/head census deterministically with bounded page
   count, record count, key bytes, value bytes, and elapsed-attempt budget.
5. On stale cursor, revision mismatch, database replacement, page-cycle,
   repeated cursor, malformed continuation, or final status other than the same
   DatabaseID and exactly `R`, discard all data from that attempt and restart
   from candidate selection.
6. Bound whole-attempt restarts. Exhaustion returns a typed unstable-store
   diagnosis with attempt count and observed revision coordinates; never loop
   indefinitely or silently downgrade to an unpinned scan.
7. Reject duplicate heads, malformed keys, key/value coordinate mismatch,
   zero/regressed versions, expired records, missing checkpoint aggregates,
   and physical deletion relative to the checkpoint census.
8. Compare operation heads with `OperationBoundaryV2`: match each exact
   scanned OperationID through the boundary's pure lookup-digest method,
   exact-read/reconstruct the state at its boundary version, and verify the
   leaf. Existing operations fetch `(boundaryVersion,currentVersion]`; new
   operations fetch from one. Missing, extra, duplicate, colliding, unmatched,
   or regressed operations close recovery.
   Recompute status counts and leaf commitments from the exact reconstructed
   boundary states; structural summary arithmetic alone is insufficient.
9. Compare the head census to the checkpoint aggregate versions to derive an
   exact immutable suffix plan. New aggregates start from version one;
   unchanged aggregates require no event read; regressed/absent aggregates
   close recovery.
10. Test store movement before the first page, between pages, after the final
   page, during suffix fetch, and at final seal. Assert every case restarts the
   whole attempt and no data from the abandoned attempt reaches playback.
11. Do not claim provider as-of Get or an explicit first-page requested
   revision. The proof is the equal capture/final store coordinate plus the
   first-page, cursor, and record-revision checks. Include a same-`R`, different
   DatabaseID replacement case.

### Validation

```bash
go test ./internal/controljournal/v2/restore -run 'Test.*Census|Test.*Restart|Test.*Revision' -count=3
```

## T019 — Bounded parallel per-aggregate exact suffix reads

### Purpose

Fetch only the immutable event versions after the selected checkpoint while
allowing unrelated aggregates to progress independently and keeping memory,
I/O, concurrency, and startup duration bounded.

### Implementation guidance

1. Build exact requests for every logical version in the half-open checkpoint
   interval `(checkpointVersion, currentHeadVersion]`; never discover suffix
   records through Watch or an EventID-wide scan.
2. Use a fixed-size worker pool with context cancellation. Make worker count,
   maximum aggregates, event count, event bytes, per-event bytes, total key
   bytes, and elapsed duration explicit validated budgets.
3. Count budgets before allocation and again from observed bytes. Avoid integer
   overflow when summing version ranges or sizes.
4. Cancel remaining work on the first terminal violation but drain/join workers
   deterministically. Return bounded diagnostics rather than partial results.
5. Exact-read WP02 semantic `RecordAddressV2` requests through the
   consumer-owned reader implemented by WP03. Validate that the returned
   semantic address and decoded envelope coordinate/version equal the request;
   do not import the adapter key codec or parse an encoded GapDB key.
6. Require continuous versions. A missing, duplicate, expired, reordered-key,
   or overwritten immutable record is journal corruption, never a reason to
   skip ahead or fall back to Watch.
7. Require each record revision to be greater than checkpoint watermark and no
   greater than `R`. Origin replay uses version zero/watermark zero under the
   same algorithm and explicit full-reconstruction budgets.
8. Sort materialized results after concurrent fetch. Worker completion order
   must have no semantic effect.
9. Collect distinct suffix PublicationIDs and exact-read one foundation
   `PublicationWitnessV2` address for each. Apply the same concurrency and byte
   bounds; a witness is not discovered through a new prefix scan.
10. Test one aggregate with a long suffix, many unrelated aggregates, empty
   suffix, new aggregate, cancellation, slow reader, budget boundaries,
   overflow inputs, missing version, and deterministic equality across worker
   counts.

### Validation

```bash
go test ./internal/controljournal/v2/restore -run 'Test.*Suffix|Test.*Budget|Test.*Parallel' -count=3
go test -race ./internal/controljournal/v2/restore -run 'Test.*Suffix|Test.*Parallel' -count=1
```

## T020 — Operation membership, total ordering, and terminal-head validation

### Purpose

Prove the fetched union represents whole atomic publications and reaches the
exact authoritative heads. Per-aggregate continuity alone cannot detect a
partial multi-aggregate operation.

### Implementation guidance

1. Group fetched events by OperationID/PublicationID, resolve each global
   immutable OperationBinding/current operation head through the reader, and
   exact-read the immutable operation event at each referenced operation
   logical version.
2. Reconstruct historical OperationState only through WP02's closed operation
   event decoder. The event payload contains the canonical state transition
   preimage and the envelope supplies LastPublicationID. For the first
   post-checkpoint transition of an existing operation, read at most the
   immediately preceding operation event as a budgeted boundary state; do not
   invent a revisioned-state wire record.
3. Require exact membership: declared event count, distinct consecutive event
   indices, one event per affected aggregate, matching event IDs/coordinates,
   common positive provider record revision, exact cumulative event/effect
   delta from the preceding OperationState, and the expected terminal operation
   version/result.
4. Validate exactly one immutable publication witness per group. Its
   PublicationID, OperationID/logical version, plan digest, non-witness
   record-set digest/count, and self-digest must validate, and its provider
   revision must equal every event revision in the publication. Advance the
   rolling prefix only through WP02's shared pure helper.
5. Reject partial groups even when the absent member's aggregate was unchanged
   relative to the checkpoint. Membership is publication evidence, not an
   inference from current heads.
6. Reject a suffix event whose operation is absent, mismatched, ambiguous,
   differently bound, or records another publication digest.
7. Order the complete union by `(Record.Revision, EventIndex, EventID)` with a
   total deterministic comparator. Reject invalid ties rather than depending
   on Go map or worker order.
8. Reduce from the checkpoint projection (or empty origin) and verify every
   terminal aggregate logical version, EventID, state digest, tombstone, and
   whole-product canonical projection digest against current heads/seal data.
9. Verify the recomputed rolling prefix digest and operation summary through
   `R`. A matching projection digest does not excuse mismatched journal or
   operation membership.
10. Return typed bounded diagnostics containing immutable coordinates but no
   unbounded state/event payloads.
11. Test split publications, repeated/missing indices, mixed revisions,
   duplicate aggregates, operation collisions, ordering ties, stale terminal
   heads, forged matching state digests, operation-only publications,
   multi-stage operations crossing a checkpoint, deleted operation head with a
   surviving binding, missing/substituted/wrong-revision witnesses, divergent
   prefix folds, and valid cross-aggregate batches.

### Validation

```bash
go test ./internal/controljournal/v2/restore -run 'Test.*Membership|Test.*Ordering|Test.*Terminal' -count=1
```

## T021 — Side-effect-free playback and fresh epoch admission

### Purpose

Make deterministic reduction independently auditable and ensure recovered state
cannot serve commands until replacement authority is durably admitted.

### Implementation guidance

1. In `playback/playback.go`, accept only immutable checkpoint projection,
   validated ordered event material, and the closed reducer registry. No
   interface in this package may represent storage, network, filesystem, Git,
   tracker, clock, randomness, agent, compensation, outbox delivery, or durable
   write behavior.
   Verify the complete event/reducer registry descriptor digests, including
   ordering, owners, and declared reducer implementation identities, before
   the first Apply.
2. Apply reducers deterministically, tracking terminal heads, event/byte
   census, through revision, rolling prefix digest, and canonical projection
   digest. Enforce the same bounded limits on direct audit playback.
   Runtime Apply is stateless; determinism qualification is separate and no
   256-observation session budget may truncate replay.
3. Make output immutable by defensive copying or opaque sealed capabilities.
   A zero-value/forged playback result cannot become restored authority.
4. In `restore`, seal a result only after checkpoint validation, stable census,
   suffix membership/order, pure playback, terminal validation, digest match,
   and final `R` recheck all succeed.
5. The sealed result may produce a provider-neutral fresh-epoch admission
   request for the production host/WP03 committer. Restore itself must not write
   the epoch or open the command barrier.
6. Bind admission input to database/host authority identity, recovered through
   revision, projection digest, prior current epoch (if any), event/reducer
   registry digests, codec version, and recovery attempt identity.
7. Existing committed OperationID results remain readable after epoch rotation.
   A predecessor capsule cannot authorize a new mutation. Keep replay identity
   resolution distinct from current-epoch admission.
8. Add compile-time/import and runtime spy controls proving playback performs
   zero adapter calls and zero durable writes. Include panic-on-call fakes at
   the restore boundary.
9. Test deterministic digest equality across repeated playback, input
   permutations normalized by validation, restored checkpoint+suffix versus
   full origin playback, forged seal rejection, and refusal to admit from any
   degraded/partial result.

### Validation

```bash
go test ./internal/controljournal/v2/playback ./internal/controljournal/v2/restore -run 'Test.*Playback|Test.*Epoch|Test.*Seal' -count=3
```

## T022 — Corruption, version, budget, fault, and performance proof

### Purpose

Exercise the recovery boundary adversarially and establish evidence usable by
later capability packages and the M2 crown campaign.

### Implementation guidance

1. Build table-driven tests for corrupt checkpoint bytes/digest/state,
   contradictory candidates, unknown schema/event/reducer/codec/order versions,
   incomplete census, regressed/deleted head, missing/duplicate suffix version,
   partial operation group, and final digest mismatch.
   Include the obsolete operation event tuple, mismatched operation event/state
   payload, missing operation-only publication, unknown reducer implementation
   digest, old scope-first head spelling, and same-revision database replacement.
2. Cover every bounded resource independently: candidates, aggregates, pages,
   cursors, restarts, suffix events, event bytes, projection bytes, operation
   groups, workers, and wall-clock/context expiration.
3. Inject faults before/after candidate selection, every scan page, each exact
   read class, playback, final revision check, and epoch-admission handoff.
   Assert no partial authority, no external effect, and deterministic typed
   disposition.
4. Prove newest-valid-checkpoint precedence, older-valid fallback, bounded
   origin reconstruction, and closed refusal when every option is invalid or
   over budget. Never skip history to achieve readiness.
5. Run at least 100 deterministic repetitions for concurrency-sensitive
   restart/worker cases in the focused test harness or expose a bounded helper
   for the later campaign without making ordinary unit tests excessively slow.
6. Add a 10,000-operation fixture with realistic aggregate distribution.
   Measure checkpoint-plus-suffix restoration over at least 30 runs and expose
   p95 calculation/evidence for the final NFR-006 gate (p95 below 2 seconds on
   the declared reference machine).
   Ensure it contains more than 256 distinct reducer inputs, at least one scope
   created after the checkpoint, an operation-only publication, and a staged
   operation crossing the checkpoint boundary.
7. Keep timing assertions robust in ordinary CI: validate operation counts,
   boundedness, and gross upper limits locally; reserve the exact reference p95
   adoption claim for the source-bound qualification environment.
8. Compare restored and uninterrupted projections byte-for-byte and by
   canonical digest. Assert playback adapter calls and durable writes remain
   exactly zero.
9. Run format, focused unit/race tests, and all v2 journal tests. Capture exact
   test census and terminal outcomes in handoff evidence.
10. Add controlled mutants that omit first-page `R`, final DatabaseID/R,
    record-revision upper bounds, operation boundary-state validation,
    publication membership, registry/reducer descriptor fields, durable
    acknowledgement provenance, or the globally enumerable head census. Each
    mutant must fail the same production-facing validator used by the positive
    case.

### Validation

```bash
go test ./internal/controljournal/v2/... -count=1
go test -race ./internal/controljournal/v2/checkpoint ./internal/controljournal/v2/playback ./internal/controljournal/v2/restore -count=1
git diff --check
```

## Definition of Done

- CheckpointV2 records a complete aggregate census, canonical projection,
  operation summary, registry/codec identities, positive watermark, rolling
  prefix digest, and immutable self-digest.
- Checkpoint creation consumes the sealed acknowledged projection/prefix and
  does not replay origin history.
- The bounded candidate ring selects newest valid evidence, falls back safely,
  and rejects contradictory candidates at one watermark.
- Restore scans the global aggregate-head and operation-record censuses under
  one attempt-bound `(DatabaseID, R)` proof and discards the whole attempt on
  any cursor/revision/database instability. It makes no as-of-Get claim.
- Suffix fetches use bounded parallel exact logical-version reads and are
  independent of Watch retention or worker completion order.
- Every operation group reconstructs historical OperationState from the closed
  operation event, has complete exact membership and common revision, and
  terminal aggregate heads and whole-product digest match.
- Playback has no representable external/durable capability and produces
  deterministic byte/digest-equivalent state.
- Only a fully sealed restore result can request fresh RuntimeEpoch admission;
  restore does not write the epoch or open command admission itself.
- Corrupt, unknown-version, incomplete, physically deleted, unstable, and
  over-budget inputs fail closed with bounded typed diagnostics.
- The 10,000-operation/30-run fixture and focused repeated fault cases are
  present, exceed 256 distinct reducer inputs, cover new-scope and
  operation-only history, and are suitable for later source-bound qualification.
- All focused tests, race tests, v2 journal tests, and `git diff --check` pass.
- Only the seven owned files changed.
- Subtask completion is recorded in the event log, not Markdown checkboxes:

```bash
spec-kitty agent tasks mark-status T017 T018 T019 T020 T021 T022 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks

- **Watch mistaken for durable history**: prohibit Watch from restore ports and
  verify suffix addressing exclusively by coordinate/logical version.
- **Mixed-revision state**: any stale cursor or final revision drift discards
  the entire attempt; never retain “safe” pages from it.
- **Checkpoint omission**: complete head census and terminal validation make
  absent/regressed aggregates corruption, including tombstones.
- **Global serialization**: do not introduce a product-wide journal CAS or
  sequence; provider record revision supplies ordering after publication.
- **Worker nondeterminism**: normalize only after complete validation and make
  worker completion order observationally irrelevant.
- **False purity**: playback's type surface must be incapable of I/O; a zeroed
  counter alone is insufficient evidence.
- **Epoch conflation**: recovery seals state; a later durable publication admits
  the new epoch. Reading an old committed result does not renew old authority.
- **Flaky performance gates**: separate deterministic budget assertions from
  exact p95 evidence on the declared reference machine.

## Reviewer Guidance

Reject this WP if recovery can retain a first page that does not report `R`,
accept a stale continuation cursor, claim provider as-of Get, retain data across
a whole-attempt restart, use Watch as history, scan opaque EventIDs for a nonzero
checkpoint, skip missing versions, accept partial operation membership, call an
adapter during playback, or admit authority from an unsealed result.

Review especially that checkpoint creation uses rolling acknowledged prefix
state rather than replaying origin; the candidate ring handles corrupt newest
and contradictory same-watermark records deterministically; suffix limits are
checked before large allocation; concurrent fetch has stable results; event
ordering uses provider revision plus event index and EventID; and every terminal
head/digest is verified after reduction.

Confirm that recovery uses only WP02 semantic addresses/records/pages and a
consumer-owned reader, never GapDB or adapter-private wire/key types. Verify the
global aggregate-head spelling discovers a post-checkpoint scope, operation
events reconstruct their exact state transitions, reducer Apply has no hidden
256-input cache, descriptor digests cover all compatibility coordinates, and
the final status check matches both DatabaseID and `R`.

Confirm the epoch boundary carefully: restore may prepare a typed admission
request, but only the production owner through the durable committer may publish
the fresh epoch and open command admission. Finally, inspect imports and test
spies to ensure playback purity is structural rather than a convention.
