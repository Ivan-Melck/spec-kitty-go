// Package v2 defines the provider-independent authority journal contract.
// It deliberately contains no persistence, clock, transport, or effect ports.
package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	publicationWitnessDomain = "spec-kitty.publication-witness.v2"
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
	if len(value) == 0 || len(value) > MaxIdentityBytes {
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

const publicationWitnessSchemaVersionV2 = uint32(1)

// PublicationWitnessV2 is the immutable durable proof needed to reconstruct
// an acknowledged publication after process loss. Its record revision is
// provider metadata and is deliberately absent from this value.
type PublicationWitnessV2 struct {
	schemaVersion   uint32
	publicationID   PublicationID
	operationID     OperationID
	logicalVersion  LogicalVersion
	planDigest      Digest
	recordSetDigest Digest
	recordCount     uint32
	witnessDigest   Digest
}

func (w PublicationWitnessV2) SchemaVersion() uint32                   { return w.schemaVersion }
func (w PublicationWitnessV2) PublicationID() PublicationID            { return w.publicationID }
func (w PublicationWitnessV2) OperationID() OperationID                { return w.operationID }
func (w PublicationWitnessV2) OperationLogicalVersion() LogicalVersion { return w.logicalVersion }
func (w PublicationWitnessV2) PlanDigest() Digest                      { return w.planDigest }
func (w PublicationWitnessV2) AuthorityRecordSetDigest() Digest        { return w.recordSetDigest }
func (w PublicationWitnessV2) AuthorityRecordCount() uint32            { return w.recordCount }
func (w PublicationWitnessV2) WitnessDigest() Digest                   { return w.witnessDigest }

func newPublicationWitnessV2(publicationID PublicationID, operationID OperationID, logicalVersion LogicalVersion, planDigest, recordSetDigest Digest, recordCount int) (PublicationWitnessV2, error) {
	if recordCount <= 0 || recordCount >= MaxMembers {
		return PublicationWitnessV2{}, invalid(ErrorBudgetExceeded, "authority_record_count")
	}
	witness := PublicationWitnessV2{schemaVersion: publicationWitnessSchemaVersionV2, publicationID: publicationID,
		operationID: operationID, logicalVersion: logicalVersion, planDigest: planDigest,
		recordSetDigest: recordSetDigest, recordCount: uint32(recordCount)}
	digest, err := publicationWitnessDigest(witness)
	if err != nil {
		return PublicationWitnessV2{}, err
	}
	witness.witnessDigest = digest
	return witness, nil
}

func publicationWitnessDigest(witness PublicationWitnessV2) (Digest, error) {
	if witness.schemaVersion != publicationWitnessSchemaVersionV2 ||
		validateIdentity("operation_id", string(witness.operationID)) != nil || witness.logicalVersion == 0 ||
		witness.planDigest.IsZero() || witness.recordSetDigest.IsZero() || witness.recordCount == 0 || witness.recordCount >= MaxMembers {
		return Digest{}, invalid(ErrorJournalCorrupt, "publication_witness")
	}
	if _, err := DecodePublicationID(string(witness.publicationID)); err != nil {
		return Digest{}, invalid(ErrorJournalCorrupt, "publication_witness")
	}
	canonical, err := marshalCanonical(struct {
		SchemaVersion   uint32 `json:"schema_version"`
		PublicationID   string `json:"publication_id"`
		OperationID     string `json:"operation_id"`
		LogicalVersion  uint64 `json:"operation_logical_version"`
		PlanSHA256      string `json:"plan_sha256"`
		RecordSetSHA256 string `json:"authority_record_set_sha256"`
		RecordCount     uint32 `json:"authority_record_count"`
	}{witness.schemaVersion, string(witness.publicationID), string(witness.operationID), uint64(witness.logicalVersion),
		witness.planDigest.String(), witness.recordSetDigest.String(), witness.recordCount}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest(publicationWitnessDomain, canonical), nil
}

// ReducerStateDigestV2 is the single canonical state commitment shared by
// live reduction, checkpoints, and restore.
func ReducerStateDigestV2(canonical []byte) (Digest, error) {
	normalized, err := CanonicalObject(canonical)
	if err != nil || !bytes.Equal(normalized, canonical) {
		return Digest{}, invalid(ErrorInvalidCanonicalData, "reducer_state")
	}
	return reducerStateDigestCanonicalV2(normalized), nil
}

func reducerStateDigestCanonicalV2(canonical []byte) Digest {
	return digest("spec-kitty.reducer-state.v2", canonical)
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
func CompareAggregateCoordinates(left, right AggregateCoordinate) (int, error) {
	leftCanonical, err := CanonicalAggregateCoordinate(left)
	if err != nil {
		return 0, err
	}
	rightCanonical, err := CanonicalAggregateCoordinate(right)
	if err != nil {
		return 0, err
	}
	return bytes.Compare(leftCanonical, rightCanonical), nil
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
	input         EventEnvelopeInput
	payload       []byte
	observations  []byte
	digest        Digest
	canonicalSize int
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
	return newEventEnvelopeV2(input, registry, false)
}

func newEventEnvelopeV2(input EventEnvelopeInput, registry EventRegistryV2, canonicalInputsSealed bool) (EventEnvelopeV2, error) {
	return newEventEnvelopeWithTrustedDigestV2(input, registry, canonicalInputsSealed, nil)
}

func newEventEnvelopeWithTrustedDigestV2(input EventEnvelopeInput, registry EventRegistryV2, canonicalInputsSealed bool, trustedDigest *Digest) (EventEnvelopeV2, error) {
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
	governance := input.GovernanceRevisions
	if !canonicalInputsSealed {
		governance = append([]string(nil), governance...)
	}
	if !sort.StringsAreSorted(governance) {
		return EventEnvelopeV2{}, invalid(ErrorInvalidCanonicalData, "governance_revisions")
	}
	for index, revision := range governance {
		if validateIdentity("governance_revision", revision) != nil || index > 0 && governance[index-1] == revision {
			return EventEnvelopeV2{}, invalid(ErrorInvalidCanonicalData, "governance_revisions")
		}
	}
	payload := input.Payload
	observations := []byte("{}")
	if !canonicalInputsSealed {
		var err error
		payload, err = CanonicalObject(input.Payload)
		if err != nil {
			return EventEnvelopeV2{}, err
		}
		if len(input.Observations) != 0 {
			observations, err = CanonicalObject(input.Observations)
			if err != nil {
				return EventEnvelopeV2{}, err
			}
		}
	} else if len(input.Observations) != 0 {
		observations = input.Observations
	}
	input.Payload, input.Observations, input.GovernanceRevisions = nil, nil, governance
	envelope := EventEnvelopeV2{input: input, payload: payload, observations: observations}
	if trustedDigest != nil {
		envelope.digest = *trustedDigest
		return envelope, nil
	}
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

// CanonicalSizeV2 is nonzero only when the envelope was decoded from and
// matched exact durable bytes. It lets recovery carry the already-validated
// byte budget into pure playback without canonicalizing the envelope again.
func (e EventEnvelopeV2) CanonicalSizeV2() int { return e.canonicalSize }

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
	signed, err := envelope.canonical(true)
	if err != nil {
		return nil, err
	}
	return canonicalDurableObject(signed, MaxEventEnvelopeBytes)
}

func DecodeEventEnvelopeV2(encoded []byte, registry EventRegistryV2) (EventEnvelopeV2, error) {
	return decodeEventEnvelopeV2(encoded, registry, false)
}

// DecodeEventEnvelopeRecordV2 decodes an event from canonical bytes already
// sealed by RecoveryRecordV2. Schema, registry membership, semantic identity,
// and the event digest remain mandatory; only duplicate JSON normalization is
// omitted.
func DecodeEventEnvelopeRecordV2(record RecoveryRecordV2, registry EventRegistryV2) (EventEnvelopeV2, error) {
	if record.address.kind != AddressJournalEvent || record.expiresAt != "" {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	return decodeEventEnvelopeV2(record.canonical, registry, true)
}

type canonicalObjectMemberV2 struct {
	name  []byte
	value []byte
}

type canonicalMemberRuleV2 struct {
	name     string
	required bool
}

var eventEnvelopeMemberRulesV2 = [...]canonicalMemberRuleV2{
	{"aggregate_logical_version", true}, {"actor_id", false}, {"canonical_event_sha256", true},
	{"committed_runtime_epoch_id", true}, {"coordinate", true}, {"envelope_version", true},
	{"event_count", true}, {"event_id", true}, {"event_index", true}, {"event_schema_version", true},
	{"event_type", true}, {"family", true}, {"governance_revisions", false}, {"observations", true},
	{"operation_id", true}, {"operation_logical_version", true}, {"payload", true}, {"policy_revision", false},
	{"process_id", false}, {"publication_id", true}, {"reducer_version", true}, {"work_revision_id", false},
}

var coordinateMemberRulesV2 = [...]canonicalMemberRuleV2{{"id", true}, {"kind", true}, {"scope", true}}
var scopeMemberRulesV2 = [...]canonicalMemberRuleV2{
	{"kind", true}, {"mission_id", false}, {"project_id", false}, {"repository_id", false},
	{"work_id", false}, {"work_revision_id", false},
}

// inspectRecoveryEventCanonicalV2 recognizes the exact closed event object
// already sealed by RecoveryRecordV2. It does not normalize or repair input:
// unknown, duplicate, escaped-key, reordered, or malformed members fail.
// The returned digest is the established declaration-order unsigned preimage,
// assembled from the exact recursively canonical value bytes without a second
// reflection marshal.
func inspectRecoveryEventCanonicalV2(encoded []byte) (eventEnvelopeWire, Digest, bool) {
	var wire eventEnvelopeWire
	if len(encoded) == 0 || len(encoded) > MaxEventEnvelopeBytes {
		return wire, Digest{}, false
	}
	var eventMembers [len(eventEnvelopeMemberRulesV2)]canonicalObjectMemberV2
	eventCount, ok := scanCanonicalObjectMembersV2(encoded, eventMembers[:])
	if !ok || !validateCanonicalMemberSetV2(eventMembers[:eventCount], eventEnvelopeMemberRulesV2[:]) {
		return wire, Digest{}, false
	}
	coordinateBytes, _ := canonicalMemberValueV2(eventMembers[:eventCount], "coordinate")
	var coordinateMembers [len(coordinateMemberRulesV2)]canonicalObjectMemberV2
	coordinateCount, ok := scanCanonicalObjectMembersV2(coordinateBytes, coordinateMembers[:])
	if !ok || !validateCanonicalMemberSetV2(coordinateMembers[:coordinateCount], coordinateMemberRulesV2[:]) {
		return wire, Digest{}, false
	}
	scopeBytes, _ := canonicalMemberValueV2(coordinateMembers[:coordinateCount], "scope")
	var scopeMembers [len(scopeMemberRulesV2)]canonicalObjectMemberV2
	scopeCount, ok := scanCanonicalObjectMembersV2(scopeBytes, scopeMembers[:])
	if !ok || !validateCanonicalMemberSetV2(scopeMembers[:scopeCount], scopeMemberRulesV2[:]) {
		return wire, Digest{}, false
	}
	// The trusted recovery path must accept only bytes that the established
	// omitempty encoder could mint. A present empty optional member is a new
	// signed preimage even though encoding/json decodes it to the same Go zero
	// value as an omitted member.
	for _, name := range []string{"actor_id", "process_id", "work_revision_id", "policy_revision"} {
		if value, exists := canonicalMemberValueV2(eventMembers[:eventCount], name); exists && bytes.Equal(value, []byte(`""`)) {
			return wire, Digest{}, false
		}
	}
	if value, exists := canonicalMemberValueV2(eventMembers[:eventCount], "governance_revisions"); exists && bytes.Equal(value, []byte(`[]`)) {
		return wire, Digest{}, false
	}
	for _, name := range []string{"project_id", "mission_id", "repository_id", "work_revision_id", "work_id"} {
		if value, exists := canonicalMemberValueV2(scopeMembers[:scopeCount], name); exists && bytes.Equal(value, []byte(`""`)) {
			return wire, Digest{}, false
		}
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte(canonicalEventDomain))
	_, _ = hasher.Write([]byte{0, '{'})
	first := true
	writeName := func(name string) {
		if !first {
			_, _ = hasher.Write([]byte{','})
		}
		first = false
		_, _ = hasher.Write([]byte{'"'})
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{'"', ':'})
	}
	writeMember := func(name string, value []byte) {
		writeName(name)
		_, _ = hasher.Write(value)
	}
	for _, name := range []string{"envelope_version", "event_id", "event_type", "family", "event_schema_version", "reducer_version"} {
		value, _ := canonicalMemberValueV2(eventMembers[:eventCount], name)
		writeMember(name, value)
	}
	writeName("coordinate")
	_, _ = hasher.Write([]byte(`{"kind":`))
	value, _ := canonicalMemberValueV2(coordinateMembers[:coordinateCount], "kind")
	_, _ = hasher.Write(value)
	_, _ = hasher.Write([]byte(`,"id":`))
	value, _ = canonicalMemberValueV2(coordinateMembers[:coordinateCount], "id")
	_, _ = hasher.Write(value)
	_, _ = hasher.Write([]byte(`,"scope":{"kind":`))
	value, _ = canonicalMemberValueV2(scopeMembers[:scopeCount], "kind")
	_, _ = hasher.Write(value)
	for _, name := range []string{"project_id", "mission_id", "repository_id", "work_revision_id", "work_id"} {
		if value, exists := canonicalMemberValueV2(scopeMembers[:scopeCount], name); exists {
			_, _ = hasher.Write([]byte(`,"`))
			_, _ = hasher.Write([]byte(name))
			_, _ = hasher.Write([]byte(`":`))
			_, _ = hasher.Write(value)
		}
	}
	_, _ = hasher.Write([]byte(`}}`))
	for _, rule := range []canonicalMemberRuleV2{
		{"aggregate_logical_version", true}, {"operation_id", true}, {"operation_logical_version", true},
		{"publication_id", true}, {"committed_runtime_epoch_id", true}, {"event_index", true}, {"event_count", true},
		{"actor_id", false}, {"process_id", false}, {"work_revision_id", false}, {"policy_revision", false},
		{"governance_revisions", false}, {"observations", true}, {"payload", true},
	} {
		if value, exists := canonicalMemberValueV2(eventMembers[:eventCount], rule.name); exists {
			writeMember(rule.name, value)
		}
	}
	_, _ = hasher.Write([]byte{'}'})
	var result Digest
	copy(result[:], hasher.Sum(nil))

	// RecoveryRecordV2 has already performed complete canonical JSON
	// validation. Decode the closed wire directly from its exact member spans;
	// this avoids encoding/json's second whole-object syntax pass and reflection
	// walk while retaining every schema, semantic, and digest check below.
	var decoded bool
	wire.EnvelopeVersion, decoded = canonicalUint32MemberV2(eventMembers[:eventCount], "envelope_version")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.EventID, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "event_id")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.EventType, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "event_type")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Family, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "family")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.EventSchemaVersion, decoded = canonicalUint32MemberV2(eventMembers[:eventCount], "event_schema_version")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.ReducerVersion, decoded = canonicalUint32MemberV2(eventMembers[:eventCount], "reducer_version")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Coordinate.Kind, decoded = canonicalAggregateKindMemberV2(coordinateMembers[:coordinateCount], "kind")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Coordinate.ID, decoded = canonicalAggregateIDMemberV2(coordinateMembers[:coordinateCount], "id")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Coordinate.Scope.Kind, decoded = canonicalScopeKindMemberV2(scopeMembers[:scopeCount], "kind")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	projectID, projectOK := canonicalOptionalStringMemberV2(scopeMembers[:scopeCount], "project_id")
	missionID, missionOK := canonicalOptionalStringMemberV2(scopeMembers[:scopeCount], "mission_id")
	repositoryID, repositoryOK := canonicalOptionalStringMemberV2(scopeMembers[:scopeCount], "repository_id")
	scopeRevisionID, scopeRevisionOK := canonicalOptionalStringMemberV2(scopeMembers[:scopeCount], "work_revision_id")
	workID, workOK := canonicalOptionalStringMemberV2(scopeMembers[:scopeCount], "work_id")
	if !projectOK || !missionOK || !repositoryOK || !scopeRevisionOK || !workOK {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Coordinate.Scope.ProjectID = ProjectID(projectID)
	wire.Coordinate.Scope.MissionID = MissionID(missionID)
	wire.Coordinate.Scope.RepositoryID = RepositoryID(repositoryID)
	wire.Coordinate.Scope.WorkRevisionID = WorkRevisionID(scopeRevisionID)
	wire.Coordinate.Scope.WorkID = WorkID(workID)
	wire.AggregateLogicalVersion, decoded = canonicalUint64MemberV2(eventMembers[:eventCount], "aggregate_logical_version")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.OperationID, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "operation_id")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.OperationLogicalVersion, decoded = canonicalUint64MemberV2(eventMembers[:eventCount], "operation_logical_version")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.PublicationID, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "publication_id")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.CommittedRuntimeEpochID, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "committed_runtime_epoch_id")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	index, indexOK := canonicalUint64MemberV2(eventMembers[:eventCount], "event_index")
	count, countOK := canonicalUint64MemberV2(eventMembers[:eventCount], "event_count")
	if !indexOK || !countOK || index > uint64(^uint16(0)) || count > uint64(^uint16(0)) {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.EventIndex, wire.EventCount = uint16(index), uint16(count)
	wire.ActorID, projectOK = canonicalOptionalStringMemberV2(eventMembers[:eventCount], "actor_id")
	wire.ProcessID, missionOK = canonicalOptionalStringMemberV2(eventMembers[:eventCount], "process_id")
	wire.WorkRevisionID, repositoryOK = canonicalOptionalStringMemberV2(eventMembers[:eventCount], "work_revision_id")
	wire.PolicyRevision, scopeRevisionOK = canonicalOptionalStringMemberV2(eventMembers[:eventCount], "policy_revision")
	if !projectOK || !missionOK || !repositoryOK || !scopeRevisionOK {
		return eventEnvelopeWire{}, Digest{}, false
	}
	if governance, exists := canonicalMemberValueV2(eventMembers[:eventCount], "governance_revisions"); exists &&
		(bytes.Equal(governance, []byte(`null`)) || json.Unmarshal(governance, &wire.GovernanceRevisions) != nil || len(wire.GovernanceRevisions) == 0) {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Observations, decoded = canonicalBase64MemberV2(eventMembers[:eventCount], "observations")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.Payload, decoded = canonicalBase64MemberV2(eventMembers[:eventCount], "payload")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	wire.CanonicalEventSHA256, decoded = canonicalStringMemberV2(eventMembers[:eventCount], "canonical_event_sha256")
	if !decoded {
		return eventEnvelopeWire{}, Digest{}, false
	}
	return wire, result, true
}

func canonicalStringMemberV2(members []canonicalObjectMemberV2, name string) (string, bool) {
	value, exists := canonicalMemberValueV2(members, name)
	if !exists || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' || bytes.IndexByte(value[1:len(value)-1], '\\') >= 0 {
		return "", false
	}
	return string(value[1 : len(value)-1]), true
}

func canonicalOptionalStringMemberV2(members []canonicalObjectMemberV2, name string) (string, bool) {
	if _, exists := canonicalMemberValueV2(members, name); !exists {
		return "", true
	}
	return canonicalStringMemberV2(members, name)
}

func canonicalUint64MemberV2(members []canonicalObjectMemberV2, name string) (uint64, bool) {
	value, exists := canonicalMemberValueV2(members, name)
	if !exists || len(value) == 0 {
		return 0, false
	}
	result, err := strconv.ParseUint(string(value), 10, 64)
	return result, err == nil
}

func canonicalUint32MemberV2(members []canonicalObjectMemberV2, name string) (uint32, bool) {
	value, ok := canonicalUint64MemberV2(members, name)
	return uint32(value), ok && value <= uint64(^uint32(0))
}

func canonicalAggregateKindMemberV2(members []canonicalObjectMemberV2, name string) (AggregateKind, bool) {
	value, ok := canonicalStringMemberV2(members, name)
	return AggregateKind(value), ok
}

func canonicalAggregateIDMemberV2(members []canonicalObjectMemberV2, name string) (AggregateID, bool) {
	value, ok := canonicalStringMemberV2(members, name)
	return AggregateID(value), ok
}

func canonicalScopeKindMemberV2(members []canonicalObjectMemberV2, name string) (ScopeKind, bool) {
	value, ok := canonicalStringMemberV2(members, name)
	return ScopeKind(value), ok
}

