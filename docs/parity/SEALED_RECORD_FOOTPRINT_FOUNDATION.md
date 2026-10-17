# Sealed-record scalar footprint prerequisite

Status: **implemented, independently scoped-reviewed, size observation ONLY**.
This is not physically complete membership, seal/ACK authentication, peak memory,
aggregate budget enforcement, attribution or qualification. Existing wire/schema/
recovery addresses/provider images remain unchanged.

## Actual source change

Only two new implementation/test files:
- `internal/controljournal/v2/record_footprint.go`:
  `986f9d7706628cc0f58419cb85bf3f72849316dce1f75523ce525ac3960dbdbc`
- `internal/controljournal/v2/record_footprint_test.go`:
  `ccf116243491f8917e34a8791e49b594fbddfc353e46ccd9424eddd1dbf0fc05`

The sealed-plan getter reads private lengths and cheap markers; returns eight
uint64 scalars by value. ALL ten record roles contribute, assertion charges remain
separate from retained membership, checkpoint class is strictly role-specific.
It preserves existing member/ordinary/checkpoint/raw-product/canonical-plan caps,
charges existing conservative 1KiB/key (not actual encoded length) and key+20 per
assertion. Bounded counts precede arithmetic/loops; existing raw budget bounds sums.
No byte copies, hashing, JSON, key codec/selector, target/value/digest strings,
mutable aliases, callback/client/RPC or provider dependency is introduced.

Nil/cancelled/deadline/record-loop/assertion-loop/final-check failures return ZERO
statistics. Successful all-role and valid 4MiB checkpoint measurements allocate
ZERO heap objects. Structural exact/+1 and malformed probes are explicitly labelled
package-local size/shape tests, NOT manufactured valid seals/ACKs/native witnesses.

The meter doesn't inspect every semantically validating plan field or prove a
cryptographic seal. Future callers MUST apply conservative aggregate clone/JSON/
validation/codec/RPC/private-record/elapsed budgets before validating/copied accessors.
No 256ACK/32MiB/128MiB proposed aggregate ceiling has been approved by this getter.

## Independent checks

Implementer Ivan loaded profile/action context and inventoried 1,587 existing
files; independent reviewer verified all unchanged, exactly two permitted code
additions. Full core unit/race ran once each: 375 passing test results including
subtests, zero failures/skips; vet0. No failed correction/retry needed.

Parent read complete actual new code/tests and independently ran once each under
Go1.26.7, TMPDIR=/home/user/.cache/spktmp, timeout120s:

| Surface | Top-level pass | Including subtests | Package seconds |
|---|---:|---:|---:|
| controljournal/v2 unit | 84 | 375 | 4.001 |
| controljournal/v2 race | 84 | 375 | 40.155 |
| selected scenario unit | 36 | 276 | 4.662 |
| selected scenario race | 36 | 276 | 42.353 |

All zero failures/skips. Core+scenario vet passed. All inventoried core/scenario
source hashes stayed unchanged during parent checks. Scenario selection:
`TestTerminal(Coordinate|Execution|BoundExecution|Repository|History|Restart|TypedRecovery|Semantic)`.

Read-only independent reviewer `c78adf26-700b-44a` found no blocking attributable
defects after full code/handoff and actual budget/fixture inspection. It separately
ran `^TestRecordFootprint` on exact Go1.26.7, including both zero-allocation probes,
and verified source/evidence counts and preserved baseline. Its original result
said parent regression pending; parent completed it afterward as recorded above.
This does not retarget that review into qualification or complete budget evidence.

Evidence under `/home/user/.cache/spec-kitty-parity-resume-followup/`:
`record-footprint-foundation/implementation-n4ts5pcs/`,
`record-footprint-parent-F0DNrfLs/` (parent checks; path verified).

## Remaining actual gates

Five selected architecture guards also passed once, zero skips/failures (14
results including subtests). This checks current production graph policy and
adapter/provider/capability classification only, not the full architecture suite.
Evidence: `record-footprint-architecture-oIgS5HF0/`.

The [authenticated read design](ATTRIBUTED_RETAINED_READS_DESIGN.md) is independently
reviewed viable CONDITIONAL design, not implementation approval. No actual-response transport,
complete ACK final-map readback, fixed private chain, coherent S1 count observer,
complete graph/private admission/physical effects/progress multiset/business
comparison or oracle integration exists. These must not be inferred from this
meter's green suite. Main is dirty, candidate/images/receipts/Op/missions unchanged.
