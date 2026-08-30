package provingground

import (
	"context"
	"fmt"
	"sync"
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
	authority *executionAuthorityRepository
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
	return &Runtime{core: core, execution: execution.NewService(authority, identities, clock), authority: authority}, nil
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
	return &Runtime{core: core, execution: execution.NewService(authority, identities, clock), authority: authority}, nil
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
	run.RepositoryID, run.LaneID, run.WorkspaceID = binding.RepositoryID, binding.LaneID, ids.WorkspaceID(binding.WorkspaceID)
	run.WorkspacePath, run.OwnedBranch, run.VerifiedBase = binding.CanonicalPath, binding.OwnedBranch, binding.PinnedBase
	run.GitDir, run.IndexPath, run.WorkspaceStage = binding.GitDir, binding.IndexPath, workspace.StageReady
	capsule, err := authorityCapsule(authority, binding)
	if err != nil {
		return Delivery{}, refusal(OutcomeIndeterminate, "start-workspace", "durable binding could not form the frozen authority capsule")
	}
	return Delivery{Run: run, Authority: capsule}, nil
}

func authorityCapsule(authority Authority, binding WorkspaceBinding) (controljournal.AuthorityCapsuleV1, error) {
	return controljournal.NewAuthorityCapsuleV1(controljournal.AuthorityCapsuleV1{
		SchemaVersion: 1, ProjectID: authority.ProjectID, MissionID: authority.MissionID,
		WorkRevisionID: authority.WorkRevisionID, ActorID: authority.ActorID, ProcessID: authority.ProcessID,
		RunID: authority.RunID, LeaseID: authority.LeaseID, WorkspaceID: binding.WorkspaceID,
		RuntimeEpochID: authority.RuntimeEpochID, LeaseGeneration: authority.Generation,
		FenceToken: authority.FenceToken, AuthorityHeadRecordRevision: authority.AuthorityHeadRecordRevision,
	})
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

// Heartbeat refreshes retained execution authority and returns a new exact
// frozen capsule carrying the journal's new authority-head revision.
func (r *Runtime) Heartbeat(ctx context.Context, operationID controljournal.OperationID, capsule controljournal.AuthorityCapsuleV1, now time.Time, ttl time.Duration) (controljournal.AuthorityCapsuleV1, error) {
	exact, err := r.authorityFromCapsule(capsule, "heartbeat")
	if err != nil {
		return controljournal.AuthorityCapsuleV1{}, err
	}
	release, err := r.bindExactAuthority(operationID, exact)
	if err != nil {
		return controljournal.AuthorityCapsuleV1{}, err
	}
	defer release()
	_, err = r.execution.Heartbeat(ctx, execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, OperationID: ids.OperationID(operationID), ActorID: ids.ActorID(capsule.ActorID), ProcessID: ids.ProcessID(capsule.ProcessID), LeaseID: ids.LeaseID(capsule.LeaseID), Generation: int64(capsule.LeaseGeneration), Token: fmt.Sprint(capsule.FenceToken), Now: now, LeaseTTL: ttl})
	if err != nil {
		return controljournal.AuthorityCapsuleV1{}, err
	}
	binding, ok := r.core.Binding(capsule.WorkspaceID)
	authority, current := r.core.Current(binding.WorkID)
	if !ok || !current {
		return controljournal.AuthorityCapsuleV1{}, refusal(OutcomeIndeterminate, "heartbeat", "refreshed authority lost its durable binding")
	}
	return authorityCapsule(authority, binding)
}

// Expire invalidates an exact frozen capsule through retained execution. An
// exact same-operation retry remains valid after sealed process restoration.
func (r *Runtime) Expire(ctx context.Context, operationID controljournal.OperationID, capsule controljournal.AuthorityCapsuleV1, now time.Time) error {
	exact, err := r.authorityFromCapsuleForExpiry(operationID, capsule, "expire")
	if err != nil {
		return err
	}
	release, err := r.bindExactAuthority(operationID, exact)
	if err != nil {
		return err
	}
	defer release()
	return r.execution.Expire(ctx, execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, OperationID: ids.OperationID(operationID), ActorID: ids.ActorID(capsule.ActorID), ProcessID: ids.ProcessID(capsule.ProcessID), LeaseID: ids.LeaseID(capsule.LeaseID), Generation: int64(capsule.LeaseGeneration), Token: fmt.Sprint(capsule.FenceToken), Now: now})
}

