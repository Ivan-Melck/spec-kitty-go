---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T13:20:35Z'
reviewer_agent: user
wp_id: WP06
---

# WP06 Review Feedback — Cycle 1

**Verdict: changes requested.** The graph direction and durable-discussion paths are promising, and all bounded quality commands passed, but readiness can currently promote a conflicted mapping into the positive provider-ready set. The commit also crosses six accepted-file ownership boundaries without the explicit coordination required by this WP.

## Blocking findings

### 1. Readiness trusts the partial `bd ready` response instead of the complete WP05 mapping catalog

**Severity:** blocker  
**Affected:** `internal/tracker/beads/graph.go:364-436`, `internal/tracker/beads/graph_test.go:412-429`  
**Contracts:** T028 steps 1 and 6; FR-010; C-001; WP05 stable one-to-one mapping

`ReadyProjection` detects duplicate `WorkID` claims only when both duplicate external issues happen to be returned by `bd ready`. It never resolves each ready result through the complete WP05 mapping catalog. If two issues in the catalog claim `(project-1, work-a)` but only one is currently ready, the implementation returns `work-a` in `ProviderReadyIDs` with no mapping diagnostic.

An adversarial review probe created `bd-a-ready` and `bd-a-not-ready`, both claiming `work-a`, while `bd ready` returned only `bd-a-ready`. The expected refusal failed:

```text
conflicting catalog mapping became positively ready:
ProviderReadyIDs:[work-a] Diagnostics:[]
```

The temporary probe was removed after execution. Add a permanent regression that varies which duplicate claim is ready, resolve ready issues against the complete mapping observation, and exclude any unknown, foreign, or non-unique mapping from the positive ready set. Return a stable reconciliation diagnostic rather than treating the provider subset as proof of uniqueness.

### 2. WP06 changes six files outside its exact ownership map

**Severity:** high  
**Affected commit:** `1bd6697`  
**Contracts:** WP06 `owned_files`, Definition of Done, DIRECTIVE_024

The WP owns only `graph.go`, `graph_test.go`, `discussion.go`, and `discussion_test.go`, but the commit also changes:

- `internal/tracker/beads/catalog.go`
- `internal/tracker/beads/catalog_test.go`
- `internal/tracker/beads/command.go`
- `internal/tracker/beads/command_test.go`
- `internal/tracker/beads/inspect.go`
- `internal/tracker/beads/inspect_test.go`

Some crossings are plausibly necessary (`dep remove` did not exist in the accepted WP03 command vocabulary, and capability reports must be updated), which is exactly the interface mismatch the WP says to stop and report. The commit message has no one-line crossing rationale and no explicit mission coordination record. Obtain an explicit ownership expansion/coordination decision before retaining these changes, or revert the crossings and surface the dependency mismatch through the mission workflow. Do not solve this by weakening the ownership rule.

### 3. The two production capability reports contradict each other

**Severity:** high  
**Affected:** `internal/tracker/beads/inspect.go:280-305`, `internal/tracker/beads/catalog.go:231-258`  
**Contracts:** FR-006; ProviderCapabilities; honest capability reporting

For the same healthy bound adapter, `Capabilities()` reports WP05 catalog read/create/update plus WP06 graph/discussion, while `Inspect()` reports catalog read/create/update as unsupported and graph/discussion as supported. The new comment in `inspect.go` says only implemented ports are advertised, but accepted WP05 catalog ports are implemented. Operator and application behavior can therefore depend on which discovery surface it calls.

Make the healthy inspection report the same implemented guarantee set as the capability reporter, while preserving fail-closed behavior for incompatible, read-only, unavailable, and unqualified server bindings. Add a test comparing the two healthy reports for all capability fields.

### 4. The new Graph and Discussion production surface has no live production caller

**Severity:** high  
**Affected:** `internal/tracker/beads/graph.go`, `internal/tracker/beads/discussion.go`  
**Contracts:** WP anti-pattern check 1; plan delivery rule that package scaffolding alone is not progress

Repository-wide non-test call-site search finds only the interface declarations, fake-provider implementations, the Beads methods themselves, and compile-time interface assertions. No production application/composition path invokes `PutBlockingDependency`, `RemoveBlockingDependency`, `ListDependencies`, `ReadyProjection`, `AppendDiscussion`, or `ListDiscussion`.

Provide one bounded production-shaped caller/composition seam that consumes only the provider-neutral `tracker.Graph`/`tracker.Discussion` ports. Because that seam is outside this WP's current ownership map, coordinate the ownership expansion first. Tests must exercise that live path rather than only direct method calls.

## Behaviors that passed review

- Dependency argv and round-trip preserve distinct blocked/blocker direction.
- Self, unsupported-kind, unmapped, cross-project, provider-cycle, malformed, duplicate, missing, and ambiguous outcomes are covered without blind second mutations.
- Non-blocking relationships do not enter the executable blocker set.
- Readiness terminates in a diagnostic projection and imports no execution, lease, workspace, review, or integration authority packages.
- Discussion markers are exact, versioned, canonical, and separated from semantic bodies; body-marker lookalikes do not forge the envelope.
- Human comments remain visible and ordered; exact replay is a no-op; marker/body/author conflicts refuse; timeout-after-apply is observed; unavailable observation remains ambiguous with one append.
- Routine discussion errors observed in the suite did not expose the secret sentinel or raw provider prose.

## Evidence

```text
PASS go test ./internal/tracker/beads -count=1 -timeout=5m                 31.464s
PASS go test -race ./internal/tracker/beads -count=1 -timeout=10m        251.925s
PASS go test ./internal/tracker/beads -run 'Test(Graph|ReadyProjection|Discussion)' -count=20
PASS go vet ./internal/tracker/beads
PASS staticcheck ./internal/tracker/beads
PASS go test ./internal/architecture -count=1 -timeout=2m                 11.034s
PASS git diff 1bd6697^..1bd6697 --check
FAIL adversarial readiness/catalog-conflict probe (finding 1; probe removed)
```

The initially started package-wide `-count=20` run was deliberately stopped because it redundantly repeated WP05's expensive 1,000-create stress test; the required WP06 graph/readiness/discussion set then passed twenty repetitions.

## Anti-pattern checklist

1. **Dead code: FAIL** — the six new public Graph/Discussion methods have no non-test production caller.
2. **Synthetic fixture: PASS** — tests invoke the real Beads adapter paths; the missing full-catalog conflict is a coverage gap, not a literal-only fixture.
3. **Silent empty return: PASS** — no silent success/empty fallback was found in the new production paths.
4. **FR coverage: FAIL** — FR-010's conflicting-mapping readiness refusal is not covered and the implementation fails the adversarial case.
5. **Frozen surface: PASS** — no separately frozen surface was identified, but owned-file locality failed independently.
6. **Locked decision: PASS** — no Beads workflow state was adopted as execution authority and no human-output parser or Beads internal import was introduced.
7. **Shared-file ownership: FAIL** — six accepted WP03-WP05 files changed without the required explicit coordination/rationale.
8. **Production fragility: N/A** — Go code adds no `raise` path; fail-loud provider errors are typed and bounded.

## Review-process note

The WP frontmatter and generated review prompt still name `implementer-ivan` rather than a reviewer profile. Review was independently performed under resolved `reviewer-renata` governance; correct the review-profile metadata on the next cycle so the authoritative prompt records reviewer intent directly.
