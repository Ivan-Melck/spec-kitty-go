package hubnot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// OperationError preserves surviving effects separately from whether they
// preceded this call. A later child refusal never erases an earlier append.
type OperationError struct {
	Outcome         MutationOutcome
	PriorMutation   bool
	AppliedThisCall bool
	Cause           error
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("hubnot operation %s: %v", e.Outcome, e.Cause)
}
func (e *OperationError) Unwrap() error { return e.Cause }

type operationStore interface {
	lookup(context.Context, *observationBudget, string) (issueOperation, error)
	append(context.Context, *observationBudget, mutationRequest) (issueMutation, MutationOutcome, error)
	inventory(context.Context, *observationBudget, ids.ProjectID) (inventoryView, error)
	observe(context.Context, *observationBudget, tracker.ExternalWorkRef) (workObservation, error)
}
type boundOperationStore struct{ bound *BoundAdapter }

func (s boundOperationStore) lookup(ctx context.Context, b *observationBudget, key string) (issueOperation, error) {
	v, _, err := s.bound.lookupOperation(ctx, b, key)
	return v, err
}
func (s boundOperationStore) append(ctx context.Context, b *observationBudget, r mutationRequest) (issueMutation, MutationOutcome, error) {
	return s.bound.mutate(ctx, b, r)
}
func (s boundOperationStore) inventory(ctx context.Context, b *observationBudget, p ids.ProjectID) (inventoryView, error) {
	return collectInventory(ctx, s.bound, b, p)
}
func (s boundOperationStore) observe(ctx context.Context, b *observationBudget, r tracker.ExternalWorkRef) (workObservation, error) {
	return s.bound.observe(ctx, b, r)
}

type operationEngine struct {
	store   operationStore
	project ids.ProjectID
	actor   string
	budget  *observationBudget
}

