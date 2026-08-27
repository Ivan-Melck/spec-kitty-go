package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestArtifactRevisionCommitmentAndImmutableValues(t *testing.T) {
	content := []byte("exact specification bytes")
	want := ContentCommitment(content)
	if len(want) != 64 || strings.ToLower(want) != want {
		t.Fatalf("digest=%q", want)
	}
	for iteration := 0; iteration < 100; iteration++ {
		if got := ContentCommitment(content); got != want {
			t.Fatalf("iteration=%d got=%q want=%q", iteration, got, want)
		}
	}
	if changed := ContentCommitment([]byte("exact specification byteS")); changed == want {
		t.Fatal("one-byte mutation retained commitment")
	}

	local := time.Date(2026, 8, 27, 10, 0, 0, 0, time.FixedZone("local", -5*60*60))
	revision := mustRevision(t, ArtifactRevisionParams{
		ID: "artifact-1", MissionID: "mission-1", Kind: KindSpecification,
		RevisionNumber: 1, ContentDigest: want, SourceRef: "specs/spec.md",
		SourceRevision: "commit-1", CreatedBy: "actor-1", CreatedAt: local,
	})
	if revision.CreatedAt().Location() != time.UTC || !revision.CreatedAt().Equal(local) {
		t.Fatalf("timestamp not normalized: %v", revision.CreatedAt())
	}
	if revision.ContentDigest() != ContentCommitment(content) {
		t.Fatal("audit timestamp changed content identity")
	}

	other := mustRevision(t, ArtifactRevisionParams{
		ID: "artifact-2", MissionID: "mission-1", Kind: KindSpecification,
		RevisionNumber: 2, ContentDigest: ContentCommitment([]byte("successor")),
		SourceRef: revision.SourceRef(), SourceRevision: "commit-2", CreatedBy: "actor-1",
		CreatedAt: local.Add(time.Minute),
	})
	if other.SourceRef() != revision.SourceRef() || other.ID() == revision.ID() || other.ContentDigest() == revision.ContentDigest() {
		t.Fatal("path equality was confused with artifact identity")
	}
}

func TestArtifactModelRejectsInvalidBoundariesByStableKind(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	valid := ArtifactRevisionParams{
		ID: "artifact-1", MissionID: "mission-1", Kind: KindSpecification,
		RevisionNumber: 1, ContentDigest: ContentCommitment([]byte("spec")),
		SourceRef: "specs/spec.md", SourceRevision: "commit-1", CreatedBy: "actor-1", CreatedAt: now,
	}
	tests := []struct {
		name   string
		mutate func(*ArtifactRevisionParams)
		kind   ErrorKind
	}{
		{"blank ID", func(p *ArtifactRevisionParams) { p.ID = "" }, ErrorInvalidIdentity},
		{"bad mission", func(p *ArtifactRevisionParams) { p.MissionID = "mission with spaces" }, ErrorInvalidIdentity},
		{"unknown kind", func(p *ArtifactRevisionParams) { p.Kind = "report" }, ErrorUnsupportedKind},
		{"zero revision", func(p *ArtifactRevisionParams) { p.RevisionNumber = 0 }, ErrorInvalidRevision},
		{"uppercase digest", func(p *ArtifactRevisionParams) { p.ContentDigest = strings.ToUpper(p.ContentDigest) }, ErrorMalformedDigest},
		{"absolute source", func(p *ArtifactRevisionParams) { p.SourceRef = "/tmp/spec.md" }, ErrorInvalidProvenance},
		{"windows source", func(p *ArtifactRevisionParams) { p.SourceRef = `C:\\spec.md` }, ErrorInvalidProvenance},
		{"source traversal", func(p *ArtifactRevisionParams) { p.SourceRef = "../spec.md" }, ErrorInvalidProvenance},
		{"ambient URL", func(p *ArtifactRevisionParams) { p.SourceRef = "https://example.invalid/spec" }, ErrorInvalidProvenance},
		{"blank source revision", func(p *ArtifactRevisionParams) { p.SourceRevision = "" }, ErrorInvalidProvenance},
		{"bad creator", func(p *ArtifactRevisionParams) { p.CreatedBy = "actor one" }, ErrorInvalidProvenance},
		{"zero time", func(p *ArtifactRevisionParams) { p.CreatedAt = time.Time{} }, ErrorInvalidTimestamp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			_, err := NewArtifactRevision(candidate)
			assertErrorKind(t, err, test.kind)
			var typed *Error
			if !errors.As(err, &typed) || typed.Field == "" || typed.Recovery == "" {
				t.Fatalf("diagnostic not bounded and typed: %#v", err)
			}
		})
	}
}

