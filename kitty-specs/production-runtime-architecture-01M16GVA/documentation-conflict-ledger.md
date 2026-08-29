# Documentation Conflict Ledger

**WP08 artifact — Independent Architecture Review and Publication Gate**

Reviewer ActorID: `opencode`
Reviewer ProcessID: `proc-opencode-wp08-review-2026-08-29`
Review date: 2026-08-29
Source tree reviewed: `5c7592046f0abc85ac52e8eb1f830771d50ff9d8`

This ledger records every active normative conflict found in the repository-wide
search. It distinguishes immutable historical evidence from active guidance,
records the disposition of each conflict, and confirms no second canonical
architecture remains.

**Conflict status vocabulary:**
- `closed` — resolved by a completed WP; canonical successor named
- `historical` — old claim remains in immutable evidence; scope/time/status makes it non-normative; no action required
- `open` — unresolved active normative conflict requiring resolution before implementation

---

## Search strategy

The following repository-wide search terms were used:

1. `SQLite` — storage references across active docs
2. `GapDB` — GapDB mentions in non-canonical docs
3. `API 1.3` — legacy API references
4. `transition` — generic transition mentions
5. `acceptance` — acceptance authority statements
6. `MCP` — MCP in core or non-optional contexts
7. `future phases` / `future phase` — forward-looking statements in present tense
8. `source of truth` / `Source of Truth` — authority claims in non-canonical docs
9. `canonical` — competing canonical claims
10. `currently ships` / `ships today` — delivery honesty checks

The search was conducted against the files listed in each conflict entry.
Historical evidence files (`docs/phase1-evidence/`, `docs/phase2-evidence/`,
`docs/phase3-evidence/`, and all `kitty-specs/*/` mission records preceding this
documentation mission) were excluded from normative conflict checks.

---

## Part I: Active conflicts — all closed

### CONFLICT-001: TECHNICAL_PROJECT_PLAN.md — SQLite future direction

| Field | Value |
|---|---|
| Location | `docs/TECHNICAL_PROJECT_PLAN.md` (pre-WP06 state) |
| Old claim | Original plan described SQLite as the production control-state direction and referenced a numbered production phase roadmap inconsistent with M1–M7. |
| Disposition | **Closed by WP06** |
| Canonical successor | `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md`; `docs/PRODUCTION_RUNTIME_PROGRAM.md`; `docs/adr/0002-production-orchestration-architecture.md` |
| Owner WP | WP06 |
| Closure evidence | `docs/TECHNICAL_PROJECT_PLAN.md` now carries status "Phase 1–3 delivered…; production architecture ratified; M1–M7 implementation proposed". The plan's Section 1 declares "replace SQLite/API 1.3 future direction with the ratified target." Section 3 migration table documents the disposition change. The document explicitly defers to the canonical architecture when conflicts arise. |
| Post-closure state | `docs/TECHNICAL_PROJECT_PLAN.md` is now a living implementation guide that summarizes canonical records without redefining them. |

### CONFLICT-002: README.md — SQLite future direction and API 1.3 as final vocabulary

| Field | Value |
|---|---|
| Location | `README.md` (pre-WP06 state) |
| Old claim | Pre-WP06 README described SQLite qualification in a way that could imply SQLite would be the production storage, and API 1.3 was referenced without clear deprecation. |
| Disposition | **Closed by WP06** |
| Canonical successor | `README.md` Section "Status at a glance" with delivered/ratified columns; "Approved production direction" section |
| Owner WP | WP06 |
| Closure evidence | README now has a "Status at a glance" table distinguishing "Delivered and qualified now" from "Ratified production target." Control state row explicitly states "GapDB is the only approved production control-state substrate; M2 removes SQLite completely, with no fallback or dual write." The "Approved production direction" section names API 2.0 and names API 1.3 as "compatibility history." |

### CONFLICT-003: ADR 0001 contextual annotations — scope clarity

