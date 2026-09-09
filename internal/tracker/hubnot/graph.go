package hubnot

import (
	"context"
	"errors"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func (b *BoundAdapter) PutBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	return b.changeDependency(ctx, request, true)
}
func (b *BoundAdapter) RemoveBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	return b.changeDependency(ctx, request, false)
}
func validateGraphRequest(project ids.ProjectID, request tracker.DependencyRequest) error {
	if request.ProjectID != project {
		return invalid("graph project must match binding", nil)
	}
	if err := request.Dependency.Validate(); err != nil {
		return invalid("invalid blocking dependency", err)
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return invalid("invalid graph project", err)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return invalid("invalid graph operation", err)
	}
	return nil
}
func (b *BoundAdapter) changeDependency(ctx context.Context, request tracker.DependencyRequest, present bool) (tracker.DependencyMutation, error) {
	if b == nil {
		return tracker.DependencyMutation{}, invalid("bound graph adapter required", nil)
	}
	if err := validateGraphRequest(b.binding.ProjectID, request); err != nil {
		return tracker.DependencyMutation{}, err
	}
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	defer e.budget.close()
	return mutateGraph(ctx, e, request, present)
}

// mutateGraph shares original-operation recovery with definition creation. The
// returned proof describes the signed original effect, not a fresh graph read.
func mutateGraph(ctx context.Context, e *operationEngine, request tracker.DependencyRequest, present bool) (tracker.DependencyMutation, error) {
	if err := validateGraphRequest(e.project, request); err != nil {
		return tracker.DependencyMutation{}, err
	}
	action := "graph.remove"
	if present {
		action = "graph.put"
	}
	effect, err := e.replaceBlockingRelation(ctx, request.OperationID, request.Dependency.BlockingWorkID, request.Dependency.BlockedWorkID, present, action)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	// The shared engine validates both original source state and immutable target.
	dependency := tracker.DependencyRef{BlockingWorkID: request.Dependency.BlockingWorkID, BlockedWorkID: request.Dependency.BlockedWorkID, Kind: tracker.DependencyBlocks, ExternalEdgeID: relationIdentity(effect.Event.IssueID, effect.Stamp.Target.ExternalID)}
	return tracker.DependencyMutation{Dependency: dependency, Observation: effect.Proof, Replayed: effect.Replayed, Noop: effect.Replayed || effect.Stamp.Noop}, nil
}

