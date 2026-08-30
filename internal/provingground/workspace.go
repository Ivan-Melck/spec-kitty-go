package provingground

import (
	"context"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type GitWorkspace interface {
	WorkspacePath(string) (string, error)
	Create(context.Context, workspace.Record) error
	Probe(context.Context, workspace.Record) (workspace.Probe, error)
	Inspect(context.Context, workspace.Record) (gitrepo.Inspection, error)
}

var _ GitWorkspace = (*gitrepo.Repository)(nil)

// Runtime is the production proving-ground composition. It deliberately
// retains execution.Service as the run admission surface and returns the exact
// frozen controljournal authority capsule after durable Git binding.
type Runtime struct {
	core      *service
	execution *execution.Service
}

type WorkspaceSpec struct {
	OperationID  controljournal.OperationID
	WorkspaceID  controljournal.WorkspaceID
	RepositoryID ids.RepositoryID
	LaneID       ids.LaneID
	OwnedBranch  string
	PinnedBase   string
	Now          time.Time
}

type Delivery struct {
	Run       execution.RunCapsule
	Authority controljournal.AuthorityCapsuleV1
}

// NewRuntime accepts only the real admitted application and Git adapters.
// Unit seams remain package-private; production cannot substitute a memory
// authority or a fake filesystem implementation.
func NewRuntime(config Config, app *application.Service, repository *gitrepo.Repository, identities execution.IdentityGenerator, clock execution.Clock) (*Runtime, error) {
	if app == nil || repository == nil || identities == nil || clock == nil {
		return nil, refusal(OutcomeInvalidCommand, "runtime", "admitted application, real Git repository, identities, and clock are required")
	}
	core, err := newService(config, app, repository)
	if err != nil {
		return nil, err
	}
	authority := &executionAuthorityRepository{core: core}
	return &Runtime{core: core, execution: execution.NewService(authority, identities, clock)}, nil
}

// RestoreRuntimeValidated is the restart counterpart to NewRuntime. It keeps
// execution.Service as the caller-facing admission surface while rebuilding
// its repository adapter exclusively from sealed checkpoint playback.
func RestoreRuntimeValidated(ctx context.Context, config Config, app *application.Service, repository *gitrepo.Repository, port storegapdb.Port, restored checkpoint.RestoreResultV1, identities execution.IdentityGenerator, clock execution.Clock) (*Runtime, error) {
	if app == nil || repository == nil || port == nil || identities == nil || clock == nil {
		return nil, refusal(OutcomeInvalidCommand, "restore-runtime", "complete real production composition is required")
	}
	core, err := restoreValidated(ctx, config, app, repository, port, restored)
	if err != nil {
		return nil, err
	}
	authority := &executionAuthorityRepository{core: core}
	return &Runtime{core: core, execution: execution.NewService(authority, identities, clock)}, nil
}

func (r *Runtime) StartWorkspace(ctx context.Context, start execution.StartRequest, spec WorkspaceSpec) (Delivery, error) {
	run, err := r.execution.Start(ctx, start)
	if err != nil {
		return Delivery{}, err
	}
	authority, ok := r.core.Current(controljournal.WorkID(run.WorkID.String()))
	if !ok || authority.LeaseID != controljournal.LeaseID(run.LeaseID.String()) || authority.Generation != uint64(run.Generation) {
		return Delivery{}, refusal(OutcomeIndeterminate, "start-workspace", "retained execution result does not match journal authority")
	}
	binding, err := r.core.EnsureWorkspace(ctx, WorkspaceRequest{
		OperationID: spec.OperationID, Authority: authority, WorkspaceID: spec.WorkspaceID,
		RepositoryID: spec.RepositoryID, LaneID: spec.LaneID, OwnedBranch: spec.OwnedBranch,
		PinnedBase: spec.PinnedBase, Now: spec.Now,
	})
	if err != nil {
		return Delivery{}, err
	}
	run.RepositoryID, run.LaneID, run.WorkspaceID = spec.RepositoryID, spec.LaneID, ids.WorkspaceID(binding.WorkspaceID)
	run.WorkspacePath, run.OwnedBranch, run.VerifiedBase = binding.CanonicalPath, binding.OwnedBranch, binding.PinnedBase
	run.GitDir, run.IndexPath, run.WorkspaceStage = binding.GitDir, binding.IndexPath, workspace.StageReady
	capsule, err := controljournal.NewAuthorityCapsuleV1(controljournal.AuthorityCapsuleV1{
		SchemaVersion: 1, ProjectID: authority.ProjectID, MissionID: authority.MissionID,
		WorkRevisionID: authority.WorkRevisionID, ActorID: authority.ActorID, ProcessID: authority.ProcessID,
		RunID: authority.RunID, LeaseID: authority.LeaseID, WorkspaceID: binding.WorkspaceID,
		RuntimeEpochID: authority.RuntimeEpochID, LeaseGeneration: authority.Generation,
		FenceToken: authority.FenceToken, AuthorityHeadRecordRevision: authority.AuthorityHeadRecordRevision,
	})
	if err != nil {
		return Delivery{}, refusal(OutcomeIndeterminate, "start-workspace", "durable binding could not form the frozen authority capsule")
	}
	return Delivery{Run: run, Authority: capsule}, nil
}

// Authorize projects the exact frozen capsule back onto the current durable
// binding and authority. It never accepts a narrower token-only coordinate.
func (r *Runtime) Authorize(capsule controljournal.AuthorityCapsuleV1, action string, now time.Time) error {
	authority, err := r.authorityFromCapsule(capsule, action)
	if err != nil {
		return err
	}
	return r.core.Authorize(authority, action, now)
}

func (r *Runtime) authorityFromCapsule(capsule controljournal.AuthorityCapsuleV1, action string) (Authority, error) {
	validated, err := controljournal.NewAuthorityCapsuleV1(capsule)
	if err != nil {
		return Authority{}, refusal(OutcomeInvalidCommand, action, "authority capsule is malformed")
	}
	binding, ok := r.core.Binding(validated.WorkspaceID)
	if !ok || binding.Status != "active-bound" || r.core.config.ProjectID != validated.ProjectID || r.core.config.MissionID != validated.MissionID ||
		binding.WorkRevisionID != validated.WorkRevisionID || binding.ActorID != validated.ActorID || binding.ProcessID != validated.ProcessID ||
		binding.RunID != validated.RunID || binding.LeaseID != validated.LeaseID || binding.RuntimeEpochID != validated.RuntimeEpochID ||
		binding.Generation != validated.LeaseGeneration || binding.FenceToken != validated.FenceToken {
		return Authority{}, refusal(OutcomeStaleLease, action, "capsule no longer matches its durable workspace binding")
	}
	authority, ok := r.core.Current(binding.WorkID)
	if !ok || authority.AuthorityHeadRecordRevision != validated.AuthorityHeadRecordRevision {
		return Authority{}, refusal(OutcomeStaleLease, action, "capsule no longer names current authority")
	}
	return authority, nil
}

type executionAuthorityRepository struct{ core *service }

var _ execution.AuthorityRepository = (*executionAuthorityRepository)(nil)

func (r *executionAuthorityRepository) LookupStartOperation(_ context.Context, request execution.StartRequest) (bool, error) {
	state := r.core.works[controljournal.WorkID(request.WorkID.String())]
	if state == nil {
		return false, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	_, ok := state.operations[controljournal.OperationID(request.OperationID.String())]
	return ok, nil
}

func (r *executionAuthorityRepository) Claim(ctx context.Context, request execution.StartRequest, runID ids.RunID, leaseID ids.LeaseID) (execution.RunCapsule, error) {
	state := r.core.works[controljournal.WorkID(request.WorkID.String())]
	if state != nil {
		state.mu.Lock()
		prior, replay := state.operations[controljournal.OperationID(request.OperationID.String())]
		state.mu.Unlock()
		if replay {
			return executionRunCapsule(request, prior, true), nil
		}
	}
	if runID.String() != string(r.core.config.RunID) {
		return execution.RunCapsule{}, refusal(OutcomeInvalidCommand, "claim", "retained execution run identity must equal the admitted umbrella run")
	}
	authority, err := r.core.Claim(ctx, ClaimRequest{
		OperationID: controljournal.OperationID(request.OperationID.String()), WorkRevisionID: controljournal.WorkRevisionID(request.WorkRevisionID.String()),
		RuntimeEpochID: r.core.epoch, WorkID: controljournal.WorkID(request.WorkID.String()), ActorID: controljournal.ActorID(request.ActorID.String()),
		ProcessID: controljournal.ProcessID(request.ProcessID.String()), LeaseID: controljournal.LeaseID(leaseID.String()),
		GovernanceContextID: request.GovernanceContext.ID().String(), GovernanceContextAlgorithm: request.GovernanceContext.Aggregate().Algorithm(), GovernanceContextDigest: request.GovernanceContext.Aggregate().Digest(), Now: request.Now,
	})
	if err != nil {
		return execution.RunCapsule{}, err
	}
	return executionRunCapsule(request, authority, false), nil
}

func executionRunCapsule(request execution.StartRequest, authority Authority, replay bool) execution.RunCapsule {
	commitment, _ := governancecontext.NewCommitment(authority.GovernanceContextAlgorithm, authority.GovernanceContextDigest)
	return execution.RunCapsule{
		SchemaVersion: execution.ResultSchemaVersion, Kind: "run_started", CorrelationID: request.CorrelationID,
		OperationID: request.OperationID, AggregateIDs: map[string]string{"work_id": string(authority.WorkID), "run_id": string(authority.RunID), "lease_id": string(authority.LeaseID)},
		WorkID: ids.WorkID(authority.WorkID), WorkRevisionID: ids.WorkRevisionID(authority.WorkRevisionID), RunID: ids.RunID(authority.RunID),
		GovernanceContextID: ids.GovernanceContextID(authority.GovernanceContextID), GovernanceContextAggregate: commitment,
		LeaseID: ids.LeaseID(authority.LeaseID), Generation: int64(authority.Generation), Token: fmt.Sprint(authority.FenceToken), Deadline: authority.Deadline, Replayed: replay,
	}
}

func (r *executionAuthorityRepository) Heartbeat(ctx context.Context, request execution.HeartbeatRequest) (time.Time, error) {
	authority, ok := r.find(request.LeaseID, request.Generation)
	if !ok {
		return time.Time{}, refusal(OutcomeStaleLease, "heartbeat", "retained execution lease is not current")
	}
	refreshed, err := r.core.Heartbeat(ctx, controljournal.OperationID(request.OperationID.String()), authority, request.Now)
	return refreshed.Deadline, err
}

func (r *executionAuthorityRepository) Expire(ctx context.Context, request execution.ExpireRequest) error {
	authority, ok := r.find(request.LeaseID, request.Generation)
	if !ok {
		return refusal(OutcomeStaleLease, "expire", "retained execution lease is not current")
	}
	return r.core.Expire(ctx, controljournal.OperationID(request.OperationID.String()), authority, request.Now)
}

func (r *executionAuthorityRepository) find(lease ids.LeaseID, generation int64) (Authority, bool) {
	for _, workID := range r.core.config.WorkIDs {
		if current, ok := r.core.Current(workID); ok && current.LeaseID == controljournal.LeaseID(lease.String()) && current.Generation == uint64(generation) {
			return current, true
		}
	}
	return Authority{}, false
}

type WorkspaceRequest struct {
	OperationID  controljournal.OperationID
	Authority    Authority
	WorkspaceID  controljournal.WorkspaceID
	RepositoryID ids.RepositoryID
	LaneID       ids.LaneID
	OwnedBranch  string
	PinnedBase   string
	Now          time.Time
}

type WorkspacePhysical struct {
	Record workspace.Record
	Probe  workspace.Probe
}

type WorkspaceBinding struct {
	WorkspaceID      controljournal.WorkspaceID    `json:"workspace_id"`
	CanonicalPath    string                        `json:"canonical_path"`
	OwnedBranch      string                        `json:"owned_branch"`
	PinnedBase       string                        `json:"pinned_base"`
	GitDir           string                        `json:"git_dir"`
	IndexPath        string                        `json:"index_path"`
	ActorID          controljournal.ActorID        `json:"actor_id"`
	ProcessID        controljournal.ProcessID      `json:"process_id"`
	RunID            controljournal.RunID          `json:"run_id"`
	LeaseID          controljournal.LeaseID        `json:"lease_id"`
	Generation       uint64                        `json:"generation"`
	FenceToken       uint64                        `json:"fence_token"`
	WorkID           controljournal.WorkID         `json:"work_id"`
	WorkRevisionID   controljournal.WorkRevisionID `json:"work_revision_id"`
	RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	LogicalVersion   uint64                        `json:"logical_version"`
	RecordRevision   uint64                        `json:"record_revision"`
	Status           string                        `json:"status"`
	QuarantineReason string                        `json:"quarantine_reason,omitempty"`
	OperationID      controljournal.OperationID    `json:"operation_id"`
}

func (s *service) EnsureWorkspace(ctx context.Context, request WorkspaceRequest) (WorkspaceBinding, error) {
	if s.git == nil {
		return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "Git workspace adapter is absent")
	}
	if err := s.Authorize(request.Authority, "workspace", request.Now); err != nil {
		return WorkspaceBinding{}, err
	}
	if _, err := controljournal.NewOperationID(string(request.OperationID)); err != nil {
		return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace", "operation identity is invalid")
	}
	if _, err := controljournal.NewWorkspaceID(string(request.WorkspaceID)); err != nil {
		return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace", "workspace identity is invalid")
	}
	s.workspaceMu.RLock()
	if replay, ok := s.workspaceOp[request.OperationID]; ok {
		s.workspaceMu.RUnlock()
		if replay.LeaseID != request.Authority.LeaseID || replay.WorkspaceID != request.WorkspaceID {
			return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace", "operation identity was reused for a different binding")
		}
		return replay, nil
	}
	for _, prior := range s.workspaces {
		if prior.WorkspaceID == request.WorkspaceID && (prior.LeaseID != request.Authority.LeaseID || prior.RuntimeEpochID != request.Authority.RuntimeEpochID) {
			s.workspaceMu.RUnlock()
			return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "mutable workspace identity is permanently lease and epoch scoped")
		}
	}
	s.workspaceMu.RUnlock()

	path, err := s.git.WorkspacePath(string(request.WorkspaceID))
	if err != nil {
		return WorkspaceBinding{}, err
	}
	record := workspace.Record{Reservation: workspace.Reservation{
		OperationID: ids.OperationID(request.OperationID), ProjectID: ids.ProjectID(s.config.ProjectID), MissionID: ids.MissionID(s.config.MissionID),
		WorkRevisionID: ids.WorkRevisionID(s.config.WorkRevisionID), WorkID: ids.WorkID(request.Authority.WorkID),
		RunID: ids.RunID(request.Authority.RunID), WorkspaceID: ids.WorkspaceID(request.WorkspaceID), RepositoryID: request.RepositoryID,
		LaneID: request.LaneID, Authority: workspace.Authority{LeaseID: ids.LeaseID(request.Authority.LeaseID), Generation: int64(request.Authority.Generation), Token: fmt.Sprint(request.Authority.FenceToken)},
		OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase, Now: request.Now,
	}, CanonicalPath: path, Stage: workspace.StageReserved, Status: "physical-unbound", CreatedAt: request.Now, UpdatedAt: request.Now}
	// A registered physical workspace without a durable binding is evidence,
	// never a binding source. This check reconstructs GJ-03 after process-memory
	// loss and prevents Create's idempotent path from silently adopting it.
	existing, probeErr := s.git.Probe(ctx, record)
	if probeErr != nil {
		return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "physical workspace facts could not be inspected before creation", "restore Git inspection and retry")
	}
	if existing.Registered {
		return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "registered workspace has no durable binding and cannot be promoted", "preserve the orphan", "retry with a fresh workspace identity")
	}
	if err := s.git.Create(ctx, record); err != nil {
		return WorkspaceBinding{}, err
	}
	probe, err := s.git.Probe(ctx, record)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	if err := s.faults.Reach(ctx, "GJ-03"); err != nil {
		return WorkspaceBinding{}, &Error{Kind: OutcomeAmbiguous, Action: "GJ-03", Detail: "physical workspace exists without durable authority", Recovery: []string{"inspect and preserve the orphan", "retry with a fresh workspace identity"}, Cause: err}
	}
	if !probe.Registered || probe.Head != request.PinnedBase || probe.Branch != request.OwnedBranch || probe.GitDir == "" || probe.IndexPath == "" {
		return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "physical Git facts do not match the requested binding")
	}
	// Creation is outside the journal transaction. Hold the same per-work
	// authority lock from the complete recheck through durable binding and
	// local publication. Expiry takes this lock too, so it either precedes the
	// bind (which refuses) or follows it (and cannot miss quarantine).
	state := s.works[request.Authority.WorkID]
	s.epochMu.RLock()
	state.mu.Lock()
	if err := s.authorizeLocked(state, request.Authority, "workspace-bind", request.Now, s.epoch); err != nil {
		state.mu.Unlock()
		s.epochMu.RUnlock()
		return WorkspaceBinding{}, err
	}
	s.workspaceMu.RLock()
	if replay, ok := s.workspaceOp[request.OperationID]; ok {
		s.workspaceMu.RUnlock()
		state.mu.Unlock()
		s.epochMu.RUnlock()
		return replay, nil
	}
	for _, current := range s.workspaces {
		if current.LeaseID == request.Authority.LeaseID && current.Generation == request.Authority.Generation && current.Status == "active-bound" && current.WorkspaceID != request.WorkspaceID {
			s.workspaceMu.RUnlock()
			state.mu.Unlock()
			s.epochMu.RUnlock()
			return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace-bind", "one lease generation cannot acquire a second mutable workspace", "preserve the physical orphan", "reuse the existing durable binding")
		}
	}
	s.workspaceMu.RUnlock()
	binding := WorkspaceBinding{
		WorkspaceID: request.WorkspaceID, CanonicalPath: path, OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase,
		GitDir: probe.GitDir, IndexPath: probe.IndexPath, ActorID: request.Authority.ActorID, ProcessID: request.Authority.ProcessID,
		RunID: request.Authority.RunID, LeaseID: request.Authority.LeaseID, Generation: request.Authority.Generation,
		FenceToken: request.Authority.FenceToken, WorkID: request.Authority.WorkID, WorkRevisionID: request.Authority.WorkRevisionID,
		RuntimeEpochID: request.Authority.RuntimeEpochID, LogicalVersion: 1, Status: "active-bound", OperationID: request.OperationID,
	}
	revision, err := s.publish(ctx, publication{
		operationID: request.OperationID, commandType: "workspace.bind", eventType: "workspace.bound",
		aggregateKind: "workspace", aggregateID: controljournal.AggregateID(request.WorkspaceID), logical: 1,
		authority: request.Authority, resultType: "workspace.bound", payload: binding,
	})
	if err != nil {
		state.mu.Unlock()
		s.epochMu.RUnlock()
		return WorkspaceBinding{}, err
	}
	binding.RecordRevision = revision
	s.workspaceMu.Lock()
	s.workspaces[request.WorkspaceID] = binding
	s.workspaceOp[request.OperationID] = binding
	s.workspaceMu.Unlock()
	state.mu.Unlock()
	s.epochMu.RUnlock()
	if err := s.faults.Reach(ctx, "GJ-04"); err != nil {
		return WorkspaceBinding{}, &Error{Kind: OutcomeAmbiguous, Action: "GJ-04", Detail: "durable binding exists but capsule delivery was lost", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	return binding, nil
}

type WorkspaceClass string

const (
	WorkspaceActiveBound   WorkspaceClass = "active-bound"
	WorkspaceQuarantined   WorkspaceClass = "quarantined"
	WorkspaceOrphanUnbound WorkspaceClass = "orphan-unbound"
	WorkspaceForeign       WorkspaceClass = "foreign"
	WorkspaceIndeterminate WorkspaceClass = "indeterminate"
)

type WorkspaceReconciliation struct {
	Class            WorkspaceClass
	Inspection       gitrepo.Inspection
	PreservedCommits []string
	OperatorActions  []string
}

func (s *service) ReconcileWorkspace(ctx context.Context, id controljournal.WorkspaceID, physical workspace.Record) WorkspaceReconciliation {
	if s.git == nil {
		return WorkspaceReconciliation{Class: WorkspaceIndeterminate, OperatorActions: []string{"restore the Git inspection adapter"}}
	}
	s.workspaceMu.RLock()
	binding, durable := s.workspaces[id]
	s.workspaceMu.RUnlock()
	inspectionRecord := physical
	if durable {
		inspectionRecord.WorkspaceID = ids.WorkspaceID(binding.WorkspaceID)
		inspectionRecord.CanonicalPath = binding.CanonicalPath
		inspectionRecord.OwnedBranch = binding.OwnedBranch
		inspectionRecord.PinnedBase = binding.PinnedBase
		inspectionRecord.DependencyHead = binding.PinnedBase
		inspectionRecord.ObservedHead = binding.PinnedBase
		inspectionRecord.GitDir = binding.GitDir
		inspectionRecord.IndexPath = binding.IndexPath
	}
	inspection, err := s.git.Inspect(ctx, inspectionRecord)
	if err != nil {
		return WorkspaceReconciliation{Class: WorkspaceIndeterminate, OperatorActions: []string{"inspect Git facts manually", "preserve the path without adoption"}}
	}
	result := WorkspaceReconciliation{Inspection: inspection, PreservedCommits: append([]string(nil), inspection.PreservedCommits...)}
	if !inspection.Known {
		result.Class, result.OperatorActions = WorkspaceForeign, []string{"leave foreign state untouched", "verify canonical repository identity"}
		return result
	}
	if !durable {
		attributable := physical.WorkspaceID == ids.WorkspaceID(id) &&
			physical.ProjectID == ids.ProjectID(s.config.ProjectID) &&
			physical.MissionID == ids.MissionID(s.config.MissionID) &&
			physical.WorkRevisionID == ids.WorkRevisionID(s.config.WorkRevisionID) &&
			physical.RunID == ids.RunID(s.config.RunID) && physical.OperationID != "" &&
			physical.CanonicalPath != "" && physical.OwnedBranch != "" && physical.PinnedBase != ""
		if attributable && inspection.Registered && len(inspection.Discrepancies) == 0 {
			result.Class, result.OperatorActions = WorkspaceOrphanUnbound, []string{"preserve attributable commits", "quarantine or explicitly collect the orphan", "retry with a fresh workspace identity"}
			return result
		}
		result.Class, result.OperatorActions = WorkspaceIndeterminate, []string{"preserve the path", "resolve missing registration or attribution manually"}
		return result
	}
	if binding.Status == "quarantined" {
		result.Class, result.OperatorActions = WorkspaceQuarantined, []string{"preserve stale evidence", "reconcile explicitly; never transfer this workspace"}
		return result
	}
	if !inspection.Registered || len(inspection.Discrepancies) != 0 {
		result.Class, result.OperatorActions = WorkspaceIndeterminate, []string{"preserve the workspace", "resolve branch, HEAD, registration, and durable-binding discrepancies"}
		return result
	}
	result.Class, result.OperatorActions = WorkspaceActiveBound, []string{"continue only with the current lease capsule"}
	return result
}

func (s *service) Binding(id controljournal.WorkspaceID) (WorkspaceBinding, bool) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	binding, ok := s.workspaces[id]
	return binding, ok
}
