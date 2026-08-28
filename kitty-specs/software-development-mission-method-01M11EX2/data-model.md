# Data Model: Software Development Mission Method

## Model boundaries

The mission engine owns lifecycle and decisions. The artifact capability owns
content revisions and approvals. The tracker owns mutable work definition before
finalization. The workgraph capability owns immutable executable generations,
drift, and reconciliation semantics. The control store persists these
aggregates transactionally without exposing storage types to domain packages.

## MissionDefinition

| Field | Meaning | Validation |
|---|---|---|
| `definition_key` | Stable mission type key | Non-empty, closed registration |
| `definition_version` | Semantic contract version | Positive, supported exact/range policy |
| `steps` | Ordered step contracts | Unique keys; graph is connected and acyclic |
| `initial_step` | Entry step | Exists in `steps` |
| `terminal_steps` | Defined outcomes | Non-empty subset of `steps` |
| `commitment` | Canonical definition digest | SHA-256 of normalized contracts |

The initial registry contains exactly one production definition:
`software-development`. A missing, duplicate, or unsupported definition is a
typed error; no default is inferred.

## StepContract

| Field | Meaning |
|---|---|
| `step_key` | Stable identity such as `specify`, `plan`, `decompose`, `finalize_work` |
| `required_artifact_kinds` | Exact typed inputs required at current revisions |
| `produced_artifact_kinds` | Outputs this step may publish |
| `validation_policy` | Closed validator set required before approval/transition |
| `approval_policy` | Required approver class and evidence rules |
| `predecessors` / `successors` | Legal transition edges |
| `governance_action` | Action name used to resolve scoped governance |
| `mutating` | Whether executing the action may change durable state |

The Phase 3 definition exposes initialize, specify, plan, decompose, and
finalize-work as production actions. Execute and later steps are reserved in the
definition vocabulary but are not actionable until later phases.

## MissionInstance

| Field | Meaning | Invariant |
|---|---|---|
| `mission_id` | Durable mission identity | Immutable |
| `project_id` | Owning project | Immutable |
| `definition_key/version` | Selected mission law | Immutable for this phase |
| `current_step` | Current lifecycle step | Changed only by validated transition |
| `current_work_revision_id` | Published executable generation | Empty before finalization; CAS-advanced |
| `state_version` | Optimistic concurrency token | Increments on each mutation |
| `created_at` / `updated_at` | Audit timestamps | UTC, monotonic by state version |
| `terminal_outcome` | Optional defined outcome | Set only at terminal step |

## ArtifactRevision

| Field | Meaning | Invariant |
|---|---|---|
| `artifact_revision_id` | Stable revision identity | Immutable |
| `mission_id` | Owning mission | Immutable |
| `kind` | `intent`, `specification`, `plan`, or later registered kind | Closed vocabulary |
| `revision_number` | Per-kind generation | Positive and unique per mission/kind |
| `content_digest` | Commitment to exact bytes or canonical structured content | SHA-256 |
| `source_ref` | Repository-relative source locator or structured source identity | No ambient absolute authority |
| `source_revision` | Git/content revision | Non-empty |
| `created_by` / `created_at` | Provenance | Valid actor and UTC time |
| `validation` | Exact validator findings and verdict | Bound to this revision |

Artifact content may remain in Git. The control record binds identity and
approval to exact content; it does not treat a mutable path as sufficient.

## ArtifactApproval

| Field | Meaning |
|---|---|
| `approval_id` | Stable verdict identity |
| `artifact_revision_id` | Exact approved/rejected content |
| `verdict` | `approved` or `rejected` |
| `actor_id` | Accountable approver |
| `evidence_digest` | Commitment to bounded evidence set |
| `governance_context_id` | Sealed action-scoped governance |
| `submitted_at` | UTC verdict time |

An approval is fresh only when the step references exactly its
`artifact_revision_id`. Publishing a new artifact revision never mutates the
old verdict; it makes that verdict inapplicable to advancement.

## Decision and Blocker

### Decision

