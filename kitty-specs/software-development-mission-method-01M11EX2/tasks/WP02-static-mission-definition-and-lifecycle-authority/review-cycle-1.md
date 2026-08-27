---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T16:39:42Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 Review Feedback — Cycle 1

Verdict: **changes requested**.

The focused lifecycle suite, ten-repeat run, race detector, compile-only build,
repository contract suite, architecture campaign, scope campaign, vet,
staticcheck, module verification, formatting, and diff hygiene all passed. The
status-projection repair also represented the authoritative event state at the
time it was committed. Those successes do not close the findings below.

## 1. Blocker — the lane still carries destructive, out-of-map mission artifacts

WP02 owns exactly seven files under `internal/ids`, `internal/mission`, and
`internal/missiontype/softwaredev`. The WP's product commit respects that map,
but the lane history and net diff do not. Commit `72ce174` deletes
`acceptance-matrix.json` and a WP01 review record, rewrites mission dossier/meta
state, renames another WP01 review record, and introduces WP02/WP03 baseline
files. Later commits remove/rematerialize status projections, but do not restore
the other coordination-owned artifacts. Therefore the current merge candidate
can delete or rewrite shared mission authority unrelated to WP02.

Repair by rebasing/merging the current coordination authority and restoring
every non-WP02 artifact to the coordination version. The final product diff
must contain only the seven owned files. Runtime status projection/event files
may be rematerialized by Spec Kitty, but must not be presented as WP02 product
evidence and must agree with the current event log.

Add a final review assertion equivalent to:

```text
git diff --name-only <current-coordination>..HEAD
```

whose product surface is exactly the seven owned paths.

## 2. High — a store adapter can replace domain-authorized state with a different valid state

`Service.Create` and `Service.Transition` validate only that returned snapshots
are structurally valid (`internal/mission/service.go:308-315`, `329-364`). They
do not verify that a create result equals the mission/definition/commitment the
service submitted, or that a transition/replay result equals the evaluated
candidate and audit for the same mission, operation, versions, steps,
definition commitment, actor, governance context, and time.

Consequently a buggy adapter can return a different but valid mission snapshot
or legal-looking successor, and the service reports success. That violates the
WP law that the store may atomically persist a domain decision but may never
choose the legal successor or alter the permanent definition binding.

Add semantic postcondition validation for create, commit, and replay results.
Add deliberately wrong store doubles that return:

- another mission or definition after create;
- another successor step/state version after commit;
- an audit with another operation, actor, event, governance reference, or
  definition commitment;
- a structurally valid result from another operation during replay.

Every case must fail closed with a stable typed diagnostic and expose no
success.

## 3. High — known-kind store errors bypass the bounded diagnostic contract

`normalizeStoreError` returns any `*mission.Error` with a recognized kind
unchanged (`internal/mission/service.go:402-412`). `Error` also exposes
unvalidated public `Recovery`, `Field`, and `Message` values
(`internal/mission/model.go:319-344`). A store can therefore return a recognized
kind carrying an invalid recovery category or unbounded/secret-bearing message,
and the mission service leaks it verbatim. The existing secret test covers only
an untyped `error`, so it misses the dangerous path.

Normalize every adapter error to a mission-owned kind/recovery mapping and
bounded field/message, or validate a sealed domain error value before returning
it. Add a wrong-store test using a recognized kind with a seeded secret,
oversized message, and mismatched recovery category; the secret must not escape
and the recovery category must be the canonical one for that kind. Apply the
same bounded rule to definition-construction diagnostics that currently embed
caller-controlled step values.

## 4. High — the static mission law is not pinned completely

`definition_test.go` pins step names and edges, but for governance actions it
only asserts non-emptiness. It pins the two approval evidence lists, while not
pinning the exact required inputs, produced outputs, validation policies,
approval policies, mutating flags, governance action names, and terminal
outcomes for every step. The commitment test proves determinism, but does not
pin the production law: changing `mission.plan` to another valid operation ID,
or changing a non-gate step contract, still leaves the suite green.

Replace the partial census with one exact expected table covering every field
of every production `StepContractSpec`, plus initial/terminal and reserved-step
status. Assert the resulting production commitment (or an equivalently strict
canonical law fixture), and assert all fields of `TransitionAudit`, including
replay preservation, so FR-028 has behavioral coverage.

While this exported API is still new, align the Go identifier
`StepReconciliationNeeded` with the canonical domain term/value
`reconciliation_required` (for example `StepReconciliationRequired`). The
current identifier introduces avoidable semantic drift before downstream WPs
adopt it.

## Anti-pattern checklist

1. Dead code: **PASS** — the earned authority roots have production
   reachability (`softwaredev -> mission.NewDefinition` and
   `Service -> EvaluateTransition`); the later composition root is explicitly
   sequenced by the mission plan.
2. Synthetic-fixture tests: **PASS** — lifecycle tests invoke production
   constructors/evaluator/services and the real test port.
3. Silent empty return: **PASS** — no unexplained empty-success path was found.
4. FR coverage: **FAIL** — FR-028's complete audit binding is not asserted, and
   the static law is only partially censused.
5. Frozen surface: **PASS** — no declared frozen product file was modified by
   the WP02 product commit.
6. Locked decision: **PASS** — no tracker/Markdown/Phase-4 authority path was
   introduced; architecture and scope mutant campaigns pass.
7. Shared-file ownership: **FAIL** — the lane deletes/rewrites coordination and
   WP01 artifacts outside the ownership map.
8. Production fragility: **N/A** — no panic/raise-style production path was
   introduced.

## Evidence run

- `go test ./internal/ids ./internal/mission ./internal/missiontype/softwaredev -count=10` — pass
- `go test -race ./internal/ids ./internal/mission ./internal/missiontype/softwaredev` — pass
- `make contract` — pass
- architecture campaign — pass
- scope campaign — pass
- `go vet ./...` and `staticcheck ./...` — pass
- compile-only `go test -run '^$' ./...`, `go mod verify`, `gofmt`, and
  `git diff --check` — pass

