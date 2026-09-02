// Package v2 defines the provider-independent authority journal contract.
// It deliberately contains no persistence, clock, transport, or effect ports.
package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion            = uint32(2)
	EnvelopeVersion          = uint32(2)
	MaxIdentityBytes         = 128
	MaxCanonicalPayloadBytes = 256 * 1024
	MaxEventEnvelopeBytes    = 1024 * 1024
	MaxMembers               = 256
	MaxEventsPerPublication  = 128
	MaxEffectsPerPublication = 64
	MaxConditionsPerPlan     = 256
	MaxCheckpointsPerPlan    = 32
	MaxCommitPlanBytes       = 4 * 1024 * 1024
	publicationDomain        = "spec-kitty.publication.v2"
	canonicalEventDomain     = "spec-kitty.event.v2"
	canonicalIntentDomain    = "spec-kitty.intent.v2"
)

type ErrorKind string

const (
	ErrorInvalidIdentity      ErrorKind = "INVALID_IDENTITY"
	ErrorInvalidScope         ErrorKind = "INVALID_SCOPE"
	ErrorInvalidVersion       ErrorKind = "INVALID_VERSION"
	ErrorInvalidKeyComponent  ErrorKind = "INVALID_KEY_COMPONENT"
	ErrorInvalidCanonicalData ErrorKind = "INVALID_CANONICAL_BYTES"
	ErrorInvalidDigest        ErrorKind = "INVALID_DIGEST"
	ErrorBudgetExceeded       ErrorKind = "BUDGET_EXCEEDED"
	ErrorIdempotencyConflict  ErrorKind = "IDEMPOTENCY_CONFLICT"
	ErrorJournalCorrupt       ErrorKind = "JOURNAL_CORRUPT"
	ErrorInvalidTransition    ErrorKind = "INVALID_TRANSITION"
	ErrorUnknownEventTuple    ErrorKind = "UNKNOWN_EVENT_TUPLE"
	ErrorNondeterministic     ErrorKind = "NONDETERMINISTIC_REDUCER"
)

// Error is bounded machine-readable diagnostic evidence. It never includes a
// rejected caller payload or identity value.
type Error struct {
	Kind  ErrorKind
	Field string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Field
}

