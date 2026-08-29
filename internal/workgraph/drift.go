package workgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type SemanticStatus string

const (
	SemanticClean           SemanticStatus = "clean"
	SemanticObservationOnly SemanticStatus = "observation_only"
	SemanticChanged         SemanticStatus = "changed"
)

type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeRemoved  ChangeKind = "removed"
	ChangeModified ChangeKind = "modified"
)

type SemanticField string

const (
	FieldPlanningSourceCommit SemanticField = "planning_source_commit"
	FieldMissionDefinition    SemanticField = "mission_definition"
	FieldDependencyPolicy     SemanticField = "dependency_policy"
	FieldWorkIdentity         SemanticField = "work.identity"
	FieldTitle                SemanticField = "work.title"
	FieldDescription          SemanticField = "work.description"
	FieldDesignContext        SemanticField = "work.design_context"
	FieldCriterionIdentity    SemanticField = "work.criteria.identity"
	FieldCriterionText        SemanticField = "work.criteria.text"
	FieldCriterionVerifier    SemanticField = "work.criteria.verifier"
	FieldCriterionRequired    SemanticField = "work.criteria.required"
	FieldCriterionMetadata    SemanticField = "work.criteria.metadata"
	FieldDependencies         SemanticField = "work.dependencies"
	FieldDependencyAncestry   SemanticField = "work.dependency_ancestry"
	FieldWriteScopes          SemanticField = "work.write_scopes"
	FieldProviderMapping      SemanticField = "work.provider_mapping"
	FieldLaneProjection       SemanticField = "work.lane_projection"
	FieldSemanticDigest       SemanticField = "graph.semantic_digest"
)

type DriftChange struct {
	id            string
	kind          ChangeKind
	field         SemanticField
	oldWorkID     ids.WorkID
	newWorkID     ids.WorkID
	oldCommitment string
	newCommitment string
}

func (change DriftChange) ID() string            { return change.id }
func (change DriftChange) Kind() ChangeKind      { return change.kind }
func (change DriftChange) Field() SemanticField  { return change.field }
func (change DriftChange) OldWorkID() ids.WorkID { return change.oldWorkID }
func (change DriftChange) NewWorkID() ids.WorkID { return change.newWorkID }
func (change DriftChange) OldCommitment() string { return change.oldCommitment }
func (change DriftChange) NewCommitment() string { return change.newCommitment }

type ProviderRevisionEvidence struct {
	WorkID      ids.WorkID `json:"work_id"`
	Provider    string     `json:"provider"`
	ExternalID  string     `json:"external_id"`
	OldRevision string     `json:"old_revision"`
	NewRevision string     `json:"new_revision"`
}

type DriftReport struct {
	id                   string
	missionID            ids.MissionID
	baseRevisionID       ids.WorkRevisionID
	baseGraphDigest      string
	candidateRevisionID  ids.WorkRevisionID
	candidateGraphDigest string
	observedAt           time.Time
	status               SemanticStatus
	changes              []DriftChange
	affectedOld          []ids.WorkID
	affectedNew          []ids.WorkID
	oldReasons           map[ids.WorkID][]string
	newReasons           map[ids.WorkID][]string
	providerEvidence     []ProviderRevisionEvidence
}

func (report DriftReport) ID() string                              { return report.id }
func (report DriftReport) MissionID() ids.MissionID                { return report.missionID }
func (report DriftReport) BaseRevisionID() ids.WorkRevisionID      { return report.baseRevisionID }
func (report DriftReport) BaseGraphDigest() string                 { return report.baseGraphDigest }
func (report DriftReport) CandidateRevisionID() ids.WorkRevisionID { return report.candidateRevisionID }
func (report DriftReport) CandidateGraphDigest() string            { return report.candidateGraphDigest }
func (report DriftReport) ObservedAt() time.Time                   { return report.observedAt }
func (report DriftReport) SemanticStatus() SemanticStatus          { return report.status }
func (report DriftReport) Changes() []DriftChange {
	return append([]DriftChange(nil), report.changes...)
}
func (report DriftReport) AffectedOldWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), report.affectedOld...)
}
func (report DriftReport) AffectedNewWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), report.affectedNew...)
}
func (report DriftReport) OldReasons(workID ids.WorkID) []string {
	return append([]string(nil), report.oldReasons[workID]...)
}
func (report DriftReport) NewReasons(workID ids.WorkID) []string {
	return append([]string(nil), report.newReasons[workID]...)
}
func (report DriftReport) ProviderRevisions() []ProviderRevisionEvidence {
	return append([]ProviderRevisionEvidence(nil), report.providerEvidence...)
}
func (report DriftReport) Encode() ([]byte, error) { return json.Marshal(driftReportWireValue(report)) }

