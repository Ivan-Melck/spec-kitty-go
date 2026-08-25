# Quickstart: Phase One Architecture Closeout

Run commands from the repository root.

## Fast development loop

```bash
make preflight
make contract
make scope
make acceptance
```

## Pull-request qualification

```bash
make quality
```

The quality target runs the bounded PR-equivalent campaign and verifies curated test censuses.

## Focused regression proofs

```bash
# Architecture policy and rejecting mutants
go test ./internal/architecture/...

# Contention stability
go test -count=20 -run TestBusy ./internal/controlstore/sqlite

# Early integration refusal and universal invariants
go test -run 'Test.*Refus|Test.*Invariant' ./internal/scenario ./internal/controlstore/sqlite

# Governance context and migration
go test ./internal/governancecontext ./internal/controlstore/... ./internal/execution/...
```

Exact test names are finalized during implementation; CI manifests, not this narrative example, are authoritative.

## Exhaustive local campaign

```bash
make full-campaign
```

This includes fault injection, zombie recovery, complete scenarios, repetitions, race detection, static analysis, dependency checks, and binary builds. It is intentionally not the default edit loop.

## Architecture mutation expectation

The guard accepts provider dependencies only from classified provider adapters. A future adapter package may exist at `internal/tracker/beads`; importing it from execution, workspace, review, integration, workgraph, handoff, or control-store contracts must fail.

## Governance-context expectation

New runs require a validated context. Databases created before this mission remain readable and report historical provenance as unknown; they never claim today's governance governed yesterday's run.

## External contract expectation

No Orchestrator API executable is added. Review [contracts/orchestrator-api-1.3-projection.md](contracts/orchestrator-api-1.3-projection.md) when designing command or result changes.
