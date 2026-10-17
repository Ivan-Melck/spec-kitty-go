# Official-upstream reconciliation: 8c89aa9

## 1. Identity, scope, and evidence strength

This is a bounded **source-and-oracle reconciliation**, not a Go implementation,
a test run, a mission admission, or a declaration of parity completion.

| Reference | Immutable identity |
|---|---|
| Official repository | `https://github.com/spec-kitty/spec-kitty` |
| New commit | `8c89aa90fe3d51f8f406158de0a30b98ac4a7ab0` |
| New tree | `8f65a91977261236544947002546aa00d83a2920` |
| Commit title | `fix(ownership): infer valid surfaces for mixed paths and literal files` |
| Detached reference | `/tmp/spec-kitty-parity-reference-8c89aa9` |
| Previous commit | `d0c6ca61d87f1d92512bb45386351aae3be1eba5` |
| Previous detached reference | `/tmp/spec-kitty-parity-reference-d0c6ca6` |

The comparison is the **entire `d0c6ca6..8c89aa9` interval**, not just the tip's
ownership fix: **72 files changed, +1395/-301**. Source/test paths below are
relative to the new immutable reference unless explicitly identified as redesign
paths. Commit/tree objects, rather than a directory name or a moving `main`, bind
these observations. The reference's inspected working tree was clean. The user's
Python checkout HEAD and local `main` remained
`bcb80f5230bd001ac8bebce25bb70788f9a32438` during the identity inspection.

`spec-kitty --version` reported **4.0.0rc6**. That is installed-tool metadata, not
proof that the installed executable runs this upstream tree. The redesign's
`AGENTS.md` orientation says 3.2.6; neither label supersedes the source identity.
No upstream tests or candidate tests were executed for this reconciliation.

**Authority distinctions:**

- Product source establishes what this immutable upstream tree implements.
- Named executable tests establish checkable upstream oracle obligations; reading
  them does **not** establish a passing run here or a passing Go candidate.
- Help fixtures, completion manifests, API inventories, prompts, and ADRs provide
  surface/documentation/architecture evidence, not executable qualification.
- This new document alone is this task's write. Existing reconciliation documents,
  historical inventories, candidate files, admission records, and proof bindings
  are not updated. Inventory regeneration belongs to the parent task.

## 2. Contract census: unchanged verbs are not unchanged behavior

`src/specify_cli/orchestrator_api/envelope.py` still declares contract **1.15.0**
and minimum provider **0.1.0**. The orchestrator API source directory and
`src/specify_cli/core/upstream_contract.json` have no changes in this interval;
the contract still lists **27 allowed verbs**. Delegated host services can change
behavior without a new API verb or a new contract version.

Both old and new raw `src/specify_cli/_completion_manifest.json` root `commands`
objects contain **63 entries**, one of which is the synthetic
`__force_multi_command_mode__` entry. Redesign
`scripts/parity/capture_upstream.py` excludes names starting with `__`, yielding
**62 root completion registrations** in both captures. Thus 63 raw entries versus
62 captured registrations is a counting convention, **not a newly landed root
command**. The manifest does change in this interval; unchanged root cardinality
must not be represented as unchanged nested signatures or help bytes.

Keep [the d0c6ca6 reconciliation](UPSTREAM_RECONCILIATION_D0C6CA6.md) and
[its inventory](upstream-main-d0c6ca6.json) as historical records. The current
[upstream inventory](upstream-main.json) is a source inventory only; this document
does not certify that it has been regenerated or checked at the new identity.

## 3. Task status: semantic mutation versus authored display

### 3.1 `mark-status` is a subtask event writer, not a WP lane writer

Sources:

- `src/specify_cli/cli/commands/agent/tasks.py` — registered CLI wrapper.
- `src/specify_cli/cli/commands/agent/tasks_mark_status.py` — live orchestration,
  owner selection, status conversion, event emission, and reporting.
- `src/specify_cli/cli/commands/agent/tasks_outline.py` — recognition/owner-index
  helpers and dedicated WP-ID refusal.
