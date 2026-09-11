# ADR 0003: One mission coordinate, one authority family per mission

- Status: proposed
- Date: 2026-09-11
- Scope: mission/readiness authority aggregate composition; entry input to M3 (production execution)
- Evidence basis: live production-host session on this repository at `24d9e2fe` (2026-09-11); scratch artifacts `/tmp/go-kitty3-spin`
- Predecessor: [ADR 0002](0002-production-orchestration-architecture.md)

## Context

The M2 production host stores mission authority in GapDB. Two state families
write events to the **same aggregate coordinate** — `("mission", missionID)`
scoped to `(projectID, missionID)`:

| Family | State type | Payload `schema_version` | Event type namespace | Canonical state schema |
|---|---|---|---|---|
| mission authority | `MissionAuthorityStateV2` | `1` | `mission_created`, `mission_completed`, … | status/step/version fields (`missionAuthorityStateWireV2`) |
| readiness authority | `ReadinessAuthorityStateV2` | `2` | `readiness_created`, `readiness_completed`, … | lifecycle/activity_count/active_facts (`readinessAuthorityStateWireV2`) |

One reducer dispatches on the payload's `schema_version`
(`ReduceMissionEventV2`, `internal/mission/authority_v2.go`), so both families
interleave legally on one event stream. The aggregate head record, however,
carries a **single authority summary** — the canonical bytes of whichever
family's state was committed last — and each family's observer decodes that
summary with its own strict decoder:

- `ObserveMission` → `DecodeMissionAuthorityStateV2(head.AuthoritySummary())`
- `ObserveReadinessAuthority` → `DecodeReadinessAuthorityStateV2(head.AuthoritySummary())`

Consequence observed live (2026-09-11, production sidecar
`/tmp/spec-kitty-gapdb-assertion-sidecar/gapdbd`, SHA verified against
`m1-assertion-extension-admission.json`): after committing a mission-family
`mission.create` + `mission.complete` pair on one mission ID, every
readiness-family observer of that coordinate failed closed. The host reported
`PROGRESS_INCOMPLETE / action_census` from the write session and
`STARTUP_REFUSED / observation_coordinate` from fresh observer sessions, because
the head summary was a mission-family state that the readiness decoder rejects.
The behavior is fail-closed by design — no state corruption occurs — but the
mixed-family composition is unobservable, and nothing in the frozen product-law
documents states the composition rule.

The same live run confirmed the enforcement backstop: a readiness-family
`readiness_created` command against a mission ID already owned by a
mission-family state is refused by the CAS layer (prior-state conflict), because
both families require a zero prior for their create events on the same
coordinate.

## Decision

1. **One coordinate, one family.** A mission's authority history is written by
   exactly one family for its whole lifecycle. Mixing families on one mission
   ID is invalid composition. The CAS laws (zero-prior create, digest-bound
   expected states) enforce this mechanically once the first family commits;
   this ADR makes the invariant explicit instead of emergent.
2. **The readiness family is the production lifecycle surface.** Progress, the
   action census, and drive gating read the readiness authority summary
   (`authorityActionCensusSource` → `ObserveReadinessAuthority`). Production
   missions — including all M3 real-agent composition — create and evolve
   missions through readiness-family operations (`kind: "readiness"`).
3. **The mission family is the bare CAS surface.** It remains the qualified
   M2 campaign and audit surface (its laws were the M2 falsification target).
   It is not the composition path for real missions.
4. **Family is an explicit request field, not ambient state.** `OperationKind`
   (`mission` vs `readiness`) already selects the decode path in
   `internal/helpercmd/request.go`. Clients — including the M3 dispatch layer
   and the M5 API — must name the family on every operation; no implicit
   fallback between families is permitted.
5. **Test convention.** Real-host scenarios that exercise both families use
   distinct mission IDs per family (the
   `TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation`
   convention). A scenario that *intends* to prove the mixed-family refusal may
   share an ID, but only under that stated intent.

## Consequences

- M3's composition root routes the production mission lifecycle through
  readiness-family commands exclusively; mission-family commands stay on
  qualification/audit paths. Drive plans, run capsules, and the future API 2.0
  surface carry the family in the request envelope (`OperationKind`), so the
  contract is wire-visible.
- Progress and drive gating remain undefined ("incomplete" → fail-closed) for
  mission-family-only stores. This is the pre-M5 observation limitation, not a
  defect; M5's progress contract will be defined against the readiness family.
- Optional hardening (M3 candidate, non-blocking): an architectural battery or
  lint asserting no production path composes a readiness-family observation
  with a mission-family write on the same aggregate in one flow.
- Documentation-only change at admission; no code behavior changes.

## Alternatives considered

- **Separate aggregate coordinates per family** (e.g. `readiness:<id>` distinct
  from `mission:<id>`): rejected — both families describe the same governed
  mission, and separate coordinates would allow the two authorities to diverge
  silently, recreating the dual-truth class M2 eliminated.
- **Single unified state schema**: rejected for M2's scope; the M2
  qualification froze the mission-family wire contract as its exact evidence
  tuple, and the readiness family's richer lifecycle (activity, facts,
  work-revision selection) is a superset that would have changed the frozen
  receipt.
- **Ambient family selection** (infer family from action string): rejected —
  both families share the `mission.create`/`mission.complete` action IDs by
  design; the family is disambiguated only by `OperationKind`, so the request
  envelope must carry it explicitly.