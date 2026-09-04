package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	ArtifactEventSchemaV2  = uint32(1)
	ArtifactReducerV2      = uint32(1)
	artifactStateSchemaV2  = uint16(1)
	artifactResultSchemaV2 = uint32(1)
	artifactContractV2     = uint16(2)
)

// ContentCommitmentV2 can only be constructed from the exact artifact bytes.
// The private preimage length prevents a caller from presenting an arbitrary
// digest string as evidence that bytes were actually observed.
type ContentCommitmentV2 struct {
	digest string
	bytes  int
}

func CommitContentV2(content []byte) (ContentCommitmentV2, error) {
	if len(content) == 0 || len(content) > journalv2.MaxCanonicalPayloadBytes {
		return ContentCommitmentV2{}, diagnostic(ErrorInvalidRevision, "content", RecoveryCorrectInput, "artifact content is empty or over budget", nil)
	}
	preimage := make([]byte, 0, len(contentCommitmentDomain)+len(content))
	preimage = append(preimage, contentCommitmentDomain...)
	preimage = append(preimage, content...)
	sum := sha256.Sum256(preimage)
	return ContentCommitmentV2{digest: fmt.Sprintf("%x", sum), bytes: len(content)}, nil
}

func (commitment ContentCommitmentV2) Digest() string { return commitment.digest }

type ArtifactCatalogStateV2 struct {
	SchemaVersion        uint16 `json:"schema_version"`
	MissionID            string `json:"mission_id"`
	Kind                 Kind   `json:"kind"`
	LogicalVersion       uint64 `json:"logical_version"`
	LatestRevisionID     string `json:"latest_revision_id"`
	LatestRevisionNumber uint64 `json:"latest_revision_number"`
}

type ArtifactRevisionStateV2 struct {
	SchemaVersion  uint16          `json:"schema_version"`
	LogicalVersion uint64          `json:"logical_version"`
	Contract       json.RawMessage `json:"contract"`
}

type catalogEventV2 struct {
	SchemaVersion        uint16 `json:"schema_version"`
	MissionID            string `json:"mission_id"`
	Kind                 Kind   `json:"kind"`
	LatestRevisionID     string `json:"latest_revision_id"`
	LatestRevisionNumber uint64 `json:"latest_revision_number"`
}

type revisionEventV2 struct {
	SchemaVersion uint16          `json:"schema_version"`
	Kind          string          `json:"kind"`
	Contract      json.RawMessage `json:"contract"`
}

type artifactAuthorityContractV2 struct {
	SchemaVersion uint16                        `json:"schema_version"`
	Revision      artifactAuthorityRevisionV2   `json:"artifact_revision"`
	Validation    artifactAuthorityValidationV2 `json:"validation"`
	Approval      *artifactAuthorityApprovalV2  `json:"approval,omitempty"`
}

