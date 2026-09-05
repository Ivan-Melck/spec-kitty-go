package integration

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
	runWP10MutantsEnvV2 = "SPEC_KITTY_RUN_WP10_MUTANTS"
	wp10MutantDigestV2  = "e206ca7cc79447c6944b45eef6eea952b862e2f1504d2068e4f906d8dd099f7e"
)

type wp10SourceMutantV2 struct {
	id          string
	file        string
	anchor      string
	replacement string
	killer      string
	want        string
}

func TestWP10ExecutableControlledMutantCensusV2(t *testing.T) {
	mutants := wp10ControlledMutantsV2()
	ids := make([]string, len(mutants))
	for index, mutant := range mutants {
		ids[index] = mutant.id
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("mutant census is not canonical: %v", ids)
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	if len(mutants) != 20 || hex.EncodeToString(sum[:]) != wp10MutantDigestV2 {
		t.Fatalf("mutant census count=%d digest=%s", len(mutants), hex.EncodeToString(sum[:]))
	}
	if os.Getenv(runWP10MutantsEnvV2) != "1" {
		t.Skip("set SPEC_KITTY_RUN_WP10_MUTANTS=1 to run the bounded production source-overlay campaign")
	}
	root := wp10RepositoryRootV2(t)
	controls := make(map[string]bool)
	for _, mutant := range mutants {
		t.Run(mutant.id, func(t *testing.T) {
			target := "./internal/integration"
			if strings.HasPrefix(mutant.file, "internal/controlstore/gapdb/") {
				target = "./internal/controlstore/gapdb"
			}
			controlKey := target + "\x00" + mutant.killer
			if !controls[controlKey] {
				output, err := runWP10MutantTestV2(t, root, "", target, mutant.killer)
				if err != nil {
					t.Fatalf("production control failed: %v\n%s", err, output)
				}
				controls[controlKey] = true
			}
			overlay := wp10MutantOverlayV2(t, root, mutant)
			output, err := runWP10MutantTestV2(t, root, overlay, target, mutant.killer)
			if err == nil {
				t.Fatalf("mutant survived production-facing killer\n%s", output)
			}
			if !strings.Contains(output, mutant.want) {
				t.Fatalf("mutant failed for wrong reason: want %q\n%s", mutant.want, output)
			}
		})
	}
}

func wp10ControlledMutantsV2() []wp10SourceMutantV2 {
	return []wp10SourceMutantV2{
		{"M-WP10-ADAPTER-BYPASSES-SEALING", "internal/controlstore/gapdb/acceptance_authority.go", "if err != nil || !acceptanceTransportMembershipV2(plan) {\n\t\treturn integration.AcceptanceAckV2{}, invalidV2Plan(\"acceptance authority plan\")\n\t}", "if err != nil {\n\t\treturn integration.AcceptanceAckV2{}, nil\n\t}\n\tif !acceptanceTransportMembershipV2(plan) {\n\t\treturn integration.AcceptanceAckV2{}, invalidV2Plan(\"acceptance authority plan\")\n\t}", "TestAcceptanceAndIntegrationGapDBRejectUnsealedCapabilitiesV2", "acceptance adapter admitted an unsealed capability"},
		{"M-WP10-AMBIGUOUS-BLIND-RETRY", "internal/integration/integration_authority_v2.go", "publication, found, publishErr = s.git.ObserveTargetPublication(ctx, intent)", "publication, publishErr = s.git.CompareAndSwapTarget(ctx, intent)\n\t\t\t\tfound = publishErr == nil", "TestIntegrationLostRefUpdateResponseIsOneAdmissionV2", "result="},
		{"M-WP10-ANCESTRY-WITHOUT-ATTRIBUTION", "internal/integration/integration_authority_v2.go", "\t\t\t\tif !found {", "\t\t\t\tif false && !found {", "TestIntegrationRefPublicationNeedsAttributableEffectNotAncestryV2", "unattributable ancestry result"},
		{"M-WP10-CANDIDATE-OMITTED", "internal/integration/acceptance_authority_v2.go", " || h.Candidate != r.Candidate", "", "TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2/candidate_mismatch", "mutant accepted"},
		{"M-WP10-CLEANUP-CONTROLS-COMPLETION", "internal/integration/integration_authority_v2.go", "next.CleanupStatus = CleanupEligible", "next.CleanupStatus = CleanupComplete\n\t\t\tnext.CleanupMarker = \"implicit-cleanup\"", "TestIntegrationCleanupIsIndependentRetryableAuthorityV2", "integration result"},
		{"M-WP10-CLEANUP-DIGEST-SKIPPED", "internal/integration/cleanup_effect_v2.go", "opValueDigest, digestErr := journalv2.ReducerStateDigestV2(opValue)\n\tif opErr != nil || digestErr != nil || opValueDigest != observed.SourceOperationValueDigest", "_, digestErr := journalv2.ReducerStateDigestV2(opValue)\n\tif opErr != nil || digestErr != nil || false", "TestIntegrationCleanupRejectsSubstitutedSourceOperationV2/value_digest", "substituted source operation digest accepted"},
		{"M-WP10-CLEANUP-EFFECT-MEMBERSHIP-SKIPPED", "internal/integration/cleanup_effect_v2.go", "!containsCleanupEffectV2(op.EffectIDs(), a.CleanupEffectID)", "false", "TestIntegrationCleanupRejectsSubstitutedSourceOperationV2/cleanup_effect_membership", "source operation without cleanup effect accepted"},
		{"M-WP10-CONFLICT-BOUND-REMOVED", "internal/integration/integration_authority_v2.go", "len(value.Workspace) > maxIntegrationWorkspacePathV2 || len(value.GitMessage) > maxIntegrationExternalTextV2 ||", "len(value.Workspace) > maxIntegrationWorkspacePathV2 ||", "TestIntegrationExternalEvidenceAndDiagnosticsAreBoundedV2", "oversized conflict evidence entered durable state"},
		{"M-WP10-CONFLICT-DISCARDED", "internal/integration/integration_authority_v2.go", "if mergeErr != nil || conflict != nil {", "if false {", "TestIntegrationConflictAndManualMovementRequireRepairV2/merge_conflict", "result="},
		{"M-WP10-EARLY-REPOSITORY-OBSERVATION", "internal/integration/integration_authority_v2.go", "\t\tproof, err := s.requireIntegrationAuthorityV2(ctx, request)", "\t\t_, _ = s.git.TargetHead(ctx, request.RepositoryID, request.TargetRef)\n\t\tproof, err := s.requireIntegrationAuthorityV2(ctx, request)", "TestIntegrationRefusesBeforeAnyRepositoryObservationV2", "git=1"},
		{"M-WP10-ELIGIBILITY-DOWNGRADED", "internal/integration/integration_authority_v2.go", "token, err := observed.Eligibility.Token(request.WorkID, request.WorkRevisionID, workgraph.EligibilityIntegration)\n\tif err != nil {", "token, err := observed.Eligibility.Token(request.WorkID, request.WorkRevisionID, workgraph.EligibilityIntegration)\n\tif false && err != nil {", "TestIntegrationCannotDowngradeZeroEligibilityBeforeGitV2", "zero eligibility crossed integration boundary"},
		{"M-WP10-MANUAL-MOVEMENT-SKIPPED", "internal/integration/integration_authority_v2.go", "if current != attempt.ExpectedOld {", "if false && current != attempt.ExpectedOld {", "TestIntegrationConflictAndManualMovementRequireRepairV2/manual_movement", "plans=5"},
		{"M-WP10-POLICY-DETAIL-IGNORED", "internal/integration/acceptance_authority_v2.go", " || fact.DetailDigest != detail", "", "TestAcceptancePolicyWaiverAndApplicabilityNeedCurrentDurableFactsV2/policy_detail_substituted", "fabricated policy accepted"},
		{"M-WP10-PUBLISH-OBSERVATION-IGNORED", "internal/integration/integration_authority_v2.go", "if !publication.validates(intent) {", "if false && !publication.validates(intent) {", "TestIntegrationRejectsSubstitutedPublishObservationV2", "substituted publication observation accepted"},
		{"M-WP10-RAW-GIT-DIAGNOSTIC", "internal/integration/integration_authority_v2.go", "return &Error{Kind: KindGitFailure, Message: message, Cause: cause}", "return &Error{Kind: KindGitFailure, Message: message + \": \" + cause.Error(), Cause: cause}", "TestIntegrationExternalEvidenceAndDiagnosticsAreBoundedV2", "unbounded public diagnostic"},
		{"M-WP10-REDUCER-IO", "internal/integration/integration_authority_v2.go", "\t\"fmt\"", "\t\"fmt\"\n\t\"os\"", "TestIntegrationReducersProductionSurfaceStructurallyNoIOV2", "I/O-capable reducer call"},
		{"M-WP10-REVIEW-INDEPENDENCE-SKIPPED", "internal/integration/acceptance_authority_v2.go", "if _, self := contributors[state.ReviewerActorID]; self && state.Exception == nil {", "if _, self := contributors[state.ReviewerActorID]; false && self && state.Exception == nil {", "TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2/same_actor_review", "mutant accepted"},
		{"M-WP10-REVIEW-OMITTED", "internal/integration/acceptance_authority_v2.go", "if len(observed.Reviews) != len(wantedReviewIDs) {", "if false && len(observed.Reviews) != len(wantedReviewIDs) {", "TestAcceptanceCannotOmitRequiredReviewV2", "required review omission accepted"},
		{"M-WP10-TARGET-COORDINATE-GLOBAL", "internal/integration/integration_authority_v2.go", "sum := sha256.Sum256([]byte(repositoryID.String() + \"\\x00\" + targetRef))", "sum := sha256.Sum256([]byte(repositoryID.String()))", "TestIndependentTargetsProgressConcurrentlyWithoutGlobalLockV2", "independent target failed"},
		{"M-WP10-WORKSPACE-BOUND-REMOVED", "internal/integration/integration_authority_v2.go", "if len(value.Path) > maxIntegrationWorkspacePathV2 || !utf8.ValidString(value.Path) {", "if false && (len(value.Path) > maxIntegrationWorkspacePathV2 || !utf8.ValidString(value.Path)) {", "TestIntegrationExternalEvidenceAndDiagnosticsAreBoundedV2", "oversized workspace evidence entered durable state"},
	}
}

func TestIntegrationReducersProductionSurfaceStructurallyNoIOV2(t *testing.T) {
	path := os.Getenv("SPEC_KITTY_WP10_MUTATED_SOURCE")
	if path == "" {
		path = filepath.Join(wp10RepositoryRootV2(t), "internal/integration/integration_authority_v2.go")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	imports := make(map[string]string)
	for _, item := range parsed.Imports {
		importPath := strings.Trim(item.Path.Value, `"`)
		name := filepath.Base(importPath)
		if item.Name != nil {
			name = item.Name.Name
		}
		imports[name] = importPath
	}
	found := false
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "ReduceIntegrationAttemptEventV2" {
			continue
		}
		found = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch imports[owner.Name] {
			case "os", "io", "net", "net/http", "database/sql":
				t.Fatalf("I/O-capable reducer call: %s.%s", owner.Name, selector.Sel.Name)
			}
			return true
		})
	}
	if !found {
		t.Fatal("integration attempt reducer not found")
	}
}

