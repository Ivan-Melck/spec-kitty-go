package productionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/projection"
)

const (
	ProgressSchemaVersion = 1
	MaxProgressItems      = 10_000
	MaxProgressPages      = 128
	MaxProgressDiagnosis  = 128
	// Query snapshots join many valid durable states; they are not event payloads.
	MaxProgressSnapshotBytes = 16 * 1024 * 1024
)

type ProgressErrorKind string

const (
	ProgressInvalidQuery   ProgressErrorKind = "INVALID_PROGRESS_QUERY"
	ProgressNotFound       ProgressErrorKind = "MISSION_NOT_FOUND"
	ProgressIncomplete     ProgressErrorKind = "PROGRESS_INCOMPLETE"
	ProgressResyncRequired ProgressErrorKind = "RESNAPSHOT_REQUIRED"
)

type ProgressError struct {
	Kind  ProgressErrorKind `json:"kind"`
	Field string            `json:"field"`
}

func (e *ProgressError) Error() string { return string(e.Kind) + ": " + e.Field }
func IsProgressKind(err error, kind ProgressErrorKind) bool {
	var target *ProgressError
	return errors.As(err, &target) && target.Kind == kind
}

// ActionAvailability is an executable authority assessment. Multiple entries
// may be authorized concurrently; their order does not imply a phase cursor.
type ActionAvailability struct {
	Action               string   `json:"action"`
	Scope                string   `json:"scope,omitempty"`
	Status               string   `json:"status"`
	BlockingPredicates   []string `json:"blocking_predicates,omitempty"`
	ResponsibleProducers []string `json:"responsible_producers,omitempty"`
}

type StateCount struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type RunningAuthority struct {
	WorkID    string `json:"work_id,omitempty"`
	RunID     string `json:"run_id"`
	LeaseID   string `json:"lease_id"`
	ActorID   string `json:"actor_id"`
	ProcessID string `json:"process_id,omitempty"`
}

// ProgressItem preserves the distinction between recorded authority and an
// observation. CanonicalState is copied on every query.
type ProgressItem struct {
	Family            string `json:"family"`
	AggregateID       string `json:"aggregate_id"`
	LogicalVersion    uint64 `json:"logical_version"`
	LastPublicationID string `json:"last_publication_id"`
	AuthorityState    string `json:"authority_state,omitempty"`
	ObservedState     string `json:"observed_state,omitempty"`
	CanonicalState    []byte `json:"canonical_state"`
}

// MissionProgress answers where a mission is now from one complete immutable
// projection revision. It is an in-process DTO suitable for later CLI, API,
// dashboard, and optional MCP projection without importing transport concepts.
type MissionProgress struct {
	SchemaVersion         uint32                   `json:"schema_version"`
	DatabaseID            journalv2.DatabaseID     `json:"database_id"`
	ProjectionRevision    uint64                   `json:"projection_revision"`
	ProjectionSHA256      string                   `json:"projection_sha256"`
	SnapshotSHA256        string                   `json:"snapshot_sha256"`
	RuntimeEpochID        journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	ProjectID             journalv2.ProjectID      `json:"project_id,omitempty"`
	MissionID             journalv2.MissionID      `json:"mission_id"`
	Lifecycle             string                   `json:"lifecycle,omitempty"`
	WorkRevisionID        journalv2.WorkRevisionID `json:"work_revision_id,omitempty"`
	Generation            uint64                   `json:"generation,omitempty"`
	Complete              bool                     `json:"complete"`
	TotalWork             int                      `json:"total_work"`
	WorkCounts            []StateCount             `json:"work_counts"`
	Running               []RunningAuthority       `json:"running"`
	Actions               []ActionAvailability     `json:"actions"`
	Blockers              []ProgressItem           `json:"blockers"`
	Decisions             []ProgressItem           `json:"decisions"`
	PendingReconciliation []ProgressItem           `json:"pending_reconciliation"`
	Effects               []ProgressItem           `json:"effects"`
	Reviews               []ProgressItem           `json:"reviews"`
	Acceptance            []ProgressItem           `json:"acceptance"`
	Integration           []ProgressItem           `json:"integration"`
	Operations            []ProgressItem           `json:"operations"`
	Items                 []ProgressItem           `json:"items"`
}