func (b *BoundAdapter) operations(ctx context.Context) (*operationEngine, error) {
	if b == nil || ctx == nil {
		return nil, invalid("bound adapter and context required", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, &OperationError{Outcome: OutcomeUnstarted, Cause: err}
	}
	return &operationEngine{store: boundOperationStore{b}, project: b.binding.ProjectID, actor: b.config.ExpectedActor, budget: newObservationBudget()}, nil
}

const operationSchema = "go-kitty.operation/1"

type operationStamp struct {
	Schema         string                   `json:"schema"`
	Action         string                   `json:"action"`
	Operation      ids.OperationID          `json:"operation"`
	SemanticDigest string                   `json:"semantic_digest"`
	Intent         string                   `json:"intent"`
	Noop           bool                     `json:"noop"`
	Target         *tracker.ExternalWorkRef `json:"target,omitempty"`
}
type operationAction struct {
	action    string
	operation ids.OperationID
	key       string
	intent    string
	digest    string
	// Callers validate original affected fields, independent of current state.
	verify func(issueOperation) error
	// State mutations use the codec stamp. Discussion owns its body framing.
	readStamp func(issueOperation) (operationStamp, error)
}

func newOperationAction(project ids.ProjectID, operation ids.OperationID, action string, meaning any) (operationAction, error) {
	if _, err := ids.ParseProjectID(project.String()); err != nil {
		return operationAction{}, invalid("invalid operation project", err)
	}
	if _, err := ids.ParseOperationID(operation.String()); err != nil {
		return operationAction{}, invalid("invalid operation identity", err)
	}
	if action == "" || len(action) > 128 {
		return operationAction{}, invalid("invalid operation action", nil)
	}
	raw, err := json.Marshal(meaning)
	if err != nil {
		return operationAction{}, invalid("cannot encode operation intent", err)
	}
	raw, err = canonicalPayloadJSON(raw)
	if err != nil || len(raw) > maxMutationBytes {
		return operationAction{}, invalid("invalid or oversized operation intent", err)
	}
	key := sha256.Sum256([]byte(project.String() + "\x00" + operation.String() + "\x00" + action))
	digest := sha256.Sum256(raw)
	return operationAction{action: action, operation: operation, key: "gk-" + hex.EncodeToString(key[:]), intent: string(raw), digest: hex.EncodeToString(digest[:]), readStamp: stateOperationStamp}, nil
}
func (a operationAction) stamp(noop bool) operationStamp {
	return operationStamp{Schema: operationSchema, Action: a.action, Operation: a.operation, SemanticDigest: a.digest, Intent: a.intent, Noop: noop}
}
func decodeOperationStamp(raw []byte) (operationStamp, error) {
	var stamp operationStamp
	if err := decodeStrict(raw, &stamp); err != nil {
		return stamp, protocolFailure(err)
	}
	canonical, err := canonicalPayloadJSON([]byte(stamp.Intent))
	digest := sha256.Sum256(canonical)
	if err != nil || stamp.Schema != operationSchema || stamp.Action == "" || !validHex(stamp.SemanticDigest, 32) || hex.EncodeToString(digest[:]) != stamp.SemanticDigest || string(canonical) != stamp.Intent {
		return stamp, operationConflict("invalid signed operation attribution", err)
	}
	if _, err := ids.ParseOperationID(stamp.Operation.String()); err != nil {
		return stamp, operationConflict("invalid signed operation identity", err)
	}
	return stamp, nil
}
func stateOperationStamp(event issueOperation) (operationStamp, error) {
	if event.State == nil {
		return operationStamp{}, operationConflict("original operation has no state", nil)
	}
	c, mapped, err := decodeWorkState(*event.State)
	if err != nil || !mapped {
		return operationStamp{}, operationConflict("original operation lacks mapped state", err)
	}
	return decodeOperationStamp(c.Operation)
}
func stampState(state issueState, a operationAction, noop bool) (issueState, error) {
	c, mapped, err := decodeWorkState(state)
	if err != nil || !mapped {
		return issueState{}, invalid("mutation requires mapped state", err)
	}
	c.Operation, err = json.Marshal(a.stamp(noop))
	if err != nil {
		return issueState{}, err
	}
	return encodeWorkState(state, c)
}
func operationConflict(message string, cause error) error {
	return failure(tracker.ErrorRevisionConflict, "reconcile_without_mutation", message, cause)
}

type operationEffect struct {
	Event           issueOperation
	Stamp           operationStamp
	Proof           tracker.ProviderObservation
	Replayed        bool
	PriorMutation   bool
	AppliedThisCall bool
	Outcome         MutationOutcome
}

func (e *operationEngine) recover(ctx context.Context, a operationAction) (operationEffect, bool, error) {
	event, err := e.store.lookup(ctx, e.budget, a.key)
	if err != nil {
		var command *CommandError
		if errors.As(err, &command) && command.ProviderCode == "not_found" {
			return operationEffect{}, false, nil
		}
		return operationEffect{}, false, &OperationError{Outcome: OutcomeUnstarted, Cause: err}
	}
	result := operationEffect{Event: event, PriorMutation: true, Outcome: OutcomeApplied}
	fail := func(err error) (operationEffect, bool, error) {
		return result, true, &OperationError{Outcome: OutcomeApplied, PriorMutation: true, Cause: err}
	}
	if event.Actor != e.actor || event.Operation != a.key || !validEventID(event.IssueID) || !validEventID(event.EventID) || !validEventID(event.Request) {
		return fail(operationConflict("original operation identity contradicts binding", nil))
	}
	stamp, err := a.readStamp(event)
	if err != nil {
		return fail(err)
	}
	if stamp.Action != a.action || stamp.Operation != a.operation || stamp.SemanticDigest != a.digest || stamp.Intent != a.intent {
		return fail(operationConflict("operation key reused for different portable intent", nil))
	}
	if a.verify != nil {
		if err := a.verify(event); err != nil {
			return fail(err)
		}
	}
	proof, err := originalProof(event, nil)
	if err != nil {
		return fail(err)
	}
	result.Stamp = stamp
	result.Proof = proof
	result.Replayed = true
	return result, true, nil
}
func originalProof(event issueOperation, children []issueOperation) (tracker.ProviderObservation, error) {
	proof, err := observationProof(event.IssueID, "original-operation:"+event.EventID, struct {
		Kind     string           `json:"kind"`
		Event    issueOperation   `json:"event"`
		Children []issueOperation `json:"children"`
	}{"original-operation/1", event, children})
	if err != nil {
		return proof, err
	}
	// The original signed timestamp makes successful replay stable across restarts.
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return proof, protocolFailure(err)
	}
	proof.ObservedAt = timestamp
	proof.ProviderUpdatedAt = timestamp
	proof.ProviderUpdatedAtPresent = true
	return proof, nil
}

