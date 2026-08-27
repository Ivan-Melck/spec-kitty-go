package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	maxFindingKindLength          = 64
	maxFindingFieldLength         = 128
	maxFindingMessageLength       = 512
	maxFindings                   = 64
	maxPolicyConcerns             = 32
	ValidationCommitmentAlgorithm = "sha256:spec-kitty-artifact-validation-v1"
	validationCommitmentDomain    = "spec-kitty/artifact-validation/v1\x00"
)

// ValidationVerdict is the closed validation decision vocabulary.
type ValidationVerdict string

const (
	ValidationPass ValidationVerdict = "pass"
	ValidationFail ValidationVerdict = "fail"
)

func (v ValidationVerdict) Validate() error {
	switch v {
	case ValidationPass, ValidationFail:
		return nil
	default:
		return diagnostic(ErrorInvalidValidation, "validation.verdict", RecoveryCorrectInput, "validation verdict is unsupported", nil)
	}
}

// Finding is immutable bounded validation evidence. It deliberately excludes
// artifact content, command output, and arbitrary metadata.
type Finding struct {
	kind     string
	field    string
	message  string
	recovery RecoveryCategory
}

func NewFinding(kind, field, message string, recovery RecoveryCategory) (Finding, error) {
	if err := validateBoundedText("finding.kind", kind, maxFindingKindLength); err != nil {
		return Finding{}, err
	}
	if err := validateBoundedText("finding.field", field, maxFindingFieldLength); err != nil {
		return Finding{}, err
	}
	if err := validateBoundedText("finding.message", message, maxFindingMessageLength); err != nil {
		return Finding{}, err
	}
	if containsSensitiveEvidence(message) {
		return Finding{}, diagnostic(ErrorInvalidFinding, "finding.message", RecoveryCorrectInput, "finding message contains prohibited private evidence", nil)
	}
	if err := recovery.Validate(); err != nil {
		return Finding{}, err
	}
	return Finding{kind: kind, field: field, message: message, recovery: recovery}, nil
}

func (f Finding) Kind() string               { return f.kind }
func (f Finding) Field() string              { return f.field }
func (f Finding) Message() string            { return f.message }
func (f Finding) Recovery() RecoveryCategory { return f.recovery }

func (f Finding) semanticKey() string {
	return f.kind + "\x00" + f.field + "\x00" + f.message + "\x00" + string(f.recovery)
}

// Concern is a typed structural concern, never a Markdown heading.
type Concern string

const (
	ConcernIntent             Concern = "intent"
	ConcernScope              Concern = "scope"
	ConcernRequirements       Concern = "requirements"
	ConcernAcceptanceFraming  Concern = "acceptance_framing"
	ConcernAssumptions        Concern = "assumptions"
	ConcernBoundaries         Concern = "boundaries"
	ConcernArchitecture       Concern = "architecture"
	ConcernDataFlow           Concern = "data_flow"
	ConcernRisk               Concern = "risk"
	ConcernCompatibility      Concern = "compatibility"
	ConcernMigration          Concern = "migration"
	ConcernValidationStrategy Concern = "validation_strategy"
)

var supportedConcerns = map[Concern]struct{}{
	ConcernIntent: {}, ConcernScope: {}, ConcernRequirements: {}, ConcernAcceptanceFraming: {},
	ConcernAssumptions: {}, ConcernBoundaries: {}, ConcernArchitecture: {}, ConcernDataFlow: {},
	ConcernRisk: {}, ConcernCompatibility: {}, ConcernMigration: {}, ConcernValidationStrategy: {},
}

// ValidationPolicy identifies the exact validator law applied to a revision.
// RequiredConcerns returns a defensive copy.
type ValidationPolicy struct {
	key      string
	revision string
	kind     Kind
	required []Concern
}