func TestValidationCanonicalizationPoliciesAndBounds(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	first := mustFinding(t, "missing_required_concern", "scope", "required structural concern is absent", RecoveryProvideEvidence)
	second := mustFinding(t, "missing_required_concern", "requirements", "required structural concern is absent", RecoveryProvideEvidence)
	policy, err := PolicyFor(KindSpecification)
	if err != nil {
		t.Fatal(err)
	}
	left := mustValidation(t, ValidationResultParams{
		ArtifactRevisionID: "artifact-1", Verdict: ValidationFail,
		Findings: []Finding{first, second}, Policy: policy, ValidatedAt: now,
	})
	right := mustValidation(t, ValidationResultParams{
		ArtifactRevisionID: "artifact-1", Verdict: ValidationFail,
		Findings: []Finding{second, first}, Policy: policy, ValidatedAt: now,
	})
	leftCommitment, err := left.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	rightCommitment, err := right.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	if leftCommitment != rightCommitment {
		t.Fatalf("finding presentation order changed commitment: %s != %s", leftCommitment, rightCommitment)
	}
	returned := left.Findings()
	returned[0] = Finding{}
	if left.Findings()[0].Kind() == "" {
		t.Fatal("caller mutated validation findings")
	}
	required := policy.RequiredConcerns()
	required[0] = "changed"
	if policy.RequiredConcerns()[0] == "changed" {
		t.Fatal("caller mutated validation policy")
	}

	_, err = NewValidationResult(ValidationResultParams{ArtifactRevisionID: "artifact-1", Verdict: ValidationPass, Findings: []Finding{first}, Policy: policy, ValidatedAt: now})
	assertErrorKind(t, err, ErrorInvalidValidation)
	_, err = NewValidationResult(ValidationResultParams{ArtifactRevisionID: "artifact-1", Verdict: ValidationFail, Policy: policy, ValidatedAt: now})
	assertErrorKind(t, err, ErrorInvalidValidation)
	_, err = NewValidationResult(ValidationResultParams{ArtifactRevisionID: "artifact-1", Verdict: ValidationFail, Findings: []Finding{first, first}, Policy: policy, ValidatedAt: now})
	assertErrorKind(t, err, ErrorInvalidValidation)
	_, err = NewFinding("kind", "field", strings.Repeat("x", maxFindingMessageLength+1), RecoveryCorrectInput)
	assertErrorKind(t, err, ErrorInvalidFinding)
	for _, private := range []string{"password=hunter2", "Authorization: Bearer abc", "private transcript: hello", "BEGIN PRIVATE KEY"} {
		_, err = NewFinding("kind", "field", private, RecoveryCorrectInput)
		assertErrorKind(t, err, ErrorInvalidFinding)
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(private)) {
			t.Fatal("sensitive value leaked into diagnostic")
		}
	}
}

func TestSpecificationAndPlanPoliciesAreTyped(t *testing.T) {
	now := time.Date(2026, 8, 27, 17, 0, 0, 0, time.UTC)
	tests := []struct {
		kind     Kind
		complete []Concern
	}{
		{KindSpecification, []Concern{ConcernIntent, ConcernScope, ConcernRequirements, ConcernAcceptanceFraming, ConcernAssumptions, ConcernBoundaries}},
		{KindPlan, []Concern{ConcernArchitecture, ConcernDataFlow, ConcernRisk, ConcernCompatibility, ConcernMigration, ConcernValidationStrategy}},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			policy, err := PolicyFor(test.kind)
			if err != nil {
				t.Fatal(err)
			}
			pass, err := ValidateConcerns("artifact-1", policy, test.complete, now)
			if err != nil || pass.Verdict() != ValidationPass || len(pass.Findings()) != 0 {
				t.Fatalf("complete policy: result=%#v err=%v", pass, err)
			}
			for index, concern := range test.complete {
				present := append([]Concern(nil), test.complete[:index]...)
				present = append(present, test.complete[index+1:]...)
				fail, validationErr := ValidateConcerns("artifact-1", policy, present, now)
				if validationErr != nil || fail.Verdict() != ValidationFail || len(fail.Findings()) != 1 || fail.Findings()[0].Field() != string(concern) || fail.Findings()[0].Kind() != "missing_required_concern" {
					t.Fatalf("missing=%s result=%#v err=%v", concern, fail, validationErr)
				}
			}
		})
	}
}

