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

type EligibilityOperation string

const (
	EligibilityHandoff     EligibilityOperation = "handoff"
	EligibilityReview      EligibilityOperation = "review"
	EligibilityIntegration EligibilityOperation = "integration"
)

// EligibilityFact is an immutable projection of reconciliation authority for
// one exact old-generation consumer operation. Permission is derived from the
// stored Consequence; callers cannot author it independently.
type EligibilityFact struct {
	reconciliationID string
	oldRevisionID    ids.WorkRevisionID
	newRevisionID    ids.WorkRevisionID
	workID           ids.WorkID
	status           WorkStatus
	disposition      Disposition
	operation        EligibilityOperation
	permitted        bool
	reason           string
	legacyV1         bool
}

// NewLegacyEligibilityFact projects the frozen v1 WorkEligibility query. It is
// explicitly incomplete: v1 rows do not expose a successor ID or disposition.
func NewLegacyEligibilityFact(reconciliationID string, oldRevisionID ids.WorkRevisionID, workID ids.WorkID, status WorkStatus, operation EligibilityOperation, consequence Consequence) (EligibilityFact, error) {
	if reconciliationID != "" {
		if _, err := ids.ParseOperationID(reconciliationID); err != nil {
			return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility reconciliation identity is invalid", nil)
		}
	}
	if _, err := ids.ParseWorkRevisionID(oldRevisionID.String()); err != nil {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility revision identity is invalid", nil)
	}
	if _, err := ids.ParseWorkID(workID.String()); err != nil {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility work identity is invalid", nil)
	}
	if !knownWorkStatus(status) || !boundedText(consequence.Reason, 256) {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility status or reason is invalid", nil)
	}
	permitted := false
	switch operation {
	case EligibilityHandoff:
		if status != StatusActive {
			return EligibilityFact{}, newError(KindInvalidReconciliation, "handoff eligibility requires active work", nil)
		}
		permitted = consequence.HandoffEligible
	case EligibilityReview:
		if status != StatusHandedOff {
			return EligibilityFact{}, newError(KindInvalidReconciliation, "review eligibility requires handed-off work", nil)
		}
		permitted = consequence.HandoffEligible
	case EligibilityIntegration:
		if status != StatusApproved {
			return EligibilityFact{}, newError(KindInvalidReconciliation, "integration eligibility requires approved work", nil)
		}
		permitted = consequence.IntegrationEligible
	default:
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility operation is unknown", nil)
	}
	return EligibilityFact{reconciliationID: reconciliationID, oldRevisionID: oldRevisionID, workID: workID, status: status, operation: operation, permitted: permitted, reason: consequence.Reason, legacyV1: true}, nil
}

// NewEligibilityFact reconstructs a complete mixed-reconciliation fact from
// durable typed authority. Permission and reason are always derived from the
// disposition/status law; callers cannot provide either value.
func NewEligibilityFact(reconciliationID string, oldRevisionID, newRevisionID ids.WorkRevisionID, workID ids.WorkID, status WorkStatus, disposition Disposition, operation EligibilityOperation) (EligibilityFact, error) {
	if reconciliationID == "" || !knownDisposition(disposition) {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "complete eligibility authority is invalid", nil)
	}
	if _, err := ids.ParseWorkRevisionID(newRevisionID.String()); err != nil {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility successor identity is invalid", nil)
	}
	consequence := consequenceFor(disposition, status)
	fact, err := NewLegacyEligibilityFact(reconciliationID, oldRevisionID, workID, status, operation, consequence)
	if err != nil {
		return EligibilityFact{}, err
	}
	fact.newRevisionID = newRevisionID
	fact.disposition = disposition
	fact.legacyV1 = false
	return fact, nil
}

