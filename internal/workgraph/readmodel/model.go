// Package readmodel exposes immutable work-graph facts to presentation-only code.
package readmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const (
	maxTextLength = 16 << 10
	maxItems      = 10_000
)

// DriftStatus is a closed projection vocabulary. NotChecked means that no
// drift observation is available; it is not evidence of semantic cleanliness.
type DriftStatus string

const (
	DriftNotChecked      DriftStatus = "not_checked"
	DriftClean           DriftStatus = "clean"
	DriftObservationOnly DriftStatus = "observation_only"
	DriftChanged         DriftStatus = "changed"
)

func (status DriftStatus) Validate() error {
	switch status {
	case DriftNotChecked, DriftClean, DriftObservationOnly, DriftChanged:
		return nil
	default:
		return fmt.Errorf("workgraph read model: unsupported drift status")
	}
}

type CriterionParams struct {
	ID           string
	Text         string
	VerifierKind string
	Required     bool
	Metadata     map[string]any
}

type Criterion struct {
	id           string
	text         string
	verifierKind string
	required     bool
	metadataJSON []byte
}

func (value Criterion) ID() string           { return value.id }
func (value Criterion) Text() string         { return value.text }
func (value Criterion) VerifierKind() string { return value.verifierKind }
func (value Criterion) Required() bool       { return value.required }
func (value Criterion) MetadataJSON() []byte { return append([]byte(nil), value.metadataJSON...) }

type ScopeParams struct {
	Path             string
	Domain           string
	Access           string
	SerializationKey string
}

type Scope struct {
	path             string
	domain           string
	access           string
	serializationKey string
}

func (value Scope) Path() string             { return value.path }
func (value Scope) Domain() string           { return value.domain }
func (value Scope) Access() string           { return value.access }
func (value Scope) SerializationKey() string { return value.serializationKey }

type WorkItemParams struct {
	WorkID        ids.WorkID
	Title         string
	Description   string
	DesignContext string
	Criteria      []CriterionParams
	Dependencies  []ids.WorkID
	WriteScopes   []ScopeParams
}

type WorkItem struct {
	workID        ids.WorkID
	title         string
	description   string
	designContext string
	criteria      []Criterion
	dependencies  []ids.WorkID
	writeScopes   []Scope
}

func (value WorkItem) WorkID() ids.WorkID    { return value.workID }
func (value WorkItem) Title() string         { return value.title }
func (value WorkItem) Description() string   { return value.description }
func (value WorkItem) DesignContext() string { return value.designContext }
func (value WorkItem) Dependencies() []ids.WorkID {
	return append([]ids.WorkID(nil), value.dependencies...)
}
func (value WorkItem) Criteria() []Criterion { return cloneCriteria(value.criteria) }
func (value WorkItem) WriteScopes() []Scope  { return append([]Scope(nil), value.writeScopes...) }

type LaneParams struct {
	LaneID            ids.LaneID
	WorkIDs           []ids.WorkID
	DependencyLaneIDs []ids.LaneID
}

type Lane struct {
	laneID            ids.LaneID
	workIDs           []ids.WorkID
	dependencyLaneIDs []ids.LaneID
}

func (value Lane) LaneID() ids.LaneID    { return value.laneID }
func (value Lane) WorkIDs() []ids.WorkID { return append([]ids.WorkID(nil), value.workIDs...) }
func (value Lane) DependencyLaneIDs() []ids.LaneID {
	return append([]ids.LaneID(nil), value.dependencyLaneIDs...)
}

type ReconciliationDisposition string

const (
	ReconciliationContinue   ReconciliationDisposition = "continue"
	ReconciliationSupersede  ReconciliationDisposition = "supersede"
	ReconciliationInvalidate ReconciliationDisposition = "invalidate"
	ReconciliationFork       ReconciliationDisposition = "fork"
)

func (value ReconciliationDisposition) validate() error {
	switch value {
	case ReconciliationContinue, ReconciliationSupersede, ReconciliationInvalidate, ReconciliationFork:
		return nil
	default:
		return fmt.Errorf("workgraph read model: unsupported reconciliation disposition")
	}
}

type ReconciliationParams struct {
	PreviousWorkID ids.WorkID
	Disposition    ReconciliationDisposition
	CurrentWorkIDs []ids.WorkID
	Rationale      string
}

