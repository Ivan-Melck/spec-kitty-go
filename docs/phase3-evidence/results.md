# Phase 3 qualification results

This record maps Phase 3 software-development mission-method claims to the clean
source candidate, exact campaign artifacts, and executable tests. It does not
grant qualification to the later payload or documentation commits.

## Result summary

| Claim | Executable proof | Measured result |
|---|---|---:|
| Drift is complete and cannot silently mutate current authority | `TestMissionMethodSemanticDriftMutations1000` | 1,000/1,000 changed; zero silent mutations |
| Canonical normalization is order-independent | `TestMissionMethodNormalizationAndPureQueries100` | 100/100 byte and digest equivalence |
| Finalization is race-safe across processes and connections | `TestMissionMethodFinalizationProcessRaces100` | 100 winners, 50 exact replays, 50 changed-operation conflicts; 100/100 restart audits |
| Declared publication faults recover to complete aggregates | `TestMissionMethodCrashBoundaryCensus` | 9/9 exact named boundaries started, terminated, and passed |
| Projected Markdown has no authority | `TestMissionMethodProjectionAndGovernanceIndependence100` | 100/100 trials across eight attack kinds; zero authority changes; four fixed mutants killed |
| Governance history retains its original interpretation | `TestMissionMethodProjectionAndGovernanceIndependence100` | 100/100 restart reconstructions |
| `next` is a pure typed query | `TestMissionMethodNormalizationAndPureQueries100` | 100/100; zero writes |
| Durable fake and Beads preserve provider-neutral mission behavior | portable parity plus `TestMissionMethodRealBeadsTwoItemQualification` | pass against both providers |
| Stale authority is checked before eligibility without defeating replay | `TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation` | pass; no downstream mutation |
| Existing Phase 1/2 behavior remains intact | historical `full-scenario` census | 73/73 pass |
| Architecture and scope laws remain executable | architecture and scope profiles with mutants | 40/40 pass |
| The bounded local PR path remains usable | `make quality` | pass in 37.951 s |
| Full portable qualification is complete | `make full-campaign` | pass; 15m 11.246s exhaustive and mission-profile runtime |
| Pinned real provider works under isolated multiwriter load | frozen real-provider runner | 11/11; 1,000/1,000 contention operations |
| Evidence publication is closed and falsifiable | Phase 3 publication validator and permanent documentation/machine/checkout mutants | strict schema v3; broken links, Mermaid, commands, claims, limitations, checksums, history, verdicts, evidence facts, and ancestry are rejected |

## Requirements and constraints

| IDs | Qualification evidence |
|---|---|
| FR-003, C-001, C-002 | Mission state, approved artifacts, immutable revisions, and typed authority facts drive advancement. Projection and tracker edits produce zero mission-authority changes. |
| FR-009, NFR-006, C-007 | The complete two-item executable projection is equal across the durable fake and qualified Beads adapter; provider identity remains observation evidence outside kernel semantics. |
| FR-010, FR-011, FR-012 | Work-quality, coverage, dependency, lane-collapse, cycle, and scope mutants run in the mission acceptance/adversarial and historical profiles. |
| FR-013, FR-015, NFR-002 | Strict immutable WorkRevision v2 publication and 100 canonical normalization repetitions pass. |
| FR-016, NFR-001 | Fixed comparator mutants plus 1,000 deterministic mutations yield structured drift and zero silent current-generation changes. |
| FR-017, FR-018, FR-020, C-011 | Successor generation is immutable, names its predecessor, carries complete per-work dispositions, and preserves integrated history. |
| FR-019 | Handoff, review, and integration bind complete live eligibility facts; replay precedes authority and eligibility inspection, and stale/ineligible work refuses before side effects. |
| FR-021, NFR-009 | One hundred reconstructed action capsules retain exact governance entries, commitments, actor/process, and grant after restart. |
| FR-022, FR-023 | The seven-kind `NextResult` contract is strict; 100 repeated queries write nothing; action operations replay exactly or conflict on changed identity. |
| FR-025, NFR-010 | One hundred deletion, edit, corruption, stale-copy, cross-mission, invalid-byte, and path/symlink projection attacks alter no canonical fact. |
| FR-027 | Fake and real Beads scenarios traverse approved plan, decomposition, generation 1, provider drift, explicit reconciliation, generation 2, and regenerated projection. |
| FR-028 | Transitions, approvals, finalization, drift, reconciliation, actions, outbox, and campaigns expose source-bound machine evidence. |
| NFR-003, NFR-007 | One hundred two-process finalization races pass; the repository race gate passes with no waiver. |
| NFR-004 | Nine transaction-abort, provider-lost-response, prewrite, and postcommit/pre-response boundaries recover without partial aggregates. The mid-transaction process-kill limit is explicit. |
| NFR-005 | Local bounded queries meet executable thresholds. This package publishes no general production SLO or hosted measurement. |
| NFR-008 | Import/reachability/deferred-surface policies and production-shaped mutants prevent trackers, projections, or transports from minting authority. |
| NFR-011 | The local PR-equivalent campaign passed in 37.951 seconds; exhaustive and real-provider work remain scheduled/manual. |
| NFR-012, C-012 | Evidence scans reject credentials, environment dumps, private transcripts, raw provider bodies/databases, and unbounded output. |
| NFR-013 | Typed read models preserve objective, dependencies, criteria, generation, and next legal action in qualification cases. |
| NFR-014 | Negative matrices require typed stable kinds, bounded observed/expected evidence, and recovery categories without raw provider or SQL leakage. |
| C-005, C-006 | Phase 4/5 execution and Phase 6 product surfaces are explicit non-claims. |
| C-008 | The unchanged historical scenario census, including universal authority and stale-writer probes, passes 73/73. |
| C-009, C-010 | SQLite remains a one-machine/filesystem control authority. Gapdb is not adopted. |

