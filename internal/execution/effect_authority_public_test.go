package execution_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

// externalEffectDispatcherV2 proves a third-party provider can implement the
// narrow port while receiving only the read-only, sealed mutation view.
type externalEffectDispatcherV2 struct {
	projection tracker.AssignmentProjection
}

func (d externalEffectDispatcherV2) ObserveEffect(ctx context.Context, target tracker.ExternalWorkRef) (execution.EffectProviderObservationV2, error) {
	observed, err := d.projection.ObserveAssignment(ctx, target)
	if err != nil {
		return execution.EffectProviderObservationV2{}, err
	}
	return externalEffectObservationV2(target, observed)
}

func (d externalEffectDispatcherV2) ApplyEffect(ctx context.Context, mutation execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	mutated, err := d.projection.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{
		ExternalRef:         mutation.Target(),
		OperationID:         ids.OperationID(mutation.OperationMarker()),
		Assignee:            mutation.IntendedValue(),
		ExpectedObservation: mutation.ExpectedRevision(),
		RequiredGuarantee:   mutation.RequiredGuarantee(),
	})
	return externalEffectMutationResultV2(mutation.Target(), mutated, err)
}

func (d externalEffectDispatcherV2) CompensateEffect(ctx context.Context, mutation execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	mutated, err := d.projection.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{
		ExternalRef:         mutation.Target(),
		OperationID:         ids.OperationID(mutation.OperationMarker()),
		ExpectedObservation: mutation.ExpectedRevision(),
		ExpectedAssignee:    mutation.ExpectedValue(),
		ExpectedMarker:      mutation.ExpectedMarker(),
		RestoreAssignee:     mutation.RestoreValue(),
	})
	return externalEffectMutationResultV2(mutation.Target(), mutated, err)
}

func externalEffectObservationV2(target tracker.ExternalWorkRef, observed tracker.AssignmentObservation) (execution.EffectProviderObservationV2, error) {
	revision := observed.Observation.Revision
	if observed.ExternalRef.ProjectID != target.ProjectID || observed.ExternalRef.WorkID != target.WorkID || observed.ExternalRef.Provider != target.Provider || observed.ExternalRef.ExternalID != target.ExternalID || observed.ExternalRef.MetadataSchema != target.MetadataSchema || observed.ExternalRef.CreatedByOperation != target.CreatedByOperation || revision == 0 || observed.ExternalRef.LastObservation != revision || observed.Observation.ExternalID != target.ExternalID || observed.Assignee != observed.Observation.Assignee || observed.OperationMarker != observed.Observation.OperationMarker {
		return execution.EffectProviderObservationV2{}, errors.New("incoherent provider observation")
	}
	outcome := "confirmed"
	if observed.Assignee == "" && observed.OperationMarker == "" {
		outcome = "absent"
	}
	return execution.NewEffectProviderObservationV2(observed.ExternalRef, revision, observed.Assignee, observed.OperationMarker, outcome)
}

func externalEffectMutationResultV2(target tracker.ExternalWorkRef, mutation tracker.AssignmentMutation, mutationErr error) (execution.EffectMutationResultV2, error) {
	observation, err := externalEffectObservationV2(target, mutation.Observation)
	if err != nil {
		return execution.EffectMutationResultV2{Kind: execution.EffectMutationIndeterminateV2}, errors.Join(mutationErr, err)
	}
	kind := execution.EffectMutationDefinitiveV2
	if mutationErr != nil {
		kind = execution.EffectMutationIndeterminateV2
	}
	return execution.EffectMutationResultV2{Kind: kind, Observation: observation}, mutationErr
}

var _ execution.EffectDispatcherPortV2 = externalEffectDispatcherV2{}

func TestExternalProviderBuildsAcceptedObservationFromRealFakeTrackerV2(t *testing.T) {
	store, err := trackerfake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	target := tracker.ExternalWorkRef{ProjectID: "project-external", WorkID: "work-external", Provider: "fake", ExternalID: "ticket-external", MetadataSchema: 1, CreatedByOperation: "operation-create-external", LastObservation: 99}
	if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: target.ExternalID, Title: "external provider target"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := externalEffectDispatcherV2{projection: trackerfake.NewProvider(store)}
	observation, err := dispatcher.ObserveEffect(t.Context(), target)
	if err != nil || observation.Outcome != "absent" || observation.ProviderRevision != 1 || observation.Target.LastObservation != 1 || observation.CanonicalDigest == "" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestEffectDispatchPublicSurfaceHasNoReusableAuthorityV2(t *testing.T) {
	resultType := reflect.TypeOf(execution.EffectAuthorityResultV2{})
	for index := 0; index < resultType.NumField(); index++ {
		field := resultType.Field(index)
		if field.IsExported() && field.Name == "Permission" {
			t.Fatal("effect result exports reusable dispatch permission")
		}
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate execution package")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range files {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), source, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Name.Name == "DispatchAuthorizedEffectV2" {
					t.Fatal("raw authorized provider dispatch remains public")
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					if named, ok := spec.(*ast.TypeSpec); ok && named.Name.Name == "EffectDispatchPermissionV2" {
						t.Fatal("reusable effect permission remains public")
					}
				}
			}
		}
	}
}