type Reconciliation struct {
	previousWorkID ids.WorkID
	disposition    ReconciliationDisposition
	currentWorkIDs []ids.WorkID
	rationale      string
}

func (value Reconciliation) PreviousWorkID() ids.WorkID             { return value.previousWorkID }
func (value Reconciliation) Disposition() ReconciliationDisposition { return value.disposition }
func (value Reconciliation) CurrentWorkIDs() []ids.WorkID {
	return append([]ids.WorkID(nil), value.currentWorkIDs...)
}
func (value Reconciliation) Rationale() string { return value.rationale }

type SnapshotParams struct {
	MissionID       ids.MissionID
	RevisionID      ids.WorkRevisionID
	Generation      uint64
	GraphDigest     string
	DriftStatus     DriftStatus
	WorkItems       []WorkItemParams
	Lanes           []LaneParams
	Reconciliations []ReconciliationParams
}

type Snapshot struct {
	missionID       ids.MissionID
	revisionID      ids.WorkRevisionID
	generation      uint64
	graphDigest     string
	driftStatus     DriftStatus
	workItems       []WorkItem
	lanes           []Lane
	reconciliations []Reconciliation
}

func NewSnapshot(params SnapshotParams) (Snapshot, error) {
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil {
		return Snapshot{}, invalid("mission identity")
	}
	if _, err := ids.ParseWorkRevisionID(params.RevisionID.String()); err != nil {
		return Snapshot{}, invalid("work revision identity")
	}
	if params.Generation == 0 {
		return Snapshot{}, invalid("generation")
	}
	if !digest(params.GraphDigest) {
		return Snapshot{}, invalid("graph digest")
	}
	if err := params.DriftStatus.Validate(); err != nil {
		return Snapshot{}, err
	}
	if len(params.WorkItems) == 0 || len(params.WorkItems) > maxItems || len(params.Lanes) == 0 || len(params.Lanes) > maxItems {
		return Snapshot{}, invalid("work or lane cardinality")
	}

	snapshot := Snapshot{missionID: params.MissionID, revisionID: params.RevisionID, generation: params.Generation, graphDigest: params.GraphDigest, driftStatus: params.DriftStatus}
	workSet := make(map[ids.WorkID]struct{}, len(params.WorkItems))
	for _, input := range params.WorkItems {
		item, err := newWorkItem(input)
		if err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := workSet[item.workID]; duplicate {
			return Snapshot{}, invalid("duplicate work identity")
		}
		workSet[item.workID] = struct{}{}
		snapshot.workItems = append(snapshot.workItems, item)
	}
	for _, item := range snapshot.workItems {
		for _, dependency := range item.dependencies {
			if dependency == item.workID {
				return Snapshot{}, invalid("self dependency")
			}
			if _, exists := workSet[dependency]; !exists {
				return Snapshot{}, invalid("unknown dependency")
			}
		}
	}
	if cyclicWork(snapshot.workItems) {
		return Snapshot{}, invalid("work dependency cycle")
	}
	sort.Slice(snapshot.workItems, func(i, j int) bool { return snapshot.workItems[i].workID < snapshot.workItems[j].workID })

	laneSet := make(map[ids.LaneID]struct{}, len(params.Lanes))
	membership := make(map[ids.WorkID]ids.LaneID, len(workSet))
	for _, input := range params.Lanes {
		lane, err := newLane(input)
		if err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := laneSet[lane.laneID]; duplicate {
			return Snapshot{}, invalid("duplicate lane identity")
		}
		laneSet[lane.laneID] = struct{}{}
		for _, workID := range lane.workIDs {
			if _, exists := workSet[workID]; !exists {
				return Snapshot{}, invalid("lane references unknown work")
			}
			if _, duplicate := membership[workID]; duplicate {
				return Snapshot{}, invalid("work belongs to multiple lanes")
			}
			membership[workID] = lane.laneID
		}
		snapshot.lanes = append(snapshot.lanes, lane)
	}
	if len(membership) != len(workSet) {
		return Snapshot{}, invalid("lane membership is incomplete")
	}
	expectedLaneEdges := make(map[ids.LaneID]map[ids.LaneID]struct{}, len(snapshot.lanes))
	for _, item := range snapshot.workItems {
		blockedLane := membership[item.workID]
		for _, dependency := range item.dependencies {
			dependencyLane := membership[dependency]
			if blockedLane == dependencyLane {
				continue
			}
			if expectedLaneEdges[blockedLane] == nil {
				expectedLaneEdges[blockedLane] = make(map[ids.LaneID]struct{})
			}
			expectedLaneEdges[blockedLane][dependencyLane] = struct{}{}
		}
	}
	for _, lane := range snapshot.lanes {
		for _, dependency := range lane.dependencyLaneIDs {
			if dependency == lane.laneID {
				return Snapshot{}, invalid("self lane dependency")
			}
			if _, exists := laneSet[dependency]; !exists {
				return Snapshot{}, invalid("unknown lane dependency")
			}
		}
		if len(lane.dependencyLaneIDs) != len(expectedLaneEdges[lane.laneID]) {
			return Snapshot{}, invalid("lane dependencies disagree with work dependencies")
		}
		for _, dependency := range lane.dependencyLaneIDs {
			if _, expected := expectedLaneEdges[lane.laneID][dependency]; !expected {
				return Snapshot{}, invalid("lane dependencies disagree with work dependencies")
			}
		}
	}
	if cyclicLanes(snapshot.lanes) {
		return Snapshot{}, invalid("lane dependency cycle")
	}
	sort.Slice(snapshot.lanes, func(i, j int) bool { return snapshot.lanes[i].laneID < snapshot.lanes[j].laneID })

	previous := make(map[ids.WorkID]struct{}, len(params.Reconciliations))
	for _, input := range params.Reconciliations {
		fact, err := newReconciliation(input, workSet)
		if err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := previous[fact.previousWorkID]; duplicate {
			return Snapshot{}, invalid("duplicate reconciliation identity")
		}
		previous[fact.previousWorkID] = struct{}{}
		snapshot.reconciliations = append(snapshot.reconciliations, fact)
	}
	sort.Slice(snapshot.reconciliations, func(i, j int) bool {
		return snapshot.reconciliations[i].previousWorkID < snapshot.reconciliations[j].previousWorkID
	})
	return snapshot, nil
}