func NewValidationPolicy(key, revision string, kind Kind, required []Concern) (ValidationPolicy, error) {
	if err := validateIdentity("validation.policy_key", key); err != nil {
		return ValidationPolicy{}, err
	}
	if err := validateIdentity("validation.policy_revision", revision); err != nil {
		return ValidationPolicy{}, err
	}
	if err := kind.Validate(); err != nil {
		return ValidationPolicy{}, err
	}
	if len(required) > maxPolicyConcerns {
		return ValidationPolicy{}, diagnostic(ErrorInvalidValidation, "validation.policy.required_concerns", RecoveryCorrectInput, "validation policy has too many concerns", nil)
	}
	seen := make(map[Concern]struct{}, len(required))
	copyRequired := append([]Concern(nil), required...)
	for _, concern := range copyRequired {
		if _, ok := supportedConcerns[concern]; !ok {
			return ValidationPolicy{}, diagnostic(ErrorPolicyUnsatisfied, "validation.policy.required_concerns", RecoveryCorrectInput, "validation policy contains an unsupported concern", nil)
		}
		if _, duplicate := seen[concern]; duplicate {
			return ValidationPolicy{}, diagnostic(ErrorInvalidValidation, "validation.policy.required_concerns", RecoveryCorrectInput, "validation policy contains duplicate concerns", nil)
		}
		seen[concern] = struct{}{}
	}
	sort.Slice(copyRequired, func(i, j int) bool { return copyRequired[i] < copyRequired[j] })
	return ValidationPolicy{key: key, revision: revision, kind: kind, required: copyRequired}, nil
}

func (p ValidationPolicy) Key() string                 { return p.key }
func (p ValidationPolicy) Revision() string            { return p.revision }
func (p ValidationPolicy) Kind() Kind                  { return p.kind }
func (p ValidationPolicy) RequiredConcerns() []Concern { return append([]Concern(nil), p.required...) }

// PolicyFor returns the Phase 3 policy for an artifact kind.
func PolicyFor(kind Kind) (ValidationPolicy, error) {
	switch kind {
	case KindIntent:
		return NewValidationPolicy("intent-policy", "v1", kind, nil)
	case KindSpecification:
		return NewValidationPolicy("specification-policy", "v1", kind, []Concern{
			ConcernIntent, ConcernScope, ConcernRequirements, ConcernAcceptanceFraming,
			ConcernAssumptions, ConcernBoundaries,
		})
	case KindPlan:
		return NewValidationPolicy("plan-policy", "v1", kind, []Concern{
			ConcernArchitecture, ConcernDataFlow, ConcernRisk, ConcernCompatibility,
			ConcernMigration, ConcernValidationStrategy,
		})
	default:
		return ValidationPolicy{}, diagnostic(ErrorUnsupportedKind, "kind", RecoveryCorrectInput, "artifact kind has no validation policy", nil)
	}
}

func samePolicy(left, right ValidationPolicy) bool {
	if left.key != right.key || left.revision != right.revision || left.kind != right.kind || len(left.required) != len(right.required) {
		return false
	}
	for index := range left.required {
		if left.required[index] != right.required[index] {
			return false
		}
	}
	return true
}

// ValidationResultParams constructs exact-revision validation evidence.
type ValidationResultParams struct {
	ArtifactRevisionID string
	Verdict            ValidationVerdict
	Findings           []Finding
	Policy             ValidationPolicy
	ValidatedAt        time.Time
}

type ValidationResult struct {
	artifactRevisionID string
	verdict            ValidationVerdict
	findings           []Finding
	policy             ValidationPolicy
	validatedAt        time.Time
}

func NewValidationResult(params ValidationResultParams) (ValidationResult, error) {
	if err := validateIdentity("validation.artifact_revision_id", params.ArtifactRevisionID); err != nil {
		return ValidationResult{}, err
	}
	if err := params.Verdict.Validate(); err != nil {
		return ValidationResult{}, err
	}
	if _, err := NewValidationPolicy(params.Policy.key, params.Policy.revision, params.Policy.kind, params.Policy.required); err != nil {
		return ValidationResult{}, err
	}
	if len(params.Findings) > maxFindings {
		return ValidationResult{}, diagnostic(ErrorInvalidValidation, "validation.findings", RecoveryCorrectInput, "validation has too many findings", nil)
	}
	if params.Verdict == ValidationPass && len(params.Findings) != 0 {
		return ValidationResult{}, diagnostic(ErrorInvalidValidation, "validation.findings", RecoveryCorrectInput, "passing validation cannot contain findings", nil)
	}
	if params.Verdict == ValidationFail && len(params.Findings) == 0 {
		return ValidationResult{}, diagnostic(ErrorInvalidValidation, "validation.findings", RecoveryCorrectInput, "failing validation requires at least one finding", nil)
	}
	findings := append([]Finding(nil), params.Findings...)
	for _, finding := range findings {
		if _, err := NewFinding(finding.kind, finding.field, finding.message, finding.recovery); err != nil {
			return ValidationResult{}, err
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].semanticKey() < findings[j].semanticKey() })
	for index := 1; index < len(findings); index++ {
		if findings[index-1].semanticKey() == findings[index].semanticKey() {
			return ValidationResult{}, diagnostic(ErrorInvalidValidation, "validation.findings", RecoveryCorrectInput, "validation contains duplicate semantic findings", nil)
		}
	}
	validatedAt, err := normalizeTime("validation.validated_at", params.ValidatedAt)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{
		artifactRevisionID: params.ArtifactRevisionID, verdict: params.Verdict,
		findings: findings, policy: params.Policy, validatedAt: validatedAt,
	}, nil
}

