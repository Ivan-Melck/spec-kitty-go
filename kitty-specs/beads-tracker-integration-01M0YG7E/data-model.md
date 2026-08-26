# Data Model: Beads Tracker Integration

This document defines provider-neutral values and the Beads translation boundary. Names are design-level and may be refined during implementation, but authority and invariants are normative.

## 1. TrackerBinding

Project-scoped configuration selecting one mutable work catalog.

| Field | Type | Rule |
|---|---|---|
| `ProjectID` | durable ID | Required; one writable binding per project |
| `Provider` | string enum | `beads` for this adapter |
| `ExecutablePath` | absolute path | Resolved before invocation; no PATH re-resolution after binding |
| `ExecutableVersion` | semantic version | Initially exactly `1.2.2` |
| `ExecutableDigest` | SHA-256 | Required for qualified/managed installations |
| `ProtocolSchema` | uint | Initially exactly `1` |
| `WorkspacePath` | absolute canonical path | Required and shared by every linked worktree |
| `WorkspaceIdentity` | opaque digest | Derived from canonical provider info; detects mismatched resolution |
| `ConcurrencyMode` | enum | `embedded_serialized` or `server_multiwriter` |
| `ReadOnly` | bool | Mutating ports refuse when true |
| `ObservedAt` | UTC timestamp | Diagnostic freshness only |

### Invariants

1. A project has at most one writable primary `TrackerBinding`.
2. `WorkspacePath` is absolute, cleaned, symlink-evaluated where possible, and never inferred independently from a lane worktree.
3. Version, digest, protocol, workspace identity, and concurrency mode must pass preflight before the first mutation in a process.
4. Discovery does not create or repair a binding.

## 2. ProviderCapabilities

An explicit contract, not a marketing version proxy.

| Capability | Level values | Beads v1.2.2 |
|---|---|---|
| `CatalogRead` | unsupported / supported | supported |
| `CatalogCreate` | unsupported / supported | supported |
| `CatalogUpdate` | unsupported / unguarded / guarded | unguarded |
| `Graph` | unsupported / supported | supported |
| `Discussion` | unsupported / append / idempotent | append; idempotency depends on observable marker |
| `Assignment` | unsupported / unguarded / atomic-claim | atomic-claim for provider actor; other updates unguarded |
| `ObservationRevision` | unsupported / content-derived / provider-native | content-derived |
| `SafeCompensation` | unsupported / conditional | unsupported for arbitrary assignment cleanup |
| `ConcurrentWriters` | none / adapter-serialized / provider-native | adapter-serialized in embedded; provider-native in qualified server mode |
| `PortableExport` | unsupported / supported | supported JSONL |
| `RestorableBackup` | unsupported / supported | supported Dolt-native filesystem backup |

### Invariants

- A weaker capability is never promoted because an adapter can retry.
- Application services branch on typed levels, not provider names.
- Unsupported safety capability yields a typed result before the unsafe mutation.

## 3. ExternalWorkRef

Stable mapping between Spec Kitty identity and the provider.

| Field | Type | Rule |
|---|---|---|
| `ProjectID` | durable ID | Required |
| `WorkID` | durable ID | Internal authority identity |
| `Provider` | string | `beads` |
| `ExternalID` | string | Hash-style Beads issue ID |
| `MetadataSchema` | uint | Starts at 1 |
| `CreatedByOperation` | `OperationID` | Required for adapter-created work |
| `LastObservation` | `ObservationRevision` | Updated after confirmed reads/writes |

### Invariants

1. `(ProjectID, WorkID, Provider)` and `(ProjectID, Provider, ExternalID)` are each unique.
2. Rebinding never changes `WorkID`.
3. Conflicting provider metadata is a reconciliation conflict, not an automatic remap.

## 4. Criterion

Structured acceptance definition rendered to Beads but evaluated by Spec Kitty.

| Field | Type | Rule |
|---|---|---|
| `ID` | stable criterion ID | Unique within work |
| `Text` | string | Non-empty, bounded |
| `Verifier` | enum | test, review, policy, human decision, or future known value |
| `Required` | bool | Required criteria gate Spec Kitty acceptance |
| `EvidenceHint` | optional string | Guidance only; not a verdict |

Criteria are rendered as readable lines in Beads' acceptance field and encoded structurally inside the namespaced metadata. Beads status, labels, and checkboxes do not satisfy a criterion.