func (fact EligibilityFact) ReconciliationID() string          { return fact.reconciliationID }
func (fact EligibilityFact) OldRevisionID() ids.WorkRevisionID { return fact.oldRevisionID }
func (fact EligibilityFact) NewRevisionID() ids.WorkRevisionID { return fact.newRevisionID }
func (fact EligibilityFact) WorkID() ids.WorkID                { return fact.workID }
func (fact EligibilityFact) Status() WorkStatus                { return fact.status }
func (fact EligibilityFact) Disposition() Disposition          { return fact.disposition }
func (fact EligibilityFact) Operation() EligibilityOperation   { return fact.operation }
func (fact EligibilityFact) Permitted() bool                   { return fact.permitted }
func (fact EligibilityFact) Reason() string                    { return fact.reason }
func (fact EligibilityFact) LegacyV1() bool                    { return fact.legacyV1 }

func (fact EligibilityFact) IsZero() bool { return fact == (EligibilityFact{}) }

// Complete reports whether every field is a valid, internally consistent
// mixed-reconciliation authority fact, including derived permission/reason.
func (fact EligibilityFact) Complete() bool {
	want, err := NewEligibilityFact(fact.reconciliationID, fact.oldRevisionID, fact.newRevisionID, fact.workID, fact.status, fact.disposition, fact.operation)
	return err == nil && fact == want
}

func (fact EligibilityFact) SameComplete(other EligibilityFact) bool {
	return fact.Complete() && other.Complete() && fact == other
}

func (fact EligibilityFact) MatchesLegacy(oldRevisionID ids.WorkRevisionID, workID ids.WorkID, status WorkStatus, operation EligibilityOperation) bool {
	return fact.legacyV1 && fact.newRevisionID == "" && fact.disposition == "" && fact.oldRevisionID == oldRevisionID && fact.workID == workID && fact.status == status && fact.operation == operation
}

func (fact EligibilityFact) MatchesComplete(reconciliationID string, oldRevisionID, newRevisionID ids.WorkRevisionID, workID ids.WorkID, status WorkStatus, disposition Disposition, operation EligibilityOperation) bool {
	return !fact.legacyV1 && fact.reconciliationID == reconciliationID && fact.oldRevisionID == oldRevisionID && fact.newRevisionID == newRevisionID && fact.workID == workID && fact.status == status && fact.disposition == disposition && fact.operation == operation
}

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

type WorkStatusAssignment struct {
	WorkID ids.WorkID
	Status WorkStatus
}

type WorkDispositionDecisionParams struct {
	OldWorkID   ids.WorkID
	Disposition Disposition
	NewWorkIDs  []ids.WorkID
	ForkEdges   []ForkEdge
	Rationale   string
}

type MixedReconcileRequest struct {
	ID         string
	Old        RevisionV2
	New        RevisionV2
	Drift      DriftReport
	Statuses   []WorkStatusAssignment
	Decisions  []WorkDispositionDecisionParams
	ActorID    ids.ActorID
	Rationale  string
	OccurredAt time.Time
}

type WorkDispositionDecision struct {
	oldWorkID   ids.WorkID
	disposition Disposition
	newWorkIDs  []ids.WorkID
	forkEdges   []ForkEdge
	rationale   string
}

func (decision WorkDispositionDecision) OldWorkID() ids.WorkID    { return decision.oldWorkID }
func (decision WorkDispositionDecision) Disposition() Disposition { return decision.disposition }
func (decision WorkDispositionDecision) NewWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), decision.newWorkIDs...)
}
func (decision WorkDispositionDecision) ForkEdges() []ForkEdge {
	return append([]ForkEdge(nil), decision.forkEdges...)
}
func (decision WorkDispositionDecision) Rationale() string { return decision.rationale }

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