- `src/specify_cli/core/subtask_rows.py` — authored roster and snapshot readers.
- `src/specify_cli/status/models.py` and `status/__init__.py` — shared subtask
  vocabulary and WP-ID normalization.

The important distinction is semantic scope, not the words printed in a table:

| Input | Canonical effect |
|---|---|
| `mark-status T001 --status done` | Owning WP's subtask slot records `done`. |
| `mark-status T001 --status pending` | Subtask slot records `planned`, not a persisted `pending` alias. |
| `mark-status T001 --status skipped` | Subtask slot records the terminal subtask value `skipped`. |
| `mark-status T001 --status not_applicable` | Input alias records `skipped`; the alias is not persisted. |
| `mark-status WP01 --status done` | Refused with `move-task` guidance; no WP lane transition. |
| `move-task WP01 --to <lane>` | Separate WP transition core and guards, not `mark-status`. |

The landed interval adds explicit subtask skip semantics and owner scoping. A
subtask's `skipped` value must not be conflated with a whole-WP skipped lane or
its metadata/transition policy. Only `done`, `pending`, `skipped`, and
`not_applicable` are admitted `mark-status` inputs.

Authored WP rosters decide ownership first; `tasks.md` is a fallback owner index
when no authored roster owns an ID. Repeated IDs across WPs require `--wp`;
`--wp` must name an actual owner and is normalized/validated. Unreadable roster
information fails closed rather than hiding another possible owner. Owner
selection happens before completion writes. A repeat whose reduced snapshot
already holds the requested canonical value is `ALREADY_SATISFIED` and writes no
new completion event. Batch completion is grouped into one subtask delta per WP,
not one delta per ID. A resolved ID without a discoverable owning WP cannot be
reported as a successful canonical mutation.

`tasks.md` checkbox and pipe-table cells are **authored reference/display bytes**.
Recognition uses throwaway copies; completion is emitted as `InnerStateChanged`
under the mission status lock. A stale unchecked checkbox does not supersede a
completed snapshot, and a checked checkbox does not supply new canonical
completion. Read placement is `TASKS_INDEX`/primary; completion write placement
uses the stored topology's write capability, not a read-path-derived write.

The event-only/no-artifact-commit contract **predates this interval** and is
explicitly documented in the new source. `--auto-commit` is a compatibility
input, not evidence of a task-artifact commit. The live flow never calls
`_ms_commit`; that symbol survives as an unreachable compatibility/test shim.
Tests directly exercising it do not prove a live commit route. The former
protected-primary artifact-commit refusal and `move-task`'s skip pre-gate do not
apply to this event-only command. Do not generalize this into removal of other
preflights: the owned arm still requires an unstaged index and refuses a resolved
false auto-commit option as `OWNED_OPTION_UNSUPPORTED`.

Named oracles in
`tests/specify_cli/cli/commands/agent/test_tasks_mark_status.py`:

- `test_repeated_subtask_requires_wp_and_scoped_mark_is_idempotent` — ambiguous
  ownership refusal, explicit owner choice, and repeat-without-event control.
- `test_skipped_subtask_delta_round_trips` and
  `test_mark_skipped_emits_skipped_state` — persisted value/alias semantics.
- `test_wp_id_rejected_with_move_task_guidance`,
  `test_wp_id_rejection_is_actionable_in_human_output`, and
  `test_wp_id_rejection_does_not_consult_lane_state` — refusal is semantic, not
  dependent on a current lane display.
- `test_existing_checkbox_unchanged`, `test_existing_pipe_table_unchanged`, and
  `test_event_append_failure_returns_error_without_mutating_tasks_md` — authored
  bytes stay untouched, including the event-write failure path.

`tests/specify_cli/cli/commands/agent/test_tasks_mark_status_seam.py` separately
pins patched seams and the retired protected-branch consultation through
`test_c001_auto_commit_input_no_longer_consults_protected_branch` and
`test_c001_protected_gate_not_consulted_when_auto_commit_resolves_false`.
Those do not waive ownership, index, topology, or persistence-error refusals.

