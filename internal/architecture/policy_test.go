package architecture

import (
	"strings"
	"testing"
	"time"
)

const fixtureModule = "example.test/system"

func TestPolicyAcceptsProviderOnlyFromClassifiedAdapter(t *testing.T) {
	policy := fixturePolicy(t)
	graph := fixtureGraph(
		[]string{fixtureModule + "/internal/tracker", fixtureModule + "/internal/tracker/example"},
		ImportEdge{SourcePackage: fixtureModule + "/internal/tracker/example", TargetPackage: "example.invalid/provider", Kind: ImportExternal},
		ImportEdge{SourcePackage: fixtureModule + "/internal/tracker/example", TargetPackage: fixtureModule + "/internal/tracker", Kind: ImportModuleLocal},
	)
	if err := policy.Check(graph); err != nil {
		t.Fatalf("adapter fixture rejected: %v", err)
	}
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
			t.Logf("mutant=%s killed_in=%s", test.name, time.Since(started))
		})
	}
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
