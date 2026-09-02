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
	MaxCheckpointValueBytes  = 4 * 1024 * 1024
	MaxEventEnvelopeBytes    = 1024 * 1024
	MaxMembers               = 256
	MaxEventsPerPublication  = 128
	MaxHeadsPerPublication   = 32
	MaxEffectsPerPublication = 16
	MaxConditionsPerPlan     = 256
	MaxCheckpointsPerPlan    = 32
	MaxQualificationRuns     = 1024
	MaxProductKeyBytes       = 1024
	MaxProductRawBatchBytes  = 6 * 1024 * 1024
	MaxCommitPlanBytes       = 12 * 1024 * 1024
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
	ErrorNotFound             ErrorKind = "NOT_FOUND"
	ErrorRevisionMoved        ErrorKind = "REVISION_MOVED"
	ErrorStaleCursor          ErrorKind = "STALE_CURSOR"
	ErrorMalformed            ErrorKind = "MALFORMED"
	ErrorUnavailable          ErrorKind = "UNAVAILABLE"
	ErrorCanceled             ErrorKind = "CANCELED"
	ErrorAmbiguous            ErrorKind = "AMBIGUOUS"
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
	HostAuthorityID    string
	ProjectID          string
	MissionID          string
	RepositoryID       string
	WorkRevisionID     string
	WorkID             string
	OperationID        string
	PublicationID      string
	RuntimeEpochID     string
	EventID            string
	EffectID           string
	EligibilityShardID string
	CheckpointID       string
	CheckpointSlotID   string
	ActorID            string
	ProcessID          string
	AggregateKind      string
	AggregateID        string
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
func NewEventID(v string) (EventID, error)   { return identity[EventID]("event_id", v) }
func NewEffectID(v string) (EffectID, error) { return identity[EffectID]("effect_id", v) }
func NewEligibilityShardID(v string) (EligibilityShardID, error) {
	return identity[EligibilityShardID]("eligibility_shard_id", v)
}
func NewCheckpointID(v string) (CheckpointID, error) {
	return identity[CheckpointID]("checkpoint_id", v)
}
func NewCheckpointSlotID(v string) (CheckpointSlotID, error) {
	return identity[CheckpointSlotID]("checkpoint_slot_id", v)
}
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

// CompareAggregateCoordinates orders semantic coordinates without exposing or
// reconstructing an adapter's persistence keys.
func CompareAggregateCoordinates(left, right AggregateCoordinate) int {
	return strings.Compare(left.canonical(), right.canonical())
}

// CanonicalAggregateCoordinate returns the stable semantic byte identity used
// by recovery, projection, and checkpoint code. The returned slice is owned by
// the caller and is not a persistence key.
func CanonicalAggregateCoordinate(coordinate AggregateCoordinate) ([]byte, error) {
	validated, err := NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil {
		return nil, err
	}
	encoded, err := marshalCanonical(validated, MaxCanonicalPayloadBytes)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), encoded...), nil
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
		{"operation", 2, 2, "internal/controljournal/application"},
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

const (
	EventRegistryIDV2      = "spec-kitty.authority-event-families.v2"
	EventRegistryVersionV2 = uint32(2)
	PublicationRuleV2      = "one_transition_event_per_aggregate_per_publication"
)

var eventOrderingV2 = [...]string{"gapdb_record_revision", "event_index", "event_id"}

type EventRegistryDescriptorV2 struct {
	canonical []byte
	digest    Digest
}

func (d EventRegistryDescriptorV2) CanonicalBytes() []byte {
	return append([]byte(nil), d.canonical...)
}
func (d EventRegistryDescriptorV2) Digest() Digest { return d.digest }

type EventRegistryV2 struct {
	tuples     map[string]EventTuple
	descriptor EventRegistryDescriptorV2
}

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
	sorted := append([]EventTuple(nil), tuples...)
	sort.Slice(sorted, func(i, j int) bool {
		return eventTupleKey(sorted[i].Family, sorted[i].EventSchemaVersion, sorted[i].ReducerVersion) <
			eventTupleKey(sorted[j].Family, sorted[j].EventSchemaVersion, sorted[j].ReducerVersion)
	})
	canonical, err := marshalCanonical(struct {
		RegistryID      string       `json:"registry_id"`
		RegistryVersion uint32       `json:"registry_version"`
		PublicationRule string       `json:"publication_rule"`
		Ordering        []string     `json:"ordering"`
		Families        []EventTuple `json:"families"`
	}{EventRegistryIDV2, EventRegistryVersionV2, PublicationRuleV2, append([]string(nil), eventOrderingV2[:]...), sorted}, MaxCanonicalPayloadBytes)
	if err != nil {
		return EventRegistryV2{}, err
	}
	return EventRegistryV2{tuples: got, descriptor: EventRegistryDescriptorV2{
		canonical: canonical,
		digest:    digest("spec-kitty.event-registry-descriptor.v2", canonical),
	}}, nil
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

func (r EventRegistryV2) Descriptor() (EventRegistryDescriptorV2, error) {
	if len(r.tuples) != len(ExpectedEventTuples()) || len(r.descriptor.canonical) == 0 || r.descriptor.digest.IsZero() ||
		digest("spec-kitty.event-registry-descriptor.v2", r.descriptor.canonical) != r.descriptor.digest {
		return EventRegistryDescriptorV2{}, invalid(ErrorUnknownEventTuple, "event_registry_descriptor")
	}
	return EventRegistryDescriptorV2{canonical: append([]byte(nil), r.descriptor.canonical...), digest: r.descriptor.digest}, nil
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
		validateIdentity("operation_id", string(input.OperationID)) != nil ||
		validateIdentity("runtime_epoch_id", string(input.CommittedRuntimeEpochID)) != nil || input.AggregateLogicalVersion == 0 || input.OperationLogicalVersion == 0 {
		return EventEnvelopeV2{}, invalid(ErrorInvalidIdentity, "event_envelope")
	}
	if _, err := DecodePublicationID(string(input.PublicationID)); err != nil {
		return EventEnvelopeV2{}, err
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
func (e EventEnvelopeV2) EnvelopeVersion() uint32          { return EnvelopeVersion }
func (e EventEnvelopeV2) EventType() string                { return e.input.EventType }
func (e EventEnvelopeV2) Family() string                   { return e.input.Family }
func (e EventEnvelopeV2) EventSchemaVersion() uint32       { return e.input.EventSchemaVersion }
func (e EventEnvelopeV2) ReducerVersion() uint32           { return e.input.ReducerVersion }
func (e EventEnvelopeV2) Coordinate() AggregateCoordinate  { return e.input.Coordinate }
func (e EventEnvelopeV2) LogicalVersion() LogicalVersion   { return e.input.AggregateLogicalVersion }
func (e EventEnvelopeV2) OperationID() OperationID         { return e.input.OperationID }
func (e EventEnvelopeV2) OperationVersion() LogicalVersion { return e.input.OperationLogicalVersion }
func (e EventEnvelopeV2) PublicationID() PublicationID     { return e.input.PublicationID }
func (e EventEnvelopeV2) EventIndex() uint16               { return e.input.EventIndex }
func (e EventEnvelopeV2) EventCount() uint16               { return e.input.EventCount }
func (e EventEnvelopeV2) CommittedRuntimeEpochID() RuntimeEpochID {
	return e.input.CommittedRuntimeEpochID
}
func (e EventEnvelopeV2) ActorID() ActorID               { return e.input.ActorID }
func (e EventEnvelopeV2) ProcessID() ProcessID           { return e.input.ProcessID }
func (e EventEnvelopeV2) WorkRevisionID() WorkRevisionID { return e.input.WorkRevisionID }
func (e EventEnvelopeV2) PolicyRevision() string         { return e.input.PolicyRevision }
func (e EventEnvelopeV2) GovernanceRevisions() []string {
	return append([]string(nil), e.input.GovernanceRevisions...)
}
func (e EventEnvelopeV2) Payload() []byte         { return append([]byte(nil), e.payload...) }
func (e EventEnvelopeV2) Observations() []byte    { return append([]byte(nil), e.observations...) }
func (e EventEnvelopeV2) CanonicalDigest() Digest { return e.digest }

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
	if input.Coordinate.Kind == AggregateKind("operation") && input.Coordinate.Scope != (Scope{Kind: ScopeHost}) {
		return AggregateHeadV2{}, invalid(ErrorInvalidScope, "operation_head")
	}
	if input.LogicalVersion == 0 || validateIdentity("last_event_id", string(input.LastEventID)) != nil ||
		validateIdentity("committed_runtime_epoch_id", string(input.CommittedRuntimeEpochID)) != nil || input.CanonicalStateDigest.IsZero() {
		return AggregateHeadV2{}, invalid(ErrorInvalidTransition, "aggregate_head")
	}
	if _, err := DecodePublicationID(string(input.LastPublicationID)); err != nil {
		return AggregateHeadV2{}, err
	}
	summary, err := CanonicalObject(input.AuthoritySummary)
	if err != nil {
		return AggregateHeadV2{}, err
	}
	input.AuthoritySummary = nil
	return AggregateHeadV2{input: input, authoritySummary: append([]byte(nil), summary...)}, nil
}

func (h AggregateHeadV2) Coordinate() AggregateCoordinate  { return h.input.Coordinate }
func (h AggregateHeadV2) LogicalVersion() LogicalVersion   { return h.input.LogicalVersion }
func (h AggregateHeadV2) SchemaVersion() uint32            { return SchemaVersion }
func (h AggregateHeadV2) LastEventID() EventID             { return h.input.LastEventID }
func (h AggregateHeadV2) LastPublicationID() PublicationID { return h.input.LastPublicationID }
func (h AggregateHeadV2) CommittedRuntimeEpochID() RuntimeEpochID {
	return h.input.CommittedRuntimeEpochID
}
func (h AggregateHeadV2) CanonicalStateDigest() Digest { return h.input.CanonicalStateDigest }
func (h AggregateHeadV2) AuthoritySummary() []byte     { return append([]byte(nil), h.authoritySummary...) }
func (h AggregateHeadV2) Tombstone() bool              { return h.input.Tombstone }

func ValidateOperationPublicationHeadV2(state OperationStateV2, event EventEnvelopeV2, head AggregateHeadV2) error {
	publicationID := state.lastPublicationID
	if _, err := DecodePublicationID(string(publicationID)); err != nil {
		return invalid(ErrorInvalidTransition, "operation_head_context")
	}
	coordinate := head.input.Coordinate
	eventInput := event.input
	if coordinate.Kind != AggregateKind("operation") || coordinate.Scope != (Scope{Kind: ScopeHost}) ||
		coordinate.ID != AggregateID(state.operationID) || eventInput.Family != "operation" || eventInput.Coordinate != coordinate ||
		eventInput.OperationID != state.operationID || eventInput.OperationLogicalVersion != state.logicalVersion ||
		eventInput.AggregateLogicalVersion != state.logicalVersion || head.input.LogicalVersion != state.logicalVersion ||
		head.input.LastEventID != eventInput.EventID || eventInput.PublicationID != publicationID || head.input.LastPublicationID != publicationID {
		return invalid(ErrorInvalidTransition, "operation_head_context")
	}
	found := false
	for _, eventID := range state.eventIDs {
		if eventID == eventInput.EventID {
			found = true
			break
		}
	}
	if !found {
		return invalid(ErrorInvalidTransition, "operation_head_context")
	}
	return nil
}

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
	Family               string
	EventSchemaVersion   uint32
	ReducerVersion       uint32
	Owner                string
	ImplementationDigest Digest
	Reduce               Reducer
}

