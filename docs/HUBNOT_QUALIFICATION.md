# Hubnot qualification

This procedure qualifies the compiled Go Kitty tracker against the pinned Hubnot CLI. It does not initialize or bind the real project. All provider writes occur in disposable repositories and identities; the original repository is read only for preservation. Tracker facts remain descriptive: assignment grants no lease, readiness grants no execution permission, and signed comments grant no review verdict. M2 remains `release_ready=true`, `adopted=false`; M3/M4 admission and unavailable production-host seams remain separate work.

## Accepted provider and installation

The single admission tuple is returned by `hubnot.QualifiedProviderIdentity()` and the qualifier's `manifest` command:

| Field | Accepted value |
|---|---|
| Hubnot source | `baf350d12accd01d4d7e7221cf859c81b2c9622b` |
| Executable SHA256 | `077058c8be07cfcd28994210ab483f739fb194b6db417f70b455d6c3a3524cf3` |
| Compiler / target | Go 1.26.7, linux/amd64, GOAMD64=v1, CGO_ENABLED=0 |
| Build arguments | `-trimpath -buildvcs=false -mod=readonly -ldflags='-s -w'` |
| Public identity / issue envelopes | `hn.identity/1`, `hn.issue/1` |
| Go binding / request / result | `go-kitty.hubnot-binding/1`, `go-kitty.tracker-request/1`, `go-kitty.tracker-result/1` |

Signed event, CLI envelope and owned work-metadata versions are separate identities. A development version string or a mutable branch containing the feature is insufficient. Use the exact accepted clean Hubnot checkout and a prepopulated module cache. No network download is part of the offline build/qualification recipe.

The following commands use explicit absolute paths. Keep binaries, captures and each attempt directory outside the source checkout. Record the actual exit of every command; a shell transcript alone is not a successful receipt.

```bash
export PATH=/absolute/go1.26.7/bin:/usr/bin:/bin
export GOTOOLCHAIN=local GOENV=off GOWORK=off
export GOFLAGS= GOEXPERIMENT= GOPROXY=off GOSUMDB=off
export GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0

/absolute/go1.26.7/bin/go build -trimpath -buildvcs=false -mod=readonly \
  -ldflags='-s -w' -o /absolute/external/build/hubnot-qualification \
  ./internal/testcampaign/cmd/hubnot-qualification
/absolute/go1.26.7/bin/go build -trimpath -buildvcs=false -mod=readonly \
  -ldflags='-s -w' -o /absolute/external/build/hubnot-spec-kitty ./cmd/spec-kitty
/absolute/external/build/hubnot-qualification manifest
/absolute/external/build/hubnot-qualification install \
  --go-bin /absolute/go1.26.7/bin/go \
  --source /absolute/clean-accepted-hubnot \
  --destination /absolute/external/build/hn
```

The compiled installer checks source, compiler and bytes before admitting the destination; matching existing bytes are idempotent, unrelated existing bytes refuse. It resolves parent-side Git under the fixed system policy, rather than trusting ambient PATH. There is no shell installer and no compiler-basename restriction. `make hubnot-build`, `hubnot-product` and `hubnot-install` provide the equivalent active build/install wiring; see [Hubnot tracker](HUBNOT_TRACKER.md).

## One source-bound full local run

Commit the final implementation and this methodology before measurement. The qualifier verifies a clean exact source tree and independently rebuilds its own/product binaries with the recorded recipe. It records the source, tree, binary digests, environment, compiler, host, commands, isolation and terminal results. A later documentation or runtime-planning commit does not retroactively become the measured commit. Store actual outcomes, times and source identities in immutable external evidence and the review/PR record.

Run the normal external executable directly, once per final candidate:

```bash
/absolute/external/build/hubnot-qualification \
  --repository /absolute/clean-final-go-kitty \
  --product /absolute/external/build/hubnot-spec-kitty \
  --hn /absolute/external/build/hn \
  --go-bin /absolute/go1.26.7/bin/go \
  --original-repository /absolute/original/go-kitty3 \
  --scope full-local --timeout 55m \
  --evidence-dir /absolute/external/new-full-local-attempt
```

A full pass requires all 28 closed rows started and terminal passing, including both preservation rows. The actual normal process exits 0 and `qualification.json` records `scope=full-local`, `verdict=pass`, `full_admissible=true`. Missing prerequisites, skipped selected tests, stale identity, omitted rows, failed sensors or incomplete captures refuse. Keep failed attempts in their original directories; never change their verdict, source or evidence mode.