type MixedReconciliation struct {
	id                string
	oldRevisionID     ids.WorkRevisionID
	newRevisionID     ids.WorkRevisionID
	oldGraphDigest    string
	newGraphDigest    string
	oldCriteriaDigest string
	newCriteriaDigest string
	actorID           ids.ActorID
	rationale         string
	oldWorkIDs        []ids.WorkID
	newWorkIDs        []ids.WorkID
	forkEdges         []ForkEdge
	occurredAt        time.Time
	consequences      map[consequenceKey]Consequence
	decisions         []WorkDispositionDecision
	assignedStatuses  map[ids.WorkID]WorkStatus
	driftReportID     string
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

// ReconcileMixed validates one complete immutable per-work decision plan.
// The legacy Reconcile constructor above remains the frozen v1 path used by
// historical control-store records.
func ReconcileMixed(request MixedReconcileRequest) (MixedReconciliation, error) {
	if _, err := ids.ParseOperationID(request.ID); err != nil {
		return MixedReconciliation{}, &Error{Kind: KindInvalidReconciliation, Message: "reconciliation ID is invalid", Cause: err}
	}
	if _, err := ids.ParseActorID(request.ActorID.String()); err != nil {
		return MixedReconciliation{}, &Error{Kind: KindInvalidReconciliation, Message: "ActorID is invalid", Cause: err}
	}
	if !boundedText(request.Rationale, 4096) || request.OccurredAt.IsZero() || request.OccurredAt.Location() != time.UTC {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "bounded rationale and UTC occurrence time are required", nil)
	}
	if err := validateMixedRevisionPair(request.Old, request.New, request.Drift); err != nil {
		return MixedReconciliation{}, err
	}
	affectedOld := request.Drift.AffectedOldWorkIDs()
	affectedNew := request.Drift.AffectedNewWorkIDs()
	if request.Drift.SemanticStatus() != SemanticChanged || (len(affectedOld) == 0 && len(affectedNew) == 0) {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "mixed reconciliation requires executable semantic drift", nil)
	}
	if !exactAffectedWorkSet(affectedOld, request.Old) || !exactAffectedWorkSet(affectedNew, request.New) {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "drift affected work is outside the bound revision pair", nil)
	}
	statuses, err := canonicalMixedStatuses(request.Statuses, affectedOld)
	if err != nil {
		return MixedReconciliation{}, err
	}
	decisions, err := canonicalMixedDecisions(request.Decisions, request.Old, request.New, request.Drift, statuses)
	if err != nil {
		return MixedReconciliation{}, err
	}
	reconciliation := MixedReconciliation{
		id: request.ID, oldRevisionID: request.Old.ID(), newRevisionID: request.New.ID(),
		oldGraphDigest: request.Old.GraphDigest(), newGraphDigest: request.New.GraphDigest(),
		oldCriteriaDigest: request.Old.CriteriaDigest(), newCriteriaDigest: request.New.CriteriaDigest(),
		actorID: request.ActorID, rationale: request.Rationale, oldWorkIDs: affectedOld,
		newWorkIDs: affectedNew, occurredAt: request.OccurredAt,
		decisions: decisions, assignedStatuses: statuses, driftReportID: request.Drift.ID(),
		consequences: make(map[consequenceKey]Consequence, len(decisions)*len(allStatuses)),
	}
	for _, decision := range decisions {
		reconciliation.forkEdges = append(reconciliation.forkEdges, decision.forkEdges...)
		for _, status := range allStatuses {
			reconciliation.consequences[consequenceKey{decision.oldWorkID, status}] = consequenceFor(decision.disposition, status)
		}
	}
	sort.Slice(reconciliation.forkEdges, func(i, j int) bool {
		if reconciliation.forkEdges[i].FromOldWorkID == reconciliation.forkEdges[j].FromOldWorkID {
			return reconciliation.forkEdges[i].ToNewWorkID < reconciliation.forkEdges[j].ToNewWorkID
		}
		return reconciliation.forkEdges[i].FromOldWorkID < reconciliation.forkEdges[j].FromOldWorkID
	})
	reconciliation.requestDigest = reconciliation.mixedSemanticDigest()
	return reconciliation, nil
}

func exactAffectedWorkSet(values []ids.WorkID, revision RevisionV2) bool {
	seen := make(map[ids.WorkID]struct{}, len(values))
	for _, workID := range values {
		if _, duplicate := seen[workID]; duplicate || !containsV2Work(revision, workID) {
			return false
		}
		seen[workID] = struct{}{}
	}
	return true
}

