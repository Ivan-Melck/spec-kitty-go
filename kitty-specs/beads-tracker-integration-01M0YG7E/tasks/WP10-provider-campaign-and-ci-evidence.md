---
work_package_id: WP10
title: Provider Campaign and CI Evidence
dependencies:
- WP08
- WP09
requirement_refs:
- FR-018
- FR-019
- FR-021
- FR-022
- FR-023
- NFR-008
- NFR-009
- NFR-010
- NFR-012
- NFR-013
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: explicit
subtasks:
- T049
- T050
- T051
- T052
- T053
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/testcampaign/
create_intent:
- internal/testcampaign/beads_evidence.go
- internal/testcampaign/beads_evidence_test.go
- .github/workflows/beads-qualification.yml
execution_mode: code_change
model: ''
owned_files:
- internal/testcampaign/manifest.go
- internal/testcampaign/census_test.go
- internal/testcampaign/beads_evidence.go
- internal/testcampaign/beads_evidence_test.go
- Makefile
- .github/workflows/beads-qualification.yml
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP10 - Provider Campaign and CI Evidence

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Turn the Phase 2 provider tests into permanent, reproducible qualification
campaigns with exact scenario censuses, bounded pull-request feedback, scheduled
real-provider evidence, and fail-closed supply-chain controls. A reviewer must be
able to determine exactly what ran, against which source and provider binary,
under which storage mode, for how long, and whether universal authority
invariants passed—without trusting a green process exit or reading human logs.

The bounded fake/dishonest-provider campaign must remain fast and portable. The
real Beads campaign must remain opt-in/scheduled, checksum-pinned, isolated, and
offline after installation. Missing prerequisites or incomplete evidence are
`non_admissible`, never a pass.

Implementation command:

```bash
spec-kitty agent action implement WP10 --agent codex
```

## Context

WP08 supplies the portable capability-aware conformance suite, dishonest child
process, exact provider-fault registry, 100-run human-preservation scenario, and
universal authority/stale-probe assertions. WP09 supplies the real Beads v1.2.2
installer, disposable workspace driver, real integration tests, concurrency and
backup scenarios, and raw qualification observations.

This WP composes those accepted surfaces. Inspect their final top-level test and
scenario names before editing the manifest; do not guess names from planning.
Do not copy their test logic into the campaign package, weaken their invariants,
or make real Beads a dependency of ordinary unit/PR development.

Existing campaign law is load-bearing:

- every registered top-level test starts exactly once and reaches one terminal
  event;
- unexpected, duplicate, missing, or malformed JSON events fail the census;
- campaign source commit/tree remain consistent across profiles;
- authority-affecting campaigns require the universal test and stale probe;
- expected test lists are sorted, duplicate-free, and defensively copied;
- mutant controls prove Make/workflow bindings and invariant wiring are real.

Preserve those rules while adding the provider profiles. A test skip is a
terminal event for generic Go census accounting, but a skipped required real
qualification scenario must become `non_admissible` in the provider evidence.

### Evidence truth model

Use the committed
`contracts/qualification-evidence.schema.json` as the normative external shape.
The following semantics are stricter than mere JSON shape:

1. `pass` requires the entire exact required census, zero failed scenarios,
   zero authority-invariant failures, verified source identity, verified
   provider version/digest/schema, and a qualified storage mode.
2. `fail` means a required scenario, assertion, mutant, threshold, safety law,
   or authority invariant actually failed.
3. `non_admissible` means the run cannot support an adoption claim: missing or
   unverified binary, unsupported host/topology, skipped required scenario,
   incomplete census, absent offline proof, dirty/drifting source, or malformed
   evidence.
4. Setup failure still emits a bounded failure/non-admissible artifact.
5. A nonzero process exit cannot be converted to pass by a well-formed summary.
6. Human logs are debugging aids only. Machine-readable events and typed
   qualification results determine the verdict.

## Branch Strategy

Work only in the WP10 lane allocated after both WP08 and WP09 are accepted. The
lane must contain their combined results. If names or command interfaces differ
from planning, adapt the campaign to the accepted implementations rather than
editing their owned files.

The planning and merge target is `feat/beads-tracker-integration`. Do not edit
the primary checkout, create worktrees manually, merge another lane, push to
`main`, or change files outside the six exact owned paths.

## T049: Register exact portable, adversarial, and real-provider profiles

**Purpose**: Make every provider test a counted, named part of a curated campaign
instead of an unverified package wildcard.

**Files**:

- `internal/testcampaign/manifest.go`
- `internal/testcampaign/census_test.go`