Linux unprivileged user/network/PID namespaces and the fixed system Git/unshare tools are prerequisites. The actual isolated worker verifies its PID namespace, distinct network namespace and current loopback-only interfaces; it is not admitted by an environment marker. Namespace teardown cleans descendant process groups. Durable launch intent, confirmed starts and bounded streamed input/output survive cancellation, with unknown completion left honestly incomplete.

`hosted-provider` is a fixed 27-row selection excluding only `cutover.local-recovery`. It checks tracked frozen history and every repeatable provider scenario, but is explicitly non-full. `identity-smoke` is a separate five-row preflight. Neither can satisfy full-local acceptance. Local absence never selects a different scope. The actual original nineteen files and historical branch/stash references are required for full local acceptance and are checked again after integration; a hosted receipt cannot stand in for them.

## Independent acceptance audits

After that same fresh full-local run, from its unchanged clean checkout:

```bash
export GO_KITTY_HUBNOT_ACCEPTANCE=1
export GO_KITTY_HUBNOT_ACCEPTANCE_RECEIPT=/absolute/external/new-full-local-attempt/qualification.json
export GO_KITTY_HUBNOT_ACCEPTANCE_COMMIT=FULL_MEASURED_COMMIT
/absolute/go1.26.7/bin/go test -count=1 -timeout=5m -v \
  ./internal/helpercmd \
  -run '^TestHubnotIntegrated(PublicAcceptance|PerformanceEvidence)$'
unset GO_KITTY_HUBNOT_ACCEPTANCE GO_KITTY_HUBNOT_ACCEPTANCE_RECEIPT GO_KITTY_HUBNOT_ACCEPTANCE_COMMIT
```

The tests read the captured public requests/results and verified artifacts; they do not execute another provider fixture or recursively call a campaign. They require the supplied commit and receipt tree to match the current clean checkout, full admission and verified artifacts. Ordinary package runs leave this external audit disabled; final qualification explicitly enables it, and missing inputs then fail. Report ordinary prerequisite skips separately from the two required explicitly executed acceptance tests.

`TestHubnotIntegratedPublicAcceptance` independently checks rich/simple request-to-result fields, native foreign preservation, graph direction/readiness, assignment ancestry, exact author/body/time/signer, historical and persisted-partial recovery, successful no-write sensors and public reconstruction. It also verifies the runner's raw exact selected unit census. The runner is the public behavioral witness; the test is an independent evidence audit, not another claimed public execution.

`TestHubnotIntegratedPerformanceEvidence` reconstructs expected work from captured compiled `define` inputs and their signed original effects, then verifies the public native opening copies and later native edits. It compares all 1,000 current mapped definitions, every ordered criterion/scope/provenance field, precise opaque number lexemes, full opening metadata/current native state, graph and comments. It does not decode the private adapter metadata format or trust one result's digest as its own oracle.

Boundary regressions in these two files reject changed ordered fields/precision, malformed or duplicate JSON, missing attribution, truncated/resumed exec records and confusion between execs and distinct child PIDs. They are audit tests, not synthetic passing qualification matrices.

## Measurement interpretation

The fixture prepares 1,000 complete definitions, 2,000 ordered criteria, 20 attributed comments, 20 blocking edges and 20 unrelated edges outside the timed query. Public template creation plus exact public opening copies preserve actual adapter-generated metadata without 1,000 growing consumer inventories. Every resulting field is compared after the timed compiled inventory.

Inventory admission measures the untraced compiled process, startup, detailed pages, normalization, graph derivation, complete serialization/capture and parsing. It must return the full result within 120 seconds on the recorded host; this is not an unmeasured platform promise. Comparison and setup durations are separate. Default detail pages have 8 rows, yielding 125 successful detail-page execs for 1,000 records. The process-filtered trace is a separate read, not part of the admission time.

Known-reference observation and original-operation recovery are separate rows and untraced timings. Each also records its own later trace:

- `untraced_command` and `traced_command`: zero-based indices into that row's commands, binding identical original argv/input/directory.
- `process_trace`: bounded captured raw trace artifact with bytes/SHA256; `traced_seconds` is separately labeled instrumentation time.
- `exec_counts`: successful `consumer`, `hubnot`, `git`, `other` execs, plus separate `failed_exec` attempts; all are required non-null integers.
- `observed_exec_child_pids`: distinct PIDs with successful execs, excluding the consumer PID. It does not count fork-only children.

