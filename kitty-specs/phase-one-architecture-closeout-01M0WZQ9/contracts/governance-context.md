# Contract: Run-Owned Governance Context

## Admission

A new run request carries a complete `GovernanceContext` or an already persisted `GovernanceContextID` plus matching aggregate commitment. The store validates and persists context and run linkage atomically.

## Validation

- Blank context identity, empty entries, blank entry identity/revision, missing commitment algorithm, invalid digest, duplicate entry identity, or aggregate mismatch is an invalid command.
- Entry order is committed and therefore significant.
- Created timestamps do not affect semantic identity.
- An existing context identity with different content is a conflict, never an update.

## Run and lease behavior

- The run result exposes the governance context ID and aggregate commitment.
- Heartbeat and workspace commands continue to present lease fencing authority; they do not resubmit governance content.
- Inspecting a lease resolves governance through its immutable run ID.
- Replacement lease generations cannot change the run's context.
- A caller that needs different governance starts a new run.

## Migration

- Pre-feature runs remain readable with provenance state `unknown_pre_provenance`.
- The adapter never synthesizes a digest from the current environment for an old run.
- Every post-migration newly admitted run has a valid context foreign key.

## Portability

No portable type contains SQL, table, YAML, filesystem, provider, or protobuf concepts. The SQLite schema is one adapter projection and must be replaceable by Gapdb without changing context semantics.