### 3.2 Completion reads and rollback signals

`src/specify_cli/core/subtask_rows.py` reads authored subtask rosters independently
of legacy checkbox rows. A missing/malformed/ambiguous roster and a silent
snapshot fail closed; an explicitly empty roster has nothing to block on.
Terminal subtask detection accepts the canonical terminal values, including
`skipped`. Migration's seed reader still understands legacy checkboxes; this is
not renewed runtime checkbox authority.

In `tests/specify_cli/core/test_subtask_rows_snapshot.py`,
`test_guard_blocks_off_snapshot_when_incomplete` and
`test_guard_unblocks_off_snapshot_when_all_done` exercise guards with no checkbox
rows. Negative controls include `test_missing_subtasks_key_fails_closed`,
`test_missing_wp_file_fails_closed`, `test_ambiguous_wp_files_fail_closed`,
`test_malformed_wp_file_fails_closed`, and
`test_fail_closed_on_silent_snapshot_blocks`. The contrasting controls are
`test_empty_roster_is_nothing_to_block_on` and
`test_backfill_seed_reader_still_folds_legacy_checkboxes`.

Do not collapse all readers into a stronger migration claim:
`src/specify_cli/cli/commands/agent/tasks_transition_core.py` retains a
snapshot-first migration window. Its low-level snapshot accessor can return
`None` when no feature context or reduced WP entry exists, selecting the request's
legacy `unchecked_subtasks` fallback. That explicit low-level compatibility
branch is distinct from the authored-roster guard's fail-closed contract.

`src/specify_cli/cli/commands/agent/tasks_transition_core.py` and
`tasks_move_task_executor.py` expose rollback-to-planned subtask-reset information
in JSON and human output. Claim release/review-override clearing and reset
semantics must not be replaced by a lane-label-only update. The summary separates
previously completed tasks from never-started siblings; without a snapshot it
cannot invent previous completions.

`tests/specify_cli/cli/commands/agent/test_move_task_rollback_signal.py` names the
relevant obligations: `test_rollback_summary_splits_completed_from_never_started`,
`test_rollback_summary_all_never_started_when_no_snapshot`,
`test_build_claim_review_override_sets_rollback_summary`,
`test_build_claim_review_override_summary_survives_empty_roster`,
`test_rollback_signal_json_fields`, and
`test_rollback_signal_human_line_names_count_and_siblings`.
`test_tasks_transition_core.py` supplies the adjacent transition-core oracles;
these are not authorization to bypass that core's independent guards.

### 3.3 `agent status emit` accepts two WP-selector forms

`src/specify_cli/cli/commands/agent/status.py:_resolve_wp_arg` accepts the optional
positional WP ID or `--wp`. Both are accepted together only when their normalized
IDs match. Missing selection, different selections, or a non-`WP<digits>` value
refuse; lowercase valid IDs normalize to uppercase. This is selector ergonomics,
not a new lane or a new direct-write capability.

Oracle:
`tests/agent/cli/commands/test_status_cli.py:TestEmit::test_emit_wp_argument_forms`
(the test function name is the durable locator). Existing transition,
evidence-parsing, materialization, and lifecycle tests in that file remain
independent obligations. `docs/api/agent-subcommands.md` documents the positional
and option alternatives; source/test behavior, not the help text, decides refusal.

## 4. Requirement grammar, finalize, and runtime agreement

### 4.1 Single authority and the actual interval delta

The single grammar authority and per-reference classification were already
present at d0c6ca6. This interval **extends them to research kinds and carries the
resolved mission type through consumers**; it does not introduce a second grammar
or make every declared requirement family a mandatory coverage family.

Authority: `src/specify_cli/requirement_mapping/grammar.py`, consumed by
`requirement_mapping/{__init__,lint}.py`,
`cli/commands/agent/{mission_parsing,mission_finalize_validation,tasks_map_requirements,tasks_mapping_core}.py`,
and runtime `src/runtime/next/{runtime_bridge_guards,runtime_bridge_cores}.py`.
The amended ADR is
`docs/adr/3.x/2026-09-29-1-requirement-id-grammar-single-authority.md`.

