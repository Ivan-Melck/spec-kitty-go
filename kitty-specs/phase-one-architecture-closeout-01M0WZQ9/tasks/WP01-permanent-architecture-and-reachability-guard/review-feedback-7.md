---
affected_files:
  - internal/architecture/policy.go
  - internal/architecture/reachability.go
  - internal/architecture/reachability_test.go
  - internal/scenario/repetition_test.go
cycle_number: 7
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reviewed_at: '2026-08-25T22:40:00Z'
reviewer_agent: codex
wp_id: WP01
---

# WP01 review feedback — cycle 7

Verdict: changes requested.

## Blocking issue

1. **The replacement guard no longer prohibits Markdown authority parsing inside an already classified production package.** T005 requires replacing the Phase 1 flat guard *without weakening it* and explicitly retaining the Markdown-authority-parsing prohibition until that tier exists. `DefaultPolicy` rejects Markdown-named package roots and three known third-party parser imports, while `CheckDeferredRuntimeSurfaces` inspects only Python launches. Production code in an existing classified package can therefore read a `.md` authority artifact and parse it with the standard library without any architecture finding.

   Reproduction: a disposable reviewer test created a temporary module with the real module path and one production file at `internal/helpercmd/authority.go` whose function calls `os.ReadFile("doctrine.md")` and `strings.Split(string(content), "# ")`. `LoadGraph`, `DefaultPolicy(module).Check(graph)`, and `CheckDeferredRuntimeSurfaces` all returned success in 10/10 executions:

   ```text
   go test ./internal/architecture -run '^TestReviewerMarkdownAuthorityParsingSurvives$' -count=10 -v
   PASS (10/10)
   ```

   The retired `TestPhase1SourceScope` guard rejected this exact `.md` source shape through its `(?i)\.md\b` production-source rule, so this is a real protection regression rather than a new reviewer preference. Add a Go-native, production-only AST/type-aware prohibition for Markdown authority-file parsing (without reverting to comment/prose grep), emit a stable `ARCH-DEFERRED-SURFACE` diagnostic naming the source package and implicated Markdown surface, and add a rejecting temporary-module mutant. The reviewer-only test file was removed after reproduction; no product code was changed.

## Cycle-6 verification

- Cancellation: `TestTypedProductionSourceLoaderCancelsStalledGoList` passed 10/10 and returned typed `LoadCommandFailed` with `context.DeadlineExceeded`.
- Parse/type-check failures: both `TestTypedProductionSourceLoadFailuresAreTyped` cases passed 10/10 and returned `LoadInvalidSource` with stable operation context.
- Every Go-list phase is bounded: `go list -m -json`, `go list -deps -json ./...`, `go list -e -deps -json ./...`, and the conditional `go list -deps -export -json ...` all run through `runGraphCommand`'s 32 MiB stdout/stderr buffers; their enclosing loaders apply the caller context plus the 30-second internal deadline.
- Diagnostics: policy findings are sorted by rule/source/target, reachability targets/findings are sorted, and the live success diagnostic was byte-identical over ten executions (`packages=17 edges=331`).
- Authoritative status: coordination event `01M0XG6GPMKTB4F4HFNN4AKPYE` records T001–T006 as `done`; the reduced `status.json` agrees, and no later event resets them.

## Contract round-trip

- `contracts/architecture-guard.md` is in scope. Its classification, directional-edge, provider-adapter, unknown/ambiguous package, load-bearing rule, and reachability examples pass against production code.
- The blocker is a direct WP/T005 regression not represented by a concrete example in that contract.
- `contracts/governance-context.md`, `contracts/orchestrator-api-1.3-projection.md`, and `contracts/validation-campaigns.md` are orthogonal to WP01. The approximately 733-second monolithic scenario campaign remains WP05 scope and was not used as a WP01 gate.

## Required anti-pattern checklist

1. **Dead code — PASS.** `internal/architecture` is in the production dependency graph of `cmd/spk-phase1-helper`; new exported architecture, handoff guard, and Git adapter entry points have live non-test callers.
2. **Synthetic-fixture test — PASS.** FR-001–FR-005 tests invoke the production graph, policy, reachability, deferred-surface, and composition-root paths.
3. **Silent empty return — PASS.** Reviewed nil/empty returns are normal success values, AST traversal control, or the documented dynamic-command proof boundary.
4. **FR coverage — FAIL.** FR-001–FR-005 have production-path assertions, but WP01's NFR-008/T005 retained Markdown-authority prohibition has neither a load-bearing assertion nor working behavior; the reviewer mutant survives.
5. **Frozen surface — N/A.** WP01 identifies no frozen or untouchable product file.
6. **Locked decision — FAIL.** The surviving Markdown parser contradicts T005's explicit retained prohibition and the plan's “no Markdown parsing in production” constraint.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both touched `internal/helpercmd/root.go`; their merged hunks are compatible. WP04, WP05, and WP06 depend on WP01 and must rebase after the correction.
8. **Production fragility — N/A.** WP01 adds no panic or equivalent unhandled fail-loud path.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass (30.9s).
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass (6.1s).
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass.
- `go test -race ./internal/scenario -run 'Scope|Architecture' -count=1` — pass.
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass.
- `go test ./... -run '^$' -count=1` — pass (repository-wide compilation).
- `go build ./cmd/spk-phase1-helper` — pass; generated binary removed after verification.
- `go mod verify` — pass; `go.mod` and `go.sum` are unchanged.
- `go run ./cmd/spk-phase1-helper architecture-check --root .` — pass 10/10 with identical output.
- `gofmt -d` over WP01 Go files — clean.

## Downstream action

WP04, WP05, and WP06 depend on WP01. Rebase those lanes after the Markdown-authority guard and rejecting mutant land.
