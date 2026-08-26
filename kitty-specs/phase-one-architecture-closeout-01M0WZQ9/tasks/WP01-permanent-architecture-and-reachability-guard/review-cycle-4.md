---
affected_files: []
cycle_number: 4
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T21:26:42Z'
reviewer_agent: user
wp_id: WP01
---

---
affected_files:
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 4
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T21:26:32Z'
reviewer_agent: codex
wp_id: WP01
---

# WP01 review feedback — cycle 4

Verdict: changes requested.

## Blocking issues

1. **Build-excluded files satisfy the production-reachability census.** `loadTypedProductionSources` in `internal/architecture/reachability.go:126-166` walks every non-test `.go` file but does not honor `//go:build` constraints or platform filename selection. A disposable reviewer fixture placed the only external call to a registered guard in a `//go:build ignore` file; `CheckReachability` returned success. That call is not part of any shipped production build, so this contradicts FR-005 and the architecture-guard contract's requirement that removing the sole production caller reject the symbol. Source the file set from Go tooling or apply the active Go build context before parsing, and add rejecting fixtures for a build-ignored caller and a platform-excluded caller.

2. **A target's self-recursive call counts as its own production caller.** `CheckReachability` scans every call expression and marks any call resolving to the registered function as same-package reachability (`internal/architecture/reachability.go:243-258`), including calls inside that function's own declaration. A disposable fixture containing only `func EnforceAuthority() { EnforceAuthority() }` passed `CallerSamePackageProduction` despite having no entry path. Exclude calls whose enclosing function is the target itself (and analogous target-initialization cycles), then add a rejecting same-package self-recursion mutant. This is required for T004's same-package caller policy to prove an actual production call path.

## Contract round-trip

- `contracts/architecture-guard.md` is in scope. Classification, directional-edge diagnostics, provider-adapter acceptance, graph loading, and the current registered targets are wired through production code.
- The two surviving reviewer mutants violate the contract's production-reachability fixtures: a non-built caller and self-recursion can both masquerade as the required live caller.
- `contracts/governance-context.md`, `contracts/orchestrator-api-1.3-projection.md`, and `contracts/validation-campaigns.md` are orthogonal to WP01.

## Required anti-pattern checklist

1. **Dead code — PASS.** New exported architecture APIs have live non-test callers through `helpercmd architecture-check`; the handoff guard and Git adapter constructor are also wired through production composition.
2. **Synthetic-fixture test — PASS.** FR tests invoke the production policy, graph loader, reachability scanner, deferred-runtime scanner, and command path.
3. **Silent empty return — PASS.** Empty/false helper results are either AST-walk control or the explicitly documented dynamic-command proof boundary; no undocumented empty failure return was introduced.
4. **FR coverage — FAIL.** FR-005 lacks assertions for active-build membership and self-recursive non-reachability, and both behaviors fail reviewer mutants.
5. **Frozen surface — N/A.** WP01 identifies no frozen or untouchable file.
6. **Locked decision — PASS.** No new provider, HTTP/API, MCP, Team Kitty, Python, Markdown-parser, donor-runtime, or forbidden authority dependency is present in the production diff.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both modify `internal/helpercmd/root.go`; the WP01 architecture-check dispatch and WP03 governance-context claim wiring are separate compatible hunks. This feedback records the overlap explicitly.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation)
- `gofmt -d` and `git diff --check` over WP01 files — clean
- `go build ./cmd/spk-phase1-helper` — pass
- `go mod verify` — pass; `go.mod` and `go.sum` are unchanged
- Reviewer mutant: sole caller in `//go:build ignore` file — survives
- Reviewer mutant: target self-recursion as the only same-package call — survives
- Event-sourced status records T001 through T006 as done before review.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the corrected WP01 revision before implementation or review continues.
