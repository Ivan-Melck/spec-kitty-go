# Continuation recovery and current stop

Status: **Environment recovered; selected production-observation layer still BLOCKED.**
No new test, runtime qualification, admission, promotion or completion is claimed.

## Fresh evidence

Evidence directory:
`/home/user/.cache/spec-kitty-parity-resume-followup/continuation-custody-99xrusz7/`

- `report.json`: independently verified all 54 current checkpoint source/docs,
  all 1,420 evidence files, both archive hashes and every archive member against
  `retained-storage-resource-checkpoint/manifest.json`. Prior functional manifest
  also matches. This verification preceded this new note/resume-link edit.
- Main HEAD `1f460f6c8fcdfa4b7f8fca2d244d92fcc5c25538` and coordination HEAD
  `fabc751d72396592a7d900860b97113694c2dd4d` match. Coordination status retains
  SHA256 `eee0a9e42d8969732dc8394d0bd3751472e5503ff883e2126e122c5a0b6da806`.
- The three pinned `/tmp` executables and clean qualification commit/ref were
  absent in this environment. The 58 original saved artifact copies all verified;
  saved qualification bundle SHA256
  `493c79000dbcf9ab4b63e8233533a33a2e2665b9000cf69a38cbfa6e128184cb` verified,
  with complete history and exactly the expected branch/commit.
- `restoration.json`: restored exact original executable paths using exclusive,
  no-follow creation in fresh owner-only directories; checked sizes/hashes and
  mode0500. No binaries executed or rebuilt. Imported saved bundle objects and
  compare-and-created the missing `parity/qualification-approved-01M4V7BW` ref
  at `41c20b14a31a9241833c39b0e3c4b6d652d7e136`, verified tree
  `f81c9201998c7a96fdac4dbd5809fc2a3de523ee`. No checkout/worktree created,
  no existing ref moved, no replacement refs. Dirty main and coordination were
  checked unchanged after recovery.

Recovery proves artifact identity, NOT old process/inode authority, qualification
of dirty source, a fresh runtime result or transfer of historical evidence.

## Requirement-to-evidence audit

Concrete deliverable remains the [selected observation contract](PRODUCTION_OBSERVATION_LAYER.md),
not general code activity or environmental repair. Its eight acceptance rows map
as follows; detailed historical evidence remains in [COMPLETION_AUDIT.md](COMPLETION_AUDIT.md).

| Contract row | Actual artifact/evidence | Current limitation |
|---|---|---|
| Lifetime/response attribution | `internal/controlstore/gapdb/retainedkernel/`, `internal/sidecar/gapdb/owned.go`, scoped code reviews | Complete live negative census remains open. |
| Closed read-only transport | `internal/controlstore/gapdb/retained_transport.go`, tests and reviews | No new complete source-bound qualification run. |
| Publication footprint | `retained_publication_map.go`, fixed geometry controls and review | Component approval is not endpoint acceptance. |
| Private footprint | `internal/productionruntime/retained_private_records.go`, tests/review | Historical admission/CAS authority is explicitly outside this layer. |
| Coherent total equality | `internal/productionruntime/retained_records_proof.go`, unchanged-image functional fixture | Deterministic expired-retained-extra witness still absent. |
| Resource/disclosure closure | [resource boundaries](RETAINED_RESOURCE_BOUNDARIES.md), partial allocator/library audit | Reporting boundary, shared runtime parameters and complete aggregate enumeration unresolved. |
| Real execution/regressions | Preserved prior unit/race/vet/architecture and same-image functional logs | No fresh tests run; P95, expiry and full negative qualification remain open. |
| Independent review/durable audit | Preserved scoped reviews; fresh manifest/member checks and restoration receipt | Scoped reviews plus preservation cannot complete the missing qualification rows. |

No new green test was sought as a proxy for these unmet requirements. Existing
failed probes, rejected reviews and receipts remain unmodified. Architect Op
`01M4V7BWB53R4CK18V28SFDD8A` and the thread goal remain open.

## Exact next decisions

1. **Resource acceptance boundary:** authorize separately reported observer-owned
   incremental allocation with borrowed/shared terms explicitly excluded from any
   aggregate claim, OR retain all-causal/full-reachable accounting and supply bounded
   shared-state/runtime/concurrency/baseline parameters or an approved isolation
   strategy. A narrower boundary requires explicit scope approval; no choice is
   inferred from a generic continuation request.
2. **Expiry witness:** authorize a source-backed, independently qualified writer-only
   releasable hold compatible with unchanged admitted images/topology, OR explicitly
   revise the evidence/admission scope to permit a separately qualified test image.
   Preserve original images/receipts; never retarget them or substitute TTL racing,
   simulated timers, filtered scans or a status-blocking snapshot hold.

Previous attempts and their source evidence are in
[RETAINED_RESOURCE_BOUNDARIES.md](RETAINED_RESOURCE_BOUNDARIES.md) and the selected
layer audit. Recovery removes environmental absence, not these authority-needed
roadblocks. Stop here pending those decisions; do not mark the goal complete.
