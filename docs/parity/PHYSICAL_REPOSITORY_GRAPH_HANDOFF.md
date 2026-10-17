# Physical repository graph — architectural handoff

Status: future private proof work, **not** implemented path correspondence.
Permanent workspace claims and a logical workspace/execution join do not prove
full physical repository correspondence or authorize path normalization.

Follow-up: [original configured-origin foundation](CONFIGURED_REPOSITORY_FOUNDATION.md)
is test-only scoped-reviewed. It preserves the same real driver's original
Repository.Binding by value and validates graph/workspace configuration/layout
without later IO. The remaining historical effect/registration/index observation
boundary below is NOT implemented or repaired by that static capture.

## Existing source-owned facts to preserve

`gitrepo.Bind` (`internal/gitrepo/repository.go`) resolves the exact primary
checkout root and Git common directory, canonicalizes the managed root, refuses
root overlap, and retains a `workspace.RepositoryBinding` with repository/project,
primary/common/managed paths, target ref and primary snapshot. `PrepareManagedRoot`
rechecks canonicalization and separation. `Repository.Binding()` exposes that
original configured value; do not reconstruct it from later workspace strings.

`WorkspaceAuthorityEffectsV2.record` binds the workspace to repository/project,
managed root and the repository-derived WorkspaceID path before any Git effect.
It also refuses generations outside the adapter's int64 domain. The logical
journal may retain larger uint64 values; do not round/clamp those to make physical
adapter evidence qualify.

`Repository.WorkspacePath` derives `managedRoot/WorkspaceID`, verifies canonical
parent and path shape, and refuses a symlink workspace entry. `Repository.Probe`
uses the exact repository worktree census, resolves workspace Git directory and
index path, and refuses Git-directory/common-dir or index/Git-dir ancestry
escapes before returning `workspace.ObservationV2` through the effect adapter.
RegistrationCensus is attempt-scoped, not a cached permanent registration fact.

The actual crown's `realScenarioGit` holds its original `*gitrepo.Repository`.
Its `prepareWorkspace` rechecks `Repository.Binding()` and derived path, then
calls the production effect adapter's Create/Observe. Its `managedRoot()` and
`repositoryPath()` strings alone are weaker evidence than that full original
binding/observation chain. The portable attributable adapter is not a real-Git
repository witness and must not inherit the crown's physical proof.

## Required next private capture boundary

Before path pairing, capture owner-scoped repository facts from the **original
configured repository**, and successful attempt-specific workspace observations
from the same actual effect calls that produced each historical workspace event.
Do not add arbitrary root lookup or perform new Git mutations to repair proof.
Bind captures to trace/history/capture identity, repository/project and the exact
workspace historical key, preserving raw path/observation bytes. Keep original
configuration and dynamic observations separate: a configured binding is not a
fresh registration/clean-index claim, and a later probe cannot prove an earlier
workspace snapshot. Capture-by-value must not retain mutable adapter maps.

A pure comparison proof can then verify the configured primary/common/managed
root graph, root separation, derived workspace layout, and each original
Git-directory/index ancestry before producing owner-specific path tokens.
Retain branch, pinned base, dependency commits, actual candidate/target Git SHAs,
workspace generation/lease/fence and all other business facts exactly. If root
registration or historical observation evidence is missing, refuse that token;
do not fallback to matching relative strings or PathIdentity flags.

Required controls: foreign configured repository/project, reused root/claim,
primary/common/managed overlap, aliased or symlinked derived workspace, Git/index
ancestry escape, trace/capture/historical-version mix, changed registration after
capture, adapter-generation overflow, duplicate/noninjective owner tokens,
changed Git SHA, and unknown business fields/exact uint64 preservation.

This handoff does not extend the private foundation or real oracle, and does
not grant admission, attest source immutability, or transfer qualification.
