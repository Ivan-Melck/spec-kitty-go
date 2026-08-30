// Package architecture contains repository quality guards. It is not part of
// the runtime authority kernel.
package architecture

import (
	"fmt"
	"sort"
	"strings"
)

// TierName is the stable identity of a production-package capability tier.
type TierName string

const (
	TierFoundation        TierName = "typed-foundation"
	TierReadModel         TierName = "typed-read-model"
	TierAuthority         TierName = "authority-capability"
	TierArtifactAuthority TierName = "artifact-authority"
	TierMissionAuthority  TierName = "mission-authority"
	TierMissionDefinition TierName = "mission-definition"
	TierProviderContract  TierName = "provider-contract"
	TierAdapter           TierName = "provider-adapter"
	TierProjection        TierName = "read-only-projection"
	TierComposition       TierName = "composition-root"
	TierQuality           TierName = "quality-tooling"
)

// Validate rejects unstable or empty tier identities.
func (n TierName) Validate() error {
	if strings.TrimSpace(string(n)) == "" || strings.TrimSpace(string(n)) != string(n) {
		return fmt.Errorf("tier name must be stable and non-empty: %q", n)
	}
	return nil
}

// RuleID identifies a policy axis in machine-checkable diagnostics.
type RuleID string

const (
	RuleUnknownPackage     RuleID = "ARCH-CLASS-UNKNOWN"
	RuleAmbiguousPackage   RuleID = "ARCH-CLASS-AMBIGUOUS"
	RuleLocalDirection     RuleID = "ARCH-LOCAL-DIRECTION"
	RuleExternalDirection  RuleID = "ARCH-EXTERNAL-DIRECTION"
	RuleDeferredSurface    RuleID = "ARCH-DEFERRED-SURFACE"
	RuleReachability       RuleID = "REACH-PRODUCTION-CALLER"
	RuleReachabilityTarget RuleID = "REACH-TARGET-MISSING"
)

// Validate rejects rule identities outside the closed diagnostic vocabulary.
func (r RuleID) Validate() error {
	switch r {
	case RuleUnknownPackage, RuleAmbiguousPackage, RuleLocalDirection,
		RuleExternalDirection, RuleDeferredSurface, RuleReachability,
		RuleReachabilityTarget:
		return nil
	default:
		return fmt.Errorf("unknown architecture rule ID %q", r)
	}
}

// PackagePattern matches either one exact import path or that import path and
// its slash-bounded descendants. It never performs substring matching.
type PackagePattern struct {
	Path        string
	Descendants bool
}

// ExactPackage returns a pattern matching path only.
func ExactPackage(path string) PackagePattern { return PackagePattern{Path: path} }

// PackagePrefix returns a pattern matching path and slash-bounded descendants.
func PackagePrefix(path string) PackagePattern {
	return PackagePattern{Path: path, Descendants: true}
}

func (p PackagePattern) validate() error {
	if p.Path == "" || strings.TrimSpace(p.Path) != p.Path || strings.HasSuffix(p.Path, "/") {
		return fmt.Errorf("package pattern must be a normalized non-empty import path: %q", p.Path)
	}
	return nil
}

func (p PackagePattern) matches(path string) bool {
	return path == p.Path || (p.Descendants && strings.HasPrefix(path, p.Path+"/"))
}

// Tier is reviewable policy data for one capability class.
type Tier struct {
	Name                    TierName
	Packages                []PackagePattern
	AllowedLocalTargets     []TierName
	AllowedExternalPrefixes []string
	ProviderAdapter         bool
	Production              bool
}

// ForbiddenLocalDirection records a package-specific denial that is narrower
// than a tier edge. These denials are used only when one adapter tier contains
// implementations with different consumer-owned ports (for example, the
// control-store adapter may implement mission persistence while tracker
// adapters must remain ignorant of mission authority).
type ForbiddenLocalDirection struct {
	Sources     []PackagePattern
	TargetTiers []TierName
	Targets     []PackagePattern
}

