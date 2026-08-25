---
affected_files: []
cycle_number: 8
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T23:01:16Z'
reviewer_agent: user
wp_id: WP01
---

---
affected_files:
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
cycle_number: 8
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T23:20:00Z'
reviewer_agent: reviewer-renata
wp_id: WP01
---

# WP01 review feedback — cycle 8

Verdict: changes requested.

## Blocking issue

1. **Simple statically knowable interprocedural and assignment forms still bypass the retained Markdown-authority prohibition.** The cycle-8 guard resolves direct standard-library reads, immutable declaration chains, function values, and `path`/`filepath` joins, but it silently accepts three ordinary production forms that still read and parse a literal Markdown authority artifact:

   ```go
   // Reader wrapper: the Markdown argument is known at the call site.
   func readAuthority(path string) []byte { b, _ := os.ReadFile(path); return b }
   func loadAuthority() { _ = strings.Fields(string(readAuthority("doctrine.md"))) }

   // Constant-returning path helper: the read API is direct and the return is fixed.
   func authorityPath() string { return "doctrine.md" }
   func loadAuthority() { b, _ := os.ReadFile(authorityPath()); _ = strings.Split(string(b), "# ") }

   // Simple assignment: the value reaching os.ReadFile is unambiguous.
   func loadAuthority() {
       var path string
       path = "doctrine.md"
       b, _ := os.ReadFile(path)
       _ = strings.Fields(string(b))
   }
   ```

   Reproduction: place each snippet in `internal/helpercmd/authority.go` in a `newReachabilityFixture`, then call `CheckDeferredRuntimeSurfaces(context.Background(), root, module)`. An independent disposable reviewer suite at `/tmp/wp01-cycle8-review.fkE0Xq` ran:

   ```text
   go test -run '^TestPlausibleSimpleInterproceduralEscapes$' -count=1 -v
   --- FAIL: TestPlausibleSimpleInterproceduralEscapes
       --- FAIL: .../reader-wrapper
       --- FAIL: .../constant-path-return-helper
       --- FAIL: .../simple-assignment
   ```

   All three cases returned `nil` from the production guard. The cause is visible at `reachability.go:291-322` and `reachability.go:691-732`: `staticBindings` records declarations and `:=` only, while `staticStringValue` recognizes constants, binding chains, and four path helpers but does not propagate constant arguments/returns across local calls or a uniquely reaching `=` assignment. This contradicts T005's requirement to retain the former `.md` prohibition without weakening it and the cycle-8 claim of production-only cross-file/function/path coverage. Extend the AST/type-aware analysis to reject these simple, unambiguous flows (or define and enforce an equally strong fail-closed boundary), and add permanent rejecting temporary-module mutants for all three shapes with exact `ARCH-DEFERRED-SURFACE`, source package, and `markdown:doctrine.md` assertions.

## Cycle-7 reproduction and cycle-8 positive evidence

- The exact cycle-7 shape, `os.ReadFile("doctrine.md")` followed by `strings.Split`, is now rejected correctly.
- Direct reads, cross-file function-value/path aliases, `os.OpenFile`, `io/fs`, and `text/template.ParseFS` cases passed 10/10.
- Comments, prose strings, non-Markdown reads, and `_test.go` Markdown reads produced no false positive in 10/10 runs.
- Repeated diagnostics were byte-identical and sorted by source/target.
- Every mandatory architecture-contract fixture ran through `go test ./internal/architecture -count=10` and passed, including current graph, adapter acceptance, kernel-to-adapter/provider rejection, unknown/ambiguous classification, removed load-bearing edge, test-only reachability, and sole-caller removal.
- Prior cycle regressions for typed target identity, Python aliases, active build tags, recursion, cancellation, typed source failures, production wiring, and Git adapter separation passed in the repeated architecture, gitrepo, handoff, and helpercmd suites.
- Authoritative coordination events record T001–T006 as `done`; latest completion event is `01M0XHMYQSM0KP8Q07V3EMAX6J`, with no later reset.

## Required anti-pattern checklist

1. **Dead code — PASS.** `internal/architecture`, the handoff guard, and the Git adapter are in the non-test production dependency graph of `cmd/spk-phase1-helper`; targeted call-site searches found live composition callers for all new exported entry points.
2. **Synthetic-fixture test — PASS.** FR/NFR fixtures invoke production graph, policy, reachability, deferred-surface, Git adapter, and helper-command paths rather than asserting literal result shapes.
3. **Silent empty return — FAIL.** Unresolved but statically knowable Markdown path flows reach `staticStringValue`'s `"", false` results and are silently ignored at `reachability.go:607-611`, allowing the three forbidden production reads above.
4. **FR coverage — FAIL.** FR-001–FR-005 and the exact cycle-7 regression are covered, but NFR-008/T005 lacks load-bearing coverage for simple interprocedural and assignment flows; all three reviewer mutants survive.
5. **Frozen surface — N/A.** WP01 identifies no frozen product file, and no immutable historical Phase 1 acceptance artifact is modified by the WP01 implementation.
6. **Locked decision — FAIL.** The surviving production Markdown authority reads contradict T005 and the plan's explicit `no Markdown parsing in production` constraint.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both touched `internal/helpercmd/root.go`; the architecture-check dispatch and governance-context wiring are compatible. This feedback records the overlap. WP04, WP05, and WP06 depend on WP01 and must rebase after correction.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud production path; load failures remain typed and bounded.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass (56.5s).
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass (7.7s).
- `go test ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=10` — pass.
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass.
- `go test -race ./internal/scenario -run 'Scope|Architecture' -count=1` — pass.
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass.
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation).
- `go build -o /tmp/wp01-cycle8-review-helper ./cmd/spk-phase1-helper` — pass.
- `go mod verify` — pass; `go.mod` and `go.sum` are unchanged.
- `go run ./cmd/spk-phase1-helper architecture-check --root .` — pass 10/10 with byte-identical `packages=17 edges=332` output.
- `gofmt -d` and WP01 product-file `git diff --check` — clean.
- The approximately 733-second monolithic scenario campaign is WP05 scope and was not used as a WP01 failure.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the corrected AST/type-aware Markdown guard and permanent rejecting mutants land.
