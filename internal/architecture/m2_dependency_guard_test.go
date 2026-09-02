package architecture

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const m2ModulePath = "github.com/spec-kitty/go-kitty3"

var m2SQLProviderPrefixes = []string{
	"database/sql",
	"github.com/glebarez/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/mutecomm/go-sqlcipher/v4",
	"github.com/ncruces/go-sqlite3",
	"modernc.org/sqlite",
	"zombiezen.com/go/sqlite",
}

func TestM2DependencyPolicyAcceptsOnlyBoundedProviderComposition(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph := fixtureGraph(
		[]string{
			m2ModulePath + "/internal/ids",
			m2ModulePath + "/internal/controljournal/v2",
			m2ModulePath + "/internal/controlstore/gapdb",
			m2ModulePath + "/internal/sidecar/gapdb",
			m2ModulePath + "/internal/productionruntime",
			m2ModulePath + "/internal/mission",
			m2ModulePath + "/internal/execution",
		},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: m2ModulePath + "/internal/ids", Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: m2ModulePath + "/internal/controljournal/v2", Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/sidecar/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/productionruntime", TargetPackage: m2ModulePath + "/internal/controlstore/gapdb", Kind: ImportModuleLocal},
	)
	graph.ModulePath = m2ModulePath
	if err := policy.Check(graph); err != nil {
		t.Fatalf("evidence=PACKAGE-DIRECTIONS legal M2 composition rejected: %v", err)
	}
}

func TestM2DependencyPolicyRejectsForbiddenEdges(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	tests := []struct {
		name   string
		source string
		target string
		kind   ImportKind
		rule   RuleID
	}{
		{"kernel-to-gapdb-adapter", m2ModulePath + "/internal/execution", m2ModulePath + "/internal/controlstore/gapdb", ImportModuleLocal, RuleLocalDirection},
		{"kernel-to-gapdb-provider", m2ModulePath + "/internal/mission", "github.com/spec-kitty/gapdb/gapdb", ImportExternal, RuleExternalDirection},
		{"pure-journal-to-filesystem-capability", m2ModulePath + "/internal/controljournal/v2", m2ModulePath + "/internal/gitrepo", ImportModuleLocal, RuleLocalDirection},
		{"tracker-to-mission-authority", m2ModulePath + "/internal/tracker/beads", m2ModulePath + "/internal/mission", ImportModuleLocal, RuleLocalDirection},
		{"unknown-production-package", m2ModulePath + "/internal/unclassified", m2ModulePath + "/internal/unclassified", ImportModuleLocal, RuleUnknownPackage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packages := []string{test.source}
			if test.kind == ImportModuleLocal && test.target != test.source {
				packages = append(packages, test.target)
			}
			graph := fixtureGraph(packages, ImportEdge{SourcePackage: test.source, TargetPackage: test.target, Kind: test.kind})
			graph.ModulePath = m2ModulePath
			violations := policy.Evaluate(graph)
			if len(violations) != 1 || violations[0].Rule != test.rule || violations[0].SourcePackage != test.source || violations[0].TargetPackage != test.target {
				t.Fatalf("evidence=PACKAGE-DIRECTIONS mutant=%s observed=%v expected=%s:%s->%s", test.name, violations, test.rule, test.source, test.target)
			}
		})
	}

	for _, prefix := range m2SQLProviderPrefixes {
		t.Run("sql-"+strings.NewReplacer("/", "-", ".", "-").Replace(prefix), func(t *testing.T) {
			source := m2ModulePath + "/internal/controljournal/v2"
			graph := fixtureGraph([]string{source}, ImportEdge{SourcePackage: source, TargetPackage: prefix + "/internal/driver", Kind: ImportExternal})
			graph.ModulePath = m2ModulePath
			violations := m2EvaluateClosedGraph(policy, graph)
			if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface {
				t.Fatalf("evidence=SQLITE-SQL-DENIAL provider=%s observed=%v expected=deferred-surface", prefix, violations)
			}
		})
	}
}

