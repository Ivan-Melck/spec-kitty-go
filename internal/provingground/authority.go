// Package provingground composes the durable control journal with retained
// execution and workspace concepts. It is an M1 qualification surface, not a
// second storage implementation.
package provingground

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/faults"
)

// OutcomeKind is the bounded proving-ground refusal vocabulary.
type OutcomeKind string

const (
	OutcomeInvalidCommand       OutcomeKind = "invalid_command"
	OutcomeStaleWorkRevision    OutcomeKind = "stale_work_revision"
	OutcomeAuthorityConflict    OutcomeKind = "authority_conflict"
	OutcomeStaleLease           OutcomeKind = "stale_lease"
	OutcomeStaleFence           OutcomeKind = "stale_fence"
	OutcomeStaleEpoch           OutcomeKind = "stale_epoch"
	OutcomeActorProcessMismatch OutcomeKind = "actor_process_mismatch"
	OutcomeWorkspaceNotReady    OutcomeKind = "workspace_not_ready"
	OutcomeAmbiguous            OutcomeKind = "ambiguous"
	OutcomeIndeterminate        OutcomeKind = "indeterminate"
)

type Error struct {
	Kind     OutcomeKind
	Action   string
	Detail   string
	Recovery []string
	Cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("proving ground: %s action=%s detail=%s", e.Kind, e.Action, e.Detail)
}
func (e *Error) Unwrap() error { return e.Cause }

func IsOutcome(err error, kind OutcomeKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}

func refusal(kind OutcomeKind, action, detail string, recovery ...string) error {
	return &Error{Kind: kind, Action: action, Detail: detail, Recovery: append([]string(nil), recovery...)}
}

type batchApplier interface {
	Apply(context.Context, application.ValidatedBatchV1) (application.ResultV1, error)
}

// Compile-time proof that production uses the admitted application service.
var _ batchApplier = (*application.Service)(nil)

type Config struct {
	ProjectID      controljournal.ProjectID
	MissionID      controljournal.MissionID
	WorkRevisionID controljournal.WorkRevisionID
	RunID          controljournal.RunID
	RuntimeEpochID controljournal.RuntimeEpochID
	Actors         controljournal.TrialImplementationActorsV1
	WorkIDs        []controljournal.WorkID
	LeaseTTL       time.Duration
}

type ClaimRequest struct {
	OperationID    controljournal.OperationID
	WorkRevisionID controljournal.WorkRevisionID
	RuntimeEpochID controljournal.RuntimeEpochID
	WorkID         controljournal.WorkID
	ActorID        controljournal.ActorID
	ProcessID      controljournal.ProcessID
	LeaseID        controljournal.LeaseID
	Now            time.Time
}

// Authority is a lease-scoped governed capability. It is intentionally not a
// filesystem capability; direct writes do not consult it.
type Authority struct {
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
	Deadline                    time.Time                     `json:"deadline"`
	Status                      string                        `json:"status"`
	OperationID                 controljournal.OperationID    `json:"operation_id"`
}

type workState struct {
	mu           sync.Mutex
	logical      uint64
	headRevision uint64
	current      *Authority
	lastFence    uint64
	lastGen      uint64
	operations   map[controljournal.OperationID]Authority
}

type Service struct {
	config Config

	epochMu             sync.RWMutex
	epoch               controljournal.RuntimeEpochID
	applier             batchApplier
	faults              faults.Hook
	runtimeLogical      uint64
	runtimeHeadRevision uint64
	predecessorEpoch    controljournal.RuntimeEpochID

	works map[controljournal.WorkID]*workState

	workspaceMu sync.RWMutex
	workspaces  map[controljournal.WorkspaceID]WorkspaceBinding
	workspaceOp map[controljournal.OperationID]WorkspaceBinding
	orphans     map[controljournal.WorkspaceID]WorkspacePhysical
	git         GitWorkspace
}

type RuntimeProjection struct {
	RuntimeEpochID     controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	PredecessorEpoch   controljournal.RuntimeEpochID `json:"predecessor_epoch_id,omitempty"`
	LogicalVersion     uint64                        `json:"logical_version"`
	HeadRecordRevision uint64                        `json:"head_record_revision"`
}

