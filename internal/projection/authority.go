package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	playback "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
	restore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
)

const (
	MaxQueryPageV2               = 100
	MaxQueryResponseV2           = 1024 * 1024
	MaxProjectionWatchesV2       = 256
	MaxProjectionSnapshotBytesV2 = playback.MaxPlaybackBytes
)

type ErrorKindV2 string

const (
	ErrorInvalidQueryV2       ErrorKindV2 = "INVALID_QUERY"
	ErrorNotFoundV2           ErrorKindV2 = "NOT_FOUND"
	ErrorStaleCursorV2        ErrorKindV2 = "STALE_CURSOR"
	ErrorResyncRequiredV2     ErrorKindV2 = "RESYNC_REQUIRED"
	ErrorIncompleteRevisionV2 ErrorKindV2 = "INCOMPLETE_REVISION"
	ErrorProjectionCorruptV2  ErrorKindV2 = "PROJECTION_CORRUPT"
)

type ErrorV2 struct {
	Kind  ErrorKindV2
	Field string
}

func (e *ErrorV2) Error() string { return string(e.Kind) + ": " + e.Field }
func IsKindV2(err error, kind ErrorKindV2) bool {
	var target *ErrorV2
	return errors.As(err, &target) && target.Kind == kind
}

// AggregateViewV2 is an immutable authority observation. CanonicalState is a
// defensive copy and contains no raw provider key or mutation capability.
type AggregateViewV2 struct {
	DatabaseID         journalv2.DatabaseID          `json:"database_id"`
	ProjectionRevision uint64                        `json:"projection_revision"`
	RuntimeEpochID     journalv2.RuntimeEpochID      `json:"runtime_epoch_id"`
	ProjectionDigest   string                        `json:"projection_digest_sha256"`
	Coordinate         journalv2.AggregateCoordinate `json:"coordinate"`
	LogicalVersion     journalv2.LogicalVersion      `json:"logical_version"`
	LastEventID        journalv2.EventID             `json:"last_event_id"`
	LastPublicationID  journalv2.PublicationID       `json:"last_publication_id"`
	StateDigest        string                        `json:"state_digest_sha256"`
	Tombstone          bool                          `json:"tombstone"`
	CanonicalState     []byte                        `json:"canonical_state"`
}

type SnapshotV2 struct {
	databaseID       journalv2.DatabaseID
	revision         uint64
	durableThrough   uint64
	epoch            journalv2.RuntimeEpochID
	states           []playback.StateV2
	views            []AggregateViewV2
	byCoordinate     map[string]int
	projectionDigest journalv2.Digest
	canonical        []byte
}

func (s *SnapshotV2) DatabaseID() journalv2.DatabaseID {
	if s == nil {
		return ""
	}
	return s.databaseID
}
func (s *SnapshotV2) Revision() uint64 {
	if s == nil {
		return 0
	}
	return s.revision
}
func (s *SnapshotV2) DurableThroughRevision() uint64 {
	if s == nil {
		return 0
	}
	return s.durableThrough
}
func (s *SnapshotV2) RuntimeEpochID() journalv2.RuntimeEpochID {
	if s == nil {
		return ""
	}
	return s.epoch
}
func (s *SnapshotV2) ProjectionDigest() journalv2.Digest {
	if s == nil {
		return journalv2.Digest{}
	}
	return s.projectionDigest
}
func (s *SnapshotV2) CanonicalBytes() []byte {
	if s == nil {
		return nil
	}
	return bytes.Clone(s.canonical)
}

type AuthorityV2 struct {
	databaseID    journalv2.DatabaseID
	registry      journalv2.EventRegistryV2
	reducers      journalv2.ReducerSetV2
	eventDigest   journalv2.Digest
	reducerDigest journalv2.Digest
	current       atomic.Pointer[SnapshotV2]
	mu            sync.RWMutex
	revision      uint64
	durable       uint64
	epoch         journalv2.RuntimeEpochID
	liveToken     journalv2.Digest
	states        map[string]playback.StateV2
	views         map[string]AggregateViewV2
	byFamily      map[string]map[string]struct{}
	byProject     map[string]map[string]struct{}
	byMission     map[string]map[string]struct{}
	byWork        map[string]map[string]struct{}
	pending       map[uint64]journalv2.AcknowledgedPublicationV2
	notified      uint64
	advanced      chan struct{}
	watches       map[uint64]*watchStateV2
	nextWatch     uint64
}

