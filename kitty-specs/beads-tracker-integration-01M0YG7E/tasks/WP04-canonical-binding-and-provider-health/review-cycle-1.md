---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T11:16:28Z'
reviewer_agent: user
wp_id: WP04
---

# WP04 review feedback — commit `240d875`

Verdict: changes requested. The binding canonicalization, refusal-before-lock ordering, advisory-lock crash recovery, focused repetitions, race detector, vet, and diff checks pass. The following contract violations still block approval.

## 1. Capability inspection overstates both compatibility and implemented ports

`internal/tracker/beads/inspect.go:225-265` calls `capabilitiesFor` even when preflight failed, and that function reports catalog read/create/update, graph, discussion, atomic assignment, portable export, and restorable backup as supported unconditionally. At this commit, `BoundAdapter` implements only `tracker.Inspector`; none of the catalog, graph, discussion, assignment, or exporter ports has a production implementation. An unavailable, malformed, version-skewed, schema-skewed, or workspace-mismatched provider therefore still advertises those future capabilities.

This violates FR-006 and T018 requirements 3-4: capabilities must derive from the characterized compatibility tuple, storage mode, and ports actually implemented at the current dependency revision. It also creates a dangerous false `AssignmentAtomicClaim` claim before the actor semantics are qualified.

Fix by deriving an exact capability set from successful compatibility evidence and live implemented ports. Until a port exists and is qualified, report it unsupported. Add inspection tests for incompatible/unavailable fixtures that assert no future capability remains advertised, plus live conformance assertions for every capability reported supported.

## 2. Server-mode identity is observed, not configured or pinned, and readiness has no freshness boundary

`BindingConfig` and `tracker.TrackerBinding` contain no expected server identity. `internal/tracker/beads/inspect.go:191-215` accepts any non-empty server ID returned by the same provider, and `validateReadiness` later checks only that this self-asserted value is non-empty. A spoofed provider returning the same arbitrary value from `info` and `doctor` qualifies native multi-writer mode. The bound adapter also has no binding-owned server credentials/endpoint values; its command binding drops the allowlisted server connection values supplied by configuration.

Additionally, a server-mode `Readiness` can be reused indefinitely after server health or identity changes because mutation validation does not re-observe or expire that qualification.

This violates the WP04 non-negotiable server-mode law and T019 requirements 6-7. Add an explicit configured server identity (and required endpoint/credential binding facts without diagnostic leakage), compare observed identity against it, establish a bounded freshness/revalidation rule, and prove that wrong/spoofed/stale/unhealthy server evidence refuses before lock acquisition or mutation. The server test must exercise overlapping writes only for a correctly pinned healthy server.

## 3. NFR-001 evidence does not measure the promised operation

`TestPreflightLatencyCharacterization` uses `inspectionRunner`, an in-process Go fake. Its reported p95 is tens of microseconds and includes no provider process startup. It neither characterizes process-start overhead separately nor records the required non-secret host detail beyond GOOS/GOARCH/Go version.

Replace or supplement it with a deterministic local helper executable exercised through the real WP03 subprocess runner. Record separate process-start characterization and the 100-preflight p50/p95/max distribution, then assert only the scoped p95 upper bound. Keep the current no-fragile-lower-bound behavior.

## 4. Required adversarial controls are incomplete

The lock test proves parent/child exclusion and killed-holder recovery, but it does not run two lane-shaped working directories and therefore would not fail if lock identity were changed from the canonical binding to the current working directory. The server test proves a lock bypass, not actual qualified overlap. The discovery census checks only executable bytes and workspace entry count; it does not census both lane repositories, agent instructions, Git config, and hook directories, and it would not catch every forbidden setup command.

Add the T020 mutant controls exactly as specified: divergent lane CWDs sharing one canonical workspace, two-process overlap recording, spoofed server identity, and a complete path/byte census whose mutation spy fails on any init/fix/hook/agent/Git setup command.

## 5. Public WP04 entry points have no live production caller

Targeted non-test call-site search finds `ResolveBinding`, `NewBoundAdapter`, `Readiness.Binding`, and `Readiness.ServerQualified` used only by tests; `BoundAdapter` is not constructed by any production path. This fails the review anti-pattern's dead-code check and the plan's rule that package scaffolding alone is not progress.

Connect the binding/inspection surface to a live production adapter construction path, or coordinate an explicit ownership adjustment that lands the smallest working preflight/read scenario in this WP. Do not add artificial self-references solely to satisfy the grep.

## Anti-pattern checklist

1. Dead code: **FAIL** — new public entry points have no non-test caller.
2. Synthetic-fixture test: **PASS** — existing tests invoke WP04 production functions, although the missing scenarios above still need coverage.
3. Silent empty return: **PASS** — empty values are paired with explicit errors or are documented optional-field selection.
4. FR coverage: **FAIL** — FR-006 capability honesty, FR-019 server qualification, and NFR-001 methodology are not proven.
5. Frozen surface: **PASS** — commit `240d875` changes only the six WP04-owned files.
6. Locked decision: **FAIL** — self-asserted server identity and unsupported capability claims contradict the locked qualification boundaries.
7. Shared-file ownership: **PASS** — all six changed files are exclusively assigned to WP04.
8. Production fragility: **N/A** — no new panic/raise path was introduced.

## Verification performed

- `go test ./internal/tracker/beads -run 'Binding|Inspect|Health|Writer|Worktree|Preflight' -count=10 -timeout=8m` — pass
- `go test -race ./internal/tracker/beads -timeout=8m` — pass
- `go test ./internal/tracker/... -count=10 -timeout=8m` — pass
- `go vet ./internal/tracker/...` — pass
- `git diff --check 240d875^ 240d875` — pass

Do not treat the earlier detached `go test -short ./...` run as green; it entered the long scenario package and timed out, as recorded by the orchestration context.
