---
work_package_id: WP03
title: Stack-Neutral Acceptance Proof System
dependencies:
- WP01
requirement_refs:
- C-009
- C-010
- C-017
- FR-015
- FR-016
- FR-017
- FR-018
- FR-019
- FR-023
- NFR-010
- NFR-012
- NFR-014
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts were generated on feat/production-runtime-architecture. Implementation uses the lane worktree assigned by lanes.json and merges accepted work back into feat/production-runtime-architecture.
subtasks:
- T013
- T014
- T015
- T016
- T017
- T018
history: []
agent_profile: architect-alphonso
authoritative_surface: docs/
create_intent:
- docs/STACK_NEUTRAL_ACCEPTANCE.md
execution_mode: planning_artifact
model: ''
owned_files:
- docs/STACK_NEUTRAL_ACCEPTANCE.md
role: architect
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 — Stack-Neutral Acceptance Proof System

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `architect-alphonso`
- **Role**: `architect`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Publish the acceptance architecture that replaces language-shaped gate execution with stack-neutral claims, explicitly approved provider bindings, provenance-rich evidence, and a policy decision. The kernel must be able to accept software, documentation, research, data, migration, and mixed-stack missions without knowing their tools.

Implementation command:

```bash
spec-kitty agent action implement WP03 --agent codex
```

## Context

WP01 supplies identity and authority laws. Use `contracts/acceptance-evidence.schema.json`, the acceptance entities in `data-model.md`, and Decisions 7–8 in `research.md`. The repository's current Make/Go qualification remains valuable as this project's provider configuration and historical evidence; it is not universal product semantics.

The design must keep three concepts separate:

```text
claim       = what must be true
provider    = an approved way to produce evidence
gate        = policy evaluation of the complete admissible evidence set
```

## Branch Strategy

Use the WP03 lane from `lanes.json`, based on accepted WP01, and merge only into `feat/production-runtime-architecture`. Own only `docs/STACK_NEUTRAL_ACCEPTANCE.md`. Do not edit Makefiles, workflows, test runners, or runtime code; later implementation missions own provider code and architecture mutants.

## Acceptance laws

1. Required policy is the strict union of organization, project, mission-type, and mission claims.
2. Lower layers cannot silently remove higher-layer requirements.
3. Detection proposes; explicit policy approves a binding.
4. Evidence is bound to exact claim, candidate, WorkRevision, provider, runner, policy, governance, time, and inputs.
5. Evidence is not an acceptance decision.
6. Missing required provider yields `decision_required`, never implicit skip or pass.
7. Human-formatted output is not parsed into authority.
8. A waiver is attributed, scoped, policy-authorized, candidate-bound, and visible.
9. Unbounded logs and artifacts live outside the envelope and are referenced by digest.
10. Provider/tool/language packages cannot be imported by the acceptance kernel.

### Subtask T013: Define claims and policy composition

Define `AcceptanceClaim` with stable identity, capability, proposition, source layer, scope, requiredness, freshness, allowed evidence kinds, dependencies, and typed conditional predicates. Explain how claims from all policy layers form a strict union, how duplicate identities/conflicts are handled, and how changes create a new acceptance-plan revision.

Give representative claims for software correctness, documentation link integrity, human design approval, external migration verification, data quality, and security attestation. None may encode a command such as `go test`, `pytest`, or `npm test` as the claim itself.

### Subtask T014: Define provider resolution and binding

Specify qualification provider capabilities and `QualificationBinding`: provider identity/version/digest, binding revision/source, supported evidence kinds, bounded execution policy, trust domain, evidence decoder, availability, and lifecycle status.

Describe suggestion-only stack detection. A detected `go.mod` may recommend a provider recipe but cannot activate it or change required claims. Define explicit approval, replacement, retirement, unavailable-provider decisions, and project/org precedence without creating hidden defaults.

Provider kinds should include bounded local argv execution, CI attestation, signed human decision, and a future typed remote evaluator. Shell strings and parsing presentation output are forbidden as authority.

### Subtask T015: Define evidence, freshness, admissibility, and waivers

Explain every field in the evidence schema and why it exists. Define outcome semantics for `pass`, `fail`, `error`, `skipped`, and `waived`; only policy determines whether an outcome can satisfy a required claim.