Grammar facts:

- All mission types admit `FR`, `NFR`, `C`, and `SC`; **only research** additionally
  admits `DR`, `AR`, and `QR`. Research still admits shared constraints and success
  criteria. Research must not need a fabricated `FR` just to validate its refs.
- Form: optional valid foreign `slug#` qualifier, kind, hyphen, significant-width
  digits, optional one-letter suffix. Kind matching is case-insensitive;
  canonical kinds uppercase and suffixes lowercase. `FR-01` and `FR-001` remain
  different IDs. Spec scans admit lowercase suffixes only; ref parsing is
  suffix-case tolerant. This is scoped case handling, not global uppercasing.
- A foreign qualifier is consumed as a qualifier and never resolved into local
  coverage. Invalid qualifier attempts must not leak their trailing ID as local.
  Compound/dotted tails are dropped, not truncated into a valid parent ID;
  sentence-final punctuation remains a positive control.
- Patterns compile through the RE2-backed safe regex seam. No second local regex,
  lookaround, backreference, or independent canonicalizer is authorized.
- Tokenization preserves raw diagnostic case and turns non-string list members
  into explicit malformed tokens rather than silently discarding them.

`tests/specify_cli/test_requirement_id_grammar.py` includes
`test_research_kinds_are_scoped_to_research_missions`,
`test_digit_width_is_significant`,
`test_find_all_never_leaks_a_local_id_from_an_invalid_qualifier`,
`test_find_all_invalid_qualifier_same_text_positive_control`,
`test_dotted_id_is_dropped_not_truncated_by_find_all`,
`test_dotted_id_sentence_final_period_positive_control`,
`test_uppercase_suffix_declares_nothing_lowercase_declares`, and
`test_verdict_table`. These controls distinguish grammar acceptance from mere
substring recognition.

`tests/architectural/test_requirement_id_grammar_single_source.py` extends the
single-source scanner to research-only patterns. Its
`test_research_only_pattern_outside_grammar_is_flagged` is the new architectural
negative control. Existing injection/prose/concern-ID controls and non-vacuous,
two-sided allowlist accounting remain obligations; prose mentioning IDs is not
itself a competing grammar implementation.

### 4.2 Mapping verdicts are per ref, with different coverage consequences

The shared verdict vocabulary is `malformed`, `unknown_spec_id`, and
`foreign_qualified`. The first two fail the gate. `foreign_qualified` is not a
failing reason, but it is also **not accepted local coverage**.

A valid sibling ref still counts even when another ref is rejected; the rejected
sibling still makes the gate fail. An accepted SC-only WP has refs. A foreign-only
WP has no accepted local ref and is missing, without misreporting the foreign
citation as an unknown local spec ID. Unreferenced SCs are diagnostic/non-failing;
unmapped functional requirements still fail. Research-kind acceptance does not
silently expand the functional coverage set beyond `FR`.

Runtime fact collection now resolves the mission type for both spec extraction
and ref classification. The pure bridge core takes the injected grammar authority;
it does not supply a local fallback grammar. Finalize uses that same mission type
for extraction, dependency/ref parsing, and mapping validation.

Oracles:

- `tests/runtime/test_requirement_grammar_parity.py`:
  `test_research_kind_parity_both_gates_agree`,
  `test_accepting_case_both_gates_agree`,
  `test_undeclared_sc_positive_control_both_gates_fail_same_reason`,
  `test_malformed_ref_parity`, `test_foreign_qualified_ref_parity`, and
  `test_foreign_only_wp_is_missing_in_both_gates` compare the runtime/finalize
  decisions, not just their parsers.