type artifactAuthorityRevisionV2 struct {
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
type artifactAuthorityPolicyV2 struct {
	Key              string    `json:"key"`
	Revision         string    `json:"revision"`
	Kind             Kind      `json:"kind"`
	RequiredConcerns []Concern `json:"required_concerns"`
}
type artifactAuthorityFindingV2 struct {
	Kind     string           `json:"kind"`
	Field    string           `json:"field"`
	Message  string           `json:"message"`
	Recovery RecoveryCategory `json:"recovery"`
}
type artifactAuthorityValidationV2 struct {
	ArtifactRevisionID string                       `json:"artifact_revision_id"`
	Verdict            ValidationVerdict            `json:"verdict"`
	Policy             artifactAuthorityPolicyV2    `json:"policy"`
	Findings           []artifactAuthorityFindingV2 `json:"findings"`
	ValidatedAt        string                       `json:"validated_at"`
	Commitment         string                       `json:"commitment"`
}
type artifactAuthorityApprovalV2 struct {
	ID                  string          `json:"approval_id"`
	ArtifactRevisionID  string          `json:"artifact_revision_id"`
	Verdict             ApprovalVerdict `json:"verdict"`
	ActorID             string          `json:"actor_id"`
	EvidenceDigest      string          `json:"evidence_digest"`
	GovernanceContextID string          `json:"governance_context_id"`
	SubmittedAt         string          `json:"submitted_at"`
}

func encodeArtifactAuthorityContractV2(contract Contract) ([]byte, error) {
	if err := validateContractBinding(contract); err != nil {
		return nil, err
	}
	policy := contract.Validation.Policy()
	commitment, err := contract.Validation.Commitment()
	if err != nil {
		return nil, err
	}
	wire := artifactAuthorityContractV2{SchemaVersion: artifactContractV2,
		Revision:   artifactAuthorityRevisionV2{contract.Revision.ID(), contract.Revision.MissionID(), contract.Revision.Kind(), contract.Revision.RevisionNumber(), contract.Revision.ContentDigest(), contract.Revision.SourceRef(), contract.Revision.SourceRevision(), contract.Revision.CreatedBy(), contract.Revision.CreatedAt().Format("2006-01-02T15:04:05.999999999Z07:00")},
		Validation: artifactAuthorityValidationV2{ArtifactRevisionID: contract.Validation.ArtifactRevisionID(), Verdict: contract.Validation.Verdict(), Policy: artifactAuthorityPolicyV2{policy.Key(), policy.Revision(), policy.Kind(), policy.RequiredConcerns()}, Findings: make([]artifactAuthorityFindingV2, 0, len(contract.Validation.Findings())), ValidatedAt: contract.Validation.ValidatedAt().Format("2006-01-02T15:04:05.999999999Z07:00"), Commitment: commitment}}
	for _, finding := range contract.Validation.Findings() {
		wire.Validation.Findings = append(wire.Validation.Findings, artifactAuthorityFindingV2{finding.Kind(), finding.Field(), finding.Message(), finding.Recovery()})
	}
	if contract.Approval != nil {
		wire.Approval = &artifactAuthorityApprovalV2{contract.Approval.ID(), contract.Approval.ArtifactRevisionID(), contract.Approval.Verdict(), contract.Approval.ActorID(), contract.Approval.EvidenceDigest(), contract.Approval.GovernanceContextID(), contract.Approval.SubmittedAt().Format("2006-01-02T15:04:05.999999999Z07:00")}
	}
	return encodeArtifactValueV2(wire)
}

func decodeArtifactAuthorityContractV2(encoded []byte) (Contract, error) {
	var wire artifactAuthorityContractV2
	if err := strictArtifactJSON(encoded, &wire); err != nil || wire.SchemaVersion != artifactContractV2 {
		return Contract{}, diagnostic(ErrorUnsupportedSchema, "authority_contract", RecoveryInspectState, "artifact authority contract is malformed", nil)
	}
	createdAt, err := parseTimestamp("created_at", wire.Revision.CreatedAt)
	if err != nil {
		return Contract{}, err
	}
	revision, err := NewArtifactRevision(ArtifactRevisionParams{ID: wire.Revision.ID, MissionID: wire.Revision.MissionID, Kind: wire.Revision.Kind, RevisionNumber: wire.Revision.RevisionNumber, ContentDigest: wire.Revision.ContentDigest, SourceRef: wire.Revision.SourceRef, SourceRevision: wire.Revision.SourceRevision, CreatedBy: wire.Revision.CreatedBy, CreatedAt: createdAt})
	if err != nil {
		return Contract{}, err
	}
	policy, err := NewValidationPolicy(wire.Validation.Policy.Key, wire.Validation.Policy.Revision, wire.Validation.Policy.Kind, wire.Validation.Policy.RequiredConcerns)
	if err != nil {
		return Contract{}, err
	}
	findings := make([]Finding, 0, len(wire.Validation.Findings))
	for _, item := range wire.Validation.Findings {
		finding, findingErr := NewFinding(item.Kind, item.Field, item.Message, item.Recovery)
		if findingErr != nil {
			return Contract{}, findingErr
		}
		findings = append(findings, finding)
	}
	validatedAt, err := parseTimestamp("validated_at", wire.Validation.ValidatedAt)
	if err != nil {
		return Contract{}, err
	}
	validation, err := NewValidationResult(ValidationResultParams{ArtifactRevisionID: wire.Validation.ArtifactRevisionID, Verdict: wire.Validation.Verdict, Findings: findings, Policy: policy, ValidatedAt: validatedAt})
	if err != nil {
		return Contract{}, err
	}
	wantCommitment, err := validation.Commitment()
	if err != nil || wantCommitment != wire.Validation.Commitment {
		return Contract{}, diagnostic(ErrorInvalidValidation, "validation.commitment", RecoveryInspectState, "artifact validation commitment is mismatched", nil)
	}
	contract := Contract{SchemaVersion: ContractSchemaVersion, Revision: revision, Validation: validation}
	if wire.Approval != nil {
		submittedAt, timestampErr := parseTimestamp("submitted_at", wire.Approval.SubmittedAt)
		if timestampErr != nil {
			return Contract{}, timestampErr
		}
		approval, approvalErr := NewArtifactApproval(ArtifactApprovalParams{ID: wire.Approval.ID, ArtifactRevisionID: wire.Approval.ArtifactRevisionID, Verdict: wire.Approval.Verdict, ActorID: wire.Approval.ActorID, EvidenceDigest: wire.Approval.EvidenceDigest, GovernanceContextID: wire.Approval.GovernanceContextID, SubmittedAt: submittedAt})
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

func strictArtifactJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing artifact JSON")
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return fmt.Errorf("artifact JSON is not canonical")
	}
	return nil
}

func encodeArtifactValueV2(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeArtifactCatalogStateV2(encoded []byte) (ArtifactCatalogStateV2, error) {
	var state ArtifactCatalogStateV2
	if err := strictArtifactJSON(encoded, &state); err != nil || state.SchemaVersion != artifactStateSchemaV2 || state.LogicalVersion == 0 || state.LatestRevisionNumber == 0 || validateIdentity("artifact_revision_id", state.LatestRevisionID) != nil || validateIdentity("mission_id", state.MissionID) != nil || state.Kind.Validate() != nil {
		return ArtifactCatalogStateV2{}, diagnostic(ErrorInvalidRevision, "artifact_catalog_state", RecoveryInspectState, "artifact catalog state is malformed", nil)
	}
	return state, nil
}

func DecodeArtifactRevisionStateV2(encoded []byte) (ArtifactRevisionStateV2, error) {
	var state ArtifactRevisionStateV2
	if err := strictArtifactJSON(encoded, &state); err != nil || state.SchemaVersion != artifactStateSchemaV2 || state.LogicalVersion == 0 {
		return ArtifactRevisionStateV2{}, diagnostic(ErrorInvalidRevision, "artifact_revision_state", RecoveryInspectState, "artifact revision state is malformed", nil)
	}
	if _, err := decodeArtifactAuthorityContractV2(state.Contract); err != nil {
		return ArtifactRevisionStateV2{}, err
	}
	return state, nil
}

// ReduceArtifactCatalogEventV2 deterministically advances the current catalog
// pointer. Exact revision verdicts remain in the distinct revision aggregate.
func ReduceArtifactCatalogEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	coordinate := event.Coordinate()
	if event.Family() != "artifact_catalog" || event.EventSchemaVersion() != ArtifactEventSchemaV2 || event.ReducerVersion() != ArtifactReducerV2 || coordinate.Kind != "artifact_catalog" {
		return nil, diagnostic(ErrorUnsupportedSchema, "event_tuple", RecoveryInspectState, "artifact catalog event tuple is unsupported", nil)
	}
	var payload catalogEventV2
	if err := strictArtifactJSON(event.Payload(), &payload); err != nil || payload.SchemaVersion != artifactStateSchemaV2 || payload.LatestRevisionNumber == 0 || payload.Kind.Validate() != nil || validateIdentity("mission_id", payload.MissionID) != nil || validateIdentity("artifact_revision_id", payload.LatestRevisionID) != nil {
		return nil, diagnostic(ErrorInvalidRevision, "event_payload", RecoveryInspectState, "artifact catalog event is malformed", nil)
	}
	if coordinate.Scope.Kind != journalv2.ScopeMission || string(coordinate.Scope.MissionID) != payload.MissionID || string(coordinate.ID) != string(payload.Kind) {
		return nil, diagnostic(ErrorInvalidRevision, "event_scope", RecoveryInspectState, "artifact catalog event scope is mismatched", nil)
	}
	wantVersion := uint64(event.LogicalVersion())
	if artifactReducerInitialV2(prior) {
		if wantVersion != 1 || payload.LatestRevisionNumber != 1 {
			return nil, diagnostic(ErrorInvalidRevision, "logical_version", RecoveryResolveConflict, "first artifact catalog transition is invalid", nil)
		}
	} else {
		state, err := DecodeArtifactCatalogStateV2(prior)
		if err != nil || state.LogicalVersion+1 != wantVersion || state.MissionID != payload.MissionID || state.Kind != payload.Kind || state.LatestRevisionNumber+1 != payload.LatestRevisionNumber || state.LatestRevisionID == payload.LatestRevisionID {
			return nil, diagnostic(ErrorInvalidRevision, "logical_version", RecoveryResolveConflict, "artifact catalog transition is stale or non-monotonic", nil)
		}
	}
	return encodeArtifactValueV2(ArtifactCatalogStateV2{artifactStateSchemaV2, payload.MissionID, payload.Kind, wantVersion, payload.LatestRevisionID, payload.LatestRevisionNumber})
}

// ReduceArtifactRevisionEventV2 creates one immutable revision aggregate and
// may append exactly one verdict bound to that same revision.
func ReduceArtifactRevisionEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	coordinate := event.Coordinate()
	if event.Family() != "artifact_revision" || event.EventSchemaVersion() != ArtifactEventSchemaV2 || event.ReducerVersion() != ArtifactReducerV2 || coordinate.Kind != "artifact_revision" {
		return nil, diagnostic(ErrorUnsupportedSchema, "event_tuple", RecoveryInspectState, "artifact revision event tuple is unsupported", nil)
	}
	var payload revisionEventV2
	if err := strictArtifactJSON(event.Payload(), &payload); err != nil || payload.SchemaVersion != artifactStateSchemaV2 {
		return nil, diagnostic(ErrorInvalidRevision, "event_payload", RecoveryInspectState, "artifact revision event is malformed", nil)
	}
	contract, err := decodeArtifactAuthorityContractV2(payload.Contract)
	if err != nil || contract.Revision.ID() != string(coordinate.ID) || coordinate.Scope.Kind != journalv2.ScopeMission || string(coordinate.Scope.MissionID) != contract.Revision.MissionID() {
		return nil, diagnostic(ErrorInvalidRevision, "contract", RecoveryInspectState, "artifact revision event contract is mismatched", nil)
	}
	wantVersion := uint64(event.LogicalVersion())
	switch payload.Kind {
	case "published":
		if !artifactReducerInitialV2(prior) || wantVersion != 1 || contract.Approval != nil {
			return nil, diagnostic(ErrorInvalidRevision, "logical_version", RecoveryResolveConflict, "artifact revision publication is not immutable", nil)
		}
	case "decided":
		if len(prior) == 0 || wantVersion < 2 || contract.Approval == nil {
			return nil, diagnostic(ErrorApprovalNotAllowed, "approval", RecoveryResolveConflict, "artifact decision lacks prior exact-revision authority", nil)
		}
		previous, err := DecodeArtifactRevisionStateV2(prior)
		if err != nil || previous.LogicalVersion+1 != wantVersion {
			return nil, diagnostic(ErrorInvalidRevision, "logical_version", RecoveryResolveConflict, "artifact decision is stale", nil)
		}
		old, err := decodeArtifactAuthorityContractV2(previous.Contract)
		undecided, encodeErr := encodeArtifactAuthorityContractV2(Contract{SchemaVersion: ContractSchemaVersion, Revision: contract.Revision, Validation: contract.Validation})
		if err != nil || encodeErr != nil || old.Approval != nil || !bytes.Equal(previous.Contract, undecided) {
			return nil, diagnostic(ErrorApprovalNotAllowed, "approval", RecoveryResolveConflict, "artifact decision rewrites immutable revision authority", nil)
		}
	default:
		return nil, diagnostic(ErrorUnsupportedSchema, "event_kind", RecoveryInspectState, "artifact revision event kind is unsupported", nil)
	}
	return encodeArtifactValueV2(ArtifactRevisionStateV2{artifactStateSchemaV2, wantVersion, append(json.RawMessage(nil), payload.Contract...)})
}

