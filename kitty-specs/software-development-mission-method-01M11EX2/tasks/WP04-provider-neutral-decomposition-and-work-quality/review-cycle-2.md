---
affected_files: []
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T22:57:54Z'
reviewer_agent: user
wp_id: WP04
---

# WP04 Review Feedback — Cycle 2

**Verdict:** Reject

**Candidate reviewed:** product head `9419ffd` (`c0c4655` topology cleanup; governed review head `78fbcf8`)

## Blocking finding

### T020 cross-domain justification is present but not validated

**Severity:** Blocker  
**Location:** `internal/workgraph/validation.go`, cross-domain ownership check near line 158

`ValidateDraft` currently rejects unrelated ownership domains only when
`CrossDomainReason() == ""`. A raw provider observation with two independent
exclusive ownership domains and a whitespace-only or oversized reason therefore
produces no `FindingUnrelatedOwnership` finding.

This violates T020 step 6, which requires an explicit **validated** reason, and
it is especially important at this boundary because `NewWorkObservation`
intentionally admits malformed provider semantic content so the typed workgraph
validator can explain why it cannot be finalized. The stricter
`tracker.NewWorkDefinition` constructor does not close the raw-provider seam.

Independent probe:

```go
params := validRawObservationParams()
params.CrossDomainReason = "   "
params.WriteScopes = append(params.WriteScopes, workgraph.WriteScopeObservation{
    Path: "internal/second", Domain: "second", Access: workgraph.ScopeWriteExclusive,
})
item, _ := workgraph.NewWorkObservation(params)
findings := workgraph.ValidateDraft(rawDraft(t, item), workgraph.DefaultQualityPolicy())
// findings == []
```

The temporary probe failed exactly this assertion and was removed. An oversized
provider-controlled reason has the same bypass shape.

**Required correction:** validate a non-empty cross-domain reason with the same
bounded-text semantics as the provider-neutral definition contract (currently
512 bytes), emit a stable typed finding for absent *or malformed* justification,
and add focused whitespace, oversized, and valid-reason controls to the permanent
FR-010/T020 raw-provider mutant table. Do not echo the provider-controlled reason
in the finding.

## Cycle-1 blocker re-review

All five cycle-1 blockers are corrected and adversarially verified:

1. Exact typed FR-010 findings now cover missing standalone context, malformed
   criteria, and invalid boundaries.
2. Lane diagnostics are canonical and bounded across provider-order
   permutations and hostile oversized identities.
3. The fake provider rejects foreign-provider mappings, and the decomposition
   seam rejects unattributed mutation/re-observation results without adopting
   them.
4. Locally detectable update mapping mismatches are refused before capability,
   preparation, mutation, or observation calls; the exact guarded-update control
   remains legal.
5. The permanent architecture rule now rejects exactly
   `internal/workgraph -> internal/tracker` while preserving
   `internal/mission -> internal/tracker`. A temporary real production import
   made `TestCurrentProductionGraphPassesClosedPolicy` fail with
   `ARCH-LOCAL-DIRECTION`; the probe was removed. The production-shaped deletion
   mutant and architecture campaign are load-bearing.

## Gate evidence

- Focused tracker/fake tests: pass, repeated 10 times.
- Focused mission decomposition tests: pass, repeated 10–20 times.
- Focused workgraph observation/validation/coverage/ownership/dependency/lane
  tests: pass, repeated 10–20 times.
- Architecture tests: pass, repeated 3 times; permanent architecture campaign
  passed all 38/38 censused tests in 13.345 seconds.
- Race detector: tracker/fake, mission, and workgraph pass. An independent
  20-caller same-operation fake-provider probe passed under `-race -count=10`
  with exactly one durable create; the temporary probe was removed.
- Compatibility: tracker, Beads, conformance, fake, workgraph, and mission pass.
- Repository compile-only suite: pass.
- `go vet`, `staticcheck`, `go mod verify`, `gofmt -d`, and `git diff --check`:
  pass/clean.
- Product scope is confined to 12 of the 13 authorized paths; the owned
  `internal/tracker/provider.go` required no change. Product commits remain
  separate from topology/status cleanup.

No implementation files were changed by this review.