func NewAuthorityV2(databaseID journalv2.DatabaseID, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2) (*AuthorityV2, error) {
	if _, err := journalv2.NewStoreRevisionV2(databaseID, 1, 1); err != nil {
		return nil, &ErrorV2{ErrorInvalidQueryV2, "database_id"}
	}
	eventDescriptor, err := registry.Descriptor()
	if err != nil {
		return nil, err
	}
	reducerDescriptor, err := reducers.Descriptor()
	if err != nil {
		return nil, err
	}
	a := &AuthorityV2{databaseID: databaseID, registry: registry, reducers: reducers, eventDigest: eventDescriptor.Digest(), reducerDigest: reducerDescriptor.Digest(), liveToken: journalv2.ProjectionPrefixGenesisV2(), states: make(map[string]playback.StateV2), views: make(map[string]AggregateViewV2), byFamily: make(map[string]map[string]struct{}), byProject: make(map[string]map[string]struct{}), byMission: make(map[string]map[string]struct{}), byWork: make(map[string]map[string]struct{}), pending: make(map[uint64]journalv2.AcknowledgedPublicationV2), advanced: make(chan struct{}), watches: make(map[uint64]*watchStateV2)}
	genesis, err := buildSnapshotV2(databaseID, 0, 0, "", nil, journalv2.Digest{})
	if err != nil {
		return nil, err
	}
	a.current.Store(genesis)
	return a, nil
}

func (a *AuthorityV2) Snapshot() *SnapshotV2 {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cached := a.current.Load(); cached != nil && cached.revision == a.revision {
		return cached
	}
	states := liveStatesV2(a.states)
	digest, err := a.canonicalProjectionDigestV2(states)
	if err != nil {
		return nil
	}
	next, err := buildSnapshotV2(a.databaseID, a.revision, a.durable, a.epoch, states, digest)
	if err != nil {
		return nil
	}
	a.current.Store(next)
	return next
}

// AttributionV2 is the constant-size identity of the current contiguous live
// projection. CursorToken is the rolling acknowledged-publication prefix. The
// canonical flat projection digest remains a checkpoint/recovery artifact.
type AttributionV2 struct {
	DatabaseID     journalv2.DatabaseID
	Revision       uint64
	DurableThrough uint64
	RuntimeEpochID journalv2.RuntimeEpochID
	CursorToken    journalv2.Digest
}

