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

type ErrorKind string

const (
	KindFinalized              ErrorKind = "work_revision_finalized"
	KindAmbiguousID            ErrorKind = "ambiguous_id"
	KindMissingNode            ErrorKind = "missing_node"
	KindInvalidLane            ErrorKind = "invalid_lane"
	KindCycle                  ErrorKind = "work_graph_cycle"
	KindLaneCycle              ErrorKind = "lane_projection_cycle"
	KindInvalidRevision        ErrorKind = "invalid_work_revision"
	KindRevisionConflict       ErrorKind = "work_revision_conflict"
	KindInvalidReconciliation  ErrorKind = "invalid_reconciliation"
	KindReconciliationConflict ErrorKind = "reconciliation_conflict"
	KindNotFound               ErrorKind = "work_graph_not_found"
)

type Error struct {
	Kind    ErrorKind         `json:"kind"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Cause   error             `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsKind(err error, kind ErrorKind) bool {
	for err != nil {
		if graphError, ok := err.(*Error); ok && graphError.Kind == kind {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		unwrapped, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func newError(kind ErrorKind, message string, fields map[string]string) *Error {
	return &Error{Kind: kind, Message: message, Fields: fields}
}

type LaneID string

type Lane struct {
	ID       LaneID `json:"id"`
	ParentID LaneID `json:"parent_id,omitempty"`
}

type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type SourceRevision struct {
	Provider string `json:"provider"`
	Revision string `json:"revision"`
}

type WorkItem struct {
	ID              ids.WorkID       `json:"id"`
	Title           string           `json:"title"`
	LaneID          LaneID           `json:"lane_id"`
	Criteria        []Criterion      `json:"criteria"`
	Dependencies    []ids.WorkID     `json:"dependencies"`
	SourceRevisions []SourceRevision `json:"source_revisions"`
}

type Draft struct {
	projectID ids.ProjectID
	missionID ids.MissionID
	lanes     map[LaneID]Lane
	items     map[ids.WorkID]WorkItem
	finalized bool
	invalid   error
}

func NewDraft(projectID ids.ProjectID, missionID ids.MissionID) *Draft {
	return &Draft{projectID: projectID, missionID: missionID, lanes: make(map[LaneID]Lane), items: make(map[ids.WorkID]WorkItem)}
}

func (d *Draft) AddLane(lane Lane) error {
	if err := d.mutable(); err != nil {
		return err
	}
	if lane.ID == "" {
		return d.remember(newError(KindInvalidLane, "lane ID is required", nil))
	}
	if _, exists := d.lanes[lane.ID]; exists {
		return d.remember(newError(KindAmbiguousID, "lane ID occurs more than once", map[string]string{"lane_id": string(lane.ID)}))
	}
	d.lanes[lane.ID] = lane
	return nil
}

func (d *Draft) ReplaceLane(lane Lane) error {
	if err := d.mutable(); err != nil {
		return err
	}
	if lane.ID == "" {
		return d.remember(newError(KindInvalidLane, "lane ID is required", nil))
	}
	if _, exists := d.lanes[lane.ID]; !exists {
		return d.remember(newError(KindInvalidLane, "cannot replace an unknown lane", map[string]string{"lane_id": string(lane.ID)}))
	}
	d.lanes[lane.ID] = lane
	return nil
}

func (d *Draft) AddWork(item WorkItem) error {
	if err := d.mutable(); err != nil {
		return err
	}
	if item.ID == "" {
		return d.remember(newError(KindAmbiguousID, "WorkID is required", nil))
	}
	if _, exists := d.items[item.ID]; exists {
		return d.remember(newError(KindAmbiguousID, "WorkID occurs more than once", map[string]string{"work_id": item.ID.String()}))
	}
	d.items[item.ID] = cloneWorkItem(item)
	return nil
}

func (d *Draft) ReplaceWork(item WorkItem) error {
	if err := d.mutable(); err != nil {
		return err
	}
	if _, exists := d.items[item.ID]; !exists {
		return d.remember(newError(KindMissingNode, "cannot replace an unknown WorkID", map[string]string{"work_id": item.ID.String()}))
	}
	d.items[item.ID] = cloneWorkItem(item)
	return nil
}

func (d *Draft) mutable() error {
	if d.finalized {
		return newError(KindFinalized, "finalized WorkRevision cannot be mutated", nil)
	}
	return nil
}

func (d *Draft) remember(err error) error {
	if d.invalid == nil {
		d.invalid = err
	}
	return err
}

type FinalizeMetadata struct {
	ID          ids.WorkRevisionID
	Number      int64
	ActorID     ids.ActorID
	FinalizedAt time.Time
}

type Revision struct {
	id             ids.WorkRevisionID
	projectID      ids.ProjectID
	missionID      ids.MissionID
	number         int64
	actorID        ids.ActorID
	finalizedAt    time.Time
	lanes          []Lane
	items          []WorkItem
	canonicalGraph []byte
	graphDigest    string
	criteriaDigest string
}

func (d *Draft) Finalize(metadata FinalizeMetadata) (Revision, error) {
	if err := d.mutable(); err != nil {
		return Revision{}, err
	}
	if d.invalid != nil {
		return Revision{}, d.invalid
	}
	if _, err := ids.ParseProjectID(d.projectID.String()); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "ProjectID is invalid", Cause: err}
	}
	if _, err := ids.ParseMissionID(d.missionID.String()); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "MissionID is invalid", Cause: err}
	}
	if _, err := ids.ParseWorkRevisionID(metadata.ID.String()); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "WorkRevisionID is invalid", Cause: err}
	}
	if _, err := ids.ParseActorID(metadata.ActorID.String()); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "ActorID is invalid", Cause: err}
	}
	if metadata.Number <= 0 || metadata.FinalizedAt.IsZero() {
		return Revision{}, newError(KindInvalidRevision, "positive revision number and finalized time are required", nil)
	}
	lanes := make([]Lane, 0, len(d.lanes))
	for _, lane := range d.lanes {
		lanes = append(lanes, lane)
	}
	items := make([]WorkItem, 0, len(d.items))
	for _, item := range d.items {
		items = append(items, cloneWorkItem(item))
	}
	if err := validateGraph(lanes, items); err != nil {
		return Revision{}, err
	}
	canonical, criteria, err := canonicalize(d.projectID, d.missionID, lanes, items)
	if err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "canonicalize work graph", Cause: err}
	}
	var canonicalValue canonicalGraph
	if err := json.Unmarshal(canonical, &canonicalValue); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "materialize canonical work graph", Cause: err}
	}
	d.finalized = true
	return Revision{
		id: metadata.ID, projectID: d.projectID, missionID: d.missionID, number: metadata.Number,
		actorID: metadata.ActorID, finalizedAt: metadata.FinalizedAt.UTC(), lanes: cloneLanes(canonicalValue.Lanes), items: cloneWorkItems(canonicalValue.WorkItems),
		canonicalGraph: append([]byte(nil), canonical...), graphDigest: digest(canonical), criteriaDigest: digest(criteria),
	}, nil
}

