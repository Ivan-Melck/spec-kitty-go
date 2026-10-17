# SDK error definition and clone events — PARTIAL leaf

Test-only source-event model: `retained_sdk_error_accounting_test.go`. No product
code, SDK, cap, provider, admission or protocol change. `fullCallCoverage=false`
always. This is NOT a full transport/JSON/error/observer memory certificate.

## Exact source boundary

Pinned SDK `github.com/spec-kitty/gapdb@v0.0.0-20260907114832-006e9d469d40`,
`gapdb/types.go`: ErrorDefinition/ErrorDefinitions and Error.Clone. Pinned ordinary
Go1.26.7/linux/amd64 allocator recipes from OBSERVER_ALLOCATION_SLOTS.md apply per
conditional event, not to one final lump. No actual heap escape multiplicity,
live/peak/RSS measurement or allocation-call certificate is claimed.

retained_transport.go calls ErrorDefinitions ONCE per reached remote classification
and Clone ONCE after successful validation/echo/context checks. At most one per
attempted fixed RPC; calls<=4294. Earlier failures stop those paths. The model uses
a branch-superset schedule charging definitions AND successful clone per call;
it does not claim every attempted RPC takes that path. SDK/stdlib exclusive work
stays owned even though delegated; it is not shared infrastructure by naming.

## Covered conditional events

-41-element ErrorDefinition table, each56 bytes (string Code, string Retry,
 slice SafeActions). Code/retry/action string PAYLOADS are immutable SDK constants,
 not newly copied per string header.
- Each definition's source SafeActions literal backing (2..4 string headers), as a
 CONDITIONAL compiler candidate; it may be static/stack rather than fresh heap.
- The separate SDK append-from-nil SafeActions clone for EVERY definition, including
 definitions past the eventual matched code. Whole table construction precedes the
 caller's range/break. Use actual append capacity, requested bytes and rounded slot.
- Returned Error.Clone object plus its SafeActions, MutationIndexes and
 SupportedVersions clone backings. SafeActions<=4 after exact definition validation;
 both integer arrays<=128, as a conservative branch superset even when the exact
 error schema permits only one. Source AssertionIndex is rejected BEFORE Clone,
 so that optional separate integer clone is not reachable here.

A frame-size/2 integer-list bound is NOT the applicable rule: the custom scanner
rejects array length129 BEFORE retainedTyped; nodes<=1024, fields<=64, depth<=16.
retainedTyped additionally rejects raw status/error objects>65536 bytes before
standard JSON materialization. The typed source and clone target are distinct:
this leaf does NOT cover the former, JSON array growth histories, string decoding,
reflection/tag maps, source parser nodes/maps/items/names or generic evidence
redecoding. Schema validation and scanner caps are semantic prerequisites, not
heap proofs from a string-search tripwire alone. Existing transport tests must
remain green; the source controls are drift leads, not whole-graph certification.

The two small clone lists do not imply all SDK error costs are bounded by these
terms: invalid-field objects, errors.Is/As/Unwrap compiler temporaries, original
materialization, envelope, maps, evidence, contexts, cleanup and ABI/runtime events
remain OPEN. No term is added to another partial ledger without replacement/
deduplication and independent full-composition review. Per-event requested/slot
values are NOT cumulative actual allocations or simultaneous resident memory.

## Verification surface

New controls check source/layout profile, zero/invalid/scaled schedules, checked
arithmetic, fresh SDK definition/action and Error.Clone backing ownership, and
critical source-order/array/typed-size guards. Tests use SDK data/scalar models,
NOT admitted-provider/native/expiry/performance evidence. Parent command/raw-log
and independent review receipts live in the current implementation record. Until
that review is complete this is AUTHOR WORK, not a newly accepted accounting leaf.
All remaining four-deliverable completion requirements remain OPEN.