// DetectDrift compares two already-normalized immutable graphs. Revision,
// finalizer, governance, and observation time remain audit evidence; only the
// executable graph and provider mapping participate in semantic drift.
func DetectDrift(base, candidate RevisionV2, observedAt time.Time) (DriftReport, error) {
	if base.ID() == "" || candidate.ID() == "" || observedAt.IsZero() || observedAt.Location() != time.UTC {
		return DriftReport{}, invalidV2("drift", "drift comparison requires two normalized revisions and a UTC observation time", nil)
	}
	if err := validateDriftRevision(base); err != nil {
		return DriftReport{}, invalidV2("base_work_revision", "drift base is not a normalized WorkRevision v2", err)
	}
	if err := validateDriftRevision(candidate); err != nil {
		return DriftReport{}, invalidV2("candidate_work_revision", "drift candidate is not a normalized WorkRevision v2", err)
	}
	if base.ProjectID() != candidate.ProjectID() || base.MissionID() != candidate.MissionID() {
		return DriftReport{}, newError(KindInvalidRevision, "drift candidate belongs to another project or mission", map[string]string{"base_work_revision_id": base.ID().String(), "candidate_work_revision_id": candidate.ID().String()})
	}
	changes := compareRevisionSemantics(base, candidate)
	providerEvidence, observationChanged := compareProviderEvidence(base.ProviderObservations(), candidate.ProviderObservations())
	status := SemanticClean
	if len(changes) != 0 {
		status = SemanticChanged
	} else if observationChanged {
		status = SemanticObservationOnly
	}
	oldAffected, newAffected, oldReasons, newReasons := deriveAffected(changes, workIDs(base.WorkItems()), workIDs(candidate.WorkItems()))
	report := DriftReport{
		missionID: base.MissionID(), baseRevisionID: base.ID(), baseGraphDigest: base.GraphDigest(),
		candidateRevisionID: candidate.ID(), candidateGraphDigest: candidate.GraphDigest(), observedAt: observedAt,
		status: status, changes: changes, affectedOld: oldAffected, affectedNew: newAffected,
		oldReasons: oldReasons, newReasons: newReasons, providerEvidence: providerEvidence,
	}
	report.id = driftReportCommitment(report)
	return report, nil
}

func validateDriftRevision(revision RevisionV2) error {
	encoded, err := revision.Encode()
	if err != nil {
		return err
	}
	_, err = DecodeRevisionV2(encoded)
	return err
}