func validateMixedRevisionPair(oldRevision, newRevision RevisionV2, report DriftReport) error {
	if validateDriftRevision(oldRevision) != nil || validateDriftRevision(newRevision) != nil ||
		oldRevision.ProjectID() != newRevision.ProjectID() || oldRevision.MissionID() != newRevision.MissionID() ||
		newRevision.Generation() != oldRevision.Generation()+1 || newRevision.PredecessorID() != oldRevision.ID() {
		return newError(KindInvalidReconciliation, "successor must be the exact next immutable generation in the same scope", nil)
	}
	if report.ID() == "" || report.MissionID() != oldRevision.MissionID() || report.BaseRevisionID() != oldRevision.ID() ||
		report.CandidateRevisionID() != newRevision.ID() || report.BaseGraphDigest() != oldRevision.GraphDigest() ||
		report.CandidateGraphDigest() != newRevision.GraphDigest() {
		return newError(KindInvalidReconciliation, "drift evidence does not bind the exact predecessor and successor", nil)
	}
	return nil
}

func canonicalMixedStatuses(input []WorkStatusAssignment, affected []ids.WorkID) (map[ids.WorkID]WorkStatus, error) {
	expected := make(map[ids.WorkID]bool, len(affected))
	for _, workID := range affected {
		expected[workID] = true
	}
	result := make(map[ids.WorkID]WorkStatus, len(input))
	for _, assignment := range input {
		if !expected[assignment.WorkID] || result[assignment.WorkID] != "" || !knownWorkStatus(assignment.Status) {
			return nil, newError(KindInvalidReconciliation, "work statuses must exactly and uniquely cover affected old work", map[string]string{"work_id": assignment.WorkID.String()})
		}
		result[assignment.WorkID] = assignment.Status
	}
	if len(result) != len(expected) {
		return nil, newError(KindInvalidReconciliation, "work statuses omit affected old work", nil)
	}
	return result, nil
}

func canonicalMixedDecisions(input []WorkDispositionDecisionParams, oldRevision, newRevision RevisionV2, report DriftReport, statuses map[ids.WorkID]WorkStatus) ([]WorkDispositionDecision, error) {
	expected := make(map[ids.WorkID]bool, len(report.affectedOld))
	newAffected := make(map[ids.WorkID]bool, len(report.affectedNew))
	for _, workID := range report.affectedOld {
		expected[workID] = true
	}
	for _, workID := range report.affectedNew {
		newAffected[workID] = true
	}
	seen := make(map[ids.WorkID]bool, len(input))
	result := make([]WorkDispositionDecision, 0, len(input))
	for _, proposed := range input {
		if !expected[proposed.OldWorkID] || seen[proposed.OldWorkID] || !containsV2Work(oldRevision, proposed.OldWorkID) {
			return nil, newError(KindInvalidReconciliation, "decisions must exactly and uniquely cover affected old work", map[string]string{"work_id": proposed.OldWorkID.String()})
		}
		seen[proposed.OldWorkID] = true
		if !knownDisposition(proposed.Disposition) || !boundedText(proposed.Rationale, 1024) {
			return nil, newError(KindInvalidReconciliation, "decision disposition or rationale is invalid", map[string]string{"work_id": proposed.OldWorkID.String()})
		}
		newWorkIDs, err := canonicalMixedNewWorkIDs(proposed.NewWorkIDs, newRevision, newAffected)
		if err != nil {
			return nil, err
		}
		edges := append([]ForkEdge(nil), proposed.ForkEdges...)
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].FromOldWorkID == edges[j].FromOldWorkID {
				return edges[i].ToNewWorkID < edges[j].ToNewWorkID
			}
			return edges[i].FromOldWorkID < edges[j].FromOldWorkID
		})
		if err := validateMixedDecisionShape(proposed.OldWorkID, proposed.Disposition, newWorkIDs, edges, statuses[proposed.OldWorkID], report); err != nil {
			return nil, err
		}
		result = append(result, WorkDispositionDecision{proposed.OldWorkID, proposed.Disposition, newWorkIDs, edges, proposed.Rationale})
	}
	if len(seen) != len(expected) {
		return nil, newError(KindInvalidReconciliation, "decisions omit affected old work", nil)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].oldWorkID < result[j].oldWorkID })
	return result, nil
}

