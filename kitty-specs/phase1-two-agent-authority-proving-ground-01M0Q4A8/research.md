# Phase 0 Research: SQLite Authority Boundary

**Mission**: `phase1-two-agent-authority-proving-ground-01M0Q4A8`

**Date**: 2026-08-23

**Decision scope**: Only questions capable of changing the Phase 1 authority
experiment. This is not a general database evaluation.

## Decision Summary

Use `modernc.org/sqlite v1.57.0` through `database/sql`, with WAL mode,
foreign keys, a 5000 ms busy timeout, and immediate writer acquisition. Treat
the database as a single-machine control store. Verify effective connection
settings at startup and expose contention as a typed result after the bounded
timeout.

This decision does not claim that SQLite fences filesystem writes. Lease-scoped
workspaces remain the mechanism that contains a stale process.

## RQ-01 — Which SQLite driver should the proving ground use?

### Finding

The selected version of `modernc.org/sqlite` is a CGo-free `database/sql`
driver. The local Go toolchain can resolve version `v1.57.0`, whose module
declares Go 1.25. The proving environment is Go 1.26.4 on Linux amd64.

### Decision

Pin `modernc.org/sqlite v1.57.0` in `go.mod`.

### Why

- Independent helper processes can run the same binary without requiring a C
  compiler or system SQLite library.
- The experiment still uses a real SQLite engine and real cross-process file
  locking.
- Pinning prevents an engine or DSN behavior change from silently altering the
  evidence.

### Rejected

- `github.com/mattn/go-sqlite3`: valid and mature, but its CGo requirement adds
  a deployment variable that proves no Phase 1 product law.
- an in-memory implementation: cannot exercise WAL, process contention, or
  crash recovery.
- a client/server database: changes the architecture being tested.

## RQ-02 — What concurrency does WAL actually provide?

### Finding

SQLite WAL permits readers and a writer to proceed concurrently, but there is
still only one writer at a time. WAL depends on same-host shared-memory
coordination and is not supported over network filesystems.

### Decision

- One control store belongs to one machine/filesystem authority.
- Remote processes must eventually use an API; they must not open this database.
- Concurrent execution is expected, while database writes remain short and
  serialized.
- Tests must never interpret WAL as a distributed coordination mechanism.

### Architectural consequence

The store is a local authority ledger, not the location of agent work. Agents
write source in distinct Git worktrees, so database serialization does not
serialize their implementation time.

## RQ-03 — How should writer contention be acquired and reported?

### Finding

SQLite deferred transactions do not acquire a write transaction until the
first write. An upgrade from a read transaction can therefore encounter
`SQLITE_BUSY` later than the application expects. An immediate transaction
attempts to establish write authority at its beginning. SQLite's busy handler
can wait for a bounded interval before returning `SQLITE_BUSY`.

### Decision

- Use `_txlock=immediate` for explicit transactional writes.
- Set `_busy_timeout=5000` on every connection.
- Keep write transactions small and forbid filesystem, Git, or tracker calls
  while a transaction is open.
- Translate exhausted busy handling to a stable typed `control_store_busy`
  result containing operation and aggregate identifiers.
- Do not retry indefinitely. Command-level retries require the same idempotency
  key and must re-read operation state.

### Test obligation

Hold a writer from one process, contend from another, and prove either bounded
success or the typed busy result. A process-local mutex is not acceptable
evidence.

## RQ-04 — Which connection settings are invariant?

### Finding

Foreign-key enablement and busy handling are connection concerns. A configured
DSN is not sufficient evidence that every acquired connection observes the
required settings.

### Decision

Open with the validated driver parameters:

```text
_journal_mode=WAL
_foreign_keys=1
_busy_timeout=5000
_txlock=immediate
```

At startup, acquire a connection and query:

```sql
PRAGMA journal_mode;
PRAGMA foreign_keys;
PRAGMA busy_timeout;
```

Refuse service startup if the effective values are not `wal`, `1`, and `5000`.
Adapter tests must exercise more than one acquired connection.

## RQ-05 — What checkpoint behavior matters in Phase 1?

### Finding

WAL checkpoints may be delayed by readers that retain older snapshots. A
long-lived application read transaction can therefore allow the WAL to grow
and make checkpoint progress appear stuck.

### Decision

- Query methods must consume rows and close transactions promptly.
- Application services may not retain read transactions across agent work,
  tracker calls, Git commands, barriers, or user decisions.
- Rely on SQLite automatic checkpointing initially.
- Add an observability test that deliberately holds a reader and demonstrates
  the expected checkpoint limitation; do not build a checkpoint manager unless
  the experiment earns one.

## RQ-06 — Where does the database live?

### Finding

Each linked Git worktree has worktree-local Git metadata while sharing one Git
common directory. Ambient CWD therefore cannot identify the one control store
reliably.

### Decision

- Resolve the canonical Git common directory once when binding a repository.
- Anchor a project runtime root beside/under a managed location derived from
  that identity, never inside a candidate worktree.
- Record the canonical repository identity in the store and reject rebinding to
  a different repository.
- Keep the database, `-wal`, `-shm`, workspace registry, and fake-tracker store
  out of Git.

The exact runtime-root override flag remains an implementation detail. Its
containment and repository-binding behavior are acceptance requirements.

## Evidence-to-Decision Matrix

| Evidence | Decision affected | Required implementation check |
|---|---|---|
| Driver is CGo-free and version-resolvable | D-01 | Pin v1.57.0; build helper process |
| WAL has one writer and same-host scope | D-02, C-001 | Refuse known shared mounts; process contention test |
| Immediate transaction acquires writer early | D-02 | Use immediate writes; no deferred upgrade dependency |
| Busy timeout is bounded | D-02 | Typed busy result after 5000 ms |
| Pragmas are connection-sensitive | D-02 | Query effective values on acquired connections |
| Readers can delay checkpoints | D-02 | Close reads promptly; diagnostic test |

## Remaining Questions (Bounded)

These may be resolved during implementation without reopening the architecture:

1. Which Linux filesystem inspection method gives a conservative “known shared
   filesystem” refusal without pretending to detect every unsafe mount?
2. Whether `database/sql` connection-pool limits improve repeatability after
   the multi-process tests establish a baseline.
3. Whether the 100-run torture suite needs sharding to meet the local CI budget.

If testing shows the driver cannot maintain required per-connection settings,
or WAL behavior makes bounded multi-process authority unreliable, D-01/D-02
must be revised rather than hidden behind retries.

## Sources

Full provenance and observations are in `research/source-register.csv` and
`research/evidence-log.csv`.