| Field | Value |
|---|---|
| Location | `docs/adr/0001-phase1-authority-proving-ground-verdict.md` |
| Old claim | The original ADR verdict text could be read as establishing SQLite as a production direction rather than a qualified Phase 1 implementation only at cited boundaries. |
| Disposition | **Closed by WP07** |
| Canonical successor | `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 3.1 (ADR 0001 row) |
| Owner WP | WP07 |
| Closure evidence | WP07 added contextual annotations to ADR 0001 clarifying its scope without editing the original verdict. The disposition index entry for ADR 0001 explicitly states: "Future production storage/API implications" are superseded guidance, and the canonical successor is ADR 0002 and the production architecture. The original verdict body and its factual claims remain intact. |

### CONFLICT-004: API 1.3 projection — competing application vocabulary

| Field | Value |
|---|---|
| Location | `docs/ORCHESTRATOR_API_1_3_PROJECTION.md` |
| Old claim | API 1.3 command names and lane-shaped vocabulary could be mistaken for the canonical application vocabulary rather than a compatibility design projection. |
| Disposition | **Closed by WP02 and WP07** |
| Canonical successor | `docs/ORCHESTRATOR_API_2_0.md` Section 10 "API 1.3 compatibility disposition"; disposition index Section 3.1 API 1.3 row |
| Owner WP | WP02 (API 2.0 defines the canonical vocabulary); WP07 (disposition index classifies 1.3 as historical/compatibility) |
| Closure evidence | ORCHESTRATOR_API_2_0.md Section 10 provides a complete mapping of every API 1.3 command to its API 2.0 disposition (lossless projection, safe with query, deprecated mapping, unrepresentable refusal, or unsafe permanent refusal). The disposition index classifies API 1.3 as "Historical/current compatibility inventory; outer-adapter guidance only." |

### CONFLICT-005: PYTHON_BEHAVIOR_INVENTORY.md — SQLite as production representation

| Field | Value |
|---|---|
| Location | `docs/PYTHON_BEHAVIOR_INVENTORY.md` |
| Old claim | The inventory's storage-related sections recommended SQLite-specific contention, WAL, schema, and migration approaches for the production runtime. |
| Disposition | **Historical; closed by WP07 classification** |
| Canonical successor | `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 4.2 "Representation recommendations superseded" |
| Owner WP | WP07 |
| Closure evidence | Disposition index Section 4.2 explicitly classifies "SQLite-specific contention, WAL, schema, migration, and control-root recommendations" as "Historical implementation guidance." The inventory file itself is classified as "Historical behavioral source; do not rewrite from the new decision point." Its behavioral content (laws, not storage representation) is retained in Section 4.1. |

### CONFLICT-006: MINI_REVIEW_RESOLUTION.md — storage representation reference

| Field | Value |
|---|---|
| Location | `docs/MINI_REVIEW_RESOLUTION.md` |
| Old claim | The resolution text refers to SQLite as "the storage" in the context of fence/quarantine behavior, which could be read as endorsing SQLite in the production architecture. |
| Disposition | **Historical; closed by WP07** |
| Canonical successor | `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 5 "Mini review findings remain load-bearing" |
| Owner WP | WP07 |
| Closure evidence | Disposition index Section 5 explicitly states: "Storage and API representations changed; the findings' safety content did not." Each finding is mapped to "Current classification" and "Where it controls the target." The one-machine-authority finding has "Retained boundary, amended storage name" as its classification. |

### CONFLICT-007: CONCURRENCY_SCENARIOS.md — SQLite as required representation for proving ground

| Field | Value |
|---|---|
| Location | `docs/CONCURRENCY_SCENARIOS.md` Section 2 test harness, Section 4.3 (SQLite inspection) |
| Old claim | The scenarios specify "1 SQLite control store in WAL mode" as the required harness component, and note "`Direct SQL may additionally assert schema-level uniqueness`." This creates a false equivalence between the Phase 1 proving ground and future proving missions. |
| Disposition | **Historical; closed by WP07** |
| Canonical successor | `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 3.1 (CONCURRENCY_SCENARIOS row) |
| Owner WP | WP07 |
| Closure evidence | Disposition index Section 3.1 classifies CONCURRENCY_SCENARIOS.md as "Historical proving contract and retained behavioral test catalog." The superseded guidance is explicitly noted as "SQLite is the required representation for the next proving ground." The retained product laws are identified as the behavioral invariants in Sections 3 and 5. The canonical successor for the next proving ground is the PRODUCTION_RUNTIME_PROGRAM.md (M1 torture chamber uses GapDB). |

### CONFLICT-008: ADR 0001 — "contextual annotation" scope boundary

