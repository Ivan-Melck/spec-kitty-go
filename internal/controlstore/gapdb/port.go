// Package gapdb is the deliberately narrow outbound storage adapter used by
// the M1 control-journal proving ground. It contains no mission policy: callers
// supply complete keys, values, conditions, operation identities, and explicit
// authority namespaces.
package gapdb

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	AdmittedModulePath                = "github.com/spec-kitty/gapdb"
	AdmittedModuleVersion             = "v0.0.0-20260824033134-ff3dd478f87c"
	AdmittedSourceRevision            = "ff3dd478f87c6b4cbb1dcc168562f99879ba8c25"
	AdmittedSourceTree                = "64ada3cd6328aca73ef902e4ab9fe0405823841c"
	AdmittedSidecarSHA256             = "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
	AdmittedTupleSHA256               = "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
	AdmittedWireVersion               = 1
	AdmittedStorageVersion            = 1
	AssertionCandidateModuleVersion   = "v0.0.0-20260904151017-4d88094749e0"
	AssertionCandidateSourceRevision  = "4d88094749e0f6fdb38a4786708d920fb51966a1"
	AssertionCandidateSourceTree      = "a109220e3dae287e321fb37e904d3bea0802b911"
	AssertionCandidateSidecarSHA256   = "947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad"
	AssertionCandidateSidecarBytes    = 5_327_126
	QualificationClientModuleVersion  = "v0.0.0-20260907061131-c27646533cde"
	QualificationClientSourceRevision = "c27646533cde29bcc9840b4d5feac54aa2215d04"
	QualificationClientSourceTree     = "093d22c790d90859aec5ecb8d53912122688994d"
	QualificationSidecarSHA256        = "4571ee2724e4b8194c8dda057adc471fcc1bcedc592e7c47a78282a9ef6388d2"
	QualificationSidecarBytes         = 5_357_809
	MaxKeyBytes                       = 1 << 10
	MaxOrdinaryValueBytes             = 256 << 10
	MaxCheckpointValueBytes           = 4 << 20
	MaxEventsPerCommand               = 128
	MaxHeadsPerCommand                = 32
	MaxIntentsPerCommand              = 16
	MaxMutationsPerBatch              = 256
	MaxRawBatchBytes                  = 6 << 20
	MaxWireFrameBytes                 = 12 << 20
	MaxScanRecords                    = 500
	MaxScanBytes                      = 8 << 20
	MaxDiagnosticBytes                = 2 << 10
	MaxOperationIDBytes               = 256
	MaxCursorBytes                    = 16 << 10
	DefaultRequestTimeout             = 5 * time.Second
)

// OutcomeKind is storage-neutral. In particular, unavailable carries no
// mutating-delivery uncertainty (a read may simply be repeated), while an
// ambiguous mutation may have been accepted and must be reconciled using the
// same OperationID.
type OutcomeKind string

const (
	OutcomeInvalid            OutcomeKind = "invalid"
	OutcomeNotFound           OutcomeKind = "not_found"
	OutcomeConflict           OutcomeKind = "conflict"
	OutcomeUnavailable        OutcomeKind = "unavailable"
	OutcomeUnsupportedVersion OutcomeKind = "unsupported_version"
	OutcomeCapabilityAbsent   OutcomeKind = "capability_absent"
	OutcomeMalformedResponse  OutcomeKind = "malformed_response"
	OutcomeAmbiguous          OutcomeKind = "ambiguous"
	OutcomeCanceled           OutcomeKind = "canceled"
	OutcomeDeadlineExceeded   OutcomeKind = "deadline_exceeded"
)

type RecoveryAction string

const (
	RecoveryFixRequest         RecoveryAction = "fix_request"
	RecoveryRead               RecoveryAction = "read"
	RecoveryRetrySameOperation RecoveryAction = "retry_same_operation"
	RecoveryReconcile          RecoveryAction = "reconcile_same_operation"
	RecoveryRestartProvider    RecoveryAction = "restart_provider"
	RecoveryUpgradeProvider    RecoveryAction = "upgrade_provider"
	RecoveryOperator           RecoveryAction = "operator"
)

