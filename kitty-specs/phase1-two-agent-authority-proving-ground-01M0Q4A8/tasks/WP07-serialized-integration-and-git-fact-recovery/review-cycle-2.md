---
affected_files: []
cycle_number: 2
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T20:41:21Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 Review Feedback — Cycle 2

Verdict: **changes requested**. Cycle 1's primary-checkout, typed-diagnostic,
fault-boundary, and live-cleanup findings are closed. Two repository-fence
ordering failures still allow stale generations to mutate shared integration
state.

## 1. Blocker — an expired publisher can finish while its replacement is pending

The replacement is made `pending` in SQLite before its Git fence is installed
(`internal/controlstore/sqlite/integration.go:88-135`). Meanwhile an old
publisher holds the repository lock, completes its final SQLite revalidation in
the callback at `internal/integration/service.go:200-205`, and can pause before
the atomic `update-ref --stdin` transaction at
`internal/gitrepo/integration.go:164-170`.

This exact real-process ordering was reproduced independently:

1. Generation 1 reached the real target `update-ref --stdin` after final
   revalidation and was paused by a Git executable wrapper.
2. Its TTL elapsed.
3. Generation 2 was acquired durably in SQLite with status `pending`, but its
   repository fence had not yet been installed.
4. Generation 1 resumed.

Generation 1 advanced the target, recorded its result and `done`, removed its
workspace, and returned success despite being expired. The failing record was
`MutationGeneration: 1`, `Status: done`, `CleanupStatus: complete` while
generation 2 was pending. This is the original FR-007/SC-004 authority TOCTOU:
the supplied stale-publisher test waits for generation 2 to become `active`, so
it misses the unsafe pending interval.

Remediation must order replacement acquisition and publication under one
enforceable repository critical section. A replacement must publish a strictly
increasing repository fence generation before it can become pending/active in
SQLite, or otherwise make it impossible for SQLite to expire the old publisher
while that publisher can still commit the old fence+target transaction. The
real-process regression must pause after the old generation's final
revalidation, acquire/start the replacement, then prove the old generation
performs zero stale target, result, `done`, or cleanup mutations.

## 2. Blocker — an older pending installer can roll the repository fence backward

`InstallIntegrationFence` reads whatever object currently occupies the shared
fence ref and uses that object as the expected old value for its update
(`internal/gitrepo/integration.go:136-146`). The update is therefore CAS-safe
against concurrent movement but not generation-monotonic.

An independent real-Git test installed generation 1, installed generation 2,
then invoked generation 1's pending installer again. The stale call succeeded
and changed the shared ref from generation 2's object back to generation 1's
object:

```text
expired pending installer rolled active fence back
```

Bind each installation to the exact predecessor recorded for that generation,
publish strictly increasing generations, and refuse any installation whose
observed predecessor/generation does not match. Same-operation recovery after
"Git fence installed, SQLite activation absent" must remain deterministic and
idempotent, but an older operation must never adopt an arbitrary newer fence as
its predecessor or overwrite it.

## Verification performed

- All three cycle-1 controls were re-run against the pre-fix code and failed as
  expected.
- Candidate stale-active-replacement, flock-wait primary attachment, and OS
  typed-diagnostic controls — pass.
- Pending-reservation and fence-before-activation recovery tests — pass for
  same-operation recovery.
- `go test ./internal/scenario -run Integration -count=2` — pass.
- Focused integration/Git/control/scenario race run — pass.
- `go test ./...` — pass.
- `go vet ./...`, `staticcheck ./...`, `gofmt`, and `git diff --check` — pass.
- Candidate handoff's unchanged `Integration -count=100` evidence is accepted
  as repetition evidence, but it does not contain the two failing orderings.
- Schema v10 digest seal passes; v8 remains the deterministic placeholder and
  v9 ordering remains intact for WP08 integration.

## Cycle-1 closure audit

- Protected-primary recheck inside the repository lock — **closed**.
- Stable helper kinds for Git/path failures — **closed**.
- IG-04 through IG-09 durable boundary facts — **closed**.
- Live cleanup eligibility/removal/replay and no destructive conflict cleanup
  — **closed**.

## WP anti-pattern checklist

1. Dead code — **PASS**; cleanup now has a live production caller.
2. Synthetic-fixture tests — **PASS** for claimed paths.
3. Silent empty return — **PASS**.
4. FR coverage — **FAIL**; FR-007 omits pending-replacement and stale-install
   orderings.
5. Frozen surface — **PASS**; v10 is sealed and v8/v9 ordering is preserved.
6. Locked decision — **FAIL**; an expired generation can still publish and an
   older generation can roll the repository fence backward.
7. Shared-file ownership — **PASS**; migration coordination remains documented.
8. Production fragility — **PASS**.

WP08 depends on WP07 and must remain blocked/rebase after remediation.