type ProgressFamily string

const (
	ProgressWork        ProgressFamily = "work"
	ProgressEffects     ProgressFamily = "effects"
	ProgressReviews     ProgressFamily = "reviews"
	ProgressAcceptance  ProgressFamily = "acceptance"
	ProgressIntegration ProgressFamily = "integration"
	ProgressOperations  ProgressFamily = "operations"
)

// List returns one closed progress family from the same immutable mission
// snapshot used by Mission. Family is a product vocabulary, not a provider
// key or tracker status string.
func (s *ProgressService) List(projectID journalv2.ProjectID, missionID journalv2.MissionID, family ProgressFamily) ([]ProgressItem, error) {
	progress, err := s.Mission(projectID, missionID)
	if err != nil {
		return nil, err
	}
	var items []ProgressItem
	switch family {
	case ProgressWork:
		for _, item := range progress.Items {
			if item.Family == "execution_slot" {
				items = append(items, item)
			}
		}
	case ProgressEffects:
		items = progress.Effects
	case ProgressReviews:
		items = progress.Reviews
	case ProgressAcceptance:
		items = progress.Acceptance
	case ProgressIntegration:
		items = progress.Integration
	case ProgressOperations:
		items = progress.Operations
	default:
		return nil, &ProgressError{Kind: ProgressInvalidQuery, Field: "family"}
	}
	return cloneProgressItems(items), nil
}

func (s *ProgressService) Get(projectID journalv2.ProjectID, missionID journalv2.MissionID, family ProgressFamily, aggregateID string) (ProgressItem, error) {
	if aggregateID == "" || len(aggregateID) > journalv2.MaxIdentityBytes {
		return ProgressItem{}, &ProgressError{Kind: ProgressInvalidQuery, Field: "aggregate_id"}
	}
	items, err := s.List(projectID, missionID, family)
	if err != nil {
		return ProgressItem{}, err
	}
	for _, item := range items {
		if item.AggregateID == aggregateID {
			return cloneProgressItem(item), nil
		}
	}
	return ProgressItem{}, &ProgressError{Kind: ProgressNotFound, Field: "aggregate_id"}
}

// ActionCensusSource supplies a capability-derived action census. It is query
// authority only: it cannot mint operations, leases, epochs, or effects.
type ActionCensusSource interface {
	Census(projectID journalv2.ProjectID, missionID journalv2.MissionID, revision uint64) (mission.ActionCensus, error)
}

// authorityActionCensusSource is the sole production census assembler. It
// observes the mission and every capability at one exact GapDB capture, seals
// the complete membership, and only then invokes the pure evaluator. Callers
// cannot inject a precomputed census or an editable projection as authority.
type authorityActionCensusSource struct {
	readiness  readinessAuthorityObserver
	observer   *mission.CapabilityAuthorityObserverV2
	definition mission.CapabilityDefinition
}

type readinessAuthorityObserver interface {
	ObserveReadinessAuthority(context.Context, journalv2.ProjectID, journalv2.MissionID) (mission.ReadinessAuthorityObservationV2, error)
}

func newAuthorityActionCensusSource(readiness readinessAuthorityObserver, source mission.CapabilityReadinessSourceV2) (*authorityActionCensusSource, error) {
	if readiness == nil || source == nil {
		return nil, &ProgressError{Kind: ProgressIncomplete, Field: "readiness_authority"}
	}
	observer, err := mission.NewCapabilityAuthorityObserverV2(source)
	if err != nil {
		return nil, err
	}
	return &authorityActionCensusSource{readiness: readiness, observer: observer, definition: softwaredev.CapabilityDefinition()}, nil
}

