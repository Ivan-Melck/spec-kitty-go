# WP09 review feedback — cycle 3

Verdict: **changes requested**.

## Blocking finding

### The projection read model accepts reconciliation facts that the authority model forbids

**Severity:** blocker  
**Location:** `internal/workgraph/readmodel/model.go`, `newReconciliation` (currently lines 451–477)

The read-model constructor validates only that targets exist, are unique, and are non-empty for `continue`, `supersede`, and `fork`. It does not preserve the authority model's disposition-specific identity laws. A temporary black-box mutant proved that `NewSnapshot` accepts all of these impossible facts:

- `continue`: `WP01 -> WP02`
- `continue`: `WP01 -> [WP01, WP02]`
- `supersede`: `WP01 -> WP01`
- `invalidate`: `WP01 -> WP01`
- `fork`: one successor only

This is not merely looser presentation validation. The generated document labels these values as reconciliation facts from canonical state, so the read-model seam must not admit a shape that `workgraph.validateMixedDecisionShape` rejects. Otherwise a composition defect or corrupt stored row can be rendered as authoritative provenance even though the authority kernel could never create it.

**Required repair (confined to the owned read-model files):**

1. In `internal/workgraph/readmodel/model.go`, enforce the disposition shapes the read model can prove:
   - `continue`: exactly one target, equal to `PreviousWorkID`;
   - `supersede`: one or more targets, none equal to `PreviousWorkID`;
   - `invalidate`: zero or more targets, none equal to `PreviousWorkID` (the status-specific integrated repair rule remains upstream because this minimal read model carries no old status);
   - `fork`: at least two distinct targets, none equal to `PreviousWorkID`.
2. In `internal/workgraph/readmodel/model_test.go`, add permanent rejection controls for all five mutants above and positive controls for every disposition. Preserve the existing canonical ordering and defensive-copy guarantees.
3. Keep this repair inside `internal/workgraph/readmodel/model.go` and `model_test.go`; no projection, authority, provider, or composition changes are needed.

The temporary probe was removed after reproducing the defect. Its focused command was:

```text
go test ./internal/workgraph/readmodel -run '^TestWP09ReviewProbe' -count=1 -v
```

It failed all five negative cases because construction returned `nil` errors; the valid `continue WP01 -> WP01` control passed.

## Evidence that passed

- Product commit `7ba7f5f` changes exactly the nine WP09-governed files.
- Clean focused repeat: `go test ./internal/projection ./internal/mission/readmodel ./internal/workgraph/readmodel -count=10`.
- Clean race repeat: `go test -race ./internal/projection ./internal/mission/readmodel ./internal/workgraph/readmodel -count=3`.
- Architecture: `go test ./internal/architecture -count=1`.
- Static/format: focused `go vet`, `staticcheck`, and `gofmt -d` passed.
- Existing campaigns cover 100 input permutations, 100 edit/delete/corrupt trials, hostile Markdown, one-terminal-LF/no-trailing-whitespace, bounded diagnostics, injected atomic-write stages, concurrent writers/readers, symlink/traversal refusal, and the 100-item performance target.
- Production dependency direction is preserved: projection imports only foundation/read-model surfaces; authority/provider packages do not import projection; there is no Markdown reader/parser/import/apply/reverse-sync production seam.
- The apparent lack of a production caller is an explicit delivery boundary, not dead code: WP10 T054 owns the `projection tasks` composition path.
- Contract walk: `reconciliation.schema.json` and `work-revision-v2.schema.json` are relevant; the remaining artifact/command/next-result contracts are orthogonal. This finding strengthens semantic validation without changing either wire contract.
- `internal/scenario` is absent from the nine-file product diff, so the recorded full-scenario timeout baseline is not attributable to WP09.

## Review anti-pattern checklist

- Dead or unwired production code: **not a blocker** — explicitly consumed by WP10 T054.
- Synthetic-only success path: **pass** — tests call the production constructors, renderer, and atomic writer.
- Silent empty-result path: **pass**.
- Functional-requirement coverage: **fail** — reconciliation semantic fidelity is incomplete as described above.
- Frozen compatibility behavior: **pass**.
- Locked architecture/semantic decisions: **fail** — the read-model seam admits reconciliation shapes rejected by the authority kernel.
- Shared-ownership/scope drift: **pass** — exact nine-file scope.
- Production fragility hidden by fixtures: **pass**, except for the explicit reconciliation mutant above.

WP10 consumes this read model and should not integrate against the current shape until the blocker is repaired and independently re-reviewed.