func IsKind(err error, kind ErrorKind) bool {
	for err != nil {
		if candidate, ok := err.(*Error); ok {
			return candidate.Kind == kind
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func invalid(kind ErrorKind, field string) error { return &Error{Kind: kind, Field: field} }

type (
	HostAuthorityID string
	ProjectID       string
	MissionID       string
	RepositoryID    string
	WorkRevisionID  string
	WorkID          string
	OperationID     string
	PublicationID   string
	RuntimeEpochID  string
	EventID         string
	EffectID        string
	ActorID         string
	ProcessID       string
	AggregateKind   string
	AggregateID     string
)

func identity[T ~string](field, value string) (T, error) {
	if err := validateIdentity(field, value); err != nil {
		return "", err
	}
	return T(value), nil
}

func NewHostAuthorityID(v string) (HostAuthorityID, error) {
	return identity[HostAuthorityID]("host_authority_id", v)
}
func NewProjectID(v string) (ProjectID, error) { return identity[ProjectID]("project_id", v) }
func NewMissionID(v string) (MissionID, error) { return identity[MissionID]("mission_id", v) }
func NewRepositoryID(v string) (RepositoryID, error) {
	return identity[RepositoryID]("repository_id", v)
}
func NewWorkRevisionID(v string) (WorkRevisionID, error) {
	return identity[WorkRevisionID]("work_revision_id", v)
}
func NewWorkID(v string) (WorkID, error)           { return identity[WorkID]("work_id", v) }
func NewOperationID(v string) (OperationID, error) { return identity[OperationID]("operation_id", v) }
func NewRuntimeEpochID(v string) (RuntimeEpochID, error) {
	return identity[RuntimeEpochID]("runtime_epoch_id", v)
}
func NewEventID(v string) (EventID, error)     { return identity[EventID]("event_id", v) }
func NewEffectID(v string) (EffectID, error)   { return identity[EffectID]("effect_id", v) }
func NewActorID(v string) (ActorID, error)     { return identity[ActorID]("actor_id", v) }
func NewProcessID(v string) (ProcessID, error) { return identity[ProcessID]("process_id", v) }
func NewAggregateKind(v string) (AggregateKind, error) {
	return identity[AggregateKind]("aggregate_kind", v)
}
func NewAggregateID(v string) (AggregateID, error) { return identity[AggregateID]("aggregate_id", v) }

func validateIdentity(field, value string) error {
	if len(value) == 0 || len(value) > MaxIdentityBytes || !utf8.ValidString(value) {
		return invalid(ErrorInvalidIdentity, field)
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		ok := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if index > 0 {
			ok = ok || char == '.' || char == '_' || char == ':' || char == '-'
		}
		if !ok {
			return invalid(ErrorInvalidIdentity, field)
		}
	}
	return nil
}

type ScopeKind string

const (
	ScopeHost         ScopeKind = "host"
	ScopeProject      ScopeKind = "project"
	ScopeMission      ScopeKind = "mission"
	ScopeRepository   ScopeKind = "repository"
	ScopeWorkRevision ScopeKind = "work_revision"
	ScopeWork         ScopeKind = "work"
)

type Scope struct {
	Kind           ScopeKind      `json:"kind"`
	ProjectID      ProjectID      `json:"project_id,omitempty"`
	MissionID      MissionID      `json:"mission_id,omitempty"`
	RepositoryID   RepositoryID   `json:"repository_id,omitempty"`
	WorkRevisionID WorkRevisionID `json:"work_revision_id,omitempty"`
	WorkID         WorkID         `json:"work_id,omitempty"`
}

func NewScope(scope Scope) (Scope, error) {
	if err := scope.Validate(); err != nil {
		return Scope{}, err
	}
	return scope, nil
}

func (s Scope) Validate() error {
	required := func(field, value string) error { return validateIdentity(field, value) }
	empty := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return false
			}
		}
		return true
	}
	switch s.Kind {
	case ScopeHost:
		if !empty(string(s.ProjectID), string(s.MissionID), string(s.RepositoryID), string(s.WorkRevisionID), string(s.WorkID)) {
			return invalid(ErrorInvalidScope, "host_components")
		}
	case ScopeProject:
		if err := required("project_id", string(s.ProjectID)); err != nil || !empty(string(s.MissionID), string(s.RepositoryID), string(s.WorkRevisionID), string(s.WorkID)) {
			return invalid(ErrorInvalidScope, "project_components")
		}
	case ScopeMission:
		if required("project_id", string(s.ProjectID)) != nil || required("mission_id", string(s.MissionID)) != nil || !empty(string(s.RepositoryID), string(s.WorkRevisionID), string(s.WorkID)) {
			return invalid(ErrorInvalidScope, "mission_components")
		}
	case ScopeRepository:
		if required("project_id", string(s.ProjectID)) != nil || required("repository_id", string(s.RepositoryID)) != nil || !empty(string(s.MissionID), string(s.WorkRevisionID), string(s.WorkID)) {
			return invalid(ErrorInvalidScope, "repository_components")
		}
	case ScopeWorkRevision:
		if required("project_id", string(s.ProjectID)) != nil || required("mission_id", string(s.MissionID)) != nil || required("work_revision_id", string(s.WorkRevisionID)) != nil || !empty(string(s.RepositoryID), string(s.WorkID)) {
			return invalid(ErrorInvalidScope, "work_revision_components")
		}
	case ScopeWork:
		if required("project_id", string(s.ProjectID)) != nil || required("mission_id", string(s.MissionID)) != nil || required("work_revision_id", string(s.WorkRevisionID)) != nil || required("work_id", string(s.WorkID)) != nil || !empty(string(s.RepositoryID)) {
			return invalid(ErrorInvalidScope, "work_components")
		}
	default:
		return invalid(ErrorInvalidScope, "kind")
	}
	return nil
}

func (s Scope) CanonicalComponent() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	parts := []string{string(s.Kind)}
	for _, value := range []string{string(s.ProjectID), string(s.MissionID), string(s.RepositoryID), string(s.WorkRevisionID), string(s.WorkID)} {
		if value != "" {
			encoded, err := EncodeKeyComponent(value)
			if err != nil {
				return "", err
			}
			parts = append(parts, encoded)
		}
	}
	return strings.Join(parts, ":"), nil
}