// Error is the stable diagnostic surface. Error() intentionally excludes the
// provider's message, values, credentials, full socket paths, and transcripts.
// Cause is retained only for errors.Is/errors.As within the current process.
type Error struct {
	Kind        OutcomeKind
	Operation   string
	OperationID string
	Recovery    RecoveryAction
	Detail      string
	Cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := fmt.Sprintf("gapdb adapter %s: %s", boundedText(e.Operation, 64), e.Kind)
	if e.OperationID != "" {
		message += " operation_id=" + boundedText(e.OperationID, MaxOperationIDBytes)
	}
	if e.Recovery != "" {
		message += " recovery=" + string(e.Recovery)
	}
	if e.Detail != "" {
		message += " detail=" + boundedText(e.Detail, MaxDiagnosticBytes)
	}
	return boundedText(message, MaxDiagnosticBytes)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsKind(err error, kind OutcomeKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}

type AckMode string

const (
	AckMemory  AckMode = "memory"
	AckDurable AckMode = "durable"
)

type ConditionKind string

const (
	ConditionAny      ConditionKind = "any"
	ConditionAbsent   ConditionKind = "absent"
	ConditionRevision ConditionKind = "revision"
)

type Condition struct {
	Kind             ConditionKind
	ExpectedRevision uint64
}

type MutationKind string

const (
	MutationPut    MutationKind = "put"
	MutationDelete MutationKind = "delete"
)

type Mutation struct {
	Kind      MutationKind
	Key       string
	Condition Condition
	Value     []byte
	ExpiresAt *time.Time
}

type Record struct {
	Key       string
	Value     []byte
	Revision  uint64
	ExpiresAt *time.Time
}

type MutationResult struct {
	Revision       uint64
	Ack            AckMode
	DurableThrough uint64
	MutationCount  int
}

type BatchMeasurement struct {
	MutationCount  int
	AssertionCount int
	RawBytes       int
	WireBytes      int
}

// ProductBatch mirrors the indivisible application transaction without
// exposing provider types. Checkpoint is the only record permitted to use the
// larger checkpoint value budget; CheckpointHead remains an ordinary value.
type ProductBatch struct {
	OperationID    string
	Operation      Mutation
	Events         []Mutation
	AggregateHeads []Mutation
	EffectIntents  []Mutation
	Checkpoint     *Mutation
	CheckpointHead *Mutation
}

// RuntimeEpochBatch is the indivisible owner-startup transaction.  Its named
// records prevent callers from smuggling epoch metadata through product event
// or aggregate-head slots merely to satisfy ProductBatch cardinalities.
type RuntimeEpochBatch struct {
	OperationID    string
	Operation      Mutation
	Event          Mutation
	AggregateHead  Mutation
	EpochRecord    Mutation
	CurrentBinding Mutation
}

type ScanRequest struct {
	Prefix   string
	Limit    int
	Cursor   string
	MaxBytes int
}

type ScanPage struct {
	ObservedRevision uint64
	AsOf             time.Time
	Records          []Record
	Cursor           string
	Truncated        bool
}

type ChangeKind string

const (
	ChangePut    ChangeKind = "put"
	ChangeDelete ChangeKind = "delete"
	ChangeExpire ChangeKind = "expire"
)

type Change struct {
	Revision uint64
	Order    uint32
	Kind     ChangeKind
	Key      string
	Record   *Record
}

// NotificationWatch is only a wake-up/observation surface. A caller must
// reconcile notifications with Get or Scan; delivery is never the journal.
type NotificationWatch interface {
	RegistrationRevision() uint64
	Next(context.Context) (Change, error)
	Close() error
}

type Inspection struct {
	EndpointID          string
	DatabaseID          string
	Lifecycle           string
	Healthy             bool
	CurrentRevision     uint64
	DurableThrough      uint64
	OwnerPID            int
	OwnerStartedAt      string
	ConfigurationSource string
	ReportedLimits      ProviderLimits
	SafeCeilings        ProviderLimits
}

// ProviderLimits contains only public, reported configuration observations.
// It deliberately does not claim executable, source, wire, or storage identity.
type ProviderLimits struct {
	KeyBytes          int
	ValueBytes        int
	FrameBytes        int
	BatchBytes        int
	BatchOperations   int
	ScanRecords       int
	ScanBytes         int
	ConcurrentClients int
}

type AuthorityNamespace string

const (
	AuthorityLease       AuthorityNamespace = "lease"
	AuthorityReview      AuthorityNamespace = "review"
	AuthorityWorkflow    AuthorityNamespace = "workflow"
	AuthorityIntegration AuthorityNamespace = "integration"
)

type AuthorityMutation struct {
	OperationID string
	Namespace   AuthorityNamespace
	Codec       string
	Mutation    Mutation
}

func AuthorityCodec(namespace AuthorityNamespace) string {
	return "spk/authority/" + string(namespace) + "/v1"
}

// Port is the complete application-facing M1 storage seam.
type Port interface {
	Get(context.Context, string) (Record, error)
	ConditionalMutate(context.Context, string, Mutation, AckMode) (MutationResult, error)
	PreflightAtomicBatch(ProductBatch) (BatchMeasurement, error)
	AtomicBatch(context.Context, ProductBatch) (MutationResult, error)
	AdmitRuntimeEpoch(context.Context, RuntimeEpochBatch) (MutationResult, error)
	Scan(context.Context, ScanRequest) (ScanPage, error)
	Watch(context.Context, string, uint64) (NotificationWatch, error)
	Inspect(context.Context) (Inspection, error)
	LeaseAuthority(context.Context, AuthorityMutation, AckMode) (MutationResult, error)
	ReviewAuthority(context.Context, AuthorityMutation, AckMode) (MutationResult, error)
	WorkflowAuthority(context.Context, AuthorityMutation, AckMode) (MutationResult, error)
	IntegrationAuthority(context.Context, AuthorityMutation, AckMode) (MutationResult, error)
}

func boundedText(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
