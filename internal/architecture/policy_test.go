package architecture

import (
	"context"
	"strings"
	"testing"
	"time"
)

const fixtureModule = "example.test/system"

func TestPolicyAcceptsProviderOnlyFromClassifiedAdapter(t *testing.T) {
	t.Run("generic classified adapter", func(t *testing.T) {
		policy := fixturePolicy(t)
		graph := fixtureGraph(
			[]string{fixtureModule + "/internal/tracker", fixtureModule + "/internal/tracker/example"},
			ImportEdge{SourcePackage: fixtureModule + "/internal/tracker/example", TargetPackage: "example.invalid/provider", Kind: ImportExternal},
			ImportEdge{SourcePackage: fixtureModule + "/internal/tracker/example", TargetPackage: fixtureModule + "/internal/tracker", Kind: ImportModuleLocal},
		)
		if err := policy.Check(graph); err != nil {
			t.Fatalf("adapter fixture rejected: %v", err)
		}
	})

	t.Run("loaded Beads command protocol shape", func(t *testing.T) {
		const module = "example.test/beads-command-fixture"
		root := t.TempDir()
		writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
		writeGoFixture(t, root, "internal/tracker/tracker.go", `package tracker

type Ticket struct { ID string }
`)
		writeGoFixture(t, root, "internal/tracker/beads/beads.go", `package beads

import _ "`+module+`/internal/tracker/beads/protocol/jsonv1"

const Executable = "bd"
`)
		writeGoFixture(t, root, "internal/tracker/beads/protocol/jsonv1/envelope.go", `package jsonv1

import (
	"encoding/json"
	"os/exec"

	"`+module+`/internal/tracker"
)

const (
	EnvironmentKey = "BD_JSON_ENVELOPE"
	SchemaVersion = 1
)

func Command(ticket tracker.Ticket) *exec.Cmd {
	_ = json.Valid([]byte("{}"))
	return exec.Command("bd", "show", ticket.ID)
}
`)
		setIsolatedGoPaths(t)
		t.Setenv("GOPROXY", "off")

		graph, err := LoadGraph(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := DefaultPolicy(module)
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.Check(graph); err != nil {
			t.Fatalf("production-shaped command adapter rejected: %v", err)
		}
		assertTier(t, policy, module+"/internal/tracker", TierProviderContract)
		assertTier(t, policy, module+"/internal/tracker/beads", TierAdapter)
		assertTier(t, policy, module+"/internal/tracker/beads/protocol/jsonv1", TierAdapter)

		for _, relative := range []string{
			"issues", "dependencies", "comments", "assignments", "status-observations", "readiness-diagnostics",
		} {
			assertTier(t, policy, module+"/internal/tracker/beads/"+relative, TierAdapter)
		}
		assertTier(t, policy, module+"/internal/tracker/beads/molecules-compatible", TierAdapter)
	})
}

func TestPolicyRejectingFixturesNameExactRuleAndEdge(t *testing.T) {
	kernel := fixtureModule + "/internal/kernel"
	contract := fixtureModule + "/internal/tracker"
	adapter := fixtureModule + "/internal/tracker/example"
	tests := []struct {
		name   string
		mutate func(Policy) Policy
		graph  Graph
		rule   RuleID
		source string
		target string
	}{
		{name: "kernel-to-adapter", graph: fixtureGraph([]string{kernel, adapter}, ImportEdge{SourcePackage: kernel, TargetPackage: adapter, Kind: ImportModuleLocal}), rule: RuleLocalDirection, source: kernel, target: adapter},
		{name: "kernel-to-provider", graph: fixtureGraph([]string{kernel}, ImportEdge{SourcePackage: kernel, TargetPackage: "example.invalid/provider", Kind: ImportExternal}), rule: RuleExternalDirection, source: kernel, target: "example.invalid/provider"},
		{name: "unknown-root", graph: fixtureGraph([]string{fixtureModule + "/internal/unreviewed"}), rule: RuleUnknownPackage, source: fixtureModule + "/internal/unreviewed", target: fixtureModule + "/internal/unreviewed"},
		{
			name: "ambiguous-tier",
			mutate: func(policy Policy) Policy {
				policy.Tiers[0].Packages = append(policy.Tiers[0].Packages, ExactPackage(kernel))
				return policy
			},
			graph: fixtureGraph([]string{kernel}), rule: RuleAmbiguousPackage, source: kernel, target: kernel,
		},
		{
			name: "removed-load-bearing-rule",
			mutate: func(policy Policy) Policy {
				for index := range policy.Tiers {
					if policy.Tiers[index].Name == TierAuthority {
						policy.Tiers[index].AllowedLocalTargets = []TierName{TierFoundation, TierAuthority}
					}
				}
				return policy
			},
			graph: fixtureGraph([]string{kernel, contract}, ImportEdge{SourcePackage: kernel, TargetPackage: contract, Kind: ImportModuleLocal}),
			rule:  RuleLocalDirection, source: kernel, target: contract,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			policy := fixturePolicy(t)
			if test.mutate != nil {
				policy = test.mutate(policy)
			}
			violations := policy.Evaluate(test.graph)
			if len(violations) != 1 {
				t.Fatalf("mutant=%s violations=%d details=%v", test.name, len(violations), violations)
			}
			got := violations[0]
			if got.Rule != test.rule || got.SourcePackage != test.source || got.TargetPackage != test.target {
				t.Fatalf("mutant=%s got=%v want_rule=%s want_edge=%s->%s", test.name, got, test.rule, test.source, test.target)
			}
			diagnostic := got.Error()
			for _, required := range []string{"rule=" + string(test.rule), "source=" + test.source, "target=" + test.target} {
				if !strings.Contains(diagnostic, required) {
					t.Fatalf("diagnostic %q lacks %q", diagnostic, required)
				}
			}
			if repeated := policy.Evaluate(test.graph).Error(); repeated != violations.Error() {
				t.Fatalf("mutant=%s diagnostics changed: first=%q repeated=%q", test.name, violations.Error(), repeated)
			}
			t.Logf("mutant=%s killed_in=%s", test.name, time.Since(started))
		})
	}

	t.Run("permanent Beads boundary", func(t *testing.T) {
		const module = "github.com/spec-kitty/go-kitty3"
		policy, err := DefaultPolicy(module)
		if err != nil {
			t.Fatal(err)
		}
		contract := module + "/internal/tracker"
		beads := module + "/internal/tracker/beads"
		mutants := []struct {
			name       string
			source     string
			target     string
			kind       ImportKind
			rule       RuleID
			sourceTier TierName
			targetTier TierName
		}{
			{name: "execution to adapter root", source: module + "/internal/execution", target: beads, kind: ImportModuleLocal, rule: RuleLocalDirection, sourceTier: TierAuthority, targetTier: TierAdapter},
			{name: "execution to nested protocol", source: module + "/internal/execution", target: beads + "/protocol/jsonv1", kind: ImportModuleLocal, rule: RuleLocalDirection, sourceTier: TierAuthority, targetTier: TierAdapter},
			{name: "review to adapter root", source: module + "/internal/review", target: beads, kind: ImportModuleLocal, rule: RuleLocalDirection, sourceTier: TierAuthority, targetTier: TierAdapter},
			{name: "integration to nested protocol", source: module + "/internal/integration", target: beads + "/protocol", kind: ImportModuleLocal, rule: RuleLocalDirection, sourceTier: TierAuthority, targetTier: TierAdapter},
			{name: "execution to historic module root", source: module + "/internal/execution", target: "github.com/steveyegge/beads", kind: ImportExternal, rule: RuleDeferredSurface, sourceTier: TierAuthority},
			{name: "execution to historic module", source: module + "/internal/execution", target: "github.com/steveyegge/beads/internal/storage", kind: ImportExternal, rule: RuleDeferredSurface, sourceTier: TierAuthority},
			{name: "adapter to historic module", source: beads, target: "github.com/steveyegge/beads/internal/storage", kind: ImportExternal, rule: RuleDeferredSurface, sourceTier: TierAdapter},
			{name: "protocol to current module root", source: beads + "/protocol", target: "github.com/gastownhall/beads", kind: ImportExternal, rule: RuleDeferredSurface, sourceTier: TierAdapter},
			{name: "protocol to current module", source: beads + "/protocol", target: "github.com/gastownhall/beads/internal/storage", kind: ImportExternal, rule: RuleDeferredSurface, sourceTier: TierAdapter},
			{name: "look-alike module remains ordinary external import", source: beads, target: "github.com/gastownhall/beadsx/internal/storage", kind: ImportExternal, rule: RuleExternalDirection, sourceTier: TierAdapter},
		}
		for _, mutant := range mutants {
			t.Run(mutant.name, func(t *testing.T) {
				packages := []string{mutant.source}
				if mutant.kind == ImportModuleLocal {
					packages = append(packages, mutant.target)
				}
				graph := fixtureGraph(packages, ImportEdge{SourcePackage: mutant.source, TargetPackage: mutant.target, Kind: mutant.kind})
				graph.ModulePath = module
				assertExactViolation(t, policy, graph, mutant.rule, mutant.source, mutant.sourceTier, mutant.target, mutant.targetTier)
			})
		}

		t.Run("kernel to tracker contract remains accepted", func(t *testing.T) {
			source := module + "/internal/execution"
			graph := fixtureGraph([]string{source, contract}, ImportEdge{SourcePackage: source, TargetPackage: contract, Kind: ImportModuleLocal})
			graph.ModulePath = module
			if err := policy.Check(graph); err != nil {
				t.Fatalf("authority-to-contract control rejected: %v", err)
			}
		})

		workflowSubtrees := []string{
			"molecules", "formulas", "agent-identity", "merge-slots", "custom-mission-status", "compaction", "git-hooks",
		}
		for _, subtree := range workflowSubtrees {
			for _, suffix := range []string{"", "/nested"} {
				path := beads + "/" + subtree + suffix
				t.Run("deferred "+subtree+strings.ReplaceAll(suffix, "/", "-"), func(t *testing.T) {
					graph := fixtureGraph([]string{path})
					graph.ModulePath = module
					assertExactViolation(t, policy, graph, RuleDeferredSurface, path, "", path, "")
				})
			}
		}
	})
}

