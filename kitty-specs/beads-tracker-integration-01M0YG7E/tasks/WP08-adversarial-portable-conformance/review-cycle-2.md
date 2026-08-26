---
affected_files: []
cycle_number: 2
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T17:29:39Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 Review Feedback — Cycle 2 Changes Requested

Candidate: `ba6a6d4`

## Blocking findings

### 1. The shared dishonest-provider conformance still does not test Beads operation semantics

`TestBeadsDishonestRunsSharedPortableConformance` now calls the shared registry, but `dishonestPortableProvider` is a fake-backed test implementation that performs a generic scripted `show` probe and then implements each typed operation itself. For example, its `CreateWork`, `UpdateWork`, graph, discussion, and assignment methods do not call the corresponding methods on `fixture.adapter`; they either construct a result locally or delegate to `fake.Provider` after `probe()`.

Consequently, deleting or breaking `BoundAdapter.CreateWork`, `UpdateWork`, `PutBlockingDependency`, `AppendDiscussion`, or `ProjectAssignment` would not fail the shared conformance cases. The child process proves only that a `show` response can be decoded before unrelated fake/manual semantics run. This is still a synthetic-fixture pass, not the same provider-neutral contract executed against the script-backed Beads adapter.

The new `Fixture.Fault` callback has the same weakness: the shared registry delegates the meaning and assertions for `unavailable`, `stale`, and `wrong` back to each factory. The fake and dishonest providers can execute different operations and diagnostics under one case ID while `Run` records both as the same portable pass.

Remediation:

- Construct the dishonest factory around the real `*beads.BoundAdapter` public ports and generate each case's exact child script from its stable case ID.
- Let the shared `Case.Exercise` functions issue and assert the typed catalog/graph/discussion/assignment request themselves. Provider factories should supply isolated state/scripts, not redefine the behavioral assertion.
- Make the diagnostic/fault cases return observable typed evidence to the shared assertion/census instead of swallowing provider-specific errors inside `Fixture.Fault`.
- Add a deletion/mutant control showing that bypassing any corresponding `BoundAdapter` method makes the dishonest shared census fail.

### 2. Universal authority evidence remains disconnected from the provider fault it labels

`TestBeadsFaultRegistryExactCensus` runs each provider fault in a temporary dishonest-provider fixture, then separately calls `quickStaleFaultAuthority` against one pre-existing `workspaceScenario`. The authority fixture does not participate in the tracker operation, its recovery, or its control-store state. The audit runs only afterward, not at the named fault boundary and again after recovery. Setting the six result booleans therefore records co-located tests, not a causal authority audit around the fault.

The human campaign has the same topology: the real saga and per-repetition SQLite store are separate from the shared authority fixture audited afterward. The stale probe itself is real and correct, but it does not prove that the tracker fault/saga left that run's lease, fence, workspace, review, and integration facts unchanged.

Remediation:

- Route each execution-adjacent tracker fault through a scenario that owns the relevant authority/control-store facts, and derive the universal assertion state from that same scenario.
- Audit before the injected boundary and after observation/recovery where recovery occurs.
- Populate `Result` authority fields from the actual per-case callback outcomes rather than literals.
- Move the nil/disabled/success mutant controls onto this per-case wiring so removing the audit or stale callback from one applicable fault fails that fault's census entry.

### 3. The exact fault census is load-sensitive and its evidence fields are partly synthetic

The combined focused command failed on an ordinary review run:

```text
go test ./internal/scenario -run 'TestBeadsDishonestRunsSharedPortableConformance|TestBeadsFaultRegistryExactCensus|TestBeadsDishonest(ScriptFailsClosed|ProtocolFaultMatrix|TruthfulWarningStaleAndWrongState|SlowCancellationKillsChild|.*ApplyTimeout)' -count=1 -timeout=8m
```

`apply-timeout.assignment` and `apply-timeout.catalog-update` expired during the 100 ms setup/preflight budget and returned `tracker timeout` instead of `tracker ambiguous`; the census then had only 23/25 terminal cases. The census passed when immediately rerun in isolation, confirming load-dependent timing. WP08 explicitly requires deterministic barriers rather than scheduler or wall-clock luck.