- `tests/specify_cli/cli/commands/agent/test_finalize_requirement_id_grammar.py`:
  `test_research_refs_validate_without_fake_fr` and
  `test_research_keeps_shared_constraint_and_success_criterion_ids` are new
  research obligations. Existing `test_cli_half1_valid_sibling_counts_while_undeclared_sc_fails`,
  `test_cli_half2_foreign_qualified_never_fails`,
  `test_cli_sc_only_wp_counts_as_having_refs`, and
  `test_cli_foreign_only_wp_is_missing_but_not_unknown` pin the nuanced outcomes.
  `test_real_run_malformed_ref_leaves_file_byte_identical` is the refusal/no-write
  control; `test_real_run_never_erases_authored_refs` and
  `test_real_run_keeps_qualified_citation_and_suffixed_ref` pin preservation.
- `tests/specify_cli/cli/commands/agent/test_map_requirements_grammar.py` and
  `tests/specify_cli/test_requirement_mapping.py` cover the adjacent mapping and
  extraction consumers; acceptance at one consumer cannot excuse divergence at
  another.

### 4.3 Tasks-outline validation is not finalization

`src/specify_cli/cli/commands/agent/mission_finalize.py` now admits manifest-only
**validation** when `wps.yaml` exists and WP prompt files do not yet exist. It
validates manifest dependencies and requirement refs, using manifest WP IDs for
coverage rather than an empty prompt-file set. JSON reports
`validation_stage: "tasks-outline"`, `validate_only: true`, parsed mappings, and
requirement diagnostics. Prompt validation remains a later tasks-packages step.

Without `--validate-only`, promptless finalization refuses with
`WP prompt files are required to finalize tasks; run tasks-packages first`.
Missing task directories, dependency cycles/conflicts, and mapping failures are
not waived by the outline path. A successful outline check is not a planning
commit refresh, lane allocation, execution admission, or completed mission.

`tests/tasks/test_finalize_tasks_wps_yaml_unit.py` names the new controls:
`test_outline_validates_manifest_refs_before_wp_prompts_exist`,
`test_outline_rejects_unmapped_manifest_refs`, and
`test_finalization_requires_wp_prompts`. Inherited controls in that file preserve
manifest precedence over prose, explicit empty dependency lists, index
regeneration, and the no-manifest prose compatibility path.

## 5. Ownership surfaces: mixed roots and literal files

Changed authority:
`src/specify_cli/ownership/inference.py:infer_authoritative_surface`;
validation relation:
`src/specify_cli/ownership/validation.py:validate_authoritative_surface`.

The inferred surface must satisfy the **existing narrow validator**: a nonempty
surface is equal to, or a prefix of, at least one owned-file entry. The tip fix
repairs inference to meet that relation; it does not weaken validation or declare
that the surface must contain every entry.

| Owned entries | Inferred surface / consequence |
|---|---|
| `src/foo/bar.py` | `src/foo/`, not a file path with an invalid trailing slash. |
| `src/foo/*.py`, `tests/foo/test_bar.py` | `src/foo/`, the first owned directory when roots diverge. |
| `src/api/*.py`, `src/api/*.ts` | Shared `src/api/`. |
| `src/foo/**` or `src/foo/*` | Existing `src/foo/` behavior. |
| `src/foo/` | Existing directory behavior. |
| Empty list | Empty surface; no invented ownership. |

Final-segment `*`, `?`, or `[` globs and dot-bearing literal filenames reduce to
their containing directory. Shared path segments determine the common prefix.
If roots diverge, inference uses the first entry's directory; if the constructed
candidate prefixes no entry, it returns the first owned entry itself. Thus the
mixed-root fallback is order-sensitive and stays tied to an actual owned path,
not a synthesized repository-wide `/` authority. This is a lexical heuristic,
not universal filesystem-aware detection of extensionless filenames.

Exact oracles in `tests/specify_cli/ownership/test_inference.py`:

- `TestInferAuthoritativeSurface::test_disjoint_top_level_uses_first_owned_directory`.
- `TestInferAuthoritativeSurface::test_single_literal_file_uses_its_directory`.
- `TestInferredSurfacePassesValidation::test_inferred_surface_validates` —
  parameterized actual inference-to-validator relation, including mixed/literal
  cases and inherited filename-glob controls.
