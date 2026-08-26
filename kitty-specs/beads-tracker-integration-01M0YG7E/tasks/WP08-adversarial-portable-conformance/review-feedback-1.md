# WP08 Review Feedback — Changes Requested

Candidate: `df29266`

## Blocking findings

### 1. The portable conformance suite is not run against the dishonest adapter

`internal/tracker/conformance/DefaultCases` contains only eight catalog/graph/discussion/assignment cases, and `suite_test.go` invokes them only through `fakeFactory`. The dishonest subprocess tests never call `conformance.Run`, so FR-018 and T038 do not yet prove the same portable invariants against both the durable fake and the dishonest command boundary. Validation/diagnostic vocabulary and the unavailable/stale/wrong/unsupported-safety cases are also absent from the shared registry.

Remediation:

- Add a dishonest-provider factory/fixture that runs the same stable shared case IDs through the public tracker ports and production command runner.
- Put validation/diagnostic and portable provider-fault safety cases into the reusable registry, with supported assertions or explicit pre-mutation typed refusal for every capability disposition.
- Prove the fake and dishonest factories produce exact, complete censuses from that one registry; leave WP09 able to register the real provider without copying case logic.

### 2. The 25-case fault census labels raw child invocations as behavioral passes

`internal/scenario/beads_fault_test.go:37-98` executes the dishonest binary directly with the same `show` command for every ID, ignores `command.Run()`'s result, and marks each case passed after only checking script consumption. Consequently names such as `apply-timeout.comment`, `apply-timeout.dependency`, `wrong.duplicate-match`, and `stale.assignment-marker` do not execute or assert those behaviors. The separate production-adapter matrix covers only a small differently named subset, and the test named `TestBeadsDishonestAssignmentApplyTimeoutObservedBeforeRetry` actually exercises catalog creation. The required unsafe mutants for timeout-as-not-applied, stale-response trust, warning branching, and blind retry are absent.

Remediation:

- Make the closed fault registry hold executable case functions and derive the census from the cases actually run.
- Drive each case through `BoundAdapter`/the relevant public port, then assert exact diagnostic kind, recovery code, mutation classification/count, retry safety, and independent post-observation.
- Implement real create/update/dependency/comment/assignment apply-before-loss cases and the required slow, unavailable, stale, and wrong-state variants; do not treat a raw scripted `show` invocation as evidence for another operation.
- Add load-bearing mutants for the four unsafe behaviors named in T040.

### 3. The authority audit is disconnected from the provider faults and census

`TestBeadsFaultsCarryUniversalAuthorityAndStaleProbe` assigns five fault-like labels to one pre-existing stale-authority fixture, but it never injects the correspondingly named provider fault. It covers only five of the 25 census IDs (including `unavailable.provider`, which is not a registry ID), and no conformance/fault result records whether its audit or stale probe ran. This is a synthetic-fixture pass: deleting the dishonest-provider behavior would not make the authority test fail.

Remediation:

- Define the applicability predicate on the actual fault cases.
- At each execution-adjacent fault boundary and again after recovery, invoke `assertUniversalInvariants` with `RequireStaleProbe: true` and a real public stale-authority operation.
- Record required/started/passed for the universal audit and stale probe in the same case result used by the exact census.
- Keep the nil/disabled/successful-stale mutants, but make them mutate this real per-case wiring.

### 4. The 100-run campaign bypasses the WP07 recovery saga and never proves durable conflict evidence

`TestBeadsHumanAssignmentPreservation100` directly calls the fake provider's projection and compensation methods. It then constructs `humanConflictRecord` as a local Go value and reports a literal JSON result map. It does not exercise the accepted WP07 `execution.TrackerEffectSaga`/durable external-effect repository, reopen the control store, or read back an `attribution_lost`/`safe_compensation_unsupported` outcome with expected and actual observations. The `humanOverwriteMutant` is an isolated toy function rather than a mutation of the production compensation path.

Remediation:

- Drive all 100 interleavings through the WP07 projection/recovery service and durable repository using deterministic hooks/barriers.
- Reopen/read the durable effect after each recovery and assert its terminal outcome plus expected/actual revision, assignee, and marker fields.
- Retain the real provider-state and zero-compensation-mutation assertions and the real authority/stale probe per repetition.
- Replace the toy overwrite mutant with a mutant/control that would fail if the actual saga compensated after attribution changed.

### 5. The architecture-policy exception needs its own load-bearing guard

The candidate edits `internal/architecture/policy.go`, outside WP08's declared ownership, to classify `internal/tracker/conformance` as quality tooling. The direction is sensible and existing Beads kernel-to-adapter mutants still pass, but no test asserts this exact package is non-production quality tooling or that authority packages cannot import it. A future reclassification could silently turn the test harness into a kernel dependency surface.

Remediation:

- Add an explicit architecture test that classifies `internal/tracker/conformance` as `TierQuality` with `Production == false`.
- Add exact-edge mutants proving authority/kernel → conformance and authority/kernel → Beads adapter imports fail while conformance → provider-neutral contract/adapter test dependencies remain allowed.
- Record the cross-owned policy edit as coordinated architecture-guard work.

## Contract round-trip notes

- `contracts/beads-json-protocol.md` and `contracts/tracker-capabilities.md` are in scope.
- Existing protocol unit tests cover several envelope shapes, but WP08's dishonest-child census does not round-trip all named shapes through the production boundary.
- `scenario.schema.json` has no mirror/validation test against the executable's closed transition vocabulary, and the executable accepts any scripted argv action because `validateInvocation` checks equality only. Add fail-closed tests for unknown action, argv/environment/workspace mismatch, duplicate/exhausted scripts, and schema drift.
- `qualification-evidence.schema.json` is owned by WP09 and is orthogonal to WP08.

## Anti-pattern checklist

1. Dead code: **PASS with scope note** — the new package is deliberate quality tooling; its exported API has a live test caller and is intended for WP09 qualification, not production runtime.
2. Synthetic-fixture test: **FAIL** — the fault census and authority audit can pass without the behaviors their IDs claim.
3. Silent empty return: **PASS** — no unexplained silent empty-return production path found in the WP diff.
4. FR coverage: **FAIL** — FR-018 and FR-022 are not exercised across the shared dishonest-provider path.
5. Frozen surface: **PASS** — no frozen contract artifact was modified.
6. Locked decision: **PASS** — no Beads internal import, direct Dolt SQL, human-output authority parsing, or weakened kernel import direction found.
7. Shared-file ownership: **FAIL** — `internal/architecture/policy.go` is outside WP08 ownership and lacks an explicit coordination note/test for the new classification.
8. Production fragility: **PASS** — no new unreasoned production `raise`/panic-equivalent path found.

## Verification performed

- `go test ./internal/tracker/conformance -count=10` — passed
- `go test ./internal/architecture -count=1` — passed
- focused dishonest/fault/authority scenario tests — passed
- `go test ./internal/scenario -run '^TestBeadsHumanAssignmentPreservation100$' -count=1` — passed (100/100, about 110 seconds)
- `go test -race ./internal/tracker/conformance ./internal/scenario -run 'Beads|Conformance' -count=1` — passed
- `go vet ./internal/tracker/conformance ./internal/scenario` — passed
- `git diff --check df29266^..df29266` — passed

These green commands establish deterministic execution and race hygiene for the implemented tests; the blockers are that several tests do not yet exercise the acceptance behavior they claim.
