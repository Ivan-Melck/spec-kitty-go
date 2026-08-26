# Contract: Tracker Capabilities

This contract is provider-neutral. Go signatures are illustrative but encode required ownership, idempotency, and error semantics.

## Capability discovery

```go
type Inspector interface {
    Inspect(context.Context, InspectRequest) (Inspection, error)
}

type Inspection struct {
    Binding      TrackerBinding
    Capabilities ProviderCapabilities
    Health       Health
    Diagnostics  []Diagnostic
}
```

`Inspect` is non-mutating. It must not initialize a provider, install hooks, edit agent files, repair state, or silently select a different workspace.

## Catalog

```go
type Catalog interface {
    Create(context.Context, CreateWorkRequest) (WorkMutation, error)
    Read(context.Context, ExternalWorkRef) (WorkRecord, error)
    Update(context.Context, UpdateWorkRequest) (WorkMutation, error)
}
```

### Create

- Requires a stable `WorkID`, `OperationID`, project binding, title, and metadata schema.
- Replay with the same operation and same semantic input returns the original mapping as a no-op.
- Same operation with different semantic input returns `invalid_request`.
- An ambiguous result must be observed through namespaced mapping/operation metadata before retry.
- It must not create sequential IDs by configuration when concurrent creation is enabled.

### Read

- Returns a complete known provider observation and opaque revision.
- Unknown optional provider fields are ignored.
- Missing or conflicting mapping metadata produces a diagnostic; it is never silently rewritten.

### Update

- Declares its guard level in capabilities.
- If the request requires a stronger guard than the provider supplies, it returns `guarded_update_unsupported` before mutation.
- Metadata updates merge the `spec_kitty` namespace and preserve unrelated keys.

## Graph

```go
type Graph interface {
    PutBlockingDependency(context.Context, DependencyRequest) (DependencyMutation, error)
    RemoveBlockingDependency(context.Context, DependencyRequest) (DependencyMutation, error)
    ListDependencies(context.Context, ExternalWorkRef) ([]DependencyRef, error)
    ReadyProjection(context.Context, ReadyRequest) (ReadyProjection, error)
}
```

- `BlockedWorkID` depends on `BlockingWorkID`; adapters must not reverse the edge.
- Only blocking dependencies affect the provider-neutral graph.
- Provider readiness is diagnostic. The caller compares it with the pinned `WorkRevision`.
- Cycle/provider validation errors translate to stable `invalid_request` or `revision_conflict`.

## Discussion

```go
type Discussion interface {
    Append(context.Context, AppendDiscussionRequest) (DiscussionMutation, error)
    List(context.Context, ExternalWorkRef) ([]DiscussionEntry, error)
}
```

- Requests include `OperationID`, body, author mapping, and expected observation.
- Exact marker/body replay is a no-op only if provider observation proves the original entry.
- If marker attachment/query is not atomic, an ambiguous append is not retried automatically.
- Routine diagnostics never include comment bodies.

## Assignment projection

```go
type AssignmentProjection interface {
    ObserveAssignment(context.Context, ExternalWorkRef) (AssignmentObservation, error)
    ProjectAssignment(context.Context, AssignmentRequest) (AssignmentMutation, error)
    CompensateAssignment(context.Context, CompensationRequest) (AssignmentMutation, error)
}
```

### ProjectAssignment

- Does not acquire, extend, or validate a Spec Kitty lease.
- Requires an already authorized application request containing `OperationID`.
- Returns guarantee level and actual post-observation.
- An atomic provider claim may be used only when its actor/status semantics match the requested projection.
- Unguarded assignment must be explicitly requested by policy; the adapter cannot represent it as CAS.

### CompensateAssignment

The adapter may mutate only when a provider primitive enforces all of:

1. expected provider observation/revision;
2. expected current assignee/value;
3. the operation marker being compensated.

If any element cannot be enforced at the mutation boundary, return `safe_compensation_unsupported` or `attribution_lost` and leave provider state untouched.

## Exporter

```go
type Exporter interface {
    ExportPortable(context.Context, ExportRequest, io.Writer) (ExportResult, error)
    Backup(context.Context, BackupRequest) (BackupResult, error)
    InspectBackup(context.Context, BackupInspectRequest) (BackupInspection, error)
}
```

- Portable JSONL export is not called a full backup.
- Restorable backup uses the provider's supported native backup mechanism.
- Destructive restore is outside automatic application flow and appears only in explicit qualification/operator documentation.

## Portable error vocabulary

| Kind | Meaning | Automatic retry |
|---|---|---|
| `unavailable` | Provider cannot be reached/spawned | bounded policy only |
| `timeout` | Deadline expired; mutation may be ambiguous | observe first |
| `ambiguous` | Apply status is unknown | observe/reconcile |
| `version_skew` | Executable version unsupported | never |
| `schema_skew` | JSON schema unsupported | never |
| `malformed_protocol` | Output violated contract | never without operator fix |
| `workspace_mismatch` | Resolved workspace differs from binding | never |
| `lock_contended` | Supported writer lock unavailable | bounded retry |
| `revision_conflict` | Observation changed | re-read/reconcile |
| `attribution_lost` | Current state is not attributable | never mutate |
| `safe_compensation_unsupported` | Provider cannot enforce compensation guard | never mutate |
| `not_found` | External work absent | policy/reconcile |
| `invalid_request` | Caller input violates contract | never |

Every error carries a stable recovery code. Provider-specific stderr text is cause detail only and is not used for branching.

## Conformance requirements

Every implementation declares capabilities and runs:

1. validation and deterministic encoding tests;
2. create/read/update round-trip for declared catalog levels;
3. mapping collision and replay tests;
4. dependency direction/readiness tests;
5. discussion replay/ambiguity tests for declared level;
6. assignment/human-change/compensation tests for declared guarantee;
7. unavailable/timeout/malformed/stale/wrong-state tests;
8. universal Spec Kitty authority assertions where the provider participates in execution scenarios.

Tests skip only capability operations the provider explicitly declares unsupported; they still assert that unsupported requests fail before mutation.