func artifactReducerInitialV2(prior []byte) bool {
	return len(prior) == 0 || bytes.Equal(prior, []byte(`{}`))
}

// ReducerRegistrationsV2 binds restore to the exact admitted artifact reducer
// implementations. The digest changes only with an intentional reducer-law
// revision and is checkpoint compatibility evidence, not a provider version.
func ReducerRegistrationsV2() []journalv2.ReducerRegistration {
	digestBytes := sha256.Sum256([]byte("spec-kitty/artifact-authority-reducers/catalog-1.1/revision-1.1/full-validation-evidence"))
	digest, _ := journalv2.ParseDigest("artifact_reducer_implementation", fmt.Sprintf("%x", digestBytes))
	return []journalv2.ReducerRegistration{
		{Family: "artifact_catalog", EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2, Owner: "internal/artifact", ImplementationDigest: digest, Reduce: ReduceArtifactCatalogEventV2},
		{Family: "artifact_revision", EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2, Owner: "internal/artifact", ImplementationDigest: digest, Reduce: ReduceArtifactRevisionEventV2},
	}
}

type AuthorityContextV2 struct {
	ProjectID         journalv2.ProjectID
	OperationID       journalv2.OperationID
	RuntimeEpochID    journalv2.RuntimeEpochID
	ActorID           journalv2.ActorID
	ProcessID         journalv2.ProcessID
	DoctrineRevision  string
	ProfileRevision   string
	PolicyRevision    string
	Governance        []string
	ObservedCreatedAt string
	OperationEventID  journalv2.EventID
	CatalogEventID    journalv2.EventID
	RevisionEventID   journalv2.EventID
}

