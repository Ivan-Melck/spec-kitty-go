# Parity: orchestrator-api (area I)

Go implementation of upstream `spec-kitty orchestrator-api` (4.0.0rc8,
`specify_cli.orchestrator_api`, contract version **1.18.0**): the JSON-only
machine contract external orchestrators drive. Every stdout emission is one
`json.dumps` envelope line

    {"contract_version", "command": "orchestrator-api.<verb>", "timestamp",
     "correlation_id": "corr-<uuid4 hex>", "success", "error_code", "data"}

including parser-level failures (`USAGE_ERROR`, command `orchestrator-api.unknown`,
exit 2) and the bare group (`Missing command.`). Failures exit 1 with
`data = {**details, "message": <canonical message>}`.

Code: `internal/commands/orchestrator` (verbs, registered from
`cmd/spec-kitty/imports_orchestrator.go`) and the library
`internal/orchestrator` (envelope, policy, contract, mission resolution,
design-authoring receipts, wps.yaml outline).

Differential tests (`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test
./internal/commands/orchestrator/ -timeout 0`) build both sandboxes with the
oracle (`missionfx.NewOraclePair`, `OracleCalcMission`, oracle-driven setup
steps) and compare exit code, normalized stdout (timestamps, correlation /
history / run ids, mid8s, paths), selected files (status logs, meta.json,
workspace contexts) and the git state (per-branch first-parent histories with
file sets, the sorted subject multiset, branches, `refs/spec-kitty/*`,
worktrees, status). `TestLocalOrchestratorAPI` and the library unit tests run
without the oracle.

## Command status