type LogicalVersion uint64

func NewLogicalVersion(value uint64) (LogicalVersion, error) {
	if value == 0 {
		return 0, invalid(ErrorInvalidVersion, "logical_version")
	}
	return LogicalVersion(value), nil
}

func (v LogicalVersion) Hex16() (string, error) {
	if v == 0 {
		return "", invalid(ErrorInvalidVersion, "logical_version")
	}
	return fmt.Sprintf("%016x", uint64(v)), nil
}

type Digest [sha256.Size]byte

func (d Digest) String() string { return hex.EncodeToString(d[:]) }
func (d Digest) IsZero() bool   { return d == Digest{} }

func ParseDigest(field, value string) (Digest, error) {
	if len(value) != sha256.Size*2 {
		return Digest{}, invalid(ErrorInvalidDigest, field)
	}
	for _, char := range []byte(value) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return Digest{}, invalid(ErrorInvalidDigest, field)
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return Digest{}, invalid(ErrorInvalidDigest, field)
	}
	var digest Digest
	copy(digest[:], decoded)
	return digest, nil
}

func digest(domain string, payload []byte) Digest {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	var result Digest
	copy(result[:], hash.Sum(nil))
	return result
}

type AggregateCoordinate struct {
	Kind  AggregateKind `json:"kind"`
	ID    AggregateID   `json:"id"`
	Scope Scope         `json:"scope"`
}

func NewAggregateCoordinate(kind AggregateKind, id AggregateID, scope Scope) (AggregateCoordinate, error) {
	if err := validateIdentity("aggregate_kind", string(kind)); err != nil {
		return AggregateCoordinate{}, err
	}
	if err := validateIdentity("aggregate_id", string(id)); err != nil {
		return AggregateCoordinate{}, err
	}
	if err := scope.Validate(); err != nil {
		return AggregateCoordinate{}, err
	}
	return AggregateCoordinate{Kind: kind, ID: id, Scope: scope}, nil
}

func (c AggregateCoordinate) canonical() string {
	scope, _ := c.Scope.CanonicalComponent()
	return scope + "/" + string(c.Kind) + "/" + string(c.ID)
}

func canonicalIDs[T ~string](field string, values []T, limit int) ([]T, error) {
	if len(values) > limit {
		return nil, invalid(ErrorBudgetExceeded, field)
	}
	result := append([]T(nil), values...)
	for _, value := range result {
		if err := validateIdentity(field, string(value)); err != nil {
			return nil, err
		}
	}
	if !sort.SliceIsSorted(result, func(i, j int) bool { return result[i] < result[j] }) {
		return nil, invalid(ErrorInvalidCanonicalData, field)
	}
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, invalid(ErrorInvalidCanonicalData, field)
		}
	}
	return result, nil
}

type EventTuple struct {
	Family             string `json:"family"`
	EventSchemaVersion uint32 `json:"event_schema_version"`
	ReducerVersion     uint32 `json:"reducer_version"`
	Owner              string `json:"owner"`
}

func ExpectedEventTuples() []EventTuple {
	return []EventTuple{
		{"runtime_host", 1, 1, "internal/productionruntime"},
		{"operation", 1, 1, "internal/controljournal/application"},
		{"mission", 1, 1, "internal/mission"},
		{"artifact_catalog", 1, 1, "internal/artifact"},
		{"artifact_revision", 1, 1, "internal/artifact"},
		{"workgraph", 1, 1, "internal/workgraph"},
		{"execution_slot", 1, 1, "internal/execution"},
		{"workspace", 1, 1, "internal/workspace"},
		{"handoff_slot", 1, 1, "internal/handoff"},
		{"review_slot", 1, 1, "internal/review"},
		{"acceptance", 1, 1, "internal/integration"},
		{"integration_target", 1, 1, "internal/integration"},
		{"integration_attempt", 1, 1, "internal/integration"},
		{"external_effect", 1, 1, "internal/execution"},
		{"outbox", 1, 1, "internal/controlstore"},
	}
}

type EventRegistryV2 struct{ tuples map[string]EventTuple }

