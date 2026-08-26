---
work_package_id: WP11
title: Operations Documentation and Phase Two Evidence
dependencies:
- WP10
requirement_refs:
- C-001
- C-002
- C-003
- C-004
- C-005
- C-006
- C-008
- C-009
- C-010
- C-011
- FR-020
- FR-023
- FR-024
- NFR-011
- NFR-012
- NFR-015
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
subtasks:
- T054
- T055
- T056
- T057
- T058
history: []
agent_profile: curator-carla
authoritative_surface: docs/
create_intent:
- docs/phase2-evidence/README.md
- docs/phase2-evidence/qualification-evidence.json
- docs/phase2-evidence/campaign-census.json
- docs/phase2-evidence/results.md
- docs/phase2-evidence/limitations.md
- docs/phase2-evidence/SHA256SUMS
- internal/architecture/beads_documentation_test.go
execution_mode: planning_artifact
model: ''
owned_files:
- README.md
- docs/TECHNICAL_PROJECT_PLAN.md
- docs/phase2-evidence/README.md
- docs/phase2-evidence/qualification-evidence.json
- docs/phase2-evidence/campaign-census.json
- docs/phase2-evidence/results.md
- docs/phase2-evidence/limitations.md
- docs/phase2-evidence/SHA256SUMS
- internal/architecture/beads_documentation_test.go
role: curator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP11 - Operations Documentation and Phase Two Evidence

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `curator-carla`
- **Role**: `curator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Publish the exact operational and architectural truth of Phase 2, then bind those claims to reproducible machine evidence.

The repository should read as a coherent delivered Beads tracker slice rather than a Phase 1 artifact that continually hedges about future implementation. It must show users how to build the Go project, install and qualify the pinned Beads provider, configure one canonical workspace across worktrees, understand declared capability limits, recover safely, export or back up data correctly, and verify the exact source/provider/schema combination that passed.

Implementation command:

```bash
spec-kitty agent action implement WP11 --agent codex
```

## Context

The current README accurately documents the qualified Phase 1 authority kernel but still says Beads is merely a candidate and lists all tracker adapters as future work. Phase 2 changes that public fact: Beads v1.2.2 is now the first production tracker adapter, behind provider-neutral ports and constrained by the authority kernel.

The technical project plan also contains a superseded research assumption:

```text
Use the published Go consumer API behind adapters/tracker/beads.
```

Research proved that Beads v1.2.2 has no supported public Go consumer package. Its reusable implementation lives below Go `internal/`, and this module cannot or should not import it. The qualified automation boundary is the documented `bd --json` protocol with `BD_JSON_ENVELOPE=1` and schema version 1. The production package is `internal/tracker/beads`, not the plan's hypothetical horizontal adapter directory.

Documentation must retain the central separation:

```text
Beads:       what work exists, what blocks it, and what humans discuss
Spec Kitty: whether work may run, where it runs, and how it is accepted
Git:         what actually happened
```

Phase 2 does not ship the whole eventual Spec Kitty product. It ships a qualified default tracker substrate composed with the Phase 1 authority kernel. The docs should state that boundary once, precisely, rather than repeatedly describing the delivered code as an incomplete experiment.

## Branch Strategy

Work only in the WP11 lane created by Spec Kitty and base it on the accepted WP10 result. The planning and merge target is `feat/beads-tracker-integration`.

Do not edit the primary checkout, create worktrees manually, merge an active lane, push directly to `main`, rewrite machine evidence, or modify qualification code/workflows to make documentation assertions pass. Other agents share the repository; preserve all unrelated changes.

Before writing, inspect the accepted commands, Make targets, capability reports, diagnostics, installer, qualification evidence schema, and WP10 output. Document the implementation that exists, using its exact names and flags. If the accepted code contradicts a mission contract, report the mismatch instead of documenting the contradiction as intended behavior.

### T054: Rewrite the README around delivered installation, configuration, capabilities, and authority truth

**Purpose**: Give developers and operators one accurate, beautiful entry point for the completed Phase 2 slice.

**Implementation requirements**:

1. Update the project-property summary to identify the qualified Phase 2 Beads tracker integration while retaining the Phase 1 authority qualification and current SQLite control-store fact.
2. Replace stale statements that Beads is only a candidate, that no tracker adapter exists, or that Phase 2's delivered package is still merely planned.
3. Preserve the Go-native redesign thesis and the original-vs-redesign comparison, adding the concrete outcome: Markdown task-list labor is being replaced by a provider-neutral ticket catalog without transferring mission authority.
4. Update the architecture diagram and repository guide to include:

   - provider-neutral tracker capability ports;
   - `internal/tracker/beads` and its bounded subprocess protocol;
   - portable fake and dishonest conformance layers;
   - canonical binding/concurrency policy;
   - real-provider qualification and evidence; and
   - the continuing kernel-to-adapter forbidden dependency.

5. Provide compile and installation instructions that distinguish:

   - ordinary portable development, which needs no `bd`, Dolt server, or network after Go dependencies are present;
   - building/installing the repository's implemented command/helper surfaces; and
   - installing or selecting the checksum-qualified Beads v1.2.2 binary for real-provider operation.

6. Use exact accepted command names and flags. Verify every documented local command against `--help`, a parser seam, `make -n`, or an equivalent non-destructive smoke.
7. Document explicit project binding: absolute executable, canonical Beads workspace shared by every linked worktree, workspace identity, schema, version/digest, and embedded-serialized versus server-multiwriter mode.
8. State that inspection is read-only and never initializes Beads, installs hooks, edits `AGENTS.md`/`CLAUDE.md`, repairs state, or silently chooses a per-worktree database.
9. Explain explicit disposable initialization with the accepted stealth/non-interactive/skip-agent/skip-hook command, clearly separating it from normal discovery.
10. Add a capability matrix for the exact qualified v1.2.2 behavior: catalog read/create, unguarded update, blocking graph, readiness diagnostic, append discussion, assignment/atomic claim where characterized, content-derived observation revision, unsupported guarded arbitrary update/safe arbitrary compensation, concurrency modes, portable export, and restorable backup.
11. Explain in the matrix that unsupported safety requests fail before mutation; do not represent them as missing product quality.
12. Keep the authority statement adjacent to capability documentation: issue assignment is not a lease, readiness is not permission to run, comments are not review verdicts, and tracker closure/status is not mission acceptance.
13. Update the verification ladder with portable PR gates and the exact real-provider qualification target from WP10.
14. Update “current scope” into a concise delivered-boundary section. List future mission method, full CLI, Team Kitty, Orchestrator API serving, MCP, migration, alternate production trackers, and multi-machine authority once, without scattering “incomplete” warnings throughout the document.
15. Preserve the Gapdb adoption boundary: SQLite is the current qualified control store for this repository; Gapdb remains a separate migration/adoption decision requiring its own evidence and explicit human record.

**Validation**: A new contributor can build and run portable tests without Beads, while an operator can install/qualify/configure v1.2.2 without guessing which workspace or authority rules apply.

### T055: Correct the technical plan's public-Go-API assumption and record compatibility policy

**Purpose**: Keep the long-form design record useful by replacing obsolete hypotheses with the implementation decision and its rationale.

**Implementation requirements**:

1. Correct every living statement that directs implementers to a published Beads Go consumer API, asks whether that API exists, or links to a nonexistent/unsupported consumer entry point.
2. Record the implemented compatibility tuple exactly:

   ```text
   Beads executable: 1.2.2
   source tag commit: 6c124203e771433a3550c348771a5b5e27fd3c21
   JSON envelope:    BD_JSON_ENVELOPE=1
   schema_version:   1
   integration:      argv-only documented JSON CLI
   ```

3. Explain why direct imports are forbidden: Go `internal` visibility, unsupported domain/storage contracts, Python-baggage-style semantic copying risk, and direct Dolt-schema coupling.
4. Update the package location to `internal/tracker/beads` and keep the authority kernel dependent only on provider-neutral tracker ports.
5. Correct Phase 2 work-package prose from “public Go API evaluation and pinned dependency” to the delivered bounded command protocol, version/schema preflight, and checksum-qualified executable.
6. Replace “confirm the exact Beads public API” in unresolved decisions with future-version protocol/capability qualification.
7. Remove or correct appendix references that imply a public Go consumer package exists. Link only to relevant official repository/protocol/architecture sources already validated by mission research.
8. State that v1.2.2 is a tested recovery release; version-number ordering alone does not prove features accidentally present in v1.2.0/v1.2.1 or future releases.
9. Define compatibility expansion: a new executable version or schema is unsupported until portable, dishonest, real-provider, contention, backup/export, and authority-invariant qualification passes with a new allowlisted digest.
10. Record that unknown optional schema-1 fields are ignored, while future envelope or metadata schemas fail before mutation.
11. Record exact safety limits: content digest is an observation token, local serialization is not CAS against humans, and arbitrary guarded update/compensation remains unsupported in v1.2.2.
12. Keep Team Kitty, Orchestrator API, MCP, hosted sync, alternate production trackers, Beads workflow authority, full mission decomposition, Gapdb migration, and distributed store access in their explicit future scopes.

**Validation**: Repository search finds no living claim that Phase 2 imports a public Beads Go API or supports an unqualified version range.

### T056: Publish operational diagnostics, recovery, export, and backup guidance

**Purpose**: Make safe response to provider faults actionable without asking operators to infer semantics from errors or human CLI output.

**Implementation requirements**:

1. Add one cohesive Beads operations section to the README, with deeper normative details in the technical plan where necessary.
2. Document preflight and health output: executable version/digest, protocol schema, canonical workspace identity, storage/concurrency mode, capability levels, read-only state, and recovery code.
3. Provide a diagnostic response table covering at least:

   - `unavailable` and spawn failure;
   - `version_skew` and `schema_skew`;
   - `workspace_mismatch`;
   - `lock_contended`;
   - `malformed_protocol` and bounded-output failure;
   - `ambiguous` mutation;
   - `revision_conflict`;
   - `attribution_lost`;
   - `guarded_update_unsupported`; and
   - `safe_compensation_unsupported`.

4. For ambiguity, require observation of mapping, operation marker, value, and content revision before retry; never recommend blind repetition.
5. For human-changed assignment, instruct the operator to preserve current provider state and resolve the durable reconciliation conflict explicitly.
6. Explain embedded writer serialization and server-mode qualification. State that adapter serialization coordinates Spec Kitty processes only and cannot fence direct human/external writers.
7. Explain how two linked worktrees verify the same canonical workspace and how mutation refuses on divergence.
8. Show the implemented portable JSONL export command/workflow and label it interchange, not full backup.
9. Show the implemented Dolt-native backup workflow, its qualification/inspection step, destination constraints, and how to verify a backup in a disposable workspace.
10. State that destructive restore never runs automatically against the project binding and requires explicit operator choice after inspection.
11. Explain disable/removal behavior: portable fake conformance and existing Spec Kitty authority/control data remain usable when Beads is absent; provider-dependent mutations fail with typed diagnostics.
12. Keep credentials, raw environment, work descriptions, criteria, comments, and provider warning prose out of examples and evidence.

**Validation**: Recovery instructions map stable diagnostic kinds to safe actions and never suggest unsafe cleanup, direct Dolt SQL, database copying while live, or parsing human output.

### T057: Curate the exact Phase 2 evidence index, results, and limitations

**Purpose**: Make Phase 2 claims independently checkable and bounded to the source/provider combination that actually ran.

**Implementation requirements**:

1. Create `docs/phase2-evidence/README.md` as the index and reproducibility guide.
2. Copy the admissible WP10 machine evidence without changing its measured fields into `qualification-evidence.json` and its exact scenario/campaign ledger into `campaign-census.json`.
3. Validate the evidence against the mission's schema before publication.
4. Record, at minimum:

   - exact qualified source commit and source-tree identity;
   - evidence generation start/finish UTC times;
   - Go/Git/tool versions and safe host GOOS/GOARCH identity;
   - Beads version `1.2.2`, exact binary SHA-256, source tag commit, JSON schema `1`, and storage mode;
   - canonical workspace identity in a redacted/digested form;
   - exact expected/started/terminal/pass/fail/non-applicable scenario counts;
   - portable, dishonest, real-provider, contention, human-preservation, architecture, and universal-invariant results;
   - latency sample counts and p50/p95/max;
   - authority invariant failure count, which must be zero for a pass;
   - backup/export verification result; and
   - every quality/race/vet/static/dependency/vulnerability/build/doc gate and duration.

5. Write `results.md` as a concise human mapping from product claims to machine files/test cases. Do not hand-convert a failure into a pass or omit failed/aborted/superseded attempts.
6. Write `limitations.md` with tested boundaries and explicit non-claims: one machine/filesystem authority, qualified OS/architecture/provider mode, no shared SQLite access, no distributed remote writer, no Team Kitty/API/MCP, no full mission task replacement, no alternate production tracker, no Gapdb migration, and any unsupported provider guarantee.
7. Generate `SHA256SUMS` over the immutable evidence payload files, excluding `SHA256SUMS` itself. Verify it with the platform's checksum tool.
8. Use a two-step provenance record to avoid circular commit claims:

   - freeze and qualify the source candidate, preserving its exact commit/tree in machine evidence;
   - commit the immutable evidence payload; then use a following docs-only attestation to name that payload commit and its checksums.

9. Make clear that later commits do not inherit the qualification merely because the evidence directory exists.
10. Never include credentials, absolute home paths, descriptions, criteria text, comment bodies, raw environment dumps, or unredacted arbitrary stderr.

**Planned files**:

- `docs/phase2-evidence/README.md`
- `qualification-evidence.json`
- `campaign-census.json`
- `results.md`
- `limitations.md`
- `SHA256SUMS`

**Validation**: A reviewer can trace every README delivery claim to an exact test/census/result and verify file hashes without trusting prose.

### T058: Add documentation, link, command-smoke, evidence, and authority-claim tests

**Purpose**: Prevent the README and technical plan from drifting back to the obsolete API assumption or overstating tracker authority.

**Implementation requirements**:

1. Add `internal/architecture/beads_documentation_test.go` using deterministic repository-relative reads and typed JSON decoding.
2. Assert the README and technical plan contain the exact compatibility tuple, JSON CLI boundary, canonical workspace law, no-unsolicited-setup rule, capability limitations, export/backup distinction, one-machine boundary, and Gapdb separation.
3. Assert living documentation does not contain stale claims such as:

   - Beads is only a candidate or no tracker adapter exists;
   - production uses a published/public Beads Go consumer API;
   - any supported Beads version or a loose semver range is accepted;
   - read-before-write is compare-and-swap;
   - JSONL export is a complete/restorable backup;
   - Beads assignment/readiness/status authorizes execution or advances a mission; or
   - Team Kitty, Orchestrator API serving, MCP, Gapdb migration, or full mission-method replacement shipped in Phase 2.

4. Validate relative Markdown links in all owned Markdown files resolve to files/anchors where practical. External links are syntax-checked but not fetched during portable tests.
5. Check Mermaid fences are balanced and retain small GitHub-compatible diagrams; do not attempt a network renderer in the unit test.
6. Smoke every documented repository-local command non-destructively:

   - command/help parsers accept documented flags;
   - referenced Make targets exist and `make -n` resolves them;
   - build/install package paths exist; and
   - provider commands are cross-checked against the accepted typed command contract rather than executed against user state.

7. Decode `qualification-evidence.json` and `campaign-census.json`; assert required fields, exact v1.2.2/schema-1 tuple, digest format, source identity, census equality, zero authority failures, and pass verdict.
8. Parse `SHA256SUMS` and verify every named local evidence payload hash.
9. Add mutant-style subtests that feed stale public-API, authority-overclaim, export-is-backup, missing-source, and census-mismatch fixtures to the assertion helpers and prove exact rejection.
10. Keep tests offline, read-only, bounded, platform-aware, and free of user configuration or real-provider mutation.

**File**: `internal/architecture/beads_documentation_test.go`.

**Validation**: The documentation gate fails for stale architecture promises, broken links/commands, malformed or mismatched evidence, and any tracker-authority overclaim.

## Test Strategy

### Documentation and evidence gate

```bash
go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=1
go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=10
```

### Command and link smoke

Run the exact smoke selector implemented in T058, then manually compare every README command with accepted `--help` output and Make targets. Provider initialization, backup, and restore examples must target obvious disposable placeholders and must not execute against the repository during tests.

### Full closeout validation

```bash
make quality
make full-campaign
make beads-qualification
go test -race ./...
go vet ./...
staticcheck ./...
go mod verify
govulncheck ./...
git diff --check
```

Use the exact accepted WP10 target names if they differ. Do not silently alter documentation to commands that were not run.

### Evidence integrity

```bash
sha256sum --check docs/phase2-evidence/SHA256SUMS
```

Record the final source candidate, evidence payload commit, docs-only attestation relationship, and working-tree state. Qualification is admissible only for the exact identities in the evidence.

## Definition of Done

- README describes a delivered Phase 2 Beads tracker slice composed with the qualified Phase 1 authority kernel.
- Build, portable development, helper installation, provider installation, configuration, qualification, and verification commands are accurate and smoke-tested.
- The v1.2.2/schema-1/checksum-qualified JSON CLI boundary is stated consistently.
- No living plan or README claim depends on a public Beads Go consumer API.
- Capability levels and unsupported guarded mutation/compensation are explicit.
- Beads never appears as mission, execution, workspace, review, acceptance, or integration authority.
- Canonical workspace, concurrency mode, single-machine ownership, and no-unsolicited-setup rules are operationally clear.
- Diagnostics map to safe recovery actions without prose parsing or blind retries.
- JSONL export and Dolt-native restorable backup are documented and tested as different capabilities.
- Gapdb remains a separate signed control-store adoption decision; SQLite remains the active repository control store.
- Phase 2 evidence records exact source, provider binary digest/version, schema, mode, census, timings, gates, and limitations.
- Evidence hashes verify, failed/superseded attempts remain visible, and no secret/body/raw environment data is published.
- Documentation tests reject broken links/commands, stale API assumptions, authority overclaims, and mismatched evidence.
- Full closeout gates pass for the frozen source candidate with no unexplained waiver.
- T054 through T058 are recorded done with:

  ```bash
  spec-kitty agent tasks mark-status T054 T055 T056 T057 T058 --status done --mission beads-tracker-integration-01M0YG7E --json
  ```

## Risks

- **Stale Phase 1 framing**: Leaving “Beads candidate” language makes delivered code look hypothetical. Update status and scope coherently across the README.
- **Overcorrection**: Phase 2 does not ship the full product. State the remaining roadmap once without weakening the tracker delivery claim.
- **API fiction**: A stale public-Go-API sentence will send future maintainers toward impossible/unsupported imports. Test its absence.
- **Version optimism**: A newer number does not imply compatibility. Bind claims to the qualified tuple and digest.
- **Authority leakage in prose**: Words such as claim, ready, assigned, or closed can imply permission. Pair every tracker capability with its authority limit.
- **Unsafe setup examples**: Default `bd init` can alter agent/Git configuration. Use only accepted disposable stealth/skip controls and say discovery never initializes.
- **Export/backup confusion**: JSONL does not preserve Dolt history. Never call it full or restorable backup.
- **Circular evidence identity**: An evidence file cannot truthfully name its own future commit. Use frozen source, payload commit, and docs-only attestation layers.
- **Evidence laundering**: Copying only successful results hides risk. Preserve failed, aborted, superseded, and non-applicable outcomes with disposition.
- **Self-mutating doc tests**: Tests must not initialize providers, fetch network links, restore data, or rewrite documentation/evidence.
- **Sensitive publication**: Redact home paths, credentials, raw environments, work bodies, and arbitrary provider output.
- **Cross-WP drift**: WP10 owns qualification commands and generated truth. Do not edit those surfaces from a documentation lane.

## Reviewer Guidance

Read the README first as a new operator. Confirm it makes the delivered Phase 2 outcome obvious, then follow the portable build path and the isolated real-provider path exactly. Reject commands that rely on unmentioned state, unsafe initialization, or an arbitrary PATH binary.

Search all living docs for public-Go-API assumptions, loose version promises, Beads-as-authority language, export/backup conflation, and stale “no adapter exists” claims. Verify the technical plan records why the researched CLI decision supersedes its hypothesis instead of silently deleting the design history.

Trace the evidence in both directions: each public claim must cite a result, and every machine result must identify exact source, binary digest/version, schema, mode, census, and verdict. Verify checksums and the non-circular source/payload/attestation chain. Ensure limitations say what was not tested.

Run the documentation mutants and command smoke. Deliberately insert a public-API sentence, an assignment-authorizes-work sentence, a JSONL-is-backup sentence, a broken link, and a mismatched scenario count; each must red for the intended reason.

Reject unverified marketing language, repeated incompleteness hedges, invented commands, direct Beads/Dolt imports, human-output parsing, unsafe restore automation, hidden failed attempts, authority claims unsupported by code, or edits outside WP11 ownership.