## 5. WorkRecord

Provider-neutral work-catalog projection.

| Field | Type | Ownership |
|---|---|---|
| `WorkID` | durable ID | Spec Kitty |
| `ExternalRef` | `ExternalWorkRef` | mapping |
| `Title` | string | primary tracker before finalization |
| `Description` | string | primary tracker before finalization |
| `DesignContext` | optional string | primary tracker |
| `Criteria` | ordered `Criterion[]` | primary tracker before finalization |
| `Dependencies` | sorted `DependencyRef[]` | primary tracker before finalization |
| `VisibleAssignee` | optional external subject | tracker projection |
| `AssignmentMarker` | optional operation marker | tracker projection |
| `Comments` | ordered `DiscussionEntry[]` | tracker |
| `ProviderUpdatedAt` | optional timestamp | diagnostic |
| `ObservationRevision` | uint64 opaque token | adapter observation |

After a mission finalizes a `WorkRevision`, later `WorkRecord` changes create drift/reconciliation input. They do not mutate the running revision.

## 6. DependencyRef

| Field | Type | Rule |
|---|---|---|
| `BlockedWorkID` | `WorkID` | Work that cannot become ready |
| `BlockingWorkID` | `WorkID` | Prerequisite work |
| `Kind` | enum | `blocks` is the only scheduling kind in this mission |
| `ExternalEdgeID` | optional provider ref | Diagnostic only |

### Invariants

- Dependency direction is always “blocked depends on blocker.”
- Non-blocking Beads edges may be observed as provenance but never enter scheduler readiness.
- Provider readiness is compared with the pinned graph and reported as diagnostic agreement/disagreement.

## 7. DiscussionEntry

| Field | Type | Rule |
|---|---|---|
| `ExternalCommentID` | optional string | Provider identity when available |
| `Author` | external subject | Human-visible attribution |
| `Body` | string | Preserved in provider; excluded from routine diagnostics |
| `OperationMarker` | optional marker | Required for adapter idempotency claims |
| `CreatedAt` | timestamp | Provider observation |
| `ObservationRevision` | opaque token | Changes when comment set/order changes |

The operation marker is encoded in a documented namespaced comment convention or metadata association. If v1.2.2 cannot atomically append body plus queryable marker, the adapter declares append-only/ambiguous semantics and recovery requires reconciliation before replay.

## 8. ProviderObservation

A complete known-state snapshot used for comparison and recovery.

| Field | Type | Rule |
|---|---|---|
| `ExternalID` | string | Required |
| `CanonicalFields` | versioned byte encoding | Stable field order and normalization |
| `Revision` | uint64 | Digest of canonical fields with nonzero reserved handling |
| `ProviderUpdatedAt` | optional timestamp | Diagnostic, included in canonical encoding |
| `Assignee` | string | Exact observed value |
| `OperationMarker` | string | Exact observed namespaced value |
| `ObservedAt` | UTC timestamp | Local observation time |

### Canonicalization

1. Start with encoding schema version.
2. Encode scalar strings with length prefixes, not delimiters.
3. Sort map keys, labels, dependency tuples, and any semantically unordered arrays.
4. Preserve comment order where order is provider-significant.
5. Include absence distinctly from an empty value.
6. Hash with SHA-256 and derive the uint64 token from the first eight bytes using fixed byte order; retain the full digest in adapter diagnostics/evidence when collision disambiguation is needed.

## 9. ProviderCommand

Closed typed subprocess request.

| Field | Type | Rule |
|---|---|---|
| `Kind` | closed enum | version, where, info, doctor, show, list, ready, create, update, dep-add, dep-list, comments, backup, export |
| `Arguments` | typed command-specific value | Validated before rendering argv |
| `WorkspacePath` | absolute path | Must match binding |
| `Timeout` | duration | Positive; default <= 30 s |
| `StdoutLimit` | bytes | Positive bounded value |
| `StderrLimit` | bytes | Positive bounded value |
| `ExpectedShape` | object / array / JSONL / empty | Command contract |
| `Mutating` | bool | Triggers preflight and writer-mode checks |

Raw arbitrary argv is not exposed outside the adapter.

## 10. ProviderCommandResult