// Policy is a closed classification and dependency-direction policy.
type Policy struct {
	ModulePath                     string
	Tiers                          []Tier
	ForbiddenLocalDirections       []ForbiddenLocalDirection
	ForbiddenPackages              []PackagePattern
	ForbiddenImportPrefixes        []string
	AllowedNeutralExternalPrefixes []string
}

// Violation is a stable, edge-oriented policy diagnostic.
type Violation struct {
	Rule          RuleID
	SourcePackage string
	SourceTier    TierName
	TargetPackage string
	TargetTier    TierName
}

// Validate checks the stable identity and exact edge fields of a violation.
func (v Violation) Validate() error {
	if err := v.Rule.Validate(); err != nil {
		return err
	}
	if v.SourcePackage == "" || v.TargetPackage == "" {
		return fmt.Errorf("architecture violation requires exact source and target packages")
	}
	return nil
}

func (v Violation) Error() string {
	return fmt.Sprintf("rule=%s source=%s source_tier=%s target=%s target_tier=%s",
		v.Rule, v.SourcePackage, v.SourceTier, v.TargetPackage, v.TargetTier)
}

// Violations is an ordered collection of policy failures.
type Violations []Violation

func (v Violations) Error() string {
	parts := make([]string, len(v))
	for index := range v {
		if err := v[index].Validate(); err != nil {
			parts[index] = fmt.Sprintf("invalid architecture violation[%d]: %v", index, err)
			continue
		}
		parts[index] = v[index].Error()
	}
	return strings.Join(parts, "\n")
}