func (r Revision) ID() ids.WorkRevisionID   { return r.id }
func (r Revision) SchemaVersion() uint16    { return 1 }
func (r Revision) ProjectID() ids.ProjectID { return r.projectID }
func (r Revision) MissionID() ids.MissionID { return r.missionID }
func (r Revision) Number() int64            { return r.number }
func (r Revision) ActorID() ids.ActorID     { return r.actorID }
func (r Revision) FinalizedAt() time.Time   { return r.finalizedAt }
func (r Revision) GraphDigest() string      { return r.graphDigest }
func (r Revision) CriteriaDigest() string   { return r.criteriaDigest }
func (r Revision) Lanes() []Lane            { return cloneLanes(r.lanes) }
func (r Revision) WorkItems() []WorkItem    { return cloneWorkItems(r.items) }
func (r Revision) CanonicalGraph() []byte   { return append([]byte(nil), r.canonicalGraph...) }

func (r Revision) ContainsWork(workID ids.WorkID) bool {
	index := sort.Search(len(r.items), func(index int) bool { return r.items[index].ID >= workID })
	return index < len(r.items) && r.items[index].ID == workID
}

type revisionEnvelope struct {
	ID             ids.WorkRevisionID `json:"id"`
	Number         int64              `json:"number"`
	ActorID        ids.ActorID        `json:"actor_id"`
	FinalizedAt    time.Time          `json:"finalized_at"`
	GraphDigest    string             `json:"graph_digest"`
	CriteriaDigest string             `json:"criteria_digest"`
	Graph          canonicalGraph     `json:"graph"`
}

func (r Revision) Encode() ([]byte, error) {
	var graph canonicalGraph
	if err := json.Unmarshal(r.canonicalGraph, &graph); err != nil {
		return nil, err
	}
	return json.Marshal(revisionEnvelope{r.id, r.number, r.actorID, r.finalizedAt, r.graphDigest, r.criteriaDigest, graph})
}

