---
affected_files:
- internal/controlstore/gapdb/adapter.go
- internal/controlstore/gapdb/adapter_test.go
- internal/controlstore/gapdb/external_scenarios_test.go
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: go test ./internal/controlstore/gapdb -run '^TestReviewerProbe'
reviewed_at: '2026-08-30T12:45:34Z'
reviewer_agent: renata-wp03
wp_id: WP03
---

# WP03 review feedback — cycle 1

Verdict: changes requested.

Reviewer: `renata-wp03` (independent of implementer `codex-wp03`)

Implementation reviewed: `0977613930de8ab422e0101579de398b97b786c9`

## Blocking findings

### 1. Durable-only atomic publication accepts a non-durable acknowledgement

Severity: blocker
Location: `internal/controlstore/gapdb/adapter.go:200-228`, `adapter.go:578-583`

`AtomicBatch` is a durable-only port, but `convertMutationResult` checks only
that the returned acknowledgement is valid. A dishonest provider result with
`AckMemory`, `Revision=9`, `DurableThroughRevision=8`, and the expected mutation
count returns success. That violates FR-011/FR-013 and the one-durable-batch law:
the caller can observe success without durable acceptance.

Reviewer probe (removed after execution):

```text
TestReviewerProbeRejectsNonDurableAtomicAcknowledgement
durable-only atomic port accepted non-durable acknowledgement: <nil>
```

Pass the requested/required acknowledgement into result validation. Require
`AtomicBatch` to receive exactly a durable acknowledgement whose durable-through
revision covers the accepted revision. Treat a different acknowledgement as a
typed malformed response, not success. Add a dishonest-provider regression test
that proves no successful result is returned.

### 2. Single-mutation acknowledgement cardinality is not validated

Severity: high
Location: `internal/controlstore/gapdb/adapter.go:166-198`, `adapter.go:578-583`

The condition `expectedCount > 1 && result.MutationCount != expectedCount`
deliberately skips validation when `expectedCount == 1`. A put/CAS/delete result
with `MutationCount=0` is therefore accepted. The real admitted daemon returns
one, so this makes the fake seam less strict than the real path and hides a
malformed/dishonest provider observation.

Reviewer probe (removed after execution):

```text
TestReviewerProbeRejectsMalformedSingleMutationCount
single mutation accepted malformed mutation_count: <nil>
```

Require exact cardinality for every positive expected count. Update unit fakes
to return the public daemon's real `MutationCount=1` behavior, and add zero and
two count refusal cases for each single-mutation route.

### 3. Authority namespace crossover can bypass the substring guard

Severity: high
Location: `internal/controlstore/gapdb/adapter.go:374-385`

The authority guard uses `strings.Contains(key, "/heads/<namespace>/")` rather
than validating the canonical key structure. A review key such as
`spk/v1/project/mission/heads/review/item/heads/lease/nested`, paired with the
caller-supplied lease label/codec, is accepted by `LeaseAuthority` and reaches
the provider. Thus a record in one authority namespace can satisfy another
adapter predicate, contrary to T014.

Reviewer probe (removed after execution):

```text
TestReviewerProbeRefusesNestedAuthorityNamespaceCrossover
review namespace key was accepted as lease authority: <nil>
namespace crossover reached provider 1 time(s)
```

Parse and validate the exact canonical M1 key form (or consume a validated key
capability from the canonical key codec) and compare the authority component at
its defined position. Refuse foreign prefixes, nested namespace segments, and
namespace/codec crossovers before any provider call. Cover all four namespaces
and every pairwise crossover.

### 4. Scan pages undercount the admitted byte budget

Severity: high
Location: `internal/controlstore/gapdb/adapter.go:231-292`

`convertScanPage` counts only `len(key)+len(value)`. The admitted GapDB scan
contract charges 32 bytes of record metadata in addition to key/value bytes,
plus 8 bytes when expiry is present. A 500-record page whose key/value payload
is exactly the product's 8 MiB limit is therefore reported as exactly 8 MiB,
although its admitted provider accounting is 8 MiB + 16,000 bytes (before any
expiry overhead). The product clamp can consequently return a page over its
revision-pinned budget.

