package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestM2AutonomyKernelHasNoCeremonyOrIOAuthority(t *testing.T) {
	root := m2RepositoryRoot(t)
	paths := []string{
		"internal/mission/readiness.go",
		"internal/mission/readiness_snapshot.go",
		"internal/mission/readiness_authority_v2.go",
		"internal/mission/readiness_service_v2.go",
		"internal/mission/readiness_workrevision.go",
		"internal/missiontype/softwaredev/capability.go",
	}
	for _, relative := range paths {
		t.Run(filepath.Base(relative), func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), relative, source, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range parsed.Imports {
				dependency := strings.Trim(item.Path.Value, `"`)
				for _, forbidden := range []string{"database/sql", "os", "io", "net", "net/http", "os/exec", "path/filepath", "time", "/controlstore/", "/tracker/", "/git", "/transport", "/mcp"} {
					containsSegment := strings.HasPrefix(forbidden, "/") && strings.Contains(dependency, forbidden)
					if dependency == forbidden || containsSegment {
						t.Fatalf("autonomy kernel imports I/O/provider surface %q", dependency)
					}
				}
			}
			forbiddenIdentifiers := map[string]bool{
				"CurrentStep": true, "StepWorkDraftReady": true, "Successor": true,
				"evaluateNext": true, "EvaluateTransition": true, "AdvanceActionHandler": true,
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if ok && forbiddenIdentifiers[identifier.Name] {
					t.Fatalf("autonomy path references legacy ceremony symbol %s", identifier.Name)
				}
				return true
			})
			text := string(source)
			for _, forbidden := range []string{"status.json", "status.events.jsonl", "SPEC_KITTY_TEST_DB_NAME", "Django", "GraphQL", "src/", "tests/", "contracts/"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("autonomy path contains projection/stack assumption %q", forbidden)
				}
			}
		})
	}
}

func TestM2AutonomyEvaluatorIsPureAndTakesOneImmutableSnapshot(t *testing.T) {
	root := m2RepositoryRoot(t)
	path := filepath.Join(root, "internal/mission/readiness.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "EvaluateReadiness" {
			continue
		}
		found = true
		if function.Type.Params.NumFields() != 1 || function.Type.Results.NumFields() != 2 {
			t.Fatalf("pure evaluator signature widened: %s", function.Name.Name)
		}
		parameter, ok := function.Type.Params.List[0].Type.(*ast.Ident)
		if !ok || parameter.Name != "ReadinessSnapshot" {
			t.Fatal("pure evaluator accepts a caller-authored bag instead of the sealed readiness snapshot")
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.GoStmt:
				t.Fatal("pure evaluator starts goroutines")
			case *ast.SendStmt:
				t.Fatal("pure evaluator performs channel I/O")
			case *ast.CallExpr:
				if selector, ok := value.Fun.(*ast.SelectorExpr); ok {
					if owner, ok := selector.X.(*ast.Ident); ok && (owner.Name == "os" || owner.Name == "time" || owner.Name == "net" || owner.Name == "http") {
						t.Fatalf("pure evaluator calls %s.%s", owner.Name, selector.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("pure readiness evaluator is absent")
	}
	snapshotSource := readM2AutonomySource(t, root, "internal/mission/readiness_snapshot.go")
	for _, required := range []string{"CapabilityObservationSetV2", "len(observations) != len(allActionIDs)", "non_capture_authority_revision", "capture.DatabaseID()", "snapshotDigest"} {
		if !strings.Contains(snapshotSource, required) {
			t.Fatalf("sealed readiness snapshot lacks completeness law %s", required)
		}
	}
	for _, forbidden := range []string{"type ReadinessEvidence struct", "type CapabilityScopeSet struct", "type CapabilityScopeCensus struct"} {
		if strings.Contains(snapshotSource, forbidden) {
			t.Fatalf("readiness seal exposes caller-authored authority bag %s", forbidden)
		}
	}
	workRevisionSource := readM2AutonomySource(t, root, "internal/mission/readiness_workrevision.go")
	for _, required := range []string{
		"func SealInitialWorkRevisionSelectionV2(result FinalizationResult)",
		"func SealReconciledWorkRevisionSelectionV2(prior WorkRevisionAuthorityV2, result SuccessorPublicationResult)",
		"publication_authority",
	} {
		if !strings.Contains(workRevisionSource, required) {
			t.Fatalf("work-revision readiness is not bound to publication authority: %s", required)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal/missiontype/softwaredev/readiness.go")); !os.IsNotExist(err) {
		t.Fatal("future-only software-development readiness facade remains in production")
	}
}

func TestM2AutonomyLegacyPhaseAPIsAreQuarantinedFromNewWrites(t *testing.T) {
	root := m2RepositoryRoot(t)
	service := readM2AutonomySource(t, root, "internal/mission/readiness_service_v2.go")
	adapter := readM2AutonomySource(t, root, "internal/controlstore/gapdb/mission_authority.go")
	finalization := readM2AutonomySource(t, root, "internal/mission/finalization.go")
	decomposition := readM2AutonomySource(t, root, "internal/mission/decomposition.go")
	reconciliation := readM2AutonomySource(t, root, "internal/mission/reconciliation.go")
	for _, forbidden := range []string{"MissionAuthorityStateV2", "MissionEventLifecycleAdvancedV2", "StepWorkDraftReady", "CurrentStep", "StepKey", "evaluateNext"} {
		if strings.Contains(service, forbidden) {
			t.Fatalf("new authority service reaches legacy phase symbol %s", forbidden)
		}
	}
	for _, required := range []string{"ObserveReadinessAuthority", "DecodeReadinessAuthorityStateV2", "VerifyRevisionV2"} {
		if !strings.Contains(adapter, required) {
			t.Fatalf("GapDB readiness adapter lacks stable authority seam %s", required)
		}
	}
	if strings.Contains(finalization, "CurrentStep != StepWorkDraftReady") || strings.Contains(decomposition, "current.CurrentStep != StepPlanApproved") || strings.Contains(reconciliation, "CurrentStep != StepWorkFinalized") {
		t.Fatal("capability still gates on legacy phase")
	}
}

func readM2AutonomySource(t *testing.T, root, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
