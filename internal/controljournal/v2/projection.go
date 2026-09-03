package v2

import (
	"bytes"
	"sort"
	"sync"
)

const (
	CanonicalCodecVersionV2         = uint32(2)
	MaxAcknowledgementsBetweenSeals = 10_000
)

type projectionEntryV2 struct {
	coordinate  AggregateCoordinate
	canonical   []byte
	digest      Digest
	version     LogicalVersion
	lastEventID EventID
	tombstone   bool
}

type ProjectionEntryV2 struct {
	coordinate  AggregateCoordinate
	canonical   []byte
	digest      Digest
	version     LogicalVersion
	lastEventID EventID
	tombstone   bool
}

func (e ProjectionEntryV2) Coordinate() AggregateCoordinate { return e.coordinate }
func (e ProjectionEntryV2) CanonicalState() []byte          { return append([]byte(nil), e.canonical...) }
func (e ProjectionEntryV2) StateDigest() Digest             { return e.digest }
func (e ProjectionEntryV2) LogicalVersion() LogicalVersion  { return e.version }
func (e ProjectionEntryV2) LastEventID() EventID            { return e.lastEventID }
func (e ProjectionEntryV2) Tombstone() bool                 { return e.tombstone }

type OperationSummaryV2 struct {
	total        uint64
	statusCounts map[OperationStatus]uint64
	leaves       []Digest
	digest       Digest
}

func (s OperationSummaryV2) Total() uint64                             { return s.total }
func (s OperationSummaryV2) StatusCount(status OperationStatus) uint64 { return s.statusCounts[status] }
func (s OperationSummaryV2) LeafDigests() []Digest                     { return append([]Digest(nil), s.leaves...) }
func (s OperationSummaryV2) Digest() Digest                            { return s.digest }

type acknowledgedLedgerEntryV2 struct {
	revision        uint64
	publicationID   PublicationID
	planDigest      Digest
	recordSetDigest Digest
}

type ProjectionTrackerV2 struct {
	mu                    sync.Mutex
	reducers              ReducerSetV2
	eventRegistryDigest   Digest
	reducerRegistryDigest Digest
	codecVersion          uint32
	states                map[string]projectionEntryV2
	operations            map[OperationID]OperationStateV2
	pending               map[PublicationID]Digest
	ledger                map[uint64]acknowledgedLedgerEntryV2
	priorPrefix           Digest
	sealedWatermark       uint64
	highestObserved       uint64
}

func NewProjectionTrackerV2(reducers ReducerSetV2, codecVersion uint32) (*ProjectionTrackerV2, error) {
	eventDescriptor, err := reducers.registry.Descriptor()
	if err != nil {
		return nil, err
	}
	reducerDescriptor, err := reducers.Descriptor()
	if err != nil {
		return nil, err
	}
	if codecVersion != CanonicalCodecVersionV2 {
		return nil, invalid(ErrorInvalidVersion, "canonical_codec_version")
	}
	genesis := digest("spec-kitty.projection-prefix.genesis.v2", nil)
	return &ProjectionTrackerV2{reducers: reducers, eventRegistryDigest: eventDescriptor.Digest(),
		reducerRegistryDigest: reducerDescriptor.Digest(), codecVersion: codecVersion,
		states: make(map[string]projectionEntryV2), operations: make(map[OperationID]OperationStateV2),
		pending: make(map[PublicationID]Digest), ledger: make(map[uint64]acknowledgedLedgerEntryV2), priorPrefix: genesis}, nil
}

func (t *ProjectionTrackerV2) registerPublication(plan SealedCommitPlanV2) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := validateSealedPlanIdentity(plan); err != nil {
		return err
	}
	if _, exists := t.pending[plan.publicationID]; exists {
		return invalid(ErrorAmbiguous, "projection_publication_registered")
	}
	t.pending[plan.publicationID] = plan.digest
	return nil
}