func wp10RepositoryRootV2(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func wp10MutantOverlayV2(t *testing.T, root string, mutant wp10SourceMutantV2) string {
	t.Helper()
	sourcePath := filepath.Join(root, mutant.file)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(source), mutant.anchor); count != 1 {
		t.Fatalf("mutant anchor count=%d want=1 anchor=%q", count, mutant.anchor)
	}
	temporary := t.TempDir()
	mutatedPath := filepath.Join(temporary, filepath.Base(sourcePath))
	mutated := strings.Replace(string(source), mutant.anchor, mutant.replacement, 1)
	if mutant.id == "M-WP10-REDUCER-IO" {
		anchor := "func ReduceIntegrationAttemptEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {"
		if strings.Count(mutated, anchor) != 1 {
			t.Fatal("integration reducer anchor is not exact")
		}
		mutated = strings.Replace(mutated, anchor, anchor+"\n\t_, _ = os.ReadFile(\"/tmp/spec-kitty-wp10-mutant\")", 1)
	}
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
	return overlayPath
}

func runWP10MutantTestV2(t *testing.T, root, overlay, target, killer string) (string, error) {
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
	if overlay != "" {
		// The structural reducer killer inspects the overlaid source directly.
		var overlayDocument struct{ Replace map[string]string }
		contents, _ := os.ReadFile(overlay)
		_ = json.Unmarshal(contents, &overlayDocument)
		for _, path := range overlayDocument.Replace {
			command.Env = append(os.Environ(), "SPEC_KITTY_WP10_MUTATED_SOURCE="+path)
		}
	}
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}