// DefaultPolicy records the repository's capability-oriented package layout.
// Every new production package or dependency direction requires an explicit
// policy diff.
func DefaultPolicy(modulePath string) (Policy, error) {
	pkg := func(relative string) PackagePattern { return ExactPackage(modulePath + "/" + relative) }
	allRuntime := []TierName{
		TierFoundation, TierReadModel, TierAuthority, TierArtifactAuthority, TierMissionAuthority,
		TierMissionDefinition, TierProviderContract, TierAdapter, TierProjection,
		TierComposition,
	}
	policy := Policy{
		ModulePath: modulePath,
		Tiers: []Tier{
			{
				Name: TierFoundation, Production: true,
				Packages: []PackagePattern{
					pkg("internal/ids"), pkg("internal/faults"), pkg("internal/governancecontext"),
					PackagePrefix(modulePath + "/internal/controljournal"),
				},
				AllowedLocalTargets: []TierName{TierFoundation},
			},
			{
				Name: TierReadModel, Production: true,
				Packages: []PackagePattern{
					PackagePrefix(modulePath + "/internal/artifact/readmodel"),
					PackagePrefix(modulePath + "/internal/mission/readmodel"),
					PackagePrefix(modulePath + "/internal/workgraph/readmodel"),
				},
				AllowedLocalTargets: []TierName{TierFoundation, TierReadModel},
			},
			{
				Name: TierAuthority, Production: true,
				Packages: []PackagePattern{
					pkg("internal/execution"), pkg("internal/handoff"), pkg("internal/integration"),
					pkg("internal/review"), pkg("internal/workgraph"), pkg("internal/workspace"),
				},
				AllowedLocalTargets: []TierName{TierFoundation, TierAuthority, TierProviderContract},
			},
			{
				Name: TierArtifactAuthority, Production: true,
				Packages: []PackagePattern{
					pkg("internal/artifact"),
				},
				AllowedLocalTargets: []TierName{TierFoundation, TierReadModel, TierArtifactAuthority},
			},
			{
				Name: TierMissionAuthority, Production: true,
				Packages: []PackagePattern{
					pkg("internal/mission"),
				},
				AllowedLocalTargets: []TierName{
					TierFoundation, TierReadModel, TierAuthority, TierArtifactAuthority,
					TierMissionAuthority, TierProviderContract,
				},
			},
			{
				Name: TierMissionDefinition, Production: true,
				Packages: []PackagePattern{
					PackagePrefix(modulePath + "/internal/missiontype/softwaredev"),
				},
				AllowedLocalTargets: []TierName{TierFoundation, TierMissionAuthority, TierMissionDefinition},
			},
			{
				Name: TierProviderContract, Production: true,
				Packages:            []PackagePattern{pkg("internal/controlstore"), pkg("internal/tracker")},
				AllowedLocalTargets: []TierName{TierFoundation, TierAuthority, TierProviderContract},
			},
			{
				Name: TierAdapter, Production: true, ProviderAdapter: true,
				Packages: []PackagePattern{
					pkg("internal/controlstore/gapdb"), pkg("internal/controlstore/sqlite"), pkg("internal/gitrepo"), pkg("internal/tracker/fake"),
					PackagePrefix(modulePath + "/internal/tracker/beads"),
				},
				AllowedLocalTargets: []TierName{
					TierFoundation, TierReadModel, TierAuthority, TierArtifactAuthority,
					TierMissionAuthority, TierProviderContract, TierAdapter,
				},
				AllowedExternalPrefixes: []string{"github.com/spec-kitty/gapdb", "modernc.org/sqlite"},
			},
			{
				Name: TierProjection, Production: true,
				Packages: []PackagePattern{
					PackagePrefix(modulePath + "/internal/projection"),
				},
				AllowedLocalTargets: []TierName{
					TierFoundation, TierReadModel, TierProjection,
				},
			},
			{
				Name: TierComposition, Production: true,
				Packages:            []PackagePattern{pkg("internal/helpercmd"), pkg("cmd/spk-phase1-helper")},
				AllowedLocalTargets: append(append([]TierName(nil), allRuntime...), TierQuality),
			},
			{
				Name: TierQuality, Production: false,
				Packages: []PackagePattern{
					pkg("internal/architecture"), pkg("internal/scenario"), pkg("internal/tracker/conformance"), PackagePrefix(modulePath + "/internal/testcampaign"),
				},
				AllowedLocalTargets:     append(append([]TierName(nil), allRuntime...), TierQuality),
				AllowedExternalPrefixes: []string{"modernc.org/sqlite"},
			},
		},
		ForbiddenLocalDirections: []ForbiddenLocalDirection{
			{
				Sources: []PackagePattern{
					PackagePrefix(modulePath + "/internal/tracker/fake"),
					PackagePrefix(modulePath + "/internal/tracker/beads"),
				},
				TargetTiers: []TierName{TierArtifactAuthority, TierMissionAuthority},
			},
			{
				Sources: []PackagePattern{pkg("internal/workgraph")},
				Targets: []PackagePattern{pkg("internal/tracker")},
			},
		},
		ForbiddenPackages: []PackagePattern{
			PackagePrefix(modulePath + "/cmd/api"),
			PackagePrefix(modulePath + "/cmd/markdown"),
			PackagePrefix(modulePath + "/cmd/mcp"),
			PackagePrefix(modulePath + "/cmd/orchestrator-api"),
			PackagePrefix(modulePath + "/cmd/orchestratorapi"),
			PackagePrefix(modulePath + "/cmd/python"),
			PackagePrefix(modulePath + "/cmd/team-kitty"),
			PackagePrefix(modulePath + "/cmd/teamkitty"),
			PackagePrefix(modulePath + "/internal/api"),
			PackagePrefix(modulePath + "/internal/markdown"),
			PackagePrefix(modulePath + "/internal/mcp"),
			PackagePrefix(modulePath + "/internal/orchestrator-api"),
			PackagePrefix(modulePath + "/internal/orchestratorapi"),
			PackagePrefix(modulePath + "/internal/python"),
			PackagePrefix(modulePath + "/internal/team-kitty"),
			PackagePrefix(modulePath + "/internal/teamkitty"),
			PackagePrefix(modulePath + "/internal/tracker/beads/agent-identity"),
			PackagePrefix(modulePath + "/internal/tracker/beads/compaction"),
			PackagePrefix(modulePath + "/internal/tracker/beads/custom-mission-status"),
			PackagePrefix(modulePath + "/internal/tracker/beads/formulas"),
			PackagePrefix(modulePath + "/internal/tracker/beads/git-hooks"),
			PackagePrefix(modulePath + "/internal/tracker/beads/merge-slots"),
			PackagePrefix(modulePath + "/internal/tracker/beads/molecules"),
		},
		ForbiddenImportPrefixes: []string{
			"net/http",
			"github.com/gastownhall/beads",
			"github.com/yuin/goldmark",
			"github.com/russross/blackfriday",
			"github.com/gomarkdown/markdown",
			"github.com/steveyegge/beads",
		},
		AllowedNeutralExternalPrefixes: []string{"golang.org/x/sys"},
	}
	return policy, policy.Validate()
}

