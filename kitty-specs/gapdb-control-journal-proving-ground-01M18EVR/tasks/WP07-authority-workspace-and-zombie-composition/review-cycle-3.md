---
affected_files: []
cycle_number: 3
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T21:15:39Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 review feedback — cycle 3

Verdict: **changes requested**.

Reviewed exact implementation commit `18e8812dc86005de79d2b279bfa493002b8a1222`. The lane subsequently merged mission-status commits only; they do not change the reviewed implementation.

Cycle 3 closes the four cycle-2 architectural blockers. The retained adapter now rejects caller-coordinate impersonation; expiry, owner-restart, and workspace-bind replays bind complete command identity; retained claim and owner restart are race-free without the prior nested `RWMutex` deadlock; sealed restore reconstructs historical invalidation receipts and verifies durable request digests plus exact journal event membership; and the production-callable runtime drives admitted external GapDB plus real Git through W1 quarantine and fresh W3 delivery. One public-contract blocker remains.

## Blocker

### Public heartbeat accepts a lease TTL that it silently ignores

Location: `internal/provingground/workspace.go:132-151`; applied policy in `internal/provingground/authority.go:982-1009`.

`Runtime.Heartbeat` accepts a caller-supplied `ttl` and passes it into the retained `execution.HeartbeatRequest`. The retained execution service validates that the value is positive, so the public API presents it as meaningful command input. The proving-ground core nevertheless always computes the new deadline with `Config.LeaseTTL`; it never compares the requested TTL with the admitted policy.

A reviewer-only mutant invoked the public method with a valid 10-minute TTL against a runtime configured for one minute. The call returned success, but the durable current deadline was `now + 1m`, not `now + 10m`:

```text
public heartbeat accepted ttl=10m0s but silently applied configured ttl:
got deadline 2026-08-30 12:01:02Z
want         2026-08-30 12:10:02Z
```

This is not cosmetic. A caller can believe it renewed authority for ten minutes while the durable lease expires after one, creating avoidable stale-agent behavior in the exact concurrency path this WP qualifies. It also leaves the public command surface with an input that is accepted but absent from the proving-ground command's effective identity.

Remediation: make the public contract have one source of TTL truth. Prefer removing the `ttl` argument and constructing the retained request with the admitted `Config.LeaseTTL`. If compatibility requires retaining the argument, reject `ttl != Config.LeaseTTL` with `OutcomeInvalidCommand` **before** exact-authority binding or durable mutation. Add a public-runtime mutant proving zero state change for a mismatched TTL and a success assertion proving the refreshed deadline equals the admitted TTL.

## Cycle-2 closure and evidence that passed

- Retained actor/process/fence impersonation: **PASS** — wrong actor, process, token, lease, and generation refuse without mutating current authority.
- Exact operation identity: **PASS** — changed expiry authority/time, owner-restart time, and GJ-04 repository/lane/branch/base/time refuse; stored application `RequestDigest` and operation event-membership mutants fail sealed restore.
- Epoch concurrency: **PASS** — retained claim versus owner restart passes the race detector; the publication path no longer recursively acquires `epochMu` while a writer is queued.
- Exact capsule TOCTOU: **PASS** — deterministic head advance after capsule admission cannot replace the admitted stale authority with a newer current head.
- Historical receipt recovery: **PASS** — expiry A can be retried after sealed restore even when replacement generation C/W3 is the terminal authority projection; no republish occurs.
- Public crown-jewel lifecycle: **PASS** — public start/restore/expire/replace/authorize/binding methods drive W1 -> quarantine and Actor A/Process C/W3 while B/W2 remains active. The admitted external test uses exact GapDB binary SHA-256 `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e` and real Git, and preserves primary HEAD/index/tree.
- Cycle-1 atomicity and recovery: **PASS** — expiry and owner restart remain one-batch transitions; sealed restore and fresh-process GJ-03 guarantees remain green.
- Zombie fact: **PASS** — POSIX writes to W1 are possible, but stale governed authority successes remain zero; no chmod, process-death, or token-aware editor fiction is used.
- Architecture boundary: **PASS** — exact proving-ground and architecture suites contain no SQLite, internal GapDB server, fallback authority, or forbidden dependency path. The declared M1 boundary remains one host and one filesystem authority; multi-process and the final production campaign caller remain owned by later dependent WPs.

## Commands and results

- `go test ./internal/provingground -count=10` — pass.
- `go test -race ./internal/provingground -count=3 -timeout=90s` — pass.
- Focused prior-blocker selectors, repeated 20 times — pass.
- `SPEC_KITTY_M1_GAPDBD=/tmp/go-kitty3-gapdb-admission.cOGcrB/gapdbd go test ./internal/provingground -run 'TestRealGapDBExecutionGitGJ04AndExpireExactRetryAfterSealedRestart|TestPublicReplaceWorkspacePrevalidatesAndCreatesFreshW3WhileBContinues' -count=3 -v` — pass.
- `go test ./internal/architecture -count=3` — pass.
- `go test ./internal/execution ./internal/workspace ./internal/gitrepo ./internal/controljournal/...` — pass, including the full checkpoint suite.
- `go vet ./internal/provingground`, `staticcheck ./internal/provingground`, and `git diff --check` — pass.
- Reviewer-only public-TTL mutant — **fails as expected**, exposing the blocker; the temporary test was removed after reproduction.

## Contract round-trip

- `contracts/control-journal-v1.md`: **PASS for the cycle-2 blockers** — exact-operation changed-content collisions now fail locally and after sealed restore.
- `contracts/authority-capsule-v1.schema.json`: **PASS** — exact start, heartbeat, expiry, GJ-04 restore, replacement, and stale-capsule refusal use the frozen capsule.
- Public heartbeat lease policy: **FAIL** — accepted input does not describe the durable deadline actually published.
- WP09/WP10 evidence contracts: unchanged and explicitly deferred; this WP does not claim final multi-process or adoption evidence.

## Anti-pattern checklist

1. Dead code: **N/A at this dependency cut, explicitly deferred** — `internal/provingground` is the planned M1 composition root; the dependent campaign/CLI WPs own its non-test caller. No dummy caller should be added merely to satisfy a grep. This deferral must be closed before mission acceptance.
2. Synthetic-fixture test: **PASS with one deferred boundary** — authority/workspace tests execute production methods, and the external scenario uses the real admitted application/GapDB/Git composition. Final owner-host/campaign qualification remains WP09/WP10 scope.
3. Silent empty return: **PASS** — no unexplained silent empty-return path found.
4. FR coverage: **FAIL** — the public heartbeat contract does not faithfully apply or refuse its supplied TTL; all WP07 concurrency and recovery behaviors otherwise have direct assertions. FR-026 through FR-029 are explicitly implemented by dependent WP08/WP09 rather than claimed here.
5. Frozen surface: **PASS** — no frozen contract file changed, and the exact authority capsule remains the public coordinate.
6. Locked decision: **FAIL** — the public heartbeat currently admits a command value that does not determine the durable result, contrary to the single-authoritative-policy and exact-command-identity design.
7. Shared-file ownership: **PASS** — cycle 3 modifies only WP07-owned proving-ground files; architecture classification remains separately owned and unchanged.
8. Production fragility: **PASS** — no panic/bare-raise path was introduced; the remaining defect is explicit silent policy substitution.

WP08 and WP09 remain dependent on WP07 and must not proceed until the heartbeat contract is corrected and independently re-reviewed.
