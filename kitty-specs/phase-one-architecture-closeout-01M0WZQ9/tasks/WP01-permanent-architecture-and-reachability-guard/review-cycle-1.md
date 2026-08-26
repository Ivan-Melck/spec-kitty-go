---
affected_files: []
cycle_number: 1
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T18:43:17Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 review feedback — cycle 1

Verdict: changes requested.

## Blocking issues

1. **The new architecture guard is test-only and has no non-test production caller.** `DefaultPolicy`, `LoadGraph`, `DefaultReachabilityTargets`, `CheckReachability`, `Policy.Check`, and `CheckDeferredRuntimeSurfaces` are called only from `*_test.go`; `go list -deps ./cmd/spk-phase1-helper` does not include `internal/architecture`. This fails the review's dead-code rule for every new public API/module. Add a live non-test quality-gate/command caller (and exercise that caller from the repository gate), or restructure the surface so the permanent guard is not an exported module reachable only through tests.

2. **The policy blesses an existing authority-to-adapter dependency instead of enforcing the contract.** `internal/architecture/policy.go:166-174` classifies `internal/gitrepo` as `provider-neutral-infrastructure` and allows `TierAuthority` to depend on that tier. `internal/gitrepo/runner.go` is a concrete Git subprocess adapter, and `internal/handoff/git.go:10` imports it directly. The architecture contract classifies the Git repository adapter in the adapter tier (`contracts/architecture-guard.md:25`) and says authority capabilities may depend on contracts, not adapters. Reclassify the Git implementation as an adapter and remove/split the authority-to-adapter edge behind a provider-neutral port; add a rejecting regression for the edge so the current accidental import cannot become permanent policy.

3. **The mandatory real reachability target is neither a registered guard nor a concrete port implementation.** `DefaultReachabilityTargets` registers only `internal/integration.NewService`, a service constructor. The required fixture is “remove a registered guard's sole production caller” (`contracts/architecture-guard.md:43`), while FR-005 requires selected authority guards and concrete port implementations to have production paths. Register at least one real authority guard and one concrete port/adapter implementation (extending the census to methods if needed), prove their current production callers, and make removal of each sole caller fail with the exact symbol diagnostic.

4. **The replacement weakens the retained Python-runtime prohibition.** `CheckDeferredRuntimeSurfaces` only rejects a string literal passed directly to `exec.Command`/`CommandContext`; at `internal/architecture/reachability.go:267-270`, every identifier or other expression is ignored. For example, `const runtime = "/usr/bin/python3"; exec.Command(runtime)` passed the old flat source guard but passes the new guard. Dot-imported `os/exec` calls are also skipped. Preserve the former protection with a fail-closed AST/type-aware check for straightforward constant/alias forms and add rejecting mutants for indirect and dot-import call shapes.

## Required anti-pattern checklist

1. **Dead code — FAIL.** The new public architecture module has zero callers from non-test production code.
2. **Synthetic-fixture test — PASS.** The FR fixtures invoke the production policy/graph/reachability implementations rather than asserting literal fixture shapes.
3. **Silent empty return — PASS.** No undocumented catch-and-empty failure path was introduced; the reviewed `nil` returns are ordinary successful results or AST walk control.
4. **FR coverage — FAIL.** FR-005's mandatory real guard/concrete-port production-reachability coverage is absent.
5. **Frozen surface — N/A.** No frozen or untouchable file is identified for WP01, and its implementation commit changes only owned files.
6. **Locked decision — PASS.** No changed code introduces a forbidden Phase 2 provider/runtime/API implementation or donor architecture.
7. **Shared-file ownership — PASS.** WP01's seven changed implementation files are exclusive to lane-a in `lanes.json`; no other lane modifies them.
8. **Production fragility — N/A.** Go has no `raise`, and WP01 adds no equivalent panic/fail-loud production path.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass
- `go test -race ./internal/architecture -count=1` — pass
- `go test -race ./internal/architecture ./internal/scenario -run 'Scope|Architecture' -count=1` — pass (architecture selector matched no tests; the separate full architecture race command above did)
- `go vet ./internal/architecture ./internal/scenario` — pass
- `gofmt -d internal/architecture internal/scenario/repetition_test.go` — clean
- `go test ./... -count=1` — inconclusive: the execution harness terminated the still-running scenario package with exit 143 after roughly eight minutes; no test failure was emitted before termination