// run observes the original key before prepare can inspect mutable state. Both
// acknowledged and uncertain appends are verified by original signed lookup.
func (e *operationEngine) run(ctx context.Context, a operationAction, prepare func() (mutationRequest, error)) (operationEffect, error) {
	prior, found, err := e.recover(ctx, a)
	if err != nil || found {
		return prior, err
	}
	request, err := prepare()
	if err != nil {
		return operationEffect{}, &OperationError{Outcome: OutcomeUnstarted, Cause: err}
	}
	request.key = a.key
	receipt, outcome, appendErr := e.store.append(ctx, e.budget, request)
	result := operationEffect{Outcome: outcome, AppliedThisCall: outcome == OutcomeApplied}
	if appendErr != nil && (outcome == OutcomeUnstarted || outcome == OutcomeNotApplied) {
		return result, &OperationError{Outcome: outcome, Cause: appendErr}
	}
	recovered, found, recoverErr := e.recover(ctx, a)
	if recoverErr != nil || !found {
		cause := errors.Join(appendErr, recoverErr)
		if cause == nil {
			cause = failure(tracker.ErrorAmbiguous, "observe_then_reconcile", "dispatched operation has no conclusive original evidence", nil)
		}
		if outcome != OutcomeApplied {
			outcome = OutcomeAmbiguous
		}
		result.Outcome = outcome
		return result, &OperationError{Outcome: outcome, AppliedThisCall: result.AppliedThisCall, Cause: cause}
	}
	recovered.Replayed = receipt.Replayed
	recovered.PriorMutation = false
	recovered.AppliedThisCall = !receipt.Replayed
	recovered.Outcome = OutcomeApplied
	if receipt.EventID != "" && (receipt.EventID != recovered.Event.EventID || receipt.IssueID != recovered.Event.IssueID) {
		return recovered, &OperationError{Outcome: OutcomeApplied, AppliedThisCall: recovered.AppliedThisCall, Cause: operationConflict("mutation receipt disagrees with signed operation", nil)}
	}
	if receipt.Conflict {
		return recovered, &OperationError{Outcome: OutcomeApplied, AppliedThisCall: recovered.AppliedThisCall, Cause: operationConflict("appended operation has competing issue heads", nil)}
	}
	return recovered, nil
}
func immutableReference(ref tracker.ExternalWorkRef) tracker.ExternalWorkRef {
	ref.LastObservation = 0
	return ref
}
func validateOperationReference(project ids.ProjectID, ref tracker.ExternalWorkRef) error {
	if ref.Validate() != nil || ref.ProjectID != project || ref.Provider != "hubnot" || ref.MetadataSchema != 1 || ref.CreatedByOperation == "" || !validEventID(ref.ExternalID) {
		return invalid("complete bound Hubnot reference required", nil)
	}
	return nil
}
func verifyEventMapping(event issueOperation, project ids.ProjectID, work ids.WorkID, created ids.OperationID, external string) (workContent, error) {
	if event.State == nil || (external != "" && event.IssueID != external) {
		return workContent{}, operationConflict("original work effect targets another issue", nil)
	}
	c, mapped, err := decodeWorkState(*event.State)
	if err != nil || !mapped || c.ProjectID != project || c.WorkID != work || c.CreatedBy != created {
		return c, operationConflict("original work mapping contradicts request", err)
	}
	return c, nil
}
func sameContent(a, b workContent) bool {
	a.Operation = nil
	b.Operation = nil
	x, err := contentData(a)
	y, other := contentData(b)
	return err == nil && other == nil && bytes.Equal(x, y)
}

