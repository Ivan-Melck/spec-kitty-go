# Post-approval defect 10: autonomous readiness replaces phase ceremony

## Finding

The external Spec Kitty 3.2.6 mission evaluation found that the machine layer is
strongest when it returns typed authority, evidence, idempotency, and recovery
facts, and weakest when a fixed prose-driven phase sequence owns progress. The
evaluation also exposed deterministic gates with no deterministic producer,
inapplicable web-stack artifacts required of a local CLI, derived state tracked
on concurrent lane branches, and commands whose own commits make the next
command refuse.

The M2 Go implementation avoided several Python defects, but the architectural
cause survived its first mission capability. `MissionSnapshot.CurrentStep`, the
mission-definition successor graph, and the single-action `next` evaluator make
`specify -> plan -> tasks -> implement -> review -> accept -> merge` an
authoritative state machine. That is ceremony, not product safety. It prevents
an autonomous driver from performing any currently safe useful action, makes
independent activities unnecessarily sequential, and invites presentation
prompts to become hidden protocol.

The desired invariant is not phase completion. It is that every mutation has
authority, every irreversible boundary has sufficient current evidence, and
every refusal names the exact missing or stale predicate and its producer.

## Ratified correction

### Capability readiness, not an exclusive phase cursor

Mission authority retains only coarse lifecycle facts such as open, paused,
failed, canceled, and completed. It records immutable activity history, current
artifact and WorkRevision authority, blockers, decisions, policies, and
terminal outcomes. No `CurrentStep`, successor edge, prompt phase, Markdown
heading, or caller-asserted stage may grant or withhold a capability.

For one exact mission projection revision, the kernel computes a complete
action census:

```text
ActionCensus {
  authorized[]
  unavailable[] {
    action
    predicates[] {
      kind
      authority
      evidence_identity
      responsible_capability
      recovery
    }
  }
  decisions_required[]
  conflicts[]
  terminal
}
```

Multiple authorized actions may coexist. A scheduler may execute independent
actions concurrently. Re-evaluation follows every durable publication; it does
not require a manual phase-advance event. `next` is a presentation convenience
over this census, never a single authoritative cursor.

### Safety gates remain first-class

The correction removes ceremony, not safety. A capability still refuses when
its exact safety predicates are false, including unresolved decisions that
materially affect the action, absent or stale artifact/workgraph authority,
missing execution lease or lease-scoped workspace, ineligible candidate or
independent review, failed project-defined acceptance, repository conflict,
stale epoch, ambiguous external effect, or explicit human policy for an
irreversible/high-risk action.

Acceptance is a durable decision derived from current evidence. It may be
requested whenever its predicates are satisfied; it is not a final ritual
phase. Integration is separately serialized by repository/target authority.

Every blocking predicate must have a typed deterministic producer or an
explicit applicability policy. A system may not require an artifact merely
because a template mentioned it. Inapplicable evidence is represented as a
typed non-requirement, not fabricated as an empty directory or fake contract.

### One authoritative home for state

GapDB journal facts are mission authority. The memory-primary projection is a
disposable, replayable view. Git contains authored project artifacts and
immutable evidence intended for version control; it does not carry mutable
runtime status, an append-only control event log, or a second derived mission
state on lane branches.

No runtime component may reconstruct authority by parsing Markdown, prompts,
human renderers, Git commit prose, or tracker prose. Acceptance matrices,
dashboards, status files, and human-readable reports are projections. Editing a
projection cannot mint authority.

### Machine contract and prompt contract

Every command result is typed and bounded. It declares exact idempotency
binding, mutation/evidence membership, replay safety, current projection
revision, action census, and the next likely blocking predicate. Successful
batch operations report a per-artifact result; a skipped or refused member may
not disappear from a success summary.

Agent-facing guidance labels statements as `invariant`, `default`, or
`example`. Only invariants can block. Defaults are overridable policy and
examples never become gates. Human prose is generated from typed contracts and
is never parsed back into machine meaning.

### Project-owned verification

The application is language- and stack-neutral. Project policy supplies typed
verifier adapters and applicability predicates. The kernel records invocation,
inputs, environment identity, bounded outputs, and verdict evidence; it does
not hardcode `src/`, `tests/`, `contracts/`, Django, SaaS, REST, GraphQL, a test
database, grep-based semantics, or any language-specific command.

An unavailable verifier is distinct from a failed verifier. An absence check
that could not inspect its declared scope is `verification_error`, never
`confirmed_absent`.

### Autonomous driving and optional surfaces

A local driver repeatedly queries the action census, schedules safe independent
actions, records results, and re-evaluates until it reaches a decision,
conflict, typed external dependency, policy-mandated human boundary, or terminal
outcome. CLI, Orchestrator API, and optional stateless MCP—including MCP Tasks—
project this same contract. None is a second workflow engine, and users are not
required to adopt MCP.

## Routing

1. WP10 implements acceptance as an evidence-readiness capability callable
   whenever its exact predicates hold. It must not read or advance a mission
   phase and must refuse inapplicable or producerless criteria.
2. A new WP18, after WP10 and before WP11, replaces the exclusive mission-step
   protocol with the capability census, activity history, coarse lifecycle,
   applicability, and autonomous-drive contracts. It preserves durable audit,
   decisions, blockers, replay, and typed refusal ordering.
3. WP11 projects the complete authorized/unavailable action census and proves
   query views cannot become authority.
4. WP13 exposes memory-primary mission progress as readiness, running work,
   blockers, decisions, conflicts, and evidence—not as a current phase.
5. WP14 treats Python ceremony, tracked derived lane state, prompt-only
   producers, and stack assumptions as intentional non-parity while preserving
   the strong machine patterns enumerated by the evaluation.
6. WP15 provides `drive`, status, query, and operation-resume behavior over the
   typed host. It must not recreate a phase command ladder.
7. WP16 proves production has no SQLite fallback and no Git-tracked runtime
   status authority.
8. WP17 runs the crown campaign through autonomous readiness with restarts and
   faults at every authority/publication boundary, not merely between phases.

## Acceptance evidence

- An objective-only two-item mission can be driven from creation to terminal
  outcome without a caller manually advancing phases.
- Two independent authorized actions are both visible and may run concurrently;
  one does not disappear behind an arbitrary successor choice.
- Reordering otherwise independent artifact authoring and verification does not
  refuse merely because a phase cursor disagrees.
- A local CLI project with no network contract is never asked to produce a
  `contracts/` artifact, and no empty or fabricated artifact improves readiness.
- Every unavailable action identifies typed blocking predicates, evidence
  identity where applicable, the responsible producer, and recovery.
- Project verification is selected by declared applicability and typed adapter
  contract; no language or framework path is assumed by the kernel.
- Deleting all in-memory progress and all generated human status views yields
  byte-identical readiness after GapDB checkpoint-plus-suffix replay.
- Concurrent lane branches do not track or merge mutable runtime status or
  control event logs.
- CLI, API, and optional MCP projections agree on action identities,
  authorization, blocking reasons, idempotency, and terminal facts.
- Controlled mutants that restore a single authoritative step, parse prose,
  accept an edited projection, require an inapplicable artifact, or hide a
  skipped batch member fail the intended semantic contract.
