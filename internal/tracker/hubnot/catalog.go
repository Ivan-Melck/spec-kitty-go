package hubnot

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

var _ tracker.Catalog = (*BoundAdapter)(nil)

func (b *BoundAdapter) CreateWork(ctx context.Context, r tracker.CreateWorkRequest) (tracker.WorkMutation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	defer e.budget.close()
	return e.createWork(ctx, r)
}
func (e *operationEngine) createWork(ctx context.Context, r tracker.CreateWorkRequest) (tracker.WorkMutation, error) {
	if r.ProjectID != e.project {
		return tracker.WorkMutation{}, invalid("project differs from binding", nil)
	}
	c, err := fromCatalog(r)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	a, err := contentAction(e.project, r.OperationID, "catalog.create", c)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	effect, err := e.run(ctx, a, func() (mutationRequest, error) {
		view, err := e.store.inventory(ctx, e.budget, e.project)
		if err != nil {
			return mutationRequest{}, err
		}
		if _, exists := view.lookupWork(c.WorkID); exists {
			return mutationRequest{}, operationConflict("work identity already has another opening", nil)
		}
		state, err := encodeWorkState(issueState{}, c)
		if err != nil {
			return mutationRequest{}, err
		}
		state, err = stampState(state, a, false)
		return mutationRequest{intent: "open", state: &state}, err
	})
	result, projectErr := catalogEffect(effect)
	if err != nil {
		return result, err
	}
	if projectErr != nil {
		return result, projectErr
	}
	if !effect.Replayed {
		if _, err := e.store.observe(ctx, e.budget, result.Record.ExternalRef); err != nil {
			return result, &OperationError{Outcome: effect.Outcome, PriorMutation: effect.PriorMutation, AppliedThisCall: effect.AppliedThisCall, Cause: err}
		}
	}
	return result, nil
}
func contentAction(project ids.ProjectID, id ids.OperationID, action string, c workContent) (operationAction, error) {
	a, err := newOperationAction(project, id, action, c)
	if err != nil {
		return a, err
	}
	a.verify = func(event issueOperation) error {
		got, err := verifyEventMapping(event, project, c.WorkID, c.CreatedBy, "")
		if err != nil {
			return err
		}
		if event.Intent != "open" || event.Kind != "issue.open" || event.EventID != event.IssueID || len(event.Parents) != 0 || !sameContent(got, c) {
			return operationConflict("original opening differs from portable content", nil)
		}
		return nil
	}
	return a, nil
}
func (b *BoundAdapter) ReadWork(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.WorkRecord, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	defer e.budget.close()
	if err := validateOperationReference(e.project, ref); err != nil {
		return tracker.WorkRecord{}, err
	}
	observed, err := e.store.observe(ctx, e.budget, ref)
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	return observedRecord(observed)
}
func (b *BoundAdapter) UpdateWork(ctx context.Context, r tracker.UpdateWorkRequest) (tracker.WorkMutation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	defer e.budget.close()
	return e.updateWork(ctx, r)
}
func (e *operationEngine) updateWork(ctx context.Context, r tracker.UpdateWorkRequest) (tracker.WorkMutation, error) {
	if err := validateOperationReference(e.project, r.ExternalRef); err != nil {
		return tracker.WorkMutation{}, err
	}
	if r.RequiredGuard != "" && r.RequiredGuard != tracker.UpdateUnguarded {
		return tracker.WorkMutation{}, unsupportedDefinitionUpdate()
	}
	r.Criteria = append([]tracker.Criterion{}, r.Criteria...)
	// The semantic request excludes only observation preconditions, never ordered
	// criteria or immutable external mapping. Read-only replay needs no fresh view.
	meaning := struct {
		Reference     tracker.ExternalWorkRef `json:"reference"`
		Guard         tracker.UpdateLevel     `json:"guard"`
		Title         string                  `json:"title"`
		Description   string                  `json:"description"`
		DesignContext string                  `json:"design_context"`
		Criteria      []tracker.Criterion     `json:"criteria"`
	}{immutableReference(r.ExternalRef), tracker.UpdateUnguarded, r.Title, r.Description, r.DesignContext, r.Criteria}
	if !validContentStrings(reflect.ValueOf(meaning)) {
		return tracker.WorkMutation{}, invalid("invalid Unicode in catalog request", nil)
	}
	a, err := newOperationAction(e.project, r.OperationID, "catalog.update", meaning)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	a.verify = func(event issueOperation) error {
		c, err := verifyEventMapping(event, e.project, r.ExternalRef.WorkID, r.ExternalRef.CreatedByOperation, r.ExternalRef.ExternalID)
		if err != nil {
			return err
		}
		if c.Complete || event.Intent != "revise" || event.Kind != "issue.revise" || c.Title != r.Title || c.Description != r.Description || c.DesignContext != r.DesignContext || !slices.Equal(catalogCriteria(c), r.Criteria) {
			return operationConflict("original update differs from portable content", nil)
		}
		return nil
	}
	effect, err := e.run(ctx, a, func() (mutationRequest, error) {
		observed, err := e.store.observe(ctx, e.budget, r.ExternalRef)
		if err != nil {
			return mutationRequest{}, err
		}
		// Check the complete marker before validating a malformed replacement, so a
		// generic update cannot strip it to evade the unsupported definition guard.
		opening, openingPresent := observed.Inventory.lookup(r.ExternalRef.ExternalID)
		if observed.Content.Complete || (openingPresent && opening.Detail.OpeningMetadata[ownedPrefix+"mode"] == "complete") {
			return mutationRequest{}, unsupportedDefinitionUpdate()
		}
		if r.ExpectedObservation != 0 && r.ExpectedObservation != observed.Provider.Revision {
			return mutationRequest{}, operationConflict("catalog observation is stale", nil)
		}
		c := cloneContent(observed.Content)
		c.Title = r.Title
		c.Description = r.Description
		c.DesignContext = r.DesignContext
		c.Criteria = make([]tracker.WorkCriterion, len(r.Criteria))
		for i, v := range r.Criteria {
			c.Criteria[i] = tracker.WorkCriterion{ID: v.ID, Text: v.Text, Verifier: v.Verifier, Required: v.Required, EvidenceHint: v.EvidenceHint}
		}
		noop := sameContent(c, observed.Content)
		state, err := encodeWorkState(observed.State, c)
		if err != nil {
			return mutationRequest{}, err
		}
		state, err = stampState(state, a, noop)
		if err != nil {
			return mutationRequest{}, err
		}
		return mutationRequest{intent: "revise", id: r.ExternalRef.ExternalID, state: &state, parents: headIDs(observed.Heads)}, nil
	})
	result, projectErr := catalogEffect(effect)
	if err != nil {
		return result, err
	}
	if projectErr != nil {
		return result, projectErr
	}
	if !effect.Replayed {
		if _, err := e.store.observe(ctx, e.budget, result.Record.ExternalRef); err != nil {
			return result, &OperationError{Outcome: effect.Outcome, PriorMutation: effect.PriorMutation, AppliedThisCall: effect.AppliedThisCall, Cause: err}
		}
	}
	return result, nil
}
func catalogCriteria(c workContent) []tracker.Criterion {
	out := make([]tracker.Criterion, len(c.Criteria))
	for i, v := range c.Criteria {
		out[i] = tracker.Criterion{ID: v.ID, Text: v.Text, Verifier: v.Verifier, Required: v.Required, EvidenceHint: v.EvidenceHint}
	}
	return out
}
func headIDs(heads []issueHead) []string {
	out := make([]string, len(heads))
	for i, h := range heads {
		out[i] = h.ID
	}
	return out
}
func recordValue(c workContent, state issueState, ref tracker.ExternalWorkRef, proof tracker.ProviderObservation, deps []tracker.DependencyRef, comments []tracker.DiscussionEntry) (tracker.WorkRecord, error) {
	assignee := ""
	if len(state.Assignees) > 1 {
		return tracker.WorkRecord{}, failure(tracker.ErrorAmbiguous, "reconcile_without_mutation", "multiple native assignees cannot project one owner", nil)
	}
	if len(state.Assignees) == 1 {
		assignee = state.Assignees[0]
	}
	return tracker.NewWorkRecord(ref, c.Title, c.Description, c.DesignContext, catalogCriteria(c), deps, assignee, "", comments, proof)
}
func catalogEffect(effect operationEffect) (tracker.WorkMutation, error) {
	result := tracker.WorkMutation{Guarantee: tracker.UpdateUnguarded, Replayed: effect.Replayed, Noop: effect.Replayed || effect.Stamp.Noop}
	if effect.Event.State == nil || effect.Proof.Revision == 0 {
		return result, nil
	}
	c, _, err := decodeWorkState(*effect.Event.State)
	if err != nil {
		return result, err
	}
	ref := tracker.ExternalWorkRef{ProjectID: c.ProjectID, WorkID: c.WorkID, Provider: "hubnot", ExternalID: effect.Event.IssueID, MetadataSchema: 1, CreatedByOperation: c.CreatedBy, LastObservation: effect.Proof.Revision}
	// This is the original signed mutation effect, deliberately a different proof
	// kind from ReadWork's full current graph and discussion observation.
	result.Record, err = recordValue(c, *effect.Event.State, ref, effect.Proof, nil, nil)
	if err != nil {
		return result, &OperationError{Outcome: effect.Outcome, PriorMutation: effect.PriorMutation, AppliedThisCall: effect.AppliedThisCall, Cause: err}
	}
	return result, nil
}
func observedRecord(observed workObservation) (tracker.WorkRecord, error) {
	deps := make([]tracker.DependencyRef, 0, len(observed.Dependencies))
	for _, supplier := range observed.Dependencies {
		source, ok := observed.Inventory.lookupWork(supplier)
		if !ok {
			return tracker.WorkRecord{}, operationConflict("observed supplier mapping missing", nil)
		}
		deps = append(deps, tracker.DependencyRef{BlockedWorkID: observed.Content.WorkID, BlockingWorkID: supplier, Kind: tracker.DependencyBlocks, ExternalEdgeID: relationIdentity(source.Detail.ID, observed.Reference.ExternalID)})
	}
	comments := make([]tracker.DiscussionEntry, len(observed.Comments))
	for i, v := range observed.Comments {
		timestamp, err := time.Parse(time.RFC3339Nano, v.Timestamp)
		if err != nil {
			return tracker.WorkRecord{}, protocolFailure(err)
		}
		comments[i] = tracker.DiscussionEntry{ExternalCommentID: v.ID, Author: v.Actor, Body: v.Body, OperationMarker: v.Operation, CreatedAt: timestamp, Observation: observed.Provider.Revision}
	}
	return recordValue(observed.Content, observed.State, observed.Reference, observed.Provider, deps, comments)
}