// Validate checks that the policy itself is closed and internally consistent.
func (p Policy) Validate() error {
	if p.ModulePath == "" || strings.TrimSpace(p.ModulePath) != p.ModulePath {
		return fmt.Errorf("module path must be stable and non-empty")
	}
	names := make(map[TierName]bool, len(p.Tiers))
	for _, tier := range p.Tiers {
		if err := tier.Name.Validate(); err != nil {
			return err
		}
		if names[tier.Name] {
			return fmt.Errorf("duplicate tier name %q", tier.Name)
		}
		names[tier.Name] = true
		if len(tier.Packages) == 0 {
			return fmt.Errorf("tier %q has no package patterns", tier.Name)
		}
		for _, pattern := range tier.Packages {
			if err := pattern.validate(); err != nil {
				return fmt.Errorf("tier %q: %w", tier.Name, err)
			}
			if !withinModule(p.ModulePath, pattern.Path) {
				return fmt.Errorf("tier %q pattern %q is outside module %q", tier.Name, pattern.Path, p.ModulePath)
			}
		}
		if len(tier.AllowedExternalPrefixes) > 0 && !tier.ProviderAdapter && tier.Production {
			return fmt.Errorf("tier %q grants external providers without provider-adapter classification", tier.Name)
		}
		for _, prefix := range tier.AllowedExternalPrefixes {
			if prefix == "" || strings.TrimSpace(prefix) != prefix || strings.HasSuffix(prefix, "/") {
				return fmt.Errorf("tier %q has invalid external prefix %q", tier.Name, prefix)
			}
		}
	}
	for _, direction := range p.ForbiddenLocalDirections {
		if len(direction.Sources) == 0 || (len(direction.TargetTiers) == 0 && len(direction.Targets) == 0) {
			return fmt.Errorf("forbidden local direction requires sources and targets")
		}
		for _, source := range direction.Sources {
			if err := source.validate(); err != nil {
				return fmt.Errorf("forbidden local direction source: %w", err)
			}
			if !withinModule(p.ModulePath, source.Path) {
				return fmt.Errorf("forbidden local direction source %q is outside module %q", source.Path, p.ModulePath)
			}
		}
		for _, target := range direction.TargetTiers {
			if !names[target] {
				return fmt.Errorf("forbidden local direction names unknown target tier %q", target)
			}
		}
		for _, target := range direction.Targets {
			if err := target.validate(); err != nil {
				return fmt.Errorf("forbidden local direction target: %w", err)
			}
			if !withinModule(p.ModulePath, target.Path) {
				return fmt.Errorf("forbidden local direction target %q is outside module %q", target.Path, p.ModulePath)
			}
		}
	}
	for _, tier := range p.Tiers {
		for _, target := range tier.AllowedLocalTargets {
			if !names[target] {
				return fmt.Errorf("tier %q allows unknown local tier %q", tier.Name, target)
			}
		}
	}
	for _, pattern := range p.ForbiddenPackages {
		if err := pattern.validate(); err != nil {
			return fmt.Errorf("forbidden package: %w", err)
		}
	}
	for _, prefix := range p.AllowedNeutralExternalPrefixes {
		if prefix == "" || strings.TrimSpace(prefix) != prefix || strings.HasSuffix(prefix, "/") {
			return fmt.Errorf("invalid provider-neutral external prefix %q", prefix)
		}
	}
	return nil
}

