# WP01 Defect 2 Terminal Arbitration — Bounded Repair Directed

Reviewed repair: `053ea420b2e143392ecdc99ee90289a4aebd4df4`

## Terminal blocker: exported composite values expose unwalked callables

The type graph is complete once entered, but the ownership inspector still
requires a top-level candidate to be a function signature. It therefore does
not recursively enter every type reachable through the exported package API.

Three valid independent probes false-greened:

```go
var Reader interface {
    Fetch(journalv2.ReadRequestV2) (Parcel, error)
}
```

```go
type service struct{}
func (service) Fetch(journalv2.ReadRequestV2) (Parcel, error)
var Reader service
```

```go
var Reader struct {
    Fetch func(journalv2.ReadRequestV2) (Parcel, error)
}
```

An unexported concrete type is externally reachable when an exported value or
function exposes it; its spelling does not make the public side seam private.

## Arbiter-directed final repair

1. Start semantic inspection from every exported package object, not only
   top-level signatures or exported named types.
2. Recursively inspect the entire externally reachable type graph, including
   method sets and callable members of interface/struct/composite values and
   unexported concrete types reached through an exported root.
3. Retain cycle detection and bounded traversal so recursive types terminate.
4. Add the exact anonymous-interface variable, exported unexported-receiver
   value, and anonymous-struct function-field mutants.
5. Preserve positive controls for unrelated composites, foundation-only
   composites, and private types that are not reachable from an exported root.
6. Bind the new cases into the sealed census/digest and deletion, substitution,
   fixture-swap, and no-op controls. Preserve every prior 116 mutant.

The next review is terminal arbitration of this directed repair. It should not
start another ordinary refinement cycle.

## Passing scope

Every prior callable, method, generic, union, alias, wrapper, pointer,
provenance, keyspace, operation tuple, registry identity, dependency, and
reachability mutant passed. Focused x3, race, full architecture, helper,
compile, vet, staticcheck, formatting, and diff gates passed. Temporary probes
were removed.
