package workgraph

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type Disposition string

const (
	Continue   Disposition = "continue"
	Supersede  Disposition = "supersede"
	Invalidate Disposition = "invalidate"
	Fork       Disposition = "fork"
)

type WorkStatus string

const (
	StatusActive     WorkStatus = "active"
	StatusHandedOff  WorkStatus = "handed_off"
	StatusApproved   WorkStatus = "approved"
	StatusIntegrated WorkStatus = "integrated"
)

var allStatuses = []WorkStatus{StatusActive, StatusHandedOff, StatusApproved, StatusIntegrated}

type ForkEdge struct {
	FromOldWorkID ids.WorkID `json:"from_old_work_id"`
	ToNewWorkID   ids.WorkID `json:"to_new_work_id"`
}

type Consequence struct {
	RunMayContinue             bool   `json:"run_may_continue"`
	HandoffEligible            bool   `json:"handoff_eligible"`
	IntegrationEligible        bool   `json:"integration_eligible"`
	ExpireAuthority            bool   `json:"expire_authority"`
	QuarantineWorkspace        bool   `json:"quarantine_workspace"`
	PreservesIntegratedHistory bool   `json:"preserves_integrated_history"`
	Reason                     string `json:"reason"`
}

type ReconcileRequest struct {
	ID          string
	Old         Revision
	New         Revision
	Disposition Disposition
	ActorID     ids.ActorID
	Rationale   string
	OldWorkIDs  []ids.WorkID
	NewWorkIDs  []ids.WorkID
	ForkEdges   []ForkEdge
	OccurredAt  time.Time
}

type consequenceKey struct {
	workID ids.WorkID
	status WorkStatus
}

type Reconciliation struct {
	id                string
	oldRevisionID     ids.WorkRevisionID
	newRevisionID     ids.WorkRevisionID
	oldGraphDigest    string
	newGraphDigest    string
	oldCriteriaDigest string
	newCriteriaDigest string
	disposition       Disposition
	actorID           ids.ActorID
	rationale         string
	oldWorkIDs        []ids.WorkID
	newWorkIDs        []ids.WorkID
	forkEdges         []ForkEdge
	occurredAt        time.Time
	consequences      map[consequenceKey]Consequence
	requestDigest     string
}

func Reconcile(request ReconcileRequest) (Reconciliation, error) {
	if _, err := ids.ParseOperationID(request.ID); err != nil {
		return Reconciliation{}, &Error{Kind: KindInvalidReconciliation, Message: "reconciliation ID is invalid", Cause: err}
	}
	if _, err := ids.ParseActorID(request.ActorID.String()); err != nil {
		return Reconciliation{}, &Error{Kind: KindInvalidReconciliation, Message: "ActorID is invalid", Cause: err}
	}
	if request.Rationale == "" || request.OccurredAt.IsZero() {
		return Reconciliation{}, newError(KindInvalidReconciliation, "rationale and occurrence time are required", nil)
	}
	if request.Old.ID() == "" || request.New.ID() == "" || request.Old.ProjectID() != request.New.ProjectID() || request.Old.MissionID() != request.New.MissionID() || request.New.Number() <= request.Old.Number() {
		return Reconciliation{}, newError(KindInvalidReconciliation, "revisions must be finalized generations in the same project and mission", nil)
	}
	switch request.Disposition {
	case Continue, Supersede, Invalidate, Fork:
	default:
		return Reconciliation{}, newError(KindInvalidReconciliation, "unknown reconciliation disposition", map[string]string{"disposition": string(request.Disposition)})
	}
	oldWorkIDs, err := canonicalWorkIDs(request.OldWorkIDs, request.Old, "old")
	if err != nil {
		return Reconciliation{}, err
	}
	newWorkIDs, err := canonicalWorkIDs(request.NewWorkIDs, request.New, "new")
	if err != nil {
		return Reconciliation{}, err
	}
	if len(oldWorkIDs) == 0 || len(newWorkIDs) == 0 {
		return Reconciliation{}, newError(KindInvalidReconciliation, "old and new affected WorkIDs are required", nil)
	}
	edges := append([]ForkEdge(nil), request.ForkEdges...)
	if request.Disposition == Fork {
		if err := validateFork(oldWorkIDs, newWorkIDs, edges); err != nil {
			return Reconciliation{}, err
		}
	} else if len(edges) != 0 {
		return Reconciliation{}, newError(KindInvalidReconciliation, "fork edges are legal only for fork disposition", nil)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromOldWorkID == edges[j].FromOldWorkID {
			return edges[i].ToNewWorkID < edges[j].ToNewWorkID
		}
		return edges[i].FromOldWorkID < edges[j].FromOldWorkID
	})
	reconciliation := Reconciliation{
		id: request.ID, oldRevisionID: request.Old.ID(), newRevisionID: request.New.ID(),
		oldGraphDigest: request.Old.GraphDigest(), newGraphDigest: request.New.GraphDigest(),
		oldCriteriaDigest: request.Old.CriteriaDigest(), newCriteriaDigest: request.New.CriteriaDigest(),
		disposition: request.Disposition, actorID: request.ActorID, rationale: request.Rationale,
		oldWorkIDs: oldWorkIDs, newWorkIDs: newWorkIDs, forkEdges: edges, occurredAt: request.OccurredAt.UTC(),
		consequences: make(map[consequenceKey]Consequence, len(oldWorkIDs)*len(allStatuses)),
	}
	for _, workID := range oldWorkIDs {
		for _, status := range allStatuses {
			reconciliation.consequences[consequenceKey{workID, status}] = consequenceFor(request.Disposition, status)
		}
	}
	reconciliation.requestDigest = reconciliation.semanticDigest()
	return reconciliation, nil
}