type ReducerRegistryDescriptorV2 struct {
	canonical []byte
	digest    Digest
}

func (d ReducerRegistryDescriptorV2) CanonicalBytes() []byte {
	return append([]byte(nil), d.canonical...)
}
func (d ReducerRegistryDescriptorV2) Digest() Digest { return d.digest }

// ReducerSetV2 is immutable after construction. Runtime application is
// stateless; repeated-call determinism checks belong to QualifyDeterminismV2.
type ReducerSetV2 struct {
	registry   EventRegistryV2
	reducers   map[string]Reducer
	descriptor ReducerRegistryDescriptorV2
}

func NewReducerSetV2(registry EventRegistryV2, registrations []ReducerRegistration) (ReducerSetV2, error) {
	if len(registrations) > MaxMembers {
		return ReducerSetV2{}, invalid(ErrorBudgetExceeded, "reducers")
	}
	if len(registrations) != len(ExpectedEventTuples()) {
		return ReducerSetV2{}, invalid(ErrorUnknownEventTuple, "reducers")
	}
	reducers := make(map[string]Reducer, len(registrations))
	descriptors := make([]struct {
		Family               string `json:"family"`
		EventSchemaVersion   uint32 `json:"event_schema_version"`
		ReducerVersion       uint32 `json:"reducer_version"`
		Owner                string `json:"owner"`
		ImplementationSHA256 string `json:"implementation_sha256"`
	}, 0, len(registrations))
	for _, registration := range registrations {
		key := eventTupleKey(registration.Family, registration.EventSchemaVersion, registration.ReducerVersion)
		if registration.Reduce == nil || reducers[key] != nil || registration.ImplementationDigest.IsZero() {
			return ReducerSetV2{}, invalid(ErrorUnknownEventTuple, "reducers")
		}
		tuple, err := registry.Resolve(registration.Family, registration.EventSchemaVersion, registration.ReducerVersion)
		if err != nil || tuple.Owner != registration.Owner {
			return ReducerSetV2{}, invalid(ErrorUnknownEventTuple, "reducer_owner")
		}
		reducers[key] = registration.Reduce
		descriptors = append(descriptors, struct {
			Family               string `json:"family"`
			EventSchemaVersion   uint32 `json:"event_schema_version"`
			ReducerVersion       uint32 `json:"reducer_version"`
			Owner                string `json:"owner"`
			ImplementationSHA256 string `json:"implementation_sha256"`
		}{registration.Family, registration.EventSchemaVersion, registration.ReducerVersion, registration.Owner, registration.ImplementationDigest.String()})
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return eventTupleKey(descriptors[i].Family, descriptors[i].EventSchemaVersion, descriptors[i].ReducerVersion) <
			eventTupleKey(descriptors[j].Family, descriptors[j].EventSchemaVersion, descriptors[j].ReducerVersion)
	})
	eventDescriptor, err := registry.Descriptor()
	if err != nil {
		return ReducerSetV2{}, err
	}
	canonical, err := marshalCanonical(struct {
		EventRegistrySHA256 string `json:"event_registry_sha256"`
		Reducers            any    `json:"reducers"`
	}{eventDescriptor.Digest().String(), descriptors}, MaxCanonicalPayloadBytes)
	if err != nil {
		return ReducerSetV2{}, err
	}
	return ReducerSetV2{registry: registry, reducers: reducers, descriptor: ReducerRegistryDescriptorV2{
		canonical: canonical, digest: digest("spec-kitty.reducer-registry-descriptor.v2", canonical),
	}}, nil
}

