# Work Packages: GapDB Control-Journal Proving Ground

## Mission outcome

Prove or falsify one narrow, production-shaped GapDB sidecar journal and recovery
slice. The mission publishes one durable atomic product batch, deterministic
checkpoint-plus-suffix recovery, side-effect-free playback, restart epochs,
lease-scoped workspace replacement, attribution-safe effects, independent
review, serialized integration, and one decisive source-bound torture trial.

This mission does not adopt GapDB for production, begin M2, alter SQLite, add a
fallback backend, or claim exactly-once provider effects.

## Execution and branch contract

- Planning/base branch: `kitty/mission-gapdb-control-journal-proving-ground-01M18EVR`
- Final merge target: `main`
- Strategy: allocate computed lanes, integrate accepted WPs into the mission
  branch, run the terminal mission gate there, then merge the mission to `main`.
- All WPs use `execution_mode: code_change` and agent `codex`.
- `go.mod` and `go.sum` belong only to WP03.
- No WP may edit `internal/controlstore/sqlite/**`, import GapDB internal/server
  packages, add SQLite fallback, or introduce current-batch revision values,
  global projection-head CAS, or ordinary runtime-epoch CAS.
- Subtask rows are references, not Markdown task checkboxes. Status remains in
  the canonical event log.

## Work package graph

```text
WP01 frozen contracts and guards
  └─ WP02 journal model, codec, reducer
       └─ WP03 public GapDB adapter and 14 scenarios
            ├─ WP04 sidecar lifecycle and response-drop proxy
            └─ WP06 checkpoint, restore, epoch, playback
                 └──────────────┐
WP04 + WP02 + WP03 ──> WP05 application batch and ambiguity
WP05 + WP06 ─────────> WP07 authority, workspace, zombie composition
WP05 + WP07 ─────────> WP08 effects, review, integration
WP06 + WP07 + WP08 ──> WP09 fault and mutant campaign
WP09 ─────────────────> WP10 one-RunID trial, evidence, adoption gate
```

## Subtask index

| ID | Description | WP |
|---|---|---|
| T001 | Freeze executable product budgets and contract fixtures | WP01 |
| T002 | Strengthen schema and semantic contract tests | WP01 |
| T003 | Enforce GapDB import and forbidden-backend boundaries | WP01 |
| T004 | Add architecture-guard mutant controls | WP01 |
| T005 | Run the frozen-contract qualification slice | WP01 |
| T006 | Define journal identities, records, events, and heads | WP02 |
| T007 | Implement bounded versioned codecs | WP02 |
| T008 | Implement deterministic reducers and stable digests | WP02 |
| T009 | Enforce event order and aggregate-local revisions | WP02 |
| T010 | Model operation outcomes and effect intents | WP02 |
| T011 | Qualify pure-model invariants and mutants | WP02 |
| T012 | Implement the admitted public GapDB port | WP03 |
| T013 | Implement atomic batch, CAS, scan, and watch semantics | WP03 |
| T014 | Expose inspect, lease, review, workflow, and integration ports | WP03 |
| T015 | Preserve ambiguous outcomes and bounded diagnostics | WP03 |
| T016 | Run the exact fourteen external-adapter scenarios | WP03 |
| T017 | Prove dependency, budget, and API admission | WP03 |
| T018 | Verify immutable sidecar provenance before execution | WP04 |
| T019 | Implement exact process and socket lifecycle ownership | WP04 |
| T020 | Verify Status, Health, and DescribeConfig attribution | WP04 |
| T021 | Implement the real response-drop proxy for GJ-02 | WP04 |
| T022 | Harden cleanup, restart, and stale-process diagnostics | WP04 |
| T023 | Qualify lifecycle and fake-timeout mutants | WP04 |
| T024 | Build one atomic application publication batch | WP05 |
| T025 | Implement stable operation identity and retry reconciliation | WP05 |
| T026 | Implement per-aggregate CAS without global serialization | WP05 |
| T027 | Admit a new runtime epoch only at owner restart | WP05 |
| T028 | Reconcile lost responses by ordinary reads | WP05 |
| T029 | Prove GJ-01 and GJ-02 application invariants | WP05 |
| T030 | Build prior-prefix checkpoints with explicit watermarks | WP06 |
| T031 | Restore checkpoint plus ordered journal suffix | WP06 |
| T032 | Implement zero-I/O playback and digest comparison | WP06 |
| T033 | Enforce restore bounds and stale-restart caps | WP06 |
| T034 | Refuse predecessor-epoch capsules after restart | WP06 |
| T035 | Qualify checkpoint, ordering, and playback mutants | WP06 |
| T036 | Compose two implementation actors and one authority per revision | WP07 |
| T037 | Bind W1/W2 durably and inject GJ-03/GJ-04 | WP07 |
| T038 | Quarantine W1 and issue fresh W3 to Process C for Actor A | WP07 |
| T039 | Refuse zombie and predecessor-epoch authority attempts | WP07 |
| T040 | Preserve W2 and reconcile orphaned physical workspaces | WP07 |
| T041 | Qualify concurrent claims and zombie isolation | WP07 |
| T042 | Journal effect intent before provider observation | WP08 |
| T043 | Compare before compensating ambiguous provider effects | WP08 |
| T044 | Enforce ActorID-independent candidate review | WP08 |
| T045 | Serialize integration and recover from Git facts | WP08 |
| T046 | Qualify GJ-05, reassignment, review, and integration races | WP08 |
| T047 | Implement five deterministic fault-cut runners | WP09 |
| T048 | Implement eight focused campaigns with at least 100 repetitions | WP09 |
| T049 | Kill the exact nineteen architectural mutants | WP09 |
| T050 | Assert universal invariants on every terminal attempt | WP09 |
| T051 | Verify honest incomplete/failure evidence under faults | WP09 |
| T052 | Execute the entire ordered one-RunID crown trial | WP10 |
| T053 | Emit and validate the exact evidence census and signature | WP10 |
| T054 | Build the proving-ground operator CLI | WP10 |
| T055 | Implement fail-closed recovery and adoption decisions | WP10 |
| T056 | Prove source binding, artifact digests, and human signature handling | WP10 |
| T057 | Run the mission exit gate and stop before M2 on any failure | WP10 |

