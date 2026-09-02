# WP09 review feedback — cycle 2

**Verdict: changes requested.** The production paths now execute and the
high-contention scenarios are mechanically stable, but several focused
summaries can reach `100/100` without performing the behavior named by their
ratified requirement. The current receipts therefore prove execution of the
harness, not the complete NFR semantics.

## Blocker 1 — Four focused summaries omit required adversarial behavior

Locations: `internal/testcampaign/m1/faults/runner.go:322-383` and
`:707-803`; requirements `spec.md:392-399`.

- `atomic-publication-nfr001` performs one ordinary `Apply` per repetition.
  It does not cross an applicable publication fault, while NFR-001 requires at
  least 100 repetitions of each applicable publication fault.
- `lost-response-idempotency-nfr002` reconciles the equal request only. It
  never reuses the operation ID with a changed digest and therefore does not
  prove the required 100% changed-digest refusal.
- `restart-epoch-nfr007` never restarts the sidecar owner or composes a new
  ready runtime lifetime. It derives an epoch string from `AttemptID`, calls
  `BindRuntimeEpochV1`, and validates two locally rewritten capsules inside the
  same environment. There is no clean/unclean restart census, durable new
  lifetime, readiness proof, or public runtime refusal of the predecessor.
- `zombie-isolation-nfr008` never resumes a stale filesystem writer. It probes
  W1, calls a refused `Runtime.Authorize`, and probes the unchanged W1 again.
  That cannot prove that direct writes in quarantined W1 leave fresh W2
  unaffected—the filesystem-isolation law that motivated lease-scoped
  workspaces.

The focused executor also accepts any nonempty or empty set of caller-chosen
fact names as long as every supplied boolean is true. Add a campaign-specific
semantic validator with an exact required observation census so deleting one
of the behaviors above cannot still mint a sealed receipt. Then execute the
missing behaviors through the public production seams:

1. repeat the applicable GJ publication cuts for NFR-001;
2. test equal retry and changed-digest conflict in every NFR-002 trial;
3. run 100 actual clean/unclean owner/runtime restarts for NFR-007, proving
   ready unique epochs and public predecessor-capsule refusal; and
4. mutate quarantined W1 as a zombie, then prove W2's contents/head/index are
   unchanged while the stale governed operation refuses.

## Blocker 2 — Universal invariant evidence contains synthesized facts

Locations: `internal/testcampaign/m1/invariants.go:456-471`,
`internal/testcampaign/m1/faults/runner.go:1508-1578`, and
`internal/testcampaign/m1/faults/runner.go:502-507`.

- `assertPrimary` checks only that workspace paths differ from the primary
  path and that active worktrees are registered. It never observes or compares
  the primary HEAD, index digest, or tree digest, so it cannot establish
  NFR-017.
- `ObserveM1` hashes its own decoded journal view, assigns that same hash to
  both restore and playback, leaves adapter-call/write counters at zero, and
  sets `StableCanonical=true`. Those are constructed values, not observations
  from `RestoreV1` plus `playback.RunV1`.
- The NFR-004 overlay assigns `ActiveAuthorities: 1`,
  `SuccessfulPublishes: 1`, and copies `winning.PreflightTarget` into both the
  expected and observed-old fields. The literal values make the universal
  integration assertion unable to detect a broken authority census or Git
  preflight observation.

Replace these overlays with read-only facts from the repository snapshot,
durable workflow journal/plan authority, and actual restore/playback result.
In particular, bind primary before/after HEAD/index/tree, use the independently
observed `before` target as `GitObservedOld`, and derive authority/publication
counts from durable records. Add mutant tests that alter each underlying
product fact and demonstrate that the production campaign—not only a literal
fixture port—fails closed.

## High — SourceBinding does not bind the bytes Go actually executes

Location: `internal/testcampaign/m1/invariants.go:57-99`.

`ObserveSource` records `HEAD` and `HEAD^{tree}`, but it never proves that the
working tree from which `go test` compiled matches that tree. A tracked or
untracked source mutation can therefore execute while the receipt retains the
unchanged commit/tree and a valid private seal. Run qualification from a clean
detached source tree or add an exact worktree/build-artifact check before
minting `SourceBinding`; include a regression that changes executable source
without moving HEAD and proves that no pass receipt can be minted.

## Verified strengths

- Exactly five named fault cuts and nineteen distinct named product guards are
  present; the product fault records and guard catalog execute successfully.
- Journal invariant code rejects duplicate event IDs, duplicate/nonmonotonic
  operation revisions, and nonconsecutive aggregate versions; predecessor
  observations require an epoch-stale probe.
- Early terminal records carry all eight false invariant assertions and the
  first literal diagnostic is preserved.
- The GapDB source observation binds the admitted executable/database/PID and
  provenance tuple; literal `validFacts`/`cleanExecution` test fixtures cannot
  mint production receipts.
- The repository-wide worktree metadata lock covers normal creation/cleanup
  and integration creation/cleanup, with typed cancellation and real
  cross-instance tests.
- Refreshed-capsule handoff and candidate-vs-heartbeat one-owner linearization
  tests pass under the race detector.
- WP10 aggregate campaign/evidence/adoption files remain unimplemented here as
  required.

## Independent verification

- Admitted binary SHA-256:
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- `go test` smoke coverage for gitrepo, provingground, invariants, five fault
  records, and nineteen mutants passed. A broad all-focused replay was
  deliberately interrupted at 230 seconds because it had been launched with
  Go's unsuitable default 10-minute timeout; it is not claimed as evidence.
- Explicit timeout-safe production rerun passed:
  `competing-integrations-nfr004` 100/100 in 289.13s and
  `attribution-reassignment-nfr015` 100/100 in 95.95s.
- Targeted `-race -count=3` coverage passed for worktree locking,
  refreshed-capsule/final-publication linearization, production fault receipts,
  the 99-repetition guard, and literal focused-fixture refusal.
- `go vet`, `staticcheck`, `gofmt -l`, `git diff --check`, and
  `go test ./internal/architecture -count=1` passed.

## Anti-pattern checklist

1. Dead code — **PASS**: the new qualification APIs have non-test callers in
   the production campaign executor.
2. Synthetic-fixture test — **FAIL**: primary/playback/integration universal
   observations include constructed pass facts rather than the product facts
   named above.
3. Silent empty return — **PASS**: reviewed zero-value returns are paired with
   typed errors or deliberate empty-domain observations.
4. Requirement coverage — **FAIL**: NFR-001, NFR-002, NFR-007, NFR-008, and
   NFR-017 are not exercised as written.
5. Frozen surface — **PASS**: the implementation commit changes only the 18
   files explicitly reconciled into WP09 ownership.
6. Locked decision — **PASS**: no embedded GapDB server, SQLite substitution,
   CLI prose parsing, or WP10 adoption claim was introduced.
7. Shared-file ownership — **PASS**: gitrepo and review ownership expansions
   are recorded in the WP activity log and lane metadata.
8. Production fragility — **PASS**: new failure paths are typed and fail loud;
   no silent transient-race panic/raise equivalent was introduced.

