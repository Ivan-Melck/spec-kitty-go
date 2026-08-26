package tracker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// SupportLevel describes a capability whose only portable distinction is
// whether the provider implements it.
type SupportLevel string

const (
	SupportUnsupported SupportLevel = "unsupported"
	SupportSupported   SupportLevel = "supported"
)

func (level SupportLevel) String() string { return string(level) }

func (level SupportLevel) validate(name string) error {
	if level != SupportUnsupported && level != SupportSupported {
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
	return nil
}

// UpdateLevel distinguishes an unsafe unconditional update from one whose
// preconditions are enforced at the provider's mutation boundary.
type UpdateLevel string

const (
	UpdateUnsupported UpdateLevel = "unsupported"
	UpdateUnguarded   UpdateLevel = "unguarded"
	UpdateGuarded     UpdateLevel = "guarded"
)

func (level UpdateLevel) String() string { return string(level) }

func (level UpdateLevel) validate(name string) error {
	switch level {
	case UpdateUnsupported, UpdateUnguarded, UpdateGuarded:
		return nil
	default:
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
}

// DiscussionLevel reports whether discussion is unavailable, append-only, or
// replay-safe because an operation marker can be observed after append.
type DiscussionLevel string

const (
	DiscussionUnsupported DiscussionLevel = "unsupported"
	DiscussionAppendOnly  DiscussionLevel = "append_only"
	DiscussionIdempotent  DiscussionLevel = "observably_idempotent"
)

func (level DiscussionLevel) String() string { return string(level) }

func (level DiscussionLevel) validate(name string) error {
	switch level {
	case DiscussionUnsupported, DiscussionAppendOnly, DiscussionIdempotent:
		return nil
	default:
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
}

// AssignmentLevel reports the primitive available for visible assignment. It
// says nothing about Spec Kitty execution authority.
type AssignmentLevel string

const (
	AssignmentUnsupported AssignmentLevel = "unsupported"
	AssignmentUnguarded   AssignmentLevel = "unguarded"
	AssignmentAtomicClaim AssignmentLevel = "atomic_claim"
)

func (level AssignmentLevel) String() string { return string(level) }

func (level AssignmentLevel) validate(name string) error {
	switch level {
	case AssignmentUnsupported, AssignmentUnguarded, AssignmentAtomicClaim:
		return nil
	default:
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
}

// ObservationRevisionLevel reports where opaque observation identities come
// from. No level implies ordering or a conditional mutation primitive.
type ObservationRevisionLevel string

const (
	ObservationRevisionUnsupported    ObservationRevisionLevel = "unsupported"
	ObservationRevisionContentDerived ObservationRevisionLevel = "content_derived"
	ObservationRevisionProviderNative ObservationRevisionLevel = "provider_native"
)

func (level ObservationRevisionLevel) String() string { return string(level) }

func (level ObservationRevisionLevel) validate(name string) error {
	switch level {
	case ObservationRevisionUnsupported, ObservationRevisionContentDerived, ObservationRevisionProviderNative:
		return nil
	default:
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
}

// CompensationLevel reports whether the provider can enforce revision, value,
// and operation-marker attribution in the same critical section as mutation.
type CompensationLevel string

const (
	CompensationUnsupported CompensationLevel = "unsupported"
	CompensationConditional CompensationLevel = "conditionally_guarded"
)

func (level CompensationLevel) String() string { return string(level) }

func (level CompensationLevel) validate(name string) error {
	if level != CompensationUnsupported && level != CompensationConditional {
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
	return nil
}

// ConcurrentWriterLevel identifies the boundary that serializes provider
// mutations. It does not describe Spec Kitty's integration serialization.
type ConcurrentWriterLevel string

const (
	ConcurrentWritersNone              ConcurrentWriterLevel = "none"
	ConcurrentWritersAdapterSerialized ConcurrentWriterLevel = "adapter_serialized"
	ConcurrentWritersProviderNative    ConcurrentWriterLevel = "provider_native"
)

func (level ConcurrentWriterLevel) String() string { return string(level) }

func (level ConcurrentWriterLevel) validate(name string) error {
	switch level {
	case ConcurrentWritersNone, ConcurrentWritersAdapterSerialized, ConcurrentWritersProviderNative:
		return nil
	default:
		return fmt.Errorf("tracker capabilities: unknown %s level %q", name, level)
	}
}

// CapabilityName is a stable provider-neutral identifier used in refusals and
// operator diagnostics.
type CapabilityName string

const (
	CapabilityCatalogRead         CapabilityName = "catalog_read"
	CapabilityCatalogCreate       CapabilityName = "catalog_create"
	CapabilityCatalogUpdate       CapabilityName = "catalog_update"
	CapabilityBlockingGraph       CapabilityName = "blocking_graph"
	CapabilityDiscussion          CapabilityName = "discussion"
	CapabilityAssignment          CapabilityName = "assignment"
	CapabilityObservationRevision CapabilityName = "observation_revision"
	CapabilitySafeCompensation    CapabilityName = "safe_compensation"
	CapabilityConcurrentWriters   CapabilityName = "concurrent_writers"
	CapabilityPortableExport      CapabilityName = "portable_export"
	CapabilityRestorableBackup    CapabilityName = "restorable_backup"
)

func (capability CapabilityName) String() string { return string(capability) }

// ProviderCapabilities is a copyable declaration of the guarantees supplied
// by one provider binding. Zero-valued levels are invalid: callers must make
// unsupported behavior explicit rather than relying on omission.
type ProviderCapabilities struct {
	CatalogRead         SupportLevel             `json:"catalog_read"`
	CatalogCreate       SupportLevel             `json:"catalog_create"`
	CatalogUpdate       UpdateLevel              `json:"catalog_update"`
	BlockingGraph       SupportLevel             `json:"blocking_graph"`
	Discussion          DiscussionLevel          `json:"discussion"`
	Assignment          AssignmentLevel          `json:"assignment"`
	ObservationRevision ObservationRevisionLevel `json:"observation_revision"`
	SafeCompensation    CompensationLevel        `json:"safe_compensation"`
	ConcurrentWriters   ConcurrentWriterLevel    `json:"concurrent_writers"`
	PortableExport      SupportLevel             `json:"portable_export"`
	RestorableBackup    SupportLevel             `json:"restorable_backup"`
}

// RequireCatalogUpdate rejects a request for guarded update when the provider
// offers only an unguarded write. The adapter must not emulate the missing
// guard with a local read followed by a write.
func (capabilities ProviderCapabilities) RequireCatalogUpdate(required UpdateLevel) error {
	if err := required.validate("required catalog update"); err != nil || required == UpdateUnsupported {
		return &Error{Kind: ErrorInvalidRequest, Message: "requested catalog update level is invalid", RecoveryCode: "repair_request", Capability: CapabilityCatalogUpdate, Cause: err}
	}
	if err := capabilities.Validate(); err != nil {
		return &Error{Kind: ErrorInvalidRequest, Message: "provider capability report is invalid", RecoveryCode: "repair_capability_report", Cause: err}
	}
	if required == UpdateGuarded && capabilities.CatalogUpdate != UpdateGuarded {
		return &Error{Kind: ErrorGuardedUpdateUnsupported, Message: "provider cannot enforce the required update guard", RecoveryCode: "reconcile_without_mutation", Capability: CapabilityCatalogUpdate}
	}
	if capabilities.CatalogUpdate == UpdateUnsupported {
		return &Error{Kind: ErrorCapabilityUnsupported, Message: "provider does not supply catalog update", RecoveryCode: "use_supported_capability", Capability: CapabilityCatalogUpdate}
	}
	return nil
}

func (capabilities ProviderCapabilities) Validate() error {
	checks := []error{
		capabilities.CatalogRead.validate("catalog read"),
		capabilities.CatalogCreate.validate("catalog create"),
		capabilities.CatalogUpdate.validate("catalog update"),
		capabilities.BlockingGraph.validate("blocking graph"),
		capabilities.Discussion.validate("discussion"),
		capabilities.Assignment.validate("assignment"),
		capabilities.ObservationRevision.validate("observation revision"),
		capabilities.SafeCompensation.validate("safe compensation"),
		capabilities.ConcurrentWriters.validate("concurrent writers"),
		capabilities.PortableExport.validate("portable export"),
		capabilities.RestorableBackup.validate("restorable backup"),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	if capabilities.CatalogUpdate != UpdateUnsupported && capabilities.CatalogRead != SupportSupported {
		return fmt.Errorf("tracker capabilities: catalog update requires catalog read")
	}
	if capabilities.Discussion == DiscussionIdempotent && capabilities.ObservationRevision == ObservationRevisionUnsupported {
		return fmt.Errorf("tracker capabilities: observably idempotent discussion requires observation revision")
	}
	if capabilities.Assignment != AssignmentUnsupported && capabilities.ObservationRevision == ObservationRevisionUnsupported {
		return fmt.Errorf("tracker capabilities: assignment requires observation revision")
	}
	if capabilities.SafeCompensation == CompensationConditional &&
		(capabilities.Assignment != AssignmentAtomicClaim || capabilities.ObservationRevision == ObservationRevisionUnsupported) {
		return fmt.Errorf("tracker capabilities: safe compensation requires guarded assignment attribution and observation revision")
	}
	return nil
}

// Require checks whether any usable level of a capability is available. Code
// requiring a particular guarantee must additionally compare the typed level;
// it must never upgrade a weaker declaration by retrying locally.
func (capabilities ProviderCapabilities) Require(capability CapabilityName) error {
	if err := capabilities.Validate(); err != nil {
		return &Error{Kind: ErrorInvalidRequest, Message: "provider capability report is invalid", RecoveryCode: "repair_capability_report", Cause: err}
	}
	supported := false
	switch capability {
	case CapabilityCatalogRead:
		supported = capabilities.CatalogRead == SupportSupported
	case CapabilityCatalogCreate:
		supported = capabilities.CatalogCreate == SupportSupported
	case CapabilityCatalogUpdate:
		supported = capabilities.CatalogUpdate != UpdateUnsupported
	case CapabilityBlockingGraph:
		supported = capabilities.BlockingGraph == SupportSupported
	case CapabilityDiscussion:
		supported = capabilities.Discussion != DiscussionUnsupported
	case CapabilityAssignment:
		supported = capabilities.Assignment != AssignmentUnsupported
	case CapabilityObservationRevision:
		supported = capabilities.ObservationRevision != ObservationRevisionUnsupported
	case CapabilitySafeCompensation:
		supported = capabilities.SafeCompensation != CompensationUnsupported
	case CapabilityConcurrentWriters:
		supported = capabilities.ConcurrentWriters != ConcurrentWritersNone
	case CapabilityPortableExport:
		supported = capabilities.PortableExport == SupportSupported
	case CapabilityRestorableBackup:
		supported = capabilities.RestorableBackup == SupportSupported
	default:
		return &Error{Kind: ErrorInvalidRequest, Message: "unknown requested tracker capability", RecoveryCode: "repair_request", Capability: capability}
	}
	if supported {
		return nil
	}
	kind := ErrorCapabilityUnsupported
	recovery := "use_supported_capability"
	if capability == CapabilitySafeCompensation {
		kind = ErrorSafeCompensationUnsupported
		recovery = "reconcile_without_mutation"
	}
	return &Error{Kind: kind, Message: "provider does not supply the requested capability", RecoveryCode: recovery, Capability: capability}
}

// VerifierKind identifies how Spec Kitty evaluates a criterion. Tracker
// rendering is only a projection and never changes this verifier or its result.
type VerifierKind string

const (
	VerifierTest          VerifierKind = "test"
	VerifierReview        VerifierKind = "review"
	VerifierPolicy        VerifierKind = "policy"
	VerifierHumanDecision VerifierKind = "human_decision"
)

func (kind VerifierKind) String() string { return string(kind) }

func (kind VerifierKind) valid() bool {
	switch kind {
	case VerifierTest, VerifierReview, VerifierPolicy, VerifierHumanDecision:
		return true
	default:
		return false
	}
}

type Criterion struct {
	ID           string
	Text         string
	Verifier     VerifierKind
	Required     bool
	EvidenceHint string
}

func (criterion Criterion) Validate() error {
	if criterion.ID == "" || criterion.Text == "" {
		return fmt.Errorf("tracker criterion: id and text are required")
	}
	if !criterion.Verifier.valid() {
		return fmt.Errorf("tracker criterion: unknown verifier %q", criterion.Verifier)
	}
	return nil
}

// ExternalWorkRef maps an internal authority identity to an external catalog
// identity. ExternalID never substitutes for WorkID.
type ExternalWorkRef struct {
	ProjectID          ids.ProjectID
	WorkID             ids.WorkID
	Provider           string
	ExternalID         string
	MetadataSchema     uint16
	CreatedByOperation ids.OperationID
	LastObservation    Revision
}

func (reference ExternalWorkRef) Validate() error {
	if reference.ProjectID == "" || reference.WorkID == "" || reference.Provider == "" || reference.ExternalID == "" || reference.MetadataSchema == 0 {
		return fmt.Errorf("tracker external work reference: project, work, provider, external id, and metadata schema are required")
	}
	if _, err := ids.ParseProjectID(reference.ProjectID.String()); err != nil {
		return fmt.Errorf("tracker external work reference: %w", err)
	}
	if _, err := ids.ParseWorkID(reference.WorkID.String()); err != nil {
		return fmt.Errorf("tracker external work reference: %w", err)
	}
	if reference.CreatedByOperation != "" {
		if _, err := ids.ParseOperationID(reference.CreatedByOperation.String()); err != nil {
			return fmt.Errorf("tracker external work reference: %w", err)
		}
	}
	return nil
}

// ProviderObservation is one complete known-state identity. Revision is kept
// compatible with Phase 1 records but remains opaque: equality is portable;
// ordering, arithmetic, timestamps, and CAS support are not.
type ProviderObservation struct {
	ExternalID               string
	Revision                 Revision
	Digest                   string
	ProviderToken            string
	ProviderUpdatedAt        time.Time
	ProviderUpdatedAtPresent bool
	Assignee                 string
	OperationMarker          string
	ObservedAt               time.Time
	canonical                []byte
}

func NewProviderObservation(externalID string, canonical []byte, revision Revision, digest, providerToken string, observedAt time.Time) (ProviderObservation, error) {
	if externalID == "" || len(canonical) == 0 || revision == 0 || observedAt.IsZero() {
		return ProviderObservation{}, fmt.Errorf("tracker observation: external id, canonical fields, revision, and observed time are required")
	}
	return ProviderObservation{
		ExternalID: externalID, Revision: revision, Digest: digest, ProviderToken: providerToken,
		ObservedAt: observedAt.UTC(), canonical: append([]byte(nil), canonical...),
	}, nil
}

func (observation ProviderObservation) CanonicalFields() []byte {
	return append([]byte(nil), observation.canonical...)
}

type HealthStatus string

const (
	HealthHealthy     HealthStatus = "healthy"
	HealthDegraded    HealthStatus = "degraded"
	HealthUnavailable HealthStatus = "unavailable"
)

func (status HealthStatus) valid() bool {
	return status == HealthHealthy || status == HealthDegraded || status == HealthUnavailable
}

// Diagnostic contains only bounded, provider-neutral branching data. Raw
// transcripts, environment values, credentials, and discussion bodies do not
// belong in this value.
type Diagnostic struct {
	Kind             ErrorKind
	RecoveryCode     string
	Message          string
	Capability       CapabilityName
	ExternalID       string
	ExpectedRevision Revision
	ObservedRevision Revision
}

// WorkRecord is the complete provider-neutral catalog observation. Callers use
// the accessors for ordered collections so a received record cannot be changed
// by mutating provider-owned backing arrays.
type WorkRecord struct {
	ExternalRef      ExternalWorkRef
	Title            string
	Description      string
	DesignContext    string
	VisibleAssignee  string
	AssignmentMarker string
	Observation      ProviderObservation
	criteria         []Criterion
	dependencies     []DependencyRef
	discussion       []DiscussionEntry
}

func NewWorkRecord(reference ExternalWorkRef, title, description, designContext string, criteria []Criterion, dependencies []DependencyRef, assignee, marker string, discussion []DiscussionEntry, observation ProviderObservation) (WorkRecord, error) {
	if err := reference.Validate(); err != nil {
		return WorkRecord{}, err
	}
	if title == "" || observation.Revision == 0 {
		return WorkRecord{}, fmt.Errorf("tracker work record: title and observation are required")
	}
	if reference.ExternalID != observation.ExternalID {
		return WorkRecord{}, fmt.Errorf("tracker work record: mapping and observation external identities differ")
	}
	for _, criterion := range criteria {
		if err := criterion.Validate(); err != nil {
			return WorkRecord{}, err
		}
	}
	for _, dependency := range dependencies {
		if err := dependency.Validate(); err != nil {
			return WorkRecord{}, err
		}
	}
	return WorkRecord{
		ExternalRef: reference, Title: title, Description: description, DesignContext: designContext,
		VisibleAssignee: assignee, AssignmentMarker: marker, Observation: observation,
		criteria: append([]Criterion(nil), criteria...), dependencies: append([]DependencyRef(nil), dependencies...),
		discussion: append([]DiscussionEntry(nil), discussion...),
	}, nil
}

func (record WorkRecord) Criteria() []Criterion {
	return append([]Criterion(nil), record.criteria...)
}

func (record WorkRecord) Dependencies() []DependencyRef {
	return append([]DependencyRef(nil), record.dependencies...)
}

func (record WorkRecord) Discussion() []DiscussionEntry {
	return append([]DiscussionEntry(nil), record.discussion...)
}

type CreateWorkRequest struct {
	ProjectID      ids.ProjectID
	WorkID         ids.WorkID
	OperationID    ids.OperationID
	Title          string
	Description    string
	DesignContext  string
	Criteria       []Criterion
	MetadataSchema uint16
	// ProviderMetadata is a bounded opaque JSON object owned by the caller and
	// provider. An adapter may add or update only its documented namespace and
	// must preserve every other key. The zero value means an empty object.
	ProviderMetadata OpaqueMetadata
}

const MaxOpaqueMetadataBytes = 1 << 20

// OpaqueMetadata carries provider-neutral JSON without exposing any provider
// schema to the kernel. Construction validates the object root and takes a
// defensive copy; access returns another copy.
type OpaqueMetadata struct{ encoded []byte }

func NewOpaqueMetadata(encoded []byte) (OpaqueMetadata, error) {
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 {
		return OpaqueMetadata{}, nil
	}
	if len(trimmed) > MaxOpaqueMetadataBytes || trimmed[0] != '{' || !json.Valid(trimmed) {
		return OpaqueMetadata{}, fmt.Errorf("tracker opaque metadata: bounded JSON object required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return OpaqueMetadata{}, fmt.Errorf("tracker opaque metadata: bounded JSON object required")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return OpaqueMetadata{}, fmt.Errorf("tracker opaque metadata: encode object: %w", err)
	}
	return OpaqueMetadata{encoded: canonical}, nil
}

func (metadata OpaqueMetadata) Bytes() []byte {
	if len(metadata.encoded) == 0 {
		return nil
	}
	return append([]byte(nil), metadata.encoded...)
}

func (metadata OpaqueMetadata) MarshalJSON() ([]byte, error) {
	if len(metadata.encoded) == 0 {
		return []byte(`{}`), nil
	}
	return metadata.Bytes(), nil
}

func (metadata *OpaqueMetadata) UnmarshalJSON(encoded []byte) error {
	value, err := NewOpaqueMetadata(encoded)
	if err != nil {
		return err
	}
	*metadata = value
	return nil
}

type UpdateWorkRequest struct {
	ExternalRef         ExternalWorkRef
	OperationID         ids.OperationID
	ExpectedObservation Revision
	RequiredGuard       UpdateLevel
	Title               string
	Description         string
	DesignContext       string
	Criteria            []Criterion
}

type WorkMutation struct {
	Record    WorkRecord
	Guarantee UpdateLevel
	Replayed  bool
	Noop      bool
}

// Catalog is the provider-neutral work-inventory port. Implementations must
// apply the declared guard semantics rather than manufacturing stronger ones
// through read-then-write.
type Catalog interface {
	CatalogCreator
	CatalogReader
	CatalogUpdater
}

type CatalogCreator interface {
	CreateWork(context.Context, CreateWorkRequest) (WorkMutation, error)
}

type CatalogReader interface {
	ReadWork(context.Context, ExternalWorkRef) (WorkRecord, error)
}

type CatalogUpdater interface {
	UpdateWork(context.Context, UpdateWorkRequest) (WorkMutation, error)
}

type DependencyKind string

const DependencyBlocks DependencyKind = "blocks"

type DependencyRef struct {
	BlockedWorkID  ids.WorkID
	BlockingWorkID ids.WorkID
	Kind           DependencyKind
	ExternalEdgeID string
}

func (dependency DependencyRef) Validate() error {
	if dependency.BlockedWorkID == "" || dependency.BlockingWorkID == "" || dependency.Kind != DependencyBlocks {
		return fmt.Errorf("tracker dependency: blocked work, blocking work, and blocks kind are required")
	}
	if dependency.BlockedWorkID == dependency.BlockingWorkID {
		return fmt.Errorf("tracker dependency: work cannot block itself")
	}
	return nil
}

type DependencyRequest struct {
	ProjectID   ids.ProjectID
	OperationID ids.OperationID
	Dependency  DependencyRef
}

type DependencyMutation struct {
	Dependency  DependencyRef
	Observation ProviderObservation
	Replayed    bool
	Noop        bool
}

type ReadyRequest struct {
	ProjectID      ids.ProjectID
	PinnedReadyIDs []ids.WorkID
}

type ReadyProjection struct {
	ProviderReadyIDs []ids.WorkID
	Diagnostics      []Diagnostic
	Observation      ProviderObservation
}

// Graph exposes mutable blocking edges and a diagnostic readiness projection.
// The projection is never an execution authorization.
type Graph interface {
	PutBlockingDependency(context.Context, DependencyRequest) (DependencyMutation, error)
	RemoveBlockingDependency(context.Context, DependencyRequest) (DependencyMutation, error)
	ListDependencies(context.Context, ExternalWorkRef) ([]DependencyRef, error)
	ReadyProjection(context.Context, ReadyRequest) (ReadyProjection, error)
}

type DiscussionEntry struct {
	ExternalCommentID string
	Author            string
	Body              string
	OperationMarker   string
	CreatedAt         time.Time
	Observation       Revision
}

type AppendDiscussionRequest struct {
	ExternalRef         ExternalWorkRef
	OperationID         ids.OperationID
	Author              string
	Body                string
	ExpectedObservation Revision
}

type DiscussionMutation struct {
	Entry       DiscussionEntry
	Observation ProviderObservation
	Replayed    bool
	Noop        bool
}

type Discussion interface {
	AppendDiscussion(context.Context, AppendDiscussionRequest) (DiscussionMutation, error)
	ListDiscussion(context.Context, ExternalWorkRef) ([]DiscussionEntry, error)
}

type AssignmentObservation struct {
	ExternalRef     ExternalWorkRef
	Assignee        string
	OperationMarker string
	Observation     ProviderObservation
}

type ProjectAssignmentRequest struct {
	ExternalRef         ExternalWorkRef
	OperationID         ids.OperationID
	Assignee            string
	ExpectedObservation Revision
	RequiredGuarantee   AssignmentLevel
}

type CompensateAssignmentRequest struct {
	ExternalRef         ExternalWorkRef
	OperationID         ids.OperationID
	ExpectedObservation Revision
	ExpectedAssignee    string
	ExpectedMarker      string
	RestoreAssignee     string
}

type AssignmentMutation struct {
	Observation AssignmentObservation
	Guarantee   AssignmentLevel
	Replayed    bool
	Noop        bool
}

type AssignmentProjection interface {
	ObserveAssignment(context.Context, ExternalWorkRef) (AssignmentObservation, error)
	ProjectAssignment(context.Context, ProjectAssignmentRequest) (AssignmentMutation, error)
	CompensateAssignment(context.Context, CompensateAssignmentRequest) (AssignmentMutation, error)
}

type InspectRequest struct {
	ProjectID ids.ProjectID
}

type ConcurrencyMode string

const (
	ConcurrencyEmbeddedSerialized ConcurrencyMode = "embedded_serialized"
	ConcurrencyServerMultiwriter  ConcurrencyMode = "server_multiwriter"
)

func (mode ConcurrencyMode) valid() bool {
	return mode == ConcurrencyEmbeddedSerialized || mode == ConcurrencyServerMultiwriter
}

// TrackerBinding pins inspection and mutation to one executable and canonical
// provider workspace. It is project-scoped and distinct from lease-scoped
// execution workspaces.
type TrackerBinding struct {
	ProjectID         ids.ProjectID
	Provider          string
	ExecutablePath    string
	ExecutableVersion string
	ExecutableDigest  string
	ProtocolSchema    uint16
	WorkspacePath     string
	WorkspaceIdentity string
	ConcurrencyMode   ConcurrencyMode
	ReadOnly          bool
	ObservedAt        time.Time
}

func (binding TrackerBinding) Validate() error {
	if _, err := ids.ParseProjectID(binding.ProjectID.String()); err != nil {
		return fmt.Errorf("tracker binding: %w", err)
	}
	if binding.Provider == "" || binding.ExecutableVersion == "" || binding.ProtocolSchema == 0 || binding.WorkspaceIdentity == "" || binding.ObservedAt.IsZero() {
		return fmt.Errorf("tracker binding: provider, version, protocol, workspace identity, and observed time are required")
	}
	if !filepath.IsAbs(binding.ExecutablePath) || filepath.Clean(binding.ExecutablePath) != binding.ExecutablePath {
		return fmt.Errorf("tracker binding: executable path must be absolute and clean")
	}
	if !filepath.IsAbs(binding.WorkspacePath) || filepath.Clean(binding.WorkspacePath) != binding.WorkspacePath {
		return fmt.Errorf("tracker binding: workspace path must be absolute and clean")
	}
	if !binding.ConcurrencyMode.valid() {
		return fmt.Errorf("tracker binding: unknown concurrency mode %q", binding.ConcurrencyMode)
	}
	if binding.ExecutableDigest != "" {
		digest, err := hex.DecodeString(binding.ExecutableDigest)
		if err != nil || len(digest) != 32 {
			return fmt.Errorf("tracker binding: executable digest must be lowercase SHA-256")
		}
		if hex.EncodeToString(digest) != binding.ExecutableDigest {
			return fmt.Errorf("tracker binding: executable digest must use lowercase hexadecimal")
		}
	}
	return nil
}

type Inspection struct {
	Binding      TrackerBinding
	Capabilities ProviderCapabilities
	Health       HealthStatus
	diagnostics  []Diagnostic
}

func NewInspection(binding TrackerBinding, capabilities ProviderCapabilities, health HealthStatus, diagnostics []Diagnostic) (Inspection, error) {
	if err := binding.Validate(); err != nil {
		return Inspection{}, err
	}
	if err := capabilities.Validate(); err != nil {
		return Inspection{}, err
	}
	if !health.valid() {
		return Inspection{}, fmt.Errorf("tracker inspection: unknown health %q", health)
	}
	binding.ObservedAt = binding.ObservedAt.UTC()
	return Inspection{Binding: binding, Capabilities: capabilities, Health: health, diagnostics: append([]Diagnostic(nil), diagnostics...)}, nil
}

func (inspection Inspection) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), inspection.diagnostics...)
}

type Inspector interface {
	Inspect(context.Context, InspectRequest) (Inspection, error)
}

type ExportRequest struct {
	ProjectID ids.ProjectID
}

type ExportResult struct {
	Records uint64
	Digest  string
}

type BackupRequest struct {
	ProjectID       ids.ProjectID
	DestinationPath string
}

type BackupResult struct {
	BackupID string
	Path     string
	Digest   string
}

type BackupInspectRequest struct {
	ProjectID ids.ProjectID
	BackupID  string
	Path      string
}

type BackupInspection struct {
	Restorable bool
	Digest     string
	CreatedAt  time.Time
}

type Exporter interface {
	ExportPortable(context.Context, ExportRequest, io.Writer) (ExportResult, error)
	Backup(context.Context, BackupRequest) (BackupResult, error)
	InspectBackup(context.Context, BackupInspectRequest) (BackupInspection, error)
}
