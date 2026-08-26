---
affected_files: []
cycle_number: 2
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T12:34:13Z'
reviewer_agent: user
wp_id: WP05
---

# WP05 Review Feedback — Cycle 2

Verdict: changes required.

Cycle 2 genuinely closes all five findings from cycle 1: explicit empty update values are represented and applied in one command; create/update replay validates live work fields; `catalog-beads` is a shipped production caller for create/read/update; bounded opaque caller metadata is representable and the adapter namespace is protected; and server mode uses a stable per-project/per-WorkID process-shared mapping lock.

Two remaining validation defects still block T023 and FR-007/FR-016.

## Blocking findings

### 1. A confirmed create does not validate the live semantic result

After a successful create envelope, `CreateWork` reads the issue but validates only mapping identity and the stored, adapter-supplied `content_digest` (`internal/tracker/beads/catalog.go:78-87`). The new `issueMatchesWorkInput` and `issueContainsProviderMetadata` checks are used for replay/recovery but not for the ordinary confirmed-success path.

A dishonest or skewed provider can therefore acknowledge create, store a different title/description/acceptance rendering, or drop caller-owned foreign metadata while retaining the submitted `spec_kitty.content_digest`; Spec Kitty returns a successful mutation containing the wrong provider state. This violates T023 requirement 8 (“validate the returned issue and metadata before reporting confirmed success”) and undermines the new caller-metadata guarantee.

Reproduction evidence used a temporary, removed test runner that altered the stored title to `provider-stored-wrong-title` and removed the caller's `human` metadata key immediately after returning successful create. `CreateWork` returned success. The probe command failed with:

```text
confirmed wrong create was accepted: title="provider-stored-wrong-title"
```

Apply the same immutable live semantic and caller-metadata validation used by replay to the confirmed create readback. Add permanent dishonest-provider tests that independently corrupt each provider-owned field and remove/change caller-owned metadata while leaving the namespace digest intact. Each case must return a typed conflict/ambiguity rather than success.

### 2. Semantic JSON comparison loses numeric precision

`semanticJSONEqual` decodes arbitrary JSON into `any` with the default decoder (`internal/tracker/beads/catalog.go:628-635`). JSON numbers therefore become `float64`. Distinct exact metadata values above 2^53, such as `9007199254740992` and `9007199254740993`, can compare equal after rounding. Create replay can then classify changed caller-owned metadata as semantically identical.

Use a lossless canonical comparison (`json.Decoder.UseNumber`, exact-number canonicalization, or an equivalent raw semantic representation). Add boundary tests for distinct large integers, equivalent map order, nested arrays/objects, and the intended policy for lexically different but mathematically equal JSON numbers.

## Anti-pattern checklist

1. **Dead code: PASS.** `RunTracker -> runCatalogBeads` is a shipped production path that calls Beads create/read/update.
2. **Synthetic-fixture test: PASS.** Correction tests invoke the production adapter and command boundary.
3. **Silent empty return: PASS.** No provider failure is swallowed behind an empty return.
4. **FR coverage: FAIL.** T023 confirmed-create verification and exact foreign-metadata drift remain uncovered/incorrect.
5. **Frozen surface: PASS.** The eight-file correction expansion is coherent with the explicitly requested cross-boundary fixes and introduces no forbidden dependency direction.
6. **Locked decision: FAIL.** Confirmed create can report applied without semantically validating the provider result; large-number drift can still be called an exact replay.
7. **Shared-file ownership: PASS.** The correction's cross-WP surfaces are the deliberate contract/command/helper expansions required by review cycle 1.
8. **Production fragility: PASS.** No unexplained transient panic/raise was introduced.

## Evidence run

- Correction tests (`CreatePreservesCallerOwnedProviderMetadata`, `ReplayRefusesLiveProviderDrift`, `UpdateCanClearDescriptionAndAcceptanceAtomically`, `ServerModeSerializesOnlyIdenticalStableMapping`) repeated 5x — PASS
- `go test ./internal/helpercmd -run 'TestTrackerCatalogBeads' -count=3` — PASS
- `go test ./internal/tracker -run 'TestOpaqueMetadata' -count=10` — PASS
- `go test ./internal/architecture -count=1` — PASS (10.239s)
- `go vet ./internal/tracker/... ./internal/helpercmd` — PASS
- `git diff --check 38c73c1^..38c73c1` — PASS
- Temporary dishonest confirmed-create probe — FAIL as described above; probe file removed and workspace restored

The correction is materially stronger and the original five findings should remain considered closed. The next cycle can stay narrowly focused on applying exact live validation consistently to confirmed create and making caller-metadata equality lossless.
