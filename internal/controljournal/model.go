package controljournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

const (
	EnvelopeVersionV1 = uint32(1)
	maxTokenBytes     = 128
	maxEventsPerGroup = 128
)

// Identity values are deliberately distinct even though they share one wire
// grammar. Their zero values are invalid and are rejected at construction and
// publication seams.
type (
	ProjectID      string
	RunID          string
	MissionID      string
	WorkRevisionID string
	WorkID         string
	ActorID        string
	ProcessID      string
	WorkspaceID    string
	RuntimeEpochID string
	OperationID    string
	RecordID       string
	EventID        string
	AggregateID    string
	IntentID       string
	CandidateID    string
	LeaseID        string
	EffectID       string
	AggregateKind  string
	EventType      string
	ReducerVersion string
)

type EventIndexV1 uint8
type EventCountV1 uint16

// RefusalKind is stable machine-readable diagnostic data. Error strings never
// include caller-controlled payload bytes.
type RefusalKind string

const (
	RefusalInvalidValue       RefusalKind = "invalid_value"
	RefusalUnsupportedVersion RefusalKind = "unsupported_version"
	RefusalBudgetExceeded     RefusalKind = "budget_exceeded"
	RefusalJournalCorrupt     RefusalKind = "journal_corrupt"
	RefusalInvalidTransition  RefusalKind = "invalid_transition"
	RefusalReducerMissing     RefusalKind = "reducer_missing"
)

type ValidationError struct {
	Kind  RefusalKind
	Field string
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Field
}

func validateToken(field, value string) error {
	if len(value) == 0 || len(value) > maxTokenBytes || !utf8.ValidString(value) {
		return ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		valid := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if i != 0 {
			valid = valid || c == '.' || c == '_' || c == ':' || c == '-'
		}
		if !valid {
			return ValidationError{Kind: RefusalInvalidValue, Field: field}
		}
	}
	return nil
}

type tokenField struct{ name, value string }