The audit rejoins unfinished/resumed execve records by PID and recomputes counts from raw records. Missing or truncated records refuse. It compares complete traced/untraced results, excluding only the consistent valid per-invocation `observed_at` fields; canonical proof bytes, digest, revision, token and all semantic facts remain exact. Original recovery remains historical replay with prior-mutation evidence and unchanged signed refs. The literal process-count obligation comes from the qualification contract and T029; NFR-002 itself asks for separate query measurements and recorded commands/elapsed time.

## Requirement and story evidence map

Paths below are repository-relative. `Public audit` means `TestHubnotIntegratedPublicAcceptance`; `Performance audit` means `TestHubnotIntegratedPerformanceEvidence`. Every row also requires exact source/command/exits in the final receipt and applicable quality logs. This map describes the validation method; it grants no verdict by itself.

| Requirement | Implementation | Concrete final evidence |
|---|---|---|
| FR-001 Exact binding | `internal/tracker/hubnot/binding.go`, `transport.go`; `internal/helpercmd/provider_request.go` | `binding.admitted`, `binding.refusals`; Public audit no-write sensors; installer/transport refusal tests |
| FR-002 Lossless work codec | `internal/tracker/hubnot/codec.go`, `observation.go` | `work.rich-roundtrip`, `performance.inventory-1000`; both audits, `TestHubnotAcceptanceFieldMutants` |
| FR-003 Useful catalog | `internal/tracker/hubnot/catalog.go` | `work.simple-update-guards`, `operation.historical-replay`; Public audit |
| FR-004 Complete definitions | `internal/tracker/hubnot/definition.go`, `operation.go` | `graph.partial-definition`, `work.rich-roundtrip`; Public/Performance audits; guarded generic-update refusal |
| FR-005 Blocking graph | `internal/tracker/hubnot/graph.go` | `graph.lifecycle`, `graph.invalid-suppliers`, `graph.partial-definition`; exact endpoints/ready/current dependencies in both audits |
| FR-006 Assignment | `internal/tracker/hubnot/assignment.go` | `assignment.provenance`, `assignment.refusals`; `TestAssignmentOriginalRecoveryAndRefusals`; Public audit |
| FR-007 Discussion | `internal/tracker/hubnot/discussion.go`, `internal/tracker/discussion_observation.go` | `discussion.attribution`, `discussion.partial-pair`; Public audit, `TestHubnotAcceptanceCommentAttributionMutants` |
| FR-008 Original recovery | `internal/tracker/hubnot/operation.go` | `operation.historical-replay`, `operation.key-attempt-refusal`, `operation.lookup-boundaries`; both audits; `TestActorLookupPreservesPinnedValidatorAndBinding`, `TestOperationLookupFailureNeverReconstructsOrAppends` |
| FR-009 Coherent observation | `internal/tracker/hubnot/observation.go`, `revision.go` | `bounds.snapshot-pages`, `bounds.large-heads`; both audits; `TestInventoryRefusesIncompleteOrContradictoryViews`, `TestKnownObservationRequiresCurrentSignerAndCompletePages` |
| FR-010 Real consumer | `internal/helpercmd/provider.go`, provider request/result DTOs | `integration.compiled-dispatch` and every public row; `TestTrackerEarlyDispatchNeverTouchesHostFactory`; both audits |
| FR-011 Active cutover | `Makefile`, `.github/workflows`, `internal/testcampaign/manifest.go`, current tracker docs | `cutover.active-successor`, binding legacy refusal; final active campaign/quality receipts |
| FR-012 Reconstruction | public replication through `internal/testcampaign/cmd/hubnot-qualification/fixture.go` | `replicas.selected-withheld`, `replicas.reconstruction`; Public audit |
| FR-013 Pinned installation | qualifier `main.go`, `internal/tracker/hubnot/binding.go` | external compiled `install` receipt and installer refusal tests; source/build/byte admission in full run |
| FR-014 Qualification | `internal/testcampaign/hubnot_manifest.go`, `hubnot_evidence.go`, qualifier | all 28 rows, exact raw selected unit census; both audits; full quality/portable conformance |
| FR-015 Existing data | recorded `internal/testcampaign/testdata/hubnot-qualification-reference.json`, qualifier preservation handler | `cutover.local-recovery`, authentic 19 file/old-ref comparisons; separate post-merge recheck |
| NFR-001 Bounded boundary | Hubnot transport/protocol/budget, consumer bounded I/O, qualifier supervision | `bounds.public-input-output`, `bounds.transport`, `bounds.snapshot-pages`, `bounds.large-heads`; actual cancellation/overflow unit census and full captured results |
| NFR-002 Query cost | detailed inventory and original operation lookup | all three `performance.*` rows; Performance audit, `TestHubnotPerformanceTraceCensus` |
| NFR-003 Regression gates | declared Make quality/static/module/build/race targets | complete source-bound `make -k quality`, explicit external all-package build, module/static checks and owned package race logs |
| NFR-004 No-write failures | preflight/operation outcome accounting | Public audit successful bracketing refs; typed unstarted/applied/ambiguous/prior facts in public and injected rows |
| NFR-005 Scanner repair | `internal/architecture/m2_absence*.go` | full M2 census including `TestM2AbsenceMutants`, `TestM2GateSourcesDoNotShelterExecutableBehavior`, `TestM2SplitExecutableTokensFailClosedWithoutOverNormalization` |
| C-001 Authority | unchanged GapDB host/playback, early tracker dispatch | `integration.authority-boundaries`, `integration.compiled-dispatch`, hostile discussion/atomic-refusal row |
| C-002 Honest guarantees | frozen neutral guards, Hubnot capabilities and effects | unguarded catalog/assignment, guarded-definition/atomic/compensation refusals; public/injected census |
| C-003 Public inward boundary | CLI-only bound adapter and architecture reachability | `TestDefaultReachabilityRegistryPassesCurrentRepository`, binding/source/byte checks, all public argv evidence; no private key imports or authority store |
| C-004 Historical integrity | frozen manifests and occurrence disposition | `cutover.frozen-source`, actual 829 objects/modes and preserved receipts; `cutover.local-recovery`; historical validators and post-merge comparison |
| C-005 Program scope | existing `internal/helpercmd/production.go` and program admission boundaries | unchanged M2 adoption fields, exact authority/playback leaves, active docs and independent review; no M3/M4 completion claim |