| Field | Meaning |
|---|---|
| `decision_id` | Stable identity |
| `mission_id` / `step_key` | Lifecycle scope |
| `question` / `options` | Bounded structured prompt data |
| `status` | `open`, `resolved`, `deferred`, or `canceled` |
| `answer` / `rationale` | Durable resolution data |
| `actor_id` / `resolved_at` | Optional resolution provenance |

### Blocker

| Field | Meaning |
|---|---|
| `blocker_id` | Stable identity |
| `mission_id` / optional `work_id` | Scope |
| `kind` | Closed machine-readable category |
| `summary` | Bounded human explanation |
| `recovery_actions` | Typed allowed recovery categories |
| `status` | `active` or `cleared` |
| `observed_at` / `cleared_at` | Audit times |

Transcript text is never the only record of a decision or blocker.

## WorkDefinitionObservation

This provider-neutral value is the finalization input.

| Field | Meaning |
|---|---|
| `work_id` | Stable Spec Kitty identity |
| `external_ref` | Provider mapping and remote identity |
| `title` / `description` / `design_context` | Executable context |
| `criteria` | Stable structured criterion records |
| `dependencies` | Stable WorkID edges |
| `write_scopes` | Declared repository ownership patterns |
| `provider_revision` | Opaque complete observation revision |
| `metadata_schema` | Provider-neutral mapping schema |
| `observed_at` | Observation boundary |

### Criterion

| Field | Meaning |
|---|---|
| `criterion_id` | Stable identity within WorkID |
| `text` | Human acceptance statement |
| `verifier_kind` | Closed verifier category |
| `required` | Whether acceptance requires a passing result |
| `metadata` | Bounded provider-neutral structured metadata |

## WorkRevision v2

| Field | Meaning | Invariant |
|---|---|---|
| `work_revision_id` | Immutable graph identity | Unique |
| `schema_version` | Encoding contract | v1 readable; v2 written |
| `project_id` / `mission_id` | Scope | Match mission |
| `generation` | Monotonic executable generation | Positive, unique per mission |
| `predecessor_id` | Prior current revision | Empty only for generation 1 |
| `planning_source_commit` | Approved plan source | Immutable Git object identity |
| `mission_definition` | Definition key/version/digest | Matches mission |
| `dependency_policy` | Satisfaction rule used by later scheduling | Closed value |
| `work_items` | Canonical executable items | Non-empty, unique IDs |
| `lanes` | Post-write-scope collapse lane DAG | Acyclic and covers every item |
| `provider_observations` | Exact external revisions | Complete for every item |
| `graph_digest` | Canonical complete graph commitment | Recomputed on decode |
| `criteria_digest` | Canonical criteria commitment | Recomputed on decode |
| `finalized_by/at` | Authority provenance | Valid actor and UTC time |
| `governance_context_id` | Sealed finalization governance | Immutable |

### Generation invariants

- A published generation is immutable.
- At most one revision is current for a mission.
- Generation 1 has no predecessor; every later generation names the exact
  previously current revision.
- Publication compares the expected current revision and generation.
- The graph is validated after lane collapse and before publication.
- Equal semantic inputs produce equal canonical bytes and digests.

## DriftReport

| Field | Meaning |
|---|---|
| `drift_report_id` | Stable observation identity |
| `mission_id` | Scope |
| `base_work_revision_id` | Current immutable contract |
| `candidate_digest` | Normalized observed graph digest |
| `observed_at` | Provider observation boundary |
| `changes` | Canonically sorted field changes |
| `affected_old_work_ids` | Old items requiring disposition if active/accepted |
| `affected_new_work_ids` | Candidate items implicated by changes |
| `semantic_status` | `clean`, `observation_only`, or `changed` |
| `provider_revisions` | Audit evidence for the observation |

### DriftChange

`change_kind` is `added`, `removed`, or `modified`. Modified records name a
closed field path such as description, criteria, dependency, write scope,
mapping, or metadata schema and retain old/new canonical commitments rather than
unbounded private content.

