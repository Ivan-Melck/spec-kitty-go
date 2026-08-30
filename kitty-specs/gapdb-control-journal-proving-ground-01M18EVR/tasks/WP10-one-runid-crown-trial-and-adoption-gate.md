---
work_package_id: WP10
title: One RunID Crown Trial and Adoption Gate
dependencies:
- WP09
requirement_refs:
- C-001
- C-015
- C-016
- C-017
- C-018
- FR-001
- FR-006
- FR-029
- FR-039
- FR-040
- FR-041
- FR-042
- FR-043
- FR-044
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-009
- NFR-010
- NFR-011
- NFR-012
- NFR-013
- NFR-014
- NFR-015
- NFR-016
- NFR-017
planning_base_branch: main
merge_target_branch: main
branch_strategy: Planning artifacts for this mission were generated on main. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into main unless the human explicitly redirects the landing branch.
subtasks:
- T052
- T053
- T054
- T055
- T056
- T057
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/testcampaign/m1/campaign.go
create_intent:
- internal/testcampaign/m1/campaign.go
- internal/testcampaign/m1/campaign_test.go
- internal/testcampaign/m1/evidence.go
- internal/testcampaign/m1/evidence_test.go
- internal/testcampaign/m1/adoption.go
- internal/testcampaign/m1/adoption_test.go
- internal/testcampaign/m1/testdata/evidence/pass.json
- internal/testcampaign/m1/testdata/evidence/incomplete.json
- cmd/spk-m1-proving-ground/main.go
- cmd/spk-m1-proving-ground/main_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/testcampaign/m1/campaign.go
- internal/testcampaign/m1/campaign_test.go
- internal/testcampaign/m1/evidence.go
- internal/testcampaign/m1/evidence_test.go
- internal/testcampaign/m1/adoption.go
- internal/testcampaign/m1/adoption_test.go
- internal/testcampaign/m1/testdata/evidence/pass.json
- internal/testcampaign/m1/testdata/evidence/incomplete.json
- cmd/spk-m1-proving-ground/main.go
- cmd/spk-m1-proving-ground/main_test.go
role: implementer
tags:
- m1
- crown-trial
- evidence
- adoption
tracker_refs: []
---

# Work Package Prompt: WP10 - One-RunID Crown Trial and Adoption Gate

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Execute the decisive M1 experiment as one source-bound ordered RunID, publish a
machine-verifiable evidence manifest with the exact census and exit signature,
and enforce a separate signed-human adoption boundary. The combined trial—not
focused tests in isolation—must simultaneously observe all crown invariants.
Any missing/failing component stops the program before M2.

## Decisive trial law

One evidence record must execute the entire ordered torture chamber and observe:

- the exact fourteen external scenarios and five GapDB cuts;
- exactly two implementation ActorIDs with A/A/W1, B/B/W2, A/C/W3;
- one valid authority per WorkRevision;
- W1 quarantine, fresh W3, preserved W2, and zero zombie authority successes;
- owner host/process restart, a new epoch, and predecessor-capsule refusal;
- equal restore/playback digest with zero playback calls and writes;
- reviewer ActorIDs independent of every candidate contributor;
- serialized integration of both approved candidates;
- eight focused summaries and nineteen killed mutants;
- source/provenance/artifact binding and exact terminal signature.

## Branch and ownership

Begin only after accepted WP09. Edit the ten owned implementation/fixture files.
Runtime evidence output belongs in an explicit operator-selected directory and
is not silently committed. Do not edit schemas to fit generated output. Do not
sign for the human or initiate M2.

## T052: Execute the entire ordered one-RunID crown trial

**Files**: Create `campaign.go` and `campaign_test.go`.

**Implementation requirements**:

1. Generate one stable RunID and source binding at start; pass it to every child.
2. Verify sidecar admission and execute all fourteen external scenarios.
3. Start Actor A/Process A/W1 and Actor B/Process B/W2 concurrently.
4. Inject GJ-01 and GJ-02 around real product command publications.
5. Cut Actor A heartbeats, expire its lease, quarantine W1, then create a new
   lease and W3 for Actor A/Process C while zombie A remains alive.
6. Inject GJ-03/GJ-04 at exact workspace boundaries.
7. Restart the owner host/process under the same RunID, admit a new epoch,
   restore product state, refuse old capsule, and compare full playback digest.
8. Inject GJ-05, reconcile provider ambiguity, create both candidates, obtain
   ActorID-independent reviews, and serialize their integration.
9. Run/attach all focused campaigns and mutant results under the same RunID.
10. Stop immediately and honestly on a terminal invariant violation while still
    emitting admissible failure/incomplete evidence.

**Tests**:

- A successful fixture proves the required ordering and simultaneous facts.
- Removing/reordering any crown segment prevents pass.
- Child RunID mismatch prevents pass.
- Focused summaries alone cannot synthesize ordered-trial success.

**Exit evidence**: `go test ./internal/testcampaign/m1 -run CrownTrial`.

## T053: Emit and validate the exact evidence census and signature

**Files**: Create `evidence.go`, `evidence_test.go`, and fixtures.

**Implementation requirements**:

1. Emit schema-versioned canonical JSON conforming to all approved M1 schemas.
2. Require exactly fourteen terminal scenario records in the approved order,
   five terminal fault records, eight focused summaries with >=100 equal counts,
   nineteen killed mutants, one ordered trial, universal invariant summary,
   independent review, sidecar/source provenance, and artifacts.
3. Recompute semantic pass independently from claimed `outcome`.
4. Bind repository commit/tree, dirty-state policy, module version, sidecar SHA,
   admission digest, contract/budget revisions, binary digest, and artifact hashes.
5. Produce the exact ratified exit signature only when every predicate passes.
6. Preserve honest fail/incomplete manifests without a pass signature.
7. Use atomic evidence-file publication and bounded artifact references.

**Tests**:

- Positive canonical fixture passes schema and semantics.
- Every census omission, false invariant, source mismatch, surviving mutant, or
  incomplete signature fails.
- Reordered/duplicate IDs and altered artifact bytes fail.

**Exit evidence**: `go test ./internal/testcampaign/m1 -run Evidence`.

## T054: Build the proving-ground operator CLI

**Files**: Create `cmd/spk-m1-proving-ground/main.go` and `main_test.go`.

**Required commands**:

1. `preflight`: validate source cleanliness/binding, contracts, budgets, sidecar
   provenance, platform support, exclusive runtime paths, and output directory.
2. `run`: execute the one-RunID campaign with explicit config and bounded time.
3. `verify`: independently validate an existing evidence directory read-only.
4. `status`: report current/last trial facts without changing readiness.
5. `adoption-template`: create an unsigned human decision template only after a
   complete verified pass.
6. `adoption-verify`: validate signed human record and source/evidence binding.

**Implementation requirements**:

- Machine-readable JSON is primary; human output is not parsed internally.
- Require explicit sidecar/config/output paths; no PATH or cwd authority.
- Stable exit codes distinguish pass, fail, incomplete, invalid input, and
  operator recovery required.
- Cancellation records incomplete; it never rolls back a durable product fact.
- Redact secrets and bound output.

**Exit evidence**: `go test ./cmd/spk-m1-proving-ground`.

## T055: Implement fail-closed recovery and adoption decisions

**Files**: Create `adoption.go` and `adoption_test.go`.

**Implementation requirements**:

1. Startup/status distinguishes byte recovery, product restore, replay equality,
   epoch admission, and operator-ready states.
2. Indeterminate recovery never exposes a force-ready waiver.
3. Adoption state remains `not_adopted` until evidence passes and a separate
   authorized human signs the exact source/evidence tuple.
4. A signature cannot repair missing scenarios, faults, invariants, or artifacts.
5. Changed source, binary, sidecar, contract, budget, or evidence invalidates the
   adoption record.
6. The tool never edits production backend selection or starts M2 automatically.

**Tests**:

- Byte-recovered/product-invalid remains not ready.
- Unsigned, wrong signer, stale tuple, and post-signature mutation refuse.
- Valid signature changes only the adoption record verdict.

**Exit evidence**: `go test ./internal/testcampaign/m1 -run Adoption`.

## T056: Prove source binding, artifact digests, and human signature handling

**Files**: Extend evidence/adoption tests.

**Implementation requirements**:

1. Canonicalize signed bytes explicitly and version the signing envelope.
2. Verify trusted signer identity/authorization from explicit operator config.
3. Reject dirty/unbound source according to the contract before running.
4. Hash every required artifact and reject absent, duplicate, external-path, or
   changed artifacts.
5. Keep the human signature out of agent-generated fixtures except clearly
   synthetic cryptographic test keys.
6. Ensure source-bound verification is reproducible read-only.

**Tests**:

- Commit/tree/binary/artifact mutation invalidates pass/adoption.
- Path traversal and symlinked evidence artifacts fail.
- Incomplete-signature mutant is killed.

**Exit evidence**: `go test ./internal/testcampaign/m1 -run 'Source|Signature|Artifact'`.

## T057: Run the mission exit gate and stop before M2 on any failure

**Validation sequence**:

1. Run preflight from the exact source commit.
2. Run all unit, contract, adapter, lifecycle, application, recovery,
   proving-ground, campaign, CLI, race, vet, static, module, and guard checks.
3. Execute the complete one-RunID external campaign once without substitution.
4. Independently verify the resulting evidence and exact exit signature.
5. Confirm fourteen scenarios, five faults, eight summaries, nineteen mutants,
   one ordered trial, all invariants, review, and artifacts are exact.
6. Leave adoption `not_adopted` until a real authorized human signs.
7. If anything fails or is incomplete, publish honest evidence, retain current
   production boundary, and stop before M2.

## Review guidance

The independent reviewer must verify evidence from source and artifacts rather
than trust CLI text. Remove one child record, alter W3 to W1, reuse an epoch,
change a reviewer to Actor A, add one playback call, or deserialize a surviving
mutant; each must eliminate the pass signature. Confirm no agent signature is
presented as human approval.

## Completion criteria

- The whole torture chamber executed in one source-bound RunID.
- Exact evidence census and signature verify independently.
- Failure/incomplete paths are honest and fail closed.
- M1 completion and human production adoption remain separate decisions.
- No M2 work begins automatically.
