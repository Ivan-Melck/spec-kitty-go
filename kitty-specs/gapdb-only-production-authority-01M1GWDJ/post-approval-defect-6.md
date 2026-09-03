# Post-approval defect 6: durable canonical byte compatibility

## Finding

The first real WP04 origin-to-suffix restore fixture proved that several
foundation encoders could not survive their own durable recovery boundary.
`EncodeEventEnvelopeV2`, `EncodeAggregateHeadV2`, and
`EncodeOperationBindingV2` emitted declaration-order JSON. The strict decoders
required byte-for-byte equality with that representation, while
`NewRecoveryRecordV2` recursively reordered object keys through
`CanonicalObject` and silently returned the rewritten bytes. The same defect
affects `OperationSummaryV2` when embedded structurally inside a recursively
canonical checkpoint.

```text
foundation Encode
  -> durable provider value
  -> RecoveryRecordV2 canonical admission
  -> foundation Decode
  -> JOURNAL_CORRUPT
```

WP04 must not repair this by parsing or reconstructing foundation wire types.

## Ratified correction

WP02 owns one recursively canonical final-byte rule for every exported v2
value classified as a direct or nested durable record.

- Preserve existing digest preimages. In particular, the unsigned event bytes
  used by `CanonicalEventSHA256` do not change.
- Normalize only the final persistence-facing bytes of operation bindings,
  event envelopes, aggregate heads, and operation summaries.
- Operation states and publication witnesses already satisfy this rule.
- `NewRecoveryRecordV2` rejects noncanonical provider evidence by exact byte
  comparison. It never silently rewrites evidence.
- Strict decoders retain exact canonical re-encode equality; Authority Journal
  v2 adds no dual-read compatibility path before production adoption.

The mandatory consumer-path law is:

```text
Encode(value) bytes
  == recursively canonical bytes
  == NewRecoveryRecordV2(...).CanonicalValue()
  -> Decode succeeds with exact semantics
```

It applies directly to operation bindings, event envelopes, aggregate heads,
operation states, and publication witnesses. Operation summaries prove the
equivalent nested-checkpoint canonicalize/extract/decode path.

## Routing

1. WP01 adds a permanent execution-bound durable-codec census and mutants for
   raw struct-order output, top-level-only normalization, discarded
   canonicalization, length-only recovery admission, removed decoder equality,
   and omitted codec classification. All prior mutant IDs remain sealed.
2. WP02 implements final-byte normalization and reject-without-rewrite
   recovery admission, plus rich direct and nested consumer-path regressions.
3. WP03 requires no production change.
4. WP04 remains blocked with its seven-file draft preserved until WP01 and
   WP02 are independently reapproved. WP04 then normalizes its own checkpoint
   and slot final bytes before they enter `RecoveryRecordV2`.

## Acceptance evidence

- The original real restore fixture passes without a WP04 wire workaround.
- Every direct durable codec round-trips through `NewRecoveryRecordV2` with
  byte identity and semantic equality.
- Operation summary survives recursive checkpoint embedding.
- Noncanonical provider bytes are refused rather than normalized in place.
- Mutating an affected encoder back to raw struct order kills the intended
  permanent guard.
- WP03 remains byte- and source-unchanged.
