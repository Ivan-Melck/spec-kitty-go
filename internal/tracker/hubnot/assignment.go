package hubnot

import (
	"context"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var _ tracker.AssignmentProjection = (*BoundAdapter)(nil)

type assignmentIntent struct {
	Reference tracker.ExternalWorkRef `json:"reference"`
	Assignee  string                  `json:"assignee"`
	Guarantee tracker.AssignmentLevel `json:"guarantee"`
}

func validClaim(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && len(s) <= 256 && !strings.ContainsFunc(s, unicode.IsControl)
}
func assignmentAction(project ids.ProjectID, r tracker.ProjectAssignmentRequest) (operationAction, error) {
	if err := validateOperationReference(project, r.ExternalRef); err != nil {
		return operationAction{}, err
	}
	if !validClaim(r.Assignee) {
		return operationAction{}, invalid("bounded valid assignee required", nil)
	}
	if r.RequiredGuarantee != tracker.AssignmentUnguarded {
		if r.RequiredGuarantee == tracker.AssignmentAtomicClaim {
			return operationAction{}, &tracker.Error{Kind: tracker.ErrorGuardedUpdateUnsupported, Capability: tracker.CapabilityAssignment, Message: "Hubnot does not provide atomic assignment", RecoveryCode: "use_explicit_unguarded_assignment"}
		}
		return operationAction{}, invalid("explicit unguarded assignment required", nil)
	}
	a, err := newOperationAction(project, r.OperationID, "assignment.project", assignmentIntent{immutableReference(r.ExternalRef), r.Assignee, r.RequiredGuarantee})
	if err != nil {
		return a, err
	}
	a.verify = func(event issueOperation) error {
		if event.Kind != "issue.revise" || event.Intent != "revise" {
			return operationConflict("assignment original is not a revision", nil)
		}
		if _, err := verifyEventMapping(event, project, r.ExternalRef.WorkID, r.ExternalRef.CreatedByOperation, r.ExternalRef.ExternalID); err != nil {
			return err
		}
		if !slices.Equal(event.State.Assignees, []string{r.Assignee}) {
			return operationConflict("assignment original differs from intent", nil)
		}
		return nil
	}
	return a, nil
}
func (b *BoundAdapter) ProjectAssignment(ctx context.Context, r tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	defer e.budget.close()
	return e.projectAssignment(ctx, r)
}
func (e *operationEngine) projectAssignment(ctx context.Context, r tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	a, err := assignmentAction(e.project, r)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	effect, err := e.run(ctx, a, func() (mutationRequest, error) {
		o, err := e.store.observe(ctx, e.budget, r.ExternalRef)
		if err != nil {
			return mutationRequest{}, err
		}
		if len(o.State.Assignees) > 1 {
			return mutationRequest{}, failure(tracker.ErrorAmbiguous, "reconcile_without_mutation", "multiple native assignees", nil)
		}
		if r.ExpectedObservation != 0 && o.Provider.Revision != r.ExpectedObservation {
			return mutationRequest{}, operationConflict("assignment observation changed", nil)
		}
		state := cloneIssueState(o.State)
		noop := slices.Equal(state.Assignees, []string{r.Assignee})
		state.Assignees = []string{r.Assignee}
		state, err = stampState(state, a, noop)
		if err != nil {
			return mutationRequest{}, err
		}
		return mutationRequest{intent: "revise", id: r.ExternalRef.ExternalID, parents: headIDs(o.Heads), state: &state}, nil
	})
	result := tracker.AssignmentMutation{Guarantee: tracker.AssignmentUnguarded, Replayed: effect.Replayed, Noop: effect.Replayed || effect.Stamp.Noop}
	if effect.Event.State != nil && effect.Proof.Revision != 0 {
		proof := effect.Proof
		proof.Assignee = r.Assignee
		proof.OperationMarker = r.OperationID.String()
		ref := r.ExternalRef
		ref.LastObservation = proof.Revision
		result.Observation = tracker.AssignmentObservation{ExternalRef: ref, Assignee: r.Assignee, OperationMarker: r.OperationID.String(), Observation: proof}
	}
	if err != nil {
		return result, err
	}
	if !effect.Replayed {
		o, readErr := e.store.observe(ctx, e.budget, r.ExternalRef)
		if readErr == nil && !slices.Equal(o.State.Assignees, []string{r.Assignee}) {
			readErr = operationConflict("assignment changed after append", nil)
		}
		if readErr != nil {
			return result, &OperationError{Outcome: OutcomeApplied, AppliedThisCall: effect.AppliedThisCall, PriorMutation: effect.PriorMutation, Cause: readErr}
		}
	}
	return result, nil
}
func (*BoundAdapter) CompensateAssignment(context.Context, tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, Capability: tracker.CapabilitySafeCompensation, RecoveryCode: "reconcile_without_mutation", Message: "Hubnot cannot conditionally compensate assignment"}
}
func (b *BoundAdapter) ObserveAssignment(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	defer e.budget.close()
	if err := validateOperationReference(e.project, ref); err != nil {
		return tracker.AssignmentObservation{}, err
	}
	for {
		result, err := b.assignmentAttempt(ctx, e.budget, ref)
		if !staleObservation(err) {
			return result, err
		}
		if err := e.budget.restart(); err != nil {
			return tracker.AssignmentObservation{}, operationConflict("assignment snapshot restart budget exhausted", err)
		}
	}
}
func (b *BoundAdapter) assignmentAttempt(ctx context.Context, budget *observationBudget, ref tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	o, err := b.observe(ctx, budget, ref)
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	if len(o.State.Assignees) > 1 {
		return tracker.AssignmentObservation{}, failure(tracker.ErrorAmbiguous, "reconcile_without_mutation", "multiple native assignees", nil)
	}
	assignee := ""
	if len(o.State.Assignees) == 1 {
		assignee = o.State.Assignees[0]
	}
	history, _, err := observedPages(ctx, budget, o.Snapshot, ref.ExternalID, -1, func(cursor string) (page[issueHistoryItem], envelope, error) {
		return b.historyPage(ctx, budget, ref.ExternalID, "revisions", cursor)
	})
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	nodes, err := assignmentHistory(history, ref.ExternalID)
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	head, exists := nodes[o.Heads[0].ID]
	if !exists || head.Actor != o.Heads[0].Actor || !equalState(*head.State, o.State) {
		return tracker.AssignmentObservation{}, operationConflict("assignment history disagrees with current signed head", nil)
	}
	marker, err := assignmentOrigin(o.Heads[0].ID, o.State.Assignees, nodes, func(row issueHistoryItem) (string, error) {
		if row.Operation == "" || row.State == nil {
			return "", nil
		}
		c, mapped, err := decodeWorkState(*row.State)
		if err != nil {
			return "", err
		}
		if !mapped || len(c.Operation) == 0 {
			return "", nil
		}
		stamp, err := decodeOperationStamp(c.Operation)
		if err != nil {
			return "", nil
		}
		if stamp.Action != "assignment.project" {
			return "", nil
		}
		var intent assignmentIntent
		if err := decodeStrict([]byte(stamp.Intent), &intent); err != nil {
			return "", operationConflict("invalid assignment attribution intent", err)
		}
		a, err := assignmentAction(ref.ProjectID, tracker.ProjectAssignmentRequest{ExternalRef: intent.Reference, OperationID: stamp.Operation, Assignee: intent.Assignee, RequiredGuarantee: intent.Guarantee})
		if err != nil {
			return "", err
		}
		// Copied stamps in unrelated native events never establish an origin.
		if row.Operation != a.key {
			return "", nil
		}
		if immutableReference(intent.Reference) != immutableReference(ref) {
			return "", operationConflict("assignment origin mapping differs", nil)
		}
		original, env, err := b.lookupActorOperation(ctx, budget, row.Actor, row.Operation)
		if err != nil {
			return "", err
		}
		if env.Snapshot != o.Snapshot {
			return "", errSnapshotChanged
		}
		if !sameHistoryOperation(row, original) || a.verify(original) != nil {
			return "", operationConflict("assignment origin differs from signed history", nil)
		}
		actual, err := stateOperationStamp(original)
		if err != nil || actual.Action != a.action || actual.Operation != a.operation || actual.Intent != a.intent || actual.SemanticDigest != a.digest {
			return "", operationConflict("assignment origin attribution differs", err)
		}
		return original.EventID, nil
	})
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	operation := ""
	if marker != "" {
		stamp, _ := stateOperationStamp(issueOperation{State: nodes[marker].State})
		operation = stamp.Operation.String()
	}
	proof := o.Provider
	proof.Assignee = assignee
	proof.OperationMarker = operation
	return tracker.AssignmentObservation{ExternalRef: o.Reference, Assignee: assignee, OperationMarker: operation, Observation: proof}, nil
}