| Field | Type | Rule |
|---|---|---|
| `Kind` | command kind | Required |
| `ExitCode` | integer/none | None if spawn failed |
| `SchemaVersion` | uint | From envelope/error |
| `Payload` | decoded typed result | Present on success |
| `ErrorCode` | optional stable provider code | Translated to tracker error kind |
| `Duration` | duration | Evidence/diagnostics |
| `StdoutTruncated` | bool | Truncation makes result malformed/ambiguous |
| `StderrTruncated` | bool | Reported without payload content |
| `MutationOutcome` | enum | not_started, not_applied, applied, ambiguous, unknown |

### Mutation outcome rules

- Spawn/validation failure before process start: `not_started`.
- Documented provider rejection with JSON error: `not_applied` unless command contract says otherwise.
- Valid successful JSON response and post-observation: `applied`.
- Timeout, cancellation, signal, truncation, malformed success output, or transport loss after start: `ambiguous` until observation resolves it.

## 11. TrackerDiagnostic

Stable provider-neutral operator result.

| Field | Type |
|---|---|
| `Kind` | unavailable, timeout, version_skew, schema_skew, malformed_protocol, workspace_mismatch, lock_contended, revision_conflict, attribution_lost, safe_compensation_unsupported, ambiguous, invalid_request |
| `OperationID` | optional |
| `WorkID` / `ExternalID` | optional |
| `ExpectedRevision` / `ObservedRevision` | optional |
| `ExpectedValue` / `ObservedValue` | optional redacted-safe scalar |
| `Capability` | optional |
| `RecoveryCode` | stable machine-readable action class |
| `Message` | concise human explanation without secrets or bodies |

## 12. ReconciliationConflict

Durable local record attached to the existing external-effect/recovery model.

| Field | Type | Rule |
|---|---|---|
| `ConflictID` | durable ID | Unique |
| `OperationID` | durable ID | Required |
| `WorkID` | durable ID | Required |
| `Provider` / `ExternalID` | strings | Required |
| `Kind` | diagnostic kind | Required |
| `ExpectedObservation` | optional snapshot ref | What the saga intended |
| `ActualObservation` | optional snapshot ref | Last proven provider state |
| `Disposition` | state enum | pending, continue, supersede, invalidate, fork, manually_repaired |
| `CreatedAt` / `ResolvedAt` | timestamps | `ResolvedAt` only when terminal |

Provider conflict does not revoke a valid execution lease unless an explicit application policy says the run precondition itself is invalid.

## 13. QualificationEvidence

| Field | Type |
|---|---|
| `SchemaVersion` | 1 |
| `SourceCommit` / `SourceTree` | Git object IDs |
| `ProviderVersion` | `1.2.2` |
| `ProviderDigestSHA256` | lowercase hex |
| `ProtocolSchema` | 1 |
| `StorageMode` | embedded_serialized / server_multiwriter |
| `Host` | GOOS/GOARCH and non-secret runner identity |
| `StartedAt` / `FinishedAt` | UTC timestamps |
| `Scenarios` | exact started/terminal/pass/fail census |
| `Latency` | sample count, p50, p95, max for named operations |
| `AuthorityInvariantFailures` | integer; must be zero |
| `Verdict` | pass/fail/non_admissible |

## State transitions

### Binding

```text
unconfigured -> discovered -> compatible -> healthy
                    |             |
                    v             v
               incompatible    degraded/unavailable
```

Only `healthy` permits mutation. Read-only inspection may operate in `compatible` or `degraded` when the command itself is safe.

### External mutation

```text
intent_recorded
  -> command_started
  -> confirmed
  -> replay_confirmed

command_started
  -> ambiguous
  -> observed_applied | observed_unchanged | external_conflict | unavailable
```

### Compensation

```text
pending
  -> compensated
  -> attribution_lost
  -> safe_compensation_unsupported
  -> ambiguous
  -> unavailable
```

`safe_compensation_unsupported` is terminal for automatic mutation but opens/updates reconciliation; it must never be stored as `compensated`.

## Relationships to existing authority data

- `ExternalWorkRef` points to `WorkID`; it never replaces it.
- A finalized `WorkRevision` embeds the tracker observation and copied work facts used by the run.
- `ExecutionLease` may reference an assignment effect; the assignment effect never references authority sufficient to recreate a lease.
- `WorkspaceBinding` remains lease/generation-scoped and independent of `TrackerBinding`.
- Review and integration records may project comments to Beads, but the provider comment is not the verdict/result.