func TestDefaultPolicyRejectsAuthorityImportOfGitAdapter(t *testing.T) {
	const module = "github.com/spec-kitty/go-kitty3"
	policy, err := DefaultPolicy(module)
	if err != nil {
		t.Fatal(err)
	}
	source := module + "/internal/handoff"
	target := module + "/internal/gitrepo"
	graph := fixtureGraph([]string{source, target}, ImportEdge{
		SourcePackage: source,
		TargetPackage: target,
		Kind:          ImportModuleLocal,
	})
	graph.ModulePath = module
	violations := policy.Evaluate(graph)
	if len(violations) != 1 || violations[0].Rule != RuleLocalDirection || violations[0].SourcePackage != source || violations[0].TargetPackage != target {
		t.Fatalf("authority-to-git-adapter edge was not rejected exactly: %+v", violations)
	}
}

func TestDefaultPolicyClassifiesCampaignToolsWithoutExposingThemToAuthority(t *testing.T) {
	const module = "github.com/spec-kitty/go-kitty3"
	policy, err := DefaultPolicy(module)
	if err != nil {
		t.Fatal(err)
	}
	tool := module + "/internal/testcampaign/cmd/campaign"
	tier, err := policy.Classify(tool)
	if err != nil || tier.Name != TierQuality || tier.Production {
		t.Fatalf("campaign tool classification=%+v err=%v", tier, err)
	}
	authority := module + "/internal/execution"
	graph := fixtureGraph([]string{authority, tool}, ImportEdge{
		SourcePackage: authority,
		TargetPackage: tool,
		Kind:          ImportModuleLocal,
	})
	graph.ModulePath = module
	violations := policy.Evaluate(graph)
	if len(violations) != 1 || violations[0].Rule != RuleLocalDirection || violations[0].SourcePackage != authority || violations[0].TargetPackage != tool {
		t.Fatalf("authority-to-quality edge was not rejected exactly: %+v", violations)
	}
}