**Implementation requirements**:

1. Register three explicit profile families from the accepted WP08/WP09 code:
   - portable provider-neutral/fake conformance;
   - dishonest/adversarial provider faults plus human-change and universal
     invariant cases;
   - real Beads catalog/worktree/concurrency/export/backup qualification.
2. Use exact anchored top-level test patterns and lexically sorted
   `ExpectedTests`. Subtests remain owned evidence of their top-level parent and
   do not inflate the stable top-level census.
3. Add a bounded provider campaign containing portable and adversarial profiles.
   It must include the exact universal-invariant top-level test and require both
   `RequireUniversal` and `RequireStaleProbe`.
4. Add a real-provider campaign/profile selection that retains the same
   invariant obligations where execution participates. A real test omitted due
   to host/provider prerequisites must be visible to evidence aggregation.
5. Do not treat capability-declared unsupported behavior as a silent skip. Its
   conformance case must execute and prove typed refusal before mutation.
6. Extend manifest validation/tests so removing one provider profile, expected
   test, universal test, stale probe, or human-preservation test fails with the
   implicated name.
7. Add a test that discovers the accepted package's top-level tests and compares
   them with the curated selector where practical. Filter intentionally unrelated
   tests explicitly; never accept arbitrary new matches without review.
8. Retain existing architecture, acceptance, scope, fault, soak, quality, and
   full-campaign censuses unchanged except for deliberate provider composition.
9. Keep the exact census independent of test order and Go map iteration.

**Tests**:

- all old and new profiles validate and return defensive copies;
- each provider campaign contains its mandatory universal/stale evidence;
- missing/duplicate/unexpected tests and required skips fail admissibility;
- a one-case removal mutant names the missing profile/test;
- source identity cannot drift between campaign profiles.

## T050: Implement qualification evidence validation and redaction

**Purpose**: Convert campaign and WP09 results into one schema-1 adoption record
whose verdict is internally consistent and safe to publish.

**Files**:

- `internal/testcampaign/beads_evidence.go`
- `internal/testcampaign/beads_evidence_test.go`

**Implementation requirements**:

1. Define typed values matching `qualification-evidence.schema.json` exactly:
   schema/source identities, provider tuple, mode, host, timestamps, scenario
   census, latency sets, invariant count, issues, and verdict.
2. Validate schema version 1, 40-character lowercase Git commit/tree IDs,
   provider version `1.2.2`, 64-character lowercase SHA-256 digest, protocol
   schema 1, allowed storage mode, nonempty GOOS/GOARCH, and ordered UTC times.
3. Validate scenario arithmetic: expected > 0; started and terminal cover the
   required census; passed plus failed is coherent with terminal disposition;
   no duplicate/missing required IDs may be hidden behind aggregate counts.
4. Validate latency records: stable nonempty operation, positive sample count,
   finite/nonnegative values, and `p50 <= p95 <= max`. Reject NaN/Inf and
   duplicate operation names.
5. Enforce verdict semantics. `pass` requires all expected scenarios passed,
   zero failures/skips/non-applicable cases, zero invariant failures, clean
   source binding, and a verified provider/offline run. Failed assertions yield
   `fail`; missing evidence/prerequisites yield `non_admissible`.
6. Provide deterministic JSON encoding with stable collection order and
   restrictive file permissions. Write through atomic replacement so a crash
   cannot leave a plausible partial pass document.
7. Redact at construction, not after serialization. Issues may contain bounded
   stable kind/recovery code and a concise safe message; reject raw stdout,
   stderr, environment dumps, credentials, descriptions, acceptance text,
   comment bodies, arbitrary metadata, and multiline transcripts.
8. Include a defensive redaction scan with seeded secret-shaped values and
   human bodies. The encoded evidence must contain none of them.
9. Keep evidence validation independent of Beads Go packages and Dolt internals.

**Required mutants**:

- `pass` with one missing terminal scenario;
- `pass` with one invariant failure;
- accepted wrong provider digest/version/schema;
- `pass` after a required real test skip;
- issue text containing a seeded secret/comment body;
- mixed source commit/tree or dirty qualification source.

Every mutant must be rejected for its exact evidence-law violation.

## T051: Separate bounded PR targets from real-provider qualification

**Purpose**: Keep the normal feedback loop under ten minutes while making the
full production-provider campaign one documented command rather than folklore.

**Files**: `Makefile` and campaign binding tests in `census_test.go`.

**Implementation requirements**:

1. Add a bounded target that runs the exact portable/adversarial provider
   campaign with fake/dishonest executables only. It must require no Beads, Dolt,
   network, provider environment, or user configuration.
2. Wire the bounded target into the pull-request quality path without replacing
   preflight, contract, race, static, dependencies, architecture, scope, or
   universal campaign gates.
3. Add a distinct real qualification target that requires the verified WP09
   installer result and absolute `BEADS_BIN`, uses temporary provider state, and
   emits the schema-1 evidence document even on failure/non-admissibility.
4. Do not make `quality`, `contract`, or ordinary `go test ./...` download or
   initialize Beads. The real target must be explicit and may run much longer.
5. Add a local aggregate target for maintainers that runs bounded evidence then
   real qualification without conflating their separate durations/verdicts.
6. Pass artifact/build/temp directories explicitly. Never write generated
   binaries, provider data, backups, or evidence into tracked repository paths.
7. Extend Make binding mutants: replacing a provider campaign with one profile,
   removing the exact census, or pointing real qualification at a PATH binary
   must fail tests.

**Expected separation**:

```text
pull request: preflight + contracts + architecture + portable/adversarial + race
scheduled/manual: verified install + all real-provider scenarios + evidence
```

## T052: Add a SHA-pinned, checksum-verified qualification workflow

**Purpose**: Reproduce the real campaign on a controlled hosted runner without
trusting mutable actions, an arbitrary PATH binary, or network during tests.

**Files**: `.github/workflows/beads-qualification.yml`.

**Implementation requirements**:

1. Trigger the bounded job on pull requests/feature pushes as appropriate, and
   the real-provider job only on schedule and `workflow_dispatch`. Keep real
   qualification out of the required fast PR path.
2. Set minimal permissions (`contents: read`), concurrency groups, cancellation
   policy, explicit job timeouts, and isolated runner-temp directories.
3. Pin every action by full commit SHA with a reviewed version comment. Do not
   use floating major tags. Disable checkout credential persistence.
4. Install the pinned Go/tool requirements and Beads only in the explicit
   installation step. Invoke WP09's installer, validate its machine-readable
   result, version, source tuple, and allowlisted SHA-256 before first execution.
5. Pass the resulting absolute `BEADS_BIN`; never fall back to PATH or install
   `latest`. Recompute the digest immediately before qualification.
6. Enforce offline-after-install with an actual runner/container/network
   boundary where supported, plus `GOPROXY=off` and other fail-closed settings.
   Environment variables alone are not proof. If isolation cannot be enforced,
   emit `non_admissible` rather than pass.
7. Initialize provider state only under runner-temp using stealth,
   non-interactive, skip-agent, and skip-hook controls. Never modify the checked
   out repository or runner user configuration.
8. Upload bounded and real evidence on `always()`, including setup failures.
   Artifact names include run ID/attempt; retention is finite and no credentials
   or raw provider transcripts are included.

## T053: Prove hosted timing, artifact, verdict, and mutant controls

**Purpose**: Make CI itself testable and ensure red evidence survives long enough
to diagnose a failed or inadmissible qualification.

**Files**:

- `internal/testcampaign/census_test.go`
- `internal/testcampaign/beads_evidence_test.go`
- `Makefile`
- `.github/workflows/beads-qualification.yml`

**Implementation requirements**:

1. Record monotonic duration plus UTC start/finish for each profile/job. The
   bounded PR provider target must finish within the mission's ten-minute hosted
   budget; real qualification has its own explicit longer timeout.
2. Build a comparable hosted baseline from at least ten successful bounded runs
   before treating timing as stable. Until then report the sample count and
   `baseline_pending`; never invent a pass from one unusually fast run.
3. Always materialize an evidence file. Setup/prerequisite/isolation failures
   produce valid `non_admissible`; test or invariant failures produce `fail`;
   complete verified runs alone produce `pass`.
4. Upload machine-readable census reports, qualification evidence, safe timing
   summary, installer identity, and mutant results. Exclude raw stderr/stdout,
   provider database, backups, work bodies, environment, and credentials.
5. Add source-level workflow tests proving SHA-pinned actions, read-only
   permissions, explicit timeouts, verified absolute binary usage,
   offline-after-install boundary, `if: always()` upload, and finite retention.
6. Add mutants for unpinned action, missing digest comparison, PATH fallback,
   network-enabled qualification, omitted artifact on failure, accepted skip,
   missing universal/stale probe, relaxed census, and raised/removed PR budget.
7. Mutants must fail for the implicated control, not YAML syntax accidents or a
   missing unrelated command.