func canonicalMixedNewWorkIDs(input []ids.WorkID, revision RevisionV2, affected map[ids.WorkID]bool) ([]ids.WorkID, error) {
	result := append([]ids.WorkID(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for index, workID := range result {
		if (index > 0 && result[index-1] == workID) || !containsV2Work(revision, workID) || !affected[workID] {
			return nil, newError(KindInvalidReconciliation, "decision successor must be one unique affected new work item", map[string]string{"work_id": workID.String()})
		}
	}
	return result, nil
}

func validateMixedDecisionShape(oldWorkID ids.WorkID, disposition Disposition, newWorkIDs []ids.WorkID, edges []ForkEdge, status WorkStatus, report DriftReport) error {
	if disposition != Fork && len(edges) != 0 {
		return newError(KindInvalidReconciliation, "fork edges are legal only for fork decisions", map[string]string{"work_id": oldWorkID.String()})
	}
	switch disposition {
	case Continue:
		if len(newWorkIDs) != 1 || newWorkIDs[0] != oldWorkID || !continueCompatible(report, oldWorkID) {
			return newError(KindInvalidReconciliation, "continue requires an unchanged same-identity work contract", map[string]string{"work_id": oldWorkID.String()})
		}
	case Supersede:
		if len(newWorkIDs) == 0 || containsWorkID(newWorkIDs, oldWorkID) {
			return newError(KindInvalidReconciliation, "supersede requires explicit distinct replacement work", map[string]string{"work_id": oldWorkID.String()})
		}
	case Invalidate:
		if containsWorkID(newWorkIDs, oldWorkID) || (status == StatusIntegrated && len(newWorkIDs) == 0) {
			return newError(KindInvalidReconciliation, "integrated invalidation requires explicit distinct repair work", map[string]string{"work_id": oldWorkID.String()})
		}
	case Fork:
		if len(newWorkIDs) < 2 {
			return newError(KindInvalidReconciliation, "fork requires at least two distinct successor identities", map[string]string{"work_id": oldWorkID.String()})
		}
		if err := validateFork([]ids.WorkID{oldWorkID}, newWorkIDs, edges); err != nil {
			return err
		}
	}
	return nil
}

func continueCompatible(report DriftReport, workID ids.WorkID) bool {
	for _, change := range report.changes {
		if change.oldWorkID == workID || change.newWorkID == workID {
			return false
		}
	}
	return true
}

func knownDisposition(value Disposition) bool {
	return value == Continue || value == Supersede || value == Invalidate || value == Fork
}
func knownWorkStatus(value WorkStatus) bool {
	return value == StatusActive || value == StatusHandedOff || value == StatusApproved || value == StatusIntegrated
}
func containsWorkID(values []ids.WorkID, target ids.WorkID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func containsV2Work(revision RevisionV2, workID ids.WorkID) bool {
	for _, item := range revision.WorkItems() {
		if item.WorkID() == workID {
			return true
		}
	}
	return false
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

func (r MixedReconciliation) ID() string                        { return r.id }
func (r MixedReconciliation) OldRevisionID() ids.WorkRevisionID { return r.oldRevisionID }
func (r MixedReconciliation) NewRevisionID() ids.WorkRevisionID { return r.newRevisionID }
func (r MixedReconciliation) OldGraphDigest() string            { return r.oldGraphDigest }
func (r MixedReconciliation) NewGraphDigest() string            { return r.newGraphDigest }
func (r MixedReconciliation) OldCriteriaDigest() string         { return r.oldCriteriaDigest }
func (r MixedReconciliation) NewCriteriaDigest() string         { return r.newCriteriaDigest }
func (r MixedReconciliation) ActorID() ids.ActorID              { return r.actorID }
func (r MixedReconciliation) Rationale() string                 { return r.rationale }
func (r MixedReconciliation) OldWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), r.oldWorkIDs...)
}
func (r MixedReconciliation) NewWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), r.newWorkIDs...)
}
func (r MixedReconciliation) ForkEdges() []ForkEdge { return append([]ForkEdge(nil), r.forkEdges...) }
func (r MixedReconciliation) OccurredAt() time.Time { return r.occurredAt }
func (r MixedReconciliation) RequestDigest() string { return r.requestDigest }
func (r MixedReconciliation) DriftReportID() string { return r.driftReportID }
func (r MixedReconciliation) Decisions() []WorkDispositionDecision {
	result := make([]WorkDispositionDecision, len(r.decisions))
	for index, decision := range r.decisions {
		result[index] = decision
		result[index].newWorkIDs = decision.NewWorkIDs()
		result[index].forkEdges = decision.ForkEdges()
	}
	return result
}
func (r MixedReconciliation) Decision(workID ids.WorkID) (WorkDispositionDecision, bool) {
	for _, decision := range r.decisions {
		if decision.oldWorkID == workID {
			decision.newWorkIDs = decision.NewWorkIDs()
			decision.forkEdges = decision.ForkEdges()
			return decision, true
		}
	}
	return WorkDispositionDecision{}, false
}
func (r MixedReconciliation) AssignedStatus(workID ids.WorkID) (WorkStatus, bool) {
	status, ok := r.assignedStatuses[workID]
	return status, ok
}
func (r MixedReconciliation) Consequence(workID ids.WorkID, status WorkStatus) (Consequence, bool) {
	consequence, ok := r.consequences[consequenceKey{workID, status}]
	return consequence, ok
}