func TestArtifactContractStrictSchemaAndApprovalShapes(t *testing.T) {
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	revision := mustRevision(t, ArtifactRevisionParams{
		ID: "artifact-1", MissionID: "mission-1", Kind: KindSpecification, RevisionNumber: 1,
		ContentDigest: ContentCommitment([]byte("spec")), SourceRef: "specs/spec.md",
		SourceRevision: "commit-1", CreatedBy: "actor-1", CreatedAt: now,
	})
	policy, _ := PolicyFor(KindSpecification)
	validation := mustValidation(t, ValidationResultParams{ArtifactRevisionID: revision.ID(), Verdict: ValidationPass, Policy: policy, ValidatedAt: now})
	approval := mustApproval(t, ArtifactApprovalParams{
		ID: "approval-1", ArtifactRevisionID: revision.ID(), Verdict: VerdictApproved,
		ActorID: "reviewer-1", EvidenceDigest: ContentCommitment([]byte("bounded evidence")),
		GovernanceContextID: "context-1", SubmittedAt: now,
	})

	for _, candidate := range []Contract{
		{SchemaVersion: 1, Revision: revision, Validation: validation},
		{SchemaVersion: 1, Revision: revision, Validation: validation, Approval: &approval},
	} {
		encoded, err := MarshalContract(candidate)
		if err != nil {
			t.Fatal(err)
		}
		validateArtifactContractSchema(t, encoded)
		var generic map[string]any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			t.Fatal(err)
		}
		if _, present := generic["approval"]; !present {
			t.Fatalf("approval null/object shape omitted: %s", encoded)
		}
		restored, err := UnmarshalContract(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if restored.Revision.ID() != revision.ID() || restored.Validation.ArtifactRevisionID() != revision.ID() || restored.Validation.Verdict() != ValidationPass {
			t.Fatalf("round trip lost exact binding: %#v", restored)
		}
		if candidate.Approval == nil && restored.Approval != nil {
			t.Fatal("null approval became a verdict")
		}
		if candidate.Approval != nil && (restored.Approval == nil || restored.Approval.ID() != approval.ID()) {
			t.Fatal("approval round trip failed")
		}
	}
	finding := mustFinding(t, "missing_required_concern", "scope", "required structural concern is absent", RecoveryCorrectInput)
	failed := mustValidation(t, ValidationResultParams{
		ArtifactRevisionID: revision.ID(), Verdict: ValidationFail, Findings: []Finding{finding},
		Policy: policy, ValidatedAt: now,
	})
	rejected := mustApproval(t, ArtifactApprovalParams{
		ID: "approval-2", ArtifactRevisionID: revision.ID(), Verdict: VerdictRejected,
		ActorID: "reviewer-1", EvidenceDigest: ContentCommitment([]byte("rejection evidence")),
		GovernanceContextID: "context-1", SubmittedAt: now,
	})
	rejectedPayload, err := MarshalContract(Contract{SchemaVersion: 1, Revision: revision, Validation: failed, Approval: &rejected})
	if err != nil {
		t.Fatal(err)
	}
	validateArtifactContractSchema(t, rejectedPayload)
	restoredRejected, err := UnmarshalContract(rejectedPayload)
	if err != nil || restoredRejected.Validation.Verdict() != ValidationFail || restoredRejected.Approval == nil || restoredRejected.Approval.Verdict() != VerdictRejected {
		t.Fatalf("fail/rejected contract did not round trip: contract=%#v err=%v", restoredRejected, err)
	}

	encoded, err := MarshalContract(Contract{SchemaVersion: 1, Revision: revision, Validation: validation})
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schema_version":1`), []byte(`"schema_version":1,"authority":true`), 1)
	_, err = UnmarshalContract(unknown)
	assertErrorKind(t, err, ErrorUnexpectedAuthority)
	unsupported := bytes.Replace(encoded, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
	_, err = UnmarshalContract(unsupported)
	assertErrorKind(t, err, ErrorUnsupportedSchema)
	_, err = UnmarshalContract(append(encoded, []byte(` {}`)...))
	assertErrorKind(t, err, ErrorUnexpectedAuthority)
	for _, timestampPath := range []string{"artifact_revision.created_at", "validation.validated_at", "approval.submitted_at"} {
		payload := rejectedPayload
		var document map[string]any
		if err := json.Unmarshal(payload, &document); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(timestampPath, ".")
		document[parts[0]].(map[string]any)[parts[1]] = "not-a-timestamp"
		malformed, marshalErr := json.Marshal(document)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		_, err = UnmarshalContract(malformed)
		assertErrorKind(t, err, ErrorInvalidTimestamp)
	}
}

func validateArtifactContractSchema(t *testing.T, payload []byte) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "kitty-specs", "software-development-mission-method-01M11EX2", "contracts", "artifact-contract.schema.json")
	schemaPayload, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaPayload, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaPath, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("artifact contract violates checked-in schema: %v", err)
	}
}

func mustRevision(t *testing.T, params ArtifactRevisionParams) ArtifactRevision {
	t.Helper()
	revision, err := NewArtifactRevision(params)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func mustApproval(t *testing.T, params ArtifactApprovalParams) ArtifactApproval {
	t.Helper()
	approval, err := NewArtifactApproval(params)
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func mustFinding(t *testing.T, kind, field, message string, recovery RecoveryCategory) Finding {
	t.Helper()
	finding, err := NewFinding(kind, field, message, recovery)
	if err != nil {
		t.Fatal(err)
	}
	return finding
}

func mustValidation(t *testing.T, params ValidationResultParams) ValidationResult {
	t.Helper()
	validation, err := NewValidationResult(params)
	if err != nil {
		t.Fatal(err)
	}
	return validation
}

func assertErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected kind=%s, got nil", kind)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("got=%T %v want kind=%s", err, err, kind)
	}
}
