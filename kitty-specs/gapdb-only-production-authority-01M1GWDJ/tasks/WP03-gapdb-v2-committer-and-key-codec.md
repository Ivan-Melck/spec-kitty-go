---
work_package_id: WP03
title: GapDB v2 Committer and Key Codec
dependencies:
- WP01
- WP02
requirement_refs:
- FR-001
- FR-003
- FR-004
- FR-005
- FR-006
- FR-020
- FR-021
- FR-025
- FR-034
- NFR-001
- NFR-002
- NFR-008
- NFR-012
- C-001
- C-003
- C-005
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T012
- T013
- T014
- T015
- T016
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/gapdb/
create_intent:
- internal/controlstore/gapdb/v2_codec.go
- internal/controlstore/gapdb/v2_committer.go
- internal/controlstore/gapdb/v2_reader.go
- internal/controlstore/gapdb/v2_ambiguity.go
- internal/controlstore/gapdb/v2_adapter_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/gapdb/v2_codec.go
- internal/controlstore/gapdb/v2_committer.go
- internal/controlstore/gapdb/v2_reader.go
- internal/controlstore/gapdb/v2_ambiguity.go
- internal/controlstore/gapdb/v2_adapter_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 - GapDB v2 Committer and Key Codec

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the canonical `spk/v2/` key codec and the narrow public GapDB adapter
that translates a WP02 sealed `CommitPlan` into exactly one durable
`AtomicBatch`. Preserve provider record metadata for later restore, reconcile
ambiguous publication by stable identity, and prove that the adapter adds no
capability policy or alternate authority path.

## Context

WP01 freezes the M2 contracts, registry, budgets, and architectural boundaries.
WP02 owns provider-neutral journal values, immutable host-global operation
binding, revisioned OperationState, canonical record bytes, and the only
constructor for a sealed commit plan and its closed record projection. This WP
begins only after both are accepted and consumes their public contracts without
weakening or duplicating their validation.

Capability packages own commands, event meaning, reducers, refusal precedence,
and consumer ports. WP02 owns plan membership, digests, logical versions, and
registry validation. This WP owns provider translation; WP04 later consumes its
read metadata without moving restore policy into the adapter.

Use only the admitted public `github.com/spec-kitty/gapdb/gapdb` client already
bound by the M1 adoption tuple. Do not import provider internals, start or
discover a sidecar executable, add a dependency, expose raw provider types to
kernel packages, or retain/reintroduce SQLite, SQL, a backend selector, a dual
write, a rescue read, or a migration path.

### Branch, lane, and ownership

Implementation command:

```bash
spec-kitty agent action implement WP03 --agent codex
```

Run that command from the repository root and perform implementation only in
the WP03 lane worktree assigned by `lanes.json`. The lane must be based on the
accepted WP01 and WP02 dependency state. Do not choose a branch manually,
reuse another WP's lane, edit the root checkout, or merge directly to `main`.
Accepted work merges only into `feat/gapdb-only-production-authority` through
the Spec Kitty workflow.

Own exactly the five files in `owned_files`. Do not modify WP02 journal files,
existing M1 adapter files, `go.mod`, `go.sum`, mission contracts, architecture
guards, sidecar lifecycle code, or later capability packages. If an upstream
contract is insufficient, stop with a concrete dependency finding rather than
creating a second model inside the adapter.

### Governing contracts

- `contracts/authority-journal-v2.md` governs keys, publication, ambiguity,
  restore metadata, and capability ownership.
- `contracts/operation-binding-v2.schema.json` fixes the immutable binding
  shape; the adapter must not reinterpret it.
- `contracts/event-family-registry-v2.json` is a closed registry enforced by
  the sealed plan, not a provider routing table.
- M1 product budgets remain stricter than provider limits: 1 KiB keys, 256 KiB
  ordinary values, 4 MiB checkpoints, 128 events, 32 heads, 16 intents, 256
  mutations, 6 MiB raw, 12 MiB wire, 500 scan records, and 8 MiB scan pages.
- Provider revisions are storage metadata. They never become logical versions,
  caller intent, authority identities, or guessed current-batch values.

### Subtask T012: Implement the canonical host-global and scoped v2 key codec

