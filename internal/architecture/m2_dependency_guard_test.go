package architecture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const m2ModulePath = "github.com/spec-kitty/go-kitty3"

const m2LegacyExemptionSHA256 = "dc09c98815fb900c0806b76b830d87b7895981875796e136d0c9742d13d6f400"

var m2SQLProviderPrefixes = []string{
	"database/sql",
	"github.com/glebarez/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/mutecomm/go-sqlcipher/v4",
	"github.com/ncruces/go-sqlite3",
	"modernc.org/sqlite",
	"zombiezen.com/go/sqlite",
}

var m2GapDBPrivateSegments = []string{"internal", "private", "private-server", "server", "server-private"}

var m2PureJournalStandardLibrary = []PackagePattern{
	ExactPackage("bytes"),
	ExactPackage("context"),
	ExactPackage("crypto/sha256"),
	PackagePrefix("encoding"),
	ExactPackage("errors"),
	ExactPackage("fmt"),
	ExactPackage("hash"),
	ExactPackage("sort"),
	ExactPackage("strconv"),
	ExactPackage("strings"),
	PackagePrefix("sync"),
	PackagePrefix("unicode"),
}

var m2ExactGapDBCallers = []PackagePattern{
	ExactPackage(m2ModulePath + "/internal/controlstore/gapdb"),
	ExactPackage(m2ModulePath + "/internal/sidecar/gapdb"),
	ExactPackage(m2ModulePath + "/internal/testcampaign/m2/external"),
}

var m2TrackerForbiddenTargets = []PackagePattern{
	PackagePrefix(m2ModulePath + "/internal/artifact"),
	PackagePrefix(m2ModulePath + "/internal/mission"),
	PackagePrefix(m2ModulePath + "/internal/execution"),
	PackagePrefix(m2ModulePath + "/internal/workspace"),
	PackagePrefix(m2ModulePath + "/internal/handoff"),
	PackagePrefix(m2ModulePath + "/internal/review"),
	PackagePrefix(m2ModulePath + "/internal/acceptance"),
	PackagePrefix(m2ModulePath + "/internal/integration"),
	PackagePrefix(m2ModulePath + "/internal/gitrepo"),
}

type m2LegacyExemption struct {
	Source          string
	Target          string
	Kind            ImportKind
	AdmissionTuple  string
	RemovalBoundary string
}

func m2LegacyExemptions(module string) []m2LegacyExemption {
	return []m2LegacyExemption{
		{module + "/internal/controlstore/sqlite", "database/sql", ImportStandardLibrary, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "database/sql", ImportTestOnly, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "modernc.org/sqlite", ImportTestOnly, m2TupleSHA, "WP16"},
		{module + "/internal/helpercmd", module + "/internal/controlstore/sqlite", ImportModuleLocal, m2TupleSHA, "WP15"},
		{module + "/internal/scenario", "database/sql", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", module + "/internal/controlstore/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "modernc.org/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "database/sql", ImportStandardLibrary, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP14"},
		{module + "/internal/testcampaign/m1/external", "github.com/spec-kitty/gapdb/gapdb", ImportExternal, m2TupleSHA, "WP17"},
		{module + "/internal/tracker/beads", module + "/internal/controlstore/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/artifact", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/execution", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/mission", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/workspace", ImportTestOnly, m2TupleSHA, "WP14"},
	}
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
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/sidecar/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/productionruntime", TargetPackage: m2ModulePath + "/internal/controlstore/gapdb", Kind: ImportModuleLocal},
	)
	graph.ModulePath = m2ModulePath
	if err := policy.Check(graph); err != nil {
		t.Fatalf("evidence=PACKAGE-DIRECTIONS legal M2 composition rejected: %v", err)
	}
}