- `TestInferOwnership::test_code_and_tests_manifest_passes_surface_validation` —
  combined source-and-test content through complete manifest inference.
- `TestInferAuthoritativeSurface::test_empty_list_returns_empty_string` and the
  existing wildcard/directory tests — negative and non-regression controls.

**Inherited, not new:** planning-only inference defaults to the mission planning
glob; mixed code/planning signals prefer code change. Missing code-path tokens
still produce `src/**` **with `SRC_FALLBACK_WARNING`**. That explicit warned
finalization inference is not a runtime permission to silently broaden or replace
ownership. `infer_ownership` never infers `scope: codebase-wide`: that exemption
must be human-authored. Existing validation still rejects narrow empty/mismatched
surfaces; execution-mode inconsistencies are warnings; codebase-wide and
explicit dependency-ordered overlap exemptions remain distinct policies.

### Runtime no-silent-fallback boundary

Do not confuse three different things: the repaired lexical surface inference,
the warned `src/**` planning fallback, and runtime placement/ownership resolution.
The first two do not authorize runtime fallback to ambient roots, primary files,
or a different claimed checkout when required identity/placement facts fail.

The inspected `tasks_mark_status.py` threads a validated `OwnedCheckout` without
ambient project/root/handle discovery in the owned arm. Its primary index read
and topology-routed status write remain separate capabilities. Owner-roster
failures refuse rather than silently choosing the first readable owner. This is
checkable in the owner-selection and unchanged-artifact oracles above, not merely
an ownership docstring promise.

`tests/specify_cli/cli/commands/test_next_owned_fact.py` additionally names the
runtime-entry obligations:
`test_resolve_mission_slug_with_a_fact_returns_its_slug_without_any_resolver`,
`test_resolve_next_owned_renders_a_refusal_in_nexts_envelope`,
`test_resolve_next_owned_renders_the_minters_selection_signal_and_exits`,
`test_minter_discovers_the_sole_mission_only_inside_the_claimed_checkout`, and
`test_lifecycle_wrappers_pass_the_fact_never_a_bare_root`.
These inherited ownership controls must not be weakened by adopting the new
mixed-path inference. They are oracle obligations, not an executed no-fallback
qualification in this task.

## 6. Mission-stage resolution and coordinator closeout

### 6.1 Stage checks use the resolved mission schema

Sources: `src/specify_cli/cli/commands/agent/mission_setup_plan.py`,
`mission_check_prerequisites.py`, and
`src/specify_cli/missions/_substantive.py`.

Research now receives its own substantive-spec schema in setup-plan and resume
probing, rather than an FR-only check or blanket non-software acceptance. A
committed research question/scope-only template is not enough to advance as a
substantive spec. The resume signal still requires committed content. Other
non-software/non-research mission types retain the existing resume-probe behavior
until their schemas are defined; this interval does not prove equivalent new
schema gates for every mission type.

Oracles:

- `tests/specify_cli/cli/commands/agent/test_mission_setup_plan_phases.py`:
  `test_research_spec_gate_uses_research_schema` and
  `test_research_setup_plan_refuses_question_scope_only_template`.
- `tests/specify_cli/cli/commands/agent/test_mission_check_prerequisites.py`:
  `test_resume_probe_checks_committed_research_spec_content`.
- `tests/specify_cli/missions/test_substantive_gate_formats.py` contains the
  format/schema-level obligations; setup-plan and resume are separate callers.

Inherited mission-stage resolution controls remain important:
`test_setup_plan_uses_single_loaded_meta_snapshot_when_file_changes_after_read`,
`test_setup_plan_resolves_template_context_from_primary_planning_surface`,
`test_resolve_plan_template_rejects_legacy_only_mission_field`,
`test_setup_plan_refuses_present_invalid_primary_meta_before_template_or_state_change`,
and `test_resolve_plan_template_fails_closed_for_bad_configuration` in the
setup-plan phase test file. A plan scaffold's existence, pristine-template state,
substantive content, and committed state are distinct observations. None alone
means every subsequent runtime stage has passed.