**Purpose**: Provide one closed, reversible, budgeted encoding for every v2 key
family without allowing ambiguous aliases, path-like traversal, or
mission-scoped operation identity.

**Steps**:

1. Create `internal/controlstore/gapdb/v2_codec.go` with constructors and
   parsers for the exact key families fixed by `authority-journal-v2.md`:

   - `spk/v2/authority/binding`;
   - `spk/v2/runtime/current` and
     `spk/v2/runtime/epochs/<runtime-epoch-id>`;
   - host-global `spk/v2/operations/<operation-id>/binding` and `/head`;
   - host-global immutable `spk/v2/effects/<effect-id>/intent`;
   - scoped aggregate heads and version-addressed immutable events;
   - checkpoint slot/record keys; and
   - WorkRevision eligibility-shard keys.

2. Consume WP02 identity and aggregate-coordinate types where available.
   Avoid parallel stringly typed identity models in the adapter. Operation keys
   must never accept or prepend a mission scope.

3. Implement the single canonical component encoding frozen by WP01. Require a
   decode-then-reencode byte-equality check. Reject empty components,
   non-canonical escapes, alternate spellings, separators inside raw
   components, `.`/`..` aliases, malformed tagged scopes, unknown key families,
   trailing components, and bytes outside the contract's admitted domain.

4. Encode a positive logical version as exactly 16 lowercase hexadecimal
   characters. Reject zero, uppercase, short, overlong, signed, overflowing,
   or non-hex versions. Prove lexical and numeric order agree.

5. Validate complete encoded key length against the 1 KiB product limit before
   allocation or provider access. Diagnostics must name the key family or
   component, remain bounded, and omit full sensitive values.

6. Keep the codec pure and deterministic: no provider calls, environment,
   filesystem, clock, random source, Unicode normalization, or current runtime
   epoch lookup.

**Files**: Create `internal/controlstore/gapdb/v2_codec.go` and add codec cases
to `internal/controlstore/gapdb/v2_adapter_test.go`.

**Validation**:

- Every canonical key family round-trips byte-for-byte.
- Operation keys remain host-global across two mission scopes.
- Scoped keys distinguish host, project, mission, repository, WorkRevision,
  and work coordinates without collisions.
- Exact 1 KiB keys pass; plus-one keys fail before a provider call.
- Canonical version `0000000000000001` passes; zero, uppercase, malformed, and
  overflow forms fail with stable typed diagnostics.
- A table of alternate escape and separator forms proves that two inputs cannot
  decode to one authority key.

### Subtask T013: Add the provider-neutral reader and record-metadata port

**Purpose**: Expose the bounded reads WP04 needs for suffix recovery while
retaining authoritative GapDB record metadata and keeping provider types behind
the adapter boundary.

**Steps**:

1. Create `internal/controlstore/gapdb/v2_reader.go` with the smallest
   provider-neutral read surface needed for exact Get and bounded prefix scan.
   Reuse the admitted M1 adapter's public client boundary rather than importing
   `internal/server` or constructing another client stack.

2. Return immutable record copies containing canonical key, copied value bytes,
   positive provider `Record.Revision`, and normalized optional expiry
   metadata. Do not expose `gapdb.Revision`, cursors, errors, or record structs
   outside this adapter package.

3. For exact reads, verify the provider returned the requested canonical key,
   a positive revision, an admitted value size, and valid metadata. A foreign
   key, zero revision, oversized value, impossible expiry, or malformed public
   response is a typed bounded refusal.

4. For prefix scans, require a canonical family prefix, a positive record limit
   no greater than 500, a byte limit no greater than 8 MiB, a bounded cursor,
   strictly increasing keys, one stable positive observed revision, and honest
   cursor/truncation agreement.

5. Preserve the provider page's observed revision and each record's own
   revision separately. Do not infer one from the other and do not assign
   current-batch revision to values.

6. If a provider page exceeds the requested byte budget, reissue the same
   revision-pinned cursor with a smaller record count. Never truncate locally,
   because that can skip records. Watch remains notification-only and is not a
   journal or suffix reader.

7. Translate public client outcomes into existing bounded storage-neutral
   error kinds. Read unavailability may be retried; malformed metadata and
   version/capability mismatch require operator or upgrade action, not a
   fabricated empty result.