Define exact-candidate freshness by default. Reuse requires a validated input digest covering every stable dependency: candidate/tree, WorkRevision, claim, binding, provider, runner trust, policy, governance, environment contract, and referenced artifacts as applicable. Any relevant change invalidates only affected evidence.

Specify waiver signer, authority, reason, scope, candidate, policy basis, visibility, expiry/revocation, and audit. Waiver must not be a string flag or “ignore failure” shortcut.

### Subtask T016: Define qualification DAG and progress

Describe a dependency-aware graph of claims/providers. Independent nodes run concurrently; dependency failure blocks descendants with explicit reasons. Define scheduling, idempotent operation identity, retries, timeouts, cancellation, targeted invalidation, evidence reuse, and progress projection.

Distinguish provider execution errors from negative evidence and policy refusal. A failed provider run must not disappear behind a skipped descendant. The acceptance decision records the complete claim-plan revision and evidence set used.

### Subtask T017: Specify architecture isolation and mutant controls

Document the intended capability/package tiers:

```text
acceptance kernel -> claim, plan, policy, evidence, decision
provider ports    -> typed execution/attestation contracts
provider adapters -> Go/Node/Python/CI/human/local tools
application       -> schedules qualification and records evidence
```

State permanent dependency checks: the kernel cannot import provider adapters, ecosystem tooling, shell execution, transport, tracker, or persistence implementations. Mutants that add names/imports for pytest, Go test commands, npm, Maven, Cargo, CI vendor APIs, or provider result parsers must fail. A deliberately stupid fake provider must prove the contract independently of any ecosystem.

### Subtask T018: Publish cross-stack examples and validate

Provide at least two materially different evidence examples conforming to the same claim: for example a local process provider and signed CI attestation, or a documentation validator and human review. Include one valid waiver example and negative examples for stale candidate, missing provider, wrong binding revision, untrusted runner, and human-formatted output without typed evidence.

Validate positive JSON against `contracts/acceptance-evidence.schema.json`; ensure waived evidence requires the waiver object and non-waived evidence rejects it. Link examples to their policy decisions rather than implying schema validity alone means acceptance.

## Expected document structure

1. Scope, status, and separation of claim/provider/evidence/decision.
2. Acceptance-plan composition and policy layers.
3. Claim schema and dependency semantics.
4. Provider capability and explicit binding lifecycle.
5. Suggestion-only detection.
6. Evidence envelope and artifact boundary.
7. Freshness, admissibility, reuse, and invalidation.
8. Qualification DAG scheduling and progress.
9. Outcome and waiver evaluation.
10. Kernel/provider/application dependency tiers.
11. Cross-stack examples and negative cases.
12. Future implementation qualification.

Add one end-to-end decision walkthrough: resolve layered claims, encounter one missing provider, obtain explicit binding, run independent providers concurrently, invalidate one result after candidate drift, reuse unaffected evidence, apply or refuse a waiver, and record the final decision revision.

## Explicit non-goals

- Selecting built-in ecosystem providers for production.
- Treating repository Make targets as universal claims.
- Running arbitrary shell snippets supplied by tracker text.
- Storing raw logs, test streams, or secrets in evidence envelopes.
- Allowing a provider to decide acceptance policy.
- Making CI or a human actor inherently trusted without configuration.
- Implementing provider code or architecture tests in this docs mission.

## Review evidence

Leave a claim-to-provider matrix, policy-union example, freshness input list, waiver checklist, and schema validation output. The reviewer should be able to substitute a fictional unfamiliar toolchain without changing kernel vocabulary.

## Definition of Done

- The owned document is a complete implementable proof-system architecture.
- It contains no universal ecosystem command or language assumption.
- Claims, providers, evidence, and decisions remain distinct.
- Policy union, missing providers, freshness, reuse, invalidation, and waivers are explicit.
- Cross-stack examples demonstrate one contract across different proof mechanisms.
- Kernel isolation and load-bearing mutant expectations are documented.
- No production code or repository-specific gate configuration changed.

## Reviewer guidance

Try to apply the model to a non-code research mission and a mixed Go/Node project. Reject if a claim embeds a tool, if detection activates policy, if exit code alone creates a verdict, if evidence can cross candidates without complete stable-input proof, or if a waiver lacks accountable authority and scope.