func NewEventRegistryV2(tuples []EventTuple) (EventRegistryV2, error) {
	if len(tuples) != len(ExpectedEventTuples()) || len(tuples) > MaxMembers {
		return EventRegistryV2{}, invalid(ErrorUnknownEventTuple, "event_registry")
	}
	want := make(map[string]EventTuple)
	for _, tuple := range ExpectedEventTuples() {
		want[eventTupleKey(tuple.Family, tuple.EventSchemaVersion, tuple.ReducerVersion)] = tuple
	}
	got := make(map[string]EventTuple, len(tuples))
	for _, tuple := range tuples {
		key := eventTupleKey(tuple.Family, tuple.EventSchemaVersion, tuple.ReducerVersion)
		if tuple.Family == "" || tuple.Owner == "" || got[key].Family != "" || want[key] != tuple {
			return EventRegistryV2{}, invalid(ErrorUnknownEventTuple, "event_registry")
		}
		got[key] = tuple
	}
	return EventRegistryV2{tuples: got}, nil
}

func DefaultEventRegistryV2() EventRegistryV2 {
	registry, err := NewEventRegistryV2(ExpectedEventTuples())
	if err != nil {
		panic("static event registry is invalid")
	}
	return registry
}

func (r EventRegistryV2) Resolve(family string, eventSchemaVersion, reducerVersion uint32) (EventTuple, error) {
	tuple, ok := r.tuples[eventTupleKey(family, eventSchemaVersion, reducerVersion)]
	if !ok {
		return EventTuple{}, invalid(ErrorUnknownEventTuple, "event_tuple")
	}
	return tuple, nil
}

func eventTupleKey(family string, eventSchemaVersion, reducerVersion uint32) string {
	return fmt.Sprintf("%s@%d@%d", family, eventSchemaVersion, reducerVersion)
}

type EventEnvelopeInput struct {
	EventID                 EventID
	EventType               string
	Family                  string
	EventSchemaVersion      uint32
	ReducerVersion          uint32
	Coordinate              AggregateCoordinate
	AggregateLogicalVersion LogicalVersion
	OperationID             OperationID
	OperationLogicalVersion LogicalVersion
	PublicationID           PublicationID
	CommittedRuntimeEpochID RuntimeEpochID
	EventIndex              uint16
	EventCount              uint16
	ActorID                 ActorID
	ProcessID               ProcessID
	WorkRevisionID          WorkRevisionID
	PolicyRevision          string
	GovernanceRevisions     []string
	Observations            []byte
	Payload                 []byte
}

type EventEnvelopeV2 struct {
	input        EventEnvelopeInput
	payload      []byte
	observations []byte
	digest       Digest
}

func preflightGovernanceRevisions(revisions []string) error {
	if len(revisions) > MaxMembers {
		return invalid(ErrorBudgetExceeded, "governance_revisions")
	}
	for _, revision := range revisions {
		if len(revision) > MaxIdentityBytes {
			return invalid(ErrorBudgetExceeded, "governance_revision")
		}
	}
	return nil
}

func validateEventAggregateAuthority(family string, coordinate AggregateCoordinate, operationID OperationID, operationVersion, aggregateVersion LogicalVersion) error {
	if coordinate.Kind == AggregateKind("operation") && (coordinate.Scope != (Scope{Kind: ScopeHost}) ||
		coordinate.ID != AggregateID(operationID) || aggregateVersion != operationVersion) {
		return invalid(ErrorInvalidTransition, "operation_event")
	}
	if string(coordinate.Kind) != family {
		return invalid(ErrorInvalidTransition, "event_family_aggregate")
	}
	return nil
}

