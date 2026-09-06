package scenario

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionCapabilityCompositionHasOnePublicationBoundary binds the
// family-semantic and publication-cut evidence axes. It inspects the actual
// production composition function, not a parallel manifest. Runtime-host
// epoch admission is a bootstrap exception qualified at its own sealed batch;
// operation is mandatory membership in each command plan; outbox currently has
// no production command surface. Every host-served command capability below
// must receive the one coordinator built over the admitted GapDB adapter.
func TestProductionCapabilityCompositionHasOnePublicationBoundary(t *testing.T) {
	sourcePath := filepath.Join(repositoryRoot(t), "internal", "productionruntime", "composition.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProductionCapabilityComposition(source); err != nil {
		t.Fatal(err)
	}
}

func TestProductionCapabilityCompositionBoundaryBypassMutant(t *testing.T) {
	sourcePath := filepath.Join(repositoryRoot(t), "internal", "productionruntime", "composition.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	old := "storegapdb.NewArtifactAuthorityPortV2(coordinator)"
	mutant := strings.Replace(string(source), old, "storegapdb.NewArtifactAuthorityPortV2(nil)", 1)
	if mutant == string(source) {
		t.Fatal("composition mutant anchor was not reached")
	}
	if err := validateProductionCapabilityComposition([]byte(mutant)); err == nil || !strings.Contains(err.Error(), "NewArtifactAuthorityPortV2") {
		t.Fatalf("publication-boundary bypass mutant survived: %v", err)
	}
}

func validateProductionCapabilityComposition(source []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "composition.go", source, 0)
	if err != nil {
		return fmt.Errorf("parse production composition: %w", err)
	}
	var body *ast.BlockStmt
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "composeRuntimeCapabilities" {
			body = function.Body
			break
		}
	}
	if body == nil {
		return fmt.Errorf("composeRuntimeCapabilities is absent")
	}
	wantCoordinatorArgument := map[string]int{
		"NewMissionAuthorityPortV2":     1,
		"ExecutionAuthorityPortV2":      0,
		"WorkspaceAuthorityPortsV2":     0,
		"HandoffAuthorityServiceV2":     0,
		"ReviewAuthorityServiceV2":      0,
		"AcceptanceAuthorityServiceV2":  0,
		"IntegrationAuthorityServiceV2": 0,
		"IntegrationCleanupServiceV2":   0,
		"EffectAuthorityPortV2":         0,
		"NewArtifactAuthorityPortV2":    0,
		"NewWorkgraphAuthorityPortV2":   0,
	}
	seen := make(map[string]int, len(wantCoordinatorArgument))
	coordinatorConstructors := 0
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calledName(call.Fun)
		if name == "NewPublicationCoordinatorV2" {
			if len(call.Args) != 2 || identifierName(call.Args[0]) != "adapter" || identifierName(call.Args[1]) != "tracker" {
				err = fmt.Errorf("publication coordinator is not bound to adapter and tracker")
				return false
			}
			coordinatorConstructors++
		}
		argumentIndex, required := wantCoordinatorArgument[name]
		if !required {
			return true
		}
		seen[name]++
		if len(call.Args) <= argumentIndex || identifierName(call.Args[argumentIndex]) != "coordinator" {
			err = fmt.Errorf("%s bypasses the shared publication coordinator", name)
			return false
		}
		return true
	})
	if err != nil {
		return err
	}
	if coordinatorConstructors != 1 {
		return fmt.Errorf("production composition constructs %d publication coordinators", coordinatorConstructors)
	}
	for name := range wantCoordinatorArgument {
		if seen[name] != 1 {
			return fmt.Errorf("%s composition count=%d", name, seen[name])
		}
	}
	return nil
}

func calledName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func identifierName(expression ast.Expr) string {
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}