func canonicalBase64MemberV2(members []canonicalObjectMemberV2, name string) ([]byte, bool) {
	value, exists := canonicalMemberValueV2(members, name)
	if !exists || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return nil, false
	}
	value = value[1 : len(value)-1]
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(value)))
	count, err := base64.StdEncoding.Decode(decoded, value)
	if err != nil {
		return nil, false
	}
	return decoded[:count:count], true
}

func validateCanonicalMemberSetV2(members []canonicalObjectMemberV2, rules []canonicalMemberRuleV2) bool {
	for _, member := range members {
		known := false
		for _, rule := range rules {
			if bytes.Equal(member.name, []byte(rule.name)) {
				known = true
				break
			}
		}
		if !known {
			return false
		}
	}
	for _, rule := range rules {
		_, exists := canonicalMemberValueV2(members, rule.name)
		if rule.required && !exists {
			return false
		}
	}
	return true
}

func canonicalMemberValueV2(members []canonicalObjectMemberV2, name string) ([]byte, bool) {
	for _, member := range members {
		if bytes.Equal(member.name, []byte(name)) {
			return member.value, true
		}
	}
	return nil, false
}

func scanCanonicalObjectMembersV2(encoded []byte, destination []canonicalObjectMemberV2) (int, bool) {
	if len(encoded) < 2 || encoded[0] != '{' || encoded[len(encoded)-1] != '}' {
		return 0, false
	}
	index, count := 1, 0
	var prior []byte
	if index == len(encoded)-1 {
		return 0, true
	}
	for index < len(encoded)-1 {
		if count == len(destination) || encoded[index] != '"' {
			return 0, false
		}
		index++
		nameStart := index
		for index < len(encoded)-1 && encoded[index] != '"' {
			if encoded[index] == '\\' || encoded[index] < 0x20 {
				return 0, false
			}
			index++
		}
		if index == len(encoded)-1 {
			return 0, false
		}
		name := encoded[nameStart:index]
		if len(name) == 0 || prior != nil && bytes.Compare(prior, name) >= 0 {
			return 0, false
		}
		prior = name
		index++
		if index >= len(encoded)-1 || encoded[index] != ':' {
			return 0, false
		}
		index++
		valueStart := index
		valueEnd, delimiter, ok := scanCanonicalMemberValueV2(encoded, index)
		if !ok || valueEnd == valueStart {
			return 0, false
		}
		destination[count] = canonicalObjectMemberV2{name: name, value: encoded[valueStart:valueEnd]}
		count++
		index = valueEnd + 1
		if delimiter == '}' {
			return count, index == len(encoded)
		}
	}
	return 0, false
}