type WorkProjection struct {
	WorkID             controljournal.WorkID `json:"work_id"`
	LogicalVersion     uint64                `json:"logical_version"`
	HeadRecordRevision uint64                `json:"head_record_revision"`
	LastGeneration     uint64                `json:"last_generation"`
	LastFence          uint64                `json:"last_fence"`
	Current            *Authority            `json:"current,omitempty"`
}

// Projection is disposable restored state derived from validated journal or
// checkpoint playback. The journal remains authority; this value never grants
// storage mutation capability by itself.
type Projection struct {
	Runtime    RuntimeProjection  `json:"runtime"`
	Works      []WorkProjection   `json:"works"`
	Workspaces []WorkspaceBinding `json:"workspaces"`
}

func New(config Config, applier batchApplier, git GitWorkspace) (*Service, error) {
	if applier == nil || config.LeaseTTL <= 0 || len(config.WorkIDs) != 2 {
		return nil, refusal(OutcomeInvalidCommand, "new", "applier, positive lease TTL, and exactly two work items are required")
	}
	if _, err := controljournal.NewProjectID(string(config.ProjectID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "project identity is invalid")
	}
	if _, err := controljournal.NewMissionID(string(config.MissionID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "mission identity is invalid")
	}
	if _, err := controljournal.NewWorkRevisionID(string(config.WorkRevisionID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "work revision identity is invalid")
	}
	if _, err := controljournal.NewRunID(string(config.RunID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "run identity is invalid")
	}
	if _, err := controljournal.NewRuntimeEpochID(string(config.RuntimeEpochID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "new", "runtime epoch identity is invalid")
	}
	actors, err := controljournal.NewTrialImplementationActorsV1(config.Actors.ActorA, config.Actors.ActorB, config.Actors.ProcessA, config.Actors.ProcessB, config.Actors.ProcessC)
	if err != nil || actors != config.Actors {
		return nil, refusal(OutcomeInvalidCommand, "new", "trial actor and process vocabulary is invalid")
	}
	works := make(map[controljournal.WorkID]*workState, 2)
	for _, workID := range config.WorkIDs {
		if _, err := controljournal.NewWorkID(string(workID)); err != nil || works[workID] != nil {
			return nil, refusal(OutcomeInvalidCommand, "new", "work identities must be valid and unique")
		}
		works[workID] = &workState{operations: make(map[controljournal.OperationID]Authority)}
	}
	return &Service{
		config: config, epoch: config.RuntimeEpochID, applier: applier, faults: faults.Noop{}, works: works, git: git,
		workspaces:  make(map[controljournal.WorkspaceID]WorkspaceBinding),
		workspaceOp: make(map[controljournal.OperationID]WorkspaceBinding),
		orphans:     make(map[controljournal.WorkspaceID]WorkspacePhysical),
	}, nil
}

