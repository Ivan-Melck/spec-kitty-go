# Phase 1 Proving-Ground Quickstart

This document defines the eventual operator path for reproducing the Phase 1
evidence. Commands are acceptance targets until their implementing work package
lands.

## Prerequisites

- Go 1.26 or newer
- Git 2.43 or newer
- Linux on one local filesystem authority
- no C compiler or standalone `sqlite3` executable required

## Build and Baseline

```bash
go build ./cmd/spk-phase1-helper
go test ./...
```

The scenario harness creates disposable repositories, control stores, tracker
stores, and worktrees below the Go test temporary directory. It must not mutate
the repository from which the tests are invoked.

## Run the Crown-Jewel Experiment

```bash
go test ./internal/scenario -run TestPhase1TwoAgentAuthority -v
```

The test must demonstrate, in this order:

1. two independent processes claim two work items;
2. each receives a distinct lease-scoped Git worktree;
3. one process is stopped at a named durable boundary;
4. its lease expires and a replacement receives a new workspace;
5. the stale process resumes and commits only in its quarantined workspace;
6. current authority rejects every stale mutation;
7. immutable candidates receive independent ActorID-level review;
8. accepted results integrate through one target-ref mutation path.

Failure output includes stable IDs, exact commits, observed/expected states,
retry safety, and recovery actions.

## Exercise a Named Fault

```bash
go test ./internal/scenario -run 'TestPhase1StartRun/SR-04' -v
go test ./internal/scenario -run 'TestPhase1Integration/IG-05' -v
```

Fault boundaries are enumerated in `contracts/fault-boundaries.md`. Tests use
barriers and an injected clock; they do not depend on arbitrary sleeps.

## Release Evidence

```bash
go test ./...
go test -race ./...
go test ./internal/scenario -run Phase1 -count=100
```

All three commands must pass before the ADR verdict is ratified. If a safety
scenario fails, record a revise/reject verdict; do not weaken or skip it.

## Inspect Without Mutating

The helper's query surface emits structured JSON and never repairs by default:

```bash
spk-phase1-helper inspect --control-db <path> --repository <path>
```

Any cleanup command must refuse unknown, dirty, active, or quarantined
workspaces. Quarantined content is preserved for explicit reconciliation.
