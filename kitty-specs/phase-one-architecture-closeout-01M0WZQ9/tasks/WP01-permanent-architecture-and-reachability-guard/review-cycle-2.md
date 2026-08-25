---
affected_files: []
cycle_number: 2
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T20:28:20Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 review feedback — cycle 2

Verdict: changes requested.

## Blocking issues

1. **Two new exported validation functions remain test-only dead code.** `internal/architecture/policy.go:45` exports `RuleID.Validate`, and `internal/architecture/policy.go:111` exports `Violation.Validate`, but the only live call to the former is from the latter and every call to the latter is in `policy_test.go`. The production architecture command reaches the rest of the module, but it never reaches this validation chain. This fails the mandatory dead-code check, which requires every new public function to have a non-test production caller. Either make these helpers unexported/test-local, or invoke violation/rule validation from the production diagnostic construction/evaluation path and add a production-path regression.

2. **The retained Python-runtime guard still misses straightforward aliases declared in another production file.** `CheckDeferredRuntimeSurfaces` parses and type-checks one file at a time (`internal/architecture/reachability.go:235-270`) and builds bindings only from that parsed file. Consequently, a package with `const runtimeName = "python3"` in `runtime.go` and `exec.Command(runtimeName)` in `runner.go` leaves `runtimeName` unresolved in the second file; `staticStringValue` returns `("", false)` and the guard silently accepts the Python launch. Group and type-check all non-test files in each production package (or otherwise resolve package-scope bindings across files), and add a rejecting two-file mutant that asserts `ARCH-DEFERRED-SURFACE` plus the exact runtime diagnostic.

## Required anti-pattern checklist

1. **Dead code — FAIL.** `RuleID.Validate` and `Violation.Validate` have no non-test production caller.
2. **Synthetic-fixture test — PASS.** FR tests invoke the actual policy, graph loader, reachability census, deferred-surface checker, and production command path.
3. **Silent empty return — FAIL.** The unresolved cross-file command expression follows `staticStringValue`'s empty/false return into an unconditional ignore at `reachability.go:310-313`, silently allowing a statically knowable Python runtime.
4. **FR coverage — FAIL.** FR-005 is covered, but the retained NFR-008/Python prohibition lacks a cross-file package-scope alias regression and the implementation fails that behavior.
5. **Frozen surface — N/A.** WP01 identifies no frozen or untouchable file.
6. **Locked decision — FAIL.** The cross-file escape contradicts NFR-008 and the WP's locked requirement to retain the Python-runtime prohibition.
7. **Shared-file ownership — PASS with coordination note.** WP01 and WP03 both modify `internal/helpercmd/root.go`; their current hunks are separate (`architecture-check` dispatch versus governance-context claim wiring). This feedback records the overlap explicitly. WP04, WP05, and WP06 depend on WP01 and must rebase after the next WP01 revision.
8. **Production fragility — N/A.** Go has no `raise`; WP01 adds no panic or equivalent unhandled fail-loud production path.

## Verification evidence

- `go test ./internal/architecture -count=10` — pass
- `go test ./internal/scenario -run 'Scope|Architecture' -count=10` — pass
- `go test -race ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd -count=1` — pass
- `go vet ./internal/architecture ./internal/gitrepo ./internal/handoff ./internal/helpercmd ./internal/scenario` — pass
- `gofmt -d` over every WP01 production/test file — clean
- `go build ./cmd/spk-phase1-helper` — pass
- `go test ./... -count=1` — inconclusive/fail: `internal/scenario` reached its built-in 10-minute timeout in `TestPhase1DurableWorkRevisionConsumerScenarios/WR-02`; no assertion failure was emitted before timeout
- Event-sourced status — T001 through T006 are recorded `done`