func canonicalWorkIDs(input []ids.WorkID, revision Revision, side string) ([]ids.WorkID, error) {
	result := append([]ids.WorkID(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for index, workID := range result {
		if index > 0 && result[index-1] == workID {
			return nil, newError(KindInvalidReconciliation, side+" WorkID occurs more than once", map[string]string{"work_id": workID.String()})
		}
		if !revision.ContainsWork(workID) {
			return nil, newError(KindInvalidReconciliation, side+" WorkID is absent from its WorkRevision", map[string]string{"work_id": workID.String(), "work_revision_id": revision.ID().String()})
		}
	}
	return result, nil
}

func validateFork(oldWorkIDs, newWorkIDs []ids.WorkID, edges []ForkEdge) error {
	old := make(map[ids.WorkID]struct{}, len(oldWorkIDs))
	newSet := make(map[ids.WorkID]struct{}, len(newWorkIDs))
	for _, workID := range oldWorkIDs {
		old[workID] = struct{}{}
	}
	for _, workID := range newWorkIDs {
		if _, reused := old[workID]; reused {
			return newError(KindInvalidReconciliation, "fork must create distinct WorkIDs", map[string]string{"work_id": workID.String()})
		}
		newSet[workID] = struct{}{}
	}
	if len(edges) == 0 {
		return newError(KindInvalidReconciliation, "fork requires explicit old-to-new edges", nil)
	}
	covered := make(map[ids.WorkID]struct{}, len(newWorkIDs))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if _, exists := old[edge.FromOldWorkID]; !exists {
			return newError(KindInvalidReconciliation, "fork edge source is not an affected old WorkID", map[string]string{"work_id": edge.FromOldWorkID.String()})
		}
		if _, exists := newSet[edge.ToNewWorkID]; !exists {
			return newError(KindInvalidReconciliation, "fork edge target is not an affected new WorkID", map[string]string{"work_id": edge.ToNewWorkID.String()})
		}
		key := edge.FromOldWorkID.String() + "\x00" + edge.ToNewWorkID.String()
		if _, exists := seen[key]; exists {
			return newError(KindInvalidReconciliation, "fork edge occurs more than once", nil)
		}
		seen[key] = struct{}{}
		covered[edge.ToNewWorkID] = struct{}{}
	}
	if len(covered) != len(newWorkIDs) {
		return newError(KindInvalidReconciliation, "every forked WorkID requires an explicit edge", nil)
	}
	return nil
}

func consequenceFor(disposition Disposition, status WorkStatus) Consequence {
	if status == StatusIntegrated {
		return Consequence{PreservesIntegratedHistory: true, Reason: fmt.Sprintf("%s records evidence only; integrated history remains immutable", disposition)}
	}
	switch disposition {
	case Continue:
		return Consequence{RunMayContinue: true, HandoffEligible: true, IntegrationEligible: true, Reason: "continue preserves old-contract completion, handoff, and integration eligibility"}
	case Supersede:
		return Consequence{RunMayContinue: true, Reason: "supersede permits evidence preservation but refuses any new acceptable handoff or integration"}
	case Invalidate:
		consequence := Consequence{Reason: "invalidate refuses old-contract handoff and integration eligibility"}
		if status == StatusActive {
			consequence.ExpireAuthority = true
			consequence.QuarantineWorkspace = true
			consequence.Reason = "invalidate expires active authority and quarantines its workspace"
		}
		return consequence
	case Fork:
		return Consequence{RunMayContinue: true, HandoffEligible: true, IntegrationEligible: true, Reason: "fork preserves the old line while distinct WorkIDs carry the new explicit lines"}
	default:
		return Consequence{Reason: "unknown disposition is ineligible"}
	}
}

func (r Reconciliation) ID() string                        { return r.id }
func (r Reconciliation) OldRevisionID() ids.WorkRevisionID { return r.oldRevisionID }
func (r Reconciliation) NewRevisionID() ids.WorkRevisionID { return r.newRevisionID }
func (r Reconciliation) OldGraphDigest() string            { return r.oldGraphDigest }
func (r Reconciliation) NewGraphDigest() string            { return r.newGraphDigest }
func (r Reconciliation) OldCriteriaDigest() string         { return r.oldCriteriaDigest }
func (r Reconciliation) NewCriteriaDigest() string         { return r.newCriteriaDigest }
func (r Reconciliation) Disposition() Disposition          { return r.disposition }
func (r Reconciliation) ActorID() ids.ActorID              { return r.actorID }
func (r Reconciliation) Rationale() string                 { return r.rationale }
func (r Reconciliation) OldWorkIDs() []ids.WorkID          { return append([]ids.WorkID(nil), r.oldWorkIDs...) }
func (r Reconciliation) NewWorkIDs() []ids.WorkID          { return append([]ids.WorkID(nil), r.newWorkIDs...) }
func (r Reconciliation) ForkEdges() []ForkEdge             { return append([]ForkEdge(nil), r.forkEdges...) }
func (r Reconciliation) OccurredAt() time.Time             { return r.occurredAt }
func (r Reconciliation) RequestDigest() string             { return r.requestDigest }
func (r Reconciliation) Consequence(workID ids.WorkID, status WorkStatus) (Consequence, bool) {
	consequence, ok := r.consequences[consequenceKey{workID, status}]
	return consequence, ok
}

func (r Reconciliation) semanticDigest() string {
	payload := struct {
		ID                string             `json:"id"`
		OldRevisionID     ids.WorkRevisionID `json:"old_work_revision_id"`
		NewRevisionID     ids.WorkRevisionID `json:"new_work_revision_id"`
		OldGraphDigest    string             `json:"old_graph_digest"`
		NewGraphDigest    string             `json:"new_graph_digest"`
		OldCriteriaDigest string             `json:"old_criteria_digest"`
		NewCriteriaDigest string             `json:"new_criteria_digest"`
		Disposition       Disposition        `json:"disposition"`
		ActorID           ids.ActorID        `json:"actor_id"`
		Rationale         string             `json:"rationale"`
		OldWorkIDs        []ids.WorkID       `json:"old_work_ids"`
		NewWorkIDs        []ids.WorkID       `json:"new_work_ids"`
		ForkEdges         []ForkEdge         `json:"fork_edges"`
		OccurredAt        time.Time          `json:"occurred_at"`
	}{
		ID: r.id, OldRevisionID: r.oldRevisionID, NewRevisionID: r.newRevisionID,
		OldGraphDigest: r.oldGraphDigest, NewGraphDigest: r.newGraphDigest,
		OldCriteriaDigest: r.oldCriteriaDigest, NewCriteriaDigest: r.newCriteriaDigest,
		Disposition: r.disposition, ActorID: r.actorID, Rationale: r.rationale,
		OldWorkIDs: r.OldWorkIDs(), NewWorkIDs: r.NewWorkIDs(), ForkEdges: r.ForkEdges(), OccurredAt: r.occurredAt,
	}
	encoded, _ := json.Marshal(payload)
	return digest(encoded)
}
