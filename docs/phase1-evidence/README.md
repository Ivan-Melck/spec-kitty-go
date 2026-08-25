# Phase 1 empirical evidence

This directory records the reproducible evidence for WP08. The executable
source of truth is the `Phase1` test family in `internal/scenario`; the JSON
files index its fault coverage, environment, commands, and results.

The frozen source candidate is
`adf15aac42f3f03dd27ad6252df973bcd8b42433`. Its final source-bound campaign
passed; independent WP08 review is still required. `scenario-catalog.json` is
the exact machine-readable coverage ledger: 31 of 35 listed scenarios are complete.
The final evidence payload is commit
`c02d6a7bb60ed1495734bb3e23744f872db629e5`; the following docs-only commit
attests that hash to avoid a circular commit identifier.
P1-15, P1-18, P1-21, and P1-23 are intentionally partial. Their executable
halves cover quarantined-output inspection/refusal, newer-handoff rejection of
a pending older judgment, durable independent rejection, and refusal to
integrate moved candidate C2 under C's approval. Human-authorized imports,
already-recorded-judgment supersession policy, automatic rework-run creation,
and acceptance/queue surfaces remain explicitly deferred to their named owners.
`results.json` preserves the first repetition campaign as superseded and
non-admissible rather than treating its 46 completed shards as a final pass. It
also records both final-campaign runner-configuration mistakes instead of
silently replacing them.

The torture chamber uses one real Git repository, one real SQLite control
database, one portable fake tracker, two work items, and separate helper
processes. Fault injection is barrier-driven. Tests never write control tables
to manufacture recovery state. Deliberate external branch/target movement uses
ordinary Git argv, while product-owned evidence refs are created only by the
product path. All control-plane mutations use existing helper commands or
service contracts. Exact expected rows come from scenario inputs, pre-fault
records, and returned capsules rather than post-fault database enumeration.
Every SR/HR/IG matrix fixture contains its own expired authority and uses it to
exercise the refusing handoff, review, and integration seams before local
assertions.

Run the validation commands from the repository root:

```text
go test ./...
go test -race ./...
go test ./internal/scenario -run Phase1 -count=100
go vet ./...
staticcheck ./...
```

The monolithic repetition command was executed as 65 exact, disjoint shards,
bounded to six concurrent processes and two Go schedulers per shard on the
12-CPU host. Every shard ran 100 iterations. The machine-readable
`final-campaign-manifest.json` records the complete top-level union, exact
regex, command, time bounds, exit code, duration, raw-log path, and SHA-256 for
each shard; it asserts no gaps and no overlap. All 65 shards passed without a
rerun. `go test ./... -timeout=30m`, `go test -race ./... -timeout=60m`, vet,
staticcheck, formatting, diff-check, module verification, vulnerability scan,
and binary builds also passed on the frozen source.

`results.json` contains only completed commands and explicitly records aborted
or superseded runner attempts. This green result supports ratification inside
the documented single-machine/local-filesystem boundary; it does not establish
correctness for shared filesystems, multiple database owners, remote agents
opening SQLite, a real external tracker, power loss, or network partitions.