8. Run the bounded provider campaign locally twice from a clean tree and compare
   exact census/source identity. Evidence timestamps/durations may differ;
   semantic selections and provider-independent results must not.

**Exit evidence**:

```bash
go test ./internal/testcampaign -count=10
make beads-contract
go test -race ./internal/testcampaign ./internal/tracker/conformance
```

Run the real target only with the checksum-verified dependency:

```bash
BEADS_BIN=/absolute/verified/path/bd make beads-qualification
```

## Test Strategy

Use table-driven evidence validation and source-level Make/workflow contract
tests before running campaigns. Campaign tests must consume `go test -json` and
typed WP08/WP09 results, never human output. Store every test artifact beneath
`t.TempDir()` or an explicit caller-owned artifact directory.

During development:

```bash
go test ./internal/testcampaign -run 'Beads|Provider|Evidence|Campaign|Workflow'
make beads-contract
```

Before handoff:

```bash
go test ./internal/testcampaign -count=10
go test -race ./internal/testcampaign ./internal/tracker/conformance
make quality
git diff --check
```

The real hosted result is necessary for final mission acceptance, but a local
machine unable to enforce the qualified topology must emit `non_admissible`
rather than weaken the requirements or masquerade as a successful provider run.

## Definition of Done

- Portable, adversarial, and real provider profiles have exact sorted censuses.
- Provider campaigns retain universal invariants and real stale probes wherever
  execution participates.
- Required unsupported operations execute refusal assertions; required real
  skips are non-admissible.
- Evidence validates the exact version/digest/schema/mode/source/census/latency
  contract and uses deterministic atomic encoding.
- Evidence and artifacts contain no secrets, bodies, raw transcripts, provider
  database, or backup contents.
- The bounded provider campaign is wired into PR quality and remains independent
  of Beads, Dolt, network, and provider configuration.
- Real qualification is scheduled/manually runnable, uses an absolute
  checksum-verified v1.2.2 binary, and is offline after installation.
- Pass, fail, and non-admissible are distinct and internally consistent.
- Failure/setup evidence uploads on every workflow outcome through SHA-pinned
  actions with minimal permissions and finite retention.
- Hosted timing records the ten-minute budget and honest baseline sample state.
- Every campaign/workflow/evidence mutant fails for its intended rule.
- Repeated tests, race detection, quality, and diff checks pass without waiver.
- Only files listed in `owned_files` are changed.
- Record completion through the event-sourced status command:

```bash
spec-kitty agent tasks mark-status T049 T050 T051 T052 T053 --status done --mission beads-tracker-integration-01M0YG7E
```

## Risks

- **Census drift**: a new test can run without being counted. Use exact selectors
  and discovery mutants, not broad package success.
- **Green skip**: generic Go census accepts skip as terminal. Provider evidence
  must mark required real skips non-admissible.
- **Supply-chain drift**: tag/version output does not prove binary bytes. Verify
  the allowlisted digest before every qualification.
- **Network theater**: `GOPROXY=off` alone does not isolate the provider. Require
  an enforceable boundary or non-admissible verdict.
- **Artifact loss**: success-only upload erases the evidence needed after a red
  run. Use `always()` and write a fallback evidence document early.
- **Evidence leakage**: raw transcripts and provider backups may contain human
  work or secrets. Upload only allowlisted structured summaries.
- **Timing folklore**: one run is not a hosted baseline. Retain sample count and
  pending status until comparable evidence exists.
- **PR bloat**: accidentally running real Beads on pull requests destroys the
  bounded loop. Test event/job conditions and Make dependency direction.
- **Mutant decoration**: a mutant that fails to parse proves nothing. Require the
  expected control-specific diagnostic.

## Reviewer Guidance

Start by comparing manifest selectors to the accepted WP08/WP09 top-level tests.
Run a missing-test and missing-universal mutant and confirm both fail with exact
names. Then feed the evidence validator a green-looking document with one skip,
wrong digest, invariant failure, dirty source, and seeded secret; none may pass.

Inspect the workflow as supply-chain code. Every action must be SHA-pinned, the
binary path absolute, the checksum verified after installation and immediately
before use, and qualification genuinely network-isolated. Confirm the real job
cannot run on a required PR path and artifacts upload on setup, test, timeout,
failure, and non-admissible outcomes.

Finally, run `make quality` without Beads installed and verify the portable
provider campaign still executes. Run the real target with an invalid binary and
confirm it emits non-admissible evidence without initializing provider state.
A green workflow badge is not acceptance; only schema-valid evidence bound to
the source tree and verified provider tuple is.