// Restore constructs the composition from validated checkpoint/playback
// output. It deliberately accepts semantic projection values, not raw bytes.
func Restore(config Config, applier batchApplier, git GitWorkspace, projection Projection) (*Service, error) {
	service, err := New(config, applier, git)
	if err != nil {
		return nil, err
	}
	if projection.Runtime.RuntimeEpochID != config.RuntimeEpochID ||
		(projection.Runtime.LogicalVersion == 0) != (projection.Runtime.HeadRecordRevision == 0) {
		return nil, refusal(OutcomeInvalidCommand, "restore", "runtime projection is inconsistent with admitted epoch")
	}
	service.runtimeLogical = projection.Runtime.LogicalVersion
	service.runtimeHeadRevision = projection.Runtime.HeadRecordRevision
	service.predecessorEpoch = projection.Runtime.PredecessorEpoch
	seenWorks := make(map[controljournal.WorkID]bool, len(projection.Works))
	for _, restored := range projection.Works {
		state := service.works[restored.WorkID]
		coordinatePair := (restored.LastGeneration == 0) == (restored.LastFence == 0)
		claimPair := (restored.LogicalVersion == 0) == (restored.LastGeneration == 0)
		if state == nil || seenWorks[restored.WorkID] || (restored.LogicalVersion == 0) != (restored.HeadRecordRevision == 0) || !coordinatePair || !claimPair {
			return nil, refusal(OutcomeInvalidCommand, "restore", "work projection is not a valid member of the executable revision")
		}
		seenWorks[restored.WorkID] = true
		state.logical, state.headRevision = restored.LogicalVersion, restored.HeadRecordRevision
		state.lastGen, state.lastFence = restored.LastGeneration, restored.LastFence
		if restored.Current != nil {
			current := *restored.Current
			actor, known := config.Actors.ActorForProcess(current.ProcessID)
			if !known || actor != current.ActorID || current.WorkID != restored.WorkID || current.WorkRevisionID != config.WorkRevisionID || current.RuntimeEpochID != config.RuntimeEpochID || current.RunID != config.RunID || current.Status != "active" || current.AuthorityHeadRecordRevision != restored.HeadRecordRevision || current.Generation == 0 || current.FenceToken == 0 || current.Generation != restored.LastGeneration || current.FenceToken != restored.LastFence {
				return nil, refusal(OutcomeInvalidCommand, "restore", "current authority projection is inconsistent")
			}
			state.current = &current
			state.operations[current.OperationID] = current
		}
	}
	if len(seenWorks) != len(service.works) {
		return nil, refusal(OutcomeInvalidCommand, "restore", "work projection must contain the exact configured work census")
	}
	seenWorkspaceOps := make(map[controljournal.OperationID]bool, len(projection.Workspaces))
	activeLease := make(map[string]bool, len(projection.Workspaces))
	for _, binding := range projection.Workspaces {
		state := service.works[binding.WorkID]
		actor, known := config.Actors.ActorForProcess(binding.ProcessID)
		coordinate := string(binding.LeaseID) + "\x00" + fmt.Sprint(binding.Generation)
		validStatus := binding.Status == "active-bound" || binding.Status == "quarantined"
		if binding.WorkspaceID == "" || binding.OperationID == "" || seenWorkspaceOps[binding.OperationID] || service.workspaces[binding.WorkspaceID].WorkspaceID != "" || state == nil ||
			binding.WorkRevisionID != config.WorkRevisionID || binding.RunID != config.RunID || !known || actor != binding.ActorID || binding.RecordRevision == 0 || binding.Generation == 0 || binding.FenceToken == 0 || binding.LeaseID == "" || binding.CanonicalPath == "" || binding.OwnedBranch == "" || binding.PinnedBase == "" || binding.GitDir == "" || binding.IndexPath == "" || !validStatus {
			return nil, refusal(OutcomeInvalidCommand, "restore", "workspace projection is inconsistent")
		}
		if binding.Status == "active-bound" {
			if activeLease[coordinate] || state.current == nil || state.current.LeaseID != binding.LeaseID || state.current.Generation != binding.Generation || state.current.FenceToken != binding.FenceToken || state.current.ActorID != binding.ActorID || state.current.ProcessID != binding.ProcessID || state.current.RuntimeEpochID != binding.RuntimeEpochID {
				return nil, refusal(OutcomeInvalidCommand, "restore", "active workspace does not match one current authority")
			}
			activeLease[coordinate] = true
		} else if binding.QuarantineReason == "" {
			return nil, refusal(OutcomeInvalidCommand, "restore", "quarantined workspace lacks an attributable reason")
		}
		seenWorkspaceOps[binding.OperationID] = true
		service.workspaces[binding.WorkspaceID] = binding
		service.workspaceOp[binding.OperationID] = binding
	}
	return service, nil
}

func (s *Service) WithFaults(hook faults.Hook) *Service {
	if hook == nil {
		hook = faults.Noop{}
	}
	s.faults = hook
	return s
}