func validateTokens(fields ...tokenField) error {
	for _, field := range fields {
		if err := validateToken(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func NewProjectID(value string) (ProjectID, error) { return identity[ProjectID]("project_id", value) }
func NewRunID(value string) (RunID, error)         { return identity[RunID]("run_id", value) }
func NewMissionID(value string) (MissionID, error) { return identity[MissionID]("mission_id", value) }
func NewWorkRevisionID(value string) (WorkRevisionID, error) {
	return identity[WorkRevisionID]("work_revision_id", value)
}
func NewWorkID(value string) (WorkID, error)       { return identity[WorkID]("work_id", value) }
func NewActorID(value string) (ActorID, error)     { return identity[ActorID]("actor_id", value) }
func NewProcessID(value string) (ProcessID, error) { return identity[ProcessID]("process_id", value) }
func NewWorkspaceID(value string) (WorkspaceID, error) {
	return identity[WorkspaceID]("workspace_id", value)
}
func NewRuntimeEpochID(value string) (RuntimeEpochID, error) {
	return identity[RuntimeEpochID]("runtime_epoch_id", value)
}
func NewOperationID(value string) (OperationID, error) {
	return identity[OperationID]("operation_id", value)
}
func NewRecordID(value string) (RecordID, error) { return identity[RecordID]("record_id", value) }
func NewEventID(value string) (EventID, error)   { return identity[EventID]("event_id", value) }
func NewAggregateID(value string) (AggregateID, error) {
	return identity[AggregateID]("aggregate_id", value)
}
func NewIntentID(value string) (IntentID, error) { return identity[IntentID]("intent_id", value) }
func NewCandidateID(value string) (CandidateID, error) {
	return identity[CandidateID]("candidate_id", value)
}
func NewLeaseID(value string) (LeaseID, error)   { return identity[LeaseID]("lease_id", value) }
func NewEffectID(value string) (EffectID, error) { return identity[EffectID]("effect_id", value) }
func NewAggregateKind(value string) (AggregateKind, error) {
	return identity[AggregateKind]("aggregate_kind", value)
}
func NewEventType(value string) (EventType, error) { return identity[EventType]("event_type", value) }
func NewReducerVersion(value string) (ReducerVersion, error) {
	return identity[ReducerVersion]("reducer_version", value)
}

// ValidateAggregateCoordinateV1 applies the root journal token grammar to one
// aggregate identity without granting storage or mutation authority.
func ValidateAggregateCoordinateV1(kind AggregateKind, id AggregateID) error {
	return validateTokens(tokenField{"aggregate_kind", string(kind)}, tokenField{"aggregate_id", string(id)})
}

// ValidateReducerCoordinateV1 validates one exact reducer-registry coordinate.
func ValidateReducerCoordinateV1(kind AggregateKind, eventType EventType, schema uint32, version ReducerVersion) error {
	if err := validateTokens(tokenField{"aggregate_kind", string(kind)}, tokenField{"event_type", string(eventType)}, tokenField{"reducer_version", string(version)}); err != nil {
		return err
	}
	if schema == 0 {
		return ValidationError{Kind: RefusalInvalidValue, Field: "event_schema_version"}
	}
	return nil
}

func identity[T ~string](field, value string) (T, error) {
	if err := validateToken(field, value); err != nil {
		return "", err
	}
	return T(value), nil
}

// TrialImplementationActorsV1 freezes the proving-ground vocabulary: Process C
// is a replacement execution of Actor A, not a third implementation actor.
type TrialImplementationActorsV1 struct {
	ActorA   ActorID
	ActorB   ActorID
	ProcessA ProcessID
	ProcessB ProcessID
	ProcessC ProcessID
}

func NewTrialImplementationActorsV1(actorA, actorB ActorID, processA, processB, processC ProcessID) (TrialImplementationActorsV1, error) {
	if err := validateTokens(
		tokenField{"actor_a", string(actorA)}, tokenField{"actor_b", string(actorB)},
		tokenField{"process_a", string(processA)}, tokenField{"process_b", string(processB)}, tokenField{"process_c", string(processC)},
	); err != nil {
		return TrialImplementationActorsV1{}, err
	}
	if actorA == actorB || processA == processB || processA == processC || processB == processC {
		return TrialImplementationActorsV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "trial_actor_vocabulary"}
	}
	return TrialImplementationActorsV1{ActorA: actorA, ActorB: actorB, ProcessA: processA, ProcessB: processB, ProcessC: processC}, nil
}

func (v TrialImplementationActorsV1) ActorForProcess(process ProcessID) (ActorID, bool) {
	switch process {
	case v.ProcessA, v.ProcessC:
		return v.ActorA, true
	case v.ProcessB:
		return v.ActorB, true
	default:
		return "", false
	}
}

func NewEventIndexV1(value uint16) (EventIndexV1, error) {
	if value >= maxEventsPerGroup {
		return 0, ValidationError{Kind: RefusalInvalidValue, Field: "event_index"}
	}
	return EventIndexV1(value), nil
}

func NewEventCountV1(value uint16) (EventCountV1, error) {
	if value == 0 || value > maxEventsPerGroup {
		return 0, ValidationError{Kind: RefusalInvalidValue, Field: "event_count"}
	}
	return EventCountV1(value), nil
}

// EventEnvelopeInputV1 contains product facts known before storage accepts a
// batch. RecordRevision is intentionally absent.
type EventEnvelopeInputV1 struct {
	EnvelopeVersion         uint32
	EventID                 EventID
	EventType               EventType
	EventSchemaVersion      uint32
	ReducerVersion          ReducerVersion
	ProjectID               ProjectID
	AggregateKind           AggregateKind
	AggregateID             AggregateID
	AggregateLogicalVersion uint64
	OperationID             OperationID
	RuntimeEpochID          RuntimeEpochID
	EventIndex              EventIndexV1
	EventCount              EventCountV1
	WorkRevisionID          WorkRevisionID
	LeaseID                 LeaseID
	LeaseGeneration         uint64
	FenceToken              uint64
	PolicyRevision          string
	DoctrineRevision        string
	ProfileRevision         string
	RecordedObservations    []byte
	Payload                 []byte
}

// EventEnvelopeV1 is immutable-style: JSON objects and the digest are held in
// unexported fixed/copy-on-access fields.
type EventEnvelopeV1 struct {
	EnvelopeVersion         uint32
	EventID                 EventID
	EventType               EventType
	EventSchemaVersion      uint32
	ReducerVersion          ReducerVersion
	ProjectID               ProjectID
	AggregateKind           AggregateKind
	AggregateID             AggregateID
	AggregateLogicalVersion uint64
	OperationID             OperationID
	RuntimeEpochID          RuntimeEpochID
	EventIndex              EventIndexV1
	EventCount              EventCountV1
	WorkRevisionID          WorkRevisionID
	LeaseID                 LeaseID
	LeaseGeneration         uint64
	FenceToken              uint64
	PolicyRevision          string
	DoctrineRevision        string
	ProfileRevision         string
	recordedObservations    []byte
	payload                 []byte
	digest                  [32]byte
}

func NewEventEnvelopeV1(input EventEnvelopeInputV1) (EventEnvelopeV1, error) {
	e := EventEnvelopeV1{
		EnvelopeVersion: input.EnvelopeVersion, EventID: input.EventID,
		EventType: input.EventType, EventSchemaVersion: input.EventSchemaVersion,
		ReducerVersion: input.ReducerVersion, ProjectID: input.ProjectID,
		AggregateKind: input.AggregateKind, AggregateID: input.AggregateID,
		AggregateLogicalVersion: input.AggregateLogicalVersion,
		OperationID:             input.OperationID, RuntimeEpochID: input.RuntimeEpochID,
		EventIndex: input.EventIndex, EventCount: input.EventCount,
		WorkRevisionID: input.WorkRevisionID, LeaseID: input.LeaseID,
		LeaseGeneration: input.LeaseGeneration, FenceToken: input.FenceToken,
		PolicyRevision: input.PolicyRevision, DoctrineRevision: input.DoctrineRevision,
		ProfileRevision: input.ProfileRevision,
	}
	var err error
	if len(input.RecordedObservations) != 0 {
		e.recordedObservations, err = canonicalJSONObject(input.RecordedObservations, "recorded_observations")
		if err != nil {
			return EventEnvelopeV1{}, err
		}
	}
	e.payload, err = canonicalJSONObject(input.Payload, "payload")
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	if err := e.validate(); err != nil {
		return EventEnvelopeV1{}, err
	}
	canonical, err := e.canonicalBytes(false)
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	e.digest = sha256.Sum256(canonical)
	return e, nil
}

func (e EventEnvelopeV1) Payload() []byte { return append([]byte(nil), e.payload...) }
func (e EventEnvelopeV1) RecordedObservations() []byte {
	return append([]byte(nil), e.recordedObservations...)
}
func (e EventEnvelopeV1) CanonicalEventDigest() [32]byte  { return e.digest }
func (e EventEnvelopeV1) CanonicalEventDigestHex() string { return hex.EncodeToString(e.digest[:]) }

func (e EventEnvelopeV1) clone() EventEnvelopeV1 {
	e.payload = append([]byte(nil), e.payload...)
	e.recordedObservations = append([]byte(nil), e.recordedObservations...)
	return e
}

func (e EventEnvelopeV1) validate() error {
	if e.EnvelopeVersion != EnvelopeVersionV1 {
		return ValidationError{Kind: RefusalUnsupportedVersion, Field: "envelope_version"}
	}
	if err := validateTokens(
		tokenField{"event_id", string(e.EventID)}, tokenField{"event_type", string(e.EventType)},
		tokenField{"reducer_version", string(e.ReducerVersion)}, tokenField{"project_id", string(e.ProjectID)},
		tokenField{"aggregate_kind", string(e.AggregateKind)}, tokenField{"aggregate_id", string(e.AggregateID)},
		tokenField{"operation_id", string(e.OperationID)}, tokenField{"runtime_epoch_id", string(e.RuntimeEpochID)},
	); err != nil {
		return err
	}
	for _, field := range []tokenField{
		{"work_revision_id", string(e.WorkRevisionID)}, {"lease_id", string(e.LeaseID)},
		{"policy_revision", e.PolicyRevision}, {"doctrine_revision", e.DoctrineRevision}, {"profile_revision", e.ProfileRevision},
	} {
		if field.value != "" {
			if err := validateToken(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if e.EventSchemaVersion == 0 || e.AggregateLogicalVersion == 0 {
		return ValidationError{Kind: RefusalInvalidValue, Field: "event_version"}
	}
	if e.EventCount == 0 || e.EventCount > maxEventsPerGroup || uint16(e.EventIndex) >= uint16(e.EventCount) {
		return ValidationError{Kind: RefusalInvalidValue, Field: "event_position"}
	}
	if (e.LeaseID == "") != (e.LeaseGeneration == 0 && e.FenceToken == 0) {
		return ValidationError{Kind: RefusalInvalidValue, Field: "lease_authority"}
	}
	if len(e.payload) == 0 || !json.Valid(e.payload) || len(e.recordedObservations) != 0 && !json.Valid(e.recordedObservations) {
		return ValidationError{Kind: RefusalInvalidValue, Field: "canonical_object"}
	}
	return nil
}

type eventEnvelopeWireV1 struct {
	AggregateID             string          `json:"aggregate_id"`
	AggregateKind           string          `json:"aggregate_kind"`
	AggregateLogicalVersion uint64          `json:"aggregate_logical_version"`
	CanonicalEventDigest    string          `json:"canonical_event_digest,omitempty"`
	DoctrineRevision        string          `json:"doctrine_revision,omitempty"`
	EnvelopeVersion         uint32          `json:"envelope_version"`
	EventCount              uint16          `json:"event_count"`
	EventID                 string          `json:"event_id"`
	EventIndex              uint16          `json:"event_index"`
	EventSchemaVersion      uint32          `json:"event_schema_version"`
	EventType               string          `json:"event_type"`
	FenceToken              uint64          `json:"fence_token,omitempty"`
	LeaseGeneration         uint64          `json:"lease_generation,omitempty"`
	LeaseID                 string          `json:"lease_id,omitempty"`
	OperationID             string          `json:"operation_id"`
	Payload                 json.RawMessage `json:"payload"`
	PolicyRevision          string          `json:"policy_revision,omitempty"`
	ProfileRevision         string          `json:"profile_revision,omitempty"`
	ProjectID               string          `json:"project_id"`
	RecordedObservations    json.RawMessage `json:"recorded_observations,omitempty"`
	ReducerVersion          string          `json:"reducer_version"`
	RuntimeEpochID          string          `json:"runtime_epoch_id"`
	WorkRevisionID          string          `json:"work_revision_id,omitempty"`
}

func (e EventEnvelopeV1) wire(includeDigest bool) eventEnvelopeWireV1 {
	w := eventEnvelopeWireV1{
		AggregateID: string(e.AggregateID), AggregateKind: string(e.AggregateKind),
		AggregateLogicalVersion: e.AggregateLogicalVersion,
		DoctrineRevision:        e.DoctrineRevision, EnvelopeVersion: e.EnvelopeVersion,
		EventCount: uint16(e.EventCount), EventID: string(e.EventID),
		EventIndex: uint16(e.EventIndex), EventSchemaVersion: e.EventSchemaVersion,
		EventType: string(e.EventType), FenceToken: e.FenceToken,
		LeaseGeneration: e.LeaseGeneration, LeaseID: string(e.LeaseID),
		OperationID: string(e.OperationID), Payload: e.Payload(),
		PolicyRevision: e.PolicyRevision, ProfileRevision: e.ProfileRevision,
		ProjectID: string(e.ProjectID), RecordedObservations: e.RecordedObservations(),
		ReducerVersion: string(e.ReducerVersion), RuntimeEpochID: string(e.RuntimeEpochID),
		WorkRevisionID: string(e.WorkRevisionID),
	}
	if includeDigest {
		w.CanonicalEventDigest = e.CanonicalEventDigestHex()
	}
	return w
}

func (e EventEnvelopeV1) canonicalBytes(includeDigest bool) ([]byte, error) {
	return json.Marshal(e.wire(includeDigest))
}

// StoredEventV1 is the sole owner of storage-assigned RecordRevision metadata.
type StoredEventV1 struct {
	RecordRevision uint64
	Envelope       EventEnvelopeV1
}

type EventVersionV1 struct {
	EventType          string
	EventSchemaVersion uint32
	ReducerVersion     string
}

type EventRegistryV1 struct {
	versions map[string]EventVersionV1
	digest   [32]byte
}

func NewEventRegistryV1(versions []EventVersionV1) (EventRegistryV1, error) {
	registry := EventRegistryV1{versions: make(map[string]EventVersionV1, len(versions))}
	if len(versions) == 0 {
		return EventRegistryV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_registry"}
	}
	ordered := append([]EventVersionV1(nil), versions...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EventType != ordered[j].EventType {
			return ordered[i].EventType < ordered[j].EventType
		}
		if ordered[i].EventSchemaVersion != ordered[j].EventSchemaVersion {
			return ordered[i].EventSchemaVersion < ordered[j].EventSchemaVersion
		}
		return ordered[i].ReducerVersion < ordered[j].ReducerVersion
	})
	var encoded bytes.Buffer
	for _, version := range ordered {
		if err := validateToken("event_type", version.EventType); err != nil {
			return EventRegistryV1{}, err
		}
		if version.EventSchemaVersion == 0 {
			return EventRegistryV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_schema_version"}
		}
		if err := validateToken("reducer_version", version.ReducerVersion); err != nil {
			return EventRegistryV1{}, err
		}
		key := eventVersionKey(version.EventType, version.EventSchemaVersion, version.ReducerVersion)
		if _, exists := registry.versions[key]; exists {
			return EventRegistryV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_registry_duplicate"}
		}
		registry.versions[key] = version
		writeLengthPrefixed(&encoded, []byte(key))
	}
	registry.digest = sha256.Sum256(encoded.Bytes())
	return registry, nil
}

func eventVersionKey(eventType string, schema uint32, reducer string) string {
	return eventType + "\x00" + fmt.Sprintf("%d", schema) + "\x00" + reducer
}

func (r EventRegistryV1) valid() bool {
	if len(r.versions) == 0 {
		return false
	}
	versions := make([]EventVersionV1, 0, len(r.versions))
	for _, version := range r.versions {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].EventType != versions[j].EventType {
			return versions[i].EventType < versions[j].EventType
		}
		if versions[i].EventSchemaVersion != versions[j].EventSchemaVersion {
			return versions[i].EventSchemaVersion < versions[j].EventSchemaVersion
		}
		return versions[i].ReducerVersion < versions[j].ReducerVersion
	})
	var encoded bytes.Buffer
	for _, version := range versions {
		key := eventVersionKey(version.EventType, version.EventSchemaVersion, version.ReducerVersion)
		if r.versions[key] != version {
			return false
		}
		writeLengthPrefixed(&encoded, []byte(key))
	}
	return sha256.Sum256(encoded.Bytes()) == r.digest
}

func (r EventRegistryV1) Digest() ([32]byte, error) {
	if !r.valid() {
		return [32]byte{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_registry"}
	}
	return r.digest, nil
}

type validationSealV1 struct {
	version uint8
	digest  [32]byte
}

type ValidatedJournalV1 struct {
	seal    validationSealV1
	records []StoredEventV1
}

type InvalidValidatedJournalError struct{}

func (InvalidValidatedJournalError) Error() string { return "invalid validated journal" }

// ValidateJournalV1 is the sole raw-event validation and ordering authority.
func ValidateJournalV1(registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error) {
	if !registry.valid() {
		return ValidatedJournalV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "event_registry"}
	}
	canonical := make([]StoredEventV1, len(events))
	for i := range events {
		canonical[i] = StoredEventV1{RecordRevision: events[i].RecordRevision, Envelope: events[i].Envelope.clone()}
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].RecordRevision != canonical[j].RecordRevision {
			return canonical[i].RecordRevision < canonical[j].RecordRevision
		}
		if canonical[i].Envelope.EventIndex != canonical[j].Envelope.EventIndex {
			return canonical[i].Envelope.EventIndex < canonical[j].Envelope.EventIndex
		}
		return canonical[i].Envelope.EventID < canonical[j].Envelope.EventID
	})

	type group struct {
		revision uint64
		count    EventCountV1
		seen     map[EventIndexV1]bool
	}
	groups := make(map[OperationID]*group)
	revisionOperations := make(map[uint64]OperationID)
	seenIDs := make(map[EventID]bool, len(canonical))
	for i := range canonical {
		record := canonical[i]
		e := record.Envelope
		if record.RecordRevision == 0 {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "record_revision"}
		}
		if err := e.validate(); err != nil {
			return ValidatedJournalV1{}, err
		}
		if seenIDs[e.EventID] {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "duplicate_event_id"}
		}
		seenIDs[e.EventID] = true
		if operation, exists := revisionOperations[record.RecordRevision]; exists && operation != e.OperationID {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "record_revision_operation"}
		}
		revisionOperations[record.RecordRevision] = e.OperationID
		if i > 0 {
			previous := canonical[i-1]
			if previous.RecordRevision == record.RecordRevision && previous.Envelope.EventIndex == e.EventIndex && previous.Envelope.EventID == e.EventID {
				return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "duplicate_order_tuple"}
			}
		}
		key := eventVersionKey(string(e.EventType), e.EventSchemaVersion, string(e.ReducerVersion))
		if _, ok := registry.versions[key]; !ok {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "event_registry"}
		}
		unsigned, err := e.canonicalBytes(false)
		if err != nil || sha256.Sum256(unsigned) != e.digest {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "canonical_event_digest"}
		}
		g := groups[e.OperationID]
		if g == nil {
			g = &group{revision: record.RecordRevision, count: e.EventCount, seen: make(map[EventIndexV1]bool)}
			groups[e.OperationID] = g
		}
		if g.revision != record.RecordRevision || g.count != e.EventCount {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "operation_group_metadata"}
		}
		if g.seen[e.EventIndex] {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "operation_group_index"}
		}
		g.seen[e.EventIndex] = true
	}
	for _, g := range groups {
		if len(g.seen) != int(g.count) {
			return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "operation_group_count"}
		}
		for index := EventIndexV1(0); uint16(index) < uint16(g.count); index++ {
			if !g.seen[index] {
				return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "operation_group_index"}
			}
		}
	}
	journal := ValidatedJournalV1{records: canonical}
	digest, err := journal.contentDigest()
	if err != nil || digest == [32]byte{} {
		return ValidatedJournalV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "validation_seal"}
	}
	journal.seal = validationSealV1{version: 1, digest: digest}
	return journal, nil
}