| Story | Required public workflow and independent oracle |
|---|---|
| 1 Work through one provider | binding + rich/simple work + graph + assignment/discussion; Public audit and all-field Performance audit |
| 2 Recover an uncertain effect | completed response discarded externally, persisted-partial saga/pair, later human edits and original replay; Public audit plus separately labeled injected duplicate/incomplete lookup |
| 3 Collaborative history and authority | distinct real signing actors, exact author/signer, selected replicas, 201 heads/staged resolve, HostFactory and production playback witnesses; Public audit and exact unit census |
| 4 Operate without Beads | compiled install/active cutover, executable environment without retired tools, fresh public reconstruction and authentic historical preservation; full-local rows plus complete quality |

The authority witness `TestQualificationProductionPlaybackMaterializedCheckpoint` uses the actual 15-registration `BuildReducerSetV2` through `RunV2`, exercising mission/runtime/operation families with typed projections and materialized-prefix/suffix equivalence. It does not claim durable checkpoint publication or all 15 state machines tested. `TestPlaybackProductionSurfaceHasStructurallyNoIO` supplies the structural bound; `TestExternalEffectTerminalPlaybackCallsNoProviderV2` and the actual HostFactory tripwire cover their connectable seams. An unused panic object is not evidence.

## Recovery and completion boundaries

Completed-response loss means a real process finished and its captured result was discarded externally before retry. Persisted-partial fixtures retain an actual public opening or attribution child under another real actor, then recover exactly missing effects. They do not claim an in-flight crash. Public divergent-key attempts prove refusal and a unique original; separately labeled injected unit history supplies duplicate/incomplete-lookup adversity. Never fabricate signed refs or bind an arbitrary proxy as the qualified executable.

Public selected synchronization reconstructs accepted work, graph and comments; it does not recover the original private signer. Backup/export/conditional compensation/global guarded update/atomic assignment remain unsupported. Native actor-head checks do not create distributed CAS. Numeric revisions are equality-only; original effects, inventory and current known-work proofs are distinct. Later human-removed edges stay removed during completed historical replay.

Run complete quality and focused package races on the unchanged committed candidate after qualification; preserve all actual failures and exits. Acceptance/merge and post-merge mission review remain independent runtime gates. Verify the original 19 files and recovery refs again after integration. Historical 27/28 pre-cutover receipts and five-row smokes retain their original non-full status and measured source; no new approval relabels them.