### 6.2 Planning reads and status reads retain different homes

The architectural closeout is read-side and action-scoped; it is not permission
to reroute status to primary or to globally flatten coordination topology.
`src/runtime/next/runtime_bridge_guards.py` resolves mission type from the
planning surface. The architectural oracle's runtime bridge scope is updated
for that read rather than treating it as an unexamined status-read exception.

`tests/architectural/test_coord_read_residuals_closeout.py` pins the distinctions:

- `test_fr003_runtime_bridge_get_mission_type_reads_are_clean_not_pinned`.
- `test_fr003_sanctioned_exclusions_are_read_func_scoped_for_status`.
- `test_fr005_runtime_next_in_both_scan_families` and
  `test_fr005_runtime_next_identity_read_floor_is_non_vacuous`.
- `test_fr005_runtime_next_floor_fails_if_scope_reverted` — scanner coverage
  cannot disappear and make the gate vacuously green.
- `test_wp04_status_from_husk_proofs_exist` and
  `test_no_status_leg_rerouted_to_primary` — planning closeout must preserve the
  topology-routed status leg.

These are upstream source/scanner obligations, not redesign runtime acceptance
or evidence that all coordinator paths have been executed here.

### 6.3 Canonical metadata keys and architectural consistency

`src/specify_cli/meta_keys.py` centralizes persisted metadata-key spellings.
Consumers changed across `mission_metadata.py`, mission-creation decisions/meta,
`audit/shape_registry.py`, `lanes/compute.py`, consolidation mission-number bake,
and migration backfill/state modules. Key reuse is not a new metadata policy or
a new mission state transition.

The new/updated architectural obligations live in:

- `tests/audit/test_shape_registry_writer_parity.py` — audit registry follows
  writer shape/key authority instead of maintaining a divergent spelling set.
- `tests/coordination/test_flatten_primitive_single_source.py` — canonical flatten
  primitive's three metadata mutations stay the single mutation authority.
- `tests/core/test_mission_creation_family.py` and
  `tests/core/test_mission_creation_purity.py` — creation-family seams and pure
  decision boundaries remain guarded.

These tests qualify their stated structural surfaces only. They do not establish
that accepting a metadata ADR has deployed a replacement runtime protocol.

## 7. Decision persistence warnings and documentation oracles

`src/specify_cli/events/decision_log.py` adds capture of decision-log Git commit
warnings. `src/specify_cli/cli/commands/next_cmd.py` captures them around answer
persistence and carries them to advancing-mode JSON as `warnings`. A successful
persistence/advance with a failed best-effort Git commit must not silently look
like a confirmed commit. This is not an invented failure verdict, a new API
error code, or authority to discard the persisted decision.

Oracles: `tests/specify_cli/events/test_decision_log.py` and
`tests/specify_cli/cli/commands/test_next_owned_fact.py`, notably
`test_print_decision_json_includes_commit_warning_without_stderr`.
The captured warning path does not convert bare query-mode `next` into a writer;
query/advancing distinction and ownership refusals remain independent.

Documentation changes include `docs/api/agent-subcommands.md`, the requirement
ADR, `docs/changelog/CHANGELOG.md`, tasks help/JSON fixtures under
`tests/specify_cli/cli/commands/agent/fixtures/tasks_cli/`, and
`docs/guidelines/software-dev/tasks-outline/prompt.md`. Use their actual changed
command shapes without treating them as evidence of a completed Go endpoint.

`tests/docs/test_committed_docs_index_fresh.py` adds a real-upstream-tree docs
retrieval-index freshness gate. It is an upstream documentation oracle, not an
instruction to run upstream tests, edit redesign inventories, or replace
historical proof identities. It was inspected, not executed here.

## 8. Commands and reproducible read-only comparison

The following are **documented upstream usage examples, not commands executed by
this task**. They mutate upstream state when used against a live project and do
not assert availability in the Go candidate:

```sh
# A repeated subtask ID needs its actual owning WP.
spec-kitty agent tasks mark-status T001 --wp WP01 --status skipped --mission <mission>
spec-kitty agent tasks mark-status T001 --wp WP01 --status not_applicable --mission <mission>

# WP lifecycle remains a different command family.
spec-kitty agent tasks move-task WP01 --to for_review --mission <mission>

# Two equivalent WP selector forms; --actor and --to still matter.
spec-kitty agent status emit WP01 --to claimed --actor claude --mission <mission>
spec-kitty agent status emit --wp WP01 --to claimed --actor claude --mission <mission>

# Outline checks may precede prompt authoring; finalization may not.
spec-kitty agent mission finalize-tasks --mission <mission> --validate-only --json

# An incomplete mapping is repaired through the documented mapping command.
spec-kitty agent tasks map-requirements --batch '<mapping-json>' --mission <mission> --json
```

Replace angle-bracket placeholders before use. `--wp` on `mark-status` selects a
subtask owner; on `status emit` it selects the WP transition target. Neither
creates a new orchestrator API verb. A `mark-status WP01 ...` invocation remains
a refusal, even though `status emit WP01 ...` is a valid selector shape.

A read-only immutable comparison can be reproduced without checking out, fetching,
importing upstream Python, regenerating inventories, or running test collectors:

```sh
UPSTREAM=/tmp/spec-kitty-parity-reference-8c89aa9
OLD=d0c6ca61d87f1d92512bb45386351aae3be1eba5
NEW=8c89aa90fe3d51f8f406158de0a30b98ac4a7ab0

git --no-replace-objects -C "$UPSTREAM" rev-parse "$NEW^{commit}" "$NEW^{tree}"
git --no-replace-objects -C "$UPSTREAM" show -s --format=%s "$NEW"
git --no-replace-objects -C "$UPSTREAM" diff --stat "$OLD" "$NEW"
git --no-replace-objects -C "$UPSTREAM" diff "$OLD" "$NEW" -- \
  src/specify_cli/ownership/inference.py \
  src/specify_cli/requirement_mapping/grammar.py \
  src/specify_cli/cli/commands/agent/tasks_mark_status.py
```

The new manifest and old manifest must be counted with the same synthetic-entry
exclusion if comparing captured registration counts. No capture `--check`, test
pass, or candidate execution result is claimed by the commands above.

## 9. Go disposition and preserved gates

The source deltas are future implementation and negative-control obligations:
separate WP lanes from subtask states; preserve authored index bytes; resolve
ambiguous ownership explicitly; share the mission-scoped grammar and verdicts;
keep promptless outline validation separate from finalization; infer mixed/literal
surfaces without widening runtime authority; read planning metadata and status
from their proper surfaces; surface decision commit warnings without relabeling
persistence truth.

Public Go families still missing from the production surface — design-phase and
authoring/context/interview APIs, decision operations, workspace/WP lifecycle,
acceptance/consolidation, and full runtime-next/CLI transport coverage — remain
**M5-gated on independently accepted M3 and M4**, with M2 prerequisites preserved.
An inventory of upstream verbs is not their Go implementation. This report does
not recensus or certify the candidate's shipped endpoint coverage.

The accepted upstream event-ledger ADR discussed in the previous reconciliation
remains architecture authority, not proof its default-on local publication and
retention implementation landed. This interval does not implement that ADR merely
by changing task events or warning presentation. **M4 ENTRY admission is not M4
acceptance, implementation, or runtime authority** and cannot satisfy the M5 join.
No new governance opcodes, admission, guard changes, hosted-consent changes,
retention operations, or historical-proof retargeting follow from this report.
Local-first/no-mandatory-hosted and source-bound immutable evidence laws remain.

## 10. Closeout

Only `docs/parity/UPSTREAM_RECONCILIATION_8C89AA9.md` is authored by this bounded
task. No tests, commits, candidate changes, reference edits, historical inventory
rewrites, or parity-completion claims are made. The evidence is an immutable
source comparison plus named, inspected executable oracle definitions; execution
and acceptance remain separate future work under the existing gates.