| Command path | Status | Differential tests | Deviations |
|---|---|---|---|
| `orchestrator-api` (bare group, unknown verb, unknown option, `--help`) | done | `TestDiffEnvelopeAndReads` | none known |
| `orchestrator-api contract-version` (`--provider-version` PEP 440 compare, `--require-capability`, delivery profile) | done | `TestDiffEnvelopeAndReads` | none known |
| `orchestrator-api mission-state` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api list-ready` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api resolve-workspace` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api start-implementation` | done | `TestDiffWPLifecycle`, `TestDiffEnvelopeAndReads`, `TestDiffSingleBranchLoop` (repo-root lane), `TestDiffSingleBranchWriteCheckoutRefusals`, `TestDiffSingleBranchClaimLockHeld` | single_branch write-checkout claim lock (`STATUS_LOCK_HELD`, 120 s bound, holder sidecar) and the repo-root `WRITE_CHECKOUT_WRONG_BRANCH` / `_OCCUPIED` / `_DIRTY` refusals are ported (area OWN, see [owned.md](owned.md)); the claim-ancestry gate checks the recorded planning commit only (approved dependency-lane tips are merged by the allocator but not re-verified) |
| `orchestrator-api start-review` | done | `TestDiffWPLifecycle`, `TestDiffLanesTopologyLoop` | none known |
| `orchestrator-api transition` (incl. raw `--to claimed` allocation, `--force`/`--note`, review-result / evidence JSON) | done | `TestDiffWPLifecycle`, `TestDiffLanesTopologyLoop` | none known |
| `orchestrator-api append-history` | done | `TestDiffWPLifecycle`, `TestDiffSingleBranchLoop` | none known |
| `orchestrator-api accept-mission` | done | `TestDiffAcceptAndConsolidateMission` (not ready), `TestDiffAcceptMissionAccepted`, `TestDiffCloseoutOriginAndGates` (origin refusal `MISSION_NOT_READY` + warn-mode `origin_warnings`, local bare remote) | — |
| `orchestrator-api consolidate-mission` (`merge`/`squash`/`rebase`, preflight, lane merges with stale auto-rebase, done transitions, cleanup) | done | `TestDiffAcceptAndConsolidateMission` (merge, coord), `TestDiffAcceptMissionAccepted` (squash), `TestDiffLanesTopologyLoop` (refusal, lanes topology), `TestDiffCloseoutOriginAndGates` (origin-freshness gate and merge gates), `TestDiffConsolidateMissionRefusals` (approved bound #5668: `LANE_MOVED_AFTER_APPROVAL` with `preflight_error_code(s)`; protected status target #5385: `PROTECTED_BRANCH_REFUSED: ... Next step: ...`) | none known |
| `orchestrator-api specify` | done | `TestDiffDesignAuthoring` | none known |
| `orchestrator-api plan` | done | `TestDiffDesignAuthoring`, `TestDiffDesignPhaseNative` | none known |
| `orchestrator-api tasks` (incl. an API-authored wps.yaml outline + WP prompts without a hand-written tasks.md, outline-only refusal, stale-receipt refusal, receipt re-stamp, authoring lock) | done | `TestDiffDesignPhaseNative`, `TestDiffDesignTasksFromOutline`, `TestDiffNativeFinalizeAPIAuthored` | none known |
| `orchestrator-api check-prerequisites` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignPhaseNative` | none known |
| `orchestrator-api record-analysis` | done | `TestDiffDesignPhaseNative`, `TestRunWithTimeout` (Go-only: the 10 s watchdog cannot be exercised offline against the oracle) | the bounded write runs the host `agent mission record-analysis` in-process; a call that outlives 10 s is left running and the failure envelope carries `underlying_call_timed_out` |
| `orchestrator-api open-decision` / `resolve-decision` / `defer-decision` / `cancel-decision` | done | `TestDiffDesignAuthoring`, `TestDiffOwnedCharterAndOrchestratorDecisions` | `--owned-checkout` (and flagless adoption) resolved through `ownedcheckout.ResolveOrAdopt`; unregistered refusal codes fold into `DECISION_OPERATION_FAILED` + `unregistered_error_code` as upstream; see [owned.md](owned.md) |
| `orchestrator-api answer-decision` | done | `TestDiffDesignPhaseNative`, `TestDiffAnswerDecisionNative` (real pending `requires_inputs` decision of a custom mission) | validation (`POLICY_*`, `RESULT_REQUIRED`, `INVALID_RESULT`, `NO_PENDING_DECISION`, `AMBIGUOUS_PENDING_DECISION`, `DECISION_NOT_PENDING`) matches; the answer is delegated to native `next --answer` (payload minus `answer`/`answered`, plus `answered_decision_id`, then `warnings`) |
| `orchestrator-api next` | done | `TestDiffDesignPhaseNative`, `TestDiffAnswerDecisionNative` | none known (delegates to native `next`, `--answer` included) |
| `orchestrator-api design-status` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignAuthoring` | none known |
| `orchestrator-api design-context` (incl. `required_artifacts` through the org-pack tier: last configured pack wins, schema-validated model dump, malformed / schema-invalid manifests fail) | done | `TestDiffDesignAuthoring` (context digest byte-equal), `TestDiffDesignContextOrgExpectedArtifacts` | the loader failures are uncaught upstream (rich traceback); Go prints rich's framed traceback with the same final exception line (frames sourceless, PYF-01 sweep) |
| `orchestrator-api design-validate` | done | `TestDiffDesignAuthoring`, `TestDiffEnvelopeAndReads` | none known |
| `orchestrator-api interview-record` | done | `TestDiffDesignAuthoring` | none known |
| `orchestrator-api artifact-read` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignAuthoring`, `TestAuthoringLock` (Go-only: contention needs two processes) | none known (the authoring lock is a real reentrant flock, `DESIGN_LOCK_TIMEOUT` after 10 s) |
| `orchestrator-api artifact-submit` | done | `TestDiffDesignAuthoring`, `TestDiffDesignTasksFromOutline` | parent checks iterate specification, plan, outline in that fixed order (irreducible: upstream iterates a hash-randomized Python set) |

General deviations:

* Mission resolution uses the shared handle resolver; an ambiguous handle
  returns a `MISSION_NOT_FOUND` envelope where upstream crashes with a
  traceback (`MissionSelectorAmbiguous`).
* Lane naming follows upstream exactly: worktree / lane branch / workspace
  context names key on the raw `--mission` handle; status events are stamped
  with the raw handle.

Help text: every verb carries upstream's docstring (first paragraph as the
listing line, full docstring on its help page, `help_text.go` generated from
the upstream sources); flags, metavars, defaults and choices follow
`inventory/help`. All 28 orchestrator-api help pages are byte-identical to
the inventory (`go test ./internal/helpparity`).

## Irreducible differences (design / planning verbs)

* `artifact-submit`: when several parents are stale, the one reported first
  follows a fixed order (specification, plan, outline); upstream iterates a
  Python `set`, whose order is hash-randomized per process.

## Remaining work (design / planning verbs)

None. `--owned-checkout` on the decision verbs and the
`start-implementation` single_branch write-checkout refusals / claim lock
are ported (see [owned.md](owned.md)); an ambiguous `--mission` handle
crashes with upstream's `MissionSelectorAmbiguous` traceback
(`orchestrator.SelectorAmbiguousCrash`, `TestDiffAmbiguousMissionHandle`).

## Exported API (`internal/orchestrator`)

* Envelope: `ContractVersion`, `MinProviderVersion`, `JSON` (json.dumps
  options), `Envelope(command, success, data, errorCode)`, `Emit(w, env)`,
  `Failure(message, data)`, `Fail(w, command, code, message, data)`,
  `UsageError(w, message)`, `NewCorrelationID`, `NewHistoryEntryID`.
* Contract: `Commands` (verb order), `AllowedErrorCodes`,
  `IsAllowedErrorCode`, `DeliveryProfile()`, authoring limits
  (`MaxArtifactBytes`, `MaxBatchBytes`, `MaxRequestBytes`, `MaxActorBytes`,
  `MaxArtifacts`, `ContextBytes`, `InterviewAnswerMax`, `InterviewActorMax`).
* Policy: `ParsePolicy(raw) (*Policy, error)` (`PolicyError` texts are
  upstream's ValueError messages), `Policy.Object()` (policy_to_dict),
  `BannedFlags`.
* Versions: `ParseVersion(s) (*Version, bool)` (PEP 440), `Version.Less`.
* Missions: `ResolveMission(root, handle) (*Mission, error)` (traversal guard
  -> `INVALID_MISSION`, handle resolution, coordination-aware status dir;
  `*ResolveError{Code, Message, Data}`), `Mission{Root, Slug, PrimaryDir,
  StatusDir}`, `Identity(dir)` (mission_slug / mission_number / mission_type
  from that dir's meta.json), `UnsafeSegmentReason`, `MissionNotFoundMessage`,
  `ResolveWPFile`, `WPIDPrefix`.
* Design authoring: `DesignError` / `NewDesignError`, `Absent`, `SHA256Hex`,
  `AuthoringEnabled`, `ReadReceipts`, `EnableAuthoring`, `SaveReceipts`,
  `ReceiptSet` / `Receipt` (pydantic JSON), `AcquireAuthoringLock(root, slug)
  (release func(), error)` / `AuthoringLockPath` (reentrant flock,
  `DESIGN_LOCK_TIMEOUT`), `TasksFinalized` (with the status.json
  snapshot-drift check).
* wps.yaml: `LoadWpsManifest` / `ParseWpsManifest` (`WpsManifest` with
  `ConcernTrackingRequired`, `WPEntry` with `PlanConcernRefs`, `CrossCutting`,
  `DependenciesExplicit`; errors are `*ManifestError` matching
  `ErrManifestInvalid`, whose text is upstream's str(exc) incl. the pydantic
  ValidationError rendering), `CheckConcernRefsCoverage`, `GenerateTasksMD`.
* Expected artifacts: `LoadExpectedArtifacts(root, missionType)` (org-pack
  tier, validated model dump; `*ManifestLoadError`).

## Extensions to other areas' packages (additive, new files)

* `internal/cli/cli.go` (foundation): `Command.UsageErrorHandler`; a usage
  error raised for a command whose ancestor sets it is rendered by the
  handler instead of the boxed click message (backwards compatible: nil for
  every other command).
* `internal/commands/planning/orchestrator_export.go`:
  `RecordAnalysisDirtyPreflight`, `ParseAnalysisVerdict`; the planning
  package also exposes `APIAuthoredFinalizeScope` (installed by
  `internal/commands/orchestrator/finalize_scope.go`).
* `internal/commands/agentcore/orchestrator_export.go`:
  `ParseReviewResultJSON`, `BuildDoneEvidence`.
* `internal/workflow/orchestrator_export.go`: `AllocateLaneWorktreeFor`
  (handle-keyed lane naming), `RefreshReusedLaneContext`, `StartReviewRef`.
* `internal/consolidate/lane_merge.go`: `MergePreflight`, `RunLaneMerge`
  (`LaneMergeOptions`, `LaneMergeError`, `DestructiveRefusal`).
* `internal/charter/orchestrator_export.go`: `ActionGovernanceDisclosure`.