func (t *ProjectionTrackerV2) applyAcknowledged(ack AcknowledgedPublicationV2) error {
	if err := validateAcknowledgement(ack); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	plan := ack.plan
	registered, exists := t.pending[plan.publicationID]
	if !exists || registered != plan.digest {
		return invalid(ErrorAmbiguous, "projection_publication_not_registered")
	}
	if ack.observation.revision <= t.sealedWatermark {
		return invalid(ErrorInvalidVersion, "projection_revision_regression")
	}
	if _, exists := t.ledger[ack.observation.revision]; exists {
		return invalid(ErrorInvalidTransition, "projection_revision_conflict")
	}
	if len(t.ledger) >= MaxAcknowledgementsBetweenSeals {
		return invalid(ErrorBudgetExceeded, "acknowledgement_ledger")
	}

	changedStates := make(map[string]projectionEntryV2, len(plan.events))
	changedOperations := make(map[OperationID]OperationStateV2, 1)
	headByCoordinate := make(map[string]HeadTransitionV2, len(plan.heads))
	for _, head := range plan.heads {
		headByCoordinate[head.Coordinate.canonical()] = head
	}
	var durableOperationHead []byte
	for _, record := range plan.records {
		if record.role == RecordOperationHead {
			durableOperationHead = append([]byte(nil), record.canonical...)
			break
		}
	}
	for _, event := range plan.events {
		key := event.input.Coordinate.canonical()
		head, ok := headByCoordinate[key]
		if !ok {
			return invalid(ErrorInvalidTransition, "projection_head_missing")
		}
		prior := []byte(`{}`)
		if entry, exists := t.states[key]; exists {
			prior = entry.canonical
		}
		var output []byte
		var stateDigest Digest
		output, stateDigest, err := t.reducers.Apply(prior, event)
		if err != nil {
			return err
		}
		if event.input.Family == "operation" {
			state, reconstructErr := ReconstructOperationStateV2(event.payload, event.input.PublicationID)
			if reconstructErr != nil || state.operationID != plan.operationState.operationID || state.logicalVersion != plan.operationState.logicalVersion {
				return invalid(ErrorInvalidTransition, "operation_projection")
			}
			reconstructed, encodeErr := EncodeOperationStateV2(state)
			planned, plannedErr := EncodeOperationStateV2(plan.operationState)
			if encodeErr != nil || plannedErr != nil || !bytes.Equal(reconstructed, planned) ||
				!bytes.Equal(output, durableOperationHead) || !bytes.Equal(output, planned) {
				return invalid(ErrorInvalidTransition, "operation_projection")
			}
			changedOperations[state.operationID] = state
		} else if stateDigest != head.CanonicalStateDigest {
			return invalid(ErrorInvalidDigest, "projection_state_digest")
		}
		if current, exists := t.states[key]; !exists || !bytes.Equal(current.canonical, output) || current.version != event.input.AggregateLogicalVersion ||
			current.lastEventID != head.EventID || current.tombstone != head.Tombstone {
			changedStates[key] = projectionEntryV2{coordinate: event.input.Coordinate, canonical: append([]byte(nil), output...), digest: stateDigest,
				version: event.input.AggregateLogicalVersion, lastEventID: head.EventID, tombstone: head.Tombstone}
		}
	}

	for key, value := range changedStates {
		t.states[key] = value
	}
	for key, value := range changedOperations {
		t.operations[key] = value
	}
	t.ledger[ack.observation.revision] = acknowledgedLedgerEntryV2{ack.observation.revision, plan.publicationID, plan.digest, plan.recordSetDigest}
	delete(t.pending, plan.publicationID)
	if ack.observation.revision > t.highestObserved {
		t.highestObserved = ack.observation.revision
	}
	return nil
}

func (t *ProjectionTrackerV2) HighestObservedRevision() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.highestObserved
}
func (t *ProjectionTrackerV2) SealedWatermark() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sealedWatermark
}
func (t *ProjectionTrackerV2) LedgerSize() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.ledger)
}

