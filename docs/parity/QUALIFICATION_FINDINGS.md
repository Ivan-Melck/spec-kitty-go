# Qualification findings — current parity candidate

These are typed development findings, not amended release receipts or admission
records. Failures are retained; corrections to source-bound qualification
contracts require separately attributable scope and a fresh candidate identity.

## PARITY-QUALIFICATION-001 — namespace label observation portability

Current disposition: **RESOLVED for the local namespace/tooling prerequisite**.
The original result below was misattributed: `/proc/self/attr/current` raises
EINVAL on this non-label-LSM host; real namespaces work. The shared bounded
[portability correction](USERNS_PORTABILITY.md), strict selected-profile controls,
real namespace identities/maps/topology and installed `strace` now pass.
Hosted-runner attestation and full candidate qualification remain separate.

- Historical kind: `host_prerequisite_refused`.
- Witness: `internal/architecture/TestCIUsernsWrapperPreservesOrdinaryExec`.
- Actual result: `namespace dispatch refused: [Errno 22] Invalid argument` with
  Go 1.26.7, both isolated and in the complete suite.
- Boundary: no environment marker, skip, privilege escalation or namespace
  assertion removal can stand in for isolated execution.
- Next path: source-bound qualification using the verified local capability and
  tooling, preserving hosted-runner/selected-profile attestation where required.
  A different machine is not an established requirement.

## PARITY-QUALIFICATION-002 — reused client versus single-exchange V1 proxy

Current disposition: **development source-contract correction verified**.
[Sequential unary relay compatibility](PROXY_UNARY_COMPATIBILITY.md) preserves
production reuse, exact daemon/response attribution and the global one-shot cut.
Original real GJ-02 witness and unconditional serial/blocked-shutdown controls
failed red-first, now pass. Response-drop race controls, complete sidecar/application
packages and independent review pass. Historical receipts remain unchanged;
new source-bound qualification is still required. Earlier observations follow.

- Historical kind: `qualification_source_contract_incompatible`.
- Witness: `internal/controljournal/application/TestReconcileRealGJ02ResponseDrop`;
  `/tmp/spec-kitty-parity-fixture-topology-green.log`.
- Actual result: `indeterminate`, committed revision zero, recovery
  `reconcile_same_operation`, before the expected lost-response assertion.
- Exact daemon: original M1 SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- Source evidence: `internal/controlstore/gapdb/adapter.go` enables
  `ReuseUnaryConnection: true` (WP17 change `f4111155`); current public client
  `reusableUnary` retains a connection after typed not-found. The V1 qualification
  proxy `internal/sidecar/gapdb/proxy.go:handle` forwards only one exchange and
  closes the product connection.
- `Service.Apply` first reads the fresh operation (not-found), then preflights its
  aggregate head. The latter reuses a closed connection; unavailable read maps
  to the observed indeterminate result. No automatic replay occurs.
- The selected `atomic_batch` response-drop point and later reconciliation are
  not reached. This is not evidence that durable reconciliation lost an event.
- The original daemon/admission pins `ff3dd478...`; current client source pins
  `006e9d469d40...`. Matching daemon bytes does not recreate old client behavior.
- Independent review confirmed the source-level incompatibility; the exact
  syscall/EOF variant was not separately captured. No underlying transport
  diagnostic is available in the bounded result.
- Next input/path: fresh source-bound qualification for the now tracked/reviewed
  compatibility correction, using the exact applicable tuple. Do not disable
  production reuse, weaken the result assertion, change the daemon tuple, or
  rewrite original M1/M2 evidence just to make it green.

## PARITY-QUALIFICATION-003 — M2 qualification artifact reproduction mismatch

Current disposition: **RESOLVED for artifact availability/identity** by recovery
from the original adoption-bound release, not by changing the expected digest.
[Recovery record](M2_ARTIFACT_RECOVERY.md) binds the verified release, producer,
manifest and exact binary. The observations below retain the initial failed
reproduction; an exact build command remains unproven, but the required artifact
is now available. No current-branch qualification transfer.

- Kind: `immutable_artifact_unresolved`.
- Required [candidate](../programs/evidence/m2-gapdb-qualification-candidate.json):
  source `006e9d469d40fc5a6ce306a083ec96a98cc02e36`, tree
  `fe4bf39150a5511db76ba6c69de2a2c176abbd82`, binary SHA-256
  `54d3c80dd225a5e58f1488f0ce62fd6b95a6bb0ecf95b541e211b199555d8eaf`,
  size 5,426,793.
- Plain `CGO_ENABLED=0 GOTOOLCHAIN=go1.26.7 go install -trimpath` of the exact
  module yielded `555ea12d9b04d84a860f581591983281fae773daf25294f000a219c09aaa8bf2`,
  size 5,431,089. It was not supplied to qualification or production admission.
- Next input: recorded exact build/artifact provenance and matching bytes;
  source/version equality does not justify a binary override.

## PARITY-QUALIFICATION-004 — other mandatory proof boundaries

