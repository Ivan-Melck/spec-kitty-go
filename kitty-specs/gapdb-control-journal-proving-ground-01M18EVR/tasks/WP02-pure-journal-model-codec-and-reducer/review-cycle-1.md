# WP02 review cycle 1

Verdict: **rejected**

Reviewer: `reviewer-renata-wp02` (`reviewer-renata`)

Implementation reviewed: `1867b34` plus architecture classification `46bda2c`
against the coordination branch.

## Blocking findings

### 1. The validation seal fails open when content encoding fails

`ValidatedJournalV1.contentDigest` returns the all-zero digest when an envelope
cannot be encoded (`internal/controljournal/model.go:593-605`). `isValid` then
accepts that digest when a manually formed capability has seal version 1 and a
zero seal digest (`model.go:589-590`). This violates the frozen law that zero,
manually formed, and content/seal-mismatched capabilities return
`InvalidValidatedJournalError` before exposing records or reaching reducers.

Reproducer in a temporary same-package test:

```go
forged := ValidatedJournalV1{
    seal: validationSealV1{version: 1},
    records: []StoredEventV1{{
        RecordRevision: 1,
        Envelope: EventEnvelopeV1{payload: []byte("{")},
    }},
}
if length, err := forged.Len(); err == nil {
    t.Fatalf("manually formed capability became usable: Len=%d", length)
}
```

Observed: `Len=1` with no error; `At(0)` also exposes the record.

Required remediation: make seal recomputation fail closed as a `(digest,
error)`-style operation (or equivalent), reject a zero/error digest, and add
same-package negative controls proving `Len`, `At`, and reducer entry points
return `InvalidValidatedJournalError` before state or reducer activity.

### 2. Compare-before-compensate admits unattributed zero-ish values

`CanCompensateEffectV1` checks only schema numbers and equality of exported
fields/byte slices (`internal/controljournal/operation.go:241-247`). Two
manually formed values with only `SchemaVersion: 1` therefore compare as safely
compensable even though they have no intent, effect, operation, provider,
revision, marker, or value attribution.

```go
intent := EffectIntentV1{SchemaVersion: 1}
observation := ProviderObservationV1{SchemaVersion: 1}
if CanCompensateEffectV1(intent, observation) {
    t.Fatal("empty values became safely compensable")
}
```

Observed: the predicate returns `true`. This violates T010 and the saga law
that compensation may undo only external state demonstrably attributable to
the operation being compensated.

Required remediation: validate both complete values at the decision seam and
bind every required attribution coordinate before returning true. Add zero,
manually formed, field-tampered, wrong-operation/effect, revision, marker, and
value mutants.

### 3. Event-index decoding narrows an invalid wire value into a valid one

The wire type is `uint16`, but decoding casts it directly to the `uint8`-backed
`EventIndexV1` before range validation (`internal/controljournal/codec.go:130-143`).
A wire `event_index` of 256 becomes zero. When the supplied digest is the valid
index-zero digest, the invalid envelope is accepted.

```go
encoded, _ := EncodeEventEnvelopeV1(validIndexZeroEnvelope)
mutant := bytes.Replace(encoded,
    []byte(`"event_index":0`), []byte(`"event_index":256`), 1)
got, err := DecodeEventEnvelopeV1(mutant)
// Observed: err == nil and got.EventIndex == 0.
```

Required remediation: validate the wide wire integer before narrowing, use the
validated constructor, and add 128, 255, 256, and boundary-plus-one decoder
mutants.

### 4. Durable codecs do not enforce required fields or the frozen array shape

The decoders use `encoding/json` structs without presence tracking, and
`canonicalJSONObject` turns absent byte fields into `{}`. Confirmed results:

- an event missing the schema-required `payload` is accepted when its digest
  matches the defaulted empty object (`codec.go:115-151`);
- an operation record missing schema-required `effect_ids` is accepted
  (`operation.go:452-488`); and
- an operation with zero effects is encoded as `"effect_ids":null`, not the
  array required by `operation-record-v1.schema.json`
  (`operation.go:432-445`).

Required remediation: track required-field presence before construction,
encode empty collections as non-nil arrays, mirror the JSON schema's required
set and closed shapes in tests, and apply the same audit to every durable v1
codec in this WP.

### 5. Aggregate-head transition validation permits wrap and invalid heads

`ValidateHeadTransition` compares against `expected.LogicalVersion+1` without
guarding overflow and does not validate the proposed head through
`ConstructAggregateHeadV1` (`internal/controljournal/model.go:644-651`). It
accepts `MaxUint64 -> 0` and accepts a next head with no epoch, last event, or
state digest.

Required remediation: refuse increment at `MaxUint64`, validate token grammar
and the complete next-head value, and add deletion-sensitive wrap/zero-digest/
missing-coordinate mutants.

### 6. Hash decoding admits noncanonical uppercase SHA-256

`hex.DecodeString` accepts uppercase input (`internal/controljournal/codec.go:126-129`),
so an event whose `canonical_event_digest` is uppercased is accepted even
though `control-journal-v1.md` and the JSON schema freeze lowercase hexadecimal.
The same decode pattern is used for other durable hash fields.

Required remediation: enforce the exact 64-character lowercase grammar before
hex decoding across every durable codec, with uppercase/mixed-case mutants.

## Verification performed

Supplied tests and static gates all passed, demonstrating that the findings are
missing semantic controls rather than existing red tests:

- `go test ./internal/controljournal/... -count=20`
- `go test -race ./internal/controljournal/... -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/controljournal/...`
- `staticcheck ./internal/controljournal/...`
- bounded codec and reducer fuzz campaigns (2 seconds each)
- `git diff --check`

Temporary reviewer tests reproduced every finding above and were removed before
this feedback was committed. The known long-running `internal/scenario` package
was intentionally not run.

## Review checklist

- Dead code: N/A for this staged foundational WP; downstream WPs are the
  declared production consumers, while the reducer already consumes the root
  capability.
- Synthetic-fixture tests: PASS; supplied tests invoke production paths, but
  the missing negative controls above leave contract failures uncovered.
- Silent empty return: PASS; no new silent error swallowing was found.
- Requirement coverage: FAIL for T006/T007/T009/T010 and FR-015/FR-016 because
  invalid capability, codec, version/range, and compensation mutants succeed.
- Frozen surface: PASS; no frozen planning contract was modified by the WP.
- Locked decisions: FAIL; the accepted mutants contradict the frozen journal
  capability, canonical value, no-wrap, and attribution laws.
- Shared-file ownership: PASS; the small graph-classification change is
  isolated in `46bda2c` and its commit message records its rationale.
- Production fragility: PASS; no new fail-loud path was found to be unsafe on a
  transient race.

The foundation-tier classification and pure-import guard are appropriately
least-privileged, and the canonical ordering/grouping/reducer logic reviewed
cleanly in the bounded audit. Approval remains blocked by the concrete
fail-open cases above.
