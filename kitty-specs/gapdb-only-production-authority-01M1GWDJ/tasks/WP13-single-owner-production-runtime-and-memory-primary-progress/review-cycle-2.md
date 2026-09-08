---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T14:31:18Z'
reviewer_agent: codex
wp_id: WP13
---

# WP13 post-approval defect 1: incomplete production capability composition

WP14's first independent review correctly rejected the claimed complete
production-host mission scenario. The failure is owned by runtime composition,
not by the scenario adapter:

- `productionruntime.Host` exposes raw workgraph publication but not the
  mission finalization service that mints a `WorkRevisionSelectionV2` from an
  acknowledged finalization result.
- the host does not compose or expose `DecompositionAuthorityServiceV2`, so a
  production client cannot perform tracker-backed decomposition through the
  single owner.
- `Config.Actions` is optional and no production implementation exists. As a
  result, `Progress().Mission` can return `Complete=false`, and WP15 cannot
  lawfully implement autonomous `drive` without inventing capability facts.
- no bounded executable-action/dispatch seam connects an authorized census
  entry to its canonical typed request, so a command adapter would have to
  synthesize policy.

## Required repair

Extend the single production composition root with the smallest closed,
provider-neutral application seam that:

1. composes workgraph finalization and decomposition from the admitted GapDB
   authority and an injected typed tracker/decomposition capability;
2. constructs a complete revision-bound `ActionCensusSource` from the restored
   projection and capability-owned observers, failing startup closed when a
   complete source cannot be built;
3. exposes bounded typed execution for census actions without a generic state
   transition, raw store, raw GapDB, backend selector, or adapter-owned policy;
4. keeps all commands behind the existing epoch/readiness barrier and refreshes
   memory from acknowledged durable truth; and
5. proves a production client can drive readiness, decomposition,
   finalization, and the existing downstream capabilities without private
   storage access.

This repair is a prerequisite for honest WP14 T072 evidence and WP15 T077. It
must preserve all approved WP13 ownership, restart, fencing, and memory-primary
query laws.
