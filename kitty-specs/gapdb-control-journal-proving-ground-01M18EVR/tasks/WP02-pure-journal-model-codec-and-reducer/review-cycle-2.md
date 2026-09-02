# WP02 review cycle 2

Verdict: **rejected**

Reviewer: `reviewer-renata-wp02-2` (`reviewer-renata`)

Implementation reviewed: cycle-two fix `d8c58ca` together with the original WP02
implementation and its bounded architecture classification.

## Cycle-one replay

All six prior blocker classes now fail closed on their real production seams:

1. malformed envelopes paired with a version-one zero or error digest are
   refused by `ValidatedJournalV1.Len`, `At`, and the reducer before its callback;
2. zero, manually incomplete, and operation/effect/provider/revision/marker/value
   mismatches cannot satisfy `CanCompensateEffectV1`;
3. wire event indexes 128, 255, 256, 65535, and overflow values are rejected
   before narrowing;
4. missing, null, and wrong-shape required fields are rejected across all six
   durable decoders; an empty effect list round-trips as `[]`, and absent optional
   recorded observations remain absent;
5. aggregate-head wrap at `MaxUint64` and incomplete proposed heads are refused;
   and
6. uppercase, mixed-case, short, long, and non-hex encodings are rejected for
   the event, aggregate-head, request, and precondition SHA-256 fields.

The fixes are deletion-sensitive: the exact mutants above reproduce the prior
failures against the pre-fix implementation, rather than merely inspecting code
shape.

## Blocking findings

### 1. Operation transitions accept zero and manually invalid receivers

The three in-memory transition seams mutate selected fields without first
validating the receiver (`internal/controljournal/operation.go:68-97`). Each can
therefore manufacture a successful-looking branch from a value that no
constructor or decoder would admit:

```go
got, err := (OperationOutcomeV1{}).WithDelivery(DeliveryReturned)
// err == nil; got.Delivery == DeliveryReturned

got, err = (OperationOutcomeV1{}).WithRecovery(RecoveryBytesRestored)
// err == nil; got.Recovery == RecoveryBytesRestored

got, err = ReconcileAmbiguousOperationV1(
    OperationOutcomeV1{Kind: OperationAmbiguous},
    OperationDurablyApplied,
)
// err == nil; got claims reconciled/durably_applied
```

Encoding these values fails later, but T006 requires zero/invalid values to be
rejectable and T010 requires deterministic validation at branching seams. A
caller may inspect the returned branch before any encoder runs, so encoder-only
validation does not close this path.

Required remediation: define one complete `OperationOutcomeV1` invariant check
covering schema, identity, kind/resolution, delivery, recovery, result type and
canonical result. Invoke it before every transition and after applying the
candidate transition. Add zero, manually partial, wrong-schema, malformed-result,
and illegal prior-state mutants for all three entry points. Fail with the existing
bounded typed diagnostics and return the zero outcome on error.

### 2. Reducer and projection constructors admit malformed typed identifiers

`NewRegistryV1` checks aggregate kind, event type, and reducer version only for
non-emptiness (`internal/controljournal/reducer/reducer.go:58-73`).
`NewProjectionV1` applies the same nonempty-only rule to aggregate keys
(`reducer.go:87-109`). Values that violate the frozen token grammar are admitted:

```go
NewRegistryV1([]EntryV1{{
    AggregateKind: "bad kind",
    EventType: "mission.updated",
    EventSchemaVersion: 1,
    ReducerVersion: "mission-v1",
    Reduce: reducer,
}}) // succeeds

NewProjectionV1(
    map[AggregateKeyV1]StateV1{{Kind: "bad kind", ID: "agg-1"}: state},
    map[AggregateKeyV1]uint64{{Kind: "bad kind", ID: "agg-1"}: 1},
) // succeeds
```

The same result occurs for malformed event types and reducer versions. This
contradicts T006's validated opaque values, T008's closed supported-version
registry, FR-015's bounded versioned event vocabulary, and FR-016's fail-closed
registry law.

Required remediation: expose or reuse root-package validation constructors at
the reducer boundary; do not duplicate a weaker grammar. Validate every registry
coordinate and every projection aggregate key, including the 128-byte bound.
Add spaces, illegal leading punctuation, invalid UTF-8, and 129-byte mutants for
each applicable coordinate. Preserve the current duplicate-key and nil-reducer
refusals.

## Bounded adjacent audit

- Required-field presence is checked before typed decoding; unknown fields,
  duplicate keys at any depth, nulls, trailing JSON values, and wrong container
  shapes refuse.
- Other wire integers reject negative, fractional, boundary-plus-one, and target
  type overflow values before construction.
- Event digest reconstruction excludes both the digest field and external
  `RecordRevision`; validation/seal errors propagate without creating a usable
  capability.
- Effect-intent and provider-observation constructors/codecs bind the same
  operation, effect, provider, revision, marker, and current value coordinates.
- Reducer entry checks journal validity before cloning state or invoking a
  callback; reducer failures publish no partial projection, and deterministic
  ordering/digest tests remain live.
- Diagnostics remain typed, bounded, and do not echo arbitrary input payloads.

No additional blocker was found within the bounded adjacent surface.

## Verification performed

Passing supplied and independent checks:

- `go test ./internal/controljournal/... -count=1`
- `go test ./internal/controljournal/... -count=20`
- `go test -race ./internal/controljournal/... -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/controljournal/...`
- `staticcheck ./internal/controljournal/...`
- bounded two-second codec and reducer fuzz campaigns
- `gofmt` cleanliness and `git diff --check`

Temporary same-package reviewer probes reproduced both blocker classes and
replayed the cycle-one matrix. They were removed before this feedback was
written. Per mission guidance, the known long-running scenario suite was not
run.

## Review checklist

1. Dead code: **N/A** for this staged foundation; downstream WPs are the declared
   application/playback consumers, while the reducer already consumes the frozen
   journal capability.
2. Synthetic-fixture tests: **PASS** for supplied coverage; tests call production
   seams. The two missing negative-control families above remain blockers.
3. Silent empty return: **PASS**; no silent error swallowing was found.
4. FR coverage: **FAIL** for T006/T008/T010 and FR-015/FR-016 because invalid
   outcomes and malformed registry coordinates are admitted.
5. Frozen surface: **PASS**; WP02 changed no frozen contract, schema, or module
   file.
6. Locked decision: **FAIL**; the accepted mutants cross the frozen invalid-value
   and closed-registry laws.
7. Shared-file ownership: **PASS**; the only out-of-map production change is the
   small pure-tier architecture classification in `46bda2c`, with its rationale
   recorded in the commit.
8. Production fragility: **PASS**; no new fail-loud path was found unsafe under a
   transient race.

The cycle-one remediation is materially sound. Approval is blocked only by the
two concrete constructor/transition validation families above.
