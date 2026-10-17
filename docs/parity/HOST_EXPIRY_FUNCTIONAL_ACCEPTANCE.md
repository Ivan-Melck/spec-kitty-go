# Host expiry — functional acceptance

## Controlling direction

The user clarified: **“the idea is to go for Functional adherence instead of code parity.”**

Required behavior is the acceptance target. Historical implementation structure,
private helper names, line numbers, AST shape, file layout, and whole-source equality
are not substitutes for functional evidence or standalone behavioral failure criteria.
Do not continue the C8 converter/parent qualification scaffolding merely to achieve
source parity. Its completed artifacts and failures remain historical evidence.
Parent-v4's 48 authored Python models remain unrun; its two r4 reviews stopped as
`INCOMPLETE_STOPPED_FOR_FUNCTIONAL_PIVOT`, without approval.

Use the original V3 DESIGN/TEST-MATRIX as the functional requirements inventory,
not as a requirement to reproduce its historical implementation. Preserve externally
observable compatibility, security boundaries, authentic input/image validation,
original operation/reader/held budgets, and truthful failure/cleanup reporting.
Hashes identify what actually ran. Hash validation that authenticates a required
input or executable remains functional security behavior; matching an old Host
source file does not prove adherence. Public wire/schema compatibility is distinct
from incidental private code structure.

## Original nineteen-family checklist

Every row needs requirement-level success and refusal evidence, not just one test.
This table is an execution checklist, **not a passed coverage claim**.

| Family | Observable acceptance target |
|---|---|
| Exact closed input | Authentic authorized input is accepted; malformed, changed, noncanonical, unsafe or unauthenticated inputs refuse before execution. |
| Ordinary exclusion | Ordinary roots retain their behavior and cannot be switched into qualification through caller admission, environment or configuration. |
| Held source/exec | Execution uses the verified held image; path replacement or descriptor/image substitution cannot change what executes. |
| Fixed child descriptors | The child receives the intended gate and sealed executable with attributable process identity; swapped, missing or unsealed descriptors refuse. |
| Gate | Actual H → R → A succeeds in order; malformed, missing, duplicated, concurrent or canceled exchanges never earn a witness. |
| Cleanup | Acquired resources have an owner; original errors remain visible; actual child exit/reap and resource teardown are observed; uncertain cleanup is not success. |
| Fresh root | Only an exclusively created, correctly owned empty root/lease can initialize; existing or substituted resources refuse without adoption. |
| Binding | Qualification identity and ordinary wire vectors remain correct; partial, mixed, mismatching or overwritten bindings refuse. |
| Recovery dispatch | Required recovery reads work without optional accelerators; ordinary recovery modes and error behavior remain compatible. |
| Real ACK/final-map | Genuine coordinator ACKs and continuation/overwrite rows are verified against the original database; fabricated, stale or foreign ACKs refuse. |
| Private chains | Actual predecessor-linked records are visited and validated within existing byte, row, epoch, RPC and time limits; malformed or incomplete chains refuse. |
| Public mismatch | A genuinely held hidden-expired extra produces zero public proof and the required sanitized refusal. |
| Private S1 closure | A new real observation completes S1/S2, terminal checks and consumption; incomplete or independently invalid observations never count as completed negatives. |
| Context/revocation | Independent cancellation, revocation or teardown at the specified cutoffs refuses; successful inner-session cancellation is not mistaken for outer cancellation. |
| Frozen composition | Proof remains attached to the original live Host, owner, database, lease generation and projection; substitution or stale authority refuses. |
| Actual expiry | Real TTL insertion, hold, authenticated not-found, public refusal, private closure, release ACK, durable revision advance and refreshed matched proof all occur. |
| Same-run restart | Clean and unclean successor flows preserve database/history while changing process and epoch; predecessor handles and failed restart authority refuse. |
| Unsupported transfer | Unsupported attach/restart/transfer attempts refuse before side effects; EOF release is not positive qualification. |
| Evidence/reporting | Results identify actual inputs, commands, binaries, outcomes and cleanup; simulations, instrumented donors and source checks are not relabeled genuine Host evidence. |

## Execution priority

1. Establish a fresh ordinary and qualification-tag owning-package baseline against
   the current implementation. Run default tests/fuzz seeds, without source overlays,
   selected-test substitutes, fabricated opt-ins or dependency acquisition. Preserve
   raw failures and list all skips. This is diagnostic evidence, not Native acceptance.
2. Classify failures by cause: real behavioral defect, environment/fixture problem,
   or obsolete source-parity assertion. Do not change expectations to fit failures,
   suppress failures, or treat a parity assertion as a demonstrated behavior defect.
3. Exercise the existing genuine Host scenario matrix: initial/clean/unclean ×
   private-only/actual-ACK, plus reconciler ACK coverage. Each must reach the entire
   TTL closure and actual final cleanup using authentic inputs and fresh owned roots.
   Review fixture safety and substantive input/ownership prerequisites first; the
   parked converter/parent pipeline is not itself a Host functional requirement.
4. Fill required refusal/cutoff/compatibility gaps with behavioral tests. A selected
   scenario pass does not replace the full owning suites or all nineteen families.
5. Audit the complete requirement-to-result mapping and independently review actual
   results before whole-area acceptance. Performance/accounting/negative-matrix
   areas remain separate unless their own obligations are fulfilled.

## Source-sensitive tests

Some existing tests deliberately locate a precise observation frontier by source
hash, AST and line number. A stale hash failure establishes that the old locator
cannot certify its target; it does not establish wrong Host behavior. Preserve
those historical controls. Replace or separate such acceptance gates only with
an explicit functional oracle that reaches the intended real event and verifies
both success and refusal. Never simply delete the check, broaden a match, renew a
budget, invent completion, or count a skipped control as passed.

## Current evidence limit

Fresh default five-owning-package ordinary/E runs after actual read-cause and
fixture/oracle ownership repairs both exit1 (four/eight top-level failing groups;
75/96 printed skips). Repaired behavioral groups pass, not whole suites. Original
socket positive+15mutants remain component evidence. A safety review blocked
initial Native execution on unjoined Host monitor; actual monitor publication/join
and refusal without teardown of undrained/unjoined work are now implemented and
independently source-reviewed. Full owning-runtime ordinary0/E1 (13/33 skips); all
three actual-worker component controls pass, two tagged source-frontier groups fail.

Evidence: followup-cache `host-functional-read-cause-20261017-v{1,2}/`,
`host-functional-oracle-corrections-20261017-v1/`, and
`host-functional-monitor-join-20261017-v1/`. Actual log hashes and full pre/post
inputs verified. No fresh genuine-Host/Native execution or family/whole-area closure
is claimed. New work/storage is OWNED_UNQUALIFIED; existing cleanup10s is not
original5s qualification and owner-cleanup failure semantics remain unresolved.
Frozen files/checkpoints remain untouched. All nineteen families/four areas OPEN;
`code_complete=false`. Source/hash/layout equality is not functional acceptance.