**Files**: Create `internal/controlstore/gapdb/v2_reader.go` and add reader
contract cases to `internal/controlstore/gapdb/v2_adapter_test.go`.

**Validation**:

- Exact Get retains key, bytes, revision, and expiry without aliasing buffers.
- Foreign keys, zero revisions, unsorted/duplicate scan records, unstable page
  metadata, stale cursor signals, and plus-one record/byte budgets fail closed.
- A 500-record page at exactly 8 MiB succeeds; a plus-one page is safely
  repaged or refused without skip/duplicate behavior.
- No public GapDB type crosses the adapter's application-facing signatures.

### Subtask T014: Translate a sealed CommitPlan into exactly one AtomicBatch

**Purpose**: Make one validated application publication equal one conditional,
durably acknowledged provider revision without interpreting capability
semantics in the adapter.

**Steps**:

1. Create `internal/controlstore/gapdb/v2_committer.go`. Accept only WP02's
   opaque/sealed commit-plan capability; do not accept a public struct or raw
   slice that callers can assemble after validation.

2. Flatten only the sealed plan's closed provider-neutral record projection in
   its canonical membership order. Map each typed semantic target through this
   WP's canonical key codec and preserve the exact canonical bytes, value class,
   and condition supplied by the projection. Do not parse the plan's evidence
   serialization, reconstruct heads, consult capability state, or maintain a
   second membership model.

3. Map immutable creates to `ConditionAbsent` and existing mutable heads/guards
   to `ConditionRevision` with a positive exact observed provider revision.
   New heads use absence. Reject every `ConditionAny`, delete, expiry, duplicate
   key, zero expected revision, or mutation role that lacks an explicit
   condition. M2 product deletion is a tombstone put, never physical deletion.

4. Verify the flattened set exactly matches the sealed plan membership and
   contains no extra or omitted mutation. The adapter may validate structural
   roles, canonical keys, sizes, and conditions; it must not evaluate mission,
   review, integration, effect, workgraph, or event-family policy.

5. Enforce all key/value/cardinality/raw/wire budgets using the exact encoder
   that will publish. Ordinary and checkpoint values retain distinct ceilings.
   An invalid or over-budget plan must make zero provider calls.

6. Set the public GapDB batch acknowledgement to durable and attach the stable
   `PublicationID` as the provider request identity. Make exactly one
   `AtomicBatch` call—no preliminary mutation, split, fallback, or per-record
   retry.

7. Accept success only when revision is positive, acknowledgement is durable,
   durable-through is at least the returned revision, and mutation count equals
   exact plan membership. Return the positive revision as provider metadata;
   do not write it back into already canonical event values.

**Files**: Create `internal/controlstore/gapdb/v2_committer.go` and add
translation/mutation-spy cases to `internal/controlstore/gapdb/v2_adapter_test.go`.

**Validation**:

- One sealed multi-aggregate plan produces one public `AtomicBatch` call and
  one positive durable revision shared by all returned records.
- Every immutable event/binding/checkpoint uses absence; every existing head or
  guard uses its independent exact revision.
- Condition-any, duplicate, delete, expiry, missing-member, extra-member,
  count, raw-byte, value-byte, key-byte, and wire-byte mutants fail before the
  provider spy increments.
- A dishonest memory-only acknowledgement, wrong mutation count, zero revision,
  or durable-through mismatch becomes ambiguity/malformed-response handling,
  never success.

### Subtask T015: Reconcile ambiguous publication by stable PublicationID

**Purpose**: Resolve may-have-committed outcomes without duplicating authority,
changing caller identity, or optimistically publishing an in-memory projection.

**Steps**:

1. Create `internal/controlstore/gapdb/v2_ambiguity.go` and preserve the exact
   `PublicationID` deterministically derived by WP02 from OperationID, next
   operation logical version, and sealed plan digest.

2. Classify EOF, timeout, cancellation after delivery, malformed
   acknowledgement, and other public-client may-have-applied results as typed
   ambiguity. Distinguish proven pre-delivery unavailability and caller
   cancellation where the public contract proves no mutation was attempted.

3. Reconcile using bounded ordinary reads of the host-global OperationBinding,
   OperationState head/event, every planned capability event/head, effects,
   guards, and checkpoint membership. Compare canonical bytes, keys,
   conditions, PublicationID, OperationID, logical versions, membership, and
   one common positive provider revision.