## Success criteria

| IDs | Result |
|---|---|
| SC-001–SC-004 | Portable two-item mission, approved artifacts, deterministic decomposition/finalization, and explicit generation evolution pass through production services. |
| SC-005 | 100/100 process races publish exactly one current generation. |
| SC-006 | 9/9 declared recoverable boundaries terminate with a complete prior or successor state. |
| SC-007 | Strict typed `next` algebra and 100 pure repeated reads pass. |
| SC-008 | 100/100 projection attacks produce zero canonical changes. |
| SC-009 | 100/100 governance histories retain original binding and grant semantics. |
| SC-010 | Typed read models and projections preserve complete work comprehension. |
| SC-011 | Architecture mutants for tracker, projection, transport, and authority reachability are killed. |
| SC-012 | Portable, provider, race, static, security, source-binding, privacy, and checksum gates pass on their recorded layers. |

## Source and provider measurements

Portable qualification binds commit
`03afabd6d875dd133d18fa759669498d92e097be`, tree
`6173b2dbce671d6243b14f99414170c8a5929f0f`, with a clean worktree before and
after execution. Go 1.26.5 on Linux/amd64 compiled and tested the application.
`go vet`, staticcheck 2026.1, module verification, formatting, binary builds,
and govulncheck 1.6.0 passed; the vulnerability scan reported zero reachable
findings.

The isolated Beads 1.2.2 run recorded p95 latency of 284.427 ms for 100
preflight observations and 165.528 ms for 200 warm single-item reads. Eight
processes completed 1,000 provider-native creates in 136,971.845 ms with no
collision or duplicate. These are local observations, not service-level
objectives.

## Failed and non-admissible history

Attempts 01–09 remain enumerated in `campaign-census.json`. They include
toolchain/prerequisite failures, two load-sensitive claimant attempts, exact
census failures, and the genuine stale-authority ordering defect. None satisfies
the pass record. The rejected cycle-1 publication and candidate attempts 20 and
21 are also retained: the first lacked a strict reusable publication validator,
and the latter two failed toolchain qualification boundaries. Real-provider
setup attempts 11–16, 23, 27–30, and 33–35 are retained as
non-admissible or diagnostic: they established network, namespace, Git, and
offline module-cache preconditions. Attempt 33 exposed the outer-container libc
mismatch, attempt 34 caught a skipped two-item selector despite a passing
provider runner, and attempt 35 was interrupted by the host restart before
terminal evidence survived. Portable attempt 32 had passed before that restart;
attempt 36 is the canonical clean rerun whose complete artifacts back this
payload. Canonical real-provider attempt 37 passed both the frozen
eleven-scenario runner and the separate durable two-item Phase 3 selector.
Attempt 25 remains the failed synthetic-publication-fixture qualification, and
attempt 17 remained diagnostic only. Review cycle 3 rejected the preceding
publication because its failure-history tail, command grammar, Mermaid subgraph
balance, and reusable validator entrypoint were not yet structurally closed.
Raw bounded artifacts remain outside the repository; published records contain
only safe digests and summaries.
