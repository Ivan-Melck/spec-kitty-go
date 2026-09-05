package mission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	runReadinessMutantsEnv = "SPEC_KITTY_RUN_WP18_MUTANTS"
	readinessMutatedSource = "SPEC_KITTY_WP18_MUTATED_SOURCE"
	readinessMutantDigest  = "6aa8696ef720819239b9f36fd04a35aecb294bb39e1c813d7a3003114df2bcb2"
)

type readinessReplacement struct {
	anchor      string
	replacement string
}

type readinessSourceMutant struct {
	id           string
	file         string
	replacements []readinessReplacement
	target       string
	killer       string
	want         string
}

func TestAutonomousReadinessExecutableControlledMutants(t *testing.T) {
	if os.Getenv(runReadinessMutantsEnv) != "1" {
		t.Skip("set SPEC_KITTY_RUN_WP18_MUTANTS=1 to run the bounded source-overlay campaign")
	}
	root := readinessRepositoryRoot(t)
	mutants := autonomousReadinessMutants()
	if len(mutants) != 20 {
		t.Fatalf("executable mutant census=%d want=20", len(mutants))
	}
	identities := make([]string, 0, len(mutants))
	seen := make(map[string]bool, len(mutants))
	for _, mutant := range mutants {
		if seen[mutant.id] {
			t.Fatalf("duplicate mutant identity %q", mutant.id)
		}
		seen[mutant.id] = true
		identities = append(identities, mutant.id)
	}
	sort.Strings(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	if hex.EncodeToString(digest[:]) != readinessMutantDigest {
		t.Fatalf("mutant census changed: digest=%x", digest)
	}

	controls := make(map[string]bool)
	for _, mutant := range mutants {
		t.Run(mutant.id, func(t *testing.T) {
			controlKey := mutant.target + "\x00" + mutant.killer
			if !controls[controlKey] {
				output, err := runReadinessMutantTest(t, root, "", mutant.target, mutant.killer, "")
				if err != nil {
					t.Fatalf("production control failed: %v\n%s", err, output)
				}
				controls[controlKey] = true
			}
			overlay, mutated := readinessMutantOverlay(t, root, mutant)
			output, err := runReadinessMutantTest(t, root, overlay, mutant.target, mutant.killer, mutated)
			if err == nil {
				t.Fatalf("mutant survived production-facing killer\n%s", output)
			}
			if !strings.Contains(output, mutant.want) {
				t.Fatalf("mutant failed for wrong reason: want %q\n%s", mutant.want, output)
			}
		})
	}
}

func autonomousReadinessMutants() []readinessSourceMutant {
	return []readinessSourceMutant{
		{id: "M18-CURRENT-STEP-AUTHORITY", file: "internal/mission/finalization.go", replacements: []readinessReplacement{{anchor: "command.ExpectedMission.Lifecycle != LifecycleOpen || command.ExpectedMission.CurrentWorkRevisionID != \"\"", replacement: "command.ExpectedMission.Lifecycle != LifecycleOpen || command.ExpectedMission.CurrentStep != StepWorkDraftReady || command.ExpectedMission.CurrentWorkRevisionID != \"\""}}, target: "./internal/mission", killer: "TestFinalizationIsCapabilityReadyWithoutWorkDraftPhaseAndDoesNotCompleteMission", want: "mission is not open"},
		{id: "M18-FIRST-ACTION-ONLY", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "\twire := struct {\n\t\tSchemaVersion", replacement: "\tif len(census.authorized) > 1 { census.authorized = census.authorized[:1] }\n\twire := struct {\n\t\tSchemaVersion"}}, target: "./internal/mission", killer: "TestCapabilityReadinessReturnsCompleteCensusAndParallelActions", want: "authorized="},
		{id: "M18-MULTIPLE-ACTIONS-AMBIGUOUS", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "\twire := struct {\n\t\tSchemaVersion", replacement: "\tif len(census.authorized) > 1 { return ActionCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: \"ambiguous\"} }\n\twire := struct {\n\t\tSchemaVersion"}}, target: "./internal/mission", killer: "TestCapabilityReadinessReturnsCompleteCensusAndParallelActions", want: "INVALID_INPUT: ambiguous"},
		{id: "M18-GLOBAL-BLOCKER", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "if slices.Contains(blocker.Affects, ref) {", replacement: "if len(blocker.Affects) != 0 {"}}, target: "./internal/mission", killer: "TestCapabilityReadinessScopesDecisionsAndBlockersToAffectedActions", want: "global ceremony gate"},
		{id: "M18-PROJECTION-MINTS-AUTHORITY", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "fact.Authority.Validate() != nil || !fact.Authority.factSource() || ", replacement: ""}, {anchor: " || fact.Authority.Family != predicateAuthorityFamily(fact.Kind)", replacement: ""}}, target: "./internal/mission", killer: "TestCapabilityReadinessRejectsProjectionAndProseAsAuthority", want: "minted readiness"},
		{id: "M18-PRODUCERLESS-FACT", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "fact.Producer.Validate() != nil || ", replacement: ""}, {anchor: "if fact.Producer != requirement.ResponsibleCapability || ", replacement: "if fact.Producer != \"\" && fact.Producer != requirement.ResponsibleCapability || "}}, target: "./internal/mission", killer: "TestCapabilityReadinessApplicabilityAndInstructionAuthority", want: "producerless invariant accepted"},
		{id: "M18-INAPPLICABLE-ARTIFACT-REQUIRED", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "} else if fact.Status == PredicateInapplicable {", replacement: "} else if fact.Status == PredicateSatisfied {"}}, target: "./internal/mission", killer: "TestCapabilityReadinessApplicabilityAndInstructionAuthority", want: "crossed the execution boundary"},
		{id: "M18-UNAVAILABLE-COLLAPSED", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "Status: fact.Status, Authority:", replacement: "Status: PredicateFailed, Authority:"}}, target: "./internal/mission", killer: "TestCapabilityReadinessPreservesUnavailableFailedStaleWaivedAndAmbiguous/unavailable", want: "was collapsed"},
		{id: "M18-SKIPPED-MEMBER-HIDDEN", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "census.assessments = append(census.assessments, ActionAssessment{Action: ref, Status: AssessmentInapplicable})", replacement: "continue"}}, target: "./internal/mission", killer: "TestCapabilityReadinessKeepsScopedWorkInstancesAndInapplicableActionsVisible", want: "census collapsed"},
		{id: "M18-ACTIVITY-ORDER-AFFECTS-AUTHORITY", file: "internal/mission/readiness_authority_v2.go", replacements: []readinessReplacement{{anchor: "\t\tActiveFacts         []ScopedMissionFactV2 `json:\"active_facts\"`\n\t}{", replacement: "\t\tActiveFacts         []ScopedMissionFactV2 `json:\"active_facts\"`\n\t\tActivityCount       uint64                `json:\"activity_count\"`\n\t}{"}, {anchor: "state.input.CurrentWorkRevision.CanonicalBytes(), state.facts})", replacement: "state.input.CurrentWorkRevision.CanonicalBytes(), state.facts, state.input.ActivityCount})"}}, target: "./internal/mission", killer: "TestReadinessAuthorityStateHasNoPhaseCursorAndFactDigestIgnoresActivityOrder", want: "activity/version metadata changed authority fact digest"},
		{id: "M18-IO-EVALUATOR", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "\t\"fmt\"", replacement: "\t\"fmt\"\n\t\"os\""}, {anchor: "func EvaluateReadiness(snapshot ReadinessSnapshot) (ActionCensus, error) {", replacement: "func EvaluateReadiness(snapshot ReadinessSnapshot) (ActionCensus, error) {\n\t_, _ = os.ReadFile(\"/tmp/spec-kitty-wp18-mutant\")"}}, target: "./internal/mission", killer: "TestReadinessEvaluatorProductionSurfaceHasNoIOOrCeremony", want: "I/O-capable evaluator"},
		{id: "M18-INCOMPLETE-DEFINITION", file: "internal/mission/readiness.go", replacements: []readinessReplacement{{anchor: "func NewCapabilityDefinition(templates []CapabilityTemplate) (CapabilityDefinition, error) {", replacement: "func NewCapabilityDefinition(templates []CapabilityTemplate) (CapabilityDefinition, error) {\n\tif len(templates) == 1 { return CapabilityDefinition{templates: slices.Clone(templates), canonical: []byte(\"{}\")}, nil }"}}, target: "./internal/mission", killer: "TestReadinessRequiresCompleteSealedDefinitionScopeCensusAndAuthorityMembership", want: "partial action definition was accepted"},
		{id: "M18-LEGACY-PHASE-STATE-ACCEPTED", file: "internal/mission/readiness_authority_v2.go", replacements: []readinessReplacement{{anchor: "if header.SchemaVersion == MissionAuthoritySchemaVersionV2 {", replacement: "if false && header.SchemaVersion == MissionAuthoritySchemaVersionV2 {"}}, target: "./internal/mission", killer: "TestReadinessAuthorityReducerReplaysCoarseLifecycleAndRejectsLegacyPhaseState", want: "legacy phase state entered readiness"},
		{id: "M18-LIFECYCLE-ADVANCE-RETURNS", file: "internal/mission/readiness_authority_v2.go", replacements: []readinessReplacement{{anchor: "ReadinessEventFactOpenedV2, ReadinessEventFactClosedV2, ReadinessEventWorkRevisionSelectedV2:", replacement: "ReadinessEventFactOpenedV2, ReadinessEventFactClosedV2, ReadinessEventWorkRevisionSelectedV2, ReadinessEventTypeV2(\"lifecycle_advanced\"):"}, {anchor: "\tdefault:\n\t\treturn missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, \"event_type\", nil)\n\t}\n\treturn nil", replacement: "\tcase ReadinessEventTypeV2(\"lifecycle_advanced\"):\n\t\treturn nil\n\tdefault:\n\t\treturn missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, \"event_type\", nil)\n\t}\n\treturn nil"}}, target: "./internal/mission", killer: "TestReadinessAuthorityTransitionAllowsActivityWithoutLifecycleAdvance", want: "lifecycle advance re-entered"},
		{id: "M18-UNSTABLE-GAPDB-READ", file: "internal/controlstore/gapdb/mission_authority.go", replacements: []readinessReplacement{{anchor: "\tif err := a.store.VerifyRevisionV2(ctx, attempt); err != nil {\n\t\treturn mission.ReadinessAuthorityObservationV2{}, err\n\t}\n", replacement: ""}}, target: "./internal/controlstore/gapdb", killer: "TestReadinessAuthorityAdapterRefusesOneRevisionDriftV2", want: "one-revision drift was accepted"},
		{id: "M18-UNACKNOWLEDGED-WORKGRAPH", file: "internal/mission/readiness_workrevision.go", replacements: []readinessReplacement{{anchor: "func SealAcknowledgedInitialWorkRevisionSelectionV2(revision workgraph.RevisionV2, acknowledged journalv2.AcknowledgedPublicationV2) (WorkRevisionSelectionV2, error) {", replacement: "func SealAcknowledgedInitialWorkRevisionSelectionV2(revision workgraph.RevisionV2, acknowledged journalv2.AcknowledgedPublicationV2) (WorkRevisionSelectionV2, error) {\n\tif acknowledged.Plan().Digest().IsZero() {\n\t\tencoded, _ := revision.Encode()\n\t\thead, _ := sealWorkRevisionAuthorityV2(revision)\n\t\tpublication := newWorkRevisionPublicationV2(revision, strings.Repeat(\"a\", 64), strings.Repeat(\"b\", 64))\n\t\treturn sealWorkRevisionSelectionV2(head, encoded, publication.canonical, \"\", \"\", nil)\n\t}"}}, target: "./internal/mission", killer: "TestAcknowledgedInitialWorkRevisionSelectionBindsExactV2Publication", want: "unacknowledged revision became selectable"},
		{id: "M18-SUBSTITUTED-WORKGRAPH", file: "internal/mission/readiness_workrevision.go", replacements: []readinessReplacement{{anchor: " || !bytes.Equal(payload.Revision, encoded)", replacement: ""}, {anchor: "if err != nil || operation.ResultKind() != \"workgraph.result\" || operation.ResultSchemaVersion() != 1 || !bytes.Equal(operation.Result(), exactResult) {", replacement: "_ = exactResult\n\tif err != nil || operation.ResultKind() != \"workgraph.result\" || operation.ResultSchemaVersion() != 1 {"}}, target: "./internal/mission", killer: "TestAcknowledgedInitialWorkRevisionSelectionBindsExactV2Publication", want: "substituted revision inherited acknowledgement"},
		{id: "M18-SUBSTITUTED-WORKGRAPH-BINDING", file: "internal/mission/readiness_workrevision.go", replacements: []readinessReplacement{{anchor: "if err != nil || binding.CommandKind() != \"workgraph.finalized\" || binding.CommandSchemaVersion() != 1 || binding.Scope() != exactScope || binding.IntentDigest() != exactIntent {", replacement: "if _ = exactIntent; err != nil || binding.CommandSchemaVersion() != 1 || binding.Scope() != exactScope {"}}, target: "./internal/mission", killer: "TestAcknowledgedInitialWorkRevisionSelectionBindsExactV2Publication", want: "wrong command kind inherited workgraph authority"},
		{id: "M18-SUBSTITUTED-WORKGRAPH-SCOPE", file: "internal/mission/readiness_workrevision.go", replacements: []readinessReplacement{{anchor: "if err != nil || binding.CommandKind() != \"workgraph.finalized\" || binding.CommandSchemaVersion() != 1 || binding.Scope() != exactScope || binding.IntentDigest() != exactIntent {", replacement: "if _ = exactIntent; err != nil || binding.CommandKind() != \"workgraph.finalized\" || binding.CommandSchemaVersion() != 1 {"}}, target: "./internal/mission", killer: "TestAcknowledgedInitialWorkRevisionSelectionBindsExactV2Publication", want: "wrong mission scope inherited workgraph authority"},
		{id: "M18-SUBSTITUTED-WORKGRAPH-RESULT", file: "internal/mission/readiness_workrevision.go", replacements: []readinessReplacement{{anchor: "if err != nil || operation.ResultKind() != \"workgraph.result\" || operation.ResultSchemaVersion() != 1 || !bytes.Equal(operation.Result(), exactResult) {", replacement: "_ = exactResult\n\tif err != nil || operation.ResultKind() != \"workgraph.result\" || operation.ResultSchemaVersion() != 1 {"}}, target: "./internal/mission", killer: "TestAcknowledgedInitialWorkRevisionSelectionBindsExactV2Publication", want: "wrong operation result inherited workgraph authority"},
	}
}

