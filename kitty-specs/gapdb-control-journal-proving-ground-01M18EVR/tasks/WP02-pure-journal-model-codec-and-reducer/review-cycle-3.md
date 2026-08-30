# WP02 review cycle 3

Verdict: **rejected**

Reviewer: `reviewer-renata-wp02-3` (`reviewer-renata`)

Implementation reviewed: cycle-three fix `7894377` together with the complete
WP02 implementation and the bounded foundation-tier classification.

## Prior-cycle replay

The cycle-one corpus remains closed: forged/zero validation seals,
unattributed compensation, narrowing event indexes, missing/null/wrong-shape
durable fields, aggregate-head wrap/incomplete heads, and noncanonical SHA-256
text all refuse on their production seams.

The two cycle-two blocker families are materially repaired:

1. zero, manually incomplete, wrong-schema, malformed-ID, kind/resolution,
   delivery, recovery, premature-readiness, malformed result-type,
   noncanonical-result, and over-budget-inner-result receivers are refused by
   `WithDelivery`, `WithRecovery`, and `ReconcileAmbiguousOperationV1` before
   and after transition; every refusal returns the zero typed outcome, while
   valid applied and reconciled-applied readiness branches remain usable; and
2. reducer registry and projection construction reject empty, whitespace,
   leading-punctuation, invalid-UTF-8, 129-byte, and schema-zero coordinates
   through the root grammar, accept the 128-byte boundary, and preserve the
   nil-reducer and duplicate-coordinate controls.

## Blocking findings

### 1. Operation outcome construction and transitions admit an outcome that cannot be encoded

`OperationOutcomeV1.validateInvariants` validates the inner canonical result
against the 256-KiB ordinary-value budget, but does not budget the complete
durable outcome. `EncodeOperationOutcomeV1` adds the wire envelope and then
rejects the complete value (`internal/controljournal/operation.go:116-155,
432-440`). The constructor and all three transition seams therefore have a
weaker invariant than the encoder.

An independent same-package probe constructed a canonical JSON object exactly
`MaxOrdinaryValueBytes` bytes long. `NewOperationOutcomeV1` succeeded, but
immediate `EncodeOperationOutcomeV1` returned
`budget_exceeded: operation_outcome`. A transition can likewise return a
successful-looking value that cannot become durable evidence.

This violates T007's requirement that over-budget values fail before
publication and breaks constructor/decoder/encoder/transition parity for the
typed result in FR-008.

Required remediation: make the single outcome invariant enforce the complete
canonical wire budget (without recursion through the public encoder), and use
that invariant at construction, encoding, decoding, and before/after every
transition. Add boundary tests for the largest complete encodable outcome and
the first byte over it; every rejected transition must return the zero outcome.

### 2. The required typed-result coordinate still permits an empty value

`validateInvariants` calls the root token grammar only when `ResultType` is
nonempty (`internal/controljournal/operation.go:141-145`). Consequently,
`NewOperationOutcomeV1(..., "", ...)` succeeds; all three transitions accept
that receiver; and the durable codec emits and accepts a required
`"result_type":""` field.

The operation record contract defines `result_type` as a nonempty token, and
FR-008 requires every canonical operation to bind a typed result. Requiring the
JSON key while admitting an empty coordinate is fail-open shape compliance.

Required remediation: validate `ResultType` unconditionally with the same root
token grammar used by operation records. Add constructor, encoder, decoder,
and all-three-transition mutants for empty, whitespace, leading punctuation,
invalid UTF-8, 129-byte, and a valid 128-byte result type.

## Bounded adjacent audit

- Outcome canonicalization and copy isolation are sound: constructor input,
  result accessors, and transitioned outcomes do not alias caller bytes.
- The implemented reconciliation/readiness matrix refuses non-ambiguous
  reconciliation and product readiness without applied evidence; valid direct
  and reconciled-applied branches survive codec round trips.
- `ValidateAggregateCoordinateV1` and `ValidateReducerCoordinateV1` match the
  root event/head/registry token grammar at empty, punctuation, UTF-8, schema,
  128-byte, and 129-byte boundaries.
- Independent mutation probes show `NewRegistryV1` copies the supplied entry
  coordinates/function value, and `NewProjectionV1` copies supplied maps and
  state bytes. Caller mutation does not alter later reduction or projection
  reads.
- Projection head-key validation applies to both state and head maps. Reducer
  capability checking still precedes clone/callback activity, and failures
  publish no partial projection.
- Foundation classification, deny-by-default imports, and reducer purity
  guards remain green. No SQLite, GapDB, filesystem, network, clock, or random
  dependency entered the pure package.

No additional blocker was found in the bounded adjacent surface.

## Verification performed

Passing supplied and independent checks after removing temporary probes:

- `go test ./internal/controljournal/... -count=1`
- `go test ./internal/controljournal/... -count=20`
- `go test -race ./internal/controljournal/... -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/controljournal/...`
- `staticcheck ./internal/controljournal/...`
- three-second bounded codec and reducer fuzz campaigns
- `gofmt` cleanliness and `git diff --check`

The known long-running scenario package was intentionally not run. Temporary
reviewer probes were removed; only the runtime-owned review lock and this
durable feedback remain before commit.

## Review checklist

1. Dead code: **N/A** for this staged foundation; downstream WPs are the
   declared production consumers and the reducer already consumes the frozen
   journal capability.
2. Synthetic-fixture tests: **PASS** for supplied tests, which exercise
   production seams; the two missing negative-control families above block
   complete coverage.
3. Silent empty return: **PASS**; no silent error swallowing was found.
4. FR coverage: **FAIL** for T006/T007/T010 and FR-008 because an untyped and an
   unserializable operation outcome can cross constructor/transition seams.
5. Frozen surface: **PASS**; no frozen contract, schema, or module file changed.
6. Locked decision: **FAIL**; the accepted mutants violate the typed-result and
   ordinary-value budget laws.
7. Shared-file ownership: **PASS**; the only out-of-map production change is
   the previously reviewed, rationale-bearing pure-tier classification in
   `46bda2c`.
8. Production fragility: **PASS**; no fail-loud path unsafe under a transient
   race was found.

Cycle three closes the prior authority and grammar gaps. Approval is blocked
only by the two concrete `OperationOutcomeV1` invariant mismatches above.