4. Return the exact recorded result/progress only when complete matching
   membership proves the plan committed. Proven absence of every planned key
   may return an explicit `retry_same_publication` disposition; retry remains a
   caller decision using the identical sealed plan and still-valid conditions.

5. Treat partial, mixed-revision, contradictory, foreign-publication, or
   mismatched-binding membership as journal corruption/authority closure. Do
   not collapse it to absence or retry.

6. Never allocate a new OperationID or PublicationID, automatically rebuild a
   plan, retry from inside the committer, call an external-effect adapter,
   mutate a projection, or claim provider exactly-once behavior.

7. Bound reconciliation reads, bytes, elapsed time, and diagnostic detail.
   Stable errors may name immutable IDs and recovery actions but must not embed
   values, credentials, full socket paths, or provider transcripts.

**Files**: Create `internal/controlstore/gapdb/v2_ambiguity.go` and add ambiguity
matrices to `internal/controlstore/gapdb/v2_adapter_test.go`.

**Validation**:

- Lost response after a complete commit returns the exact recorded result with
  zero duplicate provider mutations.
- Proven absence permits only explicit retry of the same PublicationID and
  sealed plan; changed or newly generated identities are refused.
- Partial binding/state/event/head membership and mixed revisions close
  authority with bounded diagnostics.
- Reconciliation invokes reads only and leaves the in-memory projection
  untouched until complete durable membership is proven.

### Subtask T016: Prove the public-sidecar adapter contract

**Purpose**: Exercise the v2 boundary against the admitted public GapDB
semantics and demonstrate atomic membership, contention behavior, budgets, and
provider neutrality before capability WPs depend on it.

**Steps**:

1. Build `internal/controlstore/gapdb/v2_adapter_test.go` around WP02 sealed-plan
   fixtures plus the admitted public client/sidecar contract. Production-facing
   tests must not import or instantiate GapDB `internal/server`.

2. Add a clean multi-aggregate publication containing the first global
   OperationBinding, OperationState transition, at least two independent
   capability events/heads, an effect intent or guard, and optional checkpoint
   membership. Verify all records share one returned provider revision and the
   expected PublicationID.

3. Race two plans that touch independent aggregate heads and prove both can
   commit without a global journal-head condition. Then race incompatible plans
   against the same observed head revision and prove exactly one wins while the
   loser receives a typed conflict after same-operation resolution.

4. Assert batch membership in both directions: every sealed-plan member appears
   exactly once in the provider batch, and no provider mutation lacks a sealed
   member. Include role-specific condition assertions and a no-`ConditionAny`
   invariant.

5. Test exact and plus-one boundaries for key bytes, ordinary/checkpoint value
   bytes, event/head/intent counts, total mutations, raw batch bytes, wire frame
   bytes, scan records, scan bytes, cursor bytes, reconciliation records, and
   diagnostic bytes. All plus-one inputs refuse before publication or
   unbounded allocation.

6. Inject before-delivery failure, after-commit EOF, timeout, cancellation,
   malformed acknowledgement, stale same-head conditions, and contradictory
   readback. Record provider call counts and ensure no path silently retries or
   substitutes an identity.

7. Add compile-time direction checks: only this adapter imports the admitted
   public client, WP02/domain packages remain provider-neutral, and no adapter
   API exposes provider records, revisions, batches, errors, or cursors.

8. Keep the suite bounded and deterministic. Separate any repeated race/stress
   census from ordinary unit tests and document the exact bounded invocation;
   do not claim M2 crown qualification from this WP.

**Files**: Create `internal/controlstore/gapdb/v2_adapter_test.go`.

**Validation**:

```bash
go test ./internal/controlstore/gapdb -count=1 -run 'V2Key|V2Reader|V2Commit|V2Ambiguity|V2Adapter'
go test -race ./internal/controlstore/gapdb -count=1 -run 'V2.*Contention'
go vet ./internal/controlstore/gapdb
staticcheck ./internal/controlstore/gapdb
git diff --check
```

The public-sidecar tests must prove independent progress, exactly-one same-head
success, complete same-revision membership, strict budget refusal, stable
identity reconciliation, and zero provider calls for rejected preflight cases.

