# Compositional concurrency qualification decision

**Status:** ratified correction during WP14 review rework  
**Scope:** FR-007–FR-018, NFR-001, NFR-002, WP14/T072–T074

## Finding

The first WP14 concurrency suite exercised concurrent mission creation, while
the campaign merely named deeper package tests. That proved GapDB could accept
two unrelated readiness publications, but it did not by itself prove Spec
Kitty's distinguishing claim: two work items can hold independent authority,
progress in isolated workspaces, and reach independently reviewed candidates
without a host-wide command lock.

Repeating every capability pair in one enormous scenario would not repair the
claim. It would conflate three different laws—unrelated progress, same-key
serialization, and complete-mission recovery—and turn failures into an
unattributable integration test.

## Decision

Concurrency qualification is compositional and has four executable axes:

1. The complete production-host scenario admits two independent work items
   simultaneously, observes both as running, gives them distinct lease-scoped
   workspaces, records attributed tracker effects, produces two handoffs and
   independent reviews, and reaches two accepted integration attempts.
2. Capability-owned contention tests prove same-coordinate one-winner rules and
   unrelated-coordinate progress for execution, workspace path claims, tracker
   effects, review independence, acceptance, and integration targets through
   the real GapDB adapter.
3. A production-host test proves unrelated commands are not serialized by a
   global host command mutex.
4. Controlled mutants alter execution authority, workspace semantics,
   integration target coordinates, reviewer independence, and host composition;
   their owning suites must reject every substitution.

The campaign records those axes separately. It does not multiply a two-item
scenario by package-level race labels or claim that a semantic independence
test executed a transport race it did not execute.

## Intentional serialization boundaries

- Two commands for the same aggregate compete by version/CAS; one current
  authority wins and losers receive a typed stale or conflict result.
- Integration is serialized per `(repository_id, target_ref)`. Independent
  targets may progress concurrently; one target never becomes a host-wide
  mutex.
- Mission readiness and policy-fact transitions for one mission are serialized
  because they update one mission authority aggregate. This does not serialize
  independent work execution.
- Reviewer independence is an ActorID predicate, not a concurrency primitive.
  Two reviews may progress independently, but process separation cannot make a
  contributor an independent reviewer.

## Native lifecycle boundary

The two-item scenario selects one finalized `WorkRevision` through autonomous
readiness and the acknowledged workgraph. It deliberately does not recreate
the Python phase cursor, prompt-authored task-list ceremony, or manual phase
advancement: those behaviors are frozen removals, and WP18's capability-derived
readiness is the production successor.

The scenario includes the fake tracker through the production external-effect
saga. The outbox is registered, replayed, and adapter-qualified, but it has no
production Host command port in M2; the campaign therefore does not pretend the
complete-mission scenario emitted an outbox command. A future Host outbox port
must join both the no-bypass composition guard and this concurrency census.

## Consequences

- The crown claim remains about safe parallel work, not maximum simultaneous
  calls in one opaque test.
- Each contention failure points to its owning authority boundary.
- Same-target Git publication stays serialized while unrelated work remains
  parallel.
- New command capabilities must add an attributable same-key or independent-key
  proof when their authority semantics permit concurrency.