type PublishPlanInputV2 struct {
	Context                       AuthorityContextV2
	Revision                      ArtifactRevision
	Validation                    ValidationResult
	Content                       ContentCommitmentV2
	PriorCatalogState             []byte
	ExpectedCatalogRecordRevision uint64
}

type DecisionPlanInputV2 struct {
	Context                        AuthorityContextV2
	PriorRevisionState             []byte
	Approval                       ArtifactApproval
	ExpectedRevisionRecordRevision uint64
}

// AuthorityPlanV2 is the only artifact plan capability adapters may publish.
// Its sealed foundation plan cannot be caller-constructed; only artifact
// command builders below can mint this wrapper.
type AuthorityPlanV2 struct {
	sealed journalv2.SealedCommitPlanV2
}

func (p AuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorInvalidCommand, "authority_plan", RecoveryCorrectInput, "artifact authority plan is absent", nil)
	}
	return p.sealed, nil
}

func BuildArtifactPublishAuthorityV2(input PublishPlanInputV2) (AuthorityPlanV2, error) {
	sealed, err := BuildArtifactPublishPlanV2(input)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	return AuthorityPlanV2{sealed: sealed}, nil
}

func BuildArtifactDecisionAuthorityV2(input DecisionPlanInputV2) (AuthorityPlanV2, error) {
	sealed, err := BuildArtifactDecisionPlanV2(input)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	return AuthorityPlanV2{sealed: sealed}, nil
}