func (journal ValidatedJournalV1) Len() (int, error) {
	if !journal.isValid() {
		return 0, InvalidValidatedJournalError{}
	}
	return len(journal.records), nil
}

func (journal ValidatedJournalV1) At(index int) (StoredEventV1, error) {
	if !journal.isValid() {
		return StoredEventV1{}, InvalidValidatedJournalError{}
	}
	if index < 0 || index >= len(journal.records) {
		return StoredEventV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "journal_index"}
	}
	record := journal.records[index]
	record.Envelope = record.Envelope.clone()
	return record, nil
}

func (journal ValidatedJournalV1) isValid() bool {
	if journal.seal.version != 1 || journal.seal.digest == [32]byte{} {
		return false
	}
	digest, err := journal.contentDigest()
	return err == nil && digest != [32]byte{} && journal.seal.digest == digest
}

func (journal ValidatedJournalV1) contentDigest() ([32]byte, error) {
	encoded := sha256.New()
	recordBytes := appendJournalSealUint64(make([]byte, 0, 512), uint64(len(journal.records)))
	_, _ = encoded.Write(recordBytes)
	for _, record := range journal.records {
		recordBytes = recordBytes[:0]
		event := record.Envelope
		recordBytes = appendJournalSealUint64(recordBytes, record.RecordRevision)
		recordBytes = appendJournalSealUint64(recordBytes, uint64(event.EnvelopeVersion))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.EventID))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.EventType))
		recordBytes = appendJournalSealUint64(recordBytes, uint64(event.EventSchemaVersion))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.ReducerVersion))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.ProjectID))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.AggregateKind))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.AggregateID))
		recordBytes = appendJournalSealUint64(recordBytes, event.AggregateLogicalVersion)
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.OperationID))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.RuntimeEpochID))
		recordBytes = appendJournalSealUint64(recordBytes, uint64(event.EventIndex))
		recordBytes = appendJournalSealUint64(recordBytes, uint64(event.EventCount))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.WorkRevisionID))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.LeaseID))
		recordBytes = appendJournalSealUint64(recordBytes, event.LeaseGeneration)
		recordBytes = appendJournalSealUint64(recordBytes, event.FenceToken)
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.PolicyRevision))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.DoctrineRevision))
		recordBytes = appendJournalSealBytes(recordBytes, []byte(event.ProfileRevision))
		recordBytes = appendJournalSealBytes(recordBytes, event.recordedObservations)
		recordBytes = appendJournalSealBytes(recordBytes, event.payload)
		recordBytes = appendJournalSealBytes(recordBytes, event.digest[:])
		_, _ = encoded.Write(recordBytes)
	}
	var digest [32]byte
	copy(digest[:], encoded.Sum(nil))
	return digest, nil
}

