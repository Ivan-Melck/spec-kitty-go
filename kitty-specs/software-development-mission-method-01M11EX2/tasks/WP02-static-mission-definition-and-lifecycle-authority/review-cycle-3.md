---
affected_files: []
cycle_number: 3
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T17:41:41Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 Review Feedback — Cycle 3

Verdict: **changes requested**.

The final candidate was reviewed at product commit `adb5229`; runtime HEAD
`7ebf3f7` contains the byte-identical seven-file WP02 product tree plus normal
coordination history. The cycle-2 full-snapshot CAS finding is closed:
`EvaluateTransition` now compares the complete current snapshot to
`ExpectedSnapshot` before time, blocker, decision, evidence, or successor
authorization. Direct-call mutants cover project, creation/update time,
WorkRevision, blocker, and decision mismatches, while malformed expected state
still fails request validation first. A deletion control using the pre-fix
production files makes those mutants fail. All cycle-1 authority fixes remain
intact.

## High — graph diagnostics and mutants still do not cover every invalid graph class

T007 requires invalid graph fixtures to identify the exact step and violated
definition rule, and T011 requires each graph mutant to fail for its intended
stable rule. The repair provides excellent exact diagnostics for duplicate
steps, duplicate successors, asymmetric edges, cycles, unreachable steps, and
invalid caller keys. It does not close the same law across the rest of
`NewDefinition`.

The following branches still return generic fields/messages even though a
known offending step exists (`internal/mission/definition.go:205-250`):

- invalid/reserved or known-but-absent initial step;
- duplicate or known-but-absent terminal step;
- a terminal step exposing successors;
- a terminal step with an invalid outcome;
- a nonterminal step declaring a terminal outcome.

For example, an exact probe against `adb5229` returns:

```text
field=initial_step  message=definition names an unknown initial step
field=terminal_steps message=definition has a duplicate terminal step
field=terminal_steps message=definition names an unknown terminal step
field=terminal_steps message=terminal step exposes successors
field=terminal_steps message=terminal step lacks a valid outcome
field=steps message=nonterminal step declares a terminal outcome
```

None identifies the known step. Preserve the bounded/secret-safe rule: echo
only validated closed `StepKey` values; otherwise identify a structural index
or field. A class with no possible offending step, such as an empty terminal
set, should still have one exact stable structural field/rule assertion rather
than a substring check.

The mutant table also leaves several paths unproved
(`internal/mission/lifecycle_test.go:141-203`):

- missing initial/terminal, unsupported adjacent step, self-edge,
  terminal-successor, and governance failures use substring-only checks;
- no duplicate-terminal, unknown-terminal, invalid-terminal-outcome,
  nonterminal-outcome, or unknown-predecessor mutant exists;
- the test named `unknown-successor` inserts a reserved step, so it fails in
  `validateStepSpec` as `unsupported_adjacent_step` and never exercises
  `definition.unknown_successor`.

Close the diagnostic class uniformly and make the table assert exact
machine-readable `Field`, bounded `Message`, and offending safe step/location
for every invalid graph class. Exercise definition-level unknown successor and
predecessor by removing an otherwise valid Phase 3 step from the definition,
not by inserting reserved vocabulary. Retain the invalid-secret fixture to
prove raw caller input is never disclosed.

## Anti-pattern checklist

1. Dead code: **PASS** — definition and evaluator entry points retain live
   production callers.
2. Synthetic-fixture tests: **PASS** — tests execute production constructors,
   evaluator, service, and store port.
3. Silent empty return: **PASS** — no silent empty-success path was found.
4. FR coverage: **PASS** — the cycle-3 CAS repair closes FR-003 and the existing
   suite exercises the other referenced functional laws; this rejection is the
   explicit T007/T011 and NFR-014 diagnostic contract.
5. Frozen surface: **PASS** — the product repair touches only three of the seven
   WP02-owned files.
6. Locked decision: **PASS** — no tracker, Markdown, transport, persistence
   implementation, or deferred Phase 4+ authority was introduced.
7. Shared-file ownership: **PASS** — runtime coordination files are separate
   from the exact `adb5229` product commit.
8. Production fragility: **N/A** — no panic/raise-style production path was
   introduced.

## Evidence run

- focused lifecycle/definition suite, `-count=10` — pass
- new cycle-3 mutants, `-count=50` — pass
- focused race detector with `-count=1` — pass
- deletion control against pre-fix production — fails as required
- `make contract` — pass
- architecture and scenario scope checks — pass
- canonical architecture and scope campaigns — pass
- `go vet ./...`, `staticcheck ./...`, compile-only build — pass
- module verification, formatting, and diff hygiene — pass

