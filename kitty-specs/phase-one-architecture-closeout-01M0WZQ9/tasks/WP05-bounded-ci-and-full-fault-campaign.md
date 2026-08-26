---
work_package_id: WP05
title: Bounded CI and Full Fault Campaign
dependencies:
- WP01
- WP02
- WP04
requirement_refs:
- C-004
- C-009
- FR-006
- FR-013
- NFR-001
- NFR-003
- NFR-004
- NFR-005
- NFR-009
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Planning artifacts for this mission were generated on feat/phase-one-architecture-closeout. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/phase-one-architecture-closeout unless the human explicitly redirects the landing branch.
subtasks:
- T021
- T022
- T023
- T024
- T025
- T026
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/testcampaign/
create_intent:
- Makefile
- .github/workflows/quality.yml
- .github/workflows/full-campaign.yml
- internal/testcampaign/census.go
- internal/testcampaign/census_test.go
- internal/scenario/campaign_test.go
execution_mode: code_change
model: ''
owned_files:
- Makefile
- .github/workflows/quality.yml
- .github/workflows/full-campaign.yml
- internal/testcampaign/**
- internal/scenario/campaign_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 - Bounded CI and Full Fault Campaign

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Create one fast, reproducible pull-request quality gate and one distinct exhaustive fault/soak campaign. Both must preserve Phase 1's universal invariants and prove selected tests actually ran.

Implementation command:

```bash
spec-kitty agent action implement WP05 --agent codex
```

## Context

The full scenario package is too slow for the edit loop. Splitting scheduling is allowed; weakening assertions is not. Nano Kitty's gate ladder is useful context, but independently implement only what this repository needs. Workflows must use minimal permissions, pinned actions, isolated mutable paths, concurrency cancellation for PRs, and retained evidence.

## Branch Strategy

This WP begins after WP01, WP02, and WP04 are integrated into its lane base. The mission merge target is `feat/phase-one-architecture-closeout`.

### T021: Define the local gate ladder in Make

**Purpose**: Make local commands the single canonical behavior behind CI.

**Steps**:

1. Add `preflight`, `contract`, `acceptance`, `scope`, `static`, `dependencies`, `quality`, `fault`, `soak`, and `full-campaign`.
2. Keep targets small and compositional; do not duplicate command bodies across workflow YAML.
3. Detect required tools clearly and distinguish missing optional tools from failed checks.
4. Use bounded parallelism and isolated temp/cache paths where concurrency is safe.
5. Ensure `quality` omits only explicitly classified expensive scenarios.

**Files**: `Makefile`.

### T022: Verify curated Go test execution from JSON transcripts

**Purpose**: Prevent `go test -run` zero-match exits from becoming false green gates.

**Steps**:

1. Implement a small Go transcript verifier.
2. Parse `go test -json` events structurally.
3. Require exact expected start and terminal event sets for curated selectors.
4. Reject malformed input, duplicates, missing terminals, zero matches, and process/verdict disagreement.
5. Add accepting and rejecting transcript fixtures without parsing ordinary terminal output.

**Files**: `internal/testcampaign/census.go`, tests.

### T023: Add the bounded pull-request workflow

**Purpose**: Enforce the quality target on every PR with predictable resource use.

**Steps**:

1. Trigger on pull requests and relevant feature-branch pushes.
2. Use read-only repository permissions and SHA-pinned actions.
3. Cancel older runs for the same PR ref.
4. Set an explicit timeout above the eight-minute p95 goal but low enough to detect hangs.
5. Upload compact timing/census/mutant artifacts even on failure.

**Files**: `.github/workflows/quality.yml`.

### T024: Add scheduled/manual full fault and soak workflow

**Purpose**: Retain exhaustive concurrency evidence without blocking every edit.

**Steps**:

1. Trigger nightly and through manual dispatch.
2. Run `make full-campaign` from a clean checkout.
3. Prevent mutable cache/tmp/worktree sharing between parallel jobs.
4. Retain results for fourteen days.
5. Make skipped/missing credentials or unavailable runners explicit; local product tests must not require hosted services.

**Files**: `.github/workflows/full-campaign.yml`.

### T025: Preserve universal invariants across both campaigns

**Purpose**: Ensure short/full classification changes cadence only.

**Steps**:

1. Add a campaign classification test/manifest.
2. Require both profiles to invoke the existing universal invariant helper where applicable.
3. Keep `RequireStaleProbe` active.
4. Add a mutant/fixture proving removal from either profile fails.
5. Avoid build tags that make expensive tests invisible to ordinary compilation unless a manifest gate proves inclusion.

**Files**: `internal/scenario/campaign_test.go`.

### T026: Record timing, census, mutant, and retention evidence

**Purpose**: Qualify the new gate before making it socially authoritative.

**Steps**:

1. Declare the qualification reference host, run the quality target ten times there, and compute p95.
2. Run one complete campaign locally.
3. Verify architecture/authority mutants and test census.
4. Confirm action pins, permissions, timeout, concurrency, retention settings, and that every hosted run emits comparable timing evidence without claiming hosted p95 before ten samples exist.
5. Record T021–T026 completion events; leave durable aggregate evidence for WP06.

**Validation**:

```bash
make quality
make full-campaign
```

## Definition of Done

- Local and CI gate definitions have one canonical command source.
- Curated selectors cannot pass with zero or partial matches.
- Quality meets the measured feedback budget.
- Full campaign retains all universal authority invariants.
- Workflow permissions, pins, isolation, cancellation, timeout, and retention are explicit.

## Risks

- Go cache/module downloads can dominate first-run timing; report warm/cold context honestly.
- Splitting by test name can drift; keep manifests and census tests load-bearing.
- CI YAML can be syntactically valid but semantically untested; validate locally and with workflow lint if available.

## Reviewer Guidance

Reject unpinned actions, broad write permissions, plain `go test -run` without census verification, short-suite mocks, or a full campaign that omits the stale-process probe.