## Definition of Done

- T012 provides one canonical closed v2 key codec for all contract families;
  host-global operations cannot collide with or be hidden by scoped keys.
- T013 exposes exact and paged provider-neutral reads with honest record/page
  revisions, bounded copied values, and no public GapDB types above the adapter.
- T014 accepts only a WP02 sealed plan and translates it into exactly one
  durable conditional `AtomicBatch` with no unconditional/delete semantics.
- T015 resolves ambiguity by exact OperationID/PublicationID membership,
  returns recorded results or explicit dispositions, and performs no automatic
  retry, identity allocation, effect, or projection mutation.
- T016 proves independent-head progress, same-head contention, exact batch
  membership, durable acknowledgement, malformed response handling, and all
  exact/plus-one budgets through the public-sidecar boundary.
- No provider internal package, SQLite/SQL path, alternate backend, dependency,
  sidecar lifecycle, domain policy, restore policy, or raw provider type is
  added to the WP03 surface.
- Focused tests, bounded race test, vet, staticcheck, and diff checks pass.
- Only the five WP03-owned files differ from the accepted dependency base.
- No test manufactures a literal success shape without invoking the production
  codec, reader, committer, or reconciler it claims to cover.
- Subtask completion is recorded through the event-sourced status command, not
  by editing checkboxes or WP frontmatter:

```bash
spec-kitty agent tasks mark-status T012 T013 T014 T015 T016 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

- The completed package is moved through the normal Spec Kitty review lane;
  green tests alone do not authorize merge or claim final M2 qualification.

## Risks

- **Codec aliasing**: permissive escaping can give one authority record two
  spellings. Require canonical decode/reencode equality and collision mutants.
- **Global identity regression**: placing OperationID below mission scope
  recreates cross-capability collision risk. Keep operation keys host-global.
- **Semantic leakage**: adapter convenience checks can become mission or review
  policy. Limit validation to sealed membership, key roles, conditions,
  provider metadata, and budgets.
- **Unconditional overwrite**: provider `ConditionAny` can turn a stale plan
  into authority. Reject it structurally for every v2 publication mutation.
- **Optimistic projection**: applying memory state before durable proof exposes
  partial success. Publish projection changes only above this WP after a valid
  acknowledgement or complete reconciliation.
- **Ambiguity duplication**: retrying internally or generating a fresh ID can
  create duplicate transitions/effects. Preserve the same sealed plan and
  return an explicit recovery disposition.
- **False absence**: partial or foreign membership can look retryable when only
  one key is checked. Reconcile the complete sealed membership under bounded
  reads and require one common revision.
- **Budget drift**: provider defaults are larger than product limits. Enforce
  the frozen product ceilings locally with exact/plus-one tests.

## Reviewer Guidance

Begin with dependency direction and the sealed boundary. Confirm WP02/domain
packages import no GapDB type and that no caller can construct a publishable raw
batch after sealing. Trace one representative plan from canonical members to
the sole public `AtomicBatch` call and compare every key, byte slice, condition,
request identity, mutation count, acknowledgement, and returned revision.

Search every v2 mutation path for `ConditionAny`, delete, expiry, split batch,
pre-publication side effects, automatic retry, fresh OperationID, or fresh
PublicationID. Any reachable instance blocks approval. Ensure the adapter does
not interpret event families or capability-specific payloads merely to satisfy
tests.

Run the independent-head and same-head contention tests with provider call
spies. Independent plans must lack a shared global CAS key; same-head losers
must reconcile the original operation before returning the typed conflict.
Verify a committed-but-lost response returns exact recorded progress without a
second write, while partial/mixed membership closes authority.

Adversarially mutate canonical key escapes, logical-version width/case, scope
tags, host-global operation placement, mutation membership, each condition,
provider acknowledgement fields, returned keys/revisions, and every budget at
its boundary. Confirm each mutant reaches the production path and fails for its
intended reason before any forbidden provider call.

Finally, inspect imports and the diff: only the five owned files may change;
there must be no `internal/server`, SQLite/SQL, sidecar process management,
module change, raw provider type above the adapter, table-shaped API, backend
selector, rescue read, or migration behavior. This WP is an adapter boundary,
not a claim that checkpoint restore, capability migration, or M2 qualification
is complete.
