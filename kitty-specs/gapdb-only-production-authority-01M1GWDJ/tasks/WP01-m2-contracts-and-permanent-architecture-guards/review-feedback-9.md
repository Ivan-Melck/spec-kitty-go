# WP01 Post-Approval Defect 2 Review Cycle 9 — Changes Requested

Reviewed repair: `15808e66a030e1d8aa715e277c25e328b4ff3c3b`

## Blocker: generic constraints and defined wrappers evade the type graph

The ownership inspector now covers functions, methods, fields, named function
types, and aliases, but its semantic type traversal is incomplete:

- interfaces traverse methods but not embedded constraint terms;
- unions are not traversed;
- signatures omit type-parameter constraints; and
- a new defined adapter type erases the named origin of its foundation RHS in
  `go/types` unless source declaration provenance is retained.

Valid Go 1.26 probes escaped through used and unused generic constraints, union
constraints, generic aliases, ordinary callables, named callable types,
interface methods, and defined request wrappers derived from
`journalv2.ReadRequestV2`.

## Required terminal repair

1. Traverse every interface embedded type and every union term.
2. Traverse signature type and receiver parameters, including unused
   constraints.
3. Build transitive adapter type-declaration provenance from `TypeSpec` RHS
   definitions/uses so defined wrappers retain their foundation origin.
4. Apply the same provenance-aware predicate to exported functions, variables,
   callable types, fields, and interface methods.
5. Add exact used/unused generic constraint, union, generic-alias, defined
   request wrapper, named callable, and interface-path mutants, with unrelated,
   foundation-only, and private positive controls.
6. Bind every addition to the exact census/digest and fixture substitution,
   deletion, and no-op proofs. Preserve all prior 105 mutants.

## Passing scope

Prior callable and callable-type bypasses are closed. All global-head,
operation-2/2, registry/digest, dependency, provider, reachability, and 105
existing mutant laws pass. Focused x3, race, full architecture, helper,
repository compile, vet, staticcheck, formatting, and diff checks pass.
Temporary probes were removed.