func appendJournalSealUint64(encoded []byte, value uint64) []byte {
	var binaryValue [8]byte
	binary.BigEndian.PutUint64(binaryValue[:], value)
	return appendJournalSealBytes(encoded, binaryValue[:])
}

func appendJournalSealBytes(encoded, value []byte) []byte {
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(value)))
	return append(encoded, value...)
}

func writeLengthPrefixed(buffer *bytes.Buffer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	buffer.Write(length[:])
	buffer.Write(value)
}

type AggregateHeadV1 struct {
	SchemaVersion  uint32
	AggregateKind  AggregateKind
	AggregateID    AggregateID
	LogicalVersion uint64
	RuntimeEpochID RuntimeEpochID
	LastEventID    EventID
	StateDigest    [32]byte
}

func ConstructAggregateHeadV1(kind AggregateKind, id AggregateID, logicalVersion uint64, epoch RuntimeEpochID, lastEvent EventID, digest [32]byte) (AggregateHeadV1, error) {
	if err := validateTokens(tokenField{"aggregate_kind", string(kind)}, tokenField{"aggregate_id", string(id)}, tokenField{"runtime_epoch_id", string(epoch)}, tokenField{"last_event_id", string(lastEvent)}); err != nil {
		return AggregateHeadV1{}, err
	}
	if logicalVersion == 0 || digest == [32]byte{} {
		return AggregateHeadV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "aggregate_head"}
	}
	return AggregateHeadV1{SchemaVersion: 1, AggregateKind: kind, AggregateID: id, LogicalVersion: logicalVersion, RuntimeEpochID: epoch, LastEventID: lastEvent, StateDigest: digest}, nil
}

