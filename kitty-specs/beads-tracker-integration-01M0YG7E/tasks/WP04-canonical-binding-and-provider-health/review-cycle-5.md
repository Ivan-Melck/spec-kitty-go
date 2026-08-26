---
affected_files: []
cycle_number: 5
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:45:35Z'
reviewer_agent: user
wp_id: WP04
---

# WP04 dependency correction cycle 5 review — commit `5c65c36`

Verdict: changes requested. The correction now matches the real Beads v1.2.2 schema-1 `version`, `where`, `dolt show`, and `dolt status` observations, cross-checks independent storage/server facts, forces metrics/event-flush disablement, preserves refusal-before-lock/mutation, and leaves a real provider workspace byte-identical after inspection. Two integration defects and one contract round-trip defect still block approval.

## 1. The shipped helper composition suite is broken by the command/identity migration

`go test ./internal/helpercmd -run 'InspectBeads|Tracker' -count=1 -timeout=5m` fails in three production-boundary tests:

- `TestTrackerInspectBeadsRunsFromShippedRootAndEmitsTypedJSON`
- `TestTrackerInspectBeadsServerCredentialsAreNeverSerialized`
- `TestTrackerAssignmentBeadsReachesLiveAdapterSaga`

The helper fixtures in `internal/helpercmd/tracker_test.go` still emit the removed `workspace_path`/`database_id`, `info`, and `doctor` responses and still pass opaque `--database-identity db-1`. The first two inspections therefore fail closed instead of reporting the expected writer capability; the live assignment path reaches a `malformed_protocol` refusal instead of the confirmed effect. Updating only the adapter-local catalog/graph fixtures left the shipped application caller inconsistent with the new preflight contract.

Fix every live helper fixture and composition path that opens a Beads adapter to use the real v1.2.2 `where` + `dolt show` + `dolt status` envelopes and the corrected database binding fact. Preserve the end-to-end assignment/compensation assertions. Require at least:

```text
go test ./internal/helpercmd -count=1 -timeout=5m
go test ./internal/tracker/beads ./internal/helpercmd -count=10 -timeout=10m
```

## 2. The new database-directory identity is not canonicalized or validated

`ResolveBinding` still hashes `BindingConfig.ProviderDatabaseIdentity` verbatim. The correction has changed that field's effective meaning from an opaque provider ID to the absolute `where.data.database_path`, but construction accepts empty, relative, missing, and symlink-alias values without canonicalization. Preflight later compares that raw hash with the canonical observed database path.

Using the real v1.2.2 provider, a binding whose database identity is a symlink to the exact same `embeddeddolt` directory constructs successfully but reports `workspace_mismatch`; the canonical path succeeds. This violates T016's alias convergence law and makes an accepted binding depend on spelling.

Make the database directory an explicit canonical binding fact (either by tightening/renaming the existing field or adding a distinct field). Resolve it with the same existing-path and symlink rules as the workspace, reject empty/relative/missing/non-directory inputs, hash only its canonical value, and update the CLI help/callers accordingly. Add construction plus real-preflight tests proving canonical and symlink spellings converge and invalid database paths fail at binding construction, before any subprocess.

## 3. The concrete protocol contract still directs implementations to obsolete commands

`contracts/beads-json-protocol.md` still pins `bd info --schema --json` for schema/database facts and `bd doctor --agent --json` for health, and `quickstart.md` still instructs the former. The real v1.2.2 evidence and this correction establish that those are not the qualified machine-readable preflight surfaces. This fails the required contract round-trip check and risks reintroducing the exact defect in later WPs.

Update the shared protocol/quickstart artifacts, with an explicit coordination note, to pin `version`, `where`, `dolt show`, and `dolt status`, state that read-only and logical server identity are locally sealed binding policy, and record the forced no-write environment. Do not leave `info`/`doctor` as product-state alternatives.

## Verified closure from the prior correction request

- Real v1.2.2 embedded output matches the four new payload structs and schema checks.
- `where.path`, `where.database_path`, `where.prefix`, `dolt show.data_dir/database`, and `dolt status.data_dir` are cross-bound; server endpoint/database/health observations are cross-checked while logical server identity remains local policy.
- No production `CommandInfo`, `CommandDoctor`, `newInfoCommand`, or `newDoctorCommand` remains.
- `BD_DISABLE_METRICS=1` and `BD_DISABLE_EVENT_FLUSH=1` override ambient values in the bounded child environment.
- A real inspection left the provider file manifest byte-identical.
- Adapter-local focused tests pass ten repetitions, including schema-per-observation, cross-observation splices, stale/unqualified server refusal, pre-lock refusal, worktree convergence, and writer controls.
- The catalog/graph/writer test edits are mechanical fixture migrations; no catalog or graph production behavior changed.

## Anti-pattern checklist

1. Dead code: **PASS** — the shipped helper calls the Beads composition seam.
2. Synthetic-fixture test: **FAIL** — adapter fixtures were migrated, but the production helper fixtures still exercise the obsolete protocol and the byte-real fixture test only decodes literals rather than protecting all shipped callers.
3. Silent empty return: **PASS** — no new silent-empty failure path was found.
4. FR coverage: **FAIL** — FR-003/FR-004 worktree identity convergence is false for database-path aliases, and the shipped composition regression leaves FR-005/FR-006 red outside the adapter package.
5. Frozen surface: **PASS** — no frozen authority-kernel surface changed.
6. Locked decision: **FAIL** — the concrete protocol contract still advertises unqualified `info`/`doctor` product-state parsing.
7. Shared-file ownership: **FAIL** — catalog/graph edits are mechanical, but the command migration was not propagated to the shared helper composition surface or shared contract artifacts.
8. Production fragility: **N/A** — no new panic/raise path was introduced.

WP05 and later dependents must incorporate this corrected binding/protocol contract after remediation.
