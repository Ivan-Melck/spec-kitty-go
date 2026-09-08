# Compositional fault qualification decision

**Status:** ratified correction during WP14 review rework  
**Scope:** FR-025, NFR-001, NFR-002, WP14/T074

## Finding

The first WP14 campaign constructed a 15-family by six-cut table, then reused
ordinary tests under multiple cut labels. It reported 9,000 cells while
executing far fewer distinct experiments. Independent review correctly
rejected that evidence.

Making the Cartesian claim literal would require either duplicating the same
transport experiment for every semantic family or exposing a production
fault-injection hook capable of interrupting arbitrary family publications.
The first adds repetition without new state-space coverage; the second creates
a production authority bypass solely for test convenience.

## Decision

Qualification is orthogonal and compositional:

1. Every registered authority family executes its own semantic publication,
   refusal ordering, exact replay/collision, and restore evidence under the
   required repetitions.
2. Every provider/transport cut executes through the sole real publication
   coordinator and GapDB committer under the required repetitions.
3. An executable composition guard proves every host-served command capability
   is wired to that coordinator/committer and no alternate command publisher
   exists.
4. A controlled source overlay bypasses the boundary and must be killed by the
   composition guard.
5. The evidence ledger reports these axes separately. It never multiplies
   labels or calls a metadata row an executed fault.

This is stronger than the rejected Cartesian ledger because every reported
execution is real and every inference is backed by a separately falsifiable
composition proof.

## Consequences

- No production fault-injection configuration or raw storage handle is added.
- Family semantics remain independently attributable.
- Transport ambiguity is qualified once at the actual shared boundary rather
  than simulated inconsistently in fifteen consumers.
- A future capability must join the registered family census and the
  no-bypass composition guard before it can be production-admitted.

The composition claim has three explicit, testable boundaries. `runtime_host`
bootstrap admission necessarily occurs before the coordinator exists and is
qualified as its own sealed atomic GapDB batch. `operation` is mandatory
membership in every command plan rather than a standalone command port.
`outbox` is registered, replayed, and adapter-qualified, but has no production
Host command surface yet; it must join the no-bypass guard if such a surface is
introduced. None of these cases is counted as if it traversed a port it does
not have.