func TestPackagePatternsAreSlashBoundedAndAmbiguityIsDeterministic(t *testing.T) {
	policy := fixturePolicy(t)
	for index := range policy.Tiers {
		if policy.Tiers[index].Name == TierAdapter {
			policy.Tiers[index].Packages = []PackagePattern{PackagePrefix(fixtureModule + "/internal/adapters")}
		}
	}
	nested := fixtureModule + "/internal/adapters/nested/provider"
	if tier, err := policy.Classify(nested); err != nil || tier.Name != TierAdapter {
		t.Fatalf("nested prefix classification: tier=%q err=%v", tier.Name, err)
	}
	lookalike := fixtureModule + "/internal/adaptersx"
	if _, err := policy.Classify(lookalike); err == nil || !strings.Contains(err.Error(), string(RuleUnknownPackage)) {
		t.Fatalf("look-alike prefix must remain unknown: %v", err)
	}

	policy.Tiers[0].Packages = append(policy.Tiers[0].Packages, PackagePrefix(fixtureModule+"/internal/adapters"))
	first := policy.Evaluate(fixtureGraph([]string{nested})).Error()
	second := policy.Evaluate(fixtureGraph([]string{nested})).Error()
	if first != second || !strings.Contains(first, string(RuleAmbiguousPackage)) {
		t.Fatalf("ambiguous diagnostics not deterministic: first=%q second=%q", first, second)
	}

	t.Run("permanent Beads adapter prefix is exact and slash bounded", func(t *testing.T) {
		const module = "github.com/spec-kitty/go-kitty3"
		policy, err := DefaultPolicy(module)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			path string
			tier TierName
		}{
			{path: module + "/internal/execution", tier: TierAuthority},
			{path: module + "/internal/handoff", tier: TierAuthority},
			{path: module + "/internal/integration", tier: TierAuthority},
			{path: module + "/internal/review", tier: TierAuthority},
			{path: module + "/internal/workgraph", tier: TierAuthority},
			{path: module + "/internal/workspace", tier: TierAuthority},
			{path: module + "/internal/tracker", tier: TierProviderContract},
			{path: module + "/internal/controlstore/sqlite", tier: TierAdapter},
			{path: module + "/internal/gitrepo", tier: TierAdapter},
			{path: module + "/internal/tracker/fake", tier: TierAdapter},
			{path: module + "/internal/tracker/beads", tier: TierAdapter},
			{path: module + "/internal/tracker/beads/protocol", tier: TierAdapter},
			{path: module + "/internal/tracker/beads/protocol/jsonv1", tier: TierAdapter},
			{path: module + "/internal/helpercmd", tier: TierComposition},
			{path: module + "/cmd/spk-phase1-helper", tier: TierComposition},
			{path: module + "/internal/architecture", tier: TierQuality},
			{path: module + "/internal/scenario", tier: TierQuality},
			{path: module + "/internal/testcampaign/cmd/campaign", tier: TierQuality},
		} {
			assertTier(t, policy, test.path, test.tier)
			first, second := classifyDiagnostic(policy, test.path), classifyDiagnostic(policy, test.path)
			if first != second {
				t.Fatalf("classification changed for %s: first=%q second=%q", test.path, first, second)
			}
		}
		for _, path := range []string{
			module + "/internal/tracker/beadsx",
			module + "/internal/tracker/beads-protocol",
		} {
			_, err := policy.Classify(path)
			violation, ok := err.(Violation)
			if !ok || violation.Rule != RuleUnknownPackage || violation.SourcePackage != path || violation.TargetPackage != path {
				t.Fatalf("look-alike classification path=%s err=%v", path, err)
			}
		}
	})
}

