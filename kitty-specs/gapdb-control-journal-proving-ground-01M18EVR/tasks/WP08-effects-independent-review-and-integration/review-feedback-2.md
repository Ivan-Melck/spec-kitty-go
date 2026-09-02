---
affected_files:
  - internal/provingground/effects_test.go
  - internal/provingground/review_test.go
  - internal/provingground/integration_test.go
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_commits:
  - 8c8a855b642809af82f2ca9eaf84da9efac20d37
  - 0d340ff1bdba58253f3a462e358e0a21f8e638fb
reviewer_agent: codex-wp08-reviewer-cycle2
wp_id: WP08
---

# WP08 review feedback — cycle 2

Verdict: **changes requested**. The cycle-1 authority, dispatch, provider,
pre-fence, and bounded-restore defects are materially repaired and their
adversarial controls pass. Two T046 evidence defects remain.

## 1. Blocker — the claimed 100-run competing-integration campaign contains one real trial and 99 completed-operation replays

Location: `internal/provingground/integration_test.go:432-475`.

The fixture and `operation-campaign-exact` item are created once, outside the
100-repetition callback. The first callback races two calls through one fresh
integration operation. After that operation reaches `StatusDone`, every later
callback submits the same exact operation. `integration.Service.Integrate`
returns the durable completed record from `LookupIntegrationOperation` before
eligibility, repository fencing, workspace creation, or Git publication.

Consequently the counters prove 200 successful calls, but they do not prove
100 competing-integration trials. Ninety-nine iterations are replay checks.
This does not satisfy T046/NFR-004 and repeats the anti-vacuity problem from
cycle 1 under a stronger test name.

Required correction:

- Give every repetition a fresh pre-publication state and distinct operation
  identity (a fresh repository/store fixture per repetition is acceptable).
- In every repetition, release two contenders against that fresh state and
  prove one publication fact, convergent durable results, no interleaving, and
  a complete singleton plan authority.
- Make the census distinguish fresh competing trials from replay calls so a
  future reuse of a completed operation fails the test.

The attribution/reassignment campaign does create a fresh journal, runtime,
provider, and coordinator inside every repetition and is non-vacuous.

## 2. High — WP08 universal invariants still do not run after every terminal path

Locations: `internal/provingground/effects_test.go:151`,
`internal/provingground/integration_test.go:66`, and all of
`internal/provingground/review_test.go`.

The general cleanup installed by WP08 fixtures remains only
`assertAuthorityInvariants`, the pre-existing lease/workspace assertion. There
is no cleanup or equivalent terminal assertion for:

- effect intent/operation/event coherence and attributable resolution;
- candidate-to-reserved-authority and criteria/digest coherence;
- reviewer ActorID absence from the immutable contributor set;
- singleton active integration-plan authority; or
- completed integration records agreeing with Git target/ancestry facts.

The two campaign tests perform partial happy-path checks, but they do not run
these invariants after the refusal, ambiguity, crash, recovery, compensation,
review, branch-movement, and competing-plan terminal paths exercised elsewhere
in the WP08 suite. T046 explicitly requires universal invariants after every
terminal path, and cycle-1 feedback called out this exact gap.

Required correction:

- Add focused WP08 invariant assertions to the effect/review/integration test
  fixtures and register them as cleanup so they execute on successful and
  failing terminal paths.
- Cover the five WP08 invariant families above without duplicating WP09's
  aggregate evidence runner.
- Add negative controls proving a falsified effect binding, contributor set,
  plan singleton, or completion/Git fact makes the relevant assertion fail.

## Cycle-1 blocker disposition

- Cross-work substitution and same-HandoffID HR-01 authority mixing: **fixed**.
- Concurrent exact effect dispatch, stale provider revision, full provider
  identity, and guarded compensation response: **fixed**.
- Foreign repository/target/epoch/revision publishing durable plan authority:
  **fixed**.
- Exact operation/event/effect membership, request/result tamper, and global
  32-page/16,000-event/48 MiB fail-closed bounds: **fixed**.
- Attribution/reassignment 100-run evidence: **fixed**.
- Competing-integration 100-run evidence and universal terminal invariants:
  **not fixed**, as detailed above.

## Anti-pattern checklist

1. **Dead code — N/A (staged boundary).** WP09 is explicitly the first
   non-test campaign caller and WP10 the first shipped coordinator composition;
   both are direct downstream WPs. This deferral is now stated in production
   comments and the WP09 prompt.
2. **Synthetic-fixture test — FAIL.** The competing-integration census claims
   100 fresh scenarios but 99 repetitions exercise only completed-operation
   replay.
3. **Silent empty return — PASS.** No silent empty failure return was found.
4. **FR coverage — FAIL.** NFR-004/T046's 100 real competing trials and the
   terminal-path universal invariant requirement remain unproved.
5. **Frozen surface — PASS.** The repair commits modify only the six WP08-owned
   proving-ground files.
6. **Locked decision — PASS.** No SQLite path, force-ready path, provider
   exactly-once claim, or same-contributor waiver was introduced.
7. **Shared-file ownership — N/A.** No shared production file was modified.
8. **Production fragility — PASS.** No new panic/exception-style production
   path was introduced.

## Qualification evidence

Passed:

- `go test ./internal/provingground -count=10`
- `go test -race ./internal/provingground -count=3`
- the four principal concurrency/refusal controls at `-count=100`
- signed external GapDB GJ-05/recovery test using admitted binary SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/provingground/...`
- `staticcheck ./internal/provingground/...`
- `git diff --check 29c3085..0d340ff`

`make scope` remains red only because the architecture profile census omits 31
pre-existing M1 architecture test names. Every architecture test body passes;
`internal/testcampaign/manifest.go` is outside WP08 ownership. This is a real
mission-infrastructure follow-up and the exact census guard must be updated,
not weakened, but it is not an additional WP08 implementation defect.