func (r *Runtime) bindExactAuthority(operationID controljournal.OperationID, authority Authority) (func(), error) {
	if r.authority == nil {
		return nil, refusal(OutcomeIndeterminate, "authority", "runtime retained authority adapter is unavailable")
	}
	return r.authority.bindExact(operationID, authority)
}

// ReplaceWorkspace is the public lease-replacement lifecycle: expire the
// predecessor capsule, then admit Process C into a fresh lease-scoped W3. It
// never transfers the predecessor's mutable workspace.
func (r *Runtime) ReplaceWorkspace(ctx context.Context, expireOperationID controljournal.OperationID, predecessor controljournal.AuthorityCapsuleV1, expiredAt time.Time, start execution.StartRequest, spec WorkspaceSpec) (Delivery, error) {
	binding, bound := r.core.Binding(predecessor.WorkspaceID)
	_, operationErr := controljournal.NewOperationID(string(expireOperationID))
	_, bindingOperationErr := controljournal.NewOperationID(string(spec.OperationID))
	_, workspaceErr := controljournal.NewWorkspaceID(string(spec.WorkspaceID))
	_, repositoryErr := ids.ParseRepositoryID(spec.RepositoryID.String())
	_, laneErr := ids.ParseLaneID(spec.LaneID.String())
	if _, err := controljournal.NewAuthorityCapsuleV1(predecessor); err != nil || operationErr != nil || bindingOperationErr != nil || workspaceErr != nil || repositoryErr != nil || laneErr != nil ||
		start.Validate() != nil || !bound || binding.Status != "active-bound" || spec.WorkspaceID == predecessor.WorkspaceID || spec.OwnedBranch == "" || spec.PinnedBase == "" || spec.Now.IsZero() || expiredAt.IsZero() ||
		start.ProjectID.String() != string(predecessor.ProjectID) || start.MissionID.String() != string(predecessor.MissionID) || start.WorkRevisionID.String() != string(predecessor.WorkRevisionID) ||
		start.WorkID.String() != string(binding.WorkID) || start.ActorID.String() != string(predecessor.ActorID) || start.ProcessID.String() != string(r.core.config.Actors.ProcessC) {
		return Delivery{}, refusal(OutcomeInvalidCommand, "replace-workspace", "replacement request must prevalidate Actor A/Process C, the same work, and fresh complete workspace coordinates")
	}
	if err := r.Expire(ctx, expireOperationID, predecessor, expiredAt); err != nil {
		return Delivery{}, err
	}
	delivery, err := r.StartWorkspace(ctx, start, spec)
	if err != nil {
		return Delivery{}, err
	}
	if delivery.Authority.ActorID != predecessor.ActorID || delivery.Authority.ProcessID != r.core.config.Actors.ProcessC || delivery.Authority.LeaseID == predecessor.LeaseID || delivery.Authority.WorkspaceID == predecessor.WorkspaceID {
		return Delivery{}, refusal(OutcomeIndeterminate, "replace-workspace", "replacement did not produce Actor A/Process C with fresh lease and workspace coordinates")
	}
	return delivery, nil
}

// ReplaceOwner publishes the owner-restart invalidation using a concrete
// application service admitted for the fresh epoch.
func (r *Runtime) ReplaceOwner(ctx context.Context, operationID controljournal.OperationID, epoch controljournal.RuntimeEpochID, app *application.Service, now time.Time) error {
	if app == nil {
		return refusal(OutcomeInvalidCommand, "restart", "admitted application service is required")
	}
	return r.core.ReplaceRuntime(ctx, operationID, epoch, app, now)
}

func (r *Runtime) ReconcileWorkspace(ctx context.Context, id controljournal.WorkspaceID, physical workspace.Record) WorkspaceReconciliation {
	return r.core.ReconcileWorkspace(ctx, id, physical)
}