// Walk every relevant parent. A changed value breaks attribution, and merging
// distinct origins (including an unattributed path) cannot choose one arbitrarily.
func assignmentOrigin(head string, assignees []string, nodes map[string]issueHistoryItem, managed func(issueHistoryItem) (string, error)) (string, error) {
	visiting := map[string]bool{}
	memo := map[string]string{}
	done := map[string]bool{}
	var walk func(string) (string, error)
	walk = func(id string) (string, error) {
		if done[id] {
			return memo[id], nil
		}
		if visiting[id] {
			return "", operationConflict("cyclic assignment ancestry", nil)
		}
		row, ok := nodes[id]
		if !ok {
			return "", operationConflict("missing assignment ancestor", nil)
		}
		visiting[id] = true
		defer delete(visiting, id)
		if !slices.Equal(row.State.Assignees, assignees) {
			done[id] = true
			return "", nil
		}
		origin, err := managed(row)
		if err != nil {
			return "", err
		}
		if origin != "" {
			memo[id] = origin
			done[id] = true
			return origin, nil
		}
		for i, parent := range row.Parents {
			candidate, err := walk(parent)
			if err != nil {
				return "", err
			}
			if i == 0 {
				origin = candidate
			} else if candidate != origin {
				return "", failure(tracker.ErrorAttributionLost, "reconcile_without_mutation", "contradictory assignment origins across parents", nil)
			}
		}
		memo[id] = origin
		done[id] = true
		return origin, nil
	}
	return walk(head)
}
func assignmentHistory(rows []issueHistoryItem, issue string) (map[string]issueHistoryItem, error) {
	nodes := map[string]issueHistoryItem{}
	for _, row := range rows {
		_, timeErr := time.Parse(time.RFC3339Nano, row.Timestamp)
		if !validEventID(row.ID) || !validHex(row.Actor, 32) || timeErr != nil || row.State == nil || validateState(*row.State) != nil || row.Body != "" || row.Parents == nil || len(row.Parents) > 200 || (row.Operation != "" && !validOperation(row.Operation)) {
			return nil, protocolFailure(fmt.Errorf("invalid assignment history"))
		}
		if _, ok := nodes[row.ID]; ok {
			return nil, protocolFailure(fmt.Errorf("duplicate assignment history"))
		}
		if row.Kind == "issue.open" {
			if row.ID != issue || len(row.Parents) != 0 {
				return nil, protocolFailure(fmt.Errorf("invalid assignment opening"))
			}
		} else if row.Kind != "issue.revise" || len(row.Parents) == 0 {
			return nil, protocolFailure(fmt.Errorf("invalid assignment revision"))
		}
		for i, p := range row.Parents {
			if !validEventID(p) || i > 0 && row.Parents[i-1] >= p {
				return nil, protocolFailure(fmt.Errorf("invalid ancestry parents"))
			}
		}
		nodes[row.ID] = row
	}
	return nodes, nil
}
func sameHistoryOperation(row issueHistoryItem, original issueOperation) bool {
	if row.ID != original.EventID || row.Actor != original.Actor || row.Operation != original.Operation || row.Kind != original.Kind || row.Intent != original.Intent || row.Timestamp != original.Timestamp || !slices.Equal(row.Parents, original.Parents) {
		return false
	}
	if row.State != nil || original.State != nil {
		return row.State != nil && original.State != nil && equalState(*row.State, *original.State)
	}
	return original.Body != nil && row.Body == *original.Body
}
