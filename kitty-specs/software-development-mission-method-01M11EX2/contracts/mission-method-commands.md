# Mission Method Application Contracts

These are application command/query contracts, not a promise of a production
CLI or HTTP surface in Phase 3. The internal helper composition root exercises
their JSON envelopes for qualification. Later transports must project the same
types without acquiring authority.

## Common envelope laws

Every result contains:

- `schema_version`;
- `correlation_id` for observability;
- `operation_id` for a mutating command, absent for a pure query;
- the aggregate identities affected;
- a typed success payload or typed diagnostic;
- explicit retry safety.

Human messages are explanatory only. Callers branch on closed kinds and typed
data. Same operation ID plus the same canonical request digest replays; the same
operation ID plus different content conflicts.

## `mission initialize`

**Kind**: command, idempotent mutation  
**Authority**: actor, process, and sealed governance context

**Input**: project, mission, explicit definition key/version, actor, process,
sealed governance context, operation, time

**Output**: initialized mission identity, definition commitment, bound creation
authority, state version, and next-result projection

**Refusals**: missing/unsupported definition, duplicate conflicting mission,
invalid governance context

No default mission type is inferred. The request commitment and durable mission
record bind the exact actor, process, and complete sealed governance context.
Idempotent replay restores that original authority evidence; missing or changed
provenance fails closed.

## `artifact publish`

**Kind**: command, idempotent mutation  
**Authority**: actor plus mission action governance  
**Input**: mission, artifact kind, content commitment, source reference, source
revision, validation result  
**Output**: immutable artifact revision and current applicability  
**Refusals**: unsupported kind, invalid source identity, digest mismatch,
validation/result schema error

Publishing creates a new revision and never updates an existing approval.

## `artifact approve`

**Kind**: command, idempotent mutation  
**Authority**: approver role required by current step contract  
**Input**: exact artifact revision, verdict, evidence digest, governance context  
**Output**: immutable approval record and resulting transition eligibility  
**Refusals**: stale/non-current revision, validation failure, unauthorized actor,
evidence mismatch

## `mission transition`

**Kind**: command, idempotent mutation  
**Authority**: actor and action-scoped governance  
**Input**: mission, exact expected `MissionSnapshot`, requested successor step,
referenced artifact revisions, operation identity

**Output**: new step and state version  
**Refusals**: illegal edge, missing/stale approval, open decision, active blocker,
expected-version conflict

Tracker statuses and projection files are never inputs.

## `mission decompose`

**Kind**: saga command  
**Authority**: exact `plan_approved` mission snapshot plus approved plan
**Input**: mission, exact plan artifact, provider binding, proposed stable work
definitions, operation identity  
**Output**: mapped tracker work observations and validation report  
**Refusals**: plan mismatch, provider unavailable/ambiguous, coverage/quality
failure, mapping conflict

Provider effects use the existing tracker operation marker and observation
rules. The command performs no mission transition. After a complete successful
result, the caller submits two ordinary `mission transition` commands with
distinct operation IDs and exact snapshots: `plan_approved` to `decomposing`,
then `decomposing` to `work_draft_ready`. A partial, failed, or ambiguous result
submits neither transition and remains a recoverable saga.

## `work finalize`

**Kind**: command following provider observation  
**Authority**: finalize-work step plus sealed governance  
**Input**: mission, expected current generation, planning source commit,
provider observations, proposed WorkRevision identity, operation identity  
**Output**: published WorkRevision v2 and state version  
**Refusals**: provider changed during observation, incomplete coverage, invalid
work/criteria/dependencies/write scopes, work/lane cycle, generation conflict,
digest mismatch

The command publishes generation 1 only when no current generation exists. A
later generation requires reconciliation.

## `work drift`

**Kind**: query with external observation; no local authority mutation  
**Input**: mission/current revision and a normalized provider observation  
**Output**: structured `DriftReport` with `clean`, `observation_only`, or
`changed` semantic status  
**Refusals**: unavailable/incomplete provider observation, incompatible metadata
schema, corrupt current revision

The report is untrusted observation data. The query neither assigns it an
authoritative local identity nor persists it. It becomes durable evidence only
if `work reconcile` validates and commits that exact report atomically with the
decisions and successor generation.

## `work reconcile`

**Kind**: command, atomic local publication after provider observation  
**Authority**: explicit reconciliation actor and governance  
**Input**: expected old revision, successor WorkRevision, exact typed drift
report, exactly covering per-work decisions, rationales, operation identity  
**Output**: reconciliation record, successor generation, derived consequences,
and required later authority actions  
**Refusals**: incomplete/duplicate/extra decisions, illegal disposition,
invalid fork ancestry, semantic change under unjustified continue, stale current
generation, digest mismatch

The command validates the supplied observation against the expected old
revision and exact successor, then atomically persists the drift report,
decisions, reconciliation, successor, current pointer, event, and outbox. There
is no separate persist-drift command. It does not itself manipulate Phase 4
workspaces; it records typed `expire_authority` and `quarantine_workspace`
consequences for later consumers.

## `mission next`

**Kind**: pure query  
**Input**: mission identity  
**Output**: one value conforming to `next-result.schema.json`  
**Refusals**: mission not found, unsupported definition, corrupt state snapshot

Repeated calls without intervening state change return an equivalent result and
write nothing. A result naming an action is not authority to execute it.

## `projection tasks`

**Kind**: query/render operation  
**Input**: mission identity and either in-memory output or an explicit trusted
root plus a repository-relative output destination

**Output**: UTF-8 Markdown bytes or atomically replaced file, containing source
mission state version, WorkRevision ID/digest, drift status, render time, and an
explicit non-authoritative warning  
**Refusals**: no finalized revision, corrupt canonical state, unsafe output path,
atomic replacement failure

There is intentionally no inverse `tasks import` or `tasks apply` contract.

## Diagnostic minimum

Every failure names:

- stable `kind`;
- affected aggregate identities;
- observed and expected state where safe;
- retry safety;
- bounded recovery categories;
- no credentials, raw environment, arbitrary provider output, or transcript
  content.
