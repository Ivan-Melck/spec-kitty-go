---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T22:27:47Z'
reviewer_agent: user
wp_id: WP04
---

# WP04 Review Feedback — Cycle 1

Verdict: **rejected**. The happy path and existing qualification gates pass, but five acceptance-critical contracts remain unenforced.

## 1. FR-010/T020 invalid work does not produce typed findings

`internal/workgraph/observation.go` rejects missing title, description, design context, criteria, write scopes, or provenance with one generic `error`. `internal/workgraph/validation.go` consequently has no `FindingKind` for missing standalone context, malformed/duplicate criteria, or invalid work boundaries. This means incomplete provider work cannot reach the typed-finding surface required by FR-010 and NFR-014.

Make raw provider observations representable at the validation boundary (or return a typed validation result from construction), add stable bounded finding kinds/fields/recovery for every T020 rule, and add one production-path mutant for missing objective, description, design context, criterion ID/outcome/verifier/required meaning, duplicate criterion, and invalid/absent write scope.

## 2. Lane findings are order-dependent and unbounded

`ValidateLaneProjection` records whichever lane appears first in the input map/slice as authoritative duplicate-membership context. Reversing two semantically equivalent duplicate-membership lanes changed the finding field/message and also changed whether `ordering_constraint_lost` appeared. A second probe using a malformed 6,000-byte lane identity returned a 6,006-byte `Finding.Field`.

Validate and bound lane/edge identities before interpolating them into evidence, canonicalize membership before deriving diagnostics and ordering checks, and prove byte-equivalent findings across lane/member/edge permutations. Findings must never echo unrestricted provider/tool input.

## 3. Provider attribution is not enforced

`fake.Provider.ObserveDefinition` accepts a valid `ExternalWorkRef` whose `Provider` is changed from `durable_fake` to an arbitrary foreign provider, then silently returns the local fake record. The constructor's look-alike test covers external-ID disagreement but not provider identity. The mission service also does not compare the returned mapping to the requested proposal before accepting it.

Reject foreign-provider references in the fake, bind provider identity into observation agreement, and have decomposition verify project, work, provider, external identity, operation attribution, and request content for each mutation/re-observation. Add wrong-provider, swapped-observation, and zero-observation-success mutants; these must return typed bounded recovery rather than panic or adopt state.

## 4. Locally detectable update mismatches are persisted before refusal

`validateDecompositionCommand` validates an existing reference in isolation but does not require it to match the proposal's project/work identity, provider, or expected observation. `PrepareDefinitionOperation` therefore writes the parent operation record before `UpdateDefinition` rejects a locally detectable mapping mismatch.

Perform all mapping/definition/expected-observation agreement checks before capability preparation or any provider mutation authority is acquired. Add a test proving these mismatches make zero capability, prepare, create, update, and observe calls.

## 5. The permanent architecture gate is not load-bearing for the workgraph boundary

The current `workgraph` package has no tracker import, which is correct, and the architecture campaign passes. However, a temporary production import from `internal/workgraph` to `internal/tracker` still allowed `TestCurrentProductionGraphPassesClosedPolicy` to pass because the broad authority tier permits provider-contract imports. The campaign therefore does not preserve the provider-free semantic projection boundary.

Add an exact forbidden edge or narrower tier rule for `workgraph -> tracker`, plus a rejecting mutant included in the exact architecture census. Keep the legitimate `mission -> tracker` composition edge.

## Gate evidence

- PASS: tracker/fake focused tests, count 10, and race detector.
- PASS: workgraph/mission focused tests, count 10, and race detector.
- PASS: tracker compatibility suite including Beads and conformance packages.
- PASS: compile-only repository sweep, vet, staticcheck, module verification, gofmt, and diff hygiene.
- PASS: permanent architecture campaign (38/38 top-level tests).
- PASS: concurrent same-operation fake replay probe under the race detector (20 callers, one durable create, count 10).
- FAIL as expected: deterministic duplicate-membership finding probe.
- FAIL as expected: bounded malformed-lane evidence probe.
- FAIL as expected: foreign-provider reference attribution probe.

## Anti-pattern checklist

1. Dead code: **N/A** — `ValidateLaneProjection` and decomposition are explicit forward seams for dependent WPs; the tracker/workgraph production paths otherwise have live callers.
2. Synthetic-fixture tests: **PASS** — existing tests invoke production constructors, provider persistence, saga, and validators.
3. Silent empty return: **PASS** — no unexplained silent-empty production path introduced.
4. FR coverage: **FAIL** — FR-010/NFR-014 behaviors above are absent, not merely untested.
5. Frozen surface: **PASS** — product commits `6088030` and `ad03bf6` are confined to ten of the eleven owned paths.
6. Locked decision: **PASS** — no Beads, Markdown, execution, review, or integration authority leaked into the product diff.
7. Shared-file ownership: **PASS** — product commits are separate from topology/status cleanup; no other WP-owned product path was changed.
8. Production fragility: **N/A** — no Go equivalent of a bare transient-path raise was introduced; the zero-observation trust gap is covered in Issue 3.