func TestReadinessEvaluatorProductionSurfaceHasNoIOOrCeremony(t *testing.T) {
	path := os.Getenv(readinessMutatedSource)
	if path == "" {
		path = filepath.Join(readinessRepositoryRoot(t), "internal/mission/readiness.go")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	imports := make(map[string]string)
	for _, item := range parsed.Imports {
		dependency := strings.Trim(item.Path.Value, `"`)
		name := filepath.Base(dependency)
		if item.Name != nil {
			name = item.Name.Name
		}
		imports[name] = dependency
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "EvaluateReadiness" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Ident:
				if value.Name == "CurrentStep" || strings.HasPrefix(value.Name, "Step") || value.Name == "Successor" {
					t.Fatalf("legacy ceremony symbol %s", value.Name)
				}
			case *ast.SelectorExpr:
				owner, ok := value.X.(*ast.Ident)
				if ok {
					dependency := imports[owner.Name]
					if dependency == "os" || dependency == "io" || dependency == "net" || dependency == "net/http" || dependency == "database/sql" {
						t.Fatalf("I/O-capable evaluator call %s.%s", owner.Name, value.Sel.Name)
					}
				}
			}
			return true
		})
		return
	}
	t.Fatal("readiness evaluator not found")
}

func readinessRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func readinessMutantOverlay(t *testing.T, root string, mutant readinessSourceMutant) (string, string) {
	t.Helper()
	sourcePath := filepath.Join(root, mutant.file)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	mutated := string(source)
	for _, replacement := range mutant.replacements {
		if count := strings.Count(mutated, replacement.anchor); count != 1 {
			t.Fatalf("mutant anchor count=%d want=1 anchor=%q", count, replacement.anchor)
		}
		mutated = strings.Replace(mutated, replacement.anchor, replacement.replacement, 1)
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

func runReadinessMutantTest(t *testing.T, root, overlay, target, killer, mutated string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	arguments := []string{"test"}
	if overlay != "" {
		arguments = append(arguments, "-overlay="+overlay)
	}
	arguments = append(arguments, target, "-run", "^"+killer+"$", "-count=1")
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = root
	command.Env = append(os.Environ(), readinessMutatedSource+"="+mutated)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}