The complete-suite log `/tmp/spec-kitty-parity-goal-full-suite.log` also retains
source-bound M1 fault runner dirty-HEAD-tree/socket constraints and eight skipped
required nested scenario proof invocations. Six fresh fixture helpers were
individually corrected to honor TMPDIR; the frozen fault-runner occurrences were
not bulk replaced. Exact candidate/source/binary bindings remain necessary.
Subsequent publication restart-cut tests passed; targeted scenario tests now
reach exact executable metadata refusal rather than tmpfs refusal. The focused
M1 suite remains failed despite passing external inspection. These bounded
results neither erase the complete-suite failure nor establish a release pass.

## PARITY-QUALIFICATION-005 — newly reached native progress encoding refusal

Current disposition: **size-bound defect corrected and verified** by
[consumer-owned query snapshot encoding](PROGRESS_SNAPSHOT_BOUNDARY.md), preserving
old digest preimages, all operations and unchanged durable limits. Unit/race,
complete runtime/helper/CLI packages, architecture guards and two real scenario
campaigns pass. Restart/crown tests now reach a separate terminal-history
comparison failure (finding 006); no complete suite or qualification pass inferred.

- Kind: `native_query_refused`; not evidence that namespaces are unavailable.
- Witnesses: `TestTwoItemMissionRestartAfterEachDurableBoundary`,
  `TestM2FamilyBoundPublicationCutCampaign`,
  `TestM2DirectOutboxPerformanceMutationUsesDurableEffectSource`, and
  `TestTwoItemRealGitGapDBCrown` in the completed post-portability suite.
- Actual result: `PROGRESS_INCOMPLETE: encoding` at review/acceptance/integration
  boundaries. `productionruntime/progress.go` refuses failed JSON encoding or
  canonical-object digest construction; the full log does not expose inner error.
- A separate temporary diagnostic overlay with the matching recovered M2
  native-host authority binding reached revision 56 / `integration-work-a`:
  marshaled progress **273,496 bytes**; inner error
  `BUDGET_EXCEEDED: canonical_object`. The query reused the journal payload
  bound of 262,144 bytes, exceeded by 11,352. It included 36 items (147,928
  serialized bytes) and 19 operations (88,774 bytes). This confirms the budget
  mechanism for that diagnostic execution, not every full-suite failure.
  Log: `/tmp/spk-progress-diagnostic-q1v1ctel/test-corrected-binding.log`.
  The earlier extension-only diagnostic refused executable metadata and was
  retained; no binary mode, tuple or frozen limit was overridden.
- Log: `/tmp/spec-kitty-userns-postfix-full-suite.log`, terminal exit 1, 47 passing
  packages, five failing, two without tests; 22 top-level failures. This was
  dirty/evolving-tree development verification, not source-bound release proof.
- Next path: define and independently verify a consumer-owned bounded complete
  progress/digest contract distinct from durable event payload limits. Preserve
  existing snapshot digest compatibility, refusal and exact authority budgets.
  Do not raise durable payload limits, reduce census or omit bound operations.

The same run retains M2 campaign-source byte mismatch and M1 source/socket
constraints. A nested assertion-candidate check refused sidecar type/size despite
M2 artifact availability: distinct admission tuples are not interchangeable.
Non-verbose output does not establish an exact skip count. No receipt or frozen
source hash was retargeted to declare these failures successful.

## PARITY-QUALIFICATION-006 — restart histories versus terminal-state equivalence

- Kind: `qualification_equivalence_contract_mismatch`, investigated, not waived.
- Witness: real two-work restart and real-Git crown reach terminal/restored state,
  then reject `canonical terminal item facts differ` after fixing finding 005.
- One temporary-overlay two-work diagnostic established 46 uninterrupted items
  versus 56 restarted items. All 46 shared items compare identically. Ten extra
  durable `operation` entries are explicitly generated execution rebinds, two per
  each boundary: `before-effect`, `before-workspace`, `effect-intent`,
  `effect-observation`, `execution-admitted`. Every added entry is resolved,
  `execution_slot.result`, active, and retains work/actor/run/lease/generation.
- Mission summary, work counts, family coverage, tracker observations, repository
  publications and separately collected command-result evidence match. No
  provider-revision/shared-state divergence was observed. The real-Git crown was
  not separately diagnosed; its failure is retained, not presumed identical.
- Source intentionally rebinds prior-epoch execution authority after restart.
  Complete progress correctly exposes those operations; deleting them would
  regress membership. This diagnostic is consistent with additional valid
  recovery history, not demonstrated durable state loss.
- Evidence: `/home/user/.cache/spktmp/terminal-diagnostic-vidtsjuz/test.log` and
  `differences.json`; temporary overlays are diagnostic, not qualified proof.
- Next path: review [the proposed trace-aware equivalence contract](RESTART_EQUIVALENCE_PROPOSAL.md)
  that separately proves every acknowledged recovery receipt, exact operation
  census and unchanged business terminal facts. It is not adopted; the existing
  failed oracle was not weakened or changed. Do not globally ignore operations, whitelist by name
  alone, skip restart tests, hide history in production or retarget frozen M2
  qualification-source hashes. A changed oracle requires fresh source-bound proof.

See [completion audit](COMPLETION_AUDIT.md) and
[admission readiness](ADMISSION_READINESS.md) for the broader stop/go boundary.