func NewEventEnvelopeV2(input EventEnvelopeInput, registry EventRegistryV2) (EventEnvelopeV2, error) {
	if err := preflightGovernanceRevisions(input.GovernanceRevisions); err != nil {
		return EventEnvelopeV2{}, err
	}
	if len(input.Payload) > MaxCanonicalPayloadBytes {
		return EventEnvelopeV2{}, invalid(ErrorBudgetExceeded, "event_payload")
	}
	if len(input.Observations) > MaxCanonicalPayloadBytes {
		return EventEnvelopeV2{}, invalid(ErrorBudgetExceeded, "event_observations")
	}
	if validateIdentity("event_id", string(input.EventID)) != nil || validateIdentity("event_type", input.EventType) != nil ||
		validateIdentity("operation_id", string(input.OperationID)) != nil || validateIdentity("publication_id", string(input.PublicationID)) != nil ||
		validateIdentity("runtime_epoch_id", string(input.CommittedRuntimeEpochID)) != nil || input.AggregateLogicalVersion == 0 || input.OperationLogicalVersion == 0 {
		return EventEnvelopeV2{}, invalid(ErrorInvalidIdentity, "event_envelope")
	}
	if _, err := NewAggregateCoordinate(input.Coordinate.Kind, input.Coordinate.ID, input.Coordinate.Scope); err != nil {
		return EventEnvelopeV2{}, err
	}
	if _, err := registry.Resolve(input.Family, input.EventSchemaVersion, input.ReducerVersion); err != nil {
		return EventEnvelopeV2{}, err
	}
	if err := validateEventAggregateAuthority(input.Family, input.Coordinate, input.OperationID, input.OperationLogicalVersion, input.AggregateLogicalVersion); err != nil {
		return EventEnvelopeV2{}, err
	}
	if input.EventCount == 0 || input.EventCount > MaxEventsPerPublication || input.EventIndex >= input.EventCount {
		return EventEnvelopeV2{}, invalid(ErrorInvalidVersion, "event_position")
	}
	for _, optional := range []struct{ field, value string }{{"actor_id", string(input.ActorID)}, {"process_id", string(input.ProcessID)}, {"work_revision_id", string(input.WorkRevisionID)}, {"policy_revision", input.PolicyRevision}} {
		if optional.value != "" && validateIdentity(optional.field, optional.value) != nil {
			return EventEnvelopeV2{}, invalid(ErrorInvalidIdentity, optional.field)
		}
	}
	governance := append([]string(nil), input.GovernanceRevisions...)
	if !sort.StringsAreSorted(governance) {
		return EventEnvelopeV2{}, invalid(ErrorInvalidCanonicalData, "governance_revisions")
	}
	for index, revision := range governance {
		if validateIdentity("governance_revision", revision) != nil || index > 0 && governance[index-1] == revision {
			return EventEnvelopeV2{}, invalid(ErrorInvalidCanonicalData, "governance_revisions")
		}
	}
	payload, err := CanonicalObject(input.Payload)
	if err != nil {
		return EventEnvelopeV2{}, err
	}
	observations := []byte("{}")
	if len(input.Observations) != 0 {
		observations, err = CanonicalObject(input.Observations)
		if err != nil {
			return EventEnvelopeV2{}, err
		}
	}
	input.Payload, input.Observations, input.GovernanceRevisions = nil, nil, governance
	envelope := EventEnvelopeV2{input: input, payload: append([]byte(nil), payload...), observations: append([]byte(nil), observations...)}
	unsigned, err := envelope.canonical(false)
	if err != nil {
		return EventEnvelopeV2{}, err
	}
	envelope.digest = digest(canonicalEventDomain, unsigned)
	return envelope, nil
}

func (e EventEnvelopeV2) EventID() EventID                 { return e.input.EventID }
func (e EventEnvelopeV2) Coordinate() AggregateCoordinate  { return e.input.Coordinate }
func (e EventEnvelopeV2) LogicalVersion() LogicalVersion   { return e.input.AggregateLogicalVersion }
func (e EventEnvelopeV2) OperationID() OperationID         { return e.input.OperationID }
func (e EventEnvelopeV2) OperationVersion() LogicalVersion { return e.input.OperationLogicalVersion }
func (e EventEnvelopeV2) PublicationID() PublicationID     { return e.input.PublicationID }
func (e EventEnvelopeV2) EventIndex() uint16               { return e.input.EventIndex }
func (e EventEnvelopeV2) EventCount() uint16               { return e.input.EventCount }
func (e EventEnvelopeV2) Payload() []byte                  { return append([]byte(nil), e.payload...) }
func (e EventEnvelopeV2) Observations() []byte             { return append([]byte(nil), e.observations...) }
func (e EventEnvelopeV2) CanonicalDigest() Digest          { return e.digest }