func (r ReducerSetV2) Apply(family string, eventSchemaVersion, reducerVersion uint32, prior, event []byte) ([]byte, Digest, error) {
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
	output, err := call(append([]byte(nil), canonicalPrior...), append([]byte(nil), canonicalEvent...))
	if err != nil {
		return nil, Digest{}, err
	}
	output, err = CanonicalObject(output)
	if err != nil {
		return nil, Digest{}, err
	}
	outputDigest := digest("spec-kitty.reducer-state.v2", output)
	return append([]byte(nil), output...), outputDigest, nil
}

// Reduce is retained as the stateless runtime spelling used by existing
// consumers. It delegates to Apply and retains no observations.
func (r ReducerSetV2) Reduce(family string, eventSchemaVersion, reducerVersion uint32, prior, event []byte) ([]byte, Digest, error) {
	return r.Apply(family, eventSchemaVersion, reducerVersion, prior, event)
}

func (r ReducerSetV2) Descriptor() (ReducerRegistryDescriptorV2, error) {
	if len(r.reducers) != len(ExpectedEventTuples()) || len(r.descriptor.canonical) == 0 || r.descriptor.digest.IsZero() ||
		digest("spec-kitty.reducer-registry-descriptor.v2", r.descriptor.canonical) != r.descriptor.digest {
		return ReducerRegistryDescriptorV2{}, invalid(ErrorUnknownEventTuple, "reducer_registry_descriptor")
	}
	return ReducerRegistryDescriptorV2{canonical: append([]byte(nil), r.descriptor.canonical...), digest: r.descriptor.digest}, nil
}

// QualifyDeterminismV2 detects unstable implementations in a bounded,
// deliberately separate qualification call. It never changes runtime state.
func (r ReducerSetV2) QualifyDeterminismV2(family string, eventSchemaVersion, reducerVersion uint32, prior, event []byte, runs int) error {
	if runs < 2 || runs > MaxQualificationRuns {
		return invalid(ErrorBudgetExceeded, "qualification_runs")
	}
	want, wantDigest, err := r.Apply(family, eventSchemaVersion, reducerVersion, prior, event)
	if err != nil {
		return err
	}
	for index := 1; index < runs; index++ {
		got, gotDigest, applyErr := r.Apply(family, eventSchemaVersion, reducerVersion, prior, event)
		if applyErr != nil {
			return applyErr
		}
		if !bytes.Equal(want, got) || wantDigest != gotDigest {
			return invalid(ErrorNondeterministic, "reducer")
		}
	}
	return nil
}