func TestPolicyValidationConfinesProviderAllowancesToAdapters(t *testing.T) {
	policy := fixturePolicy(t)
	for index := range policy.Tiers {
		if policy.Tiers[index].Name == TierAuthority {
			policy.Tiers[index].AllowedExternalPrefixes = []string{"example.invalid/provider"}
		}
	}
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "without provider-adapter") {
		t.Fatalf("kernel provider allowance was not rejected: %v", err)
	}
}

func TestTierRuleAndViolationValueValidation(t *testing.T) {
	if err := TierName("").Validate(); err == nil {
		t.Fatal("empty tier name accepted")
	}
	if err := RuleID("ARCH-TYPO").Validate(); err == nil {
		t.Fatal("unknown rule identity accepted")
	}
	if err := (Violation{Rule: RuleLocalDirection, SourcePackage: "source", TargetPackage: "target"}).Validate(); err != nil {
		t.Fatalf("valid violation rejected: %v", err)
	}
	if err := (Violation{Rule: RuleLocalDirection, SourcePackage: "source"}).Validate(); err == nil {
		t.Fatal("violation without exact target accepted")
	}
	malformed := Violations{{Rule: RuleID("ARCH-TYPO"), SourcePackage: "source", TargetPackage: "target"}}.Error()
	if !strings.Contains(malformed, "invalid architecture violation") || !strings.Contains(malformed, "ARCH-TYPO") {
		t.Fatalf("live diagnostic path did not reject malformed violation: %q", malformed)
	}
}

