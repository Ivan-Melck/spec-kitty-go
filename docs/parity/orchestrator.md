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
| `orchestrator-api` (bare group, unknown verb, unknown option, `--help`) | done | `TestDiffEnvelopeAndReads` | help layout is the shared `internal/cli` renderer |
| `orchestrator-api contract-version` (`--provider-version` PEP 440 compare, `--require-capability`, delivery profile) | done | `TestDiffEnvelopeAndReads` | none known |
| `orchestrator-api mission-state` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api list-ready` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api resolve-workspace` | done | `TestDiffEnvelopeAndReads`, `TestDiffWPLifecycle` | none known |
| `orchestrator-api start-implementation` | done | `TestDiffWPLifecycle`, `TestDiffEnvelopeAndReads`, `TestDiffSingleBranchLoop` (repo-root lane) | single_branch write-checkout claim lock (`STATUS_LOCK_HELD`) and the repo-root `WRITE_CHECKOUT_*` refusals are not ported; the claim-ancestry gate checks the recorded planning commit only (approved dependency-lane tips are merged by the allocator but not re-verified) |
| `orchestrator-api start-review` | done | `TestDiffWPLifecycle`, `TestDiffLanesTopologyLoop` | none known |
| `orchestrator-api transition` (incl. raw `--to claimed` allocation, `--force`/`--note`, review-result / evidence JSON) | done | `TestDiffWPLifecycle`, `TestDiffLanesTopologyLoop` | none known |
| `orchestrator-api append-history` | done | `TestDiffWPLifecycle`, `TestDiffSingleBranchLoop` | none known |
| `orchestrator-api accept-mission` | done | `TestDiffAcceptAndConsolidateMission` (not ready), `TestDiffAcceptMissionAccepted` | origin-freshness gate is a no-op (no remote contact) |
| `orchestrator-api consolidate-mission` (`merge`/`squash`/`rebase`, preflight, lane merges with stale auto-rebase, done transitions, cleanup) | done | `TestDiffAcceptAndConsolidateMission` (merge, coord), `TestDiffAcceptMissionAccepted` (squash), `TestDiffLanesTopologyLoop` (refusal, lanes topology) | merge gates, the approved-bound (#5668) refusal, protected-status-target refusal and origin gate are not evaluated; `--push` pushes without the origin checks |
| `orchestrator-api specify` | done | `TestDiffDesignAuthoring` | none known |
| `orchestrator-api plan` | done | `TestDiffDesignAuthoring`, `TestDiffDesignPhaseNative` | none known |
| `orchestrator-api tasks` | done | `TestDiffDesignPhaseNative` | inherits finalize-tasks deviations (the wps.yaml manifest flow is not ported, so finalizing an API-authored outline without a hand-written tasks.md is untested); receipts are re-stamped after a successful finalize |
| `orchestrator-api check-prerequisites` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignPhaseNative` | none known |
| `orchestrator-api record-analysis` | done | `TestDiffDesignPhaseNative` | the write runs through the host `agent mission record-analysis` command in-process (no 10 s watchdog thread, so `underlying_call_timed_out` is never set) |
| `orchestrator-api open-decision` / `resolve-decision` / `defer-decision` / `cancel-decision` | done | `TestDiffDesignAuthoring` | `--owned-checkout` refused (`DECISION_OPERATION_FAILED`, `OWNED_OPTION_UNSUPPORTED`; a non-existent path mirrors upstream's `OWNERSHIP_BROKEN_POINTER`) |
| `orchestrator-api answer-decision` | partial | `TestDiffDesignPhaseNative` | validation (`POLICY_*`, `RESULT_REQUIRED`, `INVALID_RESULT`, `NO_PENDING_DECISION`, `AMBIGUOUS_PENDING_DECISION`, `DECISION_NOT_PENDING`) matches; the answer itself is delegated to native `next --answer` (payload minus `answer`/`answered`, plus `answered_decision_id`, then `warnings`); native `next --answer` is not ported in this build, so answering a real pending decision currently fails with `RUNTIME_NEXT_FAILED` |
| `orchestrator-api next` | done | `TestDiffDesignPhaseNative` | inherits native `next` deviations (no `--answer`) |
| `orchestrator-api design-status` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignAuthoring` | none known |
| `orchestrator-api design-context` | done | `TestDiffDesignAuthoring` (context digest byte-equal) | project/org overrides of `expected-artifacts.yaml` not consulted |
| `orchestrator-api design-validate` | done | `TestDiffDesignAuthoring`, `TestDiffEnvelopeAndReads` | none known |
| `orchestrator-api interview-record` | done | `TestDiffDesignAuthoring` | none known |
| `orchestrator-api artifact-read` | done | `TestDiffEnvelopeAndReads`, `TestDiffDesignAuthoring` | the cooperative authoring lock is a touched lock file, not an flock |
| `orchestrator-api artifact-submit` | done | `TestDiffDesignAuthoring` | parent checks iterate specification, plan, outline in that fixed order (upstream iterates a Python set) |

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
`inventory/help`. Layout (column widths, wrapping of long description lines,
multi-line listing entries) is the shared `internal/cli` renderer (area M).

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
  `ReceiptSet` / `Receipt` (pydantic JSON), `TouchAuthoringLock`,
  `TasksFinalized` (with the status.json snapshot-drift check),
  `LoadWpsManifest` / `ParseWpsManifest` (`WpsManifest`, `WPEntry`).

## Extensions to other areas' packages (additive, new files)

* `internal/cli/cli.go` (foundation): `Command.UsageErrorHandler`; a usage
  error raised for a command whose ancestor sets it is rendered by the
  handler instead of the boxed click message (backwards compatible: nil for
  every other command).
* `internal/commands/planning/orchestrator_export.go`:
  `RecordAnalysisDirtyPreflight`, `ParseAnalysisVerdict`.
* `internal/commands/agentcore/orchestrator_export.go`:
  `ParseReviewResultJSON`, `BuildDoneEvidence`.
* `internal/workflow/orchestrator_export.go`: `AllocateLaneWorktreeFor`
  (handle-keyed lane naming), `RefreshReusedLaneContext`, `StartReviewRef`.
* `internal/consolidate/lane_merge.go`: `MergePreflight`, `RunLaneMerge`
  (`LaneMergeOptions`, `LaneMergeError`, `DestructiveRefusal`).
* `internal/charter/orchestrator_export.go`: `ActionGovernanceDisclosure`.