func (r MixedReconciliation) EligibilityFact(workID ids.WorkID, status WorkStatus, operation EligibilityOperation) (EligibilityFact, error) {
	decision, found := r.Decision(workID)
	if !found {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility work is absent from the mixed reconciliation", nil)
	}
	consequence, found := r.Consequence(workID, status)
	if !found {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility consequence is absent from the mixed reconciliation", nil)
	}
	fact, err := NewEligibilityFact(r.ID(), r.OldRevisionID(), r.NewRevisionID(), workID, status, decision.Disposition(), operation)
	if err != nil {
		return EligibilityFact{}, err
	}
	if fact.reason != consequence.Reason {
		return EligibilityFact{}, newError(KindInvalidReconciliation, "eligibility consequence differs from the mixed reconciliation", nil)
	}
	return fact, nil
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

func (r MixedReconciliation) mixedSemanticDigest() string {
	type decisionWire struct {
		OldWorkID   ids.WorkID   `json:"old_work_id"`
		Disposition Disposition  `json:"disposition"`
		NewWorkIDs  []ids.WorkID `json:"new_work_ids"`
		ForkEdges   []ForkEdge   `json:"fork_edges"`
		Rationale   string       `json:"rationale"`
		Status      WorkStatus   `json:"work_status"`
	}
	decisions := make([]decisionWire, len(r.decisions))
	for index, decision := range r.decisions {
		decisions[index] = decisionWire{decision.oldWorkID, decision.disposition, decision.NewWorkIDs(), decision.ForkEdges(), decision.rationale, r.assignedStatuses[decision.oldWorkID]}
	}
	payload := struct {
		SchemaVersion     uint16             `json:"schema_version"`
		ID                string             `json:"reconciliation_id"`
		DriftReportID     string             `json:"drift_report_id"`
		OldRevisionID     ids.WorkRevisionID `json:"old_work_revision_id"`
		NewRevisionID     ids.WorkRevisionID `json:"new_work_revision_id"`
		OldGraphDigest    string             `json:"old_graph_digest"`
		NewGraphDigest    string             `json:"new_graph_digest"`
		OldCriteriaDigest string             `json:"old_criteria_digest"`
		NewCriteriaDigest string             `json:"new_criteria_digest"`
		Decisions         []decisionWire     `json:"decisions"`
		ActorID           ids.ActorID        `json:"actor_id"`
		Rationale         string             `json:"rationale"`
		OccurredAt        string             `json:"occurred_at"`
	}{2, r.id, r.driftReportID, r.oldRevisionID, r.newRevisionID, r.oldGraphDigest, r.newGraphDigest, r.oldCriteriaDigest, r.newCriteriaDigest, decisions, r.actorID, r.rationale, r.occurredAt.Format(time.RFC3339Nano)}
	encoded, _ := json.Marshal(payload)
	return digest(append([]byte("spec-kitty/reconciliation/v2\x00"), encoded...))
}