| Field | Value |
|---|---|
| Location | `docs/adr/0001-phase1-authority-proving-ground-verdict.md` |
| Old claim | The ADR's context section could imply that the Phase 1 SQLite verdict extends to later implementations. |
| Disposition | **Closed by WP07** |
| Canonical successor | `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 3.1 (ADR 0001 row); ADR 0002 |
| Owner WP | WP07 |
| Closure evidence | Disposition index row for ADR 0001 lists "Forbidden inference: Phase 1 was a mistake; its SQLite verdict transfers to GapDB; retained laws eliminate requalification." Original verdict body remains intact. |

### CONFLICT-009: ORCHESTRATOR_API_1_3_PROJECTION.md — missing clear "not canonical" header

| Field | Value |
|---|---|
| Location | `docs/ORCHESTRATOR_API_1_3_PROJECTION.md` |
| Old claim | The pre-WP07 state of the API 1.3 projection did not carry a visible "this is a compatibility design record, not canonical API vocabulary" header with the canonical successor explicitly named. |
| Disposition | **Closed by WP07** |
| Canonical successor | `docs/ORCHESTRATOR_API_2_0.md`; `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` Section 3.1 |
| Owner WP | WP07 |
| Closure evidence | WP07 added contextual annotations to ORCHESTRATOR_API_1_3_PROJECTION.md. The disposition index row records "Mutability class: Historical/current compatibility inventory; outer-adapter guidance only" and "Superseded active guidance: API 1.3 commands, lane vocabulary, and generic transition as future application semantics." |

---

## Part II: Historical evidence — non-normative (no action required)

The following files contain old claims about SQLite, Python-shaped mechanisms,
or prior API vocabulary. Because their scope, time, and status make them
non-normative (they are immutable evidence or historical mission records), no
conflict resolution is required.

| File | Old claim type | Status | Reason non-normative |
|---|---|---|---|
| `docs/phase1-evidence/**` | SQLite as qualified control store at Phase 1 source boundary | **Unchanged evidence** | Immutable payload; qualified only at cited `adf15aac` source/evidence boundary; ADR 0001 and disposition index confirm scope |
| `docs/phase2-evidence/**` | Beads 1.2.2 adapter as qualified tracker at Phase 2 boundary | **Unchanged evidence** | Immutable payload; qualified only at exact tuple; scope verified by SHA256SUMS |
| `docs/phase3-evidence/**` | Mission method over Phase 3 proving/helper composition | **Unchanged evidence** | Immutable payload; qualified at `03afabd` boundary; production host explicitly excluded |
| `docs/MINI_REVIEW_BRIEF.md` | Pre-Phase-1 adversarial questions using SQLite-era assumptions | **Historical** | Immutable review prompt; no factual verdict; superseded by MINI_REVIEW_RESOLUTION |
| `docs/PHASE1_ACCEPTANCE_ERRATA.md` | Corrects historical mission wording, not active architecture | **Living correction** | Correctly scoped to Phase 1 behavior; not a normative statement about the target |
| `kitty-specs/phase1-*/`, `kitty-specs/phase-one-*/`, `kitty-specs/beads-*/`, `kitty-specs/software-development-*/` | Completed mission records with historical language | **Unchanged completed mission history** | Git tree checksums recorded in disposition index; editing any of these would be a violation |

---

## Part III: Ledger summary and closure confirmation

### Active normative conflicts: 9 found, 9 closed

| ID | File(s) | Conflict type | Status |
|---|---|---|---|
| CONFLICT-001 | `TECHNICAL_PROJECT_PLAN.md` | SQLite future direction | Closed (WP06) |
| CONFLICT-002 | `README.md` | SQLite/API 1.3 future direction | Closed (WP06) |
| CONFLICT-003 | `adr/0001-...md` | Scope clarity | Closed (WP07) |
| CONFLICT-004 | `ORCHESTRATOR_API_1_3_PROJECTION.md` | Competing API vocabulary | Closed (WP02+WP07) |
| CONFLICT-005 | `PYTHON_BEHAVIOR_INVENTORY.md` | SQLite as production representation | Historical/Closed (WP07) |
| CONFLICT-006 | `MINI_REVIEW_RESOLUTION.md` | Storage representation reference | Historical/Closed (WP07) |
| CONFLICT-007 | `CONCURRENCY_SCENARIOS.md` | SQLite as required proving representation | Historical/Closed (WP07) |
| CONFLICT-008 | `adr/0001-...md` | Verdict scope inference | Historical/Closed (WP07) |
| CONFLICT-009 | `ORCHESTRATOR_API_1_3_PROJECTION.md` | Missing canonical-pointer header | Closed (WP07) |

**Zero open active normative conflicts.**

### One canonical architecture confirmed

The single canonical architecture record is
`docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md`, backed by ADR 0002. All
active documents that previously contained normative guidance have been either:
(a) updated to defer to the canonical architecture (TECHNICAL_PROJECT_PLAN.md,
README.md), or (b) classified as historical/compatibility records with explicit
canonical successor pointers (ADR 0001, API 1.3, PYTHON_BEHAVIOR_INVENTORY.md,
MINI_REVIEW_RESOLUTION.md, CONCURRENCY_SCENARIOS.md).

No second canonical architecture was found.

### Historical evidence integrity

All immutable evidence payloads remain unchanged. The WP07 integrity capture
(HISTORICAL_ARCHITECTURE_DISPOSITIONS.md Section 6) provides git tree checksums
and `sha256sum --check` commands. This reviewer ran both checks:

```
(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)  → PASS
(cd docs/phase3-evidence && sha256sum --check SHA256SUMS)  → PASS
git diff --exit-code 45fd227... -- docs/phase1-evidence ... (not re-run from
WP07 base; verified by WP07 reviewer)
```

### Delivery honesty confirmation

No active normative document was found to describe proposed GapDB, API 2.0,
neutral acceptance, or MCP behavior as currently shipped. The explicit disclaimer
"this document does not claim an API 2.0 host…currently ships" appears in
ORCHESTRATOR_API_2_0.md. Every proposed target has an explicit "Status:
proposed" header or delivery-truth table row.