func (r *Runtime) Binding(id controljournal.WorkspaceID) (WorkspaceBinding, bool) {
	return r.core.Binding(id)
}

func (r *Runtime) authorityFromCapsuleForExpiry(operationID controljournal.OperationID, capsule controljournal.AuthorityCapsuleV1, action string) (Authority, error) {
	if authority, err := r.authorityFromCapsule(capsule, action); err == nil {
		return authority, nil
	}
	validated, err := controljournal.NewAuthorityCapsuleV1(capsule)
	if err != nil {
		return Authority{}, refusal(OutcomeInvalidCommand, action, "authority capsule is malformed")
	}
	r.core.commandMu.Lock()
	receipt, ok := r.core.invalidations[operationID]
	r.core.commandMu.Unlock()
	binding, bound := r.core.Binding(validated.WorkspaceID)
	if !ok || receipt.Kind != "lease_expired" || !bound || binding.Status != "quarantined" || !capsuleMatchesAuthority(validated, receipt.Authority, binding) {
		return Authority{}, refusal(OutcomeStaleLease, action, "capsule no longer names the exact expired authority")
	}
	return receipt.Authority, nil
}

func capsuleMatchesAuthority(capsule controljournal.AuthorityCapsuleV1, authority Authority, binding WorkspaceBinding) bool {
	return capsule.ProjectID == authority.ProjectID && capsule.MissionID == authority.MissionID && capsule.WorkRevisionID == authority.WorkRevisionID &&
		capsule.ActorID == authority.ActorID && capsule.ProcessID == authority.ProcessID && capsule.RunID == authority.RunID && capsule.LeaseID == authority.LeaseID &&
		capsule.WorkspaceID == binding.WorkspaceID && capsule.RuntimeEpochID == authority.RuntimeEpochID && capsule.LeaseGeneration == authority.Generation &&
		capsule.FenceToken == authority.FenceToken && capsule.AuthorityHeadRecordRevision == authority.AuthorityHeadRecordRevision
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

type executionAuthorityRepository struct {
	core    *service
	exactMu sync.Mutex
	exact   map[controljournal.OperationID]Authority
}

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
	if runID.String() != string(r.core.config.RunID) {
		return execution.RunCapsule{}, refusal(OutcomeInvalidCommand, "claim", "retained execution run identity must equal the admitted umbrella run")
	}
	state := r.core.works[controljournal.WorkID(request.WorkID.String())]
	if state != nil {
		state.mu.Lock()
		prior, replay := state.operations[controljournal.OperationID(request.OperationID.String())]
		state.mu.Unlock()
		if replay {
			if prior.WorkRevisionID != controljournal.WorkRevisionID(request.WorkRevisionID.String()) || prior.WorkID != controljournal.WorkID(request.WorkID.String()) ||
				prior.ActorID != controljournal.ActorID(request.ActorID.String()) || prior.ProcessID != controljournal.ProcessID(request.ProcessID.String()) ||
				prior.RunID != controljournal.RunID(runID.String()) || prior.RuntimeEpochID != r.core.currentEpoch() ||
				prior.GovernanceContextID != request.GovernanceContext.ID().String() || prior.GovernanceContextAlgorithm != request.GovernanceContext.Aggregate().Algorithm() ||
				prior.GovernanceContextDigest != request.GovernanceContext.Aggregate().Digest() || request.LeaseTTL != r.core.config.LeaseTTL || !prior.Deadline.Equal(request.Now.UTC().Add(r.core.config.LeaseTTL)) {
				return execution.RunCapsule{}, refusal(OutcomeInvalidCommand, "claim", "operation identity was reused for a different retained start")
			}
			return executionRunCapsule(request, prior, true), nil
		}
	}
	authority, err := r.core.Claim(ctx, ClaimRequest{
		OperationID: controljournal.OperationID(request.OperationID.String()), WorkRevisionID: controljournal.WorkRevisionID(request.WorkRevisionID.String()),
		RuntimeEpochID: r.core.currentEpoch(), WorkID: controljournal.WorkID(request.WorkID.String()), ActorID: controljournal.ActorID(request.ActorID.String()),
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
	authority, exact := r.exactAuthority(controljournal.OperationID(request.OperationID.String()))
	var err error
	if exact {
		err = validateRetainedCoordinates(authority, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Token, "heartbeat")
	} else {
		authority, err = r.find(request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Token, "heartbeat")
	}
	if err != nil {
		return time.Time{}, err
	}
	refreshed, err := r.core.Heartbeat(ctx, controljournal.OperationID(request.OperationID.String()), authority, request.Now)
	return refreshed.Deadline, err
}

func (r *executionAuthorityRepository) Expire(ctx context.Context, request execution.ExpireRequest) error {
	authority, exact := r.exactAuthority(controljournal.OperationID(request.OperationID.String()))
	var err error
	if exact {
		err = validateRetainedCoordinates(authority, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Token, "expire")
	} else {
		authority, err = r.find(request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Token, "expire")
	}
	if err != nil {
		r.core.commandMu.Lock()
		receipt, replay := r.core.invalidations[controljournal.OperationID(request.OperationID.String())]
		r.core.commandMu.Unlock()
		if !replay || receipt.Kind != "lease_expired" {
			return err
		}
		if coordinateErr := validateRetainedCoordinates(receipt.Authority, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Token, "expire"); coordinateErr != nil {
			return coordinateErr
		}
		authority = receipt.Authority
	}
	return r.core.Expire(ctx, controljournal.OperationID(request.OperationID.String()), authority, request.Now)
}

func (r *executionAuthorityRepository) bindExact(operationID controljournal.OperationID, authority Authority) (func(), error) {
	r.exactMu.Lock()
	defer r.exactMu.Unlock()
	if r.exact == nil {
		r.exact = make(map[controljournal.OperationID]Authority)
	}
	if _, exists := r.exact[operationID]; exists {
		return nil, refusal(OutcomeAuthorityConflict, "authority", "operation already has an exact capsule admission in flight")
	}
	r.exact[operationID] = authority
	return func() {
		r.exactMu.Lock()
		delete(r.exact, operationID)
		r.exactMu.Unlock()
	}, nil
}

func (r *executionAuthorityRepository) exactAuthority(operationID controljournal.OperationID) (Authority, bool) {
	r.exactMu.Lock()
	defer r.exactMu.Unlock()
	authority, ok := r.exact[operationID]
	return authority, ok
}

func (r *executionAuthorityRepository) find(actor ids.ActorID, process ids.ProcessID, lease ids.LeaseID, generation int64, token, action string) (Authority, error) {
	for _, workID := range r.core.config.WorkIDs {
		if current, ok := r.core.Current(workID); ok && current.LeaseID == controljournal.LeaseID(lease.String()) && current.Generation == uint64(generation) {
			if err := validateRetainedCoordinates(current, actor, process, lease, generation, token, action); err != nil {
				return Authority{}, err
			}
			return current, nil
		}
	}
	return Authority{}, refusal(OutcomeStaleLease, action, "retained execution lease is not current")
}

func validateRetainedCoordinates(authority Authority, actor ids.ActorID, process ids.ProcessID, lease ids.LeaseID, generation int64, token, action string) error {
	if authority.ActorID != controljournal.ActorID(actor.String()) || authority.ProcessID != controljournal.ProcessID(process.String()) {
		return refusal(OutcomeActorProcessMismatch, action, "retained execution actor/process coordinate is not current")
	}
	if authority.LeaseID != controljournal.LeaseID(lease.String()) || authority.Generation != uint64(generation) {
		return refusal(OutcomeStaleLease, action, "retained execution lease coordinate is not current")
	}
	if fmt.Sprint(authority.FenceToken) != token {
		return refusal(OutcomeStaleFence, action, "retained execution fence token is not current")
	}
	return nil
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
	WorkspaceID                 controljournal.WorkspaceID    `json:"workspace_id"`
	RepositoryID                ids.RepositoryID              `json:"repository_id"`
	LaneID                      ids.LaneID                    `json:"lane_id"`
	CanonicalPath               string                        `json:"canonical_path"`
	OwnedBranch                 string                        `json:"owned_branch"`
	PinnedBase                  string                        `json:"pinned_base"`
	GitDir                      string                        `json:"git_dir"`
	IndexPath                   string                        `json:"index_path"`
	ActorID                     controljournal.ActorID        `json:"actor_id"`
	ProcessID                   controljournal.ProcessID      `json:"process_id"`
	RunID                       controljournal.RunID          `json:"run_id"`
	LeaseID                     controljournal.LeaseID        `json:"lease_id"`
	Generation                  uint64                        `json:"generation"`
	FenceToken                  uint64                        `json:"fence_token"`
	AuthorityHeadRecordRevision uint64                        `json:"authority_head_record_revision"`
	WorkID                      controljournal.WorkID         `json:"work_id"`
	WorkRevisionID              controljournal.WorkRevisionID `json:"work_revision_id"`
	RuntimeEpochID              controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	LogicalVersion              uint64                        `json:"logical_version"`
	RecordRevision              uint64                        `json:"record_revision"`
	Status                      string                        `json:"status"`
	QuarantineReason            string                        `json:"quarantine_reason,omitempty"`
	OperationID                 controljournal.OperationID    `json:"operation_id"`
	RequestedAt                 time.Time                     `json:"requested_at"`
	CommandDigest               string                        `json:"command_digest"`
}

type workspaceReceipt struct {
	CommandDigest            string
	ApplicationRequestDigest [32]byte
	CanonicalResult          []byte
}

type workspaceCommandIdentity struct {
	OperationID                 controljournal.OperationID    `json:"operation_id"`
	WorkspaceID                 controljournal.WorkspaceID    `json:"workspace_id"`
	RepositoryID                ids.RepositoryID              `json:"repository_id"`
	LaneID                      ids.LaneID                    `json:"lane_id"`
	OwnedBranch                 string                        `json:"owned_branch"`
	PinnedBase                  string                        `json:"pinned_base"`
	ProjectID                   controljournal.ProjectID      `json:"project_id"`
	MissionID                   controljournal.MissionID      `json:"mission_id"`
	WorkRevisionID              controljournal.WorkRevisionID `json:"work_revision_id"`
	WorkID                      controljournal.WorkID         `json:"work_id"`
	ActorID                     controljournal.ActorID        `json:"actor_id"`
	ProcessID                   controljournal.ProcessID      `json:"process_id"`
	RunID                       controljournal.RunID          `json:"run_id"`
	LeaseID                     controljournal.LeaseID        `json:"lease_id"`
	RuntimeEpochID              controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	Generation                  uint64                        `json:"generation"`
	FenceToken                  uint64                        `json:"fence_token"`
	AuthorityHeadRecordRevision uint64                        `json:"authority_head_record_revision"`
	RequestedAt                 time.Time                     `json:"requested_at"`
}

func workspaceCommandDigest(request WorkspaceRequest) (string, error) {
	a := request.Authority
	return canonicalCommandDigest(workspaceCommandIdentity{
		OperationID: request.OperationID, WorkspaceID: request.WorkspaceID, RepositoryID: request.RepositoryID, LaneID: request.LaneID,
		OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase, ProjectID: a.ProjectID, MissionID: a.MissionID,
		WorkRevisionID: a.WorkRevisionID, WorkID: a.WorkID, ActorID: a.ActorID, ProcessID: a.ProcessID, RunID: a.RunID,
		LeaseID: a.LeaseID, RuntimeEpochID: a.RuntimeEpochID, Generation: a.Generation, FenceToken: a.FenceToken,
		AuthorityHeadRecordRevision: a.AuthorityHeadRecordRevision, RequestedAt: request.Now.UTC(),
	})
}

func workspaceRequestFromBinding(config Config, binding WorkspaceBinding) WorkspaceRequest {
	return WorkspaceRequest{OperationID: binding.OperationID, WorkspaceID: binding.WorkspaceID, RepositoryID: binding.RepositoryID, LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Now: binding.RequestedAt, Authority: Authority{
		ProjectID: config.ProjectID, MissionID: config.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID,
		ActorID: binding.ActorID, ProcessID: binding.ProcessID, RunID: binding.RunID, LeaseID: binding.LeaseID,
		RuntimeEpochID: binding.RuntimeEpochID, Generation: binding.Generation, FenceToken: binding.FenceToken,
		AuthorityHeadRecordRevision: binding.AuthorityHeadRecordRevision,
	}}
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
	commandDigest, err := workspaceCommandDigest(request)
	if err != nil || request.Now.IsZero() {
		return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace", "workspace command identity is invalid")
	}
	s.workspaceMu.RLock()
	if replay, ok := s.workspaceOp[request.OperationID]; ok {
		receipt := s.workspaceReceipts[request.OperationID]
		s.workspaceMu.RUnlock()
		applicationDigest, digestErr := applicationCommandDigest(s.config, request.OperationID, "workspace.bind", replay.RuntimeEpochID, receipt.CanonicalResult)
		if replay.CommandDigest != commandDigest || receipt.CommandDigest != commandDigest || digestErr != nil || applicationDigest != receipt.ApplicationRequestDigest {
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
		receipt := s.workspaceReceipts[request.OperationID]
		s.workspaceMu.RUnlock()
		state.mu.Unlock()
		s.epochMu.RUnlock()
		applicationDigest, digestErr := applicationCommandDigest(s.config, request.OperationID, "workspace.bind", replay.RuntimeEpochID, receipt.CanonicalResult)
		if replay.CommandDigest != commandDigest || receipt.CommandDigest != commandDigest || digestErr != nil || applicationDigest != receipt.ApplicationRequestDigest {
			return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace-bind", "operation identity was reused for a different binding")
		}
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
		WorkspaceID: request.WorkspaceID, RepositoryID: request.RepositoryID, LaneID: request.LaneID, CanonicalPath: path, OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase,
		GitDir: probe.GitDir, IndexPath: probe.IndexPath, ActorID: request.Authority.ActorID, ProcessID: request.Authority.ProcessID,
		RunID: request.Authority.RunID, LeaseID: request.Authority.LeaseID, Generation: request.Authority.Generation,
		FenceToken: request.Authority.FenceToken, AuthorityHeadRecordRevision: request.Authority.AuthorityHeadRecordRevision, WorkID: request.Authority.WorkID, WorkRevisionID: request.Authority.WorkRevisionID,
		RuntimeEpochID: request.Authority.RuntimeEpochID, LogicalVersion: 1, Status: "active-bound", OperationID: request.OperationID,
		RequestedAt: request.Now.UTC(), CommandDigest: commandDigest,
	}
	canonicalResult, canonicalErr := canonicalResultBytes(binding)
	if canonicalErr != nil {
		state.mu.Unlock()
		s.epochMu.RUnlock()
		return WorkspaceBinding{}, refusal(OutcomeInvalidCommand, "workspace-bind", "binding result cannot be canonicalized")
	}
	revision, applicationDigest, err := s.publishCommand(ctx, s.applier, s.epoch, request.OperationID, "workspace.bind", "workspace.bound", binding, []publication{{
		operationID: request.OperationID, commandType: "workspace.bind", eventType: "workspace.bound",
		aggregateKind: "workspace", aggregateID: controljournal.AggregateID(request.WorkspaceID), logical: 1,
		authority: request.Authority, resultType: "workspace.bound", payload: binding,
	}})
	if err != nil {
		state.mu.Unlock()
		s.epochMu.RUnlock()
		return WorkspaceBinding{}, err
	}
	binding.RecordRevision = revision
	s.workspaceMu.Lock()
	s.workspaces[request.WorkspaceID] = binding
	s.workspaceOp[request.OperationID] = binding
	s.workspaceReceipts[request.OperationID] = workspaceReceipt{CommandDigest: commandDigest, ApplicationRequestDigest: applicationDigest, CanonicalResult: canonicalResult}
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