func (a *AuthorityV2) Attribution() AttributionV2 {
	if a == nil {
		return AttributionV2{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return AttributionV2{a.databaseID, a.revision, a.durable, a.epoch, a.liveToken}
}

// PublishAcknowledged publishes exactly one complete provider revision. All
// reducers run against a candidate; readers keep seeing the predecessor until
// every event, head, membership, ordering, and digest check has passed.
func (a *AuthorityV2) PublishAcknowledged(ack journalv2.AcknowledgedPublicationV2) error {
	if a == nil {
		return &ErrorV2{ErrorProjectionCorruptV2, "authority"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ack.ProviderRevision() != a.revision+1 {
		return &ErrorV2{ErrorIncompleteRevisionV2, "acknowledgement"}
	}
	return a.applyAcknowledgedLockedV2(ack, true)
}

// AdvanceAcknowledgedPublicationV2 is the coordinator sink. Provider commits
// may complete out of order, so acknowledgements are retained by revision and
// only the contiguous prefix becomes query-visible.
func (a *AuthorityV2) AdvanceAcknowledgedPublicationV2(ctx context.Context, ack journalv2.AcknowledgedPublicationV2) error {
	if a == nil {
		return &ErrorV2{ErrorProjectionCorruptV2, "authority"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if ack.ProviderRevision() <= a.revision || ack.ProviderRevision() > a.revision+journalv2.MaxAcknowledgementsBetweenSeals {
		a.mu.Unlock()
		return &ErrorV2{ErrorIncompleteRevisionV2, "acknowledgement"}
	}
	if _, exists := a.pending[ack.ProviderRevision()]; exists {
		a.mu.Unlock()
		return &ErrorV2{ErrorIncompleteRevisionV2, "duplicate_acknowledgement"}
	}
	a.pending[ack.ProviderRevision()] = ack
	for {
		next, ok := a.pending[a.revision+1]
		if !ok {
			break
		}
		if err := a.applyAcknowledgedLockedV2(next, false); err != nil {
			a.mu.Unlock()
			return err
		}
		delete(a.pending, next.ProviderRevision())
		close(a.advanced)
		a.advanced = make(chan struct{})
	}
	for a.revision < ack.ProviderRevision() {
		advanced := a.advanced
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-advanced:
		}
		a.mu.Lock()
	}
	a.mu.Unlock()
	return nil
}

func (a *AuthorityV2) applyAcknowledgedLockedV2(ack journalv2.AcknowledgedPublicationV2, notify bool) error {
	plan := ack.Plan()
	if ack.ProviderRevision() != a.revision+1 || ack.DurableThroughRevision() < ack.ProviderRevision() ||
		ack.PublicationID() != plan.PublicationID() || ack.PlanDigest() != plan.Digest() || ack.RecordSetDigest() != plan.RecordSetDigest() || ack.MutationCount() != plan.MutationCount() {
		return &ErrorV2{ErrorIncompleteRevisionV2, "acknowledgement"}
	}
	heads := plan.Heads()
	if len(heads) != len(plan.Events()) {
		return &ErrorV2{ErrorIncompleteRevisionV2, "event_head_membership"}
	}
	headByEvent := make(map[journalv2.EventID]journalv2.HeadTransitionV2, len(heads))
	for _, head := range heads {
		if _, duplicate := headByEvent[head.EventID]; duplicate {
			return &ErrorV2{ErrorIncompleteRevisionV2, "duplicate_head"}
		}
		headByEvent[head.EventID] = head
	}
	events := plan.Events()
	materialized := make([]playback.MaterializedEventV2, 0, len(events))
	baseByCoordinate := make(map[string]playback.StateV2, len(events))
	var epoch journalv2.RuntimeEpochID
	for _, event := range events {
		head, ok := headByEvent[event.EventID()]
		if !ok || head.Coordinate != event.Coordinate() || head.NextLogicalVersion != event.LogicalVersion() || head.CommittedRuntimeEpochID != event.CommittedRuntimeEpochID() {
			return &ErrorV2{ErrorIncompleteRevisionV2, "head_event_mismatch"}
		}
		if epoch == "" {
			epoch = event.CommittedRuntimeEpochID()
		} else if epoch != event.CommittedRuntimeEpochID() {
			return &ErrorV2{ErrorIncompleteRevisionV2, "mixed_runtime_epoch"}
		}
		item, err := playback.NewMaterializedEventV2(ack.ProviderRevision(), event, head.Tombstone)
		if err != nil {
			return err
		}
		materialized = append(materialized, item)
		key, _ := journalv2.CanonicalAggregateCoordinate(event.Coordinate())
		if prior, ok := a.states[string(key)]; ok {
			baseByCoordinate[string(key)] = prior
		}
	}
	if a.epoch != "" && epoch != a.epoch {
		return &ErrorV2{ErrorProjectionCorruptV2, "runtime_epoch"}
	}
	base := make([]playback.StateV2, 0, len(baseByCoordinate))
	for _, state := range baseByCoordinate {
		base = append(base, state)
	}
	result, err := playback.RunV2(playback.InputV2{Base: base, Events: materialized, EventRegistry: a.registry, Reducers: a.reducers, ExpectedEventRegistryDigest: a.eventDigest, ExpectedReducerRegistryDigest: a.reducerDigest})
	if err != nil || result.Validate() != nil || result.ThroughRecordRevision() != ack.ProviderRevision() {
		return &ErrorV2{ErrorProjectionCorruptV2, "playback"}
	}
	resultByCoordinate := make(map[string]playback.StateV2, len(result.States()))
	for _, state := range result.States() {
		key, _ := journalv2.CanonicalAggregateCoordinate(state.Coordinate())
		resultByCoordinate[string(key)] = state
	}
	for _, head := range heads {
		key, _ := journalv2.CanonicalAggregateCoordinate(head.Coordinate)
		state, ok := resultByCoordinate[string(key)]
		digestMatches := state.StateDigest() == head.CanonicalStateDigest
		if head.Coordinate.Kind == "operation" {
			planned, plannedErr := journalv2.EncodeOperationStateV2(plan.OperationState())
			digestMatches = plannedErr == nil && bytes.Equal(state.CanonicalState(), planned)
		}
		if !ok || state.LogicalVersion() != head.NextLogicalVersion || state.LastEventID() != head.EventID || !digestMatches || state.Tombstone() != head.Tombstone {
			return &ErrorV2{ErrorProjectionCorruptV2, "head_projection_digest"}
		}
	}
	nextToken, err := journalv2.AdvanceProjectionPrefixV2(a.liveToken, ack.ProviderRevision(), ack.PublicationID(), ack.PlanDigest(), ack.RecordSetDigest())
	if err != nil {
		return err
	}
	for key, state := range resultByCoordinate {
		a.states[key] = state
		view := viewFromStateV2(a.databaseID, ack.ProviderRevision(), epoch, journalv2.Digest{}, state)
		a.views[key] = view
		a.indexViewLockedV2(key, view)
	}
	a.revision = ack.ProviderRevision()
	a.durable = ack.DurableThroughRevision()
	a.epoch = epoch
	a.liveToken = nextToken
	if notify {
		a.notifyAttributionLocked(AttributionV2{a.databaseID, a.revision, a.durable, a.epoch, a.liveToken}, false)
		a.notified = a.revision
	}
	return nil
}

// NotifyCurrent publishes one coalesced watch boundary after a complete Host
// command, including any checkpoint publication triggered by that command.
func (a *AuthorityV2) NotifyCurrent() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revision == 0 || a.revision == a.notified {
		return
	}
	a.notifyAttributionLocked(AttributionV2{a.databaseID, a.revision, a.durable, a.epoch, a.liveToken}, false)
	a.notified = a.revision
}

// Restore replaces empty memory from WP04's sealed restore result. The durable
// coordinator owns I/O; this method cannot accept a caller-assembled playback
// result or an independently supplied provider revision.
func (a *AuthorityV2) Restore(epoch journalv2.RuntimeEpochID, recovered restore.ResultV2) error {
	return a.restore(epoch, recovered, false)
}

// RestoreCurrentEpoch atomically replaces the current-epoch snapshot after an
// acknowledged publication or startup reconciliation. The AuthorityV2 object
// itself remains stable so existing query watches remain attached. Unlike
// Restore, it requires the replay attempt to name the same already-admitted
// epoch; it cannot be used to admit or rotate an epoch.
func (a *AuthorityV2) RestoreCurrentEpoch(epoch journalv2.RuntimeEpochID, recovered restore.ResultV2) error {
	return a.restore(epoch, recovered, true)
}

func (a *AuthorityV2) restore(epoch journalv2.RuntimeEpochID, recovered restore.ResultV2, currentEpoch bool) error {
	admission, admissionErr := recovered.FreshEpochAdmissionRequestV2()
	revision := recovered.StoreRevision()
	result := recovered.Playback()
	epochRelationValid := epoch != admission.PriorRuntimeEpochID()
	if currentEpoch {
		epochRelationValid = epoch == admission.PriorRuntimeEpochID()
	}
	if a == nil || admissionErr != nil || revision.DatabaseID() != a.databaseID || admission.DatabaseID() != a.databaseID ||
		admission.ThroughRecordRevision() != revision.CurrentRevision() || admission.CanonicalProjectionDigest() != result.CanonicalProjectionDigest() ||
		admission.EventRegistryDigest() != a.eventDigest || admission.ReducerRegistryDigest() != a.reducerDigest || result.Validate() != nil ||
		epoch == "" || !epochRelationValid {
		return &ErrorV2{ErrorProjectionCorruptV2, "restore"}
	}
	next, err := buildSnapshotV2(a.databaseID, revision.CurrentRevision(), revision.DurableThroughRevision(), epoch, result.States(), result.CanonicalProjectionDigest())
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.current.Load()
	if a.revision != 0 {
		if !currentEpoch || a.epoch != epoch || next.revision < a.revision {
			return &ErrorV2{ErrorProjectionCorruptV2, "restore_over_live_projection"}
		}
		if next.revision == a.revision {
			liveStates := liveStatesV2(a.states)
			liveDigest, digestErr := a.canonicalProjectionDigestV2(liveStates)
			live, buildErr := buildSnapshotV2(a.databaseID, a.revision, a.durable, a.epoch, liveStates, liveDigest)
			if digestErr != nil || buildErr != nil || next.projectionDigest != live.projectionDigest {
				return &ErrorV2{ErrorProjectionCorruptV2, "restore_same_revision_mismatch"}
			}
			a.current.Store(live)
			return nil
		}
	}
	a.revision = next.revision
	a.durable = next.durableThrough
	a.epoch = next.epoch
	a.liveToken = recovered.RollingPrefixDigest()
	a.states = make(map[string]playback.StateV2, len(next.states))
	a.views = make(map[string]AggregateViewV2, len(next.views))
	a.byFamily = make(map[string]map[string]struct{})
	a.byProject = make(map[string]map[string]struct{})
	a.byMission = make(map[string]map[string]struct{})
	a.byWork = make(map[string]map[string]struct{})
	for _, state := range next.states {
		key, _ := journalv2.CanonicalAggregateCoordinate(state.Coordinate())
		a.states[string(key)] = state
	}
	for _, view := range next.views {
		key, _ := journalv2.CanonicalAggregateCoordinate(view.Coordinate)
		a.views[string(key)] = view
		a.indexViewLockedV2(string(key), view)
	}
	a.pending = make(map[uint64]journalv2.AcknowledgedPublicationV2)
	close(a.advanced)
	a.advanced = make(chan struct{})
	a.notified = next.revision
	a.current.Store(next)
	a.notifyAttributionLocked(AttributionV2{next.databaseID, next.revision, next.durableThrough, next.epoch, a.liveToken}, old != nil && old.epoch != "" && old.epoch != next.epoch)
	return nil
}

func (a *AuthorityV2) canonicalProjectionDigestV2(states []playback.StateV2) (journalv2.Digest, error) {
	if len(states) == 0 {
		return journalv2.Digest{}, nil
	}
	result, err := playback.RunV2(playback.InputV2{Base: states, EventRegistry: a.registry, Reducers: a.reducers, ExpectedEventRegistryDigest: a.eventDigest, ExpectedReducerRegistryDigest: a.reducerDigest})
	if err != nil || result.Validate() != nil {
		return journalv2.Digest{}, &ErrorV2{ErrorProjectionCorruptV2, "snapshot_playback"}
	}
	return result.CanonicalProjectionDigest(), nil
}

func (a *AuthorityV2) indexViewLockedV2(key string, view AggregateViewV2) {
	addProjectionIndexV2(a.byFamily, string(view.Coordinate.Kind), key)
	addProjectionIndexV2(a.byProject, string(view.Coordinate.Scope.ProjectID), key)
	addProjectionIndexV2(a.byMission, string(view.Coordinate.Scope.MissionID), key)
	addProjectionIndexV2(a.byWork, string(view.Coordinate.Scope.WorkID), key)
}

func addProjectionIndexV2(index map[string]map[string]struct{}, value, key string) {
	if value == "" {
		return
	}
	if index[value] == nil {
		index[value] = make(map[string]struct{})
	}
	index[value][key] = struct{}{}
}

func liveStatesV2(states map[string]playback.StateV2) []playback.StateV2 {
	result := make([]playback.StateV2, 0, len(states))
	for _, state := range states {
		result = append(result, state)
	}
	return result
}

func viewFromStateV2(databaseID journalv2.DatabaseID, revision uint64, epoch journalv2.RuntimeEpochID, digest journalv2.Digest, state playback.StateV2) AggregateViewV2 {
	return AggregateViewV2{DatabaseID: databaseID, ProjectionRevision: revision, RuntimeEpochID: epoch, ProjectionDigest: digest.String(), Coordinate: state.Coordinate(), LogicalVersion: state.LogicalVersion(), LastEventID: state.LastEventID(), LastPublicationID: state.LastPublicationID(), StateDigest: state.StateDigest().String(), Tombstone: state.Tombstone(), CanonicalState: state.CanonicalState()}
}

func buildSnapshotV2(databaseID journalv2.DatabaseID, revision, durable uint64, epoch journalv2.RuntimeEpochID, states []playback.StateV2, digest journalv2.Digest) (*SnapshotV2, error) {
	views := make([]AggregateViewV2, len(states))
	index := make(map[string]int, len(states))
	for i, state := range states {
		keyBytes, err := journalv2.CanonicalAggregateCoordinate(state.Coordinate())
		if err != nil {
			return nil, err
		}
		key := string(keyBytes)
		if _, duplicate := index[key]; duplicate {
			return nil, &ErrorV2{ErrorProjectionCorruptV2, "duplicate_coordinate"}
		}
		views[i] = AggregateViewV2{
			DatabaseID: databaseID, ProjectionRevision: revision, RuntimeEpochID: epoch, ProjectionDigest: digest.String(),
			Coordinate: state.Coordinate(), LogicalVersion: state.LogicalVersion(), LastEventID: state.LastEventID(),
			LastPublicationID: state.LastPublicationID(), StateDigest: state.StateDigest().String(), Tombstone: state.Tombstone(), CanonicalState: state.CanonicalState(),
		}
		index[key] = i
	}
	sort.Slice(views, func(i, j int) bool {
		left, _ := journalv2.CanonicalAggregateCoordinate(views[i].Coordinate)
		right, _ := journalv2.CanonicalAggregateCoordinate(views[j].Coordinate)
		return bytes.Compare(left, right) < 0
	})
	index = make(map[string]int, len(views))
	for i := range views {
		key, _ := journalv2.CanonicalAggregateCoordinate(views[i].Coordinate)
		index[string(key)] = i
	}
	wire := struct {
		DatabaseID       journalv2.DatabaseID     `json:"database_id"`
		Revision         uint64                   `json:"revision"`
		DurableThrough   uint64                   `json:"durable_through_revision"`
		RuntimeEpochID   journalv2.RuntimeEpochID `json:"runtime_epoch_id,omitempty"`
		ProjectionDigest string                   `json:"projection_digest_sha256"`
		States           []AggregateViewV2        `json:"states"`
	}{databaseID, revision, durable, epoch, digest.String(), cloneViewsV2(views)}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	// The snapshot is a sorted, struct-shaped in-memory view of the complete
	// projection. It is not one provider record and must not inherit the
	// per-record payload ceiling. json.Marshal is deterministic here: the
	// envelope and all nested values are structs/slices and views were sorted
	// above. Bound the complete snapshot by the playback budget it represents.
	if len(raw) > MaxProjectionSnapshotBytesV2 {
		return nil, &ErrorV2{ErrorProjectionCorruptV2, "snapshot_budget"}
	}
	canonical := append([]byte(nil), raw...)
	return &SnapshotV2{databaseID: databaseID, revision: revision, durableThrough: durable, epoch: epoch, states: append([]playback.StateV2(nil), states...), views: views, byCoordinate: index, projectionDigest: digest, canonical: canonical}, nil
}
