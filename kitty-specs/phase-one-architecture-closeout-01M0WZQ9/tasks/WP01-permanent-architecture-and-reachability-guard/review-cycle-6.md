---
affected_files: []
cycle_number: 6
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T22:03:58Z'
reviewer_agent: user
wp_id: WP01
---

---
affected_files:
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 6
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T22:00:00Z'
reviewer_agent: codex
wp_id: WP01
---

# WP01 review feedback — cycle 6

Verdict: changes requested.

## Blocking issues

1. **The new Go-tooling production-file loader has no execution deadline or caller cancellation.** `loadTypedProductionSources` invokes `runGraphCommand(context.Background(), root, "list", "-json", "./...")` at `internal/architecture/reachability.go:133`. `runGraphCommand` relies entirely on the supplied context (`internal/architecture/graph.go:201-210`), while the 30-second `goListTimeout` is applied only by `loadGraph` at `internal/architecture/graph.go:89`. Consequently, the live `architecture-check` command can finish its bounded `LoadGraph` call and then hang indefinitely in either `CheckReachability` or `CheckDeferredRuntimeSurfaces`; neither exported check accepts a context that lets the composition root cancel it. Preserve active-build discovery through Go tooling, but thread the command context/deadline through the typed-source loader (or establish the same bounded timeout internally), and add a stalling-runner regression that proves the command is cancelled and returns a typed load failure.

2. **Source parse/type-check load failures escape the typed failure contract.** The new loader returns plain wrapped errors from `parser.ParseFile` at `internal/architecture/reachability.go:172-177` and from `productionSourceImporter.check` at `internal/architecture/reachability.go:190-191`, despite T003 requiring typed failures for load errors. A temporary reviewer fixture containing a valid package/import prefix and invalid function body made `go list -json ./...` succeed, then produced `*fmt.wrapError` from `CheckReachability`; `errors.As(err, *LoadError)` was false. Return a stable `*LoadError` (with an appropriate kind and operation/file context) for parse and type-check failures, and add focused assertions for both failure shapes. The temporary reviewer test was removed after reproduction.

## Cycle-5 mutant reproduction

- `go test ./internal/architecture -run 'TestReachabilityHonorsActiveCustomBuildTags|TestReachabilityRejectsUnrootedMutualRecursion' -count=10 -v` — pass. The active/inactive `GOFLAGS=-tags=reviewactive` cases and the unrooted mutual-recursion mutant were handled correctly in all ten executions.
- The file census now comes from `go list` `GoFiles`/`CgoFiles`, so active custom build tags and platform exclusions round-trip correctly.
- The same-package call graph now rejects the target-containing mutual-recursion component when it has no caller outside that component.

## Contract round-trip

- `contracts/architecture-guard.md` is in scope. All mandatory classification, direction, provider-adapter, unknown-root, ambiguity, load-bearing-edge, test-only-caller, and sole-production-caller fixtures invoke production code and pass. The live composition-root command is wired and the current repository graph passes.
- T003's Go-tooling load-failure contract is not complete because execution can be unbounded and parser/type-check failures are not typed; these are the blockers above.
- `contracts/governance-context.md`, `contracts/orchestrator-api-1.3-projection.md`, and `contracts/validation-campaigns.md` are orthogonal to WP01.

## Required anti-pattern checklist

1. **Dead code — PASS.** The architecture module is in the production dependency graph of `cmd/spk-phase1-helper`; exported policy, graph, reachability, handoff guard, and Git adapter entry points all have non-test production paths.
2. **Synthetic-fixture test — PASS.** FR assertions call the production policy, graph loader, reachability scanner, deferred-runtime scanner, and composition-root command rather than asserting literal synthetic shapes.
3. **Silent empty return — PASS.** Reviewed nil/empty returns are successful error-interface results, AST traversal control, or the documented dynamic-command proof boundary; no undocumented silent failure return was added.
4. **FR coverage — PASS.** FR-001 through FR-005 each have behavior assertions, including current-graph classification, exact directional diagnostics, provider-adapter acceptance, repeated architectural mutants, test-only rejection, caller removal, active build tags, and recursion controls.
5. **Frozen surface — N/A.** WP01 identifies no frozen or untouchable file.
6. **Locked decision — PASS.** No production provider, HTTP/API, MCP, Team Kitty, Python runtime, Markdown parser, donor implementation, or forbidden authority dependency was introduced; `go.mod` and `go.sum` are unchanged.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both touch `internal/helpercmd/root.go`; their architecture-check dispatch and governance-context wiring are compatible. This feedback records the overlap. WP04 also owns the scenario surface for later work, and WP04, WP05, and WP06 depend on WP01; rebase those lanes after the next WP01 revision.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path. The unbounded subprocess is separately blocking under the loader contract above.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass.
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass.
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass.
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass.
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation).
- `go run ./cmd/spk-phase1-helper architecture-check --root .` — pass; 17 packages and 331 edges inspected.
- `go mod verify` — pass; `go.mod` and `go.sum` are unchanged.
- `gofmt -d` over WP01 Go files and `git diff --check` — clean.
- Event-sourced status reports WP01 in review; prior cycle evidence records T001 through T006 complete.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the corrected WP01 revision before implementation or review continues.
