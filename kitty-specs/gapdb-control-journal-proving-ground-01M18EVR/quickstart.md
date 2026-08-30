# M1 Reproduction Quickstart

This is the planned acceptance recipe for the GapDB control-journal proving
ground. The production packages and `spk-m1-proving-ground` command do not
exist at planning time; these commands become executable implementation exit
criteria. Running them does not itself authorize production migration.

## 1. Preconditions

- Linux/amd64 on one local filesystem authority with owner-visible `flock`,
  durable file and parent-directory `fsync`, and same-filesystem atomic rename
- Git and Go 1.26.7 toolchain support
- a clean checkout of the governed implementation commit
- no network/shared control directory

Team Kitty is deliberately outside this run. Do not configure hosted sync,
hosted tracker credentials, or a Team Kitty endpoint. The campaign uses a
bounded local fake provider for adverse tracker facts and remains local-only.

## 2. Create isolated project-local roots

From the repository root:

```bash
M1_REPO_ROOT="$(git rev-parse --show-toplevel)"
M1_RUNTIME_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/spk-m1.XXXXXXXX")"
M1_BIN_ROOT="$M1_RUNTIME_ROOT/bin"
M1_DATA_ROOT="$M1_RUNTIME_ROOT/data"
M1_EVIDENCE_ROOT="$M1_RUNTIME_ROOT/evidence"
mkdir -p "$M1_BIN_ROOT" "$M1_DATA_ROOT" "$M1_EVIDENCE_ROOT"
chmod 0700 "$M1_RUNTIME_ROOT" "$M1_BIN_ROOT" "$M1_DATA_ROOT" "$M1_EVIDENCE_ROOT"
```

Do not add `$M1_BIN_ROOT` to `PATH`. The supervisor receives the absolute
verified executable path.

## 3. Install and verify the admitted sidecar

```bash
GOBIN="$M1_BIN_ROOT" \
CGO_ENABLED=0 \
GOTOOLCHAIN=go1.26.7 \
go install -trimpath \
  github.com/spec-kitty/gapdb/cmd/gapdbd@v0.0.0-20260824033134-ff3dd478f87c

M1_GAPDBD="$M1_BIN_ROOT/gapdbd"
test "$(stat -c '%s' "$M1_GAPDBD")" = "5300621"
test "$(sha256sum "$M1_GAPDBD" | cut -d ' ' -f 1)" = \
  "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"

test "$(sha256sum "$M1_REPO_ROOT/docs/programs/evidence/m1-admission.json" | cut -d ' ' -f 1)" = \
  "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
```

Module verification must also find these exact entries in the module download
evidence:

```text
github.com/spec-kitty/gapdb v0.0.0-20260824033134-ff3dd478f87c h1:kmYoO7Q3PlsNM6M0dSYtuJJSQSlbflC2wz3d/Bek2L0=
github.com/spec-kitty/gapdb v0.0.0-20260824033134-ff3dd478f87c/go.mod h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=
```

The supervisor independently reopens, hashes, and descriptor-executes the
artifact. The shell checks are operator feedback, not lifecycle admission.

## 4. Validate contracts and focused behavior

These are the target implementation gates:

```bash
go test ./internal/architecture -run '^TestM1' -count=1
go test ./internal/controljournal/... -count=1
go test ./internal/controlstore/gapdb/... -count=1
go test ./internal/sidecar/gapdb/... -count=1
go test ./internal/provingground/... -count=1
go test ./internal/architecture/... -count=1
go test -race ./internal/controljournal/... ./internal/provingground/... -count=1
```

The contract gate checks every JSON document/schema under
`kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/`,
canonical fixtures, product-budget exact/plus-one boundaries, and the sidecar's
public Status and DescribeConfig limit fields.

## 5. Run the disposable external-adapter suite

```bash
go test ./internal/controlstore/gapdb/... ./internal/sidecar/gapdb/... \
  -run 'External|Lifecycle|AmbiguousResponse' \
  -count=1 \
  -args \
  -gapdbd "$M1_GAPDBD" \
  -data-root "$M1_DATA_ROOT"
```

The test harness starts the exact binary itself. It does not start an embedded
server, search `PATH`, parse human CLI output, or reuse a persistent developer
database.

## 6. Run the one-RunID combined campaign

```bash
GOTOOLCHAIN=go1.26.7 go build -trimpath \
  -o "$M1_BIN_ROOT/spk-m1-proving-ground" \
  ./cmd/spk-m1-proving-ground

"$M1_BIN_ROOT/spk-m1-proving-ground" qualify \
  --gapdbd "$M1_GAPDBD" \
  --data-root "$M1_DATA_ROOT" \
  --evidence-root "$M1_EVIDENCE_ROOT" \
  --product-policy \
    "$M1_REPO_ROOT/kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/m1-product-budgets-v1.json" \
  --external-scenarios all-14 \
  --faults GJ-01,GJ-02,GJ-03,GJ-04,GJ-05 \
  --ordered-torture-trial \
  --focused-repetitions 100
```

One invocation creates one umbrella RunID. It emits exactly 14 ordered terminal
scenario records and one terminal ordered-trial record. The 100 repetitions are
focused fault/property subtrials summarized beneath those records; they do not
multiply the top-level census. The run may not assemble a pass from previous
runs. Only after terminal failure/incompletion may the operator rerun the full
campaign with a new RunID while preserving the prior evidence.

`GJ-02` uses a distinct product-facing Unix-socket proxy that consumes a
complete accepted daemon response and drops it before delivery; reconciliation
is a later ordinary public read. The trial preserves Process B's W2 while
expired Process A's W1 is quarantined and Actor A/Process C receives W3. In the
same umbrella run it restarts the owner/host, admits a new RuntimeEpochID,
refuses the predecessor capsule, and proves equal restore/playback digest with
zero playback adapter calls and durable writes.

## 7. Verify evidence

```bash
"$M1_BIN_ROOT/spk-m1-proving-ground" verify-evidence \
  --evidence-root "$M1_EVIDENCE_ROOT" \
  --require-scenarios 14 \
  --require-faults GJ-01,GJ-02,GJ-03,GJ-04,GJ-05 \
  --require-ordered-torture-trial \
  --require-focused-summaries all-eight \
  --minimum-focused-repetitions 100 \
  --require-mutants \
  --require-independent-review
```

The verifier recomputes every digest, enforces one RunID, checks the admitted
source/module/binary/protocol tuple, distinguishes scenario/cut/repetition
counts, and rejects missing or foreign artifacts. A successful verifier makes
the record eligible for a human decision; it does not make the decision.
Failed and incomplete manifests remain schema-valid, retained evidence, but the
pass predicate rejects any failed/nonterminal child, false invariant, surviving
mutant, incomplete focused census, or failed ordered-trial crown field.

## 8. Adoption or refusal

The accountable human signs a record conforming to
`contracts/adoption-record-v1.schema.json` against the exact evidence-manifest
hash. Either `adopt` or `refuse` is a valid mission artifact. A failed or
incomplete campaign must be retained, the authority runtime kept closed, and M2
left unadmitted.

## 9. Cleanup

Preserve `$M1_RUNTIME_ROOT` until evidence review and the human decision are
complete. Afterward, remove only that exact temporary directory using the
operator's normal recoverable cleanup procedure. The qualification command
never deletes an ambiguous store or process.

## Branch note

The current planning CLI may report the mission branch as branch context. That
is a known CLI defect, not merge authority. Governed lanes integrate to
`kitty/mission-gapdb-control-journal-proving-ground-01M18EVR`, and the
accepted mission ultimately merges to `main`; no self-merge is authorized.