// relationIdentity is stable across revisions; provenance belongs to the
// signed source event and never changes the portable edge identity.
func relationIdentity(source, target string) string {
	sum := sha256.Sum256([]byte(source + "\x00blocks\x00" + target))
	return "hubnot:blocks:" + hex.EncodeToString(sum[:])
}

type relationIntent struct {
	Project  ids.ProjectID `json:"project"`
	Blocking ids.WorkID    `json:"blocking"`
	Blocked  ids.WorkID    `json:"blocked"`
	Present  bool          `json:"present"`
}

func relationAction(project ids.ProjectID, operation ids.OperationID, blocking, blocked ids.WorkID, present bool, action string) (operationAction, error) {
	if _, err := ids.ParseWorkID(blocking.String()); err != nil {
		return operationAction{}, invalid("invalid blocking work", err)
	}
	if _, err := ids.ParseWorkID(blocked.String()); err != nil || blocked == blocking {
		return operationAction{}, invalid("invalid blocked work", err)
	}
	a, err := newOperationAction(project, operation, action, relationIntent{project, blocking, blocked, present})
	if err != nil {
		return a, err
	}
	a.verify = func(event issueOperation) error {
		if event.State == nil || event.Kind != "issue.revise" || event.Intent != "revise" {
			return operationConflict("original relation action is not a revision", nil)
		}
		c, mapped, err := decodeWorkState(*event.State)
		if err != nil || !mapped || c.ProjectID != project || c.WorkID != blocking {
			return operationConflict("original relation source differs", err)
		}
		stamp, err := stateOperationStamp(event)
		if err != nil {
			return err
		}
		if stamp.Target == nil || validateOperationReference(project, *stamp.Target) != nil || stamp.Target.WorkID != blocked || stamp.Target.LastObservation != 0 {
			return operationConflict("original relation target attribution differs", nil)
		}
		found := false
		for _, r := range event.State.Relations {
			if r.Kind == "blocks" && r.Target == stamp.Target.ExternalID {
				found = true
			}
		}
		if found != present {
			return operationConflict("original relation effect differs from portable intent", nil)
		}
		return nil
	}
	return a, nil
}
func (e *operationEngine) replaceBlockingRelation(ctx context.Context, operation ids.OperationID, blocking, blocked ids.WorkID, present bool, action string) (operationEffect, error) {
	a, err := relationAction(e.project, operation, blocking, blocked, present, action)
	if err != nil {
		return operationEffect{}, err
	}
	return e.run(ctx, a, func() (mutationRequest, error) {
		v, err := e.store.inventory(ctx, e.budget, e.project)
		if err != nil {
			return mutationRequest{}, err
		}
		source, ok := v.lookupWork(blocking)
		if !ok {
			return mutationRequest{}, failure(tracker.ErrorNotFound, "reconcile_mapping", "blocking work is absent", nil)
		}
		target, ok := v.lookupWork(blocked)
		if !ok {
			return mutationRequest{}, failure(tracker.ErrorNotFound, "reconcile_mapping", "blocked work is absent", nil)
		}
		observed, err := e.store.observe(ctx, e.budget, source.reference(0))
		if err != nil {
			return mutationRequest{}, err
		}
		// Observe supplies its own complete inventory; use that same accepted view.
		target, ok = observed.Inventory.lookupWork(blocked)
		if !ok {
			return mutationRequest{}, failure(tracker.ErrorNotFound, "reconcile_mapping", "blocked work disappeared", nil)
		}
		if present && wouldCycle(observed.Inventory, source.Detail.ID, target.Detail.ID) {
			return mutationRequest{}, operationConflict("blocking relation would create a cycle", nil)
		}
		state := cloneIssueState(observed.State)
		found := false
		relations := make([]issueRelation, 0, len(state.Relations)+1)
		for _, r := range state.Relations {
			if r.Kind == "blocks" && r.Target == target.Detail.ID {
				found = true
				if !present {
					continue
				}
			}
			relations = append(relations, r)
		}
		if present && !found {
			relations = append(relations, issueRelation{Kind: "blocks", Target: target.Detail.ID})
		}
		state.Relations = relations
		c := cloneContent(observed.Content)
		stamp := a.stamp(found == present)
		ref := target.reference(0)
		stamp.Target = &ref
		c.Operation, err = json.Marshal(stamp)
		if err != nil {
			return mutationRequest{}, err
		}
		state, err = encodeWorkState(state, c)
		if err != nil {
			return mutationRequest{}, err
		}
		return mutationRequest{intent: "revise", id: source.Detail.ID, parents: headIDs(observed.Heads), state: &state}, nil
	})
}
func wouldCycle(v inventoryView, source, target string) bool {
	seen := map[string]bool{}
	queue := []string{target}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if id == source {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, edge := range v.edges {
			if edge.Source == id {
				queue = append(queue, edge.Target)
			}
		}
	}
	return false
}

