---
affected_files:
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 9
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T23:50:00Z'
reviewer_agent: reviewer-renata
wp_id: WP01
---

# WP01 review feedback — cycle 9

Verdict: changes requested.

## Blocking issue

1. **Known Markdown paths passed through function literals still bypass the retained production prohibition.** The cycle-9 evaluator closes the three cycle-8 forms, nested named wrappers, cross-file helpers, function aliases, and uniquely reaching ordinary assignments, but it does not propagate arguments into function literals. Both of these classified production forms return `nil` from `CheckDeferredRuntimeSurfaces`:

   ```go
   func load() {
       read := func(path string) []byte { b, _ := os.ReadFile(path); return b }
       _ = read("literal.md")
   }

   func load() {
       _ = func(path string) []byte { b, _ := os.ReadFile(path); return b }("iife.md")
   }
   ```

   The cause is structural: `staticFunctionValue` resolves only identifiers and selectors, `staticFunctions` indexes only `*ast.FuncDecl`, and the interprocedural body walk explicitly stops at `*ast.FuncLit`. The outer file walk therefore sees `os.ReadFile(path)` without the call-site parameter value, while neither literal invocation can be resolved as a callable body. This is a straightforward, statically knowable production read that the retired `.md` source guard rejected, so it contradicts T005, NFR-008, and the plan's locked “no Markdown parsing in production” boundary.

   Reproduction uses only the disposable external harness at `/tmp/wp01-cycle9-review-harness`. Each fixture first passes `LoadGraph` and `DefaultPolicy.Check`, then expects the exact typed violation. Both mutants survived in 10/10 executions:

   ```text
   cd /tmp/wp01-cycle9-review-harness
   go test -run 'TestBoundedStaticEvaluatorVariants/(function-literal-known-argument|immediately-invoked-function-literal)$' -count=10

   function-literal-known-argument: got <nil>, want Violations{{
     Rule: ARCH-DEFERRED-SURFACE,
     SourcePackage: example.test/cycle9/internal/helpercmd,
     TargetPackage: markdown:literal.md,
   }}
   immediately-invoked-function-literal: got <nil>, want the same typed shape for markdown:iife.md
   ```

   Extend the bounded evaluator to resolve direct and uniquely bound function literals and propagate their known arguments without treating every closure as a root or guessing dynamic values. Add permanent classified temporary-module mutants for the named-literal and immediately invoked forms, asserting exactly one typed `Violations` entry with rule, source, and Markdown target. Preserve the current recursion bound and ambiguous-flow false-positive controls.

## Cycle-8 reproduction and bounded adversarial evidence

- The three exact cycle-8 escapes now reject with exact typed diagnostics: reader wrapper, constant-returning path helper, and uniquely reaching ordinary assignment.
- Nested named wrappers, cross-file helpers, standard-library function aliases, constant returns, assignment ordering/scope, and deterministic multi-diagnostic ordering behaved correctly in the external harness.
- Multiple assignments, multiple returns, use-before-assignment, reassigned short declarations, dynamic wrapper arguments, and recursive path helpers remained unresolved without false positives or hangs.
- The two function-literal variants above are the only surviving mutants in the bounded external set.
- The permanent test `TestDeferredRuntimeSurfaceRejectsMarkdownAuthorityParsing` asserts exact `Violations` values for the cycle-8 cases. `TestDeferredRuntimeSurfaceDoesNotGuessAmbiguousMarkdownFlows` covers the conservative negative boundary.

## Prior regressions and contract round-trip

- `go test ./internal/architecture -count=10` passed all prior-cycle regressions for typed target identity, Python aliases/dot imports, cross-file bindings, active build tags, build-excluded files, self/mutual recursion, bounded cancellation, typed parse/type-check failures, Markdown direct/cross-file/path/template forms, deterministic diagnostics, live production wiring, and Git-adapter separation.
- All mandatory `contracts/architecture-guard.md` fixtures passed: current graph, provider adapter acceptance, kernel-to-adapter/provider rejection, unknown and ambiguous classification, removed load-bearing edge, test-only target rejection, and registered sole-caller removal.
- Authoritative coordination event `01M0XK8QRZTR6Z46YXHD18TF4J` records T001–T006 as `done`; no later event resets them before this review claim.
- The complete branch diff was inspected. `go.mod` and `go.sum` are unchanged; production searches found no Beads/Gapdb provider, Team Kitty, MCP, HTTP server, Python runtime, copied donor implementation, protobuf core, or universal-ledger leakage. Forbidden names in `policy.go` are deny-list policy data, not dependencies.

## Required anti-pattern checklist

1. **Dead code — PASS.** `internal/architecture` is in the non-test dependency graph of `cmd/spk-phase1-helper`; the policy, graph, reachability, deferred-surface, handoff guard, Git adapter, and helper-command entry points all have live production callers.
2. **Synthetic-fixture test — PASS.** FR/NFR fixtures invoke production graph loading, policy evaluation, typed reachability/deferred-surface analysis, Git adapter composition, and the live helper command.
3. **Silent empty return — FAIL.** The two statically knowable function-literal flows reach unresolved paths and the production guard returns `nil`, silently accepting forbidden Markdown reads.
4. **FR coverage — FAIL.** FR-001–FR-005 and the exact cycle-8 shapes have load-bearing assertions, but NFR-008/T005 has no permanent function-literal mutant and both reviewer variants survive.
5. **Frozen surface — N/A.** WP01 identifies no frozen product file, and no immutable historical Phase 1 acceptance artifact is modified by the implementation.
6. **Locked decision — FAIL.** The surviving production Markdown reads contradict T005 and the plan's explicit no-Markdown-parsing constraint.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both touched `internal/helpercmd/root.go`; the architecture-check dispatch and governance-context wiring are compatible. WP04, WP05, and WP06 depend on WP01 and must rebase after correction.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path; Go-tooling failures remain bounded and typed.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass (81.922s).
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass (8.282s).
- `go test ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=10` — pass (9.955s / 5.220s / 7.679s).
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass.
- `go test -race ./internal/scenario -run 'Scope|Architecture' -count=1` — pass.
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass.
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation).
- `go build -o /tmp/wp01-cycle9-review-helper-current ./cmd/spk-phase1-helper` and `go mod verify` — pass.
- `gofmt -d` and product-file `git diff --check` — clean. Whole-branch `git diff --check` reports only pre-existing CommonMark hard-break spaces in shared mission planning artifacts, outside WP01 product files.
- Live `architecture-check` — pass 10/10 with byte-identical `packages=17 edges=332` output.
- External deterministic multi-diagnostic probe — pass 10/10.
- External function-literal Markdown mutants — survive 10/10 (blocking).
- The approximately 733-second monolithic campaign was not run; it remains WP05 scope as directed.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the function-literal guard and permanent exact typed mutants land.