func (v ValidationResult) ArtifactRevisionID() string { return v.artifactRevisionID }
func (v ValidationResult) Verdict() ValidationVerdict { return v.verdict }
func (v ValidationResult) Findings() []Finding        { return append([]Finding(nil), v.findings...) }
func (v ValidationResult) Policy() ValidationPolicy   { return v.policy }
func (v ValidationResult) ValidatedAt() time.Time     { return v.validatedAt }

// Commitment returns the canonical commitment for complete validation
// evidence. Constructor sorting makes presentation order irrelevant.
func (v ValidationResult) Commitment() (string, error) {
	type canonicalFinding struct {
		Kind     string           `json:"kind"`
		Field    string           `json:"field"`
		Message  string           `json:"message"`
		Recovery RecoveryCategory `json:"recovery"`
	}
	identity := struct {
		ArtifactRevisionID string             `json:"artifact_revision_id"`
		Verdict            ValidationVerdict  `json:"verdict"`
		PolicyKey          string             `json:"policy_key"`
		PolicyRevision     string             `json:"policy_revision"`
		ValidatedAt        time.Time          `json:"validated_at"`
		Findings           []canonicalFinding `json:"findings"`
	}{
		ArtifactRevisionID: v.artifactRevisionID, Verdict: v.verdict,
		PolicyKey: v.policy.key, PolicyRevision: v.policy.revision,
		ValidatedAt: v.validatedAt.UTC(), Findings: make([]canonicalFinding, 0, len(v.findings)),
	}
	for _, finding := range v.findings {
		identity.Findings = append(identity.Findings, canonicalFinding{
			Kind: finding.kind, Field: finding.field, Message: finding.message, Recovery: finding.recovery,
		})
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", diagnostic(ErrorInvalidValidation, "validation", RecoveryCorrectInput, "validation commitment could not be encoded", nil)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(validationCommitmentDomain))
	_, _ = hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ValidateConcerns applies one typed structural policy. It never inspects file
// paths, Markdown, or artifact bytes.
func ValidateConcerns(artifactRevisionID string, policy ValidationPolicy, present []Concern, validatedAt time.Time) (ValidationResult, error) {
	seen := make(map[Concern]struct{}, len(present))
	for _, concern := range present {
		if _, ok := supportedConcerns[concern]; !ok {
			return ValidationResult{}, diagnostic(ErrorPolicyUnsatisfied, "validation.concerns", RecoveryCorrectInput, "validation input contains an unsupported concern", nil)
		}
		seen[concern] = struct{}{}
	}
	findings := make([]Finding, 0)
	for _, required := range policy.required {
		if _, ok := seen[required]; ok {
			continue
		}
		finding, err := NewFinding("missing_required_concern", string(required), "required structural concern is absent", RecoveryProvideEvidence)
		if err != nil {
			return ValidationResult{}, err
		}
		findings = append(findings, finding)
	}
	verdict := ValidationPass
	if len(findings) != 0 {
		verdict = ValidationFail
	}
	return NewValidationResult(ValidationResultParams{
		ArtifactRevisionID: artifactRevisionID, Verdict: verdict, Findings: findings,
		Policy: policy, ValidatedAt: validatedAt,
	})
}

func validateBoundedText(field, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || strings.ContainsAny(value, "\r\n\x00") {
		return diagnostic(ErrorInvalidFinding, field, RecoveryCorrectInput, "finding field is blank, malformed, or above its bound", nil)
	}
	return nil
}

func containsSensitiveEvidence(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"begin private key", "password=", "password:", "secret=", "secret:",
		"access_token", "authorization: bearer", "private transcript", "transcript:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