func TestM2DependencyPositiveBoundaries(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	legal := fixtureGraph(
		[]string{
			m2ModulePath + "/internal/controljournal/v2",
			m2ModulePath + "/internal/controlstore/gapdb",
			m2ModulePath + "/internal/sidecar/gapdb",
			m2ModulePath + "/internal/testcampaign/m2/external",
		},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "context", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "encoding/json", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "crypto/sha256", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/sidecar/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/testcampaign/m2/external", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
	)
	legal.ModulePath = m2ModulePath
	if violations := m2EvaluateClosedGraph(policy, legal); len(violations) != 0 {
		t.Fatalf("evidence=PURE-KERNEL-IMPORTS legal provider-neutral/provider callers rejected:\n%s", violations.Error())
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
	for _, target := range []string{
		"github.com/spec-kitty/gapdb/internal/authority",
		"github.com/spec-kitty/gapdb/server/authority",
		"github.com/spec-kitty/gapdb/gapdb/internal/authority",
		"github.com/spec-kitty/gapdb/gapdb/private-server/authority",
	} {
		t.Run("gapdb-private-"+strings.NewReplacer("/", "-", ".", "-").Replace(target), func(t *testing.T) {
			source := m2ModulePath + "/internal/controlstore/gapdb"
			graph := fixtureGraph([]string{source}, ImportEdge{SourcePackage: source, TargetPackage: target, Kind: ImportExternal})
			graph.ModulePath = m2ModulePath
			violations := m2EvaluateClosedGraph(policy, graph)
			if len(violations) == 0 || violations[0].Rule != RuleDeferredSurface {
				t.Fatalf("evidence=GAPDB-PRIVATE-DENIAL provider=%s observed=%v expected=deferred-surface", target, violations)
			}
		})
	}
	for _, target := range []string{"github.com/spec-kitty/gapdb/gapdb", "github.com/spec-kitty/gapdb/gapdbapi", "github.com/spec-kitty/gapdb/gapdb/internalized", "github.com/spec-kitty/gapdb/gapdb/serverless"} {
		if m2IsGapDBPrivatePath(target) {
			t.Fatalf("evidence=GAPDB-PRIVATE-DENIAL public-or-lookalike=%s observed=private", target)
		}
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

func TestM2LaterCapabilityCannotHideBehindLegacySQLite(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph := fixtureGraph(
		[]string{m2ModulePath + "/internal/controlstore/sqlite", m2ModulePath + "/internal/workgraph"},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/sqlite", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/workgraph", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal},
	)
	graph.ModulePath = m2ModulePath
	violations := m2EvaluateIncrementalRepository(policy, graph)
	if len(violations) != 1 || violations[0].SourcePackage != m2ModulePath+"/internal/workgraph" || violations[0].TargetPackage != "modernc.org/sqlite" {
		t.Fatalf("evidence=SQLITE-SQL-DENIAL reviewer-probe observed=%v expected=workgraph SQLite violation", violations)
	}
}

func TestM2LegacyExemptionsAreExactAndProvenanceBound(t *testing.T) {
	exemptions := m2LegacyExemptions(m2ModulePath)
	if err := validateM2LegacyExemptions(exemptions); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(exemptions))
	for _, exemption := range exemptions {
		key := fmt.Sprintf("%s|%s|%s", exemption.Source, exemption.Target, exemption.Kind)
		if seen[key] || exemption.AdmissionTuple != m2TupleSHA || exemption.RemovalBoundary == "" {
			t.Fatalf("evidence=SQLITE-SQL-DENIAL invalid legacy exemption=%+v", exemption)
		}
		seen[key] = true
	}
	mutants := []struct {
		candidate m2LegacyExemption
		list      []m2LegacyExemption
	}{
		{m2LegacyExemption{m2ModulePath + "/internal/workgraph", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP16"}, exemptions},
		{m2LegacyExemption{m2ModulePath + "/internal/controlstore/sqlite", "modernc.org/sqlite/lib", ImportExternal, m2TupleSHA, "WP16"}, exemptions},
		{exemptions[0], func() []m2LegacyExemption {
			values := slices.Clone(exemptions)
			values[0].AdmissionTuple = "mutable"
			return values
		}()},
	}
	for _, mutant := range mutants {
		if m2IsLegacyExempt(mutant.candidate.Source, mutant.candidate.Target, mutant.candidate.Kind, mutant.list) {
			t.Fatalf("evidence=SQLITE-SQL-DENIAL broad legacy mutant accepted=%+v", mutant.candidate)
		}
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
	policy, err := m2ClosedPolicyValue(module)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func m2ClosedPolicyValue(module string) (Policy, error) {
	policy, err := DefaultPolicy(module)
	if err != nil {
		return Policy{}, err
	}
	policy = cloneM2Policy(policy)
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
		return Policy{}, err
	}
	return policy, nil
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
		sourceTier, _ := policy.Classify(edge.SourcePackage)
		if matchesPrefix(edge.TargetPackage, m2SQLProviderPrefixes) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		}
		if m2IsGapDBPrivatePath(edge.TargetPackage) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		} else if matchesPrefix(edge.TargetPackage, []string{"github.com/spec-kitty/gapdb"}) && !forbidden(edge.SourcePackage, m2ExactGapDBCallers) {
			violations = appendM2Violation(violations, violationForEdge(RuleExternalDirection, edge, sourceTier.Name, ""))
		}
		if edge.Kind != ImportTestOnly && matchesPrefix(edge.SourcePackage, []string{policy.ModulePath + "/internal/controljournal/v2"}) && edge.Kind == ImportStandardLibrary && !forbidden(edge.TargetPackage, m2PureJournalStandardLibrary) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		}
		if sourceTier.Name == TierAdapter && matchesPrefix(edge.SourcePackage, []string{policy.ModulePath + "/internal/tracker"}) && forbidden(edge.TargetPackage, m2TrackerForbiddenTargets) {
			targetTier, _ := policy.Classify(edge.TargetPackage)
			violations = appendM2Violation(violations, violationForEdge(RuleLocalDirection, edge, sourceTier.Name, targetTier.Name))
		}
	}
	slices.SortFunc(violations, func(left, right Violation) int { return strings.Compare(left.Error(), right.Error()) })
	return violations
}