func DecodeRevision(encoded []byte) (Revision, error) {
	var envelope revisionEnvelope
	if err := decodeStrictJSON(encoded, &envelope); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "decode persisted WorkRevision", Cause: err}
	}
	if err := requireLegacyEnvelopeShape(encoded); err != nil {
		return Revision{}, &Error{Kind: KindInvalidRevision, Message: "decode persisted WorkRevision", Cause: err}
	}
	draft := NewDraft(envelope.Graph.ProjectID, envelope.Graph.MissionID)
	for _, lane := range envelope.Graph.Lanes {
		if err := draft.AddLane(lane); err != nil {
			return Revision{}, err
		}
	}
	for _, item := range envelope.Graph.WorkItems {
		if err := draft.AddWork(item); err != nil {
			return Revision{}, err
		}
	}
	revision, err := draft.Finalize(FinalizeMetadata{ID: envelope.ID, Number: envelope.Number, ActorID: envelope.ActorID, FinalizedAt: envelope.FinalizedAt})
	if err != nil {
		return Revision{}, err
	}
	if revision.graphDigest != envelope.GraphDigest || revision.criteriaDigest != envelope.CriteriaDigest {
		return Revision{}, newError(KindInvalidRevision, "persisted WorkRevision digest does not match canonical content", map[string]string{"work_revision_id": envelope.ID.String()})
	}
	return revision, nil
}

func requireLegacyEnvelopeShape(encoded []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	required := []string{"id", "number", "actor_id", "finalized_at", "graph_digest", "criteria_digest", "graph"}
	if len(fields) != len(required) {
		return fmt.Errorf("legacy WorkRevision has an incomplete top-level shape")
	}
	for _, field := range required {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("legacy WorkRevision is missing %s", field)
		}
	}
	return nil
}

type canonicalGraph struct {
	ProjectID ids.ProjectID `json:"project_id"`
	MissionID ids.MissionID `json:"mission_id"`
	Lanes     []Lane        `json:"lanes"`
	WorkItems []WorkItem    `json:"work_items"`
}

type canonicalCriteria struct {
	WorkID   ids.WorkID  `json:"work_id"`
	Criteria []Criterion `json:"criteria"`
}

func canonicalize(projectID ids.ProjectID, missionID ids.MissionID, lanes []Lane, items []WorkItem) ([]byte, []byte, error) {
	lanes = cloneLanes(lanes)
	items = cloneWorkItems(items)
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].ID < lanes[j].ID })
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	criteria := make([]canonicalCriteria, len(items))
	for index := range items {
		sort.Slice(items[index].Criteria, func(i, j int) bool { return items[index].Criteria[i].ID < items[index].Criteria[j].ID })
		sort.Slice(items[index].Dependencies, func(i, j int) bool { return items[index].Dependencies[i] < items[index].Dependencies[j] })
		sort.Slice(items[index].SourceRevisions, func(i, j int) bool {
			if items[index].SourceRevisions[i].Provider == items[index].SourceRevisions[j].Provider {
				return items[index].SourceRevisions[i].Revision < items[index].SourceRevisions[j].Revision
			}
			return items[index].SourceRevisions[i].Provider < items[index].SourceRevisions[j].Provider
		})
		criteria[index] = canonicalCriteria{WorkID: items[index].ID, Criteria: append([]Criterion(nil), items[index].Criteria...)}
	}
	graphJSON, err := json.Marshal(canonicalGraph{ProjectID: projectID, MissionID: missionID, Lanes: lanes, WorkItems: items})
	if err != nil {
		return nil, nil, err
	}
	criteriaJSON, err := json.Marshal(criteria)
	return graphJSON, criteriaJSON, err
}