// CurrentProjection returns one defensive live projection entry. Operation
// entries contain the exact durable OperationState head; checkpoint seals
// compact them into OperationSummaryV2 instead of retaining result bytes.
func (t *ProjectionTrackerV2) CurrentProjection(coordinate AggregateCoordinate) (ProjectionEntryV2, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.states[coordinate.canonical()]
	if !ok {
		return ProjectionEntryV2{}, false
	}
	return ProjectionEntryV2{coordinate: entry.coordinate, canonical: append([]byte(nil), entry.canonical...), digest: entry.digest,
		version: entry.version, lastEventID: entry.lastEventID, tombstone: entry.tombstone}, true
}

type SealedProjectionPrefixV2 struct {
	watermark             uint64
	highestObserved       uint64
	eventRegistryDigest   Digest
	reducerRegistryDigest Digest
	codecVersion          uint32
	rollingPrefix         Digest
	projectionDigest      Digest
	entries               []ProjectionEntryV2
	operationSummary      OperationSummaryV2
	seal                  Digest
}

func (s SealedProjectionPrefixV2) Watermark() uint64               { return s.watermark }
func (s SealedProjectionPrefixV2) HighestObservedRevision() uint64 { return s.highestObserved }
func (s SealedProjectionPrefixV2) EventRegistryDigest() Digest     { return s.eventRegistryDigest }
func (s SealedProjectionPrefixV2) ReducerRegistryDigest() Digest   { return s.reducerRegistryDigest }
func (s SealedProjectionPrefixV2) CodecVersion() uint32            { return s.codecVersion }
func (s SealedProjectionPrefixV2) RollingPrefixDigest() Digest     { return s.rollingPrefix }
func (s SealedProjectionPrefixV2) ProjectionDigest() Digest        { return s.projectionDigest }
func (s SealedProjectionPrefixV2) OperationSummary() OperationSummaryV2 {
	return cloneOperationSummary(s.operationSummary)
}
func (s SealedProjectionPrefixV2) Entries() []ProjectionEntryV2 {
	result := make([]ProjectionEntryV2, len(s.entries))
	for i, entry := range s.entries {
		entry.canonical = append([]byte(nil), entry.canonical...)
		result[i] = entry
	}
	return result
}

func (s SealedProjectionPrefixV2) Validate() error {
	if s.watermark == 0 || s.highestObserved < s.watermark || s.eventRegistryDigest.IsZero() || s.reducerRegistryDigest.IsZero() ||
		s.codecVersion != CanonicalCodecVersionV2 || s.rollingPrefix.IsZero() || s.projectionDigest.IsZero() || s.operationSummary.digest.IsZero() || s.seal.IsZero() {
		return invalid(ErrorInvalidDigest, "sealed_projection_prefix")
	}
	projectionDigest, err := digestProjectionEntries(s.entries)
	if err != nil || projectionDigest != s.projectionDigest {
		return invalid(ErrorInvalidDigest, "sealed_projection_prefix")
	}
	summaryDigest, err := digestOperationSummary(s.operationSummary)
	if err != nil || summaryDigest != s.operationSummary.digest {
		return invalid(ErrorInvalidDigest, "sealed_projection_prefix")
	}
	seal, err := projectionSeal(s)
	if err != nil || seal != s.seal {
		return invalid(ErrorInvalidDigest, "sealed_projection_prefix")
	}
	return nil
}

