---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T06:07:28Z'
reviewer_agent: codex
wp_id: WP06
---

---
affected_files:
- internal/workgraph/reconcile.go
- internal/workgraph/reconcile_test.go
- internal/handoff/service.go
- internal/handoff/handoff_test.go
- internal/review/service.go
- internal/review/review_test.go
- internal/integration/service.go
- internal/integration/service_test.go
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command: go test ./internal/workgraph -run TestReviewerProbeAdditionOnlySuccessorNeedsNoInventedOldDisposition -count=1
reviewed_at: '2026-08-28T06:05:58Z'
reviewer_agent: codex
wp_id: WP06
---

# WP06 Review Feedback — Cycle 1

Verdict: changes requested.

## 1. Full mixed-reconciliation authority never reaches any live consumer (blocker)

T033 requires each handoff, review, and integration live path to consume the
common-shaped immutable eligibility fact containing the reconciliation ID,
old/new revision IDs, WorkID, status, disposition, permitted operation, and
bounded reason. It explicitly forbids leaving that fact as a detached validator.

The product defines the complete fact and derives it in
`MixedReconciliation.EligibilityFact`, but no production consumer calls that
method or accepts that shape. All three store ports still return only the frozen
v1 tuple `(Consequence, reconciliationID)`, and all three services immediately
reconstruct `NewLegacyEligibilityFact` and call `MatchesLegacy`. That projection
explicitly has no successor revision or disposition. Consequently a v2 mixed
decision cannot be distinguished from an incomplete legacy consequence at the
acceptance boundary, and the consumers never validate the exact successor or
disposition they were required to bind.

This is observable directly in the production paths:

- `internal/handoff/service.go`: `Store.WorkEligibility` and
  `requireEligibility` use only `Consequence`, reconciliation ID, old revision,
  WorkID, status, and operation;
- `internal/review/service.go`: the same legacy-only projection is used before
  review inspection;
- `internal/integration/service.go`: the same legacy-only projection is used
  after the approved-review lookup and before repository fencing/Git work;
- `MixedReconciliation.EligibilityFact` has no production caller outside its
  defining package.

The replay/eligibility ordering tests pass, but they prove only that a Boolean
legacy consequence is consulted at the right time. They do not prove the v2
authority identity required by T033.

### Required repair

Add a typed eligibility query/inspection seam capable of returning the complete
`EligibilityFact` for mixed/v2 reconciliation and make each live service validate
the complete old revision, current successor revision, WorkID, status,
disposition, and operation before accepting `Permitted`. Preserve the frozen v1
query as an explicitly selected compatibility branch for genuinely historical
v1 records; a v2 record must never silently downgrade to `LegacyV1`.

Add permanent tests for all three consumers that kill:

- successor-revision substitution;
- disposition substitution;
- reconciliation-ID substitution;
- WorkID/status/operation mismatch;
- a v2 record returned through the legacy branch;
- a permissive caller-authored/stubbed consequence not attributable to a
  complete stored fact.

Keep the proven order: exact durable replay first, then eligibility, then
verifier/authority/fence/Git/store mutation. The SQLite implementation remains
WP08-owned; WP06 can define the honest typed port and explicit legacy adapter
contract without changing frozen schema or SQL.

## 2. Addition-only semantic drift cannot publish a successor (blocker)

`ReconcileMixed` rejects every changed report whose affected-old set is empty.
That is stricter than the mission law. An independent new work item is a valid
executable graph change: drift correctly reports one affected new WorkID and no
affected old WorkID, so the exact complete disposition set is empty. There is no
old item for which a `continue | supersede | invalidate | fork` decision can or
should be invented.

An independent temporary production-path probe built a valid generation-1
revision, added one independent normalized work item in generation 2, detected
`changed` with `affected_old=[]` and `affected_new=[work-c]`, then called
`ReconcileMixed` with the complete empty status/decision collections. It failed:

```text
invalid_reconciliation: mixed reconciliation requires semantic drift affecting prior work
```

The probe was removed. This prevents legitimate graph growth even though T029
and T030 explicitly require work addition to be detected and represented, and
T031 requires decisions for affected prior work rather than manufacturing a
prior identity for new-only work.

### Required repair

Permit `SemanticChanged` reports with a non-empty affected-new set and an empty
affected-old set. Require exact empty status and decision collections in that
case, retain the successor/report identity and CAS rules, and publish no old-run
authority action. Add permanent independent-addition controls plus mutants for
an invented status/decision, unknown new identity, and clean/observation-only
reports. Keep rejecting a changed report whose affected old and new sets are
both empty.

## Verified behavior and gate evidence

The remainder of the review was materially strong:

- drift separates opaque provider revision noise from semantic change, detects
  equal-time mutations, covers every `RevisionV2` executable field, and reports
  bounded commitment-only diagnostics;
- dependency ancestry and collapsed-lane changes produce deterministic affected
  sets without pulling in an independent component;
- mixed decisions reject missing, duplicate, unknown, extra, malformed,
  incompatible-continue, and fork-identity mutants and defensively copy their
  collections;
- successor publication binds exact old/candidate v2 bytes, complete mission
  snapshot, drift, plan, actions, operation, request, actor, governance, and time;
  replay, substitution, precommit/lost-response, CAS, and concurrent same-op
  tests pass;
- integration performs an exact immutable approved-review lookup before
  eligibility, and all three consumers perform durable replay before eligibility
  and eligibility before downstream mutation;
- focused five-package suites pass at `-count=20`;
- focused five-package race suites pass;
- architecture suite passes, including the load-bearing exact
  `workgraph -> tracker` denial while `mission -> tracker` remains legal;
- `go vet` and `staticcheck` pass for all affected production packages;
- `internal/workgraph` has no tracker/controlstore/consumer dependency;
- product commits `37823ff`, `f85bb2f`, `73bca89`, and `bb0db64` change exactly
  the fifteen owned product files; `internal/controlstore` is byte-identical to
  base tree `54a0fd570c77682b863e25e7bd149a5d7f3ebc7f`;
- `internal/scenario` is byte-identical before and after the product commits
  (`cf52671e1bbbead61b728bf110d77847424e4e12`), so the known full-census/10-minute
  baseline is non-attributable to WP06; the previously isolated IG-02 evidence
  therefore remains applicable;
- topology cleanup `29e965e` changes only `kitty-specs` mission projection,
  status, baseline, and review/planning artifacts; it does not alter product,
  SQL, Git, filesystem, workspace, or lease code.

All temporary reviewer probes were removed.

## Anti-pattern checklist

1. Dead code: **FAIL** — complete mixed `EligibilityFact` derivation is detached
   from every production consumer.
2. Synthetic-fixture test: **FAIL** — current consumer tests exercise only the
   incomplete legacy tuple; no complete-fact substitution mutant exists.
3. Silent empty return: **PASS** — failures are typed and bounded.
4. FR coverage: **FAIL** — T033 is not wired end to end, and independent
   addition-only drift cannot reconcile.
5. Frozen surface: **PASS** — v1 compatibility and frozen SQLite/controlstore
   code remain unchanged.
6. Locked decision: **PASS** — provider neutrality, immutable revisions, mixed
   decisions, atomic publication, and no direct authority side effects remain
   intact.
7. Shared-file ownership: **PASS** — the product net diff is exactly the fifteen
   authorized files and topology artifacts are separate.
8. Production fragility: **PASS** — no panic, secret leak, tracker coupling, or
   new filesystem/Git/lease side effect was found.
