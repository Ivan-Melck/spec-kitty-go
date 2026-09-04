package mission

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const runMissionMutantsEnvV2 = "SPEC_KITTY_RUN_WP05_MUTANTS"

type missionSourceMutantV2 struct {
	id          string
	file        string
	anchor      string
	replacement string
	target      string
	killer      string
	want        string
}

func TestMissionAuthorityExecutableControlledMutantsV2(t *testing.T) {
	if os.Getenv(runMissionMutantsEnvV2) != "1" {
		t.Skip("set SPEC_KITTY_RUN_WP05_MUTANTS=1 to run the bounded source-overlay campaign")
	}
	root := missionRepositoryRootV2(t)
	mutants := []missionSourceMutantV2{
		{"M-MISSION-GENERIC-STORE", "internal/mission/authority_v2.go", "type MissionAuthorityPortV2 interface {", "type MissionAuthorityPortV2 interface {\n\tSaveMission(context.Context, []byte) error", "./internal/mission", "TestMissionReducerEveryEventAndVersionRefusalV2", "missing method SaveMission"},
		{"M-MISSION-PROVIDER-IMPORT", "internal/mission/authority_v2.go", "\t\"context\"", "\t\"context\"\n\t_ \"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb\"", "./internal/mission", "TestMissionReducerEveryEventAndVersionRefusalV2", "import cycle not allowed"},
		{"M-MISSION-OPERATION-SCOPED", "internal/controlstore/gapdb/mission_authority.go", "journalv2.NewOperationBindingAddressV2(candidate.OperationID())", "journalv2.NewOperationBindingAddressV2(journalv2.OperationID(string(candidate.OperationID()) + \"-\" + string(candidate.Scope().MissionID)))", "./internal/controlstore/gapdb", "TestMissionAuthorityAdapterBindingConflictPrecedesMissionReadV2", "global binding collision"},
		{"M-MISSION-LATE-COLLISION", "internal/mission/authority_v2.go", "\tcase OperationConflictV2:\n\t\treturn MissionAuthorityResultV2{}", "\tcase OperationConflictV2:\n\t\t_, _ = s.epoch.CurrentRuntimeEpoch(ctx)\n\t\treturn MissionAuthorityResultV2{}", "./internal/mission", "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2", "binding conflict read mission or epoch"},
		{"M-MISSION-EPOCH-BEFORE-REPLAY", "internal/mission/authority_v2.go", "\tresolution, err := s.port.ResolveOperation(ctx, binding)", "\t_, _ = s.epoch.CurrentRuntimeEpoch(ctx)\n\tresolution, err := s.port.ResolveOperation(ctx, binding)", "./internal/mission", "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2", "replay crossed authority boundary"},
		{"M-MISSION-HEAD-WITHOUT-CAS", "internal/mission/authority_v2.go", "ExpectedRecordRevision: command.RelatedFact.ExpectedRecordRevision", "ExpectedRecordRevision: 0", "./internal/mission", "TestMissionAuthorityFactUpdateCarriesIndependentCASV2", "INVALID_TRANSITION: condition_revision"},
		{"M-MISSION-FACT-GATE-SKIPPED", "internal/mission/authority_v2.go", "if event == MissionEventLifecycleAdvancedV2 || event == MissionEventCompletedV2 {", "if event == MissionEventCompletedV2 {", "./internal/mission", "TestMissionAuthorityBlockerAndDecisionGatesPrecedeAdvanceV2", "gate skipped or reordered"},
		{"M-MISSION-TERMINAL-REOPEN", "internal/mission/authority_v2.go", " || prior.Status().terminal()", "", "./internal/mission", "TestMissionReducerEveryEventAndVersionRefusalV2", "terminal mission reopened"},
		{"M-MISSION-PROVIDER-BEFORE-INTENT", "internal/mission/decomposition_authority_v2.go", "\t\trequestedPlan, planErr := buildMissionAuthorityStagePlanV2", "\t\t_ = s.provider.MutateEffect(ctx, effects[0].mutation)\n\t\trequestedPlan, planErr := buildMissionAuthorityStagePlanV2", "./internal/mission", "TestDecompositionCrashBeforeIntentMakesNoProviderCallV2", "provider mutated before durable intent"},
		{"M-MISSION-NAKED-RESERVATION", "internal/mission/decomposition_authority_v2.go", "command.EffectIntents, s.registry)", "nil, s.registry)", "./internal/mission", "TestDecompositionIntentPrecedesProviderAndCompletionV2", "first publication omitted durable request/effect"},
		{"M-MISSION-CHILD-IDENTITY-DRIFT", "internal/mission/decomposition_authority_v2.go", "\tobservations := make([]DecompositionProviderObservationV2, 0, len(effects))", "\tif resolution.Kind == OperationExactV2 {\n\t\teffects[0].mutation.ChildOperationID += \"-retry\"\n\t}\n\tobservations := make([]DecompositionProviderObservationV2, 0, len(effects))", "./internal/mission", "TestDecompositionRestartAfterRequestedPublicationReusesIdentitiesV2", "restart identity drift"},
		{"M-MISSION-UNATTRIBUTED-COMPLETION", "internal/mission/decomposition_authority_v2.go", " && observation.ProviderRevision > e.mutation.ExpectedRevision", "", "./internal/mission", "TestDecompositionDishonestProviderMatrixNeverCompletesV2", "dishonest provider completed"},
		{"M-MISSION-BLIND-COMPENSATION", "internal/mission/decomposition_authority_v2.go", " || current != recorded || !prepared.matches(current)", "", "./internal/mission", "TestDecompositionCompensationRequiresUnchangedRevisionValueAndMarkerV2", "human change was compensated"},
		{"M-MISSION-IO-REDUCER", "internal/mission/authority_v2.go", "\t\"fmt\"", "\t\"fmt\"\n\t\"os\"", "./internal/mission", "TestMissionReducerProductionSurfaceHasStructurallyNoIOV2", "I/O-capable reducer call"},
		{"M-MISSION-TRACKER-GRANTS-AUTHORITY", "internal/mission/decomposition_authority_v2.go", "type DecompositionAuthorityResultV2 struct {", "type DecompositionAuthorityResultV2 struct {\n\tExecutionAuthorized bool", "./internal/mission", "TestDecompositionTrackerResultCannotGrantExecutionAuthorityV2", "tracker result exposes execution authority"},
	}
	if len(mutants) != 15 {
		t.Fatalf("executable mutant census=%d want=15", len(mutants))
	}
	controls := make(map[string]bool)
	for _, mutant := range mutants {
		t.Run(mutant.id, func(t *testing.T) {
			controlKey := mutant.target + "\x00" + mutant.killer
			if !controls[controlKey] {
				output, err := runMissionMutantTestV2(t, root, "", mutant.target, mutant.killer, "")
				if err != nil {
					t.Fatalf("production control failed: %v\n%s", err, output)
				}
				controls[controlKey] = true
			}
			overlay, mutatedSource := missionMutantOverlayV2(t, root, mutant)
			output, err := runMissionMutantTestV2(t, root, overlay, mutant.target, mutant.killer, mutatedSource)
			if err == nil {
				t.Fatalf("mutant survived production-facing killer\n%s", output)
			}
			if !strings.Contains(output, mutant.want) {
				t.Fatalf("mutant failed for wrong reason: want %q\n%s", mutant.want, output)
			}
		})
	}
}