func fixturePolicy(t *testing.T) Policy {
	t.Helper()
	policy := Policy{ModulePath: fixtureModule, Tiers: []Tier{
		{Name: TierFoundation, Production: true, Packages: []PackagePattern{ExactPackage(fixtureModule + "/internal/ids")}, AllowedLocalTargets: []TierName{TierFoundation}},
		{Name: TierAuthority, Production: true, Packages: []PackagePattern{ExactPackage(fixtureModule + "/internal/kernel")}, AllowedLocalTargets: []TierName{TierFoundation, TierAuthority, TierProviderContract}},
		{Name: TierProviderContract, Production: true, Packages: []PackagePattern{ExactPackage(fixtureModule + "/internal/tracker")}, AllowedLocalTargets: []TierName{TierFoundation, TierProviderContract}},
		{Name: TierAdapter, Production: true, ProviderAdapter: true, Packages: []PackagePattern{ExactPackage(fixtureModule + "/internal/tracker/example")}, AllowedLocalTargets: []TierName{TierFoundation, TierProviderContract, TierAdapter}, AllowedExternalPrefixes: []string{"example.invalid/provider"}},
		{Name: TierComposition, Production: true, Packages: []PackagePattern{ExactPackage(fixtureModule + "/cmd/app")}, AllowedLocalTargets: []TierName{TierFoundation, TierAuthority, TierProviderContract, TierAdapter, TierComposition}},
	}}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}

func fixtureGraph(packages []string, edges ...ImportEdge) Graph {
	graph := Graph{ModulePath: fixtureModule, Edges: edges}
	for _, packagePath := range packages {
		graph.Packages = append(graph.Packages, Package{ImportPath: packagePath})
	}
	return graph
}

func assertTier(t *testing.T, policy Policy, packagePath string, want TierName) {
	t.Helper()
	tier, err := policy.Classify(packagePath)
	if err != nil || tier.Name != want {
		t.Fatalf("classification path=%s tier=%q err=%v want=%q", packagePath, tier.Name, err, want)
	}
}

func classifyDiagnostic(policy Policy, packagePath string) string {
	tier, err := policy.Classify(packagePath)
	if err != nil {
		return err.Error()
	}
	return string(tier.Name)
}

func assertExactViolation(t *testing.T, policy Policy, graph Graph, rule RuleID, source string, sourceTier TierName, target string, targetTier TierName) {
	t.Helper()
	first := policy.Evaluate(graph)
	if len(first) != 1 {
		t.Fatalf("edge=%s->%s violations=%d details=%v", source, target, len(first), first)
	}
	got := first[0]
	if got.Rule != rule || got.SourcePackage != source || got.SourceTier != sourceTier || got.TargetPackage != target || got.TargetTier != targetTier {
		t.Fatalf("edge=%s->%s got=%+v want_rule=%s want_source_tier=%s want_target_tier=%s", source, target, got, rule, sourceTier, targetTier)
	}
	for _, required := range []string{
		"rule=" + string(rule), "source=" + source, "source_tier=" + string(sourceTier), "target=" + target, "target_tier=" + string(targetTier),
	} {
		if !strings.Contains(got.Error(), required) {
			t.Fatalf("diagnostic %q lacks %q", got.Error(), required)
		}
	}
	if repeated := policy.Evaluate(graph).Error(); repeated != first.Error() {
		t.Fatalf("edge=%s->%s diagnostics changed: first=%q repeated=%q", source, target, first.Error(), repeated)
	}
}
