# WP09 review feedback — cycle 1

Verdict: changes requested. Candidate `b0b1c54` has a sound envelope and useful negative controls, but the current passing evidence is not yet admissible because the decisive fault and focused-campaign results are disconnected from the production behavior they claim to qualify.

## Issue 1 — Blocker: the five passing fault records and eight passing focused summaries are synthetic fixtures

- `faults/runner_test.go:27-40` creates each passing `Record` from `validFacts`, `cleanPort`, and literal artifacts. `TestFiveFaultCutsHaveExecutableProductionGuards` separately shells out to five production tests, but no identity, source, artifact, or observed product fact connects those executions to the passing `Record`.
- `focused/campaign_test.go:22-43` runs all eight named campaigns through `cleanExecution`, which fabricates a sealed `ExternalObservation`, a complete provider trace, and an entirely literal `campaignObservation`. The only real-sidecar test (`TestExternalReceiptCrossesAdmittedSidecarPublicInspection`) repeats `Inspect` 100 times; it is not any of NFR-001/002/003/004/005/007/008/015 and is not coupled to any focused summary.
- This violates the WP's production-binding rule, FR-029's “actual external boundary,” the eight NFR behavior definitions, and the review anti-pattern's synthetic-fixture check. The package runtime (about 0.16 s for all 800 nominal attempts plus the receipt check) is further evidence that no publication, retry, claim, integration, restore, restart, zombie, or reassignment campaign ran.

Replace the literal pass paths with concrete scenario/fault executors that invoke the existing production seams and derive facts through public observations. The real execution must itself produce the `Record`/`Summary`; a separately passing named test is not provenance. Where an exact external dependency is unavailable, emit terminal `incomplete`, never a synthetic pass. Add a mutation proving that deleting/bypassing each production scenario makes its named summary non-pass.

## Issue 2 — High: universal journal and stale-epoch invariants accept invalid evidence

Temporary adversarial tests demonstrated both cases:

1. Appending a second operation that reuses the first operation's EventIDs, aggregate logical versions, and event identities still leaves `JournalComplete == true`. `assertJournalAndPlayback` checks uniqueness only inside one operation and selects `latest` only on `>`, so duplicate/replayed cross-operation identities survive.
2. The standard observation declares predecessor `epoch-1`, but the standard probe sets only `LeaseStale`; `StaleAuthorityRefused` still passes. When a predecessor epoch exists, NFR-007 requires an old-epoch capsule probe, not merely some stale lease probe.

Enforce global EventID uniqueness, valid per-aggregate logical-version progression/uniqueness and record ordering across operations. Require `EpochStale` when `Epoch.Predecessor` is present, while retaining the lease-stale probe required for lease replacement paths. Add named negative controls for both defects.

## Issue 3 — High: fault fact/source anti-vacuity and terminal invariant coverage are incomplete

Temporary adversarial tests demonstrated all of these green holes:

- `validateFacts("GJ-01", Facts{})` succeeds because two empty maps compare equal. GJ-01 explicitly requires exact preservation of preexisting revisions/digests rather than global emptiness.
- GJ-02 accepts a 64-character non-hex `ProxySHA256` and a `ProxyCut.SourceCommit` unrelated to `Record.Source.Commit`; `validateFacts` has no source argument with which to bind them.
- Invalid-source, missing-executor, missing-RunID, and unknown-fault early returns are terminal records with zero invariant assertions. The law is universal invariants after every terminal attempt; inability to inspect must yield the exact eight false assertions, not an absent census. The existing panic test explicitly exempts one such omission.

Require an explicitly inspected, nonempty preexisting GJ-01 census; bind GJ-02 cut source/proxy/provenance to the record source with real hex/digest checks; and initialize every terminal record with the exact failed invariant census before any early return. Then remove the panic-path exemption and add mutations for empty GJ-01 facts, foreign GJ-02 source, malformed proxy digest, and every pre-execution terminal path.

## Issue 4 — High: source binding is caller-asserted rather than observed

`validateSource` proves only hex shape plus one hard-coded tuple digest. Tests pass fabricated `aaaa…` commit and `bbbb…` tree values. The focused summaries contain no source/tree binding at all. Consequently, a result can claim arbitrary well-shaped source identities, and the production guard subprocess can execute a different checkout from the record it supposedly proves.

Derive commit/tree and admitted sidecar provenance from the actual execution environment, bind subprocess/external observations to those values, and reject caller-only identity assertions. Preserve these bindings in each child result so WP10 can assemble rather than invent the umbrella provenance.

## Issue 5 — Medium: an empty first-failure artifact allows later failures to overwrite first-failure evidence

`Summary.record` uses `FirstFailure == nil` as the “not captured” sentinel. A legitimate first failure with no artifact appends an empty/nil slice, so a later failure replaces both `FirstFailure` and `FirstDiagnostic`. A temporary test recorded diagnostics `first` then `second` and observed `second` as the alleged first failure.

Track first-failure capture with an explicit boolean (or attempt index), preserve the first diagnostic even when artifact bytes are absent, and test empty-artifact followed by nonempty-artifact failures.

## Required mutation and gate evidence observed

- Omitting `AssertUniversal` made focused qualification fail.
- Replacing one mutant's named guard with a no-op/nonexistent test produced `not_run` and failed the aggregate mutant test.
- Shortening the eight focused campaigns to 99 prevented every summary from passing.
- Wrong, missing, multiply reached, and fake-timeout cuts all failed.
- With `SPEC_KITTY_M1_GAPDBD=/tmp/go-kitty3-gapdb-admission.cOGcrB/gapdbd`, `go test ./internal/testcampaign/m1/... -count=1` and the same package set under `-race` passed.
- `go vet ./internal/testcampaign/m1/...` and `staticcheck ./internal/testcampaign/m1/...` passed.
- The repository-wide short campaign is known to exceed Go's default 10-minute package timeout; the isolated scenario evidence reports PASS at 1164.924 s. That timing limitation is recorded honestly and is not itself classified as a candidate defect.
- `make scope` could not start during canonical review because the tool-owned untracked `.spec-kitty/review-lock.json` makes the campaign's clean-source preflight refuse. The candidate's eight owned files were restored byte-for-byte after mutations and `git diff --check` passed.

## WP anti-pattern checklist

1. Dead code: **FAIL** — the new exported qualification APIs have no non-test caller yet. This can be resolved by the concrete WP09 executors and/or the WP10 composition caller, but approval now would leave only test callers.
2. Synthetic-fixture test: **FAIL** — blocking Issue 1.
3. Silent empty return: **PASS** — no unexplained silent empty production return found; retained-loop `return nil` is documented.
4. FR coverage: **FAIL** — FR-029 and the named NFRs have literal envelope assertions, not production-behavior assertions.
5. Frozen surface: **PASS** — candidate modifies exactly the eight WP09-owned files.
6. Locked decision: **PASS** — no embedded GapDB owner, SQLite fallback, sleep-based cut, or combined-trial/adoption claim was added.
7. Shared-file ownership: **PASS** — no shared production file was modified.
8. Production fragility: **PASS** — panic recovery is bounded to qualification attempt envelopes; no new fail-open request-handler panic path was added.