## Work package summaries

### WP01 — Frozen Journal Contracts and Architecture Guards

**Goal**: Make the admitted tuple, product budgets, evidence predicates, and
dependency boundary executable before implementation begins.

**Dependencies**: None

**Independent test**: Contract schemas compile, positive fixtures pass,
vacuous evidence fails, forbidden imports are detected, and each guard has a
mutant proving it can fail.

**Prompt**: `tasks/WP01-frozen-journal-contracts-and-guards.md`

### WP02 — Pure Journal Model, Codec, and Reducer

**Goal**: Provide the deterministic, storage-neutral semantic core.

**Dependencies**: WP01

**Independent test**: Equal ordered histories yield equal bytes and digests;
invalid versions, orders, bounds, and global serialization attempts fail.

**Prompt**: `tasks/WP02-pure-journal-model-codec-and-reducer.md`

### WP03 — Public GapDB Adapter and External Scenarios

**Goal**: Connect only through the admitted public GapDB surface and pass the
exact fourteen external-adapter scenarios.

**Dependencies**: WP02

**Independent test**: All fourteen named scenarios pass against the exact
sidecar tuple with bounded values and no forbidden dependency.

**Prompt**: `tasks/WP03-public-gapdb-adapter-and-external-scenarios.md`

### WP04 — Exact Sidecar Lifecycle and Response-Drop Proxy

**Goal**: Own the admitted daemon safely and create a real ambiguous response.

**Dependencies**: WP03

**Independent test**: Provenance is verified-to-exec, runtime endpoints are
actual configured endpoints, and GJ-02 drops a fully consumed response.

**Prompt**: `tasks/WP04-exact-sidecar-lifecycle-and-response-drop-proxy.md`

### WP05 — Atomic Application Batch and Ambiguity Reconciliation

**Goal**: Publish one product command atomically and reconcile lost responses.

**Dependencies**: WP02, WP03, WP04

**Independent test**: GJ-01 preserves all prior state exactly; GJ-02 produces
one durable outcome and retry/read reconciliation without double apply.

**Prompt**: `tasks/WP05-atomic-application-batch-and-ambiguity.md`

### WP06 — Checkpoint, Restore, Epoch, and Playback

**Goal**: Recover product state, not merely bytes, and prove zero-I/O playback.

**Dependencies**: WP02, WP03

**Independent test**: Checkpoint-plus-suffix restore and full playback produce
the same digest with zero adapter calls and zero durable writes during playback.

**Prompt**: `tasks/WP06-checkpoint-restore-epoch-and-playback.md`

### WP07 — Authority, Workspace, and Zombie Composition

**Goal**: Preserve safe two-agent concurrency when process and filesystem state
disagree.

**Dependencies**: WP05, WP06

**Independent test**: W1 is quarantined, W3 is fresh for Process C/Actor A, W2
survives, and no stale writer obtains authority.

**Prompt**: `tasks/WP07-authority-workspace-and-zombie-composition.md`

### WP08 — Effects, Independent Review, and Serialized Integration

**Goal**: Finish the mission path without attribution, review, or integration
authority leakage.

**Dependencies**: WP05, WP07

**Independent test**: Provider ambiguity never causes unsafe compensation,
reviewers differ from all candidate contributors by ActorID, and one integration
publication succeeds at a time.

**Prompt**: `tasks/WP08-effects-independent-review-and-integration.md`

### WP09 — Fault and Mutant Qualification Campaign

**Goal**: Attack every critical cut and architecture law with repeatable tests.

**Dependencies**: WP06, WP07, WP08

**Independent test**: Five cuts terminate honestly, eight campaigns each pass
at least 100 repetitions, and all nineteen named mutants are killed.

**Prompt**: `tasks/WP09-fault-and-mutant-qualification-campaign.md`

### WP10 — One-RunID Crown Trial and Adoption Gate

**Goal**: Run the complete ordered trial, emit exact evidence, and enforce the
human adoption boundary.

**Dependencies**: WP09

**Independent test**: A source-bound manifest contains exactly 14 scenario
records, five fault records, eight campaign summaries, nineteen killed mutants,
one ordered trial, invariants, review, artifacts, and the exact exit signature.

**Prompt**: `tasks/WP10-one-runid-crown-trial-and-adoption-gate.md`

