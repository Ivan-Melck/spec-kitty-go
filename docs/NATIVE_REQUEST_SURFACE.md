# Native Operation Request Surface (M3 client contract)

- Status: proposed; entry input to M3
- Date: 2026-09-11
- Scope: constructing legal operation requests against the production GapDB host from outside the Go module
- Evidence basis: live session 2026-09-11 at `24d9e2fe`; construction derived from `internal/helpercmd/runner_test.go` and `internal/helpercmd/production_real_test.go`
- Companion: [ADR 0003](adr/0003-mission-coordinate-authority-family.md) (family selection)

## Command surface

```
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] initialize
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] status
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] progress --project ID --mission ID
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation inspect --project ID --mission ID --operation ID
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation execute --request FILE   # single OperationRequest JSON
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation resume  --request FILE
spec-kitty --control-root ABSOLUTE_PATH [--output json|human] drive --plan FILE                  # DrivePlan JSON
```

- `-` reads stdin. `--output json` is the machine envelope; `human` is for operators.
- Backend selection, waivers, and importers are intentionally NOT CLI options.

## Envelope (machine output)

`schema_version`, `command`, `outcome` (`success|stopped|refused|unavailable|invalid|conflict`), `database_id`,
`runtime_epoch_id`, `projection_revision`, plus `host` (status/progress), `result` (typed),
`drive` (steps + stop_reason), `diagnostic {kind, field, retry_safety, recovery_actions}`.
Exit codes map to outcome classes (0 success; >0 refused/conflict/unavailable/invalid).

Drive stop reasons: `action_not_authorized`, `mission_progress_unavailable`, `operation_failed`, `plan_exhausted`.
`plan_exhausted` with a committed step is **success with a stopped envelope** — plans do not self-extend;
emit the next plan from the observed state.

## DrivePlan / OperationRequest wire

```json
{
  "schema_version": 1,
  "operations": [
    {
      "schema_version": 1,
      "action": "mission.create",
      "scope": "mission:<missionID>",
      "kind": "readiness",
      "payload": { ...family-specific command wire... }
    }
  ]
}
```

- `schema_version` = `1` at both levels (const `RequestSchemaVersion`).
- `action` ≤128 chars, from the capability action set (`mission.create`, `mission.complete`, `mission.pause`,
  `mission.resume`, `mission.fail`, `mission.cancel`, `execution.claim`, `execution.heartbeat`,
  `workspace.allocate`, `workspace.quarantine`, `candidate.handoff`, `review.record`, `acceptance.evaluate`,
  `integration.publish`, `effect.dispatch`, `effect.reconcile`, …).
- `scope` ≤256 chars, `"<scope_kind>:<identity>"`, and MUST equal the scope derived from the decoded payload
  (mismatch = `INVALID_COMMAND` before any execution).
- `kind` selects the decode path; see ADR 0003 for family law. Both mission and readiness families use the
  `mission.create`/`mission.complete` action IDs; `kind` is the only family discriminator.
- `payload` must be **recursively snake_case** and **strictly decodable** (unknown fields rejected), then
  canonicalized (`journalv2.CanonicalObject`) before digesting.

## Drive scheduling semantics (what a client may rely on)

- Waves: every iteration, the scheduler reads `progress` for each pending (project, mission, scope), selects
  operations whose action+scope is `authorized` in the returned action census, executes the ready set **concurrently**,
  then repeats until no candidate is ready.
- First `mission.create` on a fresh control root is authorized by the progress-not-found escape.
- Any failed step stops the plan (`operation_failed`); later waves never run.
- `terminal` in the drive result = every touched mission is in a terminal lifecycle.

## Readiness-family command wire (production lifecycle family)

```json
{
  "schema_version": 2,
  "operation_id": "op-…", "project_id": "…", "mission_id": "…",
  "actor_id": "…", "process_id": "…",
  "runtime_epoch_id": "current",
  "doctrine_revision": "…", "profile_revision": "…", "policy_revision": "…",
  "observed_at": "2026-09-11T18:10:02Z",
  "event_type": "readiness_created",
  "expected": null,
  "updated": { "schema_version": 2, "project_id": "…", "mission_id": "…",
               "definition_revision": "…", "definition_sha256": "<64-hex>",
               "version": 1, "lifecycle": "open", "activity_count": 0,
               "active_facts": null, "last_operation_id": "op-…",
               "observed_at": "2026-09-11T18:10:02Z" }
}
```