func (value Snapshot) MissionID() ids.MissionID       { return value.missionID }
func (value Snapshot) RevisionID() ids.WorkRevisionID { return value.revisionID }
func (value Snapshot) Generation() uint64             { return value.generation }
func (value Snapshot) GraphDigest() string            { return value.graphDigest }
func (value Snapshot) DriftStatus() DriftStatus       { return value.driftStatus }
func (value Snapshot) WorkItems() []WorkItem {
	result := make([]WorkItem, len(value.workItems))
	for i := range value.workItems {
		result[i] = cloneWorkItem(value.workItems[i])
	}
	return result
}
func (value Snapshot) Lanes() []Lane {
	result := make([]Lane, len(value.lanes))
	for i := range value.lanes {
		result[i] = cloneLane(value.lanes[i])
	}
	return result
}
func (value Snapshot) Reconciliations() []Reconciliation {
	result := make([]Reconciliation, len(value.reconciliations))
	for i := range value.reconciliations {
		result[i] = cloneReconciliation(value.reconciliations[i])
	}
	return result
}
func (value Snapshot) Validate() error {
	params := SnapshotParams{MissionID: value.missionID, RevisionID: value.revisionID, Generation: value.generation, GraphDigest: value.graphDigest, DriftStatus: value.driftStatus}
	for _, item := range value.workItems {
		input := WorkItemParams{WorkID: item.workID, Title: item.title, Description: item.description, DesignContext: item.designContext, Dependencies: item.Dependencies()}
		for _, criterion := range item.criteria {
			metadata, err := decodeMetadata(criterion.metadataJSON)
			if err != nil {
				return invalid("criterion metadata")
			}
			input.Criteria = append(input.Criteria, CriterionParams{ID: criterion.id, Text: criterion.text, VerifierKind: criterion.verifierKind, Required: criterion.required, Metadata: metadata})
		}
		for _, scope := range item.writeScopes {
			input.WriteScopes = append(input.WriteScopes, ScopeParams{Path: scope.path, Domain: scope.domain, Access: scope.access, SerializationKey: scope.serializationKey})
		}
		params.WorkItems = append(params.WorkItems, input)
	}
	for _, lane := range value.lanes {
		params.Lanes = append(params.Lanes, LaneParams{LaneID: lane.laneID, WorkIDs: lane.WorkIDs(), DependencyLaneIDs: lane.DependencyLaneIDs()})
	}
	for _, fact := range value.reconciliations {
		params.Reconciliations = append(params.Reconciliations, ReconciliationParams{PreviousWorkID: fact.previousWorkID, Disposition: fact.disposition, CurrentWorkIDs: fact.CurrentWorkIDs(), Rationale: fact.rationale})
	}
	_, err := NewSnapshot(params)
	return err
}