// Classify returns exactly one tier for a package or a stable classification
// violation. The violation's source and target both name the implicated package.
func (p Policy) Classify(packagePath string) (Tier, error) {
	matches := make([]Tier, 0, 2)
	for _, tier := range p.Tiers {
		for _, pattern := range tier.Packages {
			if pattern.matches(packagePath) {
				matches = append(matches, tier)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	rule := RuleUnknownPackage
	if len(matches) > 1 {
		rule = RuleAmbiguousPackage
	}
	return Tier{}, Violation{Rule: rule, SourcePackage: packagePath, TargetPackage: packagePath}
}

// Evaluate checks every production package and direct production import edge.
// Test-only edges are retained by Graph for inspection but do not grant runtime
// dependency permission.
func (p Policy) Evaluate(graph Graph) Violations {
	if err := p.Validate(); err != nil {
		return Violations{{Rule: RuleUnknownPackage, SourcePackage: p.ModulePath, TargetPackage: err.Error()}}
	}
	violations := make(Violations, 0)
	classes := make(map[string]Tier, len(graph.Packages))
	for _, productionPackage := range graph.Packages {
		if forbidden(productionPackage.ImportPath, p.ForbiddenPackages) {
			violations = append(violations, Violation{Rule: RuleDeferredSurface, SourcePackage: productionPackage.ImportPath, TargetPackage: productionPackage.ImportPath})
			continue
		}
		tier, err := p.Classify(productionPackage.ImportPath)
		if err != nil {
			violations = append(violations, err.(Violation))
			continue
		}
		classes[productionPackage.ImportPath] = tier
	}
	for _, edge := range graph.Edges {
		if edge.Kind == ImportTestOnly {
			continue
		}
		source, sourceOK := classes[edge.SourcePackage]
		if !sourceOK {
			continue
		}
		if matchesPrefix(edge.TargetPackage, p.ForbiddenImportPrefixes) {
			violations = append(violations, violationForEdge(RuleDeferredSurface, edge, source.Name, ""))
			continue
		}
		switch edge.Kind {
		case ImportStandardLibrary:
			continue
		case ImportExternal:
			if !matchesPrefix(edge.TargetPackage, p.AllowedNeutralExternalPrefixes) && !matchesPrefix(edge.TargetPackage, source.AllowedExternalPrefixes) {
				violations = append(violations, violationForEdge(RuleExternalDirection, edge, source.Name, ""))
			}
		case ImportModuleLocal:
			target, targetOK := classes[edge.TargetPackage]
			if !targetOK {
				continue
			}
			if p.forbidsLocalDirection(edge.SourcePackage, edge.TargetPackage, target.Name) || !containsTier(source.AllowedLocalTargets, target.Name) {
				violations = append(violations, violationForEdge(RuleLocalDirection, edge, source.Name, target.Name))
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		left, right := violations[i], violations[j]
		if left.Rule != right.Rule {
			return left.Rule < right.Rule
		}
		if left.SourcePackage != right.SourcePackage {
			return left.SourcePackage < right.SourcePackage
		}
		return left.TargetPackage < right.TargetPackage
	})
	return violations
}

func (p Policy) forbidsLocalDirection(sourcePackage, targetPackage string, targetTier TierName) bool {
	for _, direction := range p.ForbiddenLocalDirections {
		if !containsTier(direction.TargetTiers, targetTier) && !forbidden(targetPackage, direction.Targets) {
			continue
		}
		for _, source := range direction.Sources {
			if source.matches(sourcePackage) {
				return true
			}
		}
	}
	return false
}

// Check returns all deterministic policy violations as one error.
func (p Policy) Check(graph Graph) error {
	violations := p.Evaluate(graph)
	if len(violations) == 0 {
		return nil
	}
	return violations
}

func violationForEdge(rule RuleID, edge ImportEdge, source, target TierName) Violation {
	return Violation{Rule: rule, SourcePackage: edge.SourcePackage, SourceTier: source, TargetPackage: edge.TargetPackage, TargetTier: target}
}

func containsTier(values []TierName, target TierName) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func withinModule(modulePath, path string) bool {
	return path == modulePath || strings.HasPrefix(path, modulePath+"/")
}

func matchesPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func forbidden(path string, patterns []PackagePattern) bool {
	for _, pattern := range patterns {
		if pattern.matches(path) {
			return true
		}
	}
	return false
}
