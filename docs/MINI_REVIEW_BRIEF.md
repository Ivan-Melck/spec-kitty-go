# Mini Review Brief: Spec Kitty Go Re-architecture

Status: Completed 2026-08-23. Retained as the original review prompt. See
`docs/MINI_REVIEW_RESOLUTION.md` for accepted findings and plan changes.

Please review `docs/TECHNICAL_PROJECT_PLAN.md` as an independent technical
critic.

This is a proposed Go re-architecture of Spec Kitty, not a source-level port.
Its central decisions are:

1. Preserve Spec Kitty's mission methodology, governance, implementation/review
   separation, lanes, worktrees, and controlled integration.
2. Use Beads as the default ticket substrate for work descriptions,
   dependencies, discussion, visible assignment, and acceptance text.
3. Keep Beads replaceable behind narrow work capabilities.
4. Keep Spec Kitty authoritative for mission transitions, execution leases,
   worktree authority, review verdicts, and merge behavior.
5. Separate durable actors, provider identities, and ephemeral processes.
6. Replace canonical `tasks.md` with a tracker-backed work graph and
   evidence-derived acceptance.
7. Coordinate the Spec Kitty control store, Beads, Git, and filesystem through
   durable sagas rather than pretending they share a transaction.
8. Keep Team Kitty, MCP, CLI, and the Orchestrator API outside the authority
   core.

Please look especially for:

- hidden leakage of Beads semantics into mission or execution policy;
- a regression from current Spec Kitty lane/worktree concurrency;
- double-claim, stale lease, fencing, crash-recovery, cleanup, or merge races;
- unclear or operationally unrealistic source-of-truth boundaries;
- needless complexity in the proposed SQLite + Beads + Git arrangement;
- weaknesses in `WorkRevision` finalization and post-finalization edit handling;
- acceptance/checklist behavior that still creates bookkeeping work for agents;
- important Python Spec Kitty behavior missing from migration or parity work;
- interfaces cut at the wrong boundary;
- scope that should be deferred from the first executable slice.

For each finding, please use:

```text
Severity: blocker | high | medium | low
Location: section or decision
Finding: what is wrong or underspecified
Failure scenario: how it breaks in practice
Recommendation: the smallest corrective change
```

Please finish with:

1. Does the plan preserve what Spec Kitty does better than ThickTicket and
   Beads?
2. Is Beads truly bounded as a default substrate rather than a hidden kernel
   dependency?
3. What must change before implementation begins?
4. What is the smallest executable experiment most likely to disprove the
   architecture?