In addition, `faultEvidenceSummary` manufactures operation, diagnostic, recovery, mutation, and retry fields from the case ID after the test helper returns. Those fields are not the typed result actually produced by the operation, so they can drift while the census remains green.

Remediation:

- Synchronize on an explicit child/process barrier proving preflight is complete and the intended mutation step has started before applying a bounded cancellation. Do not let setup cost consume the semantic timeout boundary.
- Have each executable fault case return its actual typed diagnostic/result, observed mutation count, and post-observation classification; construct the census from that evidence rather than a parallel ID-to-string table.
- Add a repetition/load test that reliably kills the current short-deadline mutant.

### 4. Required unsafe mutants and strict-script controls remain incomplete

The cycle-1 request and T040 require load-bearing mutants for timeout-as-not-applied, trusting a stale response, branching on warning prose, and blindly retrying apply-before-loss. The candidate contains assertions with mutant-shaped messages, but no implementation mutants for the first three and no blind-retry implementation control; `assertSingleMutationInvocation` only checks the already-correct path. The former human-overwrite toy was removed, but no replacement mutates the real saga/provider compensation path to show the 100-run test kills an automated overwrite after attribution changes.

The script test adds unknown top-level action and argv/exhaustion checks, but does not exercise the requested environment mismatch, workspace mismatch, duplicate-step, or scenario-schema drift controls. `knownAction` and the JSON schema also accept unrecognized subcommands/argv forms (for example arbitrary `comments <value>` or extra arguments to simple verbs), so a scripted but invalid action can still look plausible.

Remediation:

- Add explicit unsafe controls that execute the four T040 failure modes through the same result assertions used by the real cases.
- Add a saga/provider mutant that attempts compensation after human attribution changes and prove the durable/provider assertions fail it.
- Add executable self-tests for environment and workspace mismatch, duplicate IDs, unsupported/unknown scenario fields or version, and unknown subactions/argv shapes; keep the schema and runtime vocabulary in one exact mirrored set.

## What is now correct

- All five apply-before-response-loss cases invoke the corresponding real Beads public port and independently observe provider state with one mutation invocation.
- The 100-run campaign uses `execution.NewTrackerSaga`, closes/reopens SQLite, and verifies coherent durable `attribution_lost` evidence plus zero compensation mutations.
- `internal/tracker/conformance` is explicitly pinned as non-production `TierQuality`; authority-to-conformance direction mutants and existing Beads direction guards pass.
- The stale operation used by the authority helper is a real public handoff refusal.
- Focused race, vet, architecture, and diff checks are clean; the blocker is correctness/evidentiary coupling, not a data race.

## Anti-pattern checklist

1. Dead code: **PASS** — the quality-tooling package and added helpers have live test callers.
2. Synthetic-fixture test: **FAIL** — the shared dishonest suite delegates semantics to fake/manual methods, and authority auditing uses an unrelated fixture.
3. Silent empty return: **PASS** — no unexplained silent empty-return path found.
4. FR coverage: **FAIL** — FR-018/FR-022 are not yet proven through the same Beads operation paths and authority state claimed by the census.
5. Frozen surface: **PASS** — no frozen artifact changed in this correction.
6. Locked decision: **PASS** — no provider-name authority switch, human-output parsing, false CAS, or kernel-to-adapter import was introduced.
7. Shared-file ownership: **PASS** — the out-of-map architecture test is explicitly coordinated in the commit message and pins the exact intended direction.
8. Production fragility: **N/A** — this correction changes test/quality tooling only.

## Verification performed

- `go test ./internal/tracker/conformance -count=10` — passed.
- Combined shared/fault/apply-timeout scenario command — **failed once** with the two load-sensitive timeout classifications above.
- `go test ./internal/scenario -run '^TestBeadsFaultRegistryExactCensus$' -count=1 -timeout=5m` — passed in isolation, confirming flakiness.
- `go test ./internal/scenario -run '^TestBeadsHumanAssignmentPreservation100$' -count=1 -timeout=5m` — passed, 100/100, about 113 seconds.
- Focused `-race` over fake/dishonest shared conformance plus the 100-run human campaign — passed, about 150 seconds.
- `go vet ./internal/tracker/conformance ./internal/scenario` — passed.
- `go test ./internal/architecture -count=1` — passed.
- `git diff ba6a6d4^ ba6a6d4 --check` — passed.
