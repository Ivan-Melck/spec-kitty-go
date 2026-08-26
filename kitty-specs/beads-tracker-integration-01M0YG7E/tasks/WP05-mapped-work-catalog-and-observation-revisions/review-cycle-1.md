---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T12:17:10Z'
reviewer_agent: user
wp_id: WP05
---

# WP05 Review Feedback — Cycle 1

Verdict: changes required.

## Blocking findings

### 1. Empty provider-owned values cannot be applied safely

`BoundAdapter.UpdateWork` always models title, description, acceptance rendering, and metadata as the requested replacement state (`internal/tracker/beads/catalog.go:166-169`). However, the accepted command builder omits `--description` and `--acceptance` when their values are empty (`internal/tracker/beads/command.go:150-155`). A valid request that clears a description or removes all criteria therefore mutates the namespaced metadata and `last_operation` while leaving the old provider field in place. The post-write `issueMatchesUpdate` check then returns a conflict, but the mutation has already partially applied.

This violates FR-008 and T025's requirement to merge the requested provider-owned fields and validate one self-consistent fresh observation. Make presence/change intent explicit in the typed update command so an empty replacement differs from an omitted field. Add destructive-shortcut tests for clearing description and clearing acceptance/criteria; assert the exact argv, the resulting provider record, and that no partially updated metadata is left behind.

### 2. Replay trusts stale self-reported metadata instead of live semantic input

Create replay checks only `created_by_operation` and the stored `content_digest` (`internal/tracker/beads/catalog.go:511-526`). Update replay likewise returns a no-op when `last_operation` and stored digest match (`internal/tracker/beads/catalog.go:150-155`). Neither path confirms that the current provider title, description, acceptance rendering, design context, and structured criteria still equal the requested semantics.

Consequently, a human can change a title or acceptance rendering without changing the Spec Kitty namespace, after which the original operation is falsely classified as an exact replay. That contradicts T023 requirement 7, T025's external-conflict semantics, and Required Invariants 10-11. Recompute/compare semantic input from the immutable decoded issue (including the machine criteria and relevant provider fields) before declaring replay. Add tests for human drift after create and after update with an unchanged operation marker/digest; both must refuse or classify external conflict without mutation.

### 3. The production catalog has no production caller

Repository-wide non-test call-site search finds no invocation of Beads `CreateWork`, `ReadWork`, or `UpdateWork`; only interface assertions exist at `internal/tracker/beads/catalog.go:635-636`. The WP-level anti-pattern checklist therefore fails its dead-code rule. The fake tests demonstrate the object in isolation but do not prove that a production command/service can obtain a validated binding and reach the catalog path.

Wire one narrow production-shaped caller through the accepted application/helper boundary (with an explicit ownership expansion if required), and add a test that reaches at least one catalog operation through that caller. Do not add Beads knowledge to the kernel or parse human-formatted output.

### 4. Caller-supplied provider metadata cannot be preserved on create

T023 requirement 5 requires create to preserve caller-supplied provider metadata outside `spec_kitty`, but `tracker.CreateWorkRequest` has no provider-metadata input (`internal/tracker/capabilities.go:473-482`) and `CreateWork` always calls `mergeMetadata(nil, desired)`. This makes the requirement structurally unreachable and leaves no test for it.

Resolve the contract mismatch explicitly: add a bounded provider-neutral opaque metadata input whose ownership rules are documented, or amend the requirement through the governed planning surface if caller metadata was intentionally removed. Then prove foreign nested keys survive create. Do not silently declare this complete while the request cannot express it.

### 5. Server-mode concurrent creates can violate the one-to-one mapping invariant

`withCatalogMutation` bypasses the adapter lock in provider-native server mode (`internal/tracker/beads/catalog.go:195-205`). Create uniqueness is implemented as `list -> create`; Beads does not provide a uniqueness constraint over the namespaced `(ProjectID, WorkID)` mapping. Two processes creating the same stable `WorkID` can both observe no match and create two native issues. The existing 1,000-create test uses distinct WorkIDs and an embedded serialized adapter, so it cannot detect this race.

Preserve provider-native concurrency while introducing a mapping-safe coordination strategy for identical stable identities, or truthfully constrain/report create concurrency if it cannot be guaranteed. Add a barrier-controlled same-WorkID race test in server topology and a mutant that removes the protecting mechanism. The final provider state must contain exactly one mapping, not merely return a conflict after duplicates already exist.

## Anti-pattern checklist

1. **Dead code: FAIL.** No production caller reaches the new catalog operations.
2. **Synthetic-fixture test: PASS.** Existing FR-oriented tests invoke the production adapter methods and dishonest command seam.
3. **Silent empty return: PASS.** Reviewed empty/nil returns are normal typed control flow, not swallowed provider failures.
4. **FR coverage: FAIL.** FR-008 empty-value replacement and NFR-006 same-identity contention are not covered; T023 caller metadata is unrepresentable.
5. **Frozen surface: PASS.** Commit `6249235` changes only the six WP05-owned files.
6. **Locked decision: FAIL.** Replay can contradict the locked same-operation/semantically-identical-input rule.
7. **Shared-file ownership: PASS.** No shared file was modified by the WP commit.
8. **Production fragility: PASS.** No transient production path introduces an unexplained panic/raise; `setJSON`'s panic is limited to statically JSON-encodable internal wire values.

## Evidence run

- `go test ./internal/tracker/beads -count=1 -timeout=5m` — PASS (29.732s)
- Focused metadata/revision/catalog correctness selection — PASS
- `go test ./internal/architecture -count=1 -timeout=3m` — PASS (10.905s)
- `go vet ./internal/tracker/beads` — PASS
- `git diff --check 6249235^..6249235` — PASS
- `git show --name-status 6249235` — exactly the six owned WP05 files

The green suite is useful evidence for deterministic metadata encoding, content-derived revisions, guarded refusal ordering, ordinary replay, ambiguous observation, and portable scale. It does not discharge the blockers above.