func (t *ProjectionTrackerV2) sealPrefix() (SealedProjectionPrefixV2, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) != 0 {
		return SealedProjectionPrefixV2{}, invalid(ErrorAmbiguous, "unresolved_publication")
	}
	if len(t.ledger) == 0 {
		return SealedProjectionPrefixV2{}, invalid(ErrorInvalidTransition, "empty_acknowledgement_ledger")
	}
	entries := make([]acknowledgedLedgerEntryV2, 0, len(t.ledger))
	for _, entry := range t.ledger {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].revision < entries[j].revision })
	prefix := t.priorPrefix
	for _, entry := range entries {
		canonical, err := marshalCanonical(struct {
			Prior         string `json:"prior_prefix_sha256"`
			Revision      uint64 `json:"provider_revision"`
			PublicationID string `json:"publication_id"`
			Plan          string `json:"plan_sha256"`
			RecordSet     string `json:"record_set_sha256"`
		}{prefix.String(), entry.revision, string(entry.publicationID), entry.planDigest.String(), entry.recordSetDigest.String()}, MaxCanonicalPayloadBytes)
		if err != nil {
			return SealedProjectionPrefixV2{}, err
		}
		prefix = digest("spec-kitty.projection-prefix.v2", canonical)
	}
	projectionEntries := snapshotProjectionEntries(t.states)
	projectionDigest, err := digestProjectionEntries(projectionEntries)
	if err != nil {
		return SealedProjectionPrefixV2{}, err
	}
	summary, err := summarizeOperations(t.operations)
	if err != nil {
		return SealedProjectionPrefixV2{}, err
	}
	sealed := SealedProjectionPrefixV2{watermark: entries[len(entries)-1].revision, highestObserved: t.highestObserved,
		eventRegistryDigest: t.eventRegistryDigest, reducerRegistryDigest: t.reducerRegistryDigest,
		codecVersion: t.codecVersion, rollingPrefix: prefix, projectionDigest: projectionDigest,
		entries: projectionEntries, operationSummary: summary}
	sealed.seal, err = projectionSeal(sealed)
	if err != nil {
		return SealedProjectionPrefixV2{}, err
	}
	t.priorPrefix = prefix
	t.sealedWatermark = sealed.watermark
	t.ledger = make(map[uint64]acknowledgedLedgerEntryV2)
	return sealed, nil
}

func snapshotProjectionEntries(states map[string]projectionEntryV2) []ProjectionEntryV2 {
	keys := make([]string, 0, len(states))
	for key, value := range states {
		if value.coordinate.Kind == AggregateKind("operation") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ProjectionEntryV2, len(keys))
	for index, key := range keys {
		value := states[key]
		result[index] = ProjectionEntryV2{coordinate: value.coordinate, canonical: append([]byte(nil), value.canonical...), digest: value.digest,
			version: value.version, lastEventID: value.lastEventID, tombstone: value.tombstone}
	}
	return result
}