func compareRevisionSemantics(base, candidate RevisionV2) []DriftChange {
	changes := make([]DriftChange, 0)
	addGlobal := func(field SemanticField, oldValue, newValue any) {
		if canonicalEqual(oldValue, newValue) {
			return
		}
		changes = append(changes, newDriftChange(ChangeModified, field, "", "", oldValue, newValue))
	}
	addGlobal(FieldPlanningSourceCommit, base.PlanningSourceCommit(), candidate.PlanningSourceCommit())
	addGlobal(FieldMissionDefinition, base.MissionDefinition(), candidate.MissionDefinition())
	addGlobal(FieldDependencyPolicy, base.DependencyPolicy(), candidate.DependencyPolicy())

	oldItems := itemIndex(base.WorkItems())
	newItems := itemIndex(candidate.WorkItems())
	allIDs := unionWorkIDs(oldItems, newItems)
	oldMappings := mappingIndex(base.ProviderObservations())
	newMappings := mappingIndex(candidate.ProviderObservations())
	oldLanes := laneViews(base.Lanes())
	newLanes := laneViews(candidate.Lanes())
	for _, workID := range allIDs {
		oldItem, inOld := oldItems[workID]
		newItem, inNew := newItems[workID]
		switch {
		case !inOld:
			changes = append(changes, newDriftChange(ChangeAdded, FieldWorkIdentity, "", workID, nil, workID))
			continue
		case !inNew:
			changes = append(changes, newDriftChange(ChangeRemoved, FieldWorkIdentity, workID, "", workID, nil))
			continue
		}
		compareWorkItem(&changes, workID, oldItem, newItem)
		if !canonicalEqual(oldMappings[workID], newMappings[workID]) {
			changes = append(changes, newDriftChange(ChangeModified, FieldProviderMapping, workID, workID, oldMappings[workID], newMappings[workID]))
		}
		if !canonicalEqual(oldLanes[workID], newLanes[workID]) {
			changes = append(changes, newDriftChange(ChangeModified, FieldLaneProjection, workID, workID, oldLanes[workID], newLanes[workID]))
		}
	}
	oldAncestry := dependencyAncestry(oldItems)
	newAncestry := dependencyAncestry(newItems)
	for _, workID := range allIDs {
		oldAncestors, oldOK := oldAncestry[workID]
		newAncestors, newOK := newAncestry[workID]
		if oldOK && newOK && !canonicalEqual(oldAncestors, newAncestors) {
			changes = append(changes, newDriftChange(ChangeModified, FieldDependencyAncestry, workID, workID, oldAncestors, newAncestors))
		}
	}
	if (base.GraphDigest() != candidate.GraphDigest() || base.CriteriaDigest() != candidate.CriteriaDigest()) && len(changes) == 0 {
		changes = append(changes, newDriftChange(ChangeModified, FieldSemanticDigest, "", "", []string{base.GraphDigest(), base.CriteriaDigest()}, []string{candidate.GraphDigest(), candidate.CriteriaDigest()}))
	}
	sort.Slice(changes, func(i, j int) bool { return driftChangeKey(changes[i]) < driftChangeKey(changes[j]) })
	return changes
}

func compareWorkItem(changes *[]DriftChange, workID ids.WorkID, oldItem, newItem WorkItemV2) {
	compare := func(field SemanticField, oldValue, newValue any) {
		if !canonicalEqual(oldValue, newValue) {
			*changes = append(*changes, newDriftChange(ChangeModified, field, workID, workID, oldValue, newValue))
		}
	}
	compare(FieldTitle, oldItem.Title(), newItem.Title())
	compare(FieldDescription, oldItem.Description(), newItem.Description())
	compare(FieldDesignContext, oldItem.DesignContext(), newItem.DesignContext())
	oldCriteria := criterionIndex(oldItem.Criteria())
	newCriteria := criterionIndex(newItem.Criteria())
	criterionIDs := unionStrings(oldCriteria, newCriteria)
	for _, criterionID := range criterionIDs {
		oldCriterion, oldOK := oldCriteria[criterionID]
		newCriterion, newOK := newCriteria[criterionID]
		switch {
		case !oldOK:
			*changes = append(*changes, newDriftChange(ChangeAdded, FieldCriterionIdentity, workID, workID, nil, criterionID))
		case !newOK:
			*changes = append(*changes, newDriftChange(ChangeRemoved, FieldCriterionIdentity, workID, workID, criterionID, nil))
		default:
			compareCriterion := func(field SemanticField, oldValue, newValue any) {
				if !canonicalEqual(oldValue, newValue) {
					*changes = append(*changes, newDriftChange(ChangeModified, field, workID, workID, []any{criterionID, oldValue}, []any{criterionID, newValue}))
				}
			}
			compareCriterion(FieldCriterionText, oldCriterion.Text(), newCriterion.Text())
			compareCriterion(FieldCriterionVerifier, oldCriterion.VerifierKind(), newCriterion.VerifierKind())
			compareCriterion(FieldCriterionRequired, oldCriterion.Required(), newCriterion.Required())
			compareCriterion(FieldCriterionMetadata, oldCriterion.Metadata(), newCriterion.Metadata())
		}
	}
	compare(FieldDependencies, oldItem.Dependencies(), newItem.Dependencies())
	compare(FieldWriteScopes, oldItem.WriteScopes(), newItem.WriteScopes())
}

type providerMapping struct{ Provider, ExternalID string }
type laneView struct {
	Members      []ids.WorkID
	Dependencies []ids.LaneID
}

