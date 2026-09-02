# M2 Reproduction and Qualification Quickstart

This is the target acceptance recipe for M2. Commands named `m2-*` become
executable gates as their owning work packages land. Until final qualification,
their existence does not claim production adoption.

## 1. Preconditions

- Linux/amd64 on one local filesystem authority
- Git, `jq`, `sha256sum`, and Go 1.26.7 toolchain support
- a clean checkout of the exact candidate source commit
- no shared/network control directory
- enough local space for source-bound binaries, SBOM, fault evidence, and the
  10,000-operation recovery fixture

Hosted sync, Beads production composition, stack-neutral acceptance providers,
API 2.0, and MCP are later mission concerns and remain disabled in this run.

## 2. Create isolated absolute paths

From the repository root:

```bash
M2_REPO_PATH="$(realpath "$(git rev-parse --show-toplevel)")"
M2_SCRATCH_PATH="$(mktemp -d /tmp/spk-m2.XXXXXXXX)"
M2_BIN_PATH="$M2_SCRATCH_PATH/bin"
M2_RUNTIME_PATH="$M2_SCRATCH_PATH/runtime"
M2_DATA_PATH="$M2_SCRATCH_PATH/data"
M2_EVIDENCE_PATH="$M2_SCRATCH_PATH/evidence"
M2_INSTALL_PATH="$M2_SCRATCH_PATH/install"
M2_SIDECAR_PATH="$M2_BIN_PATH/gapdbd"
M2_QUALIFIER_PATH="$M2_BIN_PATH/spk-m2-qualification"
M2_ADOPTION_PATH="$M2_REPO_PATH/docs/programs/evidence/m1-adoption.json"
M2_RUN_ID="m2-crown-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p "$M2_BIN_PATH" "$M2_RUNTIME_PATH" "$M2_DATA_PATH" \
  "$M2_EVIDENCE_PATH" "$M2_INSTALL_PATH"
chmod 0700 "$M2_SCRATCH_PATH" "$M2_BIN_PATH" "$M2_RUNTIME_PATH" \
  "$M2_DATA_PATH" "$M2_EVIDENCE_PATH" "$M2_INSTALL_PATH"
```

Every authority path is explicit and absolute. Do not add the scratch binary
directory to `PATH`; the sidecar supervisor verifies and descriptor-executes an
exact binary.

## 3. Reverify M1 admission

The committed adoption record must contain the accepted coordinates:

```bash
jq -e '
  .decision == "adopt" and
  .source_commit == "e05a5e46feb3e65ff42c4544c16678c440ef3818" and
  .source_tree == "c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73" and
  .evidence_manifest_sha256 == "397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309" and
  .admitted_tuple_sha256 == "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d" and
  .sidecar_sha256 == "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
' "$M2_ADOPTION_PATH"
```

Download the immutable M1 release archive and checksum from
`https://github.com/spec-kitty/spec-kitty-redesign/releases/tag/m1-gapdb-evidence-v1`.
The archive SHA-256 must be:

```text
86b8cd608f5cd9d1dce7ce25dffa5d87e1df25a233847316416f14e7948a18df
```

M2 preflight performs that download and independent manifest verification. A
mutable branch or cached unpacked directory is not acceptable entry evidence.

## 4. Install the exact admitted sidecar

```bash
GOBIN="$M2_BIN_PATH" \
CGO_ENABLED=0 \
GOTOOLCHAIN=go1.26.7 \
go install -trimpath \
  github.com/spec-kitty/gapdb/cmd/gapdbd@v0.0.0-20260824033134-ff3dd478f87c

test "$(stat -c '%s' "$M2_SIDECAR_PATH")" = "5300621"
test "$(sha256sum "$M2_SIDECAR_PATH" | cut -d ' ' -f 1)" = \
  "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
```

The product invokes this exact absolute object. It never imports GapDB server
internals, discovers another executable, or falls back to SQLite.

## 5. Run progressive development gates

```bash
GOTOOLCHAIN=go1.26.7 make m2-contract
GOTOOLCHAIN=go1.26.7 make m2-capabilities
GOTOOLCHAIN=go1.26.7 make m2-recovery
GOTOOLCHAIN=go1.26.7 make m2-race
GOTOOLCHAIN=go1.26.7 make m2-fault
GOTOOLCHAIN=go1.26.7 make m2-mutants
GOTOOLCHAIN=go1.26.7 make m2-absence
```

