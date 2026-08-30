---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 8
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T09:52:32Z'
reviewer_agent: reviewer-renata-capability-4
verdict: rejected
wp_id: WP01
---

# WP01 capability-boundary review — cycle 8

Verdict: changes requested.

The cycle-7 repair is effective for the defects it targets. Independent replay
confirmed that root and child type-check failures now fail closed; `Sink`, a
consumer interface, and a named holder remain valid; direct composite literals,
`new`, and typed zero-value declarations outside the validator are rejected;
the exact validator body remains admitted; playback may depend on the pure
reducer sibling; and `os` remains denied in every journal child. The complete
committed cycle-5 and cycle-6 corpus also passes.

One remaining construction-census blocker permits callers inside the root
journal package to manufacture `ValidatedJournalV1` without
`ValidateJournalV1`.

## Blocking issue — legal conversion and context-elided literals bypass the sole constructor

`m1CapabilityConstructionViolations` recognizes an exact explicit composite
literal, the built-in `new`, and an explicitly typed `ValueSpec`. It does not
recognize a `CallExpr` whose target is the exact capability type, and it asks
for the type of `CompositeLit.Type` even though Go omits that syntax node for
context-elided nested literals.

All five independently reproduced fixtures type-check and return an empty
violation set:

```go
func ForgeByConversion() {
    raw := struct{ events []StoredEventV1 }{}
    _ = ValidatedJournalV1(raw)
}

func ForgeInterfaceByConversion() any {
    raw := struct{ events []StoredEventV1 }{}
    return any(ValidatedJournalV1(raw))
}

func ForgeClosureByConversion() any {
    return func() any {
        raw := struct{ events []StoredEventV1 }{}
        return ValidatedJournalV1(raw)
    }()
}

func ForgeElidedSlice() {
    consumeAny([]ValidatedJournalV1{{}}[0])
}

func ForgeElidedMap() {
    consumeMap(map[string]ValidatedJournalV1{"x": {}})
}
```

The interface and closure variants also demonstrate why callable result
policing cannot substitute for construction policing: their declared result is
`any`, while the capability is still constructed in the body. This violates
the frozen rule that only `ValidateJournalV1` may construct the capability.

Resolve conversion call targets through `go/types` and reject conversion to the
exact `ValidatedJournalV1` outside the authoritative validator body. For
composite literals, inspect the resolved type of the literal expression itself
so a context-elided literal is not invisible when `CompositeLit.Type` is nil.
Retain the exact-validator exception and add all five forms as permanent
mutants with direct validator and ordinary consumer controls.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1.*(Mutant|Capability|Dependency|Budget|Schemas|Evidence)'` — PASS.
- `go test ./internal/architecture -count=1` — PASS (`12.583s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git show --check e64b6fc` — PASS.
- Out-of-tree cycle-7 replay — PASS for every reported defect and clean
  control.
- Out-of-tree cycle-5/cycle-6 replay — PASS through the committed mutant and
  clean-control corpus.
- Out-of-tree adjacent package campaign — PASS: a three-level pure sibling
  DAG is admitted; missing sibling source and an import cycle both fail closed;
  recursive capability consumers remain valid; recursive capability producers
  and raw-event consumers are rejected.
- Out-of-tree adjacent construction campaign — FAIL as expected: the five
  reproductions above survive without `journal-capability-construction`.
- The known long `internal/scenario` campaign was not run, as directed, and is
  not treated as WP01 evidence.

The temporary reviewer archive was removed. No implementation file or probe
remains in the lane worktree.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — the committed sole-constructor mutants
   cover only explicit syntax and miss legal conversion and elided-literal
   construction.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — FR-015/FR-016/NFR-013 sole-constructor exclusivity
   remains bypassable.
5. Frozen surface: **PASS** — cycle-8 implementation changes only the WP-owned
   architecture guard; no frozen contract or module file changed.
6. Locked decision: **FAIL** — non-authoritative code can still construct the
   frozen validation capability contrary to the control-journal contract.
7. Shared-file ownership: **PASS** — the implementation diff remains within
   WP01's exclusive owned-file surface.
8. Production fragility: **N/A** — no production panic or fail-loud path was
   added.