func itemIndex(items []WorkItemV2) map[ids.WorkID]WorkItemV2 {
	result := make(map[ids.WorkID]WorkItemV2, len(items))
	for _, item := range items {
		result[item.WorkID()] = item
	}
	return result
}
func criterionIndex(criteria []CriterionV2) map[string]CriterionV2 {
	result := make(map[string]CriterionV2, len(criteria))
	for _, criterion := range criteria {
		result[criterion.ID()] = criterion
	}
	return result
}
func mappingIndex(observations []ProviderObservationV2) map[ids.WorkID]providerMapping {
	result := make(map[ids.WorkID]providerMapping, len(observations))
	for _, item := range observations {
		result[item.WorkID] = providerMapping{item.Provider, item.ExternalID}
	}
	return result
}
func laneViews(lanes []LaneV2) map[ids.WorkID]laneView {
	result := make(map[ids.WorkID]laneView)
	for _, lane := range lanes {
		view := laneView{Members: lane.WorkIDs(), Dependencies: lane.DependencyLaneIDs()}
		for _, workID := range view.Members {
			result[workID] = view
		}
	}
	return result
}

func dependencyAncestry(items map[ids.WorkID]WorkItemV2) map[ids.WorkID][]ids.WorkID {
	result := make(map[ids.WorkID][]ids.WorkID, len(items))
	for workID := range items {
		seen := map[ids.WorkID]bool{}
		var visit func(ids.WorkID)
		visit = func(current ids.WorkID) {
			for _, dependency := range items[current].Dependencies() {
				if !seen[dependency] {
					seen[dependency] = true
					visit(dependency)
				}
			}
		}
		visit(workID)
		ancestors := make([]ids.WorkID, 0, len(seen))
		for ancestor := range seen {
			ancestors = append(ancestors, ancestor)
		}
		sort.Slice(ancestors, func(i, j int) bool { return ancestors[i] < ancestors[j] })
		result[workID] = ancestors
	}
	return result
}

func compareProviderEvidence(oldValues, newValues []ProviderObservationV2) ([]ProviderRevisionEvidence, bool) {
	oldIndex := make(map[ids.WorkID]ProviderObservationV2, len(oldValues))
	newIndex := make(map[ids.WorkID]ProviderObservationV2, len(newValues))
	for _, value := range oldValues {
		oldIndex[value.WorkID] = value
	}
	for _, value := range newValues {
		newIndex[value.WorkID] = value
	}
	all := unionWorkIDs(oldIndex, newIndex)
	evidence := make([]ProviderRevisionEvidence, 0, len(all))
	changed := false
	for _, workID := range all {
		oldValue := oldIndex[workID]
		newValue := newIndex[workID]
		entry := ProviderRevisionEvidence{WorkID: workID, Provider: newValue.Provider, ExternalID: newValue.ExternalID, OldRevision: oldValue.Revision, NewRevision: newValue.Revision}
		if entry.Provider == "" {
			entry.Provider = oldValue.Provider
		}
		if entry.ExternalID == "" {
			entry.ExternalID = oldValue.ExternalID
		}
		evidence = append(evidence, entry)
		changed = changed || oldValue != newValue
	}
	return evidence, changed
}

func deriveAffected(changes []DriftChange, allOld, allNew []ids.WorkID) ([]ids.WorkID, []ids.WorkID, map[ids.WorkID][]string, map[ids.WorkID][]string) {
	oldReasons := map[ids.WorkID][]string{}
	newReasons := map[ids.WorkID][]string{}
	for _, change := range changes {
		if change.oldWorkID != "" {
			oldReasons[change.oldWorkID] = append(oldReasons[change.oldWorkID], change.id)
		} else if change.newWorkID == "" {
			for _, workID := range allOld {
				oldReasons[workID] = append(oldReasons[workID], change.id)
			}
		}
		if change.newWorkID != "" {
			newReasons[change.newWorkID] = append(newReasons[change.newWorkID], change.id)
		} else if change.oldWorkID == "" {
			for _, workID := range allNew {
				newReasons[workID] = append(newReasons[workID], change.id)
			}
		}
	}
	oldIDs := sortedReasonIDs(oldReasons)
	newIDs := sortedReasonIDs(newReasons)
	return oldIDs, newIDs, oldReasons, newReasons
}

func workIDs(items []WorkItemV2) []ids.WorkID {
	result := make([]ids.WorkID, len(items))
	for index := range items {
		result[index] = items[index].WorkID()
	}
	return result
}