`m2-contract` validates every JSON schema/document and the event-family
registry. `m2-capabilities` includes behavior/refusal parity. Recovery includes
nonzero checkpoint suffixes, corrupt-latest fallback, and zero-I/O playback.
The absence gate inspects source, module graph, SBOM, binaries, installation,
configuration, help, runtime probes, and one mutant per prohibited edge.

## 6. Build exact qualification artifacts

```bash
GOTOOLCHAIN=go1.26.7 CGO_ENABLED=0 go build -trimpath \
  -o "$M2_QUALIFIER_PATH" ./cmd/spk-m2-qualification

GOTOOLCHAIN=go1.26.7 make m2-sbom \
  M2_OUTPUT_DIR="$M2_EVIDENCE_PATH/$M2_RUN_ID"
```

The final verifier records source commit/tree, Go version, module hashes,
sidecar digest, qualifier digest, production binary digests, deterministic
CycloneDX SBOM digest, and the complete test/mutant census.

## 7. Run the M2 crown campaign

```bash
"$M2_QUALIFIER_PATH" preflight \
  --repo "$M2_REPO_PATH" \
  --sidecar "$M2_SIDECAR_PATH" \
  --m1-adoption "$M2_ADOPTION_PATH" \
  --runtime "$M2_RUNTIME_PATH" \
  --data "$M2_DATA_PATH" \
  --evidence "$M2_EVIDENCE_PATH" \
  --install-root "$M2_INSTALL_PATH" \
  --run-id "$M2_RUN_ID"

"$M2_QUALIFIER_PATH" run \
  --repo "$M2_REPO_PATH" \
  --sidecar "$M2_SIDECAR_PATH" \
  --m1-adoption "$M2_ADOPTION_PATH" \
  --runtime "$M2_RUNTIME_PATH" \
  --data "$M2_DATA_PATH" \
  --evidence "$M2_EVIDENCE_PATH" \
  --install-root "$M2_INSTALL_PATH" \
  --run-id "$M2_RUN_ID"
```

One RunID must contain the entire census:

- every authority/event family and frozen behavior;
- crash/ambiguity/restart repetitions;
- 100 two-agent concurrency trials;
- complete two-item mission with restart after every major phase;
- historical-root refusal matrix;
- structural absence and clean-install evidence;
- 30 recovery measurements and 10,000 query/mutation observations; and
- all rejecting mutants.

A rerun uses a new RunID and preserves failed/incomplete evidence. Results from
different commits or runs cannot be assembled into a pass.

## 8. Verify independently

```bash
"$M2_QUALIFIER_PATH" verify \
  --repo "$M2_REPO_PATH" \
  --sidecar "$M2_SIDECAR_PATH" \
  --m1-adoption "$M2_ADOPTION_PATH" \
  --evidence "$M2_EVIDENCE_PATH" \
  --run-id "$M2_RUN_ID"
```

The verifier validates the manifest against
`contracts/m2-qualification-v1.schema.json`, recomputes every digest and count,
requires zero SQLite edges, and checks all thresholds. A machine pass makes the
candidate eligible for independent mission review; it does not self-approve.

## 9. Historical-root negative check

Copy only disposable fixtures into a separate root. Never point qualification
at a valuable historical project directory.

```bash
M2_HISTORICAL_FIXTURE="$M2_SCRATCH_PATH/historical-fixture"
mkdir -p "$M2_HISTORICAL_FIXTURE"
cp "$M2_REPO_PATH/testdata/historical/control.db.fixture" \
  "$M2_HISTORICAL_FIXTURE/renamed-state.bin"

"$M2_QUALIFIER_PATH" classify-root \
  --control-root "$M2_HISTORICAL_FIXTURE" \
  --output "$M2_EVIDENCE_PATH/$M2_RUN_ID/historical-disposition.json"

jq -e '
  .classification == "historical" and
  .action == "offline_disposition_required" and
  .current_mutation_count == 0
' "$M2_EVIDENCE_PATH/$M2_RUN_ID/historical-disposition.json"
```

The fixture path and command become available in their owning work package.
Production never opens this fixture through SQL and never offers an importer.