## ReconciliationPlan

| Field | Meaning | Invariant |
|---|---|---|
| `reconciliation_id` | Operation/idempotency identity | Same ID + same digest replays |
| `mission_id` | Scope | Matches both revisions |
| `old_revision_id` | Expected current authority | CAS checked |
| `new_revision_id` | Successor candidate | Generation increases |
| `drift_report_id` | Evidence source | Matches old/candidate digests |
| `decisions` | Per-work dispositions | Exactly covers affected active/accepted work |
| `actor_id` / `rationale` | Human authority and explanation | Required |
| `governance_context_id` | Sealed reconciliation governance | Required |
| `occurred_at` | Decision time | UTC |
| `request_digest` | Canonical idempotency digest | Recomputed on decode |

### WorkDispositionDecision

| Field | Meaning |
|---|---|
| `old_work_id` | Affected prior identity |
| `disposition` | `continue`, `supersede`, `invalidate`, or `fork` |
| `new_work_ids` | Related successor identities |
| `fork_edges` | Explicit ancestry when disposition is `fork` |
| `rationale` | Item-specific justification |
| `consequences` | Derived per known work status, not caller-authored |

Integrated work always receives `preserves_integrated_history=true`. Any needed
change is represented by new work; the prior candidate and evidence remain.

## GovernanceContextRef

The existing sealed aggregate remains authoritative:

- `context_id`, schema version, aggregate algorithm/digest, created time;
- ordered entries with kind, stable ref, revision, and content commitment;
- closed kinds for config, doctrine, profile, and mission type.

Mission actions reference the context; they do not duplicate secrets or mutable
source paths.

## NextResult

| Field | Meaning |
|---|---|
| `schema_version` | Wire/result contract |
| `kind` | One closed next-outcome kind |
| `mission_id` | Durable scope |
| `mission_state_version` | Snapshot version used for decision |
| `step_key` | Current lifecycle step |
| `work_revision_id` | Exact generation when applicable |
| `action` | Typed action descriptor when applicable |
| `decision` / `blocker` | Stable reference and bounded data when applicable |
| `ready_work_ids` | Canonical ready identities, never provider IDs |
| `governance_requirement` | Action name/profile class required to mutate |
| `diagnostic` | Optional stable typed diagnostic |

`NextResult` is not persisted merely because it was queried. It is reproducible
from one consistent snapshot. Actions use separate operation records.

## State transitions

### Mission lifecycle in Phase 3

```text
initialized
  -> specifying
  -> specification_approved
  -> planning
  -> plan_approved
  -> decomposing
  -> work_draft_ready
  -> work_finalized

Any nonterminal step may expose:
  decision_required
  blocked

work_finalized + semantic drift
  -> reconciliation_required
  -> work_finalized (successor generation)
```

Later execution/review/accept/merge states are reserved by the mission
definition but not entered by Phase 3 production commands.

### Artifact lifecycle

```text
published -> validated_pass -> approved
          -> validated_fail
          -> rejected

new revision published -> prior approval remains historical but stale for next transition
```

### Reconciliation lifecycle

```text
drift_observed
  -> decisions_incomplete
  -> ready_to_publish
  -> published

Any expected-generation mismatch -> conflict (no partial successor)
```

## Persistence ownership

Domain packages define store ports around aggregate operations. The SQLite
adapter owns schema and SQL. Transactions are aggregate-scoped:

- publish artifact plus validation event;
- approve exact artifact revision;
- transition mission under expected state version;
- reserve decomposition saga operation identity and request digest before
  external effects;
- finalize revision under expected current generation;
- publish successor revision plus complete reconciliation plan and consequences.

Provider work mutation and observation remain outside these transactions and use
the existing saga/idempotency rules. Before those effects, the local store
immutably reserves the decomposition root `OperationID`, mission, operation kind,
and complete request digest in `phase3_operations`. This reservation is not a
tracker lease, update fence, or provider capability; it prevents changed-command
replay and permits exact partial-saga resumption across process restart.