func digestProjectionEntries(entries []ProjectionEntryV2) (Digest, error) {
	type wire struct {
		Coordinate  AggregateCoordinate `json:"coordinate"`
		Version     uint64              `json:"version"`
		LastEventID string              `json:"last_event_id"`
		Tombstone   bool                `json:"tombstone"`
		State       []byte              `json:"state"`
		StateSHA256 string              `json:"state_sha256"`
	}
	values := make([]wire, len(entries))
	for index, entry := range entries {
		if _, err := NewAggregateCoordinate(entry.coordinate.Kind, entry.coordinate.ID, entry.coordinate.Scope); err != nil ||
			entry.coordinate.Kind == AggregateKind("operation") || entry.version == 0 || validateIdentity("last_event_id", string(entry.lastEventID)) != nil ||
			entry.digest.IsZero() || digest("spec-kitty.reducer-state.v2", entry.canonical) != entry.digest {
			return Digest{}, invalid(ErrorInvalidDigest, "projection_entry")
		}
		values[index] = wire{entry.coordinate, uint64(entry.version), string(entry.lastEventID), entry.tombstone,
			append([]byte(nil), entry.canonical...), entry.digest.String()}
	}
	canonical, err := marshalCanonical(values, MaxCommitPlanBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.projection.v2", canonical), nil
}

func summarizeOperations(operations map[OperationID]OperationStateV2) (OperationSummaryV2, error) {
	ids := make([]OperationID, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	leaves := make([]Digest, len(ids))
	counts := make(map[OperationStatus]uint64)
	for index, id := range ids {
		state := operations[id]
		eventCanonical, _ := marshalCanonical(state.eventIDs, MaxCanonicalPayloadBytes)
		effectCanonical, _ := marshalCanonical(state.effectIDs, MaxCanonicalPayloadBytes)
		canonical, err := marshalCanonical(struct {
			OperationID   string          `json:"operation_id"`
			Version       uint64          `json:"version"`
			PublicationID string          `json:"publication_id"`
			Status        OperationStatus `json:"status"`
			Result        string          `json:"result_sha256"`
			Events        string          `json:"event_membership_sha256"`
			Effects       string          `json:"effect_membership_sha256"`
		}{string(id), uint64(state.logicalVersion), string(state.lastPublicationID), state.status, state.resultDigest.String(), digest("spec-kitty.operation-events.v2", eventCanonical).String(), digest("spec-kitty.operation-effects.v2", effectCanonical).String()}, MaxCanonicalPayloadBytes)
		if err != nil {
			return OperationSummaryV2{}, err
		}
		leaves[index] = digest("spec-kitty.operation-summary-leaf.v2", canonical)
		counts[state.status]++
	}
	summary := OperationSummaryV2{total: uint64(len(leaves)), statusCounts: counts, leaves: leaves}
	summaryDigest, err := digestOperationSummary(summary)
	if err != nil {
		return OperationSummaryV2{}, err
	}
	summary.digest = summaryDigest
	return summary, nil
}

func digestOperationSummary(summary OperationSummaryV2) (Digest, error) {
	type statusCount struct {
		Status OperationStatus `json:"status"`
		Count  uint64          `json:"count"`
	}
	statuses := make([]statusCount, 0, len(summary.statusCounts))
	counted := uint64(0)
	for status, count := range summary.statusCounts {
		if !validOperationStatus(status) || count == 0 {
			return Digest{}, invalid(ErrorInvalidCanonicalData, "operation_summary")
		}
		statuses = append(statuses, statusCount{status, count})
		counted += count
	}
	if counted != summary.total || uint64(len(summary.leaves)) != summary.total {
		return Digest{}, invalid(ErrorInvalidCanonicalData, "operation_summary")
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Status < statuses[j].Status })
	leaves := make([]string, len(summary.leaves))
	for index, leaf := range summary.leaves {
		if leaf.IsZero() {
			return Digest{}, invalid(ErrorInvalidDigest, "operation_summary_leaf")
		}
		leaves[index] = leaf.String()
	}
	canonical, err := marshalCanonical(struct {
		Total    uint64        `json:"total"`
		Statuses []statusCount `json:"status_counts"`
		Leaves   []string      `json:"leaf_sha256"`
	}{summary.total, statuses, leaves}, MaxCheckpointValueBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.operation-summary.v2", canonical), nil
}

func cloneOperationSummary(summary OperationSummaryV2) OperationSummaryV2 {
	result := summary
	result.leaves = append([]Digest(nil), summary.leaves...)
	result.statusCounts = make(map[OperationStatus]uint64, len(summary.statusCounts))
	for status, count := range summary.statusCounts {
		result.statusCounts[status] = count
	}
	return result
}

func projectionSeal(value SealedProjectionPrefixV2) (Digest, error) {
	canonical, err := marshalCanonical(struct {
		Watermark        uint64 `json:"watermark"`
		Highest          uint64 `json:"highest_observed_revision"`
		EventRegistry    string `json:"event_registry_sha256"`
		ReducerRegistry  string `json:"reducer_registry_sha256"`
		Codec            uint32 `json:"codec_version"`
		Prefix           string `json:"rolling_prefix_sha256"`
		Projection       string `json:"projection_sha256"`
		OperationSummary string `json:"operation_summary_sha256"`
	}{value.watermark, value.highestObserved, value.eventRegistryDigest.String(), value.reducerRegistryDigest.String(), value.codecVersion, value.rollingPrefix.String(), value.projectionDigest.String(), value.operationSummary.digest.String()}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.sealed-projection-prefix.v2", canonical), nil
}
