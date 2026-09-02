---
affected_files:
  - internal/provingground/workspace.go
  - internal/provingground/workspace_test.go
cycle_number: 4
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T21:27:01Z'
reviewer_agent: codex-wp07-reviewer-cycle4
reviewed_commit: 6762c0083c9f44baadc0c00aa877d12f63687c20
wp_id: WP07
---

# WP07 review cycle 4 — approved

Reviewer: `codex-wp07-reviewer-cycle4`  
Implementation: `6762c0083c9f44baadc0c00aa877d12f63687c20`

## Verdict

**APPROVE.** The cycle-3 public-heartbeat TTL blocker is closed. A positive
caller TTL unequal to the admitted runtime policy refuses before exact-capsule
binding or application publication, while the matching TTL refreshes the
authority head and deadline to the admitted policy. No regression was found in
the previous authority, concurrency, recovery, workspace, or external-adapter
gates.

## Decisive evidence

- `Runtime.Heartbeat` checks the requested TTL against `Config.LeaseTTL` before
  capsule admission, operation-scoped exact-authority binding, or retained
  execution dispatch.
- A 10-minute request against a one-minute policy returns
  `OutcomeInvalidCommand`; the current authority head and deadline remain
  unchanged. The matching one-minute request succeeds and publishes a deadline
  equal to authoritative time plus one minute.
- Retained heartbeat/expiry requests cannot impersonate actor, process, lease,
  generation, or fence coordinates.
- Changed-input expiry, owner-restart, and GJ-04 retries refuse, including after
  sealed reconstruction. Stored application `RequestDigest` and operation
  event-membership mutants also refuse restore.
- The exact-capsule TOCTOU probe proves a concurrent head advance cannot replace
  the admitted stale authority with the newer current authority.
- Historical expiry receipts survive sealed restore after Process C/W3 becomes
  the terminal generation, without republishing or damaging the replacement.
- The public lifecycle preserves Actor B/Process B/W2 while quarantining W1 and
  delivering fresh Actor A/Process C/W3. Zombie POSIX writes remain possible but
  yield zero governed authority successes.
- The admitted external GapDB binary has SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
  The real GapDB plus real Git GJ-04/restore/replacement scenario passes and
  preserves the primary HEAD, index, and tree.

## Qualification gates

- PASS: public TTL/replacement regression, `-count=50`.
- PASS: prior blocker probe set, `-count=20`.
- PASS: `go test ./internal/provingground -count=10`.
- PASS: `go test -race ./internal/provingground -count=3 -timeout=90s`.
- PASS: admitted external GapDB/real Git focused scenarios, `-count=3`.
- PASS: `go test ./internal/architecture -count=3`.
- PASS: execution, workspace, Git repository, and control-journal dependency
  suites.
- PASS: repository compile-only census, `go vet`, `staticcheck`, `gofmt`, and
  `git diff --check`.
- PASS: the proving-ground dependency graph contains the GapDB adapter and no
  SQLite or SQLCipher dependency.

## Contract and scope assessment

- `control-journal-v1.md`: **PASS** — exact operation identity and sealed
  historical replay remain fail-closed.
- `authority-capsule-v1.schema.json`: **PASS** — start, heartbeat, expiry,
  replacement, restoration, and stale-capsule refusal retain the frozen shape.
- C-012/C-013 and FR-023/FR-030–FR-034: **PASS** at the WP07 composition cut.
- FR-026–FR-029 are represented by the journal/application and GJ-03/GJ-04
  composition used here; their effect/review/GJ-05 and final campaign closure
  remain explicitly assigned to dependent WP08/WP09.
- The first non-test campaign caller remains explicitly assigned to WP09 and is
  not a WP07 blocker.

## WP anti-pattern checklist

1. Dead code — **N/A at this dependency cut**: WP09 owns the first live campaign
   caller; no dummy caller was added.
2. Synthetic fixture — **PASS**: production methods are exercised, including an
   admitted external GapDB and real Git scenario.
3. Silent empty return — **PASS**: no unexplained silent failure path was added.
4. FR coverage — **PASS for the WP07 composition boundary**; dependent
   effect/review/final-campaign closure remains explicitly assigned downstream.
5. Frozen surface — **PASS**: no frozen contract file changed.
6. Locked decision — **PASS**: one TTL policy, lease-scoped workspaces, exact
   operation identity, and POSIX-write realism are preserved.
7. Shared-file ownership — **PASS**: cycle 4 changes only WP07-owned files.
8. Production fragility — **PASS**: the repair adds a typed fail-closed refusal,
   not a panic or transient-race crash path.