// lookupActorOperation is read-only attribution discovery for any accepted signer.
// Keep validation equivalent to the pinned transport wrapper; ownership isolates
// this helper from WP01. Parity regressions prevent the two decoders drifting.
func (b *BoundAdapter) lookupActorOperation(ctx context.Context, budget *observationBudget, actor, key string) (issueOperation, envelope, error) {
	var result issueOperation
	if !validOperation(key) || !validHex(actor, 32) {
		return result, envelope{}, invalid("invalid operation key", nil)
	}
	response, err := b.execute(ctx, budget, command{argv: []string{"issue", "operation", "--actor", actor, "--operation", key, "--json"}, schema: issueSchema})
	if err != nil {
		return result, response.envelope, err
	}
	if err := decodeStrict(response.envelope.Data, &result); err != nil {
		return result, response.envelope, protocolFailure(err)
	}
	_, timeErr := time.Parse(time.RFC3339Nano, result.Timestamp)
	if result.Actor != actor || result.Operation != key || !validEventID(result.IssueID) || !validEventID(result.EventID) || !validEventID(result.Request) || result.Parents == nil || len(result.Parents) > 200 || timeErr != nil {
		return result, response.envelope, protocolFailure(errors.New("invalid original operation identity"))
	}
	for i, p := range result.Parents {
		if !validEventID(p) || (i > 0 && result.Parents[i-1] >= p) {
			return result, response.envelope, protocolFailure(errors.New("invalid operation parents"))
		}
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(response.envelope.Data, &fields)
	_, hasState := fields["state"]
	_, hasBody := fields["body"]
	if result.Kind == "issue.comment" && hasState || result.Kind != "issue.comment" && hasBody {
		return result, response.envelope, protocolFailure(errors.New("forbidden original-operation member"))
	}
	valid := false
	switch result.Intent {
	case "open":
		valid = result.Kind == "issue.open" && result.State != nil && result.Body == nil && len(result.Parents) == 0 && result.IssueID == result.EventID
	case "comment":
		valid = result.Kind == "issue.comment" && result.State == nil && result.Body != nil && len(result.Parents) == 0
	case "revise", "close", "reopen":
		valid = result.Kind == "issue.revise" && result.State != nil && result.Body == nil && len(result.Parents) == 1
	case "resolve":
		valid = result.Kind == "issue.revise" && result.State != nil && result.Body == nil && len(result.Parents) >= 2
	}
	if !valid {
		return result, response.envelope, protocolFailure(errors.New("contradictory original operation semantics"))
	}
	return result, response.envelope, nil
}
