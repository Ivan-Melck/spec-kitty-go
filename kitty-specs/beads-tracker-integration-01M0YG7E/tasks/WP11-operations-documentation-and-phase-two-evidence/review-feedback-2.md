# WP11 Review Feedback — Cycle 2

Verdict: changes required.

Cycle 2 correctly publishes the complete real-provider tuple, makes `BEADS_BIN`/server-variable/target/flag typos red, updates the Phase 0/1 and SQLite status, and adds distinct source-commit/source-tree mutants. One command-smoke blocker remains.

## Blocking finding

### 1. The smoke gate still accepts unsafe command semantics and runner drift

The new validators substantially improve T058, but they do not yet verify every documented command or the actual storage-mode decision:

- `phase2ValidateQualificationCommand` merely checks that each server variable name occurs somewhere in `runner.go` (`internal/architecture/beads_documentation_test.go:405-409`). A variable can disappear from `qualificationStorageMode` while remaining in `runProviderTests`, and the test still passes. In a clean archive of candidate `9537496`, deleting `DOLT_SERVER_PORT` from the four-key slice in `qualificationStorageMode` left the complete documentation selector green. This does not prove the required law that all four values select `server_multiwriter` and omission of any one selects `embedded_serialized`.
- `phase2ValidateHelperCommand` checks flag names only (`:417-432`); it neither runs the documented argument set through the helper command nor validates its safety-critical values. Changing `--read-only=true` to `--read-only=false` or `--mode embedded_serialized` to `--mode nonsense` left the complete selector green. The former defeats the README's read-only inspection promise.
- `phase2ValidateProviderCommands` omits the documented `backup init "$backup_root"` command even though production has `newBackupInitCommand`. Changing that line to `backup begin` left the complete selector green. The explicit disposable `bd init --stealth --skip-agents --skip-hooks --non-interactive --prefix skt` example is likewise not covered by the accepted-command validator.

Each mutant was run with:

```text
go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=1
ok github.com/spec-kitty/go-kitty3/internal/architecture
```

Close this at the semantic seams:

1. Extract or invoke a pure storage-mode classifier and permanently prove exact-four → `server_multiwriter` plus four separate one-missing → `embedded_serialized` cases. The documentation validator should depend on that production decision, not a source-file substring that can be satisfied by an unrelated occurrence. Retain the endpoint-equals-host/port check and the existing binding test that rejects endpoint drift.
2. Validate the complete documented helper invocation through the real parser/validation path without reaching provider state. At minimum pin `--read-only=true`, the accepted mode, schema, digest shape, and required absolute-path/value semantics; add unsafe-value mutants.
3. Cover every documented provider argv, including dynamic `backup init` and the explicit disposable initialization command. Cross-check the former against `newBackupInitCommand`; pin the latter's exact no-agent/no-hook/non-interactive safety flags against the accepted setup contract. Add deletion/typo mutants for both.

Do not run a real provider, initialize user state, or weaken the offline/read-only boundary to make the tests pass.

## Cycle 1 disposition

1. **Complete tuple in README: PASS.** All four server values are now present, endpoint equals host/port, and the text accurately explains missing-key fallback.
2. **Command-smoke gate: PARTIAL / FAIL.** Make dry-runs, helper build/help, installer parser refusals, static typed argv extraction, and common typo mutants are valuable; the escaping semantic mutants above keep this blocker open.
3. **Plan completion truth: PASS.** Phase 0/1 and SQLite are no longer described as provisional; later phases remain clearly future.
4. **True missing-source mutants: PASS.** Commit and tree identity mutants are distinct from census failures and reach `phase2ValidateSourceIdentity`.

## Evidence reviewed

- Candidate `9537496c8c1861480834be5626b3619a8db93bac`, tree `5070d57d0416f76412ae6aa952fe4b352d522c5f`; correction changes exactly README, technical plan, and WP11 documentation tests.
- `go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=10` — PASS.
- Full `internal/architecture` and focused exact-census tests — PASS.
- Focused race across architecture, qualification runner, and Beads binding/command tests — PASS.
- `go vet` and `staticcheck` across the same packages — PASS.
- Checksums 4/4, `gofmt`, and `git diff --check` — PASS.
- `/tmp/go-kitty3-wp11-cycle2-architecture.CWjiou/architecture.report.json` is a clean exact 38/38 campaign bound to candidate/tree, `source_dirty:false`.
- Evidence payload remains byte-identical to commit `106c7a2`; no evidence laundering or provenance regression found.

## Anti-pattern checklist

1. Dead code: N/A — WP11 adds review-only helpers, not production API.
2. Synthetic fixture: **FAIL** for the remaining command-smoke behaviors; source substring/help-name checks survive deletion or unsafe semantic drift.
3. Silent empty return: PASS.
4. FR coverage: FAIL only for T058's every-command/actual-runner semantic coverage described above.
5. Frozen surface: PASS — no WP10 qualification or evidence payload files changed.
6. Locked decision: PASS in shipped docs; the test gate still fails to protect the read-only locked decision.
7. Shared-file ownership: PASS — correction stays within WP11-owned files.
8. Production fragility: N/A — no production code changed.