func BuildArtifactPublishPlanV2(input PublishPlanInputV2) (journalv2.SealedCommitPlanV2, error) {
	if input.Content.bytes == 0 || input.Content.digest != input.Revision.ContentDigest() {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorInvalidRevision, "content_digest", RecoveryCorrectInput, "artifact bytes do not match the immutable revision commitment", nil)
	}
	contract, err := encodeArtifactAuthorityContractV2(Contract{SchemaVersion: ContractSchemaVersion, Revision: input.Revision, Validation: input.Validation})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	version := uint64(1)
	if len(input.PriorCatalogState) != 0 {
		prior, err := DecodeArtifactCatalogStateV2(input.PriorCatalogState)
		if err != nil || prior.MissionID != input.Revision.MissionID() || prior.Kind != input.Revision.Kind() || prior.LatestRevisionNumber+1 != input.Revision.RevisionNumber() {
			return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorInvalidRevision, "expected_latest_revision", RecoveryResolveConflict, "artifact catalog expectation is stale", nil)
		}
		version = prior.LogicalVersion + 1
	} else if input.Revision.RevisionNumber() != 1 {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorInvalidRevision, "revision_number", RecoveryResolveConflict, "first artifact revision must be one", nil)
	}
	catalogPayload, err := encodeArtifactValueV2(catalogEventV2{artifactStateSchemaV2, input.Revision.MissionID(), input.Revision.Kind(), input.Revision.ID(), input.Revision.RevisionNumber()})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	revisionPayload, err := encodeArtifactValueV2(revisionEventV2{artifactStateSchemaV2, "published", contract})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	catalogState, err := reduceArtifactDraftV2("artifact_catalog", version, catalogPayload, input.PriorCatalogState, input.Revision)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	revisionState, err := reduceArtifactDraftV2("artifact_revision", 1, revisionPayload, nil, input.Revision)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	return sealArtifactPlanV2(input.Context, input.Revision, version, input.PriorCatalogState, catalogPayload, revisionPayload, catalogState, revisionState, input.ExpectedCatalogRecordRevision)
}

