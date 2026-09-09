package hubnot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

var _ tracker.WorkDefinitionPort = (*BoundAdapter)(nil)

func unsupportedDefinitionUpdate() error {
	return &tracker.Error{Kind: tracker.ErrorGuardedUpdateUnsupported, Capability: tracker.CapabilityCatalogUpdate, RecoveryCode: "reconcile_without_mutation", Message: "Hubnot actor-local checks do not provide guarded definition updates"}
}
func (b *BoundAdapter) DefinitionCapabilities(ctx context.Context) (tracker.WorkDefinitionCapabilities, error) {
	if b == nil || ctx == nil {
		return tracker.WorkDefinitionCapabilities{}, invalid("bound adapter and context required", nil)
	}
	if err := ctx.Err(); err != nil {
		return tracker.WorkDefinitionCapabilities{}, err
	}
	return tracker.WorkDefinitionCapabilities{Create: true, GuardedUpdate: false, BlockingGraph: true}, nil
}
func (b *BoundAdapter) UpdateDefinition(context.Context, tracker.UpdateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	return tracker.WorkDefinitionMutation{}, unsupportedDefinitionUpdate()
}
func (b *BoundAdapter) ObserveDefinition(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.WorkDefinitionObservation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.WorkDefinitionObservation{}, err
	}
	defer e.budget.close()
	if err := validateOperationReference(e.project, ref); err != nil {
		return tracker.WorkDefinitionObservation{}, err
	}
	observed, err := e.store.observe(ctx, e.budget, ref)
	if err != nil {
		return tracker.WorkDefinitionObservation{}, err
	}
	d, err := observed.Content.definition(observed.Dependencies)
	if err != nil {
		return tracker.WorkDefinitionObservation{}, err
	}
	return tracker.NewWorkDefinitionObservation(d, observed.Reference, observed.Provider, observed.Provider.ObservedAt)
}
func (b *BoundAdapter) CreateDefinition(ctx context.Context, r tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.WorkDefinitionMutation{}, err
	}
	defer e.budget.close()
	return e.createDefinition(ctx, r)
}
func definitionChildID(parent ids.OperationID, blocking, blocked ids.WorkID) ids.OperationID {
	sum := sha256.Sum256([]byte(parent.String() + "\x00" + blocking.String() + "\x00" + blocked.String()))
	return ids.OperationID("definition-edge-" + hex.EncodeToString(sum[:]))
}
func (e *operationEngine) createDefinition(ctx context.Context, r tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	var result tracker.WorkDefinitionMutation
	c, err := fromDefinition(r.Definition, r.OperationID)
	if err != nil {
		return result, err
	}
	if c.ProjectID != e.project {
		return result, invalid("definition project differs from binding", nil)
	}
	a, err := contentAction(e.project, r.OperationID, "definition.create", c)
	if err != nil {
		return result, err
	}
	// Initial census precedes any write: PriorMutation means persisted before
	// this invocation, not merely a preceding child of the current invocation.
	opening, found, err := e.recover(ctx, a)
	if err != nil {
		result.PriorMutation = opening.PriorMutation
		return result, err
	}
	dependencies := append([]ids.WorkID(nil), c.CreateDependencies...)
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i] < dependencies[j] })
	children := make([]operationEffect, len(dependencies))
	present := make([]bool, len(dependencies))
	prior := found
	applied := false
	ambiguous := false
	fail := func(cause error) (tracker.WorkDefinitionMutation, error) {
		var oe *OperationError
		if errors.As(cause, &oe) {
			applied = applied || oe.AppliedThisCall
			ambiguous = ambiguous || oe.Outcome == OutcomeAmbiguous
		}
		outcome := OutcomeUnstarted
		if found || prior || applied {
			outcome = OutcomeApplied
		}
		if ambiguous {
			outcome = OutcomeAmbiguous
		}
		result.PriorMutation = prior
		return result, &OperationError{Outcome: outcome, PriorMutation: prior, AppliedThisCall: applied, Cause: cause}
	}
	for i, blocking := range dependencies {
		child, err := relationAction(e.project, definitionChildID(r.OperationID, blocking, c.WorkID), blocking, c.WorkID, true, "definition.edge")
		if err != nil {
			return fail(err)
		}
		children[i], present[i], err = e.recover(ctx, child)
		prior = prior || present[i]
		if err != nil {
			return fail(err)
		}
		if present[i] && !found {
			return fail(operationConflict("definition edge exists without original opening", nil))
		}
		if present[i] {
			if err := validateDefinitionTarget(opening, children[i], c); err != nil {
				return fail(err)
			}
		}
	}
	all := found
	for _, p := range present {
		all = all && p
	}
	if all {
		return definitionResult(opening, children, c, true, true)
	}
	if !found {
		opening, err = e.run(ctx, a, func() (mutationRequest, error) {
			view, err := e.store.inventory(ctx, e.budget, e.project)
			if err != nil {
				return mutationRequest{}, err
			}
			if _, exists := view.lookupWork(c.WorkID); exists {
				return mutationRequest{}, operationConflict("work identity already mapped to another creation", nil)
			}
			// Refuse missing suppliers and native capacity before opening any issue.
			for _, dependency := range dependencies {
				if _, ok := view.lookupWork(dependency); !ok {
					return mutationRequest{}, failure(tracker.ErrorNotFound, "reconcile_mapping", "required definition supplier is absent", nil)
				}
			}
			state, err := encodeWorkState(issueState{}, c)
			if err != nil {
				return mutationRequest{}, err
			}
			state, err = stampState(state, a, false)
			return mutationRequest{intent: "open", state: &state}, err
		})
		applied = applied || opening.AppliedThisCall
		found = opening.Event.EventID != ""
		if found {
			result, _ = definitionResult(opening, nil, c, false, prior)
		}
		if err != nil {
			return fail(err)
		}
	} else {
		result, _ = definitionResult(opening, nil, c, false, prior)
	}
	for i, blocking := range dependencies {
		if present[i] {
			continue
		}
		children[i], err = e.replaceBlockingRelation(ctx, definitionChildID(r.OperationID, blocking, c.WorkID), blocking, c.WorkID, true, "definition.edge")
		applied = applied || children[i].AppliedThisCall
		if err != nil {
			return fail(err)
		}
		if err := validateDefinitionTarget(opening, children[i], c); err != nil {
			return fail(err)
		}
	}
	replayed := opening.Replayed && !opening.AppliedThisCall
	for _, child := range children {
		replayed = replayed && child.Replayed && !child.AppliedThisCall
	}
	if replayed {
		return definitionResult(opening, children, c, true, prior)
	}
	// The first completed execution must also see all required actual edges in
	// one coherent current view. Historical all-child replay above never relinks.
	ref := tracker.ExternalWorkRef{ProjectID: c.ProjectID, WorkID: c.WorkID, Provider: "hubnot", ExternalID: opening.Event.IssueID, MetadataSchema: 1, CreatedByOperation: c.CreatedBy}
	current, err := e.store.observe(ctx, e.budget, ref)
	if err != nil {
		return fail(err)
	}
	for _, required := range dependencies {
		exists := false
		for _, actual := range current.Dependencies {
			exists = exists || actual == required
		}
		if !exists {
			return fail(operationConflict("definition saga has surviving facts but a required edge is absent", nil))
		}
	}
	return definitionResult(opening, children, c, false, prior)
}
func definitionResult(opening operationEffect, children []operationEffect, c workContent, replayed, prior bool) (tracker.WorkDefinitionMutation, error) {
	events := make([]issueOperation, len(children))
	deps := make([]ids.WorkID, len(children))
	for i, child := range children {
		events[i] = child.Event
		content, _, err := decodeWorkState(*child.Event.State)
		if err != nil {
			return tracker.WorkDefinitionMutation{}, err
		}
		deps[i] = content.WorkID
	}
	proof, err := originalProof(opening.Event, events)
	if err != nil {
		return tracker.WorkDefinitionMutation{}, err
	}
	d, err := c.definition(deps)
	if err != nil {
		return tracker.WorkDefinitionMutation{}, err
	}
	ref := tracker.ExternalWorkRef{ProjectID: c.ProjectID, WorkID: c.WorkID, Provider: "hubnot", ExternalID: opening.Event.IssueID, MetadataSchema: 1, CreatedByOperation: c.CreatedBy, LastObservation: proof.Revision}
	observation, err := tracker.NewWorkDefinitionObservation(d, ref, proof, proof.ObservedAt)
	return tracker.WorkDefinitionMutation{Observation: observation, Replayed: replayed, PriorMutation: prior, Noop: replayed}, err
}

// Every child recovery path must bind the same complete immutable target,
// whether discovered in the initial census or after a concurrent publication.
func validateDefinitionTarget(opening, child operationEffect, content workContent) error {
	expected := tracker.ExternalWorkRef{ProjectID: content.ProjectID, WorkID: content.WorkID, Provider: "hubnot", ExternalID: opening.Event.IssueID, MetadataSchema: 1, CreatedByOperation: content.CreatedBy}
	if child.Stamp.Target == nil || *child.Stamp.Target != expected {
		return operationConflict("definition edge points to another original mapping", nil)
	}
	return nil
}