Reviewer probe (removed after execution):

```text
TestReviewerProbeScanBudgetUsesProviderRecordAccounting
provider-sized page was undercounted: accounted=8388608 provider=8404608
```

Use the admitted scan-record accounting—or a frozen product formula at least as
strict—and repage before returning. Add exact and plus-one tests with maximum
record count and expiry metadata, and prove the provider is never asked to
publish or expose an over-budget product page.

## Required evidence corrections

### 5. Commit the missing adversarial boundary coverage

Severity: high
Locations: `internal/controlstore/gapdb/adapter_test.go:47-185`,
`external_scenarios_test.go:404-430`

T013/T016/T017 require exact and boundary-plus-one evidence before any provider
call. The current suite covers event/head/intent counts and the raw batch
boundary, but it does not independently prove the 256-mutation boundary, the
12-MiB wire boundary, the 500-record scan boundary, full scan byte accounting,
or all relevant plus-one refusals with a zero-call assertion. The test named
`TestBatchRawAndWireBudgets` never constructs a wire exact/plus-one case; it
only asserts that the constants are below GapDB defaults.

The committed external scan scenario traverses two pages successfully, but it
does not advance the daemon revision between pages to force `SCAN_STALE`, nor
does it restart from an empty cursor and compare the exact expected key set and
constant observed revision. A temporary reviewer test against the admitted
daemon proved that the current adapter surfaces the stale cursor as a scan
conflict and that a full restart can succeed; preserve that behavior in the
committed external scenario so the contract cannot regress.

Add dishonest acknowledgement, slow/deadline, unavailable, malformed, stale,
and before-send/after-send cases where the prompt requires them. Every mutating
transport test must assert one provider call, the original `OperationID`, no
new-ID retry, and the correct recovery action.

## Evidence that passed

- Exact admitted binary: SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`,
  size `5300621`.
- Admission document SHA-256:
  `4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d`.
- Exact public module and sums verified; dependency closure contains only
  `github.com/spec-kitty/gapdb/gapdb` from the provider module. No GapDB
  `internal/server`, `internal/protocol`, SQLite fallback, `replace`, or PATH
  lookup enters the adapter.
- Real external daemon qualification: exact 14/14 named cases passed with the
  documented `-gapdbd` and fresh `-data-root` arguments. The race-enabled 14/14
  run also passed. Every spawned daemon and response-drop proxy terminated.
- Real multi-page stable scan and forced `SCAN_STALE`/full-restart reviewer
  probe passed; the temporary probe was removed.
- Unit tests, race detector, bounded fuzz, complete architecture suite and
  mutant controls, `go mod verify`, `go list -deps`, vet, staticcheck, gofmt,
  and `git diff --check` passed.
- The known architectural Python coverage gate remains nonblocking
  `no_coverage`; it is not evidence against this Go package.

## Anti-pattern checklist

1. Dead code: N/A for this explicitly staged adapter WP; the production adapter
   is exercised through the real public-client external harness and is the
   declared dependency of subsequent WPs.
2. Synthetic-fixture test: PASS for the tests that exist; they invoke production
   adapter methods. Coverage remains incomplete as described above.
3. Silent empty return: PASS.
4. FR coverage: FAIL — FR-013/FR-014 and the dishonest-provider/budget portions
   of FR-039 are not protected by the committed suite.
5. Frozen surface: PASS.
6. Locked decision: FAIL in behavior — non-durable atomic success contradicts
   the one-durable-batch MUST requirement.
7. Shared-file ownership: PASS — architecture-tier coordination was isolated in
   dedicated commits with mutant controls; WP03's implementation surface did
   not rewrite another WP's production files.
8. Production fragility: N/A (Go; no new panic/fail-open production path found).