// BuildArtifactDecisionPlanV2 appends one exact-revision verdict. It never
// consults the mutable catalog head, so publishing a later revision cannot
// transfer or reinterpret this decision.
func BuildArtifactDecisionPlanV2(input DecisionPlanInputV2) (journalv2.SealedCommitPlanV2, error) {
	prior, err := DecodeArtifactRevisionStateV2(input.PriorRevisionState)
	if err != nil || input.ExpectedRevisionRecordRevision == 0 {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorInvalidRevision, "artifact_revision", RecoveryResolveConflict, "artifact decision requires an exact observed revision head", nil)
	}
	contract, err := decodeArtifactAuthorityContractV2(prior.Contract)
	if err != nil || contract.Approval != nil || input.Approval.ArtifactRevisionID() != contract.Revision.ID() {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorApprovalNotAllowed, "approval", RecoveryResolveConflict, "approval does not bind the undecided revision", nil)
	}
	if input.Approval.Verdict() == VerdictApproved && contract.Validation.Verdict() != ValidationPass {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorValidationRequired, "validation", RecoveryProvideEvidence, "approval requires passing exact-revision validation", nil)
	}
	governanceBound := false
	for _, revision := range input.Context.Governance {
		if revision == input.Approval.GovernanceContextID() {
			governanceBound = true
			break
		}
	}
	if !governanceBound {
		return journalv2.SealedCommitPlanV2{}, diagnostic(ErrorApprovalNotAllowed, "governance_context_id", RecoveryCorrectInput, "approval governance is not bound to the operation evidence", nil)
	}
	decided, err := encodeArtifactAuthorityContractV2(Contract{SchemaVersion: ContractSchemaVersion, Revision: contract.Revision, Validation: contract.Validation, Approval: &input.Approval})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	payload, err := encodeArtifactValueV2(revisionEventV2{artifactStateSchemaV2, "decided", decided})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	next, err := reduceArtifactDraftV2("artifact_revision", prior.LogicalVersion+1, payload, input.PriorRevisionState, contract.Revision)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	return sealArtifactDecisionPlanV2(input.Context, contract.Revision, prior.LogicalVersion+1, payload, next, input.ExpectedRevisionRecordRevision)
}

