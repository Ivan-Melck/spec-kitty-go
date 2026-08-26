package tracker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestCapabilityLevelsUseStableText(t *testing.T) {
	t.Parallel()
	values := []fmt.Stringer{
		tracker.SupportUnsupported, tracker.SupportSupported,
		tracker.UpdateUnsupported, tracker.UpdateUnguarded, tracker.UpdateGuarded,
		tracker.DiscussionUnsupported, tracker.DiscussionAppendOnly, tracker.DiscussionIdempotent,
		tracker.AssignmentUnsupported, tracker.AssignmentUnguarded, tracker.AssignmentAtomicClaim,
		tracker.ObservationRevisionUnsupported, tracker.ObservationRevisionContentDerived, tracker.ObservationRevisionProviderNative,
		tracker.CompensationUnsupported, tracker.CompensationConditional,
		tracker.ConcurrentWritersNone, tracker.ConcurrentWritersAdapterSerialized, tracker.ConcurrentWritersProviderNative,
	}
	for _, value := range values {
		value := value
		t.Run(value.String(), func(t *testing.T) {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(encoded); got != `"`+value.String()+`"` {
				t.Fatalf("encoded level = %s", got)
			}
			decoded := reflect.New(reflect.TypeOf(value)).Interface()
			if err := json.Unmarshal(encoded, decoded); err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(reflect.ValueOf(decoded).Elem().Interface()); got != value.String() {
				t.Fatalf("decoded level = %q", got)
			}
		})
	}
}

func TestCapabilityValidationRejectsUnknownAndContradictoryClaims(t *testing.T) {
	t.Parallel()
	valid := readOnlyCapabilities()
	if err := valid.Validate(); err != nil {
		t.Fatalf("read-only capabilities: %v", err)
	}

	unknown := valid
	unknown.Assignment = tracker.AssignmentLevel("invented")
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "assignment") {
		t.Fatalf("unknown assignment validation = %v", err)
	}

	unsafe := valid
	unsafe.Assignment = tracker.AssignmentUnguarded
	unsafe.ObservationRevision = tracker.ObservationRevisionContentDerived
	unsafe.SafeCompensation = tracker.CompensationConditional
	if err := unsafe.Validate(); err == nil || !strings.Contains(err.Error(), "safe compensation") {
		t.Fatalf("contradictory compensation validation = %v", err)
	}

	observedAssignment := valid
	observedAssignment.Assignment = tracker.AssignmentUnguarded
	observedAssignment.ObservationRevision = tracker.ObservationRevisionContentDerived
	if err := observedAssignment.Validate(); err != nil {
		t.Fatalf("assignment observation without compensation: %v", err)
	}
}

func TestCapabilityRequirementReturnsTypedRefusal(t *testing.T) {
	t.Parallel()
	caps := readOnlyCapabilities()
	if err := caps.Require(tracker.CapabilityCatalogRead); err != nil {
		t.Fatalf("require supported read: %v", err)
	}
	if err := caps.Require(tracker.CapabilitySafeCompensation); !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) {
		t.Fatalf("safe compensation refusal = %v", err)
	}
	err := caps.Require(tracker.CapabilityCatalogCreate)
	var typed *tracker.Error
	if !errors.As(err, &typed) || typed.Kind != tracker.ErrorCapabilityUnsupported || typed.Capability != tracker.CapabilityCatalogCreate || typed.RecoveryCode == "" {
		t.Fatalf("catalog refusal = %#v", err)
	}
	unguarded := caps
	unguarded.CatalogUpdate = tracker.UpdateUnguarded
	if err := unguarded.RequireCatalogUpdate(tracker.UpdateGuarded); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("guarded update refusal = %v", err)
	}
}

func TestCriterionAndExternalWorkRefValidation(t *testing.T) {
	t.Parallel()
	criterion := tracker.Criterion{ID: "criterion-1", Text: "observable result", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "run focused tests"}
	if err := criterion.Validate(); err != nil {
		t.Fatal(err)
	}
	ref := tracker.ExternalWorkRef{
		ProjectID: ids.ProjectID("project-1"), WorkID: ids.WorkID("work-1"), Provider: "portable-provider",
		ExternalID: "external-1", MetadataSchema: 1, CreatedByOperation: ids.OperationID("operation-1"), LastObservation: 7,
	}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	if ref.WorkID.String() == ref.ExternalID {
		t.Fatal("external identity replaced the internal work identity")
	}
	invalid := criterion
	invalid.Verifier = tracker.VerifierKind("checkbox")
	if err := invalid.Validate(); err == nil {
		t.Fatal("unknown verifier accepted")
	}
}