func TestM2PackageClassificationActivatesOnPresence(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	want := map[string]TierName{
		m2ModulePath + "/internal/controljournal/v2":          TierFoundation,
		m2ModulePath + "/internal/controljournal/v2/playback": TierFoundation,
		m2ModulePath + "/internal/acceptance":                 TierAuthority,
		m2ModulePath + "/internal/productionruntime":          TierComposition,
		m2ModulePath + "/internal/controlroot":                TierFoundation,
		m2ModulePath + "/cmd/spec-kitty":                      TierComposition,
		m2ModulePath + "/cmd/spk-m2-qualification":            TierQuality,
	}
	for path, expected := range want {
		tier, err := policy.Classify(path)
		if err != nil || tier.Name != expected {
			t.Fatalf("evidence=PACKAGE-DIRECTIONS package=%s observed=%+v error=%v expected=%s", path, tier, err, expected)
		}
	}
	if _, err := policy.Classify(m2ModulePath + "/internal/controljournal/v2x"); err == nil {
		t.Fatal("evidence=PACKAGE-DIRECTIONS slash-bounded lookalike was classified")
	}
}

func TestM2CurrentRepositoryActivatesCompleteGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	graph, err := LoadGraph(ctx, m2RepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := m2ClosedPolicy(t, graph.ModulePath)
	violations := m2EvaluateIncrementalRepository(policy, graph)
	if len(violations) != 0 {
		t.Fatalf("evidence=PACKAGE-DIRECTIONS current M2 surface violations:\n%s", violations.Error())
	}
}