func scanCanonicalMemberValueV2(encoded []byte, start int) (int, byte, bool) {
	var stack [maxCanonicalFastDepth]byte
	depth := 0
	inString, escaped := false, false
	for index := start; index < len(encoded); index++ {
		char := encoded[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{', '[':
			if depth == len(stack) {
				return 0, 0, false
			}
			stack[depth], depth = char, depth+1
		case '}', ']':
			if depth == 0 {
				if char == '}' {
					return index, char, true
				}
				return 0, 0, false
			}
			want := byte('{')
			if char == ']' {
				want = '['
			}
			if stack[depth-1] != want {
				return 0, 0, false
			}
			depth--
		case ',':
			if depth == 0 {
				return index, char, true
			}
		}
	}
	return 0, 0, false
}

func decodeEventEnvelopeV2(encoded []byte, registry EventRegistryV2, canonicalSealed bool) (EventEnvelopeV2, error) {
	var wire eventEnvelopeWire
	var trustedDigest *Digest
	if canonicalSealed {
		decoded, digest, ok := inspectRecoveryEventCanonicalV2(encoded)
		if !ok {
			return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
		}
		wire = decoded
		trustedDigest = &digest
	} else if err := decodeStrict(encoded, MaxEventEnvelopeBytes, &wire); err != nil {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	if wire.EnvelopeVersion != EnvelopeVersion {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	want, err := ParseDigest("canonical_event_sha256", wire.CanonicalEventSHA256)
	if err != nil || trustedDigest != nil && *trustedDigest != want {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	envelope, err := newEventEnvelopeWithTrustedDigestV2(EventEnvelopeInput{
		EventID: EventID(wire.EventID), EventType: wire.EventType, Family: wire.Family, EventSchemaVersion: wire.EventSchemaVersion,
		ReducerVersion: wire.ReducerVersion, Coordinate: wire.Coordinate, AggregateLogicalVersion: LogicalVersion(wire.AggregateLogicalVersion),
		OperationID: OperationID(wire.OperationID), OperationLogicalVersion: LogicalVersion(wire.OperationLogicalVersion), PublicationID: PublicationID(wire.PublicationID),
		CommittedRuntimeEpochID: RuntimeEpochID(wire.CommittedRuntimeEpochID), EventIndex: wire.EventIndex, EventCount: wire.EventCount,
		ActorID: ActorID(wire.ActorID), ProcessID: ProcessID(wire.ProcessID), WorkRevisionID: WorkRevisionID(wire.WorkRevisionID),
		PolicyRevision: wire.PolicyRevision, GovernanceRevisions: wire.GovernanceRevisions, Observations: wire.Observations, Payload: wire.Payload,
	}, registry, canonicalSealed, trustedDigest)
	if err != nil || envelope.digest != want {
		return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
	}
	if !canonicalSealed {
		canonical, _ := EncodeEventEnvelopeV2(envelope)
		if !bytes.Equal(canonical, encoded) {
			return EventEnvelopeV2{}, invalid(ErrorJournalCorrupt, "event_envelope")
		}
	}
	envelope.canonicalSize = len(encoded)
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
	return marshalCanonicalObject(aggregateHeadWire{SchemaVersion, rebuilt.input.Coordinate, uint64(rebuilt.input.LogicalVersion), string(rebuilt.input.LastEventID),
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

// Reducer receives one already-validated immutable event envelope. This keeps
// playback pure while making envelope-owned semantics such as PublicationID
// available to reducers whose durable state depends on them.
type Reducer func(priorCanonicalState []byte, event EventEnvelopeV2) ([]byte, error)

// ReduceOperationStateV2 is the closed operation-family reducer. The payload
// supplies the transition preimage and the validated envelope supplies the
// PublicationID excluded during plan sealing.
func ReduceOperationStateV2(prior []byte, event EventEnvelopeV2) ([]byte, error) {
	if event.input.Family != "operation" || event.input.EventSchemaVersion != 2 || event.input.ReducerVersion != 2 {
		return nil, invalid(ErrorUnknownEventTuple, "operation_reducer")
	}
	state, err := ReconstructOperationStateV2(event.payload, event.input.PublicationID)
	if err != nil {
		return nil, err
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if state.logicalVersion != 1 {
			return nil, invalid(ErrorInvalidTransition, "operation_reducer_initial_version")
		}
	} else {
		previous, decodeErr := DecodeOperationStateV2(prior)
		if decodeErr != nil {
			return nil, decodeErr
		}
		expected, advanceErr := AdvanceOperationState(previous, OperationTransition{ExpectedVersion: previous.logicalVersion,
			NextVersion: state.logicalVersion, Status: state.status, ResultKind: state.resultKind,
			ResultSchemaVersion: state.resultSchemaVersion, CanonicalResult: state.canonicalResult,
			EventIDs: state.eventIDs, EffectIDs: state.effectIDs, LastPublicationID: state.lastPublicationID})
		if advanceErr != nil {
			return nil, advanceErr
		}
		expectedBytes, expectedErr := EncodeOperationStateV2(expected)
		stateBytes, stateErr := EncodeOperationStateV2(state)
		if expectedErr != nil || stateErr != nil || !bytes.Equal(expectedBytes, stateBytes) {
			return nil, invalid(ErrorInvalidTransition, "operation_reducer_transition")
		}
	}
	return EncodeOperationStateV2(state)
}

func OperationReducerImplementationDigestV2() Digest {
	return digest("spec-kitty.reducer-implementation.v2", []byte("operation-state-2/2-envelope-publication"))
}

type ReducerRegistration struct {
	Family                 string
	EventSchemaVersion     uint32
	ReducerVersion         uint32
	Owner                  string
	ImplementationDigest   Digest
	Reduce                 Reducer
	ReducePlaybackSequence PlaybackSequenceReducerV2
}

// PlaybackSequenceReducerV2 is an optional behavior-equivalent replay
// optimization for one aggregate's already ordered, immutable event chain.
type PlaybackSequenceReducerV2 func(prior []byte, events []EventEnvelopeV2) ([]byte, error)

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
	sequences  map[string]PlaybackSequenceReducerV2
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
	sequences := make(map[string]PlaybackSequenceReducerV2)
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
		if registration.ReducePlaybackSequence != nil {
			sequences[key] = registration.ReducePlaybackSequence
		}
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
	return ReducerSetV2{registry: registry, reducers: reducers, sequences: sequences, descriptor: ReducerRegistryDescriptorV2{
		canonical: canonical, digest: digest("spec-kitty.reducer-registry-descriptor.v2", canonical),
	}}, nil
}

// ApplyPlaybackSequenceV2 applies one ordered aggregate chain. Definitions
// without a specialized sequence reducer retain exact per-event behavior.
func (r ReducerSetV2) ApplyPlaybackSequenceV2(prior []byte, events []EventEnvelopeV2) ([]byte, Digest, error) {
	if len(events) == 0 {
		return nil, Digest{}, invalid(ErrorInvalidTransition, "reducer_sequence")
	}
	first := events[0]
	key := eventTupleKey(first.input.Family, first.input.EventSchemaVersion, first.input.ReducerVersion)
	sequence := r.sequences[key]
	if sequence == nil {
		current := prior
		var digest Digest
		for _, event := range events {
			var err error
			current, digest, err = r.ApplyPlaybackV2(current, event)
			if err != nil {
				return nil, Digest{}, err
			}
		}
		return current, digest, nil
	}
	for _, event := range events {
		if event.digest.IsZero() || eventTupleKey(event.input.Family, event.input.EventSchemaVersion, event.input.ReducerVersion) != key {
			return nil, Digest{}, invalid(ErrorUnknownEventTuple, "reducer_sequence")
		}
		if _, err := r.registry.Resolve(event.input.Family, event.input.EventSchemaVersion, event.input.ReducerVersion); err != nil {
			return nil, Digest{}, err
		}
	}
	output, err := sequence(append([]byte(nil), prior...), events)
	if err != nil {
		return nil, Digest{}, err
	}
	output, err = canonicalObjectOwned(output, MaxCanonicalPayloadBytes)
	if err != nil {
		return nil, Digest{}, err
	}
	return output, reducerStateDigestCanonicalV2(output), nil
}

func (r ReducerSetV2) Apply(prior []byte, event EventEnvelopeV2) ([]byte, Digest, error) {
	return r.apply(prior, event, false)
}

// ApplyPlaybackV2 is the reducer route for playback's already-validated state
// map. It preserves output validation but does not re-canonicalize a prior
// value which entered the map only through NewStateV2 or an earlier validated
// reducer result.
func (r ReducerSetV2) ApplyPlaybackV2(prior []byte, event EventEnvelopeV2) ([]byte, Digest, error) {
	return r.apply(prior, event, true)
}

func (r ReducerSetV2) apply(prior []byte, event EventEnvelopeV2, priorIsCanonical bool) ([]byte, Digest, error) {
	// EventEnvelopeV2 is immutable outside this package and every constructor
	// seals its canonical digest. Playback already materializes through the
	// validating constructor, so re-encoding the same envelope for every
	// reducer invocation would perform a fourth identical canonicalization.
	if event.digest.IsZero() {
		return nil, Digest{}, invalid(ErrorInvalidDigest, "event_envelope")
	}
	if _, err := r.registry.Resolve(event.input.Family, event.input.EventSchemaVersion, event.input.ReducerVersion); err != nil {
		return nil, Digest{}, err
	}
	call := r.reducers[eventTupleKey(event.input.Family, event.input.EventSchemaVersion, event.input.ReducerVersion)]
	if call == nil {
		return nil, Digest{}, invalid(ErrorUnknownEventTuple, "reducer")
	}
	canonicalPrior := prior
	if !priorIsCanonical {
		var err error
		canonicalPrior, err = CanonicalObject(prior)
		if err != nil {
			return nil, Digest{}, err
		}
	}
	output, err := call(append([]byte(nil), canonicalPrior...), event)
	if err != nil {
		return nil, Digest{}, err
	}
	output, err = canonicalObjectOwned(output, MaxCanonicalPayloadBytes)
	if err != nil {
		return nil, Digest{}, err
	}
	outputDigest := reducerStateDigestCanonicalV2(output)
	// CanonicalObject returned a newly owned slice; transfer it to playback
	// instead of immediately cloning the same validated bytes again.
	return output, outputDigest, nil
}

// Reduce is retained as the stateless runtime spelling used by existing
// consumers. It delegates to Apply and retains no observations.
func (r ReducerSetV2) Reduce(prior []byte, event EventEnvelopeV2) ([]byte, Digest, error) {
	return r.Apply(prior, event)
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
func (r ReducerSetV2) QualifyDeterminismV2(prior []byte, event EventEnvelopeV2, runs int) error {
	if runs < 2 || runs > MaxQualificationRuns {
		return invalid(ErrorBudgetExceeded, "qualification_runs")
	}
	want, wantDigest, err := r.Apply(prior, event)
	if err != nil {
		return err
	}
	for index := 1; index < runs; index++ {
		got, gotDigest, applyErr := r.Apply(prior, event)
		if applyErr != nil {
			return applyErr
		}
		if !bytes.Equal(want, got) || wantDigest != gotDigest {
			return invalid(ErrorNondeterministic, "reducer")
		}
	}
	return nil
}