func m2IsGapDBPrivatePath(target string) bool {
	const root = "github.com/spec-kitty/gapdb"
	if target != root && !strings.HasPrefix(target, root+"/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(target, root+"/"), "/") {
		if slices.Contains(m2GapDBPrivateSegments, segment) {
			return true
		}
	}
	return false
}

func m2EvaluateIncrementalRepository(policy Policy, graph Graph) Violations {
	exemptions := m2LegacyExemptions(graph.ModulePath)
	filtered := graph
	filtered.Edges = make([]ImportEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if m2IsLegacyExempt(edge.SourcePackage, edge.TargetPackage, edge.Kind, exemptions) {
			continue
		}
		filtered.Edges = append(filtered.Edges, edge)
	}
	return m2EvaluateClosedGraph(policy, filtered)
}

func appendM2Violation(values Violations, candidate Violation) Violations {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func m2IsLegacyExempt(source, target string, kind ImportKind, exemptions []m2LegacyExemption) bool {
	for _, exemption := range exemptions {
		if exemption.AdmissionTuple == m2TupleSHA && exemption.Source == source && exemption.Target == target && exemption.Kind == kind {
			return true
		}
	}
	return false
}

func validateM2LegacyExemptions(exemptions []m2LegacyExemption) error {
	identities := make([]string, 0, len(exemptions))
	seen := make(map[string]bool, len(exemptions))
	for _, exemption := range exemptions {
		identity := fmt.Sprintf("%s|%s|%s|%s|%s", exemption.Source, exemption.Target, exemption.Kind, exemption.AdmissionTuple, exemption.RemovalBoundary)
		if exemption.AdmissionTuple != m2TupleSHA || exemption.RemovalBoundary == "" || seen[identity] {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=legacy-exemption-provenance observed=%+v", exemption)
		}
		seen[identity] = true
		identities = append(identities, identity)
	}
	slices.Sort(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	got := hex.EncodeToString(digest[:])
	if got != m2LegacyExemptionSHA256 {
		return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=legacy-exemption-census observed-sha256=%s expected=%s", got, m2LegacyExemptionSHA256)
	}
	return nil
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
