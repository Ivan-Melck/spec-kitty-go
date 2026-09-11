# M3: Production Mission Execution and Hubnot Composition — Mission Outline

**Status:** DRAFT — pending Lynn's approval · **License:** GPL-3.0-only · **Build agent:** autonomous codex (same lane pattern as `hubnot-tracker-replacement-01M22QEJ`) — authority defined in [PERMISSION_GRANT.md](PERMISSION_GRANT.md)

## 1. Purpose

When M3 succeeds, the go-kitty3 production host composes **real** agents, the
admitted Hubnot provider, real Git repositories, and real workspaces on top of
the adopted M2 GapDB authority — and the concurrency crown jewel survives the
disagreement. Concretely: two real agents can work concurrently in distinct
lease-scoped workspaces on real repositories; expired/zombie writers are
quarantined with their work preserved; dishonest or stale tracker responses
create reconciliation, never authority; integration is serialized
repository-first and merge conflicts preserve candidates as explicit repair
work. Recovery from every failure produces typed next actions, never
fabricated success.

## 2. Observation surface / problem space

- **Repository:** `spec-kitty/spec-kitty-redesign` (this repo), Go module
  `github.com/spec-kitty/go-kitty3`. M2 production host in
  `internal/productionruntime/` + `internal/controlstore/gapdb/`; capability
  seams currently wired to deliberate refusals (`unavailableReadinessSource`,
  `unavailableDecompositionProvider`, `unavailableIntegrationGit` in
  `internal/helpercmd/production.go` are the M2 absence evidence to replace
  at the composition root).