func (b *BoundAdapter) ListDependencies(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DependencyRef, error) {
	if b == nil || ctx == nil {
		return nil, invalid("bound graph adapter and context required", nil)
	}
	if err := validateOperationReference(b.binding.ProjectID, reference); err != nil {
		return nil, err
	}
	budget := newObservationBudget()
	defer budget.close()
	observed, err := b.observe(ctx, budget, reference)
	if err != nil {
		return nil, err
	}
	return graphDependencies(observed)
}
func graphDependencies(observed workObservation) ([]tracker.DependencyRef, error) {
	result := make([]tracker.DependencyRef, 0, len(observed.Dependencies))
	for _, row := range observed.Graph {
		edge := row.Edge
		if edge == nil || edge.Kind != "blocks" || edge.Target != observed.Reference.ExternalID {
			continue
		}
		source, ok := observed.Inventory.lookup(edge.Source)
		if !ok || !source.Mapped {
			return nil, failure(tracker.ErrorAttributionLost, "reconcile_mapping", "blocking source has no stable mapping", nil)
		}
		result = append(result, tracker.DependencyRef{BlockingWorkID: source.Content.WorkID, BlockedWorkID: observed.Reference.WorkID, Kind: tracker.DependencyBlocks, ExternalEdgeID: relationIdentity(edge.Source, edge.Target)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BlockingWorkID < result[j].BlockingWorkID })
	return result, nil
}

func (b *BoundAdapter) ReadyProjection(ctx context.Context, request tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	if b == nil || request.ProjectID != b.binding.ProjectID {
		return tracker.ReadyProjection{}, invalid("readiness project must match binding", nil)
	}
	budget := newObservationBudget()
	defer budget.close()
	return readyProjection(ctx, b, budget, request)
}

// readyProjection is descriptive only. It neither consumes a host authority
// port nor modifies dependencies when a supplier is closed.
func readyProjection(ctx context.Context, reader observationReader, budget *observationBudget, request tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	if ctx == nil {
		return tracker.ReadyProjection{}, invalid("readiness context required", nil)
	}
	if err := ctx.Err(); err != nil {
		return tracker.ReadyProjection{}, err
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return tracker.ReadyProjection{}, invalid("invalid readiness project", err)
	}
	pinned := map[ids.WorkID]bool{}
	for _, id := range request.PinnedReadyIDs {
		if _, err := ids.ParseWorkID(id.String()); err != nil {
			return tracker.ReadyProjection{}, invalid("invalid pinned work identity", err)
		}
		pinned[id] = true
	}
	view, err := collectInventory(ctx, reader, budget, request.ProjectID)
	if err != nil {
		// Incomplete evidence is never a successful empty/ready graph. Diagnostic
		// projections explicitly report its absence without a fabricated proof.
		var portable *tracker.Error
		if errors.As(err, &portable) {
			switch portable.Kind {
			case tracker.ErrorNotFound, tracker.ErrorAttributionLost, tracker.ErrorRevisionConflict, tracker.ErrorUnavailable, tracker.ErrorTimeout:
				return tracker.ReadyProjection{ProviderReadyIDs: []ids.WorkID{}, Diagnostics: []tracker.Diagnostic{{Kind: portable.Kind, RecoveryCode: portable.RecoveryAction(), Message: "provider readiness is unavailable; reconcile the complete current graph", Capability: tracker.CapabilityBlockingGraph}}}, nil
			}
		}
		return tracker.ReadyProjection{}, err
	}
	inventory, err := view.inventory()
	if err != nil {
		return tracker.ReadyProjection{}, err
	}
	blocked := map[string]bool{}
	for _, edge := range view.edges {
		source, _ := view.lookup(edge.Source)
		if source.Detail.State.Status == "open" {
			blocked[edge.Target] = true
		}
	}
	ready := make([]ids.WorkID, 0)
	providerSet := map[ids.WorkID]bool{}
	for id, node := range view.nodes {
		if node.Mapped && node.Detail.State.Status == "open" && !blocked[id] {
			ready = append(ready, node.Content.WorkID)
			providerSet[node.Content.WorkID] = true
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	diagnostics := make([]tracker.Diagnostic, 0)
	for _, id := range ready {
		if !pinned[id] {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_pinned_graph", Message: "provider reports work ready but the pinned graph does not", ExternalID: id.String()})
		}
	}
	pinIDs := make([]ids.WorkID, 0, len(pinned))
	for id := range pinned {
		pinIDs = append(pinIDs, id)
		if !providerSet[id] {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_pinned_graph", Message: "pinned graph reports work ready but the provider does not", ExternalID: id.String()})
		}
	}
	sort.Slice(pinIDs, func(i, j int) bool { return pinIDs[i] < pinIDs[j] })
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].ExternalID < diagnostics[j].ExternalID })
	proof, err := observationProof("ready:"+request.ProjectID.String(), view.Snapshot, struct {
		Schema        string `json:"schema"`
		Inventory     []byte `json:"inventory"`
		Pinned, Ready []ids.WorkID
		Diagnostics   []tracker.Diagnostic
	}{"diagnostic-ready/1", inventory.CommonProof().CanonicalFields(), pinIDs, ready, diagnostics})
	if err != nil {
		return tracker.ReadyProjection{}, err
	}
	return tracker.ReadyProjection{ProviderReadyIDs: ready, Diagnostics: diagnostics, Observation: proof}, nil
}

var _ tracker.Graph = (*BoundAdapter)(nil)