func validateGraph(lanes []Lane, items []WorkItem) error {
	if len(items) == 0 {
		return newError(KindMissingNode, "work graph must contain at least one WorkID", nil)
	}
	laneByID := make(map[LaneID]Lane, len(lanes))
	for _, lane := range lanes {
		if _, exists := laneByID[lane.ID]; exists {
			return newError(KindAmbiguousID, "lane ID occurs more than once", map[string]string{"lane_id": string(lane.ID)})
		}
		laneByID[lane.ID] = lane
	}
	for _, lane := range lanes {
		if lane.ParentID != "" {
			if _, exists := laneByID[lane.ParentID]; !exists {
				return newError(KindInvalidLane, "lane lineage references a missing parent", map[string]string{"lane_id": string(lane.ID), "parent_lane_id": string(lane.ParentID)})
			}
		}
	}
	lineageEdges := make(map[string][]string, len(lanes))
	lineageNodes := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		lineageNodes = append(lineageNodes, string(lane.ID))
		if lane.ParentID != "" {
			lineageEdges[string(lane.ID)] = append(lineageEdges[string(lane.ID)], string(lane.ParentID))
		}
	}
	if cyclic(lineageNodes, lineageEdges) {
		return newError(KindInvalidLane, "lane lineage contains a cycle", nil)
	}
	itemByID := make(map[ids.WorkID]WorkItem, len(items))
	for _, item := range items {
		if _, exists := itemByID[item.ID]; exists {
			return newError(KindAmbiguousID, "WorkID occurs more than once", map[string]string{"work_id": item.ID.String()})
		}
		if _, err := ids.ParseWorkID(item.ID.String()); err != nil {
			return &Error{Kind: KindAmbiguousID, Message: "WorkID is invalid", Fields: map[string]string{"work_id": item.ID.String()}, Cause: err}
		}
		if _, exists := laneByID[item.LaneID]; !exists {
			return newError(KindInvalidLane, "work item maps to a missing lane", map[string]string{"work_id": item.ID.String(), "lane_id": string(item.LaneID)})
		}
		if err := validateItemSets(item); err != nil {
			return err
		}
		itemByID[item.ID] = item
	}
	workEdges := make(map[string][]string, len(items))
	workNodes := make([]string, 0, len(items))
	laneEdges := make(map[string][]string, len(lanes))
	for _, item := range items {
		workNodes = append(workNodes, item.ID.String())
		for _, dependency := range item.Dependencies {
			dependencyItem, exists := itemByID[dependency]
			if !exists {
				return newError(KindMissingNode, "dependency references a missing WorkID", map[string]string{"work_id": item.ID.String(), "dependency_work_id": dependency.String()})
			}
			workEdges[item.ID.String()] = append(workEdges[item.ID.String()], dependency.String())
			if item.LaneID != dependencyItem.LaneID {
				laneEdges[string(item.LaneID)] = append(laneEdges[string(item.LaneID)], string(dependencyItem.LaneID))
			}
		}
	}
	if cyclic(workNodes, workEdges) {
		return newError(KindCycle, "WorkID dependency graph contains a cycle", nil)
	}
	if cyclic(lineageNodes, laneEdges) {
		return newError(KindLaneCycle, "dependency graph becomes cyclic after lane projection", nil)
	}
	return nil
}

func validateItemSets(item WorkItem) error {
	criteria := make(map[string]struct{}, len(item.Criteria))
	for _, criterion := range item.Criteria {
		if criterion.ID == "" || criterion.Text == "" {
			return newError(KindAmbiguousID, "criterion ID and text are required", map[string]string{"work_id": item.ID.String()})
		}
		if _, exists := criteria[criterion.ID]; exists {
			return newError(KindAmbiguousID, "criterion ID occurs more than once in a WorkID", map[string]string{"work_id": item.ID.String(), "criterion_id": criterion.ID})
		}
		criteria[criterion.ID] = struct{}{}
	}
	dependencies := make(map[ids.WorkID]struct{}, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		if _, exists := dependencies[dependency]; exists {
			return newError(KindAmbiguousID, "dependency edge occurs more than once", map[string]string{"work_id": item.ID.String(), "dependency_work_id": dependency.String()})
		}
		dependencies[dependency] = struct{}{}
	}
	sources := make(map[string]struct{}, len(item.SourceRevisions))
	for _, source := range item.SourceRevisions {
		if source.Provider == "" || source.Revision == "" {
			return newError(KindInvalidRevision, "source provider and revision are required", map[string]string{"work_id": item.ID.String()})
		}
		key := source.Provider + "\x00" + source.Revision
		if _, exists := sources[key]; exists {
			return newError(KindAmbiguousID, "source revision occurs more than once", map[string]string{"work_id": item.ID.String(), "provider": source.Provider, "source_revision": source.Revision})
		}
		sources[key] = struct{}{}
	}
	return nil
}

func cyclic(nodes []string, edges map[string][]string) bool {
	const visiting, visited = 1, 2
	state := make(map[string]uint8, len(nodes))
	var visit func(string) bool
	visit = func(node string) bool {
		if state[node] == visiting {
			return true
		}
		if state[node] == visited {
			return false
		}
		state[node] = visiting
		for _, next := range edges[node] {
			if visit(next) {
				return true
			}
		}
		state[node] = visited
		return false
	}
	for _, node := range nodes {
		if visit(node) {
			return true
		}
	}
	return false
}

func cloneLanes(input []Lane) []Lane { return append([]Lane(nil), input...) }
func cloneWorkItems(input []WorkItem) []WorkItem {
	result := make([]WorkItem, len(input))
	for index := range input {
		result[index] = cloneWorkItem(input[index])
	}
	return result
}
func cloneWorkItem(item WorkItem) WorkItem {
	item.Criteria = append([]Criterion{}, item.Criteria...)
	item.Dependencies = append([]ids.WorkID{}, item.Dependencies...)
	item.SourceRevisions = append([]SourceRevision{}, item.SourceRevisions...)
	return item
}
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