func (s *authorityActionCensusSource) Census(projectID journalv2.ProjectID, missionID journalv2.MissionID, revision uint64) (mission.ActionCensus, error) {
	if s == nil || s.readiness == nil || s.observer == nil || revision == 0 {
		return mission.ActionCensus{}, &ProgressError{Kind: ProgressIncomplete, Field: "readiness_authority"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	authority, err := s.readiness.ObserveReadinessAuthority(ctx, projectID, missionID)
	if err != nil || authority.Capture().CurrentRevision() != revision || authority.Capture().DurableThroughRevision() < revision {
		return mission.ActionCensus{}, &ProgressError{Kind: ProgressResyncRequired, Field: "readiness_capture"}
	}
	observations := make([]mission.CapabilityAuthorityObservationV2, 0, len(s.definition.Templates()))
	for _, template := range s.definition.Templates() {
		observed, observeErr := s.observer.Observe(ctx, authority.Capture(), projectID, missionID, template.Action)
		if observeErr != nil {
			return mission.ActionCensus{}, &ProgressError{Kind: ProgressIncomplete, Field: "capability_observation"}
		}
		observations = append(observations, observed)
	}
	set, err := mission.SealCapabilityObservationSetV2(authority.Capture(), projectID, missionID, observations)
	if err != nil {
		return mission.ActionCensus{}, err
	}
	snapshot, err := mission.SealReadinessSnapshot(authority, s.definition, set)
	if err != nil {
		return mission.ActionCensus{}, err
	}
	return mission.EvaluateReadiness(snapshot)
}

type ProgressService struct {
	query   projection.QueryPortV2
	actions ActionCensusSource
	cacheMu sync.RWMutex
	cache   map[progressCacheKey]MissionProgress
}

type progressCacheKey struct {
	project  journalv2.ProjectID
	mission  journalv2.MissionID
	revision uint64
	epoch    journalv2.RuntimeEpochID
}

func NewProgressService(query projection.QueryPortV2, actions ActionCensusSource) (*ProgressService, error) {
	if query == nil {
		return nil, &ProgressError{Kind: ProgressInvalidQuery, Field: "query_authority"}
	}
	return &ProgressService{query: query, actions: actions, cache: make(map[progressCacheKey]MissionProgress)}, nil
}

func (s *ProgressService) Mission(projectID journalv2.ProjectID, missionID journalv2.MissionID) (MissionProgress, error) {
	if s == nil || missionID == "" || len(missionID) > journalv2.MaxIdentityBytes || len(projectID) > journalv2.MaxIdentityBytes {
		return MissionProgress{}, &ProgressError{Kind: ProgressInvalidQuery, Field: "mission"}
	}
	base, err := s.query.ProgressBase(string(missionID))
	if err != nil {
		if projection.IsKindV2(err, projection.ErrorNotFoundV2) {
			return MissionProgress{}, &ProgressError{Kind: ProgressNotFound, Field: "mission_id"}
		}
		return MissionProgress{}, &ProgressError{Kind: ProgressIncomplete, Field: "projection_base"}
	}
	key := progressCacheKey{project: projectID, mission: missionID, revision: base.Revision, epoch: base.RuntimeEpochID}
	s.cacheMu.RLock()
	cached, found := s.cache[key]
	s.cacheMu.RUnlock()
	if found {
		return cloneMissionProgress(cached), nil
	}
	items, projectionDigest, err := s.listAll(projectID, missionID, base.Revision)
	if err != nil {
		return MissionProgress{}, err
	}
	progress := MissionProgress{SchemaVersion: ProgressSchemaVersion, DatabaseID: base.DatabaseID, ProjectionRevision: base.Revision, ProjectionSHA256: projectionDigest, RuntimeEpochID: base.RuntimeEpochID, ProjectID: projectID, MissionID: missionID}
	counts := make(map[string]int)
	budget := progressSnapshotBudget{}
	for _, item := range items {
		if err := classifyProgressItem(&progress, item, counts, &budget); err != nil {
			return MissionProgress{}, err
		}
	}
	for state, count := range counts {
		progress.WorkCounts = append(progress.WorkCounts, StateCount{State: state, Count: count})
	}
	sort.Slice(progress.WorkCounts, func(i, j int) bool { return progress.WorkCounts[i].State < progress.WorkCounts[j].State })
	sort.Slice(progress.Running, func(i, j int) bool {
		return progress.Running[i].WorkID+"\x00"+progress.Running[i].RunID < progress.Running[j].WorkID+"\x00"+progress.Running[j].RunID
	})
	if s.actions != nil {
		census, censusErr := s.actions.Census(projectID, missionID, base.Revision)
		if censusErr != nil || census.MissionID() != string(missionID) || census.Revision() != base.Revision || !hexDigest(census.SnapshotDigest()) || len(census.CanonicalBytes()) == 0 || len(census.Assessments()) > MaxProgressItems {
			return MissionProgress{}, &ProgressError{Kind: ProgressIncomplete, Field: "action_census"}
		}
		progress.Actions = actionAvailability(census)
		progress.Complete = true
		sort.Slice(progress.Actions, func(i, j int) bool {
			return progress.Actions[i].Action+"\x00"+progress.Actions[i].Scope < progress.Actions[j].Action+"\x00"+progress.Actions[j].Scope
		})
	}
	digest, err := progressSnapshotDigest(progress)
	if err != nil {
		return MissionProgress{}, err
	}
	progress.SnapshotSHA256 = digest
	s.cacheMu.Lock()
	// A revision uniquely identifies an immutable projection. Retain only the
	// newest snapshot for this mission so a long-running host cannot accumulate
	// one cache entry per publication.
	for cachedKey := range s.cache {
		if cachedKey.project == projectID && cachedKey.mission == missionID {
			delete(s.cache, cachedKey)
		}
	}
	s.cache[key] = cloneMissionProgress(progress)
	s.cacheMu.Unlock()
	return cloneMissionProgress(progress), nil
}

func actionAvailability(census mission.ActionCensus) []ActionAvailability {
	assessments := census.Assessments()
	result := make([]ActionAvailability, 0, len(assessments))
	for _, assessment := range assessments {
		value := ActionAvailability{Action: string(assessment.Action.ID), Scope: string(assessment.Action.Scope.Kind) + ":" + assessment.Action.Scope.Identity, Status: string(assessment.Status)}
		for _, predicate := range assessment.Predicates {
			if predicate.Status == mission.PredicateSatisfied || predicate.Status == mission.PredicateInapplicable {
				continue
			}
			value.BlockingPredicates = append(value.BlockingPredicates, string(predicate.Kind))
			value.ResponsibleProducers = append(value.ResponsibleProducers, string(predicate.ResponsibleCapability))
		}
		sort.Strings(value.BlockingPredicates)
		sort.Strings(value.ResponsibleProducers)
		result = append(result, value)
	}
	return result
}

func (s *ProgressService) listAll(projectID journalv2.ProjectID, missionID journalv2.MissionID, revision uint64) ([]ProgressItem, string, error) {
	items := make([]ProgressItem, 0, 64)
	budget := progressSnapshotBudget{}
	projectionDigest := ""
	cursor := ""
	for page := 0; page < MaxProgressPages; page++ {
		result, err := s.query.List(projection.ListQueryV2{ProjectID: projectID, MissionID: missionID, Limit: projection.MaxQueryPageV2, Cursor: cursor})
		if err != nil {
			if projection.IsKindV2(err, projection.ErrorStaleCursorV2) {
				return nil, "", &ProgressError{Kind: ProgressResyncRequired, Field: "projection_cursor"}
			}
			return nil, "", &ProgressError{Kind: ProgressIncomplete, Field: "projection_list"}
		}
		if result.Revision != revision {
			return nil, "", &ProgressError{Kind: ProgressResyncRequired, Field: "projection_revision"}
		}
		for _, view := range result.Items {
			if len(items) >= MaxProgressItems {
				return nil, "", &ProgressError{Kind: ProgressIncomplete, Field: "item_budget"}
			}
			if projectionDigest == "" {
				projectionDigest = view.ProjectionDigest
			} else if view.ProjectionDigest != projectionDigest {
				return nil, "", &ProgressError{Kind: ProgressResyncRequired, Field: "projection_digest"}
			}
			// Bound retained state before cloning it. The final builder separately
			// charges every classified occurrence of each item.
			if len(view.CanonicalState) > MaxProgressSnapshotBytes {
				return nil, "", progressSnapshotBudgetError()
			}
			item := progressItem(view)
			if err := budget.append(item, &items); err != nil {
				return nil, "", err
			}
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
		if page == MaxProgressPages-1 {
			return nil, "", &ProgressError{Kind: ProgressIncomplete, Field: "page_budget"}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Family+"\x00"+items[i].AggregateID < items[j].Family+"\x00"+items[j].AggregateID
	})
	if !hexDigest(projectionDigest) {
		return nil, "", &ProgressError{Kind: ProgressIncomplete, Field: "projection_digest"}
	}
	return items, projectionDigest, nil
}

// progressItem borrows the query-owned state until budget admission; the
// builder clones it only after the serialized contribution has been charged.
func progressItem(view projection.AggregateViewV2) ProgressItem {
	state := selectiveState(view.CanonicalState)
	return ProgressItem{Family: string(view.Coordinate.Kind), AggregateID: string(view.Coordinate.ID), LogicalVersion: uint64(view.LogicalVersion), LastPublicationID: string(view.LastPublicationID), AuthorityState: state.authority, ObservedState: state.observed, CanonicalState: view.CanonicalState}
}

type selectedState struct {
	authority, observed, lifecycle, workRevision, kind string
	generation                                         uint64
	runID, leaseID, actorID, processID, workID         string
}

func selectiveState(raw []byte) selectedState {
	var wire map[string]json.RawMessage
	if json.Unmarshal(raw, &wire) != nil {
		return selectedState{}
	}
	text := func(names ...string) string {
		for _, name := range names {
			var value string
			if json.Unmarshal(wire[name], &value) == nil && len(value) <= MaxProgressDiagnosis {
				return value
			}
		}
		return ""
	}
	var generation uint64
	_ = json.Unmarshal(wire["generation"], &generation)
	workRevision := text("work_revision_id", "current_work_revision_id")
	// Workgraph reducer state wraps the complete immutable revision under
	// current_revision. Decode only the two bounded coordinates progress needs;
	// do not teach the transport-neutral projection the rest of that schema.
	var currentRevision struct {
		WorkRevisionID string `json:"work_revision_id"`
		Generation     uint64 `json:"generation"`
	}
	if json.Unmarshal(wire["current_revision"], &currentRevision) == nil {
		if generation == 0 {
			generation = currentRevision.Generation
		}
		if workRevision == "" && len(currentRevision.WorkRevisionID) <= MaxProgressDiagnosis {
			workRevision = currentRevision.WorkRevisionID
		}
	}
	return selectedState{authority: text("stage", "status", "lifecycle", "state"), observed: text("observed_state", "provider_state"), lifecycle: text("lifecycle"), workRevision: workRevision, kind: text("kind", "fact_kind"), generation: generation, runID: text("run_id"), leaseID: text("lease_id"), actorID: text("actor_id"), processID: text("process_id"), workID: text("work_id")}
}

func classifyProgressItem(progress *MissionProgress, item ProgressItem, counts map[string]int, budget *progressSnapshotBudget) error {
	selected := selectiveState(item.CanonicalState)
	// One classification owns both membership and its exact byte charge.
	targets := []*[]ProgressItem{&progress.Items}
	switch item.Family {
	case "mission":
		if selected.lifecycle != "" {
			progress.Lifecycle = selected.lifecycle
		} else if item.AuthorityState != "" {
			progress.Lifecycle = item.AuthorityState
		}
		if selected.workRevision != "" {
			progress.WorkRevisionID = journalv2.WorkRevisionID(selected.workRevision)
		}
		if selected.kind == "blocker" || strings.Contains(selected.kind, "blocker") {
			targets = append(targets, &progress.Blockers)
		}
		if selected.kind == "decision" || strings.Contains(selected.kind, "decision") {
			targets = append(targets, &progress.Decisions)
		}
	case "workgraph":
		if selected.generation > progress.Generation {
			progress.Generation = selected.generation
		}
		if progress.WorkRevisionID == "" && selected.workRevision != "" {
			progress.WorkRevisionID = journalv2.WorkRevisionID(selected.workRevision)
		}
	case "execution_slot":
		progress.TotalWork++
		state := item.AuthorityState
		if state == "" {
			state = "unknown"
		}
		counts[state]++
		if selected.runID != "" && selected.leaseID != "" && (strings.Contains(state, "active") || strings.Contains(state, "running") || strings.Contains(state, "admitted")) {
			progress.Running = append(progress.Running, RunningAuthority{selected.workID, selected.runID, selected.leaseID, selected.actorID, selected.processID})
		}
	case "external_effect":
		targets = append(targets, &progress.Effects)
		if strings.Contains(item.AuthorityState, "ambiguous") || strings.Contains(item.AuthorityState, "reconciliation") {
			targets = append(targets, &progress.PendingReconciliation)
		}
	case "review_slot":
		targets = append(targets, &progress.Reviews)
	case "acceptance":
		targets = append(targets, &progress.Acceptance)
	case "integration_target", "integration_attempt":
		targets = append(targets, &progress.Integration)
	case "operation":
		targets = append(targets, &progress.Operations)
		if strings.Contains(item.AuthorityState, "ambiguous") || strings.Contains(item.AuthorityState, "reconciliation") {
			targets = append(targets, &progress.PendingReconciliation)
		}
	}
	return budget.append(item, targets...)
}

type ProgressWatch struct {
	C      <-chan MissionProgress
	Errors <-chan error
	cancel func()
	once   sync.Once
}

func (w *ProgressWatch) Cancel() {
	if w != nil {
		w.once.Do(func() {
			if w.cancel != nil {
				w.cancel()
			}
		})
	}
}

func (s *ProgressService) Watch(projectID journalv2.ProjectID, missionID journalv2.MissionID, databaseID journalv2.DatabaseID, afterRevision uint64) (*ProgressWatch, error) {
	current, err := s.Mission(projectID, missionID)
	if err != nil {
		return nil, err
	}
	resnapshot := databaseID != current.DatabaseID || afterRevision != current.ProjectionRevision
	watch, err := s.query.Watch(current.DatabaseID, current.ProjectionRevision)
	if err != nil {
		return nil, &ProgressError{Kind: ProgressResyncRequired, Field: "watch_start"}
	}
	updates := make(chan MissionProgress, 1)
	errorsOut := make(chan error, 1)
	done := make(chan struct{})
	if resnapshot {
		updates <- current
	}
	go func() {
		defer close(updates)
		defer close(errorsOut)
		for {
			select {
			case <-done:
				return
			case notification, open := <-watch.C:
				if !open {
					return
				}
				if notification.ResyncRequired {
					errorsOut <- &ProgressError{Kind: ProgressResyncRequired, Field: "watch_overflow"}
					return
				}
				value, queryErr := s.Mission(projectID, missionID)
				if queryErr != nil || value.ProjectionRevision != notification.Revision {
					errorsOut <- &ProgressError{Kind: ProgressResyncRequired, Field: "watch_revision"}
					return
				}
				select {
				case updates <- value:
				default:
					errorsOut <- &ProgressError{Kind: ProgressResyncRequired, Field: "subscriber_slow"}
					return
				}
			}
		}
	}()
	return &ProgressWatch{C: updates, Errors: errorsOut, cancel: func() {
		close(done)
		watch.Cancel()
	}}, nil
}

func cloneProgressItem(item ProgressItem) ProgressItem {
	item.CanonicalState = bytes.Clone(item.CanonicalState)
	return item
}
func cloneProgressItems(items []ProgressItem) []ProgressItem {
	result := make([]ProgressItem, len(items))
	for i := range items {
		result[i] = cloneProgressItem(items[i])
	}
	return result
}
func cloneMissionProgress(value MissionProgress) MissionProgress {
	raw, _ := json.Marshal(value)
	var clone MissionProgress
	_ = json.Unmarshal(raw, &clone)
	return clone
}
