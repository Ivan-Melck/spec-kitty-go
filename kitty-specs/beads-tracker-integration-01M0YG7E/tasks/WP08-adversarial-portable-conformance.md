---
work_package_id: WP08
title: Adversarial Portable Conformance
dependencies:
- WP06
- WP07
requirement_refs:
- C-007
- C-012
- FR-018
- FR-022
- NFR-004
- NFR-005
- NFR-007
- NFR-009
- NFR-014
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Start the WP08 lane from the accepted combined results of WP06 and WP07, use only the Spec Kitty worktree allocated to this package, and merge the reviewed candidate into feat/beads-tracker-integration; do not edit or push directly to main.
subtasks:
- T038
- T039
- T040
- T041
- T042
history: []
agent_profile: debugger-debbie
authoritative_surface: internal/tracker/conformance/
create_intent:
- internal/tracker/conformance/suite.go
- internal/tracker/conformance/suite_test.go
- internal/tracker/conformance/catalog.go
- internal/tracker/conformance/graph.go
- internal/tracker/conformance/discussion.go
- internal/tracker/conformance/assignment.go
- internal/tracker/beads/testdata/dishonest_bd.go
- internal/tracker/beads/testdata/scenario.schema.json
- internal/scenario/beads_fault_test.go
- internal/scenario/beads_fault_helpers_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/conformance/**
- internal/tracker/beads/testdata/**
- internal/scenario/beads_fault_test.go
- internal/scenario/beads_fault_helpers_test.go
role: investigator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP08 - Adversarial Portable Conformance

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `debugger-debbie`
- **Role**: `investigator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Build a portable, adversarial conformance layer that proves the tracker contract against both the durable fake and the Beads adapter without requiring Beads, Dolt, network access, or provider-specific host configuration.

The suite must test declared capabilities honestly, drive the production command boundary through a deliberately dishonest child executable, and carry Spec Kitty's universal authority invariants into every provider-fault scenario that can interact with execution. It must especially prove that a human assignment survives one hundred recovery races and that stale processes never regain authority through tracker state.

Implementation command:

```bash
spec-kitty agent action implement WP08 --agent codex
```

## Context

WP01 defines provider-neutral capability levels and diagnostics. WP03 owns the bounded JSON command runner and decoder. WP04 owns canonical binding and concurrency preflight. WP05 and WP06 implement catalog, graph, and discussion behavior. WP07 adapts assignment projection and recovery to honest mutation guarantees.

Use those accepted APIs. Do not fork tracker domain types, duplicate the Beads adapter, or reach into its private implementation merely to make tests convenient. The conformance surface must be usable by the fake, dishonest command adapter, and later real-Beads qualification.

The three evidence layers have different jobs:

```text
durable fake            proves provider portability and contract semantics
dishonest child process proves subprocess, protocol, ambiguity, and wrong-state handling
real Beads              proves actual v1.2.2 behavior in WP09
```

A truthful in-memory fake cannot prove timeout-after-apply or stale/wrong responses. A real provider is too slow and nondeterministic for systematic rare-fault injection. This WP therefore owns the deterministic middle layer and the portable contract shared by all three.

The crown-jewel invariant remains local authority safety. Tracker failure may make visible coordination stale or create reconciliation debt. It may not mint a lease, transfer a workspace, approve review, move the integration target, or authorize a zombie process.

## Branch Strategy

Work only in the WP08 lane created by Spec Kitty. Confirm the lane contains accepted WP06 graph/discussion and WP07 assignment/reconciliation behavior before implementation. Adapt to their final exported test seams rather than editing their files.

The mission planning and merge target is `feat/beads-tracker-integration`. Do not work in the primary checkout, create a worktree manually, merge an active lane, push directly to `main`, or edit campaign manifests/workflows owned by a later package. Other agents share the repository; preserve unrelated changes.

## Conformance Laws

Every suite and scenario must preserve these rules:

1. Tests consume provider-neutral ports and capability reports, not provider-name switches.
2. Supported capabilities run their full assertions.
3. Unsupported capability requests prove typed refusal before mutation; they are not silently skipped.
4. A declared non-applicable topology is a visible terminal census outcome, never a pass.
5. Human-formatted stdout/stderr is never parsed to decide behavior.
6. Mutating timeout/cancellation/truncation after start is ambiguous until observation proves an outcome.
7. Stale or wrong provider state cannot be normalized into a successful replay.
8. Read-before-write and local locks are never described as provider CAS.
9. Human/provider changes win over automated compensation when attribution cannot be enforced at the write boundary.
10. Every execution-adjacent provider fault runs the universal authority audit and a required real stale-authority probe.
11. Tests are deterministic under repetition and race detection; sleeps are not synchronization.
12. Failure output contains safe IDs, case names, codes, and counts, not descriptions, criteria, comments, raw metadata, secrets, or arbitrary stderr prose.

### T038: Implement reusable capability-aware conformance suites

**Purpose**: Prove one provider-neutral behavioral contract across implementations without forcing weaker providers to impersonate stronger ones.

**Implementation requirements**:

1. Define a small `conformance` harness around accepted tracker capabilities. A factory should create an isolated provider fixture, return its declared capabilities, expose only public ports/observations, and provide deterministic cleanup.
2. Keep suites split by capability so a provider can supply only the interfaces it declares:

   - validation and diagnostic vocabulary;
   - catalog create/read/update, mapping, revision, and replay;
   - blocking graph direction and readiness projection;
   - discussion ordering, markers, replay, and ambiguity;
   - assignment observation/projection/compensation guarantees; and
   - unavailable, stale, wrong-state, and unsupported-safety behavior.

3. Define stable case IDs independent of Go test names. Use table-driven subtests so fake, dishonest adapter, and real provider can run the same cases.
4. For each operation, compare requested guarantee with declared capability before selecting assertions.
5. When supported, assert complete observable results, post-observation, replay behavior, and mutation count/effect.
6. When unsupported, invoke the public request and assert the exact typed unsupported result plus zero mutation evidence. Do not call `t.Skip` for a required refusal contract.
7. Keep provider setup outside timed or behavior assertions and allocate all databases, state, scripts, and executables under temporary directories.
8. Make the durable fake run without `bd`, Dolt, network, user config, Git hooks, or Beads environment variables.
9. Ensure provider-specific optional fields cannot become required by the shared suite.
10. Let later WP09 plug a real-provider factory into the same registry without copying cases.

**Planned files**:

- `internal/tracker/conformance/suite.go` — factory, capability selection, stable case registry, result/census types.
- `catalog.go` — portable catalog/mapping/revision/replay cases.
- `graph.go` — dependency direction and diagnostic readiness cases.
- `discussion.go` — comment ordering, marker, replay, and ambiguity cases.
- `assignment.go` — projection, human-change, guarded-refusal, and compensation cases.
- `suite_test.go` — fake-provider execution, registry mutants, portability and redaction assertions.

**Validation**: The fake executes every portable case it declares, every unsupported guarantee has a refusal assertion, and removing one registered case or capability branch fails the deterministic census.

### T039: Build a script-controlled dishonest subprocess executable

**Purpose**: Exercise actual spawn, environment, stdout/stderr, deadline, process termination, and post-crash observation boundaries deterministically.

**Implementation requirements**:

1. Implement a dedicated Go `package main` under `internal/tracker/beads/testdata/dishonest_bd.go`. Tests build it once into a temporary executable; production code must not import it.
2. Control the executable with an explicit versioned JSON scenario script, validated against the checked-in `scenario.schema.json` or equivalent strict decoder.
3. Give each ordered script step:

   - stable step/case ID;
   - expected argv vector and required safe environment keys;
   - expected workspace identity;
   - optional precondition over its temporary state file;
   - state transition timing (`before_response`, `after_response`, or none);
   - stdout bytes/JSON envelope behavior;
   - stderr warning/error-envelope behavior;
   - exit code;
   - deterministic barrier/delay/cancellation behavior; and
   - next observation state.

4. Persist only synthetic provider state and invocation history beneath the test's temporary root. Use atomic replacement/locking so separate invocations observe a coherent script position.
5. Fail closed on unexpected argv, environment, workspace, duplicate consumption, unknown action, or exhausted script. Emit a machine-readable test failure, not plausible provider success.
6. Support valid object/array envelopes, documented JSON error objects, warning-plus-success, empty/truncated/oversized/multiple stdout values, future schema, nonzero exit, and abrupt termination.
7. Support applying a mutation to synthetic state before blocking or losing the response, which is required for timeout-after-apply recovery.
8. Support returning a deliberately stale snapshot and a deliberately wrong current mapping/value/marker independent of persisted truth.
9. Do not use a shell, network, real Beads, user configuration, repository Git state, or wall-clock sleeps as ordering controls. Use explicit files/pipes/barriers and bounded contexts.
10. Ensure test cleanup terminates process groups and validates that every expected script step was consumed exactly once or explicitly marked unused by capability disposition.

**Planned files**:

- `internal/tracker/beads/testdata/dishonest_bd.go`
- `internal/tracker/beads/testdata/scenario.schema.json`
- scenario builders and executable compilation helpers in `beads_fault_helpers_test.go`

**Validation**: A self-test proves the executable can be truthful, stale, wrong, malformed, and apply-then-timeout while the same production adapter and command runner handle every response.

### T040: Cover slow, unavailable, malformed, stale, wrong, and apply-timeout behavior

**Purpose**: Attack the adapter where provider state, command outcome, and process lifetime disagree.

**Implementation requirements**:

1. Define a closed fault registry with stable IDs for at least:

   | Family | Required cases |
   |---|---|
   | slow | success below deadline; deadline before apply; ignored cancellation/process kill |
   | unavailable | spawn failure; documented nonzero JSON error; workspace/lock unavailable |
   | malformed | empty, truncated, oversized, multiple values, warning noise, future schema, wrong payload shape |
   | stale | old revision with tied timestamp; stale mapping; stale assignment marker |
   | wrong | foreign project/work mapping; unexpected assignee; changed operation marker; duplicate match |
   | apply-timeout | create, catalog update, dependency, comment, and assignment applied before response loss |

2. Assert exact portable diagnostic kind, recovery code, mutation-outcome classification, retry safety, and safe identifiers for every case.
3. Make parser/protocol cases cover object, array, error, warning, unknown-field, malformed, truncated, oversized, and future-schema branches through the production decoder.
4. For timeout-before-apply, prove no provider mutation. For timeout-after-apply, independently observe state before any retry.
5. For create/comment/dependency replay, prove an observed exact operation/effect is returned as applied/replayed without a second mutation.
6. For stale or wrong values, prove the adapter returns revision conflict, attribution loss, invalid mapping, or ambiguity as appropriate; never rewrite provider truth to match intent.
7. For assignment compensation where Beads lacks conditional mutation, assert `safe_compensation_unsupported` before mutation even if a pre-read appears attributable.
8. Assert warning prose does not affect a valid success decision and never appears in routine stored evidence.
9. Add mutant controls for treating timeout as not-applied, trusting stale response, branching on warning text, and blindly retrying apply-then-timeout.

**Files**: `beads_fault_test.go`, `beads_fault_helpers_test.go`, dishonest testdata, and reusable conformance files as needed.

**Validation**: Every named family is executed through a real child process, produces its intended diagnostic/effect, and kills the corresponding unsafe mutant.

### T041: Carry universal authority invariants and `RequireStaleProbe`

**Purpose**: Prove provider failure cannot corrupt the local concurrency model that Spec Kitty does better than the reference ticket systems.

**Implementation requirements**:

1. Reuse the existing scenario fixture and `assertUniversalInvariants`; do not implement a weaker Beads-only authority checker.
2. For every fault case that participates in start, assignment, recovery, review projection, or integration projection, build an independently derived `universalAssertionState` and run the audit at the named fault boundary and after recovery when recovery occurs.
3. Set `RequireStaleProbe: true` for every relevant state and provide an actual public stale-authority operation that must return `execution.KindStaleAuthority`, `RetryDecisionRequired`, and nonempty recovery actions.
4. The audit must continue proving:

   - at most one valid lease per pinned revision/work;
   - fence and generation refusal;
   - immutable lease-scoped workspace binding and no workspace transfer;
   - unchanged primary checkout before integration;
   - candidate reachability;
   - serialized target integration authority;
   - attribution-safe provider facts; and
   - actor-level reviewer independence where review evidence is present.

5. Add an explicit read-only reviewer-independence assertion if the existing universal helper does not cover it; do not mutate review state to inspect it.
6. Provider readiness, assignment, or status may disagree with pinned work, but the expected lease/workspace/review/integration facts must remain the local source of truth.
7. Define an explicit applicability predicate. Pure decoder tests need not allocate a Git repository, but no execution-adjacent case may opt out by omission.
8. Record in each case result whether universal audit and stale probe were required, started, and passed.
9. Add mutant controls that remove the universal callback, set `RequireStaleProbe` false, supply a nil probe, or make the stale operation succeed. Each must fail with the implicated invariant/case ID.

**Planned files**:

- `internal/scenario/beads_fault_test.go`
- `internal/scenario/beads_fault_helpers_test.go`

**Validation**: All applicable provider faults pass the existing universal audit with a real stale refusal, and each missing-wiring mutant fails deterministically.

### T042: Run the 100-repetition human-preservation/race campaign with an exact census

**Purpose**: Establish statistically repeated evidence that recovery never overwrites a direct human assignment.

**Implementation requirements**:

1. Run exactly one hundred independently named repetitions of the human-change interleaving:

   ```text
   Spec Kitty records intent and projects assignment
   -> human assigns a different value/marker
   -> recovery observes the changed provider state
   -> compensation is considered
   -> provider lacks the conditional primitive or attribution is lost
   -> recovery records conflict and leaves the human value untouched
   ```

2. Use deterministic barriers/hooks to place the human mutation between projection and compensation. Do not use `time.Sleep` or scheduler luck.
3. Vary stable work/operation/actor/process IDs per repetition while using a fixed, recorded seed for any ordering permutation.
4. After every repetition assert:

   - provider assignee remains the human value;
   - the automated operation marker is not restored over the human marker;
   - automatic compensation performed zero provider mutations;
   - the durable outcome is `attribution_lost` or `safe_compensation_unsupported` as dictated by observed capability;
   - expected and actual observations are retained in the conflict record;
   - local lease/workspace authority remains unchanged; and
   - applicable universal/stale probes pass.

5. Define an exact deterministic census for the entire WP08 registry: expected, started, terminal, passed, failed, and non-applicable IDs sorted lexically.
6. Reject duplicate IDs, missing terminal events, unexpected cases, silent skips, missing capability dispositions, missing universal audits, and missing stale probes.
7. Keep subtests under a bounded number of top-level test names so later campaign registration can enumerate them exactly.
8. Emit one redacted JSON census artifact under `t.TempDir` or the caller-provided artifact directory; never dirty the repository.
9. Run the human campaign and conformance suite under `go test -race`. No race finding may be waived.
10. Add a census mutant for one omitted repetition and a human-overwrite mutant; both must fail for the exact missing case/invariant.

**Validation**: Exactly 100/100 human-change repetitions terminate passing, zero automated compensations change human state, and the full stable registry has no missing or extra case.

## Test Strategy

### Portable conformance

```bash
go test ./internal/tracker/conformance -count=1
go test ./internal/tracker/conformance -count=10
```

The package must run with Beads/Dolt absent and with provider-specific environment variables cleared by the fixture.

### Dishonest subprocess faults

```bash
go test ./internal/scenario -run 'Beads.*(Fault|Protocol|Ambiguous|Wrong|Stale)' -count=1
go test ./internal/scenario -run 'Beads.*(Fault|Protocol|Ambiguous|Wrong|Stale)' -count=10
```

Assert script consumption, child termination, mutation counts, and exact diagnostics. A nonzero child exit by itself is not sufficient evidence.

### Human-change and race qualification

```bash
go test ./internal/scenario -run 'BeadsHumanAssignmentPreservation100' -count=1
go test -race ./internal/tracker/conformance ./internal/scenario -run 'Beads|Conformance' -count=1
```

The scenario must report 100 expected, 100 started, 100 terminal, and 100 passed human-preservation repetitions with zero human-state mutations by compensation.

### Hygiene

```bash
go vet ./internal/tracker/conformance ./internal/scenario
gofmt -w internal/tracker/conformance/suite.go internal/tracker/conformance/suite_test.go internal/tracker/conformance/catalog.go internal/tracker/conformance/graph.go internal/tracker/conformance/discussion.go internal/tracker/conformance/assignment.go internal/tracker/beads/testdata/dishonest_bd.go internal/scenario/beads_fault_test.go internal/scenario/beads_fault_helpers_test.go
git diff --check
git status --short
```

Do not run formatting over files owned by another WP. Inspect the final diff and keep implementation changes inside the declared ownership globs and two scenario files.

## Definition of Done

- Reusable provider-neutral suites cover every declared catalog, graph, discussion, assignment, and failure capability.
- Unsupported guarantees are invoked and refused before mutation rather than silently skipped.
- The durable fake passes portable conformance without Beads, Dolt, network, Git hooks, or provider environment.
- A dedicated script-controlled executable drives the production subprocess boundary.
- Slow, unavailable, malformed, stale, wrong-state, and apply-then-timeout families have stable IDs and exact outcome assertions.
- Protocol coverage includes object, array, error, warning, unknown, malformed, truncated, oversized, and future-schema responses.
- Apply-then-timeout recovery observes before retry and performs no duplicate effect.
- Every execution-adjacent case runs `assertUniversalInvariants` with `RequireStaleProbe: true` and an actual stale-authority refusal.
- Reviewer independence and serialized integration remain true wherever corresponding evidence exists.
- Exactly 100 human-change races preserve the human assignment and marker with zero automated overwrite.
- The exact deterministic census rejects omissions, extras, duplicates, silent skips, and missing authority probes.
- Repetition, race, vet, formatting, and diff checks pass with no waiver.
- No human output parsing, Beads internal import, direct Dolt SQL, raw body logging, or provider-specific requirement enters the portable contract.
- T038 through T042 are recorded done with:

  ```bash
  spec-kitty agent tasks mark-status T038 T039 T040 T041 T042 --status done --mission beads-tracker-integration-01M0YG7E --json
  ```

## Risks

- **Truthful-fake blindness**: A normal fake cannot reproduce disagreement between persisted provider truth and returned response. The dishonest executable must own that distinction.
- **Mocking below the boundary**: Replacing the command runner with a function mock misses process, timeout, environment, and stream behavior. Run a real child executable.
- **Script nondeterminism**: Loose step matching can let retries consume the wrong response. Require ordered exact argv/workspace/environment expectations.
- **False absence**: A truncated or unavailable observation cannot prove timeout-before-apply. Leave the operation ambiguous.
- **Human overwrite**: Read-compare-update remains racy against direct writers. Refuse unsafe compensation and preserve the human state.
- **Authority test dilution**: Provider-focused assertions can pass while lease/workspace invariants regress. Carry the existing universal audit and stale probe explicitly.
- **Nil stale probe theater**: Setting a boolean without invoking a real stale operation is not evidence. Mutant-test both wiring and behavior.
- **Census drift**: Dynamic cases, `t.Skip`, or renamed tests can hide missing evidence. Use a closed stable registry and terminal disposition for every ID.
- **Race flakiness**: Sleeps and shared global fixture state will fail under load. Use isolated paths, barriers, and deterministic state transitions.
- **Sensitive output**: Dishonest scripts contain arbitrary payloads. Redact bodies/raw stderr from diagnostics and evidence.
- **Scope collision**: WP09 owns real Beads qualification and later packages own campaign/workflow registration. Do not edit them here.

## Reviewer Guidance

Start with the provider factory and case registry. Verify capability branching results in either full supported assertions or an explicit pre-mutation refusal; reject `t.Skip` as proof of unsupported safety.

Run the dishonest executable directly through the production adapter and inspect its script log. Force a mutation before timeout, then verify recovery observes the exact effect and issues no second mutation. Repeat with a stale response and a human-changed value. Reject any branch based on stderr prose or process exit alone.

For every execution-adjacent case, inspect the recorded applicability, universal-audit result, and stale-probe result. Remove the probe or make it return success and confirm the mutant reds with `stale_token_refusal`. Check reviewer actor independence and target-ref serialization when those records exist.

Finally run the hundred human-change repetitions under `-race`. Confirm 100 expected/started/terminal/passed, zero compensation writes, and the human value after each run. Remove one registry entry and enable the overwrite mutant to prove the census and invariant are load-bearing.

Reject function-only process mocks, network/real-Beads requirements in portable tests, sleeps as coordination, capability-name provider switches, silent skips, raw payload diagnostics, blind retries, false CAS language, a reduced Beads-specific authority checker, missing stale probes, mutable global scripts, or edits outside WP08 ownership.
