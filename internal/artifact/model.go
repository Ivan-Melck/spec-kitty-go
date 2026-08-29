// Package artifact owns immutable mission-artifact revisions, validation
// evidence, and exact-revision approval verdicts. Files and source references
// are inputs and audit locators; they are never authority in this package.
package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	// ContractSchemaVersion is the only artifact JSON projection accepted in
	// Phase 3.
	ContractSchemaVersion = 1
	// ContentCommitmentAlgorithm makes the domain separation and digest
	// migration boundary explicit. The digest is SHA-256 over the ASCII domain,
	// a NUL separator, and the exact artifact bytes.
	ContentCommitmentAlgorithm = "sha256:spec-kitty-artifact-content-v1"
	contentCommitmentDomain    = "spec-kitty/artifact-content/v1\x00"
	maxErrorMessageLength      = 512
)

var (
	stableIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	lowerSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	windowsVolume  = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// Kind is the closed Phase 3 artifact vocabulary.
type Kind string

const (
	KindIntent        Kind = "intent"
	KindSpecification Kind = "specification"
	KindPlan          Kind = "plan"
)

func (k Kind) Validate() error {
	switch k {
	case KindIntent, KindSpecification, KindPlan:
		return nil
	default:
		return diagnostic(ErrorUnsupportedKind, "kind", RecoveryCorrectInput, "artifact kind is unsupported", nil)
	}
}

// ApprovalVerdict is an immutable historical verdict. Only approved can
// satisfy freshness, and only for the exact named revision.
type ApprovalVerdict string

const (
	VerdictApproved ApprovalVerdict = "approved"
	VerdictRejected ApprovalVerdict = "rejected"
)

func (v ApprovalVerdict) Validate() error {
	switch v {
	case VerdictApproved, VerdictRejected:
		return nil
	default:
		return diagnostic(ErrorUnsupportedVerdict, "verdict", RecoveryCorrectInput, "approval verdict is unsupported", nil)
	}
}

// RecoveryCategory is a closed machine-readable recovery vocabulary.
type RecoveryCategory string

const (
	RecoveryCorrectInput       RecoveryCategory = "correct_input"
	RecoveryRetrySameOperation RecoveryCategory = "retry_same_operation"
	RecoveryResolveConflict    RecoveryCategory = "resolve_conflict"
	RecoveryProvideEvidence    RecoveryCategory = "provide_evidence"
	RecoveryInspectState       RecoveryCategory = "inspect_state"
)

func (r RecoveryCategory) Validate() error {
	switch r {
	case RecoveryCorrectInput, RecoveryRetrySameOperation, RecoveryResolveConflict,
		RecoveryProvideEvidence, RecoveryInspectState:
		return nil
	default:
		return diagnostic(ErrorUnsupportedRecovery, "recovery", RecoveryCorrectInput, "recovery category is unsupported", nil)
	}
}

// ErrorKind is the stable artifact-domain diagnostic vocabulary.
type ErrorKind string

const (
	ErrorInvalidIdentity     ErrorKind = "invalid_identity"
	ErrorUnsupportedKind     ErrorKind = "unsupported_kind"
	ErrorUnsupportedVerdict  ErrorKind = "unsupported_verdict"
	ErrorMalformedDigest     ErrorKind = "malformed_digest"
	ErrorInvalidRevision     ErrorKind = "invalid_revision"
	ErrorInvalidProvenance   ErrorKind = "invalid_provenance"
	ErrorInvalidTimestamp    ErrorKind = "invalid_timestamp"
	ErrorInvalidValidation   ErrorKind = "invalid_validation"
	ErrorInvalidFinding      ErrorKind = "invalid_finding"
	ErrorUnsupportedRecovery ErrorKind = "unsupported_recovery"
	ErrorPolicyUnsatisfied   ErrorKind = "policy_unsatisfied"
	ErrorInvalidCommand      ErrorKind = "invalid_command"
	ErrorIdempotencyConflict ErrorKind = "idempotency_conflict"
	ErrorNotFound            ErrorKind = "not_found"
	ErrorValidationRequired  ErrorKind = "validation_required"
	ErrorApprovalNotAllowed  ErrorKind = "approval_not_allowed"
	ErrorRepositoryFailure   ErrorKind = "repository_failure"
	ErrorUnsupportedSchema   ErrorKind = "unsupported_schema"
	ErrorUnexpectedAuthority ErrorKind = "unexpected_authority_field"
)

func (k ErrorKind) Validate() error {
	switch k {
	case ErrorInvalidIdentity, ErrorUnsupportedKind, ErrorUnsupportedVerdict,
		ErrorMalformedDigest, ErrorInvalidRevision, ErrorInvalidProvenance,
		ErrorInvalidTimestamp, ErrorInvalidValidation, ErrorInvalidFinding,
		ErrorUnsupportedRecovery, ErrorPolicyUnsatisfied, ErrorInvalidCommand,
		ErrorIdempotencyConflict, ErrorNotFound, ErrorValidationRequired,
		ErrorApprovalNotAllowed, ErrorRepositoryFailure, ErrorUnsupportedSchema,
		ErrorUnexpectedAuthority:
		return nil
	default:
		return diagnostic(ErrorRepositoryFailure, "error.kind", RecoveryInspectState, "artifact error kind is unsupported", nil)
	}
}

// Error is a bounded typed diagnostic. Field names a logical contract field;
// it never contains the rejected value. Cause is intentionally excluded from
// durable JSON evidence.
type Error struct {
	Kind     ErrorKind        `json:"kind"`
	Field    string           `json:"field,omitempty"`
	Recovery RecoveryCategory `json:"recovery"`
	Message  string           `json:"message"`
	cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("artifact: kind=%s recovery=%s: %s", e.Kind, e.Recovery, e.Message)
	}
	return fmt.Sprintf("artifact: kind=%s field=%s recovery=%s: %s", e.Kind, e.Field, e.Recovery, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// IsKind classifies an error without parsing its message.
func IsKind(err error, kind ErrorKind) bool {
	var artifactError *Error
	return errors.As(err, &artifactError) && artifactError.Kind == kind
}

func diagnostic(kind ErrorKind, field string, recovery RecoveryCategory, message string, cause error) *Error {
	message = strings.TrimSpace(message)
	if len(message) > maxErrorMessageLength {
		message = message[:maxErrorMessageLength]
	}
	return &Error{Kind: kind, Field: field, Recovery: recovery, Message: message, cause: cause}
}

// ContentCommitment returns a lowercase domain-qualified SHA-256 commitment
// over exact caller bytes. It does not retain the supplied slice.
func ContentCommitment(content []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(contentCommitmentDomain))
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}

// ArtifactRevisionParams is the construction/restoration boundary for an
// immutable artifact revision.
type ArtifactRevisionParams struct {
	ID             string
	MissionID      string
	Kind           Kind
	RevisionNumber uint64
	ContentDigest  string
	SourceRef      string
	SourceRevision string
	CreatedBy      string
	CreatedAt      time.Time
}

// ArtifactRevision is immutable after construction. Its fields remain private
// so no caller can partly initialize or rewrite durable evidence.
type ArtifactRevision struct {
	id             string
	missionID      string
	kind           Kind
	revisionNumber uint64
	contentDigest  string
	sourceRef      string
	sourceRevision string
	createdBy      string
	createdAt      time.Time
}

// NewArtifactRevision validates and constructs a published revision value.
func NewArtifactRevision(params ArtifactRevisionParams) (ArtifactRevision, error) {
	if err := validateIdentity("artifact_revision_id", params.ID); err != nil {
		return ArtifactRevision{}, err
	}
	if err := validateIdentity("mission_id", params.MissionID); err != nil {
		return ArtifactRevision{}, err
	}
	if err := params.Kind.Validate(); err != nil {
		return ArtifactRevision{}, err
	}
	if params.RevisionNumber == 0 {
		return ArtifactRevision{}, diagnostic(ErrorInvalidRevision, "revision_number", RecoveryCorrectInput, "revision number must be positive", nil)
	}
	if err := validateDigest("content_digest", params.ContentDigest); err != nil {
		return ArtifactRevision{}, err
	}
	if err := validateSourceRef(params.SourceRef); err != nil {
		return ArtifactRevision{}, err
	}
	if err := validateIdentity("source_revision", params.SourceRevision); err != nil {
		return ArtifactRevision{}, diagnostic(ErrorInvalidProvenance, "source_revision", RecoveryCorrectInput, "source revision must be a stable identity", err)
	}
	if err := validateIdentity("created_by", params.CreatedBy); err != nil {
		return ArtifactRevision{}, diagnostic(ErrorInvalidProvenance, "created_by", RecoveryCorrectInput, "creator must be a stable identity", err)
	}
	createdAt, err := normalizeTime("created_at", params.CreatedAt)
	if err != nil {
		return ArtifactRevision{}, err
	}
	return ArtifactRevision{
		id: params.ID, missionID: params.MissionID, kind: params.Kind,
		revisionNumber: params.RevisionNumber, contentDigest: params.ContentDigest,
		sourceRef: params.SourceRef, sourceRevision: params.SourceRevision,
		createdBy: params.CreatedBy, createdAt: createdAt,
	}, nil
}

// RestoreArtifactRevision applies the same invariant checks as construction.
func RestoreArtifactRevision(params ArtifactRevisionParams) (ArtifactRevision, error) {
	return NewArtifactRevision(params)
}

func (r ArtifactRevision) ID() string             { return r.id }
func (r ArtifactRevision) MissionID() string      { return r.missionID }
func (r ArtifactRevision) Kind() Kind             { return r.kind }
func (r ArtifactRevision) RevisionNumber() uint64 { return r.revisionNumber }
func (r ArtifactRevision) ContentDigest() string  { return r.contentDigest }
func (r ArtifactRevision) SourceRef() string      { return r.sourceRef }
func (r ArtifactRevision) SourceRevision() string { return r.sourceRevision }
func (r ArtifactRevision) CreatedBy() string      { return r.createdBy }
func (r ArtifactRevision) CreatedAt() time.Time   { return r.createdAt }

// ArtifactApprovalParams constructs an immutable exact-revision verdict.
type ArtifactApprovalParams struct {
	ID                  string
	ArtifactRevisionID  string
	Verdict             ApprovalVerdict
	ActorID             string
	EvidenceDigest      string
	GovernanceContextID string
	SubmittedAt         time.Time
}

type ArtifactApproval struct {
	id                  string
	artifactRevisionID  string
	verdict             ApprovalVerdict
	actorID             string
	evidenceDigest      string
	governanceContextID string
	submittedAt         time.Time
}

func NewArtifactApproval(params ArtifactApprovalParams) (ArtifactApproval, error) {
	if err := validateIdentity("approval_id", params.ID); err != nil {
		return ArtifactApproval{}, err
	}
	if err := validateIdentity("artifact_revision_id", params.ArtifactRevisionID); err != nil {
		return ArtifactApproval{}, err
	}
	if err := params.Verdict.Validate(); err != nil {
		return ArtifactApproval{}, err
	}
	if err := validateIdentity("actor_id", params.ActorID); err != nil {
		return ArtifactApproval{}, diagnostic(ErrorInvalidProvenance, "actor_id", RecoveryCorrectInput, "approval actor must be a stable identity", err)
	}
	if err := validateDigest("evidence_digest", params.EvidenceDigest); err != nil {
		return ArtifactApproval{}, err
	}
	if err := validateIdentity("governance_context_id", params.GovernanceContextID); err != nil {
		return ArtifactApproval{}, diagnostic(ErrorInvalidProvenance, "governance_context_id", RecoveryCorrectInput, "governance context must be sealed by stable identity", err)
	}
	submittedAt, err := normalizeTime("submitted_at", params.SubmittedAt)
	if err != nil {
		return ArtifactApproval{}, err
	}
	return ArtifactApproval{
		id: params.ID, artifactRevisionID: params.ArtifactRevisionID,
		verdict: params.Verdict, actorID: params.ActorID,
		evidenceDigest:      params.EvidenceDigest,
		governanceContextID: params.GovernanceContextID, submittedAt: submittedAt,
	}, nil
}

func RestoreArtifactApproval(params ArtifactApprovalParams) (ArtifactApproval, error) {
	return NewArtifactApproval(params)
}

func (a ArtifactApproval) ID() string                  { return a.id }
func (a ArtifactApproval) ArtifactRevisionID() string  { return a.artifactRevisionID }
func (a ArtifactApproval) Verdict() ApprovalVerdict    { return a.verdict }
func (a ArtifactApproval) ActorID() string             { return a.actorID }
func (a ArtifactApproval) EvidenceDigest() string      { return a.evidenceDigest }
func (a ArtifactApproval) GovernanceContextID() string { return a.governanceContextID }
func (a ArtifactApproval) SubmittedAt() time.Time      { return a.submittedAt }

// Contract is the strict schema-v1 JSON projection. Validation remains bound
// to ArtifactRevisionID in memory even though the external schema nests it.
type Contract struct {
	SchemaVersion int
	Revision      ArtifactRevision
	Validation    ValidationResult
	Approval      *ArtifactApproval
}

type contractJSON struct {
	SchemaVersion    int                  `json:"schema_version"`
	ArtifactRevision artifactRevisionJSON `json:"artifact_revision"`
	Validation       validationJSON       `json:"validation"`
	Approval         *approvalJSON        `json:"approval"`
}

type artifactRevisionJSON struct {
	ID             string `json:"artifact_revision_id"`
	MissionID      string `json:"mission_id"`
	Kind           Kind   `json:"kind"`
	RevisionNumber uint64 `json:"revision_number"`
	ContentDigest  string `json:"content_digest"`
	SourceRef      string `json:"source_ref"`
	SourceRevision string `json:"source_revision"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
}

type validationJSON struct {
	Verdict     ValidationVerdict `json:"verdict"`
	Findings    []findingJSON     `json:"findings"`
	ValidatedAt string            `json:"validated_at"`
}

type findingJSON struct {
	Kind    string `json:"kind"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type approvalJSON struct {
	ID                  string          `json:"approval_id"`
	ArtifactRevisionID  string          `json:"artifact_revision_id"`
	Verdict             ApprovalVerdict `json:"verdict"`
	ActorID             string          `json:"actor_id"`
	EvidenceDigest      string          `json:"evidence_digest"`
	GovernanceContextID string          `json:"governance_context_id"`
	SubmittedAt         string          `json:"submitted_at"`
}

// MarshalContract emits the strict artifact-contract.schema.json shape.
func MarshalContract(contract Contract) ([]byte, error) {
	if contract.SchemaVersion != ContractSchemaVersion {
		return nil, diagnostic(ErrorUnsupportedSchema, "schema_version", RecoveryCorrectInput, "artifact contract schema version is unsupported", nil)
	}
	if err := validateContractBinding(contract); err != nil {
		return nil, err
	}
	projection := contractJSON{
		SchemaVersion: ContractSchemaVersion,
		ArtifactRevision: artifactRevisionJSON{
			ID: contract.Revision.ID(), MissionID: contract.Revision.MissionID(), Kind: contract.Revision.Kind(),
			RevisionNumber: contract.Revision.RevisionNumber(), ContentDigest: contract.Revision.ContentDigest(),
			SourceRef: contract.Revision.SourceRef(), SourceRevision: contract.Revision.SourceRevision(),
			CreatedBy: contract.Revision.CreatedBy(), CreatedAt: contract.Revision.CreatedAt().Format(time.RFC3339Nano),
		},
		Validation: validationJSON{
			Verdict: contract.Validation.Verdict(), Findings: make([]findingJSON, 0, len(contract.Validation.Findings())),
			ValidatedAt: contract.Validation.ValidatedAt().Format(time.RFC3339Nano),
		},
	}
	for _, finding := range contract.Validation.Findings() {
		projection.Validation.Findings = append(projection.Validation.Findings, findingJSON{Kind: finding.Kind(), Field: finding.Field(), Message: finding.Message()})
	}
	if contract.Approval != nil {
		projection.Approval = &approvalJSON{
			ID: contract.Approval.ID(), ArtifactRevisionID: contract.Approval.ArtifactRevisionID(),
			Verdict: contract.Approval.Verdict(), ActorID: contract.Approval.ActorID(),
			EvidenceDigest: contract.Approval.EvidenceDigest(), GovernanceContextID: contract.Approval.GovernanceContextID(),
			SubmittedAt: contract.Approval.SubmittedAt().Format(time.RFC3339Nano),
		}
	}
	return json.Marshal(projection)
}

// UnmarshalContract strictly decodes schema v1. Unknown fields and trailing
// JSON fail closed with stable diagnostic kinds instead of decoder prose.
func UnmarshalContract(encoded []byte) (Contract, error) {
	var projection contractJSON
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projection); err != nil {
		kind := ErrorInvalidValidation
		if strings.Contains(err.Error(), "unknown field") {
			kind = ErrorUnexpectedAuthority
		}
		return Contract{}, diagnostic(kind, "contract", RecoveryCorrectInput, "artifact contract is malformed", nil)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Contract{}, err
	}
	if projection.SchemaVersion != ContractSchemaVersion {
		return Contract{}, diagnostic(ErrorUnsupportedSchema, "schema_version", RecoveryCorrectInput, "artifact contract schema version is unsupported", nil)
	}
	createdAt, err := parseTimestamp("created_at", projection.ArtifactRevision.CreatedAt)
	if err != nil {
		return Contract{}, err
	}
	revision, err := NewArtifactRevision(ArtifactRevisionParams{
		ID: projection.ArtifactRevision.ID, MissionID: projection.ArtifactRevision.MissionID,
		Kind: projection.ArtifactRevision.Kind, RevisionNumber: projection.ArtifactRevision.RevisionNumber,
		ContentDigest: projection.ArtifactRevision.ContentDigest, SourceRef: projection.ArtifactRevision.SourceRef,
		SourceRevision: projection.ArtifactRevision.SourceRevision, CreatedBy: projection.ArtifactRevision.CreatedBy,
		CreatedAt: createdAt,
	})
	if err != nil {
		return Contract{}, err
	}
	policy, err := PolicyFor(revision.Kind())
	if err != nil {
		return Contract{}, err
	}
	findings := make([]Finding, 0, len(projection.Validation.Findings))
	for _, item := range projection.Validation.Findings {
		finding, findingErr := NewFinding(item.Kind, item.Field, item.Message, RecoveryCorrectInput)
		if findingErr != nil {
			return Contract{}, findingErr
		}
		findings = append(findings, finding)
	}
	validatedAt, err := parseTimestamp("validation.validated_at", projection.Validation.ValidatedAt)
	if err != nil {
		return Contract{}, err
	}
	validation, err := NewValidationResult(ValidationResultParams{
		ArtifactRevisionID: revision.ID(), Verdict: projection.Validation.Verdict,
		Findings: findings, Policy: policy, ValidatedAt: validatedAt,
	})
	if err != nil {
		return Contract{}, err
	}
	contract := Contract{SchemaVersion: ContractSchemaVersion, Revision: revision, Validation: validation}
	if projection.Approval != nil {
		submittedAt, timestampErr := parseTimestamp("approval.submitted_at", projection.Approval.SubmittedAt)
		if timestampErr != nil {
			return Contract{}, timestampErr
		}
		approval, approvalErr := NewArtifactApproval(ArtifactApprovalParams{
			ID: projection.Approval.ID, ArtifactRevisionID: projection.Approval.ArtifactRevisionID,
			Verdict: projection.Approval.Verdict, ActorID: projection.Approval.ActorID,
			EvidenceDigest:      projection.Approval.EvidenceDigest,
			GovernanceContextID: projection.Approval.GovernanceContextID,
			SubmittedAt:         submittedAt,
		})
		if approvalErr != nil {
			return Contract{}, approvalErr
		}
		contract.Approval = &approval
	}
	if err := validateContractBinding(contract); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

func validateContractBinding(contract Contract) error {
	if contract.Revision.ID() == "" {
		return diagnostic(ErrorInvalidIdentity, "artifact_revision_id", RecoveryCorrectInput, "artifact revision is absent", nil)
	}
	if contract.Validation.ArtifactRevisionID() != contract.Revision.ID() {
		return diagnostic(ErrorInvalidValidation, "validation.artifact_revision_id", RecoveryCorrectInput, "validation must name the enclosed artifact revision", nil)
	}
	expectedPolicy, err := PolicyFor(contract.Revision.Kind())
	if err != nil || !samePolicy(contract.Validation.Policy(), expectedPolicy) {
		return diagnostic(ErrorPolicyUnsatisfied, "validation.policy", RecoveryProvideEvidence, "validation policy does not match the enclosed artifact kind", nil)
	}
	if contract.Approval != nil && contract.Approval.ArtifactRevisionID() != contract.Revision.ID() {
		return diagnostic(ErrorApprovalNotAllowed, "approval.artifact_revision_id", RecoveryCorrectInput, "approval must name the enclosed artifact revision", nil)
	}
	return nil
}

func validateIdentity(field, value string) error {
	if !stableIdentity.MatchString(value) {
		return diagnostic(ErrorInvalidIdentity, field, RecoveryCorrectInput, "identity is blank or malformed", nil)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !lowerSHA256.MatchString(value) {
		return diagnostic(ErrorMalformedDigest, field, RecoveryCorrectInput, "digest must be lowercase SHA-256", nil)
	}
	return nil
}

func validateSourceRef(ref string) error {
	if ref == "" || strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, "\r\n\x00") {
		return diagnostic(ErrorInvalidProvenance, "source_ref", RecoveryCorrectInput, "source reference must be a bounded stable locator", nil)
	}
	if strings.HasPrefix(ref, "urn:") {
		if len(ref) > 256 || len(ref) <= len("urn:") || strings.ContainsAny(ref, " \t") {
			return diagnostic(ErrorInvalidProvenance, "source_ref", RecoveryCorrectInput, "structured source identity is malformed", nil)
		}
		return nil
	}
	if len(ref) > 256 || strings.Contains(ref, "://") || path.IsAbs(ref) || windowsVolume.MatchString(ref) || strings.Contains(ref, "\\") {
		return diagnostic(ErrorInvalidProvenance, "source_ref", RecoveryCorrectInput, "source reference must be repository-relative or structured", nil)
	}
	cleaned := path.Clean(ref)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != ref {
		return diagnostic(ErrorInvalidProvenance, "source_ref", RecoveryCorrectInput, "source reference must be normalized and repository-relative", nil)
	}
	return nil
}

func normalizeTime(field string, value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, diagnostic(ErrorInvalidTimestamp, field, RecoveryCorrectInput, "timestamp must be non-zero", nil)
	}
	return value.UTC(), nil
}

func parseTimestamp(field, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() {
		return time.Time{}, diagnostic(ErrorInvalidTimestamp, field, RecoveryCorrectInput, "timestamp must use RFC3339 with a non-zero value", nil)
	}
	return parsed.UTC(), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return diagnostic(ErrorUnexpectedAuthority, "contract", RecoveryCorrectInput, "artifact contract contains trailing data", nil)
}
