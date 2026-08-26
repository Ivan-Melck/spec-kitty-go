---
affected_files: []
cycle_number: 3
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T20:46:34Z'
reviewer_agent: user
wp_id: WP01
---

---
affected_files:
  - internal/architecture/graph.go
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 3
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T20:46:23Z'
reviewer_agent: codex
wp_id: WP01
---

# WP01 review feedback — cycle 3

Verdict: changes requested.

## Blocking issues

1. **The reachability census accepts a shadowed method as a call to the registered target.** In internal/architecture/reachability.go:135-173, import aliases are recorded as strings and every selector with the registered method name is credited to that imported package. A valid production file can import guardpkg, use it for another symbol, then shadow guardpkg locally and call an unrelated EnforceAuthority method; CheckReachability returns success even though the registered guard has no production caller. This violates FR-005 and the architecture-guard contract's fabricated/test-only and caller-removal guarantees. Resolve call expressions to their go/types objects (including selectors and same-package identifiers), require the resolved function object to be the registered declaration, and add rejecting external-package and same-package shadowing mutants. Reproduction confirmed by a temporary focused test: the syntactically valid shadowed-call fixture was accepted.

2. **The retained Python-runtime prohibition still misses a straightforward local alias.** CheckDeferredRuntimeSurfaces records package/value declarations only from ast.ValueSpec at internal/architecture/reachability.go:272-285. A function containing interpreter := "python3" followed by exec.Command(interpreter) reaches staticStringValue's empty/false result and is unconditionally ignored at lines 322-325. This violates NFR-008 and T005's requirement to retain the Python-runtime prohibition. Resolve statically knowable short declarations (and other supported local bindings) with scope-correct object identity, then add a rejecting mutant asserting ARCH-DEFERRED-SURFACE, the source package, and runtime:python3. The guard should document which genuinely dynamic command expressions are outside its proof boundary instead of silently treating every unresolved expression as safe.

3. **LoadError.Unwrap is a new exported method with no production caller.** Targeted non-test searches find its declaration at internal/architecture/graph.go:74 but no call or errors.Is/errors.As path that receives an architecture LoadError; the only focused errors.As use is in graph_test.go and the value is already top-level. This fails the review prompt's strict dead-code rule for new public functions/methods. Remove the unused protocol surface, make the cause handling private, or add a real production error-classification path that exercises it.

## Contract round-trip

- contracts/architecture-guard.md is in scope. Package classification, directional-edge diagnostics, provider-adapter acceptance, graph loading, and registered-target fixtures are wired through production code.
- The reachability shadowing mutant above contradicts Required behavior / Mandatory fixtures because the census does not prove that the resolved registered symbol is called.
- contracts/governance-context.md, contracts/orchestrator-api-1.3-projection.md, and contracts/validation-campaigns.md are orthogonal to WP01.

## Required anti-pattern checklist

1. **Dead code — FAIL.** LoadError.Unwrap has no non-test production caller. Other new modules and exported functions have live production paths through helpercmd architecture-check, handoff composition, or interface dispatch.
2. **Synthetic-fixture test — PASS.** The tests invoke the production policy, graph loader, reachability scanner, deferred-runtime scanner, and composition-root command; no FR assertion is merely a literal fixture comparison.
3. **Silent empty return — FAIL.** staticStringValue returns empty/false for a statically knowable local short-declaration alias, and the production scanner silently continues, accepting the Python launch.
4. **FR coverage — FAIL.** FR-001 through FR-004 have direct assertions. FR-005's caller-removal test checks spelling/shape, but no assertion proves the call resolves to the registered production function; the shadowed-call mutant survives. NFR-008's local-alias path is also untested and fails.
5. **Frozen surface — N/A.** WP01 names no frozen or untouchable file, and the diff does not modify the mission's documentation-only frozen surfaces.
6. **Locked decision — FAIL.** The local Python alias contradicts NFR-008 and T005's explicit retained prohibition. No provider, HTTP server, MCP, Team Kitty, donor runtime, or Markdown parser dependency is present in the checked-in production diff.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both modify internal/helpercmd/root.go; the current hunks are compatible (architecture-check dispatch versus governance-context claim wiring). This feedback records the overlap. WP04, WP05, and WP06 depend on WP01 and must rebase after the next WP01 revision.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path.

## Verification evidence

- go test ./internal/architecture -count=10 — pass
- go test ./internal/scenario -run 'Scope|Architecture' -count=10 — pass
- go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1 — pass
- go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario — pass
- go test ./... -run '^$' -count=1 — pass (repository-wide compilation)
- gofmt -d over all WP01 Go files — clean
- go build ./cmd/spk-phase1-helper — pass
- go mod verify — pass; go.mod and go.sum are unchanged
- Reviewer mutant: shadowed imported-package alias calling an unrelated same-named method — survives (blocker 1)
- Reviewer mutant: local short-declaration alias launching python3 — survives (blocker 2)
- Event-sourced WP status reports WP01 in review; prior cycle evidence records T001 through T006 done.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the corrected WP01 revision before implementation or review continues.
