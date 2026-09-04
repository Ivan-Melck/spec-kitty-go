---
affected_files: []
cycle_number: 24
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T04:37:21Z'
reviewer_agent: renata-gapdb-deadline-structural
wp_id: WP01
---

# WP01 Review Cycle 24 — Changes Requested

Reviewed implementation commit: `c05094d5772e616ab44be740e4240856715d49a5`

The defect-8 proof split is substantially improved: the custom CFG/caller/
dominance verifier is gone, intentionally unusual control flow is accepted,
and the intended structural, overlay, race, architecture, static, module,
formatting, and ownership gates pass. One blocking structural-confinement gap
remains.

## Blocker: additional duration-bearing state is admitted

The validator counts explicit identifier uses resolving to `time.Duration`,
but does not inspect inferred variable types or transitive type containment.
Consequently both of these illegal shapes pass 20/20:

```go
type BudgetsV2 struct {
	MaximumDuration time.Duration
}

var leakedDuration = time.Second

func run(ctx context.Context, b BudgetsV2) {
	child, cancel := context.WithTimeout(ctx, b.MaximumDuration)
	defer cancel()
	_ = child
}
```

```go
type BudgetsV2 struct {
	MaximumDuration time.Duration
}

type ResultV2 struct {
	Budget BudgetsV2
}
```

Both must produce `ARCH-DEFERRED-SURFACE target=time.Duration.state`.
The first introduces independent package state whose inferred type is
`time.Duration`. The second carries the duration-bearing budget transitively
into result state, which the ratified defect-8 contract forbids.

## Required repair

- Inspect `go/types` variable and named-type graphs, not only explicit
  `time.Duration` syntax.
- Reject independently inferred duration-typed package state.
- Reject transitive duration reachability from result, durable, admission,
  journal, digest, ordering, and authority state.
- Preserve the real `InputV2.Budgets` configuration path; a blanket ban on
  nesting `BudgetsV2` would reject the production implementation.
- Add both fixtures above as permanent negative controls.
- Preserve the structural-only boundary: do not reintroduce CFG, dominance,
  arbitrary call-graph, or validation-flow inference.

All other requested cycle-24 checks passed, including exact restore-only time
ownership, aliases and lexical shadowing, ambient clock/timer refusal, direct
real `context.WithTimeout`, function-object escape refusal, abnormal-control-
flow acceptance, the unchanged real WP04 overlay, and the sealed 185-mutant
census.
