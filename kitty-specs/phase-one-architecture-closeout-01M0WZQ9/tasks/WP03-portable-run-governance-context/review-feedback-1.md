**Issue 1 — live `claim` admission path omits required governance provenance (FR-010 / T014)**

`internal/execution.StartRequest.Validate` now rejects a zero governance context, but the production `runClaim` constructor in `internal/helpercmd/root.go:166` still creates `StartRequest` without `GovernanceContext`. A direct invocation with otherwise valid IDs returns `invalid_command: governance_context is invalid` before repository admission. The new helper test only exercises `syntheticStartGovernanceContext`/`start-run`, so deleting or breaking the `claim` integration remains undetected.

Update every live production `StartRequest` constructor, including `runClaim`, to supply an explicitly named synthetic Phase 1 context (or another validated admission context consistent with the locked design), and add a regression test that invokes the `claim` command path and proves it no longer fails for missing provenance. Because `internal/helpercmd/root.go` is not currently in WP03's `owned_files`, coordinate/record the ownership expansion rather than silently editing an unowned surface.

Downstream note: WP04 depends on WP03 and must rebase after the corrected WP03 changes land.