func sealArtifactDecisionPlanV2(context AuthorityContextV2, revision ArtifactRevision, revisionVersion uint64, payload, state []byte, expectedRecordRevision uint64) (journalv2.SealedCommitPlanV2, error) {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: context.ProjectID, MissionID: journalv2.MissionID(revision.MissionID())}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "artifact.decide", CommandSchemaVersion: 1, Scope: scope, Command: payload})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: context.OperationID, CommandKind: "artifact.decide", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: context.ActorID, ProcessID: context.ProcessID,
		DoctrineRevision: context.DoctrineRevision, ProfileRevision: context.ProfileRevision, PolicyRevision: context.PolicyRevision, OriginatingRuntimeEpochID: context.RuntimeEpochID, ObservedCreatedAt: context.ObservedCreatedAt})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	eventIDs := []journalv2.EventID{context.OperationEventID, context.RevisionEventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	result, _ := encodeArtifactValueV2(struct {
		ArtifactRevisionID string `json:"artifact_revision_id"`
		DecisionRecorded   bool   `json:"decision_recorded"`
	}{revision.ID(), true})
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: context.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "artifact.decision.result", ResultSchemaVersion: artifactResultSchemaV2, CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(context.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	revisionCoordinate, _ := journalv2.NewAggregateCoordinate("artifact_revision", journalv2.AggregateID(revision.ID()), scope)
	events := []journalv2.EventDraftV2{
		{EventID: context.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: opPayload},
		{EventID: context.RevisionEventID, EventType: "artifact_revision.decided", Family: "artifact_revision", EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2, Coordinate: revisionCoordinate, AggregateLogicalVersion: journalv2.LogicalVersion(revisionVersion), CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: payload},
	}
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	revisionDigest, _ := journalv2.ReducerStateDigestV2(state)
	opSummary, _ := journalv2.CanonicalObject([]byte(`{"status":"resolved"}`))
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: context.OperationEventID, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opSummary},
		{Coordinate: revisionCoordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(revisionVersion - 1), NextLogicalVersion: journalv2.LogicalVersion(revisionVersion), EventID: context.RevisionEventID, ExpectedRecordRevision: expectedRecordRevision, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: revisionDigest, AuthoritySummary: state},
	}
	conditions := make([]journalv2.ProviderCondition, 0, 2)
	for _, head := range heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		conditions = append(conditions, condition)
	}
	return journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: events, Heads: heads, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
}

func reduceArtifactDraftV2(family string, version uint64, payload, prior []byte, revision ArtifactRevision) ([]byte, error) {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-draft", MissionID: journalv2.MissionID(revision.MissionID())}
	id := revision.ID()
	if family == "artifact_catalog" {
		id = string(revision.Kind())
	}
	coordinate, _ := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(family), journalv2.AggregateID(id), scope)
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-artifact-draft", EventType: family + ".draft", Family: family, EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2,
		Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(version), OperationID: "operation-artifact-draft", OperationLogicalVersion: 1, PublicationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CommittedRuntimeEpochID: "epoch-draft", EventIndex: 0, EventCount: 1, Payload: payload}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		return nil, err
	}
	if family == "artifact_catalog" {
		return ReduceArtifactCatalogEventV2(prior, envelope)
	}
	return ReduceArtifactRevisionEventV2(prior, envelope)
}