- `runtime_epoch_id: "current"` is the only native first-attempt epoch selector; the host stamps the real epoch.
- `observed_at` must be RFC3339Nano UTC canonical (`…Z`, round-trip exact; `+00:00` is rejected).
- Identity strings: `[A-Za-z0-9]` first char, then also `. _ : -`; ≤ `MaxIdentityBytes`.
- `readiness_created` requires `expected` absent/zero, `version: 1`, `lifecycle: "open"`,
  `activity_count: 0`, no active facts, no current work revision.
- Terminal events (`readiness_completed|failed|canceled`) require `expected` = the prior canonical state
  (exact bytes), same definition identity, `version = prior.version + 1`, same lifecycle-authority fields.
- The `updated`/`successor` state must carry `last_operation_id` = the operation ID of this very command.
- Definition digest is client-computed (`journalv2.ReducerStateDigestV2` over the canonical state); use the
  same `(definition_revision, definition_sha256)` pair across every event of one mission.

## Mission-family command wire (bare CAS/audit family — see ADR 0003)

Same shape with `"schema_version": 1`, `event_type: mission_created|mission_completed|…`, `successor` instead of
`updated`, state schema `1` with `status/step/decomposition/...` fields. Create requires zero prior;
`mission_completed` requires expected = prior canonical bytes, `status: completed`, same-base fields
(no blocker/decision/effect churn), `observed_at >= prior.observed_at`.

## Progress, census, and gating (what "authorized" means)

`progress` returns the mission's durable projection plus, when the readiness authority is observable, the
**action census**: for every capability action, status + blocking predicates + recovery hints. Drive gates
on `status == "authorized"`. The census is derived — it is query authority only; it cannot mint operations,
leases, epochs, or effects.

Known composition boundary (ADR 0003): a coordinate whose head summary is mission-family state yields
`PROGRESS_INCOMPLETE / action_census` from the write session and `STARTUP_REFUSED / observation_coordinate`
from fresh observer processes. Route production lifecycles through the readiness family.

## Idempotency, conflicts, epochs

- `operation_id` is the idempotency key. Replaying a resolved operation returns its recorded typed result.
- Digest-bound CAS: a command whose `expected` state digest ≠ committed digest is refused
  (`VERSION_CONFLICT`, `retry_safety: same_operation_id` — fix the expected state, keep the operation ID).
- Epoch rotation: every write process admits a fresh epoch; stale-epoch commands are refused
  (`STALE_EPOCH`). Read-only sessions replay the journal and verify the durable `runtime/current`
  coordinate before serving.

## Typed refusal taxonomy (stable diagnostics)

`INVALID_COMMAND` (shape/canonicality), `VERSION_CONFLICT` (CAS),
`IDEMPOTENCY_CONFLICT`, `AMBIGUOUS` (restart-safe resolution required), `STALE_EPOCH`,
`NOT_FOUND`, `TERMINAL`, `STARTUP_REFUSED` (observer coordinate), `PROGRESS_INCOMPLETE`,
`COMMAND_UNAVAILABLE`, plus domain kinds (`stale_authority`, `evidence_unavailable`) on the
execution/integration paths. Every diagnostic carries `retry_safety` — honor it; it is the contract for
what may be retried automatically.

## Environment for the production host

```
SPEC_KITTY_GAPDBD_PATH      # the admitted sidecar binary (SHA-pinned in the adoption record)
SPEC_KITTY_EVIDENCE_ROOT    # dir holding m1-adoption.json + m1-assertion-extension-{admission,adoption}.json
SPEC_KITTY_RUNTIME_ROOT     # private runtime dir (created 0700; outside git worktrees)
```

## Reference implementation

`cmd/spk-drivegen/` (scratch, currently uncommitted in this tree) mints legal
create/complete plans for both families end-to-end; `internal/helpercmd/runner_test.go` remains the
authoritative behavior spec. Proposed M3 follow-up: promote the request-minting path into a supported
library or CLI so real agents do not reconstruct wire shapes from test code.