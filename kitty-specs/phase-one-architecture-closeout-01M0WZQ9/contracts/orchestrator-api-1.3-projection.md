# Contract Study: Orchestrator API 1.3 Projection

## Sources and target

Target contract: canonical Python Spec Kitty `CONTRACT_VERSION=1.3.0`, cross-checked against Nano Kitty's strict native consumer. The donor Go port's 1.1 adapter is historical context only.

The future adapter must emit the canonical seven-field JSON envelope, validate exact command identity/version/correlation/timestamp, reject unknown or duplicate JSON fields, bound stdout/stderr, and never parse human terminal output.

## Command mapping

| API 1.3 command | Redesign capability | Projection status | Required translation or gap |
|---|---|---|---|
| `contract-version` | future transport composition | Direct | Static adapter metadata; no kernel authority. |
| `mission-state` | WorkRevision plus future mission engine | Deferred | Current kernel has work/run authority but no mission aggregate/lane projection. |
| `list-ready` | WorkRevision dependency graph | Projectable with translation | Readiness is computed from the pinned graph, never tracker status. Mission-level aggregation remains deferred. |
| `resolve-workspace` | workspace inspection by run/lease | Projectable with translation | Return an existing lease-owned path only; absence does not allocate. Replacement leases never inherit an expired workspace. |
| `start-implementation` | execution start saga | Projectable with translation | Host allocates lease and fresh workspace, records tracker projection safely, and returns typed authority. Legacy “reuse lane workspace” semantics must not weaken generation isolation. |
| `start-review` | review start/claim | Projectable with translation | Review actor must satisfy mission independence policy relative to implementation actor. |
| `transition` | typed handoff/review/integration commands | Partial/deferred | Do not expose a generic lane mutation. Translate only recognized commands and structured review results; full mission transitions await the mission engine. |
| `append-history` | future projection/event journal | Deferred | No canonical human activity-log capability exists yet. Must not write Markdown as authority. |
| `accept-mission` | future mission acceptance | Deferred | Current review acceptance is per candidate/work item; aggregate acceptance awaits mission engine. |
| `merge-mission` | serialized integration | Partial/deferred | Per-candidate integration exists; mission-wide ordered merge and final state await mission engine. |

## Identity translation

| API field | Redesign source |
|---|---|
| `mission_id` | portable MissionID |
| `mission_slug` | presentation metadata only |
| `mission_number` | display-only nullable projection |
| `wp_id` | WorkID external presentation |
| `actor` | ActorID plus external-subject translation |
| correlation | CorrelationID |
| operation identity/idempotency | OperationID and command request digest |
| workspace path | host-resolved WorkspaceID record, never caller-composed |

## Error translation

The adapter maps typed diagnostics to stable API codes. It must preserve retry and “may have applied” semantics internally even when API 1.3 has a coarser error vocabulary. Prose messages remain advisory.

Important mappings include:

- invalid selector/identity → usage or not-found/ambiguous code;
- stale fence or generation → transition rejected/stale authority extension;
- conflicting active claim → `WP_ALREADY_CLAIMED`;
- missing handoff/review evidence → `WORKFLOW_EVIDENCE_REQUIRED`;
- repository preflight/ref mismatch → `PREFLIGHT_FAILED` or typed safe-commit code;
- contract skew → `CONTRACT_VERSION_MISMATCH`.

## Host-only boundary

External orchestrators may submit typed intent and non-secret policy metadata. They may not:

- open the control store;
- compute or transfer workspace ownership;
- mutate Git refs directly;
- supply fencing tokens as a substitute for host authority;
- infer readiness from Beads;
- smuggle credentials through policy JSON;
- use force as an ordinary transition mechanism.

## Design verdict

No current kernel type blocks eventual 1.3 projection. The missing pieces are deliberately absent mission-level aggregates and projections, not authority-model incompatibilities. The future API should therefore be an outer adapter over application commands, with explicit translations for legacy lane terminology.