func sealArtifactPlanV2(context AuthorityContextV2, revision ArtifactRevision, catalogVersion uint64, priorCatalogState, catalogPayload, revisionPayload, catalogState, revisionState []byte, expectedCatalogRevision uint64) (journalv2.SealedCommitPlanV2, error) {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: context.ProjectID, MissionID: journalv2.MissionID(revision.MissionID())}
	expectedPreviousID := ""
	if len(priorCatalogState) != 0 {
		prior, err := DecodeArtifactCatalogStateV2(priorCatalogState)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		expectedPreviousID = prior.LatestRevisionID
	}
	intentCommand, err := encodeArtifactValueV2(struct {
		ExpectedPreviousRevisionID string          `json:"expected_previous_revision_id,omitempty"`
		CatalogTransition          json.RawMessage `json:"catalog_transition"`
		RevisionPublication        json.RawMessage `json:"revision_publication"`
	}{expectedPreviousID, catalogPayload, revisionPayload})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "artifact.publish", CommandSchemaVersion: 1, Scope: scope, Command: intentCommand})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: context.OperationID, CommandKind: "artifact.publish", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: context.ActorID, ProcessID: context.ProcessID,
		DoctrineRevision: context.DoctrineRevision, ProfileRevision: context.ProfileRevision, PolicyRevision: context.PolicyRevision, OriginatingRuntimeEpochID: context.RuntimeEpochID, ObservedCreatedAt: context.ObservedCreatedAt})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	eventIDs := []journalv2.EventID{context.OperationEventID, context.CatalogEventID, context.RevisionEventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	result, _ := encodeArtifactValueV2(struct {
		ArtifactRevisionID string `json:"artifact_revision_id"`
		ContentDigest      string `json:"content_digest"`
	}{revision.ID(), revision.ContentDigest()})
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: context.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "artifact.publish.result", ResultSchemaVersion: artifactResultSchemaV2, CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	operationPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(context.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	catalogCoordinate, _ := journalv2.NewAggregateCoordinate("artifact_catalog", journalv2.AggregateID(revision.Kind()), scope)
	revisionCoordinate, _ := journalv2.NewAggregateCoordinate("artifact_revision", journalv2.AggregateID(revision.ID()), scope)
	events := []journalv2.EventDraftV2{
		{EventID: context.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: operationPayload},
		{EventID: context.CatalogEventID, EventType: "artifact_catalog.advanced", Family: "artifact_catalog", EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2, Coordinate: catalogCoordinate, AggregateLogicalVersion: journalv2.LogicalVersion(catalogVersion), CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: catalogPayload},
		{EventID: context.RevisionEventID, EventType: "artifact_revision.published", Family: "artifact_revision", EventSchemaVersion: ArtifactEventSchemaV2, ReducerVersion: ArtifactReducerV2, Coordinate: revisionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: revisionPayload},
	}
	opDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	catalogDigest, _ := journalv2.ReducerStateDigestV2(catalogState)
	revisionDigest, _ := journalv2.ReducerStateDigestV2(revisionState)
	opSummary, _ := journalv2.CanonicalObject([]byte(`{"status":"resolved"}`))
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: context.OperationEventID, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opSummary},
		{Coordinate: catalogCoordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(catalogVersion - 1), NextLogicalVersion: journalv2.LogicalVersion(catalogVersion), EventID: context.CatalogEventID, ExpectedRecordRevision: expectedCatalogRevision, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: catalogDigest, AuthoritySummary: catalogState},
		{Coordinate: revisionCoordinate, NextLogicalVersion: 1, EventID: context.RevisionEventID, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: revisionDigest, AuthoritySummary: revisionState},
	}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		conditions = append(conditions, condition)
	}
	return journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: events, Heads: heads, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
}

func ResolveArtifactRetryV2(candidate journalv2.OperationBindingV2, existingBinding []byte, bindingPresent bool, existingState *journalv2.OperationStateV2, currentEpoch journalv2.RuntimeEpochID) (journalv2.RetryResolution, error) {
	return journalv2.ResolveRetryV2(candidate, existingBinding, bindingPresent, existingState, currentEpoch)
}