func newWorkItem(input WorkItemParams) (WorkItem, error) {
	if _, err := ids.ParseWorkID(input.WorkID.String()); err != nil || !safeText(input.Title, true) || !safeText(input.Description, false) || !safeText(input.DesignContext, false) {
		return WorkItem{}, invalid("work item")
	}
	item := WorkItem{workID: input.WorkID, title: input.Title, description: input.Description, designContext: input.DesignContext}
	seenCriteria := make(map[string]struct{}, len(input.Criteria))
	for _, inputCriterion := range input.Criteria {
		if !safeText(inputCriterion.ID, true) || !safeText(inputCriterion.Text, true) || !safeText(inputCriterion.VerifierKind, true) {
			return WorkItem{}, invalid("criterion")
		}
		if _, duplicate := seenCriteria[inputCriterion.ID]; duplicate {
			return WorkItem{}, invalid("duplicate criterion")
		}
		seenCriteria[inputCriterion.ID] = struct{}{}
		encoded, err := canonicalMetadata(inputCriterion.Metadata)
		if err != nil {
			return WorkItem{}, invalid("criterion metadata")
		}
		item.criteria = append(item.criteria, Criterion{id: inputCriterion.ID, text: inputCriterion.Text, verifierKind: inputCriterion.VerifierKind, required: inputCriterion.Required, metadataJSON: encoded})
	}
	sort.Slice(item.criteria, func(i, j int) bool { return item.criteria[i].id < item.criteria[j].id })
	seenDependencies := make(map[ids.WorkID]struct{}, len(input.Dependencies))
	for _, dependency := range input.Dependencies {
		if _, err := ids.ParseWorkID(dependency.String()); err != nil {
			return WorkItem{}, invalid("dependency identity")
		}
		if _, duplicate := seenDependencies[dependency]; duplicate {
			return WorkItem{}, invalid("duplicate dependency")
		}
		seenDependencies[dependency] = struct{}{}
		item.dependencies = append(item.dependencies, dependency)
	}
	sort.Slice(item.dependencies, func(i, j int) bool { return item.dependencies[i] < item.dependencies[j] })
	seenScopes := make(map[string]struct{}, len(input.WriteScopes))
	for _, inputScope := range input.WriteScopes {
		if !safeText(inputScope.Path, true) || !safeText(inputScope.Domain, true) || (inputScope.Access != "read_only" && inputScope.Access != "write_exclusive") || !safeText(inputScope.SerializationKey, false) {
			return WorkItem{}, invalid("write scope")
		}
		key := inputScope.Path + "\x00" + inputScope.Domain + "\x00" + inputScope.Access + "\x00" + inputScope.SerializationKey
		if _, duplicate := seenScopes[key]; duplicate {
			return WorkItem{}, invalid("duplicate write scope")
		}
		seenScopes[key] = struct{}{}
		item.writeScopes = append(item.writeScopes, Scope{path: inputScope.Path, domain: inputScope.Domain, access: inputScope.Access, serializationKey: inputScope.SerializationKey})
	}
	sort.Slice(item.writeScopes, func(i, j int) bool { return scopeKey(item.writeScopes[i]) < scopeKey(item.writeScopes[j]) })
	return item, nil
}

func newLane(input LaneParams) (Lane, error) {
	if _, err := ids.ParseLaneID(input.LaneID.String()); err != nil || len(input.WorkIDs) == 0 {
		return Lane{}, invalid("lane")
	}
	lane := Lane{laneID: input.LaneID}
	workSeen := map[ids.WorkID]struct{}{}
	laneSeen := map[ids.LaneID]struct{}{}
	for _, id := range input.WorkIDs {
		if _, err := ids.ParseWorkID(id.String()); err != nil {
			return Lane{}, invalid("lane member")
		}
		if _, ok := workSeen[id]; ok {
			return Lane{}, invalid("duplicate lane member")
		}
		workSeen[id] = struct{}{}
		lane.workIDs = append(lane.workIDs, id)
	}
	for _, id := range input.DependencyLaneIDs {
		if _, err := ids.ParseLaneID(id.String()); err != nil {
			return Lane{}, invalid("lane dependency")
		}
		if _, ok := laneSeen[id]; ok {
			return Lane{}, invalid("duplicate lane dependency")
		}
		laneSeen[id] = struct{}{}
		lane.dependencyLaneIDs = append(lane.dependencyLaneIDs, id)
	}
	sort.Slice(lane.workIDs, func(i, j int) bool { return lane.workIDs[i] < lane.workIDs[j] })
	sort.Slice(lane.dependencyLaneIDs, func(i, j int) bool { return lane.dependencyLaneIDs[i] < lane.dependencyLaneIDs[j] })
	return lane, nil
}