func (s *Service) Claim(ctx context.Context, request ClaimRequest) (Authority, error) {
	state, err := s.validateClaim(request)
	if err != nil {
		return Authority{}, err
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	if request.RuntimeEpochID != s.epoch {
		return Authority{}, refusal(OutcomeStaleEpoch, "claim", "runtime epoch changed before claim publication")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if replay, ok := state.operations[request.OperationID]; ok {
		if replay.WorkID != request.WorkID || replay.ActorID != request.ActorID || replay.ProcessID != request.ProcessID || replay.LeaseID != request.LeaseID {
			return Authority{}, refusal(OutcomeInvalidCommand, "claim", "operation identity was reused for a different claim")
		}
		return replay, nil
	}
	if state.current != nil && state.current.Status == "active" {
		return Authority{}, refusal(OutcomeAuthorityConflict, "claim", "one current authority already exists", "wait for expiry or select other work")
	}
	next := Authority{
		ProjectID: s.config.ProjectID, MissionID: s.config.MissionID, WorkRevisionID: s.config.WorkRevisionID,
		WorkID: request.WorkID, ActorID: request.ActorID, ProcessID: request.ProcessID, RunID: s.config.RunID,
		LeaseID: request.LeaseID, RuntimeEpochID: s.epoch, Generation: state.lastGen + 1,
		FenceToken: state.lastFence + 1, Deadline: request.Now.UTC().Add(s.config.LeaseTTL), Status: "active", OperationID: request.OperationID,
	}
	revision, err := s.publish(ctx, publication{
		operationID: request.OperationID, commandType: "authority.claim", eventType: "authority.claimed",
		aggregateKind: "authority", aggregateID: controljournal.AggregateID(request.WorkID), logical: state.logical + 1,
		observedRevision: state.headRevision, authority: next, resultType: "authority.claimed", payload: next,
	})
	if err != nil {
		return Authority{}, err
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.lastFence, state.lastGen = next.FenceToken, next.Generation
	state.current = &next
	state.operations[request.OperationID] = next
	return next, nil
}

func (s *Service) validateClaim(request ClaimRequest) (*workState, error) {
	if _, err := controljournal.NewOperationID(string(request.OperationID)); err != nil || request.Now.IsZero() {
		return nil, refusal(OutcomeInvalidCommand, "claim", "operation identity and authoritative time are required")
	}
	state := s.works[request.WorkID]
	if state == nil || request.WorkRevisionID != s.config.WorkRevisionID {
		return nil, refusal(OutcomeStaleWorkRevision, "claim", "work is not a member of the immutable executable revision")
	}
	s.epochMu.RLock()
	epoch := s.epoch
	s.epochMu.RUnlock()
	if request.RuntimeEpochID != epoch {
		return nil, refusal(OutcomeStaleEpoch, "claim", "claim belongs to a predecessor runtime epoch")
	}
	actor, known := s.config.Actors.ActorForProcess(request.ProcessID)
	if !known || actor != request.ActorID {
		return nil, refusal(OutcomeActorProcessMismatch, "claim", "process is not bound to the requested implementation actor")
	}
	if _, err := controljournal.NewLeaseID(string(request.LeaseID)); err != nil {
		return nil, refusal(OutcomeInvalidCommand, "claim", "lease identity is invalid")
	}
	return state, nil
}

// Authorize validates every coordinate for heartbeat, handoff, review,
// integration, and any later governed operation. No caller may validate only
// a token or only a process identity.
func (s *Service) Authorize(authority Authority, action string, now time.Time) error {
	state := s.works[authority.WorkID]
	if state == nil || authority.WorkRevisionID != s.config.WorkRevisionID {
		return refusal(OutcomeStaleWorkRevision, action, "work revision is not current")
	}
	s.epochMu.RLock()
	epoch := s.epoch
	s.epochMu.RUnlock()
	if authority.RuntimeEpochID != epoch {
		return refusal(OutcomeStaleEpoch, action, "capsule belongs to a predecessor runtime epoch")
	}
	actor, known := s.config.Actors.ActorForProcess(authority.ProcessID)
	if !known || actor != authority.ActorID {
		return refusal(OutcomeActorProcessMismatch, action, "actor and process do not match")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return s.authorizeLocked(state, authority, action, now, epoch)
}

func (s *Service) authorizeLocked(state *workState, authority Authority, action string, now time.Time, epoch controljournal.RuntimeEpochID) error {
	current := state.current
	if current == nil || current.Status != "active" || authority.LeaseID != current.LeaseID || authority.Generation != current.Generation || authority.AuthorityHeadRecordRevision != current.AuthorityHeadRecordRevision {
		return refusal(OutcomeStaleLease, action, "lease is not current")
	}
	if authority.FenceToken != current.FenceToken {
		return refusal(OutcomeStaleFence, action, "fence is not current")
	}
	if authority.ActorID != current.ActorID || authority.ProcessID != current.ProcessID || authority.RunID != current.RunID {
		return refusal(OutcomeActorProcessMismatch, action, "capsule execution identity is not current")
	}
	if now.IsZero() || !now.Before(current.Deadline) {
		return refusal(OutcomeStaleLease, action, "lease deadline has passed")
	}
	return nil
}

// Heartbeat is a governed journal transition and returns a refreshed capsule.
// Merely checking a token is insufficient; every coordinate is revalidated.
func (s *Service) Heartbeat(ctx context.Context, operationID controljournal.OperationID, authority Authority, now time.Time) (Authority, error) {
	state := s.works[authority.WorkID]
	if state == nil {
		return Authority{}, refusal(OutcomeStaleWorkRevision, "heartbeat", "work is unknown")
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := s.authorizeLocked(state, authority, "heartbeat", now, s.epoch); err != nil {
		return Authority{}, err
	}
	next := *state.current
	next.Deadline = now.UTC().Add(s.config.LeaseTTL)
	next.OperationID = operationID
	revision, err := s.publish(ctx, publication{
		operationID: operationID, commandType: "authority.heartbeat", eventType: "authority.heartbeat",
		aggregateKind: "authority", aggregateID: controljournal.AggregateID(authority.WorkID), logical: state.logical + 1,
		observedRevision: state.headRevision, authority: next, resultType: "authority.heartbeat", payload: next,
	})
	if err != nil {
		return Authority{}, err
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.current = &next
	return next, nil
}

func (s *Service) Expire(ctx context.Context, operationID controljournal.OperationID, authority Authority, now time.Time) error {
	state := s.works[authority.WorkID]
	if state == nil {
		return refusal(OutcomeStaleWorkRevision, "expire", "work is unknown")
	}
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	state.mu.Lock()
	if state.current == nil {
		state.mu.Unlock()
		return refusal(OutcomeStaleLease, "expire", "lease is not current")
	}
	validationTime := state.current.Deadline.Add(-time.Nanosecond)
	if err := s.authorizeLocked(state, authority, "expire", validationTime, s.epoch); err != nil {
		state.mu.Unlock()
		return err
	}
	if now.Before(state.current.Deadline) {
		state.mu.Unlock()
		return refusal(OutcomeAuthorityConflict, "expire", "live lease cannot expire before its deadline")
	}
	next := *state.current
	next.Status = "expired"
	next.OperationID = operationID
	revision, err := s.publish(ctx, publication{
		operationID: operationID, commandType: "authority.expire", eventType: "authority.expired",
		aggregateKind: "authority", aggregateID: controljournal.AggregateID(authority.WorkID), logical: state.logical + 1,
		observedRevision: state.headRevision, authority: next, resultType: "authority.expired", payload: next,
	})
	if err != nil {
		state.mu.Unlock()
		return err
	}
	next.AuthorityHeadRecordRevision = revision
	state.logical++
	state.headRevision = revision
	state.current = nil
	state.mu.Unlock()
	return s.quarantineLeaseWorkspaces(ctx, authority, "lease generation expired", now)
}

// ReplaceRuntime separates owner restart from lease replacement. The caller
// must supply an application service admitted under the fresh runtime epoch.
// This process-local swap is the explicit M1 product-host boundary; WP10 owns
// multi-process qualification.
func (s *Service) ReplaceRuntime(ctx context.Context, operationID controljournal.OperationID, epoch controljournal.RuntimeEpochID, applier batchApplier, now time.Time) error {
	if applier == nil || epoch == "" || now.IsZero() {
		return refusal(OutcomeInvalidCommand, "restart", "fresh epoch, admitted application service, and authoritative time are required")
	}
	s.epochMu.Lock()
	old := s.epoch
	if epoch == old {
		s.epochMu.Unlock()
		return refusal(OutcomeInvalidCommand, "restart", "runtime epoch must be fresh")
	}
	oldApplier := s.applier
	s.epoch, s.applier = epoch, applier
	type invalidatedLease struct {
		WorkID     controljournal.WorkID    `json:"work_id"`
		ActorID    controljournal.ActorID   `json:"actor_id"`
		ProcessID  controljournal.ProcessID `json:"process_id"`
		LeaseID    controljournal.LeaseID   `json:"lease_id"`
		Generation uint64                   `json:"generation"`
		FenceToken uint64                   `json:"fence_token"`
	}
	invalidated := make([]invalidatedLease, 0, len(s.works))
	for _, workID := range s.config.WorkIDs {
		state := s.works[workID]
		state.mu.Lock()
		if state.current != nil && state.current.RuntimeEpochID == old {
			invalidated = append(invalidated, invalidatedLease{WorkID: workID, ActorID: state.current.ActorID, ProcessID: state.current.ProcessID, LeaseID: state.current.LeaseID, Generation: state.current.Generation, FenceToken: state.current.FenceToken})
		}
		state.mu.Unlock()
	}
	restart := struct {
		OperationID      controljournal.OperationID    `json:"operation_id"`
		RunID            controljournal.RunID          `json:"run_id"`
		PredecessorEpoch controljournal.RuntimeEpochID `json:"predecessor_epoch_id"`
		RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
		OccurredAt       time.Time                     `json:"occurred_at"`
		Invalidated      []invalidatedLease            `json:"invalidated_authorities"`
	}{operationID, s.config.RunID, old, epoch, now.UTC(), invalidated}
	revision, err := s.publish(ctx, publication{
		operationID: operationID, commandType: "runtime.owner_restart", eventType: "runtime.owner_restarted",
		aggregateKind: "runtime-composition", aggregateID: controljournal.AggregateID(s.config.RunID),
		logical: s.runtimeLogical + 1, observedRevision: s.runtimeHeadRevision,
		resultType: "runtime.owner_restarted", payload: restart,
	})
	if err != nil {
		s.epoch, s.applier = old, oldApplier
		s.epochMu.Unlock()
		return err
	}
	s.runtimeLogical++
	s.runtimeHeadRevision = revision
	s.predecessorEpoch = old
	var stale []Authority
	for _, state := range s.works {
		state.mu.Lock()
		if state.current != nil && state.current.RuntimeEpochID == old {
			stale = append(stale, *state.current)
			state.current = nil
		}
		state.mu.Unlock()
	}
	s.epochMu.Unlock()
	for _, authority := range stale {
		if err := s.quarantineLeaseWorkspaces(ctx, authority, "predecessor runtime epoch", now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Projection() Projection {
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	projection := Projection{Runtime: RuntimeProjection{RuntimeEpochID: s.epoch, PredecessorEpoch: s.predecessorEpoch, LogicalVersion: s.runtimeLogical, HeadRecordRevision: s.runtimeHeadRevision}}
	for _, workID := range s.config.WorkIDs {
		state := s.works[workID]
		state.mu.Lock()
		entry := WorkProjection{WorkID: workID, LogicalVersion: state.logical, HeadRecordRevision: state.headRevision, LastGeneration: state.lastGen, LastFence: state.lastFence}
		if state.current != nil {
			current := *state.current
			entry.Current = &current
		}
		state.mu.Unlock()
		projection.Works = append(projection.Works, entry)
	}
	s.workspaceMu.RLock()
	for _, binding := range s.workspaces {
		projection.Workspaces = append(projection.Workspaces, binding)
	}
	s.workspaceMu.RUnlock()
	sort.Slice(projection.Workspaces, func(i, j int) bool {
		return projection.Workspaces[i].WorkspaceID < projection.Workspaces[j].WorkspaceID
	})
	return projection
}

func (s *Service) Current(workID controljournal.WorkID) (Authority, bool) {
	state := s.works[workID]
	if state == nil {
		return Authority{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.current == nil {
		return Authority{}, false
	}
	return *state.current, true
}

type publication struct {
	operationID      controljournal.OperationID
	commandType      string
	eventType        controljournal.EventType
	aggregateKind    controljournal.AggregateKind
	aggregateID      controljournal.AggregateID
	logical          uint64
	observedRevision uint64
	authority        Authority
	resultType       string
	payload          any
}

func (s *Service) publish(ctx context.Context, input publication) (uint64, error) {
	payload, err := json.Marshal(input.payload)
	if err != nil {
		return 0, refusal(OutcomeInvalidCommand, input.commandType, "payload cannot be canonicalized")
	}
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: s.config.ProjectID, MissionID: s.config.MissionID,
		OperationID: input.operationID, CommandType: input.commandType, RuntimeEpochID: s.epoch, Payload: payload,
	})
	if err != nil {
		return 0, err
	}
	index, _ := controljournal.NewEventIndexV1(0)
	count, _ := controljournal.NewEventCountV1(1)
	observations, _ := json.Marshal(map[string]any{
		"actor_id": input.authority.ActorID, "process_id": input.authority.ProcessID,
		"run_id": input.authority.RunID, "work_id": input.authority.WorkID,
	})
	eventID := stableEventID(input.operationID)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: eventID, EventType: input.eventType, EventSchemaVersion: 1,
		ReducerVersion: "proving-ground-v1", ProjectID: s.config.ProjectID,
		AggregateKind: input.aggregateKind, AggregateID: input.aggregateID, AggregateLogicalVersion: input.logical,
		OperationID: input.operationID, RuntimeEpochID: s.epoch, EventIndex: index, EventCount: count,
		WorkRevisionID: s.config.WorkRevisionID, LeaseID: input.authority.LeaseID,
		LeaseGeneration: input.authority.Generation, FenceToken: input.authority.FenceToken,
		PolicyRevision: "m1", DoctrineRevision: "m1", ProfileRevision: "implementer-ivan",
		RecordedObservations: observations, Payload: payload,
	})
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(payload)
	head, err := controljournal.ConstructAggregateHeadV1(input.aggregateKind, input.aggregateID, input.logical, s.epoch, eventID, digest)
	if err != nil {
		return 0, err
	}
	expected := []controljournal.ExpectedAggregateHeadV1{{
		AggregateKind: input.aggregateKind, AggregateID: input.aggregateID,
		LogicalVersion: input.logical - 1, ObservedHeadRevision: input.observedRevision,
	}}
	preconditions, err := application.CanonicalPreconditionsDigestV1(expected)
	if err != nil {
		return 0, err
	}
	batch, err := application.NewValidatedBatchV1(application.BatchInputV1{
		Command: command, ExpectedHeads: expected, NextHeads: []controljournal.NewAggregateHeadV1{{Head: head}},
		Events: []controljournal.EventEnvelopeV1{event}, ExpectedPreconditionsDigest: preconditions,
		ResultType: input.resultType, ResultSchemaVersion: 1, CanonicalResult: payload,
	})
	if err != nil {
		return 0, err
	}
	result, err := s.applier.Apply(ctx, batch)
	if err != nil {
		return 0, err
	}
	switch result.Kind {
	case application.OutcomeCommitted, application.OutcomeAlreadyCommitted:
		if result.CommittedRevision == 0 {
			return 0, refusal(OutcomeIndeterminate, input.commandType, "accepted operation has no durable revision")
		}
		return result.CommittedRevision, nil
	case application.OutcomeLogicalVersionConflict, application.OutcomeHeadRevisionConflict:
		return 0, refusal(OutcomeAuthorityConflict, input.commandType, "aggregate authority changed concurrently")
	case application.OutcomeRuntimeEpochConflict:
		return 0, refusal(OutcomeStaleEpoch, input.commandType, "runtime epoch is no longer admitted")
	case application.OutcomeAmbiguousResponse, application.OutcomeIndeterminate:
		return 0, refusal(OutcomeAmbiguous, input.commandType, "durable result requires same-operation reconciliation", "retry the same operation ID")
	default:
		return 0, refusal(OutcomeIndeterminate, input.commandType, "journal application refused the transition")
	}
}

func stableEventID(operationID controljournal.OperationID) controljournal.EventID {
	digest := sha256.Sum256([]byte(operationID))
	return controljournal.EventID("event-" + hex.EncodeToString(digest[:16]))
}

// RunFocusedCampaign executes one source-bound proving scenario repeatedly.
// The trial owns concrete assertions; this helper supplies bounded cancellation
// and enforces the M1 minimum repetition count used by WP09's combined runner.
func RunFocusedCampaign(ctx context.Context, repetitions int, trial func(context.Context, int) error) error {
	if repetitions < 100 || trial == nil {
		return refusal(OutcomeInvalidCommand, "campaign", "at least 100 repetitions and a trial are required")
	}
	for index := 0; index < repetitions; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := trial(ctx, index); err != nil {
			return fmt.Errorf("campaign trial %d: %w", index, err)
		}
	}
	return nil
}