- **Authority kernel (frozen, do not re-cut):** event/CAS laws in
  `internal/mission/`, `internal/execution/`, `internal/workspace/`,
  `internal/handoff/`, `internal/review/`, `internal/integration/` — qualified
  by the adopted M2 tuple (PR #6; `m2-adoption.json`).
- **Hubnot provider:** admitted tuple pinned in `docs/HUBNOT_QUALIFICATION.md`
  (source `baf350d12…`, executable SHA256 `077058c8…`, envelopes
  `hn.identity/1`, `hn.issue/1`, binding `go-kitty.hubnot-binding/1`).
  Installed via the compiled pinned installer (`make hubnot-install`).
- **Client contract:** `docs/NATIVE_REQUEST_SURFACE.md` (PR #8) — operation
  requests, drive waves, refusal taxonomy; family law in
  `docs/adr/0003-mission-coordinate-authority-family.md` (readiness family =
  production lifecycle surface).
- **Program law:** `docs/PRODUCTION_RUNTIME_PROGRAM.md` §M3 — outcome,
  falsification target, scope, nine required adversarial cases, exit evidence,
  non-goals, rollback. This outline is a scaffold for that mission; the
  mission spec must preserve all of them (admission law §4.1).

## 3. Failure catalog / success criteria

Required adversarial cases (from the program — each must have a named
falsification scenario in the spec, and pass with exact census):

- **F1 Two-process race:** two processes racing for the same work → one lease,
  one loser refused with typed stale/lease diagnostics, no torn state.
- **F2 Concurrent agents:** two agents executing independent work in distinct
  workspaces → both succeed, primary checkout untouched.
- **F3 Review independence:** same `ActorID` attempting review under a
  different `ProcessID` → refused; ActorID is the independence boundary.
- **F4 Zombie continuation:** an expired lease's process continuing ordinary
  writes in its quarantined workspace → writes fail closed, workspace
  preserved, typed recovery action.
- **F5 Dishonest/stale tracker:** Hubnot responses that are stale, dishonest,
  unavailable, or contradicted by a human edit → drift reconciliation,
  never silent acceptance; tracker facts grant no authority.
- **F6 Human reassignment:** reassignment between effect and compensation →
  compare-before-compensate over provider revision, value, and operation
  marker; compensation refused on mismatch.
- **F7 Simultaneous integration:** two integration attempts for one
  repository/target fence → one proceeds, the other refused; serialized.
- **F8 Merge conflict:** conflict that preserves candidates and creates
  explicit repair work → candidates never discarded, repair is a typed work
  item, primary checkout remains untouched.
- **F9 Dirty/unowned workspaces:** never deleted under any recovery path.

Success = every case passing in the mission's acceptance campaigns, plus the
exit-evidence census: "recovery produces typed next actions rather than
fabricated success."

## 4. Architecture sketch

- **Composition root** (`internal/productionruntime/composition.go` +
  `internal/helpercmd/production.go`): replace the M2 absence-evidence stubs
  with the real Hubnot-backed tracker port, real Git integration
  (`unavailableIntegrationGit` → qualified implementation of
  `integration.*` ports), and real agent dispatch. The M2 capability laws,
  request wire, and admission records are constraints, not suggestions.
- **Tracker projection:** attribution-safe read models over the admitted
  Hubnot tuple; every tracker effect compare-before-compensate; compensation
  never edits authority state.
- **Git integration:** serialized per (repository, target ref) fence;
  repository-first integration fencing; conflicts produce explicit repair
  work with candidates preserved.
- **Workspaces:** lease-scoped real worktrees under a managed root; path
  identity claims (`workspace.PathIdentityV2`) enforced; quarantine on
  lease expiry; dirty/unowned workspaces never deleted.
- **Review:** handoff-bound, ActorID-independent, consuming immutable
  candidates — never live directories.
- Owner-fixed constraints (never re-cut): Go 1.26 toolchain; GapDB as the
  sole production authority (no SQLite, no dual write, no fallback import);
  Hubnot admitted tuple exact; the M2 kernel's wire laws unchanged
  (ADR 0003 family law applies to every new surface).

## 5. Deployment / delivery

- All work lands via PRs to `spec-kitty/spec-kitty-redesign` (PR-then-merge
  policy even as sole builders). Feature branch per WP lane, coordination
  branch for mission bookkeeping.
- No new infrastructure: production host runs on Lynn's machine against the
  admitted GapDB sidecar. No new credentials, VMs, or DNS. Nothing
  human-gated inside the mission; the only human gates are the program's
  existing mission gates (accept, mission-review, retrospective) and Lynn's
  final adoption-style decisions for M4/M5 entry.

## 6. Non-goals (v1)

- Neutral acceptance kernel or public API (M4/M5 own those).
- MCP adapter (M7). Core release qualification (M6).
- No new tracker semantics beyond the qualified Hubnot tuple; no SQLite;
  no remote callers beyond the one-machine boundary.

## 7. Suggested work packages

The build agent may re-cut with STATUS.log justification; each WP must be
independently reviewable with source-bound evidence, following the
`hubnot-tracker-replacement` lane discipline (implement → independent review
→ approve, per WP):

1. **WP1 — Composition root & provider admission:** replace the
   `unavailable*` seams at the production composition root with the
   admitted Hubnot tuple and real Git integration wiring; no law changes;
   refusal-preserving fallbacks removed only where a qualified
   implementation replaces them.
2. **WP2 — Real execution composition:** claims, heartbeat, lease
   renewal/expiry, quarantine wired to real processes (F1, F4).
3. **WP3 — Lease-scoped workspaces & fencing:** real worktrees, path-claim
   enforcement, zombie quarantine, dirty/unowned preservation (F2, F4, F9).
4. **WP4 — Handoff, review, rejection cycles:** immutable candidate binding
   to real Git facts; ActorID-independent review incl. same-actor refusal
   (F3).
5. **WP5 — Tracker projection & compensation:** Hubnot attribution-safe
   projection, drift reconciliation, human-edit contradiction handling,
   compare-before-compensate (F5, F6).
6. **WP6 — Serialized integration & conflict repair:** repository-target
   fencing, serialized integration, conflict → repair work, candidate
   preservation (F7, F8).
7. **WP7 — Crown-jewel adversarial census:** the full nine-case adversarial
   suite with exact census, integrated at the composition root; exit
   evidence assembly.

## 8. Workflow notes for the build agent

- **Mission scaffold:** author `kitty-specs/<slug>/spec.md` from this outline
  (your first act under the grant — the specify scaffold is placeholder-only),
  then `spec-kitty plan` → tasks → implement/review loop via
  `spec-kitty next`. Reference the full slug from the specify JSON.
- Read `docs/adr/0003-mission-coordinate-authority-family.md` first: the
  production lifecycle runs on the readiness family; mission-family
  operations are the audit surface. Drive plans carry `kind` explicitly.
- `docs/NATIVE_REQUEST_SURFACE.md` is the wire contract; its request-minting
  reference lives at `cmd/spk-drivegen/` (uncommitted scratch — treat as
  reading material, decide its fate in WP1).
- Every plan deviation and every spec-kitty issue goes in [STATUS.log](STATUS.log)
  — that is the audit trail. See [PERMISSION_GRANT.md](PERMISSION_GRANT.md).
- Charter lives in `.kittify/charter/`; keep `charter sync` clean
  (legacy-key warning is known, non-fatal).