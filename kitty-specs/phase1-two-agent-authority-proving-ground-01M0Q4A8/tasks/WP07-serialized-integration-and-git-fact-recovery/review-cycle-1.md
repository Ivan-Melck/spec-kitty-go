---
affected_files: []
cycle_number: 1
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T18:41:51Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 Review Feedback — Cycle 1

Verdict: **changes requested**. The ordinary focused, race, static, and repeated
scenario gates are green, but adversarial interleavings expose two target-ref
safety violations. The current crash matrix also does not implement the named
fault-boundary contract it claims to cover.

## 1. Blocker — an expired generation can publish after final revalidation

`internal/integration/service.go:191-198` commits the final SQLite
revalidation, reaches `IG-06`, and then invokes `UpdateIntegrationTarget`
without carrying or rechecking the mutation generation/token. A process can
pause after line 191, let its lease expire while a replacement generation is
granted, resume, and still win the expected-old Git update when the target has
not yet moved. It then records `done`. This violates FR-007, SC-004, and the
bounded target-ref authority contract.

An independent scheduler-hook mutant at the exact post-revalidation seam fails
with:

```text
expired generation moved target and completed: updates=1 status=done
```

Remediation must make this interleaving unenforceable, not merely add one more
check before another pausable instruction. Put target mutation behind an
exclusive coordinator/critical-section authority whose ownership cannot be
transferred while the old process can still publish, and revalidate approval,
WorkRevision eligibility, generation, token, and target inside that authority.
Add a real-process test that blocks at the last pre-update seam, expires the old
lease, starts a replacement, resumes the old process, and proves the stale
process performs zero target-ref updates and zero `done` writes.

## 2. Blocker — the protected-primary guard is outside the Git authority lock

`internal/gitrepo/integration.go:124-128` checks whether the target is attached
to the primary checkout before `UpdateTarget` acquires the flock. If another
publisher holds that lock, the updater may wait indefinitely. The primary can
attach `refs/heads/main` during the wait; after the lock is released,
`internal/gitrepo/workspace.go:53-58` rechecks only the target SHA and moves the
now-checked-out branch.

A real temporary-repository adversarial test reproduces this:

```text
target moved after protected primary attached
```

Move the primary attachment probe into the locked compare/update callback,
immediately before `update-ref`, and add the lock-wait/primary-attachment test.
The target, primary HEAD, index, and tree must remain unchanged on refusal.

## 3. Blocker — IG-04..IG-09 do not round-trip the fault-boundary contract

`contracts/fault-boundaries.md` pins:

- IG-04 after candidate application;
- IG-05 immediately before compare/update;
- IG-06 after target movement;
- IG-07 after integration result commit;
- IG-08 after work-done commit;
- IG-09 during cleanup-eligibility publication.

The service currently reaches IG-04 before candidate application, IG-06 before
ref movement, IG-07 after ref movement, and IG-09 without any cleanup-eligible
publication (`internal/integration/service.go:147-237`). Consequently the green
`IG-00..IG-09` loop does not test the crash windows named by FR-019 and the
contract. In addition, `CleanupIntegrationWorkspace` has no production caller;
all call sites are tests, so the WP anti-pattern dead-code check fails.

Place each hook at its contracted effect boundary, implement a durable/live
cleanup-eligibility path without deleting dirty/conflicted/unknown state, and
make cleanup refusal reachable through production orchestration. Add assertions
for the state on both sides of the actual ref movement; do not rely on the hook
names alone.

## 4. High — Git capability failures lose their stable typed diagnostic kind

`encodeIntegrationFailure` recognizes only `*integration.Error`
(`internal/helpercmd/integration.go:140-161`), while shared Git methods such as
`TargetHead` and `UpdateTarget` return `*workspace.Error`. Direct service returns
therefore fall through to the default `invalid_command` kind. An independent
diagnostic mutant produces:

```text
Error: invalid_command: git_failure: resolve repository target head
```

Normalize capability errors at the integration boundary (or map the shared
typed errors explicitly) so `git_failure`, `target_changed`, and path/repair
states survive with the full NFR-006 envelope. Add helper-level JSON tests for
missing target, target movement, and unsafe/changed managed-root failures.

## Verification performed

- `go test ./internal/integration ./internal/gitrepo ./internal/controlstore/sqlite` — pass
- `go test ./internal/scenario -run Integration -count=3` — pass
- targeted integration/Git/control/scenario race run — pass
- `go vet ./...` — pass
- `staticcheck ./...` — pass
- `gofmt` and `git diff --check` guards — pass
- three independent adversarial mutants above — fail as described
- migration review — v10 is additive; the deterministic v8 placeholder and
  real v9 ordering remain intact for WP08's required merge

## WP anti-pattern checklist

1. Dead code — **FAIL** (`CleanupIntegrationWorkspace` is test-only).
2. Synthetic-fixture tests — **PASS** for shipped scenario tests, but they omit
   the failing interleavings above.
3. Silent empty return — **PASS**.
4. FR coverage — **FAIL** for FR-019's contracted boundaries and FR-007's stale
   post-revalidation publisher.
5. Frozen surface — **PASS**.
6. Locked decision — **FAIL** because the stale publisher contradicts the
   current-authority law.
7. Shared-file ownership — **PASS**; migration/root coordination is documented
   in the handoff.
8. Production fragility — **PASS** (no new fail-loud panic/raise path).

WP08 depends on WP07 and must remain blocked/rebase after remediation.
