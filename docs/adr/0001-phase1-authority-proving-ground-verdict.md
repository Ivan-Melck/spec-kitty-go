# ADR 0001: Phase 1 authority proving-ground verdict

- Status: proposed ratification; source campaign passed, independent WP08 review pending
- Date: 2026-08-24
- Scope: one machine and one local filesystem authority
- Frozen source: `adf15aac42f3f03dd27ad6252df973bcd8b42433`
- Evidence: `docs/phase1-evidence/results.json`, `final-campaign-manifest.json`, and `scenario-catalog.json`

## Decision

Ratify the Phase 1 authority architecture inside the tested single-machine and
local-filesystem boundary, subject to independent WP08 review. The executable catalog has
complete evidence for 31 of 35 Phase 1 and WorkRevision scenarios. Four entries
are intentionally partial: P1-15 defers human-authorized import choices; P1-18
defers policy for superseding an already-recorded judgment; P1-21 defers
automatic rejection-to-rework-run creation; and P1-23 has executable C2
integration refusal but no Phase 1 acceptance/queue surfaces. The shipped
halves and owning boundaries are recorded exactly in `scenario-catalog.json`.
The independent pre-freeze audit cleared the frozen source. Its 65-shard,
100-iteration campaign and all full-suite, race, static-analysis, module,
vulnerability, formatting, diff, and build gates passed. The exact shard union
and raw-log hashes are recorded in `final-campaign-manifest.json`.

This is a provisional empirical architecture decision, not an adoption
decision for a production tracker, distributed control store, or remote shared
filesystem.

## Component verdicts

### Authority: ratify

The SR-00..SR-09 matrix recovers identical operations from durable facts. The
two-agent narrative expires generation 1, creates generation 2 without reusing
the mutable workspace, resumes the old process, and observes typed stale
authority refusals at the shipped handoff and review seams. Integration refuses
the unapproved stale candidate without publishing it. Each matrix fixture now
contains its own expired authority; the universal assertion derives exact
active-lease and workspace rows from inputs, pre-fault records, and returned
capsules, then runs refusing handoff, review, and integration attempts against
that same store/repository/tracker/workspace before every scenario-specific
assertion. Missing stale probes and unexpected well-formed rows are explicit
anti-vacuity failures.

P1-14 is complete under the actual authority boundaries. The fake tracker has
no execution lease and cannot grant execution, review, or integration
authority. An unapproved zombie publication is refused. An immutable approval
recorded before the source lease expires remains integrable afterward unless a
WorkRevision reconciliation invalidates it; integration uses its own
repository-and-target-ref mutation lease rather than the expired source lease.

### Lane and workspace separation: ratify

Concurrent agents receive distinct workspace paths, branches, Git directories,
indexes, runs, leases, and candidates while their edit intervals overlap.
Immutable binding triggers and recorded binding facts are checked after every
fault. An expired workspace is quarantined; the replacement receives a new
workspace; and a resumed zombie can change only its quarantined Git evidence.
P1-08's typed `decision_required` disposition with a `workspace_not_ready`
diagnostic is the contract-compatible physical-mismatch result: the artifact is
never reused or advertised ready. P1-15 proves exact output inspection and
preservation plus stale-publication and cleanup refusal; its human-authorized
import choices remain deliberately deferred.

### Review independence: ratify

HR-00..HR-07 replay preserves immutable handoff and review facts. The complete
narrative demonstrates that a new process with the implementing ActorID still
cannot approve its own handoff, while distinct reviewer ActorIDs can approve the
two candidates independently. Manual branch movement is refused, but P1-12
uses the portable `candidate_mismatch` kind with the typed
`owned_branch_moved` divergence. It atomically preserves product-owned
candidate and observed refs, persists one immutable refusal per OperationID,
replays after lease expiry and branch restoration, safely recreates a deleted
exact ref, and refuses rather than overwriting a mutated ref.

A newer immutable handoff makes a still-pending judgment for the older handoff
ineligible. Durable rejection and its failed criterion are retained. The
follow-on policy that supersedes an already-recorded judgment and automatically
creates or reopens a rework run remains owned by the review workflow and is not
claimed by this ADR.

### Integration: ratify

IG-00..IG-09 recover from real SQLite and Git facts. A detached managed
integration workspace leaves the primary checkout unchanged. Concurrent
integration attempts for one `(RepositoryID, TargetRef)` yield one authority;
the loser receives a stable busy diagnostic and later integrates from the first
result. Manual target movement becomes the observed preflight target. A real
merge conflict leaves the target unmoved and preserves the candidate, conflict
workspace, file list, and repair action.

A candidate approved under the live source lease remains integrable after that
lease expires because acceptance is immutable and integration obtains separate
repository-first target authority. Tracker assignment is unchanged by this
path and supplies no hidden authority.

Moving the owned branch from approved C to C2 does not transfer C's approval:
an integration request naming C2 is refused and leaves the target unchanged.
Because Phase 1 exposes no separate acceptance or queue command, those two
negative halves remain explicit catalog limitations rather than inferred
passes.

Executable WorkRevision consumers prove durable `continue`, `supersede`,
`invalidate`, and `fork` consequences across handoff, workspace replacement,
execution, review, and integration, including preservation of integrated
history and explicit follow-up lineage.

## Consequences

Continued implementation may rely on durable local leases,
lease-scoped workspaces, ActorID-based review independence, repository-first
Git fencing,
and serialized detached integration. Those laws must remain provider-neutral;
the production dependency/source test prevents Beads, Team Kitty, API, MCP,
Python, or Markdown compatibility concerns from entering this kernel.

Remote agents must cross an orchestrator boundary. They must not open the local
SQLite database or assume local filesystem ownership. A future distributed or
shared-filesystem design requires a new ADR and a new proving ground.

## Observed limitations

- Linux/POSIX process stop and resume are used for the zombie scenario.
- Git 2.43.0 and modernc SQLite 1.57.0 are the observed implementations.
- The fake tracker proves the portable provider contract, not a hosted
  provider's availability or consistency model.
- Named helper exits model process death; power loss, disk loss, kernel failure,
  and network partitions are outside the experiment.
- Repetition establishes absence of observed failures, not a mathematical proof
  and not production migration approval.
- The first 100-iteration campaign is non-admissible: a preliminary audit
  superseded its frozen source after 46 of 47 planned shards completed. The
  runner attempts and exact completed union remain recorded in `results.json`.
- The final campaign ran every discovered `Phase1` top-level test through a
  documented exact, disjoint 65-shard union. Each shard completed 100
  iterations with no reruns; full test and race suites, vet, staticcheck,
  formatting, diff-check, module verification, vulnerability scan, and binary
  builds passed on the same frozen source.
- This is the implementation verdict. Independent WP08 review remains the
  governance gate before the work package is accepted.
