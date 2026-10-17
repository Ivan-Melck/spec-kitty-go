# Whole-owner retained membership — proposed product implementation sequence

Status: **architecture/next-action handoff, not implemented**. Parent synthesized
[finite-set contract](GLOBAL_CARDINALITY_CENSUS_ALTERNATIVE.md), independently
reviewed viable under its conditions. Planner `a6103f0a-909b-432` inspected current
source/import boundaries and proposed staged seams. This is not canonical M4
planning; setup-plan, tasks and mission state were not invoked/advanced.

All four observation seams below are still absent. The independent size-only
[scalar footprint prerequisite](SEALED_RECORD_FOOTPRINT_FOUNDATION.md) is now
implemented and scoped-reviewed; it does not implement complete preflight budgets
or any observation. [Closed attribution design](ATTRIBUTED_RETAINED_READS_DESIGN.md)
is independently reviewed viable under explicit prerequisites, not code approval. Provider bytes/tuple stay
unchanged. Any changed consumer requires fresh source-bound checks, not reuse of
adopted receipts. No full graph/oracle/census qualification is claimed.

## Parent assessment of planner proposal

The dependency warning is material: sidecar already imports the storage adapter.
Adapter cannot import sidecar to authenticate reads. Public SDK does not expose
actual response connections/attribution hooks. Separate Probe, cached provenance,
or a status-only authenticated connection cannot authenticate intervening reads.

**Resolve the opaque authenticated-read interface before implementation.** A
selector serializer returning raw keys/request bytes, a generic operation/argument
callback, raw SDK/transport port, or new caller-selected private identity factory
would violate this task's closed boundary. Planner's proposed opaque selector must
not acquire one of those escape routes in code. A possible alternative is a shared
provider-free retained-kernel attribution utility and a read-only adapter-owned
transport whose operations/serialization stay private; such extraction requires
its own ownership/import/lifecycle review, preserving actual peer/socket/image/
startticks checks and not leaking descriptors. This alternative is not selected
or reviewed implementation, nor permission for broad refactoring.

## Stage sequence

1. **Allocation-free sealed footprint measurement.** In
   `controljournal/v2/commit_plan.go`, expose only bounded scalar measurement of
   private sealed fields: record occurrences, canonical byte totals, checkpoint
   charges and conservative encoded-key charge, checked arithmetic. No keys,
   record slices or canonical copies. Consume actual opaque ACKs. Reject malformed/
   oversized input before Records()/CanonicalValue() clones or RPCs. This is size
   observation only, not a foundation address/codec expansion.
2. **Actual-response attribution.** Reuse the invariants in sidecar
   `lifecycle.go:727–785`: each status/get response's real peer, connected endpoint,
   pre/post retained kernel lifetime and strict envelope/db/key checks. Reconnects
   remain bound to the original lifetime; restart/substitution refuses. Operations
   fixed to status/get and targets minted from closed owner evidence, not caller
   keys. Keep serialization/ports private and avoid the import cycle above.
3. **Publication final-map readback.** Within storage adapter, reuse private
   `v2_committer.go:209–299` role-to-key codec. All sealed record roles included;
   assertions aren't records. Order by provider revision, reject publication /
   revision duplicates, aliases/contradictions, immutable recreation, unsupported
   deletes; exact predecessor revisions govern overwrites. Count unique encoded
   keys. Read every final exact key/bytes/revision/no-expiry independently. No raw
   scans or foundation recovery-address expansion.
4. **Fixed private metadata readback.** In production owner, root in fixed binding/
   current/head; derive epoch/op/event IDs only from authenticated canonical
   predecessor/current records. Bounded acyclic chain, owner/database binding,
   chronology/recovered-prefix ordering, same triple revisions and latest head/
   current agreement. Every selected row read; no predicted 3*n+3 counts. Endpoint
   membership is distinct from original historical private batch writer/CAS/prefix
   authority. Future real binding/admission ACK capture can support the latter;
   do not fabricate an old ACK from current rows or race an unacknowledged write.
5. **Host composition.** Proposed `productionruntime/retained_membership.go`:
   preflight -> projection capture -> authenticated S0 -> publication/private
   reads -> counted S1 -> closing S2 -> projection recheck. Same database/lifetime/R,
   ready and R<=durable<=current. Compare ONLY S1 count to actual unique matched
   rows. No barrier/mutation/refresh/retry, partial output, count/unknown content /
   provider diagnostic disclosure on failures. Immutable matched-only proof after
   total success. Uncollected projects/families refuse, not silently omitted.

Typed owner/reference continuity, private admission authority, projection digest,
coordinate-to-progress multiset and business equivalence remain independent gates.

## Resource contract still needs concrete approval/review

Planner proposed initial maxima: 256 ACKs, 4096 publication record occurrences,
64 private epochs, 32MiB expected payload, 128MiB conservatively charged memory,
30s total. These are **suggestions, not accepted or changed ceilings**. Check
against existing product/recovery limits and actual required traces. Never widen
1KiB keys/256KiB ordinary values/4MiB checkpoint or existing wire/recovery ceilings
merely for convenience. Bound clone/JSON/base64 amplification, IDs/key encoding,
RPC count, depth/tokens, elapsed duration and cancellation before allocations.
Any coverage cap must cause explicit refusal, not truncation presented as complete.

## Acceptance evidence per stage

Unit and attributable live controls, as applicable: torn Status revision/count;
expired extra with cleanup withheld; missing expected plus compensating extra;
role aliases and immutable/mutable mistakes; incomplete private triples, cycles,
foreign owner and unknown families; actual-response/endpoint/reconnect substitution;
kill/restart with same db/R; stale projection/current revision; exact and over
count/byte/work/time bounds; cancellation; defensive copies and nonleaking failure.
Source-only/manual-clock evidence must not be relabeled as admitted-image live
witness. No ordinary progress-query performance-path or public API parity expansion
is authorized by this proof handoff.

Before observation code, resolve immutable owned wiring, retained-lifetime /
revocation/context-aware hashing details and resource contract, then delegate
narrow implementations, independently run/check source hashes and scoped review
at each boundary. No real oracle integration until complete graph/reference/
physical/progress proof exists. Clean41c20b14… and historical red receipts remain.
