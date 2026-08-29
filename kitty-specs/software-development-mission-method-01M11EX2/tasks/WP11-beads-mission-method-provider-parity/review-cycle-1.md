---
affected_files:
- internal/tracker/beads/mission_method_integration_test.go
- internal/scenario/mission_method_beads_test.go
- internal/tracker/beads/graph.go
- internal/tracker/beads/graph_test.go
- internal/mission/decomposition.go
- internal/mission/decomposition_test.go
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command: go test -count=10 ./internal/tracker/beads ./internal/mission ./internal/controlstore/sqlite ./internal/scenario -run 'TestMissionMethod|TestDecomposition|TestWorkDefinition'
reviewed_at: '2026-08-28T13:12:33Z'
reviewer_agent: reviewer-renata
wp_id: WP11
---

# WP11 Review Feedback — Cycle 1

**Verdict**: Rejected

The provider-neutral capability and durable root-reservation design is sound,
but the candidate does not yet prove the complete accepted mission path or the
required provider-failure invariants.

## Blockers

1. **The real Beads qualification bypasses decomposition authority.**
   `mission_method_integration_test.go` calls `BoundAdapter.CreateDefinition`
   directly and then advances the mission with test transitions. It therefore
   does not prove approved-plan eligibility, consumer-owned root reservation,
   derived child operations, operation-scoped capabilities, or exact
   decomposition replay against Beads. Drive the real provider through
   `mission.NewDecompositionService(...).Decompose` with SQLite root authority,
   reopen the store, and prove exact root replay adds no provider mutations
   before finalization and reconciliation.

2. **T060 parity and dishonest-provider qualification are incomplete.** The
   fake/Beads comparison stops at each definition's `CanonicalData`; it does not
   compare complete dependencies, coverage, lanes, graph, or semantic
   WorkRevision digest. Add equivalent complete fake and Beads
   drafts/revisions and compare those semantic values while retaining distinct
   provider observations. Run slow, unavailable, timeout, malformed, stale,
   wrong-identity, omitted/additional work, reversed-dependency, schema-skew,
   and between-observation mutation cases through decomposition/finalization.
   Every refusal must preserve the SQLite mission, current pointer, revisions,
   events/outbox, and projection state, and a clean retry must observe afresh.

## High findings

3. **Identical duplicate provider mappings are accepted.**
   `mappingIndexFromIssues` detects only differing duplicates. A byte-identical
   repeated list entry reaches `ObserveDefinitionCatalog` twice. Treat every
   repeated external/list mapping as a typed conflict and add the identical
   duplicate mutant alongside rebound, cross-project, and distinct-external
   controls.

4. **Unsupported capabilities consume durable root authority.** The
   decomposition service reserves `decomposition_saga` before capability
   discovery. Guarded-update or blocking-graph refusal must happen before any
   root reservation or provider mutation. Reorder capability validation and
   add both capability mutants with zero authority calls/rows and zero provider
   mutation calls.

## Passing evidence to preserve

- Exact governed scope and WP10 lineage are correct; the lane is clean.
- Focused repetitions, race, architecture, scope, vet, staticcheck,
  formatting, and diff checks pass.
- The real Beads binary/version/digest and durable finalization/reconciliation
  portions pass in offline containment.
- The historical Phase 2 `provider-adversarial` failure is non-attributable:
  unchanged WP08 fixtures fail complete eligibility before provider faults.
  That red cannot substitute for the missing WP11 T060 evidence.
- Operation-scoped requirements, full request digesting, SQLite root
  reservation, schema-1 compatibility, and kernel isolation remain valid.

Repair the invariant classes above without weakening any passing control or
expanding beyond the ratified sixteen-file scope.
