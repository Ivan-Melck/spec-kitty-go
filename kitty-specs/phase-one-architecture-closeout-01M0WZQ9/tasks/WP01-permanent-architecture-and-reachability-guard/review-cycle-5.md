---
affected_files: []
cycle_number: 5
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T21:43:31Z'
reviewer_agent: user
wp_id: WP01
---

---
affected_files:
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 5
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T21:43:04Z'
reviewer_agent: codex
wp_id: WP01
---

# WP01 review feedback — cycle 5

Verdict: changes requested.

## Blocking issues

1. **The reachability census does not honor active custom build tags.** `loadTypedProductionSources` filters files with `build.Default.MatchFile` (`internal/architecture/reachability.go:147`), but `build.Default.BuildTags` is empty and does not consume the Go command's active `GOFLAGS=-tags=...` configuration. Consequently, with `GOFLAGS=-tags=reviewactive`, a sole caller in a `//go:build !reviewactive` file still satisfies `CallerExternalPackage`, even though `go list` excludes that file from the active production build. A focused reviewer mutant reproduced this exact false acceptance. Source the compiled file set from Go tooling (preferred, so it matches `LoadGraph`) or construct a build context from the complete active Go configuration, including custom tags; add both positive and rejecting custom-tag fixtures alongside the existing `ignore` and platform fixtures.

2. **An unrooted mutual-recursion cycle still satisfies same-package reachability.** The cycle-4 fix excludes calls physically inside the target's own body, but `func EnforceAuthority() { helper() }` plus `func helper() { EnforceAuthority() }` passes `CallerSamePackageProduction` because the call in `helper` lies outside the target body (`internal/architecture/reachability.go:274-290`). Neither function has an entry path, so the registered target remains unreachable from production while the census reports success. A focused reviewer mutant reproduced this false acceptance. Build a same-package call graph and require a caller path rooted outside the registered target's strongly connected component (or otherwise reject target-containing call cycles); add a rejecting mutual-recursion mutant that asserts `REACH-PRODUCTION-CALLER`, package, and symbol.

## Contract round-trip

- `contracts/architecture-guard.md` is in scope. Classification, directional-edge diagnostics, provider-adapter acceptance, graph loading, and the current registered targets are wired through production code.
- Both surviving mutants violate FR-005 and the contract's requirement for an inspectable production call path. They also violate NFR-003 because declared reachability mutants are not killed under all supported active-build and recursion shapes.
- `contracts/governance-context.md`, `contracts/orchestrator-api-1.3-projection.md`, and `contracts/validation-campaigns.md` are orthogonal to WP01.

## Required anti-pattern checklist

1. **Dead code — PASS.** The architecture module is reached from the production `architecture-check` command; registered handoff guard and Git adapter targets have non-test composition callers. Targeted non-test call-site greps and `go list -deps ./cmd/spk-phase1-helper` confirm the wiring.
2. **Synthetic-fixture test — PASS.** FR tests invoke the production policy, graph loader, reachability scanner, deferred-runtime scanner, and command path.
3. **Silent empty return — PASS.** Reviewed empty/nil returns are normal successful results, AST-walk control, or the documented dynamic-command proof boundary; no undocumented silent failure path was added.
4. **FR coverage — FAIL.** FR-005's tests cover direct self-recursion and default build exclusions, but not active custom tags or target-containing mutual recursion; both uncovered paths fail focused mutants.
5. **Frozen surface — N/A.** WP01 identifies no frozen or untouchable file.
6. **Locked decision — PASS.** No production provider, HTTP/API, MCP, Team Kitty, Python, Markdown-parser, donor-runtime, or forbidden authority dependency was introduced.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both modify `internal/helpercmd/root.go`; the architecture-check dispatch and governance-context wiring are compatible. This feedback records the overlap. WP04, WP05, and WP06 depend on WP01 and must rebase after the next revision.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation)
- `gofmt -d` and `git diff --check` over WP01-owned Go files — clean
- `go run ./cmd/spk-phase1-helper architecture-check --root .` — pass; 17 packages and 333 edges inspected
- `go mod verify` — pass; `go.mod` and `go.sum` are unchanged
- Reviewer mutant: caller excluded by active `GOFLAGS=-tags=reviewactive` — survives
- Reviewer mutant: target/helper mutual-recursion cycle with no external root — survives
- Both temporary reviewer test files were removed after reproduction.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the corrected WP01 revision before implementation or review continues.
