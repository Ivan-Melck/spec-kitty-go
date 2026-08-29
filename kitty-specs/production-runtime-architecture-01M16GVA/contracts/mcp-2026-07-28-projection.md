# Optional MCP 2026-07-28 Projection

## Status

Proposed optional adapter contract. The core application, CLI, and qualification suite must build and operate without this module or any MCP dependency.

The adapter targets the stateless MCP `2026-07-28` core and the independently negotiated `io.modelcontextprotocol/tasks` extension. A protocol request is self-describing; no MCP session is a source of mission, lease, review, or integration authority.

## Package boundary

```text
MCP transport and protocol SDK
        |
        v
optional MCP adapter
        |
        v
Orchestrator API 2.0 client
        |
        v
host-owned Spec Kitty application
```

The dependency arrow never reverses. The adapter may know API 2.0; the application and kernel may not import the adapter or MCP SDK.

## Discovery

The adapter publishes tools and resources derived from API 2.0 capability discovery and current caller authorization. It does not advertise commands the host cannot perform. Tool schemas use opaque identities and typed objects, never working-directory inference or shell command strings.

Representative tools:

- mission create/inspect/amend;
- work finalize/reconcile/claim;
- run heartbeat/report/handoff;
- review start/verdict;
- qualification evaluate and acceptance request;
- integration request;
- decision answer; and
- operation cancel.

Representative resources:

- mission progress and graph;
- active runs and outstanding decisions;
- candidate/review/acceptance/integration summaries;
- operation state; and
- health and recovery state.

Subscriptions/listen resume from an explicit application revision. MCP delivery state is not the canonical cursor.

## Tasks mapping

When the Tasks extension is negotiated, a task-augmented tool call creates or references one native API 2.0 operation.

| MCP concept | Native meaning |
|---|---|
| task ID | opaque adapter handle to `NativeOperationID` |
| `working` | accepted/working; cancellation request may be described in metadata |
| `input_required` | durable typed decision awaiting an authorized answer |
| `completed` | native terminal success with typed tool result |
| `failed` | execution failure; a valid domain refusal may instead be a successful typed diagnostic |
| `cancelled` | application confirmed cooperative cancellation |
| TTL | transport retrieval lifetime only |
| poll interval | advisory pacing only |

The mapping is deliberately not mission-shaped: one mission can outlive many Tasks, and a Task can represent only one bounded native operation.

## Cancellation and loss

- `tasks/cancel` records cancellation intent through API 2.0.
- Cancellation never directly expires a lease, deletes a workspace, reverses Git, clears a tracker assignment, or erases evidence.
- Completion may win a cancellation race and must be reported honestly.
- Task expiry or adapter restart may lose a transport handle; the native operation and mission remain queryable by native identity subject to authorization.
- Compensating an external effect requires the same attribution-safe comparison as any non-MCP caller.

## Input and MRTR

Model-runtime requests and task input are transport mechanisms for an already-recorded native `DecisionID`. The adapter:

- exposes bounded typed choices and context;
- records the responding actor and authorization evidence;
- rejects unrecognized, expired, or already-resolved decisions; and
- cannot convert free-form model output into authority without the application validating it.

## Privacy and resource bounds

Responses are bounded projections and artifact references. Secrets, raw environment dumps, full transcripts, and unbounded logs are not embedded in task metadata or progress notifications. Caller authorization is re-evaluated for every stateless request.

## Qualification

The adapter qualifies only after API 2.0 is stable. Conformance must prove:

- core build and normal CLI work with the MCP module absent;
- tool calls and native clients yield equivalent application results;
- request replay is safe and connection loss does not imply cancellation;
- Tasks map to native operations without identity collapse;
- subscription reset and backpressure are bounded;
- an adapter cannot mint evidence or authority; and
- cancellation, TTL, or task deletion cannot mutate mission history.

Primary references:

- [MCP 2026-07-28 announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28/)
- [MCP Tasks extension draft](https://tasks.extensions.modelcontextprotocol.io/specification/draft/tasks)