func newReconciliation(input ReconciliationParams, current map[ids.WorkID]struct{}) (Reconciliation, error) {
	if _, err := ids.ParseWorkID(input.PreviousWorkID.String()); err != nil {
		return Reconciliation{}, invalid("reconciliation identity")
	}
	if err := input.Disposition.validate(); err != nil {
		return Reconciliation{}, err
	}
	if !safeText(input.Rationale, false) {
		return Reconciliation{}, invalid("reconciliation rationale")
	}
	value := Reconciliation{previousWorkID: input.PreviousWorkID, disposition: input.Disposition, rationale: input.Rationale}
	seen := map[ids.WorkID]struct{}{}
	for _, id := range input.CurrentWorkIDs {
		if _, ok := current[id]; !ok {
			return Reconciliation{}, invalid("reconciliation references unknown work")
		}
		if _, ok := seen[id]; ok {
			return Reconciliation{}, invalid("duplicate reconciliation target")
		}
		seen[id] = struct{}{}
		value.currentWorkIDs = append(value.currentWorkIDs, id)
	}
	if (input.Disposition == ReconciliationContinue || input.Disposition == ReconciliationSupersede || input.Disposition == ReconciliationFork) && len(value.currentWorkIDs) == 0 {
		return Reconciliation{}, invalid("reconciliation target is required")
	}
	sort.Slice(value.currentWorkIDs, func(i, j int) bool { return value.currentWorkIDs[i] < value.currentWorkIDs[j] })
	return value, nil
}

func canonicalMetadata(value map[string]any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxTextLength {
		return nil, fmt.Errorf("metadata")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func decodeMetadata(encoded []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
func safeText(value string, required bool) bool {
	if required && value == "" || len(value) > maxTextLength || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r == 0 {
			return false
		}
	}
	return true
}
func digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func invalid(field string) error { return fmt.Errorf("workgraph read model: invalid %s", field) }
func scopeKey(v Scope) string {
	return strings.Join([]string{v.path, v.domain, v.access, v.serializationKey}, "\x00")
}
func cloneCriteria(values []Criterion) []Criterion {
	out := make([]Criterion, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].metadataJSON = append([]byte(nil), values[i].metadataJSON...)
	}
	return out
}
func cloneWorkItem(v WorkItem) WorkItem {
	v.criteria = cloneCriteria(v.criteria)
	v.dependencies = append([]ids.WorkID(nil), v.dependencies...)
	v.writeScopes = append([]Scope(nil), v.writeScopes...)
	return v
}
func cloneLane(v Lane) Lane {
	v.workIDs = append([]ids.WorkID(nil), v.workIDs...)
	v.dependencyLaneIDs = append([]ids.LaneID(nil), v.dependencyLaneIDs...)
	return v
}
func cloneReconciliation(v Reconciliation) Reconciliation {
	v.currentWorkIDs = append([]ids.WorkID(nil), v.currentWorkIDs...)
	return v
}

func cyclicWork(values []WorkItem) bool {
	edges := map[ids.WorkID][]ids.WorkID{}
	for _, v := range values {
		edges[v.workID] = v.dependencies
	}
	state := map[ids.WorkID]uint8{}
	var visit func(ids.WorkID) bool
	visit = func(id ids.WorkID) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range edges {
		if visit(id) {
			return true
		}
	}
	return false
}
func cyclicLanes(values []Lane) bool {
	edges := map[ids.LaneID][]ids.LaneID{}
	for _, v := range values {
		edges[v.laneID] = v.dependencyLaneIDs
	}
	state := map[ids.LaneID]uint8{}
	var visit func(ids.LaneID) bool
	visit = func(id ids.LaneID) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range edges {
		if visit(id) {
			return true
		}
	}
	return false
}
