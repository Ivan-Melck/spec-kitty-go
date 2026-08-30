package provingground

import (
	"context"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
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
	RecordRevision   uint64                        `json:"record_revision"`
	Status           string                        `json:"status"`
	QuarantineReason string                        `json:"quarantine_reason,omitempty"`
	OperationID      controljournal.OperationID    `json:"operation_id"`
}

func (s *Service) EnsureWorkspace(ctx context.Context, request WorkspaceRequest) (WorkspaceBinding, error) {
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
	if _, orphan := s.orphans[request.WorkspaceID]; orphan {
		s.workspaceMu.RUnlock()
		return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace", "authority-free orphan identity cannot be promoted", "preserve the orphan", "retry with a fresh workspace identity")
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
	if err := s.git.Create(ctx, record); err != nil {
		return WorkspaceBinding{}, err
	}
	probe, err := s.git.Probe(ctx, record)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	physical := WorkspacePhysical{Record: record, Probe: probe}
	if err := s.faults.Reach(ctx, "GJ-03"); err != nil {
		s.workspaceMu.Lock()
		s.orphans[request.WorkspaceID] = physical
		s.workspaceMu.Unlock()
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
		s.workspaceMu.Lock()
		s.orphans[request.WorkspaceID] = physical
		s.workspaceMu.Unlock()
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
			s.workspaceMu.Lock()
			s.orphans[request.WorkspaceID] = physical
			s.workspaceMu.Unlock()
			return WorkspaceBinding{}, refusal(OutcomeWorkspaceNotReady, "workspace-bind", "one lease generation cannot acquire a second mutable workspace", "preserve the physical orphan", "reuse the existing durable binding")
		}
	}
	s.workspaceMu.RUnlock()
	binding := WorkspaceBinding{
		WorkspaceID: request.WorkspaceID, CanonicalPath: path, OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase,
		GitDir: probe.GitDir, IndexPath: probe.IndexPath, ActorID: request.Authority.ActorID, ProcessID: request.Authority.ProcessID,
		RunID: request.Authority.RunID, LeaseID: request.Authority.LeaseID, Generation: request.Authority.Generation,
		FenceToken: request.Authority.FenceToken, WorkID: request.Authority.WorkID, WorkRevisionID: request.Authority.WorkRevisionID,
		RuntimeEpochID: request.Authority.RuntimeEpochID, Status: "active-bound", OperationID: request.OperationID,
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
	delete(s.orphans, request.WorkspaceID)
	s.workspaceMu.Unlock()
	state.mu.Unlock()
	s.epochMu.RUnlock()
	if err := s.faults.Reach(ctx, "GJ-04"); err != nil {
		return WorkspaceBinding{}, &Error{Kind: OutcomeAmbiguous, Action: "GJ-04", Detail: "durable binding exists but capsule delivery was lost", Recovery: []string{"retry the same operation ID"}, Cause: err}
	}
	return binding, nil
}

func (s *Service) quarantineLeaseWorkspaces(ctx context.Context, authority Authority, reason string, now time.Time) error {
	s.workspaceMu.RLock()
	var selected []WorkspaceBinding
	for _, binding := range s.workspaces {
		if binding.LeaseID == authority.LeaseID && binding.Generation == authority.Generation && binding.Status == "active-bound" {
			selected = append(selected, binding)
		}
	}
	s.workspaceMu.RUnlock()
	for _, binding := range selected {
		op := controljournal.OperationID("quarantine-" + string(binding.WorkspaceID) + "-" + fmt.Sprint(binding.Generation))
		if len(op) > 128 {
			op = controljournal.OperationID(stableEventID(op))
		}
		binding.Status, binding.QuarantineReason = "quarantined", reason
		revision, err := s.publish(ctx, publication{
			operationID: op, commandType: "workspace.quarantine", eventType: "workspace.quarantined",
			aggregateKind: "workspace", aggregateID: controljournal.AggregateID(binding.WorkspaceID), logical: 2,
			observedRevision: binding.RecordRevision, authority: authority, resultType: "workspace.quarantined", payload: binding,
		})
		if err != nil {
			return err
		}
		binding.RecordRevision = revision
		s.workspaceMu.Lock()
		current, ok := s.workspaces[binding.WorkspaceID]
		if ok && current.LeaseID == authority.LeaseID && current.Generation == authority.Generation {
			s.workspaces[binding.WorkspaceID] = binding
		}
		s.workspaceMu.Unlock()
	}
	return nil
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

func (s *Service) ReconcileWorkspace(ctx context.Context, id controljournal.WorkspaceID, physical workspace.Record) WorkspaceReconciliation {
	if s.git == nil {
		return WorkspaceReconciliation{Class: WorkspaceIndeterminate, OperatorActions: []string{"restore the Git inspection adapter"}}
	}
	s.workspaceMu.RLock()
	binding, durable := s.workspaces[id]
	_, orphan := s.orphans[id]
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
		if orphan && inspection.Registered && len(inspection.Discrepancies) == 0 {
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

func (s *Service) Binding(id controljournal.WorkspaceID) (WorkspaceBinding, bool) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	binding, ok := s.workspaces[id]
	return binding, ok
}