func TestMissionReducerProductionSurfaceHasStructurallyNoIOV2(t *testing.T) {
	path := os.Getenv("SPEC_KITTY_WP05_MUTATED_SOURCE")
	if path == "" {
		path = filepath.Join(missionRepositoryRootV2(t), "internal/mission/authority_v2.go")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	imports := make(map[string]string)
	for _, item := range parsed.Imports {
		path := strings.Trim(item.Path.Value, `"`)
		name := filepath.Base(path)
		if item.Name != nil {
			name = item.Name.Name
		}
		imports[name] = path
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "ReduceMissionEventV2" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			path := imports[owner.Name]
			if path == "os" || path == "io" || path == "net" || path == "net/http" || path == "database/sql" {
				t.Fatalf("I/O-capable reducer call: %s.%s", owner.Name, selector.Sel.Name)
			}
			return true
		})
		return
	}
	t.Fatal("mission reducer not found")
}

func TestDecompositionTrackerResultCannotGrantExecutionAuthorityV2(t *testing.T) {
	result := reflect.TypeOf(DecompositionAuthorityResultV2{})
	allowed := map[string]bool{"State": true, "Observations": true, "Replayed": true}
	for index := 0; index < result.NumField(); index++ {
		if !allowed[result.Field(index).Name] {
			t.Fatalf("tracker result exposes execution authority: field=%s", result.Field(index).Name)
		}
	}
	if result.NumField() != len(allowed) {
		t.Fatalf("tracker result exposes execution authority: fields=%d", result.NumField())
	}
}

func missionRepositoryRootV2(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func missionMutantOverlayV2(t *testing.T, root string, mutant missionSourceMutantV2) (string, string) {
	t.Helper()
	sourcePath := filepath.Join(root, mutant.file)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(source), mutant.anchor); count != 1 {
		t.Fatalf("mutant anchor count=%d want=1 anchor=%q", count, mutant.anchor)
	}
	mutated := strings.Replace(string(source), mutant.anchor, mutant.replacement, 1)
	if mutant.id == "M-MISSION-IO-REDUCER" {
		anchor := "func ReduceMissionEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {"
		if strings.Count(mutated, anchor) != 1 {
			t.Fatalf("I/O reducer anchor is not exact")
		}
		mutated = strings.Replace(mutated, anchor, anchor+"\n\t_, _ = os.ReadFile(\"/tmp/spec-kitty-wp05-mutant\")", 1)
	}
	temporary := t.TempDir()
	mutatedPath := filepath.Join(temporary, filepath.Base(sourcePath))
	if err := os.WriteFile(mutatedPath, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: map[string]string{sourcePath: mutatedPath}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return overlayPath, mutatedPath
}

func runMissionMutantTestV2(t *testing.T, root, overlay, target, killer, mutatedSource string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	arguments := []string{"test"}
	if overlay != "" {
		arguments = append(arguments, "-overlay="+overlay)
	}
	arguments = append(arguments, target, "-run", "^"+killer+"$", "-count=1")
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = root
	command.Env = append(os.Environ(), "SPEC_KITTY_WP05_MUTATED_SOURCE="+mutatedSource)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}