func TestProviderObservationAndHealthReportDefensivelyCopyCollections(t *testing.T) {
	t.Parallel()
	canonical := []byte("canonical-known-fields")
	observation, err := tracker.NewProviderObservation("external-1", canonical, 7, "full-digest", "native-token", time.Unix(10, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	canonical[0] = 'X'
	first := observation.CanonicalFields()
	if string(first) != "canonical-known-fields" {
		t.Fatalf("observation changed through input: %q", first)
	}
	first[0] = 'Y'
	if got := string(observation.CanonicalFields()); got != "canonical-known-fields" {
		t.Fatalf("observation changed through accessor: %q", got)
	}

	diagnostics := []tracker.Diagnostic{{Kind: tracker.ErrorUnavailable, RecoveryCode: "retry_later", Message: "provider unavailable"}}
	report, err := tracker.NewHealthReport(tracker.HealthHealthy, readOnlyCapabilities(), diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics[0].Message = "mutated"
	got := report.Diagnostics()
	if got[0].Message != "provider unavailable" {
		t.Fatalf("report changed through input: %+v", got)
	}
	got[0].Message = "mutated again"
	if report.Diagnostics()[0].Message != "provider unavailable" {
		t.Fatal("report changed through accessor")
	}
}

func TestWorkRecordDefensivelyCopiesPortableCollections(t *testing.T) {
	t.Parallel()
	observation, err := tracker.NewProviderObservation("external-1", []byte("canonical"), 7, "digest", "token", time.Unix(10, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project-1", WorkID: "work-1", Provider: "portable-provider", ExternalID: "external-1", MetadataSchema: 1}
	criteria := []tracker.Criterion{{ID: "criterion-1", Text: "observable", Verifier: tracker.VerifierReview, Required: true}}
	dependencies := []tracker.DependencyRef{{BlockedWorkID: "work-1", BlockingWorkID: "work-0", Kind: tracker.DependencyBlocks}}
	discussion := []tracker.DiscussionEntry{{ExternalCommentID: "comment-1", Author: "author", Body: "body", Observation: 7}}
	record, err := tracker.NewWorkRecord(reference, "title", "description", "design", criteria, dependencies, "agent-a", "operation-a", discussion, observation)
	if err != nil {
		t.Fatal(err)
	}
	criteria[0].Text = "changed"
	dependencies[0].BlockingWorkID = "changed"
	discussion[0].Body = "changed"
	if record.Criteria()[0].Text != "observable" || record.Dependencies()[0].BlockingWorkID != "work-0" || record.Discussion()[0].Body != "body" {
		t.Fatalf("record changed through constructor inputs: %+v %+v %+v", record.Criteria(), record.Dependencies(), record.Discussion())
	}
	got := record.Criteria()
	got[0].Text = "changed again"
	if record.Criteria()[0].Text != "observable" {
		t.Fatal("record changed through accessor")
	}
}

func TestErrorKindsAndDiagnosticsRemainStableAndRedacted(t *testing.T) {
	t.Parallel()
	kinds := []tracker.ErrorKind{
		tracker.ErrorUnavailable, tracker.ErrorAmbiguous, tracker.ErrorRevisionConflict,
		tracker.ErrorAttributionLost, tracker.ErrorNotFound, tracker.ErrorInvalidRequest,
		tracker.ErrorTimeout, tracker.ErrorVersionSkew, tracker.ErrorSchemaSkew,
		tracker.ErrorMalformedProtocol, tracker.ErrorWorkspaceMismatch, tracker.ErrorLockContended,
		tracker.ErrorSafeCompensationUnsupported, tracker.ErrorGuardedUpdateUnsupported,
		tracker.ErrorCapabilityUnsupported,
	}
	for _, kind := range kinds {
		wrapped := fmt.Errorf("outer: %w", &tracker.Error{Kind: kind, Message: "stable message", Cause: errors.New("secret-token comment-body")})
		if !tracker.IsKind(wrapped, kind) {
			t.Errorf("IsKind did not recognize %s", kind)
		}
		if got := wrapped.Error(); strings.Contains(got, "secret-token") || strings.Contains(got, "comment-body") {
			t.Errorf("diagnostic disclosed cause for %s: %q", kind, got)
		}
		var typed *tracker.Error
		if !errors.As(wrapped, &typed) || typed.RecoveryAction() == "" {
			t.Errorf("missing recovery action for %s", kind)
		}
	}
	if got := (&tracker.Error{Kind: tracker.ErrorUnavailable}).Error(); got == "" {
		t.Fatal("empty optional fields produced an empty error")
	}
	revisionA := tracker.Revision(len("opaque"))
	revisionB := tracker.Revision(6)
	if revisionA != revisionB {
		t.Fatal("equal opaque observations did not compare equally")
	}
}

func TestProviderCompatibilityAndNarrowInterfaces(t *testing.T) {
	t.Parallel()
	var legacy tracker.Provider = legacyProvider{}
	var reader tracker.WorkReader = readOnlyProvider{}
	var reporter tracker.CapabilityReporter = capabilityOnlyProvider{}
	_, _ = legacy, reader
	caps, err := reporter.Capabilities(context.Background())
	if err != nil || caps.CatalogRead != tracker.SupportSupported {
		t.Fatalf("independent capability reporting = %+v, %v", caps, err)
	}
}

func readOnlyCapabilities() tracker.ProviderCapabilities {
	return tracker.ProviderCapabilities{
		CatalogRead:         tracker.SupportSupported,
		CatalogCreate:       tracker.SupportUnsupported,
		CatalogUpdate:       tracker.UpdateUnsupported,
		BlockingGraph:       tracker.SupportUnsupported,
		Discussion:          tracker.DiscussionUnsupported,
		Assignment:          tracker.AssignmentUnsupported,
		ObservationRevision: tracker.ObservationRevisionUnsupported,
		SafeCompensation:    tracker.CompensationUnsupported,
		ConcurrentWriters:   tracker.ConcurrentWritersNone,
		PortableExport:      tracker.SupportUnsupported,
		RestorableBackup:    tracker.SupportUnsupported,
	}
}

type legacyProvider struct{}

func (legacyProvider) Read(context.Context, string) (tracker.Work, error) { return tracker.Work{}, nil }
func (legacyProvider) CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, nil
}
func (legacyProvider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, nil
}
func (legacyProvider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, nil
}

type readOnlyProvider struct{}

func (readOnlyProvider) Read(context.Context, string) (tracker.Work, error) {
	return tracker.Work{}, nil
}

type capabilityOnlyProvider struct{}

func (capabilityOnlyProvider) Capabilities(context.Context) (tracker.ProviderCapabilities, error) {
	return readOnlyCapabilities(), nil
}
