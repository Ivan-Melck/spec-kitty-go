---
affected_files: []
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T17:22:35Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 Review Feedback — Cycle 2

Verdict: **changes requested**.

The candidate was reviewed at canonical lane-a commit `4b73229`, after the
product-only migration through `ef2e523` and `4b73229`. The attributed product
diff is exactly the seven WP02-owned files and is byte-equivalent to the former
lane-b candidate. No pre-reconciliation runtime or topology commit was treated
as product evidence.

All four cycle-1 findings are closed: store create/commit/replay results now
have semantic postconditions, recognized store errors are normalized into the
bounded mission contract, the production definition and transition audit have
exhaustive static censuses and mutants, the reconciliation identifier is
canonical, and the migrated ownership boundary is clean. Two remaining
authority-contract gaps block approval.

## 1. High — invalid graph diagnostics do not identify the exact step

T007's validation contract explicitly requires: “Invalid graph fixtures name
the exact step and violated definition rule.” The implementation preserves the
rule but drops the step. `NewStepContract` reports only fields such as
`step_contract.duplicate_successor`; `NewDefinition` reports generic messages
such as `definition has a duplicate step`, `definition contains an asymmetric
edge`, `definition contains a cycle`, and `definition contains an unreachable
step` (`internal/mission/definition.go:30-42`, `187-245`, `258-282`).

The test named `TestDefinitionGraphMutantsFailClosedWithExactRuleContext` only
asserts a generic rule substring (`internal/mission/lifecycle_test.go:141-184`),
so it would remain green if the diagnostic pointed to no step or the wrong
step. This matters operationally: a multi-step definition cannot be repaired
deterministically from “asymmetric edge” or “unreachable step” alone.

Preserve the cycle-1 confidentiality/bounds repair while restoring precise
context. For known closed `StepKey` values, report the exact step and rule. For
an invalid caller-controlled key, identify a bounded structural location such
as the step index rather than echoing raw input. Cycle/asymmetry/unreachable
fixtures should assert the exact offending known step (and, where needed, the
edge endpoint) plus the stable rule.

## 2. High — the exported pure evaluator does not enforce its full snapshot CAS input

Cycle 2 adds `TransitionRequest.ExpectedSnapshot` and includes the complete
snapshot in the request commitment. `Service.Transition` correctly compares
that snapshot to the loaded authoritative snapshot before evaluation
(`internal/mission/service.go:369-384`). However, the exported authority
function `EvaluateTransition` compares only mission identity, definition,
state version, and current step (`internal/mission/service.go:123-150`). It does
not compare the supplied instance to `request.ExpectedSnapshot`.

As a result, a direct evaluator call can present an expected snapshot with a
different project, timestamps, lifecycle/work-revision state, blocker set, or
decision set while retaining the same mission/definition/step/version; the
evaluator then authorizes from the separately supplied instance. That makes
some fields part of the semantic request commitment and replay law without
making them part of the pure authority law. The service masks this today, but
the exported evaluator is itself the WP's reusable authority seam and the
existing mutants do not protect the invariant.

Either enforce full `instance.Snapshot() == request.ExpectedSnapshot` inside
`EvaluateTransition` with the stable refresh/conflict refusal, or narrow the
request/API so it does not advertise a redundant full-snapshot CAS contract.
Add direct evaluator mutants that alter project identity, timestamps,
work-revision/lifecycle facts, active blockers, and unresolved decisions on
only one side of the instance/request pair. The same semantic snapshot law
should then be shared by evaluator, service, and replay validation.

## Anti-pattern checklist

1. Dead code: **PASS** — the mission definition and evaluator have live
   production reachability through softwaredev construction and `Service`.
2. Synthetic-fixture tests: **PASS** — behavioral tests invoke production
   constructors, registry, evaluator, service, and the aggregate-shaped test
   store.
3. Silent empty return: **PASS** — no silent empty-success path was found.
4. FR coverage: **FAIL** — FR-003/CAS authority is not enforced consistently by
   the exported evaluator, and the T007 graph-diagnostic acceptance contract is
   not asserted.
5. Frozen surface: **PASS** — the exact product commits touch only the seven
   owned WP02 files.
6. Locked decision: **PASS** — no tracker, Markdown, transport, or deferred
   Phase 4+ authority path was introduced; architecture and scope mutant
   campaigns pass.
7. Shared-file ownership: **PASS** — the product-only migration removed the
   cycle-1 topology contamination; coordination state is not counted as
   product evidence.
8. Production fragility: **N/A** — no raise/panic-style production path was
   introduced.

## Evidence run

- `go test ./internal/ids ./internal/mission ./internal/missiontype/softwaredev -count=10` — pass
- `go test -race ./internal/ids ./internal/mission ./internal/missiontype/softwaredev` — pass
- `go test ./internal/architecture -count=1` — pass
- `go test ./internal/scenario -run 'Scope|Architecture' -count=1` — pass
- `make contract` — pass
- canonical architecture campaign — pass
- canonical scope campaign — pass
- `go vet ./...` and `staticcheck ./...` — pass
- compile-only `go test -run '^$' ./...`, `go mod verify`, `gofmt`, and
  `git diff --check ef2e523^..4b73229` — pass