type eventEnvelopeWire struct {
	EnvelopeVersion         uint32              `json:"envelope_version"`
	EventID                 string              `json:"event_id"`
	EventType               string              `json:"event_type"`
	Family                  string              `json:"family"`
	EventSchemaVersion      uint32              `json:"event_schema_version"`
	ReducerVersion          uint32              `json:"reducer_version"`
	Coordinate              AggregateCoordinate `json:"coordinate"`
	AggregateLogicalVersion uint64              `json:"aggregate_logical_version"`
	OperationID             string              `json:"operation_id"`
	OperationLogicalVersion uint64              `json:"operation_logical_version"`
	PublicationID           string              `json:"publication_id"`
	CommittedRuntimeEpochID string              `json:"committed_runtime_epoch_id"`
	EventIndex              uint16              `json:"event_index"`
	EventCount              uint16              `json:"event_count"`
	ActorID                 string              `json:"actor_id,omitempty"`
	ProcessID               string              `json:"process_id,omitempty"`
	WorkRevisionID          string              `json:"work_revision_id,omitempty"`
	PolicyRevision          string              `json:"policy_revision,omitempty"`
	GovernanceRevisions     []string            `json:"governance_revisions,omitempty"`
	Observations            []byte              `json:"observations"`
	Payload                 []byte              `json:"payload"`
	CanonicalEventSHA256    string              `json:"canonical_event_sha256,omitempty"`
}

func (e EventEnvelopeV2) canonical(includeDigest bool) ([]byte, error) {
	wire := eventEnvelopeWire{EnvelopeVersion, string(e.input.EventID), e.input.EventType, e.input.Family, e.input.EventSchemaVersion, e.input.ReducerVersion,
		e.input.Coordinate, uint64(e.input.AggregateLogicalVersion), string(e.input.OperationID), uint64(e.input.OperationLogicalVersion), string(e.input.PublicationID),
		string(e.input.CommittedRuntimeEpochID), e.input.EventIndex, e.input.EventCount, string(e.input.ActorID), string(e.input.ProcessID), string(e.input.WorkRevisionID),
		e.input.PolicyRevision, append([]string(nil), e.input.GovernanceRevisions...), append([]byte(nil), e.observations...), append([]byte(nil), e.payload...), ""}
	if includeDigest {
		wire.CanonicalEventSHA256 = e.digest.String()
	}
	return marshalCanonical(wire, MaxEventEnvelopeBytes)
}

func EncodeEventEnvelopeV2(envelope EventEnvelopeV2) ([]byte, error) {
	unsigned, err := envelope.canonical(false)
	if err != nil || digest(canonicalEventDomain, unsigned) != envelope.digest {
		return nil, invalid(ErrorInvalidDigest, "canonical_event_sha256")
	}
	return envelope.canonical(true)
}