func sortedReasonIDs(input map[ids.WorkID][]string) []ids.WorkID {
	result := make([]ids.WorkID, 0, len(input))
	for workID := range input {
		result = append(result, workID)
		sort.Strings(input[workID])
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func newDriftChange(kind ChangeKind, field SemanticField, oldWorkID, newWorkID ids.WorkID, oldValue, newValue any) DriftChange {
	change := DriftChange{kind: kind, field: field, oldWorkID: oldWorkID, newWorkID: newWorkID, oldCommitment: semanticCommitment(field, oldValue), newCommitment: semanticCommitment(field, newValue)}
	change.id = semanticCommitment("drift-change", []any{kind, field, oldWorkID, newWorkID, change.oldCommitment, change.newCommitment})
	return change
}

func driftChangeKey(change DriftChange) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", change.field, change.oldWorkID, change.newWorkID, change.kind, change.id)
}

func canonicalEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func semanticCommitment(domain any, value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(fmt.Sprintf("spec-kitty/drift/v1/%v\x00", domain)), encoded...))
	return hex.EncodeToString(sum[:])
}

type driftChangeWire struct {
	ID            string        `json:"change_id"`
	Kind          ChangeKind    `json:"change_kind"`
	Field         SemanticField `json:"field"`
	OldWorkID     ids.WorkID    `json:"old_work_id,omitempty"`
	NewWorkID     ids.WorkID    `json:"new_work_id,omitempty"`
	OldCommitment string        `json:"old_commitment"`
	NewCommitment string        `json:"new_commitment"`
}

type driftReasonWire struct {
	Side      string     `json:"side"`
	WorkID    ids.WorkID `json:"work_id"`
	ChangeIDs []string   `json:"change_ids"`
}

type driftReportWire struct {
	ReportID             string                     `json:"drift_report_id"`
	MissionID            ids.MissionID              `json:"mission_id"`
	BaseRevisionID       ids.WorkRevisionID         `json:"base_work_revision_id"`
	BaseGraphDigest      string                     `json:"base_graph_digest"`
	CandidateRevisionID  ids.WorkRevisionID         `json:"candidate_work_revision_id"`
	CandidateGraphDigest string                     `json:"candidate_graph_digest"`
	ObservedAt           string                     `json:"observed_at"`
	Status               SemanticStatus             `json:"semantic_status"`
	Changes              []driftChangeWire          `json:"changes"`
	AffectedOld          []ids.WorkID               `json:"affected_old_work_ids"`
	AffectedNew          []ids.WorkID               `json:"affected_new_work_ids"`
	Reasons              []driftReasonWire          `json:"reasons"`
	ProviderEvidence     []ProviderRevisionEvidence `json:"provider_revisions"`
}

func driftReportWireValue(report DriftReport) driftReportWire {
	changes := make([]driftChangeWire, len(report.changes))
	for index, change := range report.changes {
		changes[index] = driftChangeWire{change.id, change.kind, change.field, change.oldWorkID, change.newWorkID, change.oldCommitment, change.newCommitment}
	}
	reasons := make([]driftReasonWire, 0, len(report.affectedOld)+len(report.affectedNew))
	for _, workID := range report.affectedOld {
		reasons = append(reasons, driftReasonWire{"old", workID, report.OldReasons(workID)})
	}
	for _, workID := range report.affectedNew {
		reasons = append(reasons, driftReasonWire{"new", workID, report.NewReasons(workID)})
	}
	return driftReportWire{report.id, report.missionID, report.baseRevisionID, report.baseGraphDigest, report.candidateRevisionID, report.candidateGraphDigest, report.observedAt.Format(time.RFC3339Nano), report.status, changes, report.AffectedOldWorkIDs(), report.AffectedNewWorkIDs(), reasons, report.ProviderRevisions()}
}

func driftReportCommitment(report DriftReport) string {
	wire := driftReportWireValue(report)
	wire.ReportID = ""
	return semanticCommitment("report", wire)
}

func unionWorkIDs[A any, B any](left map[ids.WorkID]A, right map[ids.WorkID]B) []ids.WorkID {
	set := make(map[ids.WorkID]struct{}, len(left)+len(right))
	for workID := range left {
		set[workID] = struct{}{}
	}
	for workID := range right {
		set[workID] = struct{}{}
	}
	result := make([]ids.WorkID, 0, len(set))
	for workID := range set {
		result = append(result, workID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func unionStrings[A any, B any](left map[string]A, right map[string]B) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		set[key] = struct{}{}
	}
	for key := range right {
		set[key] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
