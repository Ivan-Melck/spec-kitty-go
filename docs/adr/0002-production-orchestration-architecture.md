# ADR 0002: Production orchestration architecture

- Status: proposed for ratification
- Date: 2026-08-29
- Scope: first production runtime with one machine and one local-filesystem authority
- Canonical record: [Production Orchestration Architecture](../PRODUCTION_ORCHESTRATION_ARCHITECTURE.md)

## Context

Phase 1–3 qualified valuable laws at exact commits: immutable executable work,
isolated concurrent workspaces, stale-authority refusal, ActorID-level review
independence, candidate-bound evidence, and serialized Git integration. The
current evidence also qualifies SQLite only within those historical boundaries,
the Beads 1.2.2 adapter only at its pinned tuple, and a proving/helper-oriented
mission composition rather than a production host.

Active design guidance still mixes current SQLite qualification, Python-shaped
lane and filesystem representations, incomplete API 1.3 projection, and later
production phases. Without one new canonical record, persistence, API,
acceptance, and transport work could create competing authorities or claim
unimplemented behavior as shipped.

## Decision

Adopt the linked architecture as the sole normative production recommendation.
In particular:

1. One host-owned authority runtime serves active state from memory and reports
   authority-changing success only after one durable atomic control mutation.
2. GapDB is the sole target production control-state substrate. The first proof
   uses a pinned project-local `gapdbd` sidecar over its public Unix socket;
   Spec Kitty does not import GapDB's internal owner lifecycle.
3. GapDB WAL/snapshot recovery, the Spec Kitty control journal and mission
   checkpoint, runtime-epoch replacement, external reconciliation, and
   side-effect-free playback are separate contracts.
4. A replacement lease receives a fresh workspace. A fencing token cannot stop
   POSIX writes, so an expired workspace is quarantined and never transferred.
5. `ActorID`, not `ProcessID`, is the default review-independence boundary.
6. Beads owns mutable work definition and discussion; Spec Kitty owns mission,
   execution, review, acceptance, and integration decisions; Git owns code and
   ref facts; GapDB supplies ordered storage, not product semantics.
7. Orchestrator API 2.0 is the typed process-complete application seam. CLI,
   hosted, automation, dashboard, and optional transports project the same
   capabilities. Callers cannot use generic transitions, mint authority, open
   GapDB, or mutate host Git/workspaces directly.
8. Acceptance is stack-neutral: claims, revision-pinned qualification bindings,
   evidence envelopes, and policy determine the gate. Ecosystem commands stay
   in providers.
9. MCP `2026-07-28` is optional and independently packaged; MCP Tasks project
   native operations and never own mission or lease state.
10. Dependency, reachability, structural-absence, playback, and mutant guards
    make these boundaries executable.

All GapDB authority, API 2.0, neutral acceptance, production-host, and MCP
behavior described above is proposed. This ADR does not claim it ships.

## Consequences

### Positive

- Every state category has one writable authority and explicit projections.
- Memory-primary operation does not weaken durable recovery or explanation.
- Zombie writes are isolated by construction rather than by an impossible
  filesystem-fencing promise.
- Clients share one semantic application boundary.
- Projects can prove acceptance with local stacks without teaching the kernel
  ecosystem vocabulary.
- Historical Phase 1–3 evidence remains valid at its attested boundary.

### Costs and risks

- GapDB lifecycle, packaging, and response-loss behavior require a real external
  proving mission and signed adoption tuple.
- SQLite removal cannot begin from documentation alone and cannot retain a
  runtime fallback.
- Checkpoint, journal, outbox, and reconciliation schemas add explicit design
  work that a current-state-only model would avoid.
- Compatibility clients may receive typed refusals where legacy generic or
  lane-shaped operations cannot translate safely.
- Stack-neutral acceptance requires provider conformance and evidence trust
  policy rather than one repository command.

## Superseded active guidance

This ADR supersedes active future-production guidance that recommends SQLite as
the production control model, treats Python lane/worktree artifacts as canonical
state, makes API 1.3 the final application vocabulary, or implies core MCP or
ecosystem-specific acceptance. The dependent documentation migration will
amend `README.md` and `docs/TECHNICAL_PROJECT_PLAN.md` and publish exact
historical dispositions.

It does **not** supersede the factual verdicts in
[ADR 0001](0001-phase1-authority-proving-ground-verdict.md), the Phase 1–3
evidence payloads, the Python behavior inventory, or the mini-review resolution.
Those remain historical evidence at their exact source and scope.

## Non-goals

- Implement or adopt GapDB in this documentation mission.
- Remove SQLite code, dependencies, or historical evidence now.
- Implement API 2.0, progress watches, neutral acceptance, a production host,
  qualification providers, MCP, or a migration importer.
- Design shared-filesystem, replicated, or multi-machine store ownership.
- Select final performance thresholds or promise remote deployment semantics.
- Translate Python modules or command names into Go package recommendations.

## Proof obligations

1. M1 qualifies a pinned public `gapdbd`/client/protocol tuple, durable command
   batch, checkpoint restore, epoch invalidation, fresh replacement workspace,
   side-effect-free playback, and the real two-agent zombie scenario; a human
   signs the adoption record.
2. M2 proves every authority capability on GapDB and proves SQLite absent from
   production source, module graph/transitives, SBOM, binaries, installation,
   configuration, fallback, and migration paths.
3. M3 proves real-agent workspace isolation, ActorID review independence,
   dishonest/unavailable tracker recovery, attribution-safe compensation, and
   serialized conflict-preserving integration.
4. M4 proves one neutral gate across materially different providers, exact
   evidence freshness, missing-provider decisions, attributed waivers, and zero
   ecosystem dependency in the kernel.
5. M5 proves normal CLI/API parity, disconnect/restart resumability, revisioned
   progress, legal next actions, API 1.3 translation/refusal, and caller
   inability to manufacture authority.
6. Core release qualification proves sustained concurrency, arbitrary crash
   timing, clean install/restart/recovery, and absence of Python, SQLite, and MCP
   from the core installation.
7. Any optional MCP release proves the core builds and operates without it and
   that task expiry, connection loss, input pauses, and cancellation races do
   not alter durable Spec Kitty authority.