type ExpectedAggregateHeadV1 struct {
	AggregateKind        AggregateKind
	AggregateID          AggregateID
	LogicalVersion       uint64
	ObservedHeadRevision uint64
}

type NewAggregateHeadV1 struct{ Head AggregateHeadV1 }

func ValidateHeadTransition(expected ExpectedAggregateHeadV1, next NewAggregateHeadV1) error {
	if err := validateTokens(tokenField{"aggregate_kind", string(expected.AggregateKind)}, tokenField{"aggregate_id", string(expected.AggregateID)}); err != nil {
		return err
	}
	if expected.ObservedHeadRevision == 0 || expected.LogicalVersion == ^uint64(0) {
		return ValidationError{Kind: RefusalInvalidValue, Field: "expected_head"}
	}
	if next.Head.SchemaVersion != 1 {
		return ValidationError{Kind: RefusalUnsupportedVersion, Field: "aggregate_head"}
	}
	if _, err := ConstructAggregateHeadV1(next.Head.AggregateKind, next.Head.AggregateID, next.Head.LogicalVersion, next.Head.RuntimeEpochID, next.Head.LastEventID, next.Head.StateDigest); err != nil {
		return err
	}
	if next.Head.AggregateKind != expected.AggregateKind || next.Head.AggregateID != expected.AggregateID || next.Head.LogicalVersion != expected.LogicalVersion+1 {
		return ValidationError{Kind: RefusalInvalidTransition, Field: "aggregate_head"}
	}
	return nil
}
