package v2

import (
	"bytes"
	"sort"
	"sync"
)

const (
	CanonicalCodecVersionV2         = uint32(2)
	MaxAcknowledgementsBetweenSeals = 10_000
	MaxOperationSummaryBytesV2      = 2_112_000
	maxOperationBoundariesV2        = 10_000
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

type OperationBoundaryV2 struct {
	operationIDLookupDigest Digest
	logicalVersion          LogicalVersion
	leafDigest              Digest
}

func (b OperationBoundaryV2) OperationIDLookupDigest() Digest { return b.operationIDLookupDigest }
func (b OperationBoundaryV2) LogicalVersion() LogicalVersion  { return b.logicalVersion }
func (b OperationBoundaryV2) LeafDigest() Digest              { return b.leafDigest }
func (b OperationBoundaryV2) MatchesOperationID(operationID OperationID) bool {
	return validateIdentity("operation_id", string(operationID)) == nil && b.operationIDLookupDigest == operationIDLookupDigestV2(operationID)
}

type OperationSummaryV2 struct {
	total        uint64
	statusCounts map[OperationStatus]uint64
	boundaries   []OperationBoundaryV2
	digest       Digest
}

func (s OperationSummaryV2) Total() uint64                             { return s.total }
func (s OperationSummaryV2) StatusCount(status OperationStatus) uint64 { return s.statusCounts[status] }
func (s OperationSummaryV2) LeafDigests() []Digest {
	leaves := make([]Digest, len(s.boundaries))
	for index, boundary := range s.boundaries {
		leaves[index] = boundary.leafDigest
	}
	return leaves
}
func (s OperationSummaryV2) Boundaries() []OperationBoundaryV2 {
	return append([]OperationBoundaryV2(nil), s.boundaries...)
}
func (s OperationSummaryV2) Digest() Digest { return s.digest }

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
	genesis := ProjectionPrefixGenesisV2()
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
		next, err := AdvanceProjectionPrefixV2(prefix, entry.revision, entry.publicationID, entry.planDigest, entry.recordSetDigest)
		if err != nil {
			return SealedProjectionPrefixV2{}, err
		}
		prefix = next
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
			entry.digest.IsZero() {
			return Digest{}, invalid(ErrorInvalidDigest, "projection_entry")
		}
		stateDigest, err := ReducerStateDigestV2(entry.canonical)
		if err != nil || stateDigest != entry.digest {
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
	if len(operations) > maxOperationBoundariesV2 {
		return OperationSummaryV2{}, invalid(ErrorBudgetExceeded, "operation_summary")
	}
	states := make([]OperationStateV2, 0, len(operations))
	for id, state := range operations {
		if id != state.operationID {
			return OperationSummaryV2{}, invalid(ErrorInvalidCanonicalData, "operation_summary_identity")
		}
		states = append(states, state)
	}
	return BuildOperationSummaryV2(states)
}

func operationIDLookupDigestV2(operationID OperationID) Digest {
	return digest("spec-kitty.operation-id-lookup.v2", []byte(operationID))
}

func operationBoundaryLeafV2(state OperationStateV2) (Digest, error) {
	if _, err := EncodeOperationStateV2(state); err != nil || state.lastPublicationID == "" {
		return Digest{}, invalid(ErrorJournalCorrupt, "operation_boundary_state")
	}
	eventCanonical, err := marshalCanonical(state.eventIDs, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	effectCanonical, err := marshalCanonical(state.effectIDs, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	canonical, err := marshalCanonical(struct {
		OperationID   string          `json:"operation_id"`
		Version       uint64          `json:"version"`
		PublicationID string          `json:"publication_id"`
		Status        OperationStatus `json:"status"`
		Result        string          `json:"result_sha256"`
		Events        string          `json:"event_membership_sha256"`
		Effects       string          `json:"effect_membership_sha256"`
	}{string(state.operationID), uint64(state.logicalVersion), string(state.lastPublicationID), state.status,
		state.resultDigest.String(), digest("spec-kitty.operation-events.v2", eventCanonical).String(),
		digest("spec-kitty.operation-effects.v2", effectCanonical).String()}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.operation-summary-leaf.v2", canonical), nil
}

// BuildOperationSummaryV2 constructs fixed-size attributable boundaries from
// exact current operation states. It rejects the plus-one census before
// allocating the boundary result.
func BuildOperationSummaryV2(states []OperationStateV2) (OperationSummaryV2, error) {
	if len(states) == 0 || len(states) > maxOperationBoundariesV2 {
		return OperationSummaryV2{}, invalid(ErrorBudgetExceeded, "operation_summary")
	}
	boundaries := make([]OperationBoundaryV2, len(states))
	counts := make(map[OperationStatus]uint64, 6)
	for index, state := range states {
		leaf, err := operationBoundaryLeafV2(state)
		if err != nil {
			return OperationSummaryV2{}, err
		}
		boundaries[index] = OperationBoundaryV2{operationIDLookupDigest: operationIDLookupDigestV2(state.operationID),
			logicalVersion: state.logicalVersion, leafDigest: leaf}
		counts[state.status]++
	}
	sort.Slice(boundaries, func(i, j int) bool {
		return bytes.Compare(boundaries[i].operationIDLookupDigest[:], boundaries[j].operationIDLookupDigest[:]) < 0
	})
	for index := 1; index < len(boundaries); index++ {
		if boundaries[index].operationIDLookupDigest == boundaries[index-1].operationIDLookupDigest {
			return OperationSummaryV2{}, invalid(ErrorInvalidCanonicalData, "operation_boundary_collision")
		}
	}
	summary := OperationSummaryV2{total: uint64(len(boundaries)), statusCounts: counts, boundaries: boundaries}
	digest, err := digestOperationSummary(summary)
	if err != nil {
		return OperationSummaryV2{}, err
	}
	summary.digest = digest
	if err := validateOperationSummaryStructure(summary); err != nil {
		return OperationSummaryV2{}, err
	}
	return summary, nil
}

// ValidateOperationSummaryV2 performs structural validation and then proves
// every boundary and status count against exact reconstructed operation state.
func ValidateOperationSummaryV2(summary OperationSummaryV2, states []OperationStateV2) error {
	if err := validateOperationSummaryStructure(summary); err != nil {
		return err
	}
	rebuilt, err := BuildOperationSummaryV2(states)
	if err != nil {
		return err
	}
	if rebuilt.total != summary.total || rebuilt.digest != summary.digest || len(rebuilt.boundaries) != len(summary.boundaries) || len(rebuilt.statusCounts) != len(summary.statusCounts) {
		return invalid(ErrorJournalCorrupt, "operation_summary_state")
	}
	for status, count := range rebuilt.statusCounts {
		if summary.statusCounts[status] != count {
			return invalid(ErrorJournalCorrupt, "operation_summary_state")
		}
	}
	for index := range rebuilt.boundaries {
		if rebuilt.boundaries[index] != summary.boundaries[index] {
			return invalid(ErrorJournalCorrupt, "operation_summary_state")
		}
	}
	return nil
}

type operationBoundaryWireV2 struct {
	OperationIDSHA256 string `json:"operation_id_sha256"`
	LogicalVersion    uint64 `json:"logical_version"`
	LeafSHA256        string `json:"leaf_sha256"`
}

type operationStatusCountsWireV2 struct {
	Accepted               uint64 `json:"accepted"`
	InProgress             uint64 `json:"in_progress"`
	Resolved               uint64 `json:"resolved"`
	Refused                uint64 `json:"refused"`
	Ambiguous              uint64 `json:"ambiguous"`
	ReconciliationRequired uint64 `json:"reconciliation_required"`
}

func operationSummaryPreimageV2(summary OperationSummaryV2) (any, error) {
	if summary.total == 0 || summary.total > maxOperationBoundariesV2 || uint64(len(summary.boundaries)) != summary.total {
		return nil, invalid(ErrorBudgetExceeded, "operation_summary")
	}
	counted := uint64(0)
	for status, count := range summary.statusCounts {
		if !validOperationStatus(status) || count == 0 {
			return nil, invalid(ErrorInvalidCanonicalData, "operation_summary_status")
		}
		counted += count
	}
	if counted != summary.total {
		return nil, invalid(ErrorInvalidCanonicalData, "operation_summary_status")
	}
	boundaries := make([]operationBoundaryWireV2, len(summary.boundaries))
	for index, boundary := range summary.boundaries {
		if boundary.operationIDLookupDigest.IsZero() || boundary.logicalVersion == 0 || boundary.leafDigest.IsZero() ||
			index > 0 && bytes.Compare(summary.boundaries[index-1].operationIDLookupDigest[:], boundary.operationIDLookupDigest[:]) >= 0 {
			return nil, invalid(ErrorInvalidCanonicalData, "operation_summary_boundary")
		}
		boundaries[index] = operationBoundaryWireV2{boundary.operationIDLookupDigest.String(), uint64(boundary.logicalVersion), boundary.leafDigest.String()}
	}
	counts := operationStatusCountsWireV2{summary.statusCounts[StatusAccepted], summary.statusCounts[StatusInProgress],
		summary.statusCounts[StatusResolved], summary.statusCounts[StatusRefused], summary.statusCounts[StatusAmbiguous],
		summary.statusCounts[StatusReconciliationRequired]}
	return struct {
		Total        uint64                      `json:"total"`
		StatusCounts operationStatusCountsWireV2 `json:"status_counts"`
		Boundaries   []operationBoundaryWireV2   `json:"boundaries"`
	}{summary.total, counts, boundaries}, nil
}

func digestOperationSummary(summary OperationSummaryV2) (Digest, error) {
	preimage, err := operationSummaryPreimageV2(summary)
	if err != nil {
		return Digest{}, err
	}
	canonical, err := marshalCanonical(preimage, MaxOperationSummaryBytesV2)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.operation-summary.v2", canonical), nil
}

func operationSummaryCanonicalV2(summary OperationSummaryV2) ([]byte, error) {
	preimage, err := operationSummaryPreimageV2(summary)
	if err != nil {
		return nil, err
	}
	return marshalCanonical(struct {
		Summary any    `json:"summary"`
		Digest  string `json:"summary_sha256"`
	}{preimage, summary.digest.String()}, MaxOperationSummaryBytesV2)
}

func validateOperationSummaryStructure(summary OperationSummaryV2) error {
	want, err := digestOperationSummary(summary)
	if err != nil || summary.digest.IsZero() || summary.digest != want {
		return invalid(ErrorInvalidDigest, "operation_summary")
	}
	if _, err := operationSummaryCanonicalV2(summary); err != nil {
		return err
	}
	return nil
}

func cloneOperationSummary(summary OperationSummaryV2) OperationSummaryV2 {
	result := summary
	result.boundaries = append([]OperationBoundaryV2(nil), summary.boundaries...)
	result.statusCounts = make(map[OperationStatus]uint64, len(summary.statusCounts))
	for status, count := range summary.statusCounts {
		result.statusCounts[status] = count
	}
	return result
}

func ProjectionPrefixGenesisV2() Digest {
	return digest("spec-kitty.projection-prefix.genesis.v2", nil)
}

// AdvanceProjectionPrefixV2 is shared by live sealing and restore. Its inputs
// are provider-neutral evidence coordinates; it performs no I/O.
func AdvanceProjectionPrefixV2(prior Digest, providerRevision uint64, publicationID PublicationID, planDigest, recordSetDigest Digest) (Digest, error) {
	if prior.IsZero() || providerRevision == 0 || planDigest.IsZero() || recordSetDigest.IsZero() {
		return Digest{}, invalid(ErrorInvalidDigest, "projection_prefix")
	}
	if _, err := DecodePublicationID(string(publicationID)); err != nil {
		return Digest{}, err
	}
	canonical, err := marshalCanonical(struct {
		Prior         string `json:"prior_prefix_sha256"`
		Revision      uint64 `json:"provider_revision"`
		PublicationID string `json:"publication_id"`
		Plan          string `json:"plan_sha256"`
		RecordSet     string `json:"record_set_sha256"`
	}{prior.String(), providerRevision, string(publicationID), planDigest.String(), recordSetDigest.String()}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.projection-prefix.v2", canonical), nil
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