func DecodeEventEnvelopeV2(encoded []byte, registry EventRegistryV2) (EventEnvelopeV2, error) {
	var wire eventEnvelopeWire
	if err := decodeStrict(encoded, MaxEventEnvelopeBytes, &wire); err != nil || wire.EnvelopeVersion != EnvelopeVersion {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	want, err := ParseDigest("canonical_event_sha256", wire.CanonicalEventSHA256)
	if err != nil {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	envelope, err := NewEventEnvelopeV2(EventEnvelopeInput{
		EventID: EventID(wire.EventID), EventType: wire.EventType, Family: wire.Family, EventSchemaVersion: wire.EventSchemaVersion,
		ReducerVersion: wire.ReducerVersion, Coordinate: wire.Coordinate, AggregateLogicalVersion: LogicalVersion(wire.AggregateLogicalVersion),
		OperationID: OperationID(wire.OperationID), OperationLogicalVersion: LogicalVersion(wire.OperationLogicalVersion), PublicationID: PublicationID(wire.PublicationID),
		CommittedRuntimeEpochID: RuntimeEpochID(wire.CommittedRuntimeEpochID), EventIndex: wire.EventIndex, EventCount: wire.EventCount,
		ActorID: ActorID(wire.ActorID), ProcessID: ProcessID(wire.ProcessID), WorkRevisionID: WorkRevisionID(wire.WorkRevisionID),
		PolicyRevision: wire.PolicyRevision, GovernanceRevisions: wire.GovernanceRevisions, Observations: wire.Observations, Payload: wire.Payload,
	}, registry)
	if err != nil || envelope.digest != want {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	canonical, _ := EncodeEventEnvelopeV2(envelope)
	if !bytes.Equal(canonical, encoded) {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	return envelope, nil
}

func ValidatePublicationEventsV2(events []EventEnvelopeV2) error {
	if len(events) == 0 || len(events) > MaxEventsPerPublication {
		return invalid(ErrorBudgetExceeded, "publication_events")
	}
	first := events[0].input
	coordinates := make(map[string]bool, len(events))
	eventIDs := make(map[EventID]bool, len(events))
	for index, event := range events {
		if _, err := EncodeEventEnvelopeV2(event); err != nil {
			return err
		}
		input := event.input
		coordinate := input.Coordinate.canonical()
		if input.PublicationID != first.PublicationID || input.OperationID != first.OperationID || input.OperationLogicalVersion != first.OperationLogicalVersion ||
			input.EventCount != uint16(len(events)) || input.EventIndex != uint16(index) || coordinates[coordinate] || eventIDs[input.EventID] {
			return invalid(ErrorInvalidTransition, "publication_event_membership")
		}
		coordinates[coordinate], eventIDs[input.EventID] = true, true
	}
	return nil
}

type AggregateHeadInput struct {
	Coordinate              AggregateCoordinate
	LogicalVersion          LogicalVersion
	LastEventID             EventID
	LastPublicationID       PublicationID
	CommittedRuntimeEpochID RuntimeEpochID
	CanonicalStateDigest    Digest
	AuthoritySummary        []byte
	Tombstone               bool
}

type AggregateHeadV2 struct {
	input            AggregateHeadInput
	authoritySummary []byte
}

func NewAggregateHeadV2(input AggregateHeadInput) (AggregateHeadV2, error) {
	if _, err := NewAggregateCoordinate(input.Coordinate.Kind, input.Coordinate.ID, input.Coordinate.Scope); err != nil {
		return AggregateHeadV2{}, err
	}
	if input.LogicalVersion == 0 || validateIdentity("last_event_id", string(input.LastEventID)) != nil ||
		validateIdentity("last_publication_id", string(input.LastPublicationID)) != nil || validateIdentity("committed_runtime_epoch_id", string(input.CommittedRuntimeEpochID)) != nil || input.CanonicalStateDigest.IsZero() {
		return AggregateHeadV2{}, invalid(ErrorInvalidTransition, "aggregate_head")
	}
	summary, err := CanonicalObject(input.AuthoritySummary)
	if err != nil {
		return AggregateHeadV2{}, err
	}
	input.AuthoritySummary = nil
	return AggregateHeadV2{input: input, authoritySummary: append([]byte(nil), summary...)}, nil
}

func (h AggregateHeadV2) Coordinate() AggregateCoordinate { return h.input.Coordinate }
func (h AggregateHeadV2) LogicalVersion() LogicalVersion  { return h.input.LogicalVersion }
func (h AggregateHeadV2) AuthoritySummary() []byte        { return append([]byte(nil), h.authoritySummary...) }

type aggregateHeadWire struct {
	SchemaVersion           uint32              `json:"schema_version"`
	Coordinate              AggregateCoordinate `json:"coordinate"`
	LogicalVersion          uint64              `json:"logical_version"`
	LastEventID             string              `json:"last_event_id"`
	LastPublicationID       string              `json:"last_publication_id"`
	CommittedRuntimeEpochID string              `json:"committed_runtime_epoch_id"`
	CanonicalStateSHA256    string              `json:"canonical_state_sha256"`
	AuthoritySummary        []byte              `json:"authority_summary"`
	Tombstone               bool                `json:"tombstone"`
}

func EncodeAggregateHeadV2(head AggregateHeadV2) ([]byte, error) {
	rebuilt, err := NewAggregateHeadV2(AggregateHeadInput{head.input.Coordinate, head.input.LogicalVersion, head.input.LastEventID,
		head.input.LastPublicationID, head.input.CommittedRuntimeEpochID, head.input.CanonicalStateDigest, head.authoritySummary, head.input.Tombstone})
	if err != nil {
		return nil, err
	}
	return marshalCanonical(aggregateHeadWire{SchemaVersion, rebuilt.input.Coordinate, uint64(rebuilt.input.LogicalVersion), string(rebuilt.input.LastEventID),
		string(rebuilt.input.LastPublicationID), string(rebuilt.input.CommittedRuntimeEpochID), rebuilt.input.CanonicalStateDigest.String(), rebuilt.authoritySummary, rebuilt.input.Tombstone}, MaxCanonicalPayloadBytes)
}

func DecodeAggregateHeadV2(encoded []byte) (AggregateHeadV2, error) {
	var wire aggregateHeadWire
	if err := decodeStrict(encoded, MaxCanonicalPayloadBytes, &wire); err != nil || wire.SchemaVersion != SchemaVersion {
		return AggregateHeadV2{}, invalid(ErrorJournalCorrupt, "aggregate_head")
	}
	stateDigest, err := ParseDigest("canonical_state_sha256", wire.CanonicalStateSHA256)
	if err != nil {
		return AggregateHeadV2{}, invalid(ErrorJournalCorrupt, "aggregate_head")
	}
	head, err := NewAggregateHeadV2(AggregateHeadInput{wire.Coordinate, LogicalVersion(wire.LogicalVersion), EventID(wire.LastEventID), PublicationID(wire.LastPublicationID), RuntimeEpochID(wire.CommittedRuntimeEpochID), stateDigest, wire.AuthoritySummary, wire.Tombstone})
	if err != nil {
		return AggregateHeadV2{}, invalid(ErrorJournalCorrupt, "aggregate_head")
	}
	canonical, _ := EncodeAggregateHeadV2(head)
	if !bytes.Equal(canonical, encoded) {
		return AggregateHeadV2{}, invalid(ErrorJournalCorrupt, "aggregate_head")
	}
	return head, nil
}

type Reducer func(priorCanonicalState, canonicalEventPayload []byte) ([]byte, error)

type ReducerRegistration struct {
	Family             string
	EventSchemaVersion uint32
	ReducerVersion     uint32
	Reduce             Reducer
}

type ReducerSetV2 struct {
	registry EventRegistryV2
	reducers map[string]Reducer
}

func NewReducerSetV2(registry EventRegistryV2, registrations []ReducerRegistration) (ReducerSetV2, error) {
	if len(registrations) > MaxMembers {
		return ReducerSetV2{}, invalid(ErrorBudgetExceeded, "reducers")
	}
	reducers := make(map[string]Reducer, len(registrations))
	for _, registration := range registrations {
		key := eventTupleKey(registration.Family, registration.EventSchemaVersion, registration.ReducerVersion)
		if registration.Reduce == nil || reducers[key] != nil {
			return ReducerSetV2{}, invalid(ErrorUnknownEventTuple, "reducers")
		}
		if _, err := registry.Resolve(registration.Family, registration.EventSchemaVersion, registration.ReducerVersion); err != nil {
			return ReducerSetV2{}, err
		}
		reducers[key] = registration.Reduce
	}
	return ReducerSetV2{registry: registry, reducers: reducers}, nil
}

func (r ReducerSetV2) Reduce(family string, eventSchemaVersion, reducerVersion uint32, prior, event []byte) ([]byte, Digest, error) {
	if _, err := r.registry.Resolve(family, eventSchemaVersion, reducerVersion); err != nil {
		return nil, Digest{}, err
	}
	call := r.reducers[eventTupleKey(family, eventSchemaVersion, reducerVersion)]
	if call == nil {
		return nil, Digest{}, invalid(ErrorUnknownEventTuple, "reducer")
	}
	canonicalPrior, err := CanonicalObject(prior)
	if err != nil {
		return nil, Digest{}, err
	}
	canonicalEvent, err := CanonicalObject(event)
	if err != nil {
		return nil, Digest{}, err
	}
	first, err := call(append([]byte(nil), canonicalPrior...), append([]byte(nil), canonicalEvent...))
	if err != nil {
		return nil, Digest{}, err
	}
	second, err := call(append([]byte(nil), canonicalPrior...), append([]byte(nil), canonicalEvent...))
	if err != nil {
		return nil, Digest{}, err
	}
	first, err = CanonicalObject(first)
	if err != nil {
		return nil, Digest{}, err
	}
	second, err = CanonicalObject(second)
	if err != nil || !bytes.Equal(first, second) {
		return nil, Digest{}, invalid(ErrorNondeterministic, "reducer")
	}
	return append([]byte(nil), first...), digest("spec-kitty.reducer-state.v2", first), nil
}