func TestM2PolicyDeletionMutantsFailClosed(t *testing.T) {
	base := m2ClosedPolicy(t, m2ModulePath)
	mutants := []struct {
		name   string
		mutate func(*Policy)
	}{
		{"remove-controljournal-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/controljournal/v2") }},
		{"remove-composition-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/productionruntime") }},
		{"remove-sql-forbidden-prefix", func(policy *Policy) {
			policy.ForbiddenImportPrefixes = removeM2String(policy.ForbiddenImportPrefixes, "modernc.org/sqlite")
		}},
		{"remove-gapdb-adapter-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/controlstore/gapdb") }},
		{"remove-kernel-direction", func(policy *Policy) {
			for index := range policy.Tiers {
				if policy.Tiers[index].Name == TierAuthority {
					policy.Tiers[index].AllowedLocalTargets = append(policy.Tiers[index].AllowedLocalTargets, TierAdapter)
				}
			}
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			policy := cloneM2Policy(base)
			mutant.mutate(&policy)
			if err := validateM2PolicyShape(policy); err == nil {
				t.Fatalf("evidence=GUARD-DELETION-MUTANTS mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}
}

func m2ClosedPolicy(t *testing.T, module string) Policy {
	t.Helper()
	policy, err := DefaultPolicy(module)
	if err != nil {
		t.Fatal(err)
	}
	policy = cloneM2Policy(policy)
	add := func(tierName TierName, patterns ...PackagePattern) {
		for index := range policy.Tiers {
			if policy.Tiers[index].Name == tierName {
				policy.Tiers[index].Packages = append(policy.Tiers[index].Packages, patterns...)
				return
			}
		}
		t.Fatalf("M2 policy missing tier %s", tierName)
	}
	add(TierFoundation,
		PackagePrefix(module+"/internal/controljournal/v2"),
		PackagePrefix(module+"/internal/controlroot"),
	)
	add(TierAuthority, PackagePrefix(module+"/internal/acceptance"))
	add(TierAdapter, PackagePrefix(module+"/internal/controlstore/gapdb"), PackagePrefix(module+"/internal/sidecar/gapdb"))
	add(TierComposition, PackagePrefix(module+"/internal/productionruntime"), PackagePrefix(module+"/cmd/spec-kitty"))
	add(TierQuality, PackagePrefix(module+"/cmd/spk-m2-qualification"))
	for _, prefix := range m2SQLProviderPrefixes {
		if !slices.Contains(policy.ForbiddenImportPrefixes, prefix) {
			policy.ForbiddenImportPrefixes = append(policy.ForbiddenImportPrefixes, prefix)
		}
	}
	for index := range policy.Tiers {
		if policy.Tiers[index].Name == TierAdapter || policy.Tiers[index].Name == TierQuality {
			policy.Tiers[index].AllowedExternalPrefixes = removeM2String(policy.Tiers[index].AllowedExternalPrefixes, "modernc.org/sqlite")
		}
	}
	if err := validateM2PolicyShape(policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func validateM2PolicyShape(policy Policy) error {
	requiredClasses := map[string]TierName{
		policy.ModulePath + "/internal/controljournal/v2":  TierFoundation,
		policy.ModulePath + "/internal/controlroot":        TierFoundation,
		policy.ModulePath + "/internal/acceptance":         TierAuthority,
		policy.ModulePath + "/internal/controlstore/gapdb": TierAdapter,
		policy.ModulePath + "/internal/productionruntime":  TierComposition,
	}
	for path, expected := range requiredClasses {
		tier, err := policy.Classify(path)
		if err != nil || tier.Name != expected {
			return fmt.Errorf("evidence=PACKAGE-DIRECTIONS law=required-class package=%s observed=%+v error=%v expected=%s", path, tier, err, expected)
		}
	}
	for _, prefix := range m2SQLProviderPrefixes {
		if !slices.Contains(policy.ForbiddenImportPrefixes, prefix) {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=forbidden-prefix observed=missing expected=%s", prefix)
		}
	}
	for _, tier := range policy.Tiers {
		if tier.Name == TierAuthority && slices.Contains(tier.AllowedLocalTargets, TierAdapter) {
			return fmt.Errorf("evidence=PACKAGE-DIRECTIONS law=kernel-adapter observed=allowed expected=denied")
		}
		if (tier.Name == TierAdapter || tier.Name == TierQuality) && slices.Contains(tier.AllowedExternalPrefixes, "modernc.org/sqlite") {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=external-allowance tier=%s observed=sqlite expected=absent", tier.Name)
		}
	}
	return policy.Validate()
}

func m2EvaluateClosedGraph(policy Policy, graph Graph) Violations {
	violations := policy.Evaluate(graph)
	for _, edge := range graph.Edges {
		if edge.Kind == ImportTestOnly || !matchesPrefix(edge.TargetPackage, m2SQLProviderPrefixes) {
			continue
		}
		sourceTier, _ := policy.Classify(edge.SourcePackage)
		violation := violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, "")
		found := false
		for _, existing := range violations {
			if existing == violation {
				found = true
			}
		}
		if !found {
			violations = append(violations, violation)
		}
	}
	slices.SortFunc(violations, func(left, right Violation) int { return strings.Compare(left.Error(), right.Error()) })
	return violations
}

func m2EvaluateIncrementalRepository(policy Policy, graph Graph) Violations {
	all := m2EvaluateClosedGraph(policy, graph)
	legacySQLitePresent := false
	for _, pkg := range graph.Packages {
		if pkg.ImportPath == graph.ModulePath+"/internal/controlstore/sqlite" {
			legacySQLitePresent = true
			break
		}
	}
	filtered := make(Violations, 0, len(all))
	for _, violation := range all {
		if !legacySQLitePresent || matchesPrefix(violation.SourcePackage, m2ActivatedSourcePrefixes(graph.ModulePath)) {
			filtered = append(filtered, violation)
		}
	}
	return filtered
}

func m2ActivatedSourcePrefixes(module string) []string {
	return []string{
		module + "/internal/controljournal/v2",
		module + "/internal/acceptance",
		module + "/internal/productionruntime",
		module + "/internal/controlroot",
		module + "/cmd/spec-kitty",
		module + "/cmd/spk-m2-qualification",
	}
}

func cloneM2Policy(policy Policy) Policy {
	clone := policy
	clone.ForbiddenPackages = slices.Clone(policy.ForbiddenPackages)
	clone.ForbiddenImportPrefixes = slices.Clone(policy.ForbiddenImportPrefixes)
	clone.AllowedNeutralExternalPrefixes = slices.Clone(policy.AllowedNeutralExternalPrefixes)
	clone.Tiers = slices.Clone(policy.Tiers)
	for index := range clone.Tiers {
		clone.Tiers[index].Packages = slices.Clone(policy.Tiers[index].Packages)
		clone.Tiers[index].AllowedLocalTargets = slices.Clone(policy.Tiers[index].AllowedLocalTargets)
		clone.Tiers[index].AllowedExternalPrefixes = slices.Clone(policy.Tiers[index].AllowedExternalPrefixes)
	}
	clone.ForbiddenLocalDirections = slices.Clone(policy.ForbiddenLocalDirections)
	return clone
}

func removeM2Pattern(policy *Policy, path string) {
	for tierIndex := range policy.Tiers {
		patterns := policy.Tiers[tierIndex].Packages[:0]
		for _, pattern := range policy.Tiers[tierIndex].Packages {
			if pattern.Path != path {
				patterns = append(patterns, pattern)
			}
		}
		policy.Tiers[tierIndex].Packages = patterns
	}
}

func removeM2String(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
