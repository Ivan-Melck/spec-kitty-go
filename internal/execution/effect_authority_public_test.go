package execution_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// externalEffectDispatcherV2 proves a third-party provider can implement the
// narrow port while receiving only the read-only, sealed mutation view.
type externalEffectDispatcherV2 struct{}

func (externalEffectDispatcherV2) ObserveEffect(context.Context, tracker.ExternalWorkRef) (execution.EffectProviderObservationV2, error) {
	return execution.EffectProviderObservationV2{}, nil
}

func (externalEffectDispatcherV2) ApplyEffect(_ context.Context, mutation execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	_ = mutation.EffectID()
	_ = mutation.Action()
	_ = mutation.Target()
	_ = mutation.ExpectedRevision()
	_ = mutation.ExpectedValue()
	_ = mutation.ExpectedMarker()
	_ = mutation.OperationMarker()
	_ = mutation.IntendedValue()
	_ = mutation.RestoreValue()
	_ = mutation.RequiredGuarantee()
	return execution.EffectMutationResultV2{}, nil
}

func (externalEffectDispatcherV2) CompensateEffect(ctx context.Context, mutation execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	return externalEffectDispatcherV2{}.ApplyEffect(ctx, mutation)
}

var _ execution.EffectDispatcherPortV2 = externalEffectDispatcherV2{}

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
