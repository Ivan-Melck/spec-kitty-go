package architecture

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func m2CapabilityGapDBDirection(module string) ForbiddenLocalDirection {
	return ForbiddenLocalDirection{Sources: m2CapabilitySourcePatterns(module), Targets: []PackagePattern{PackagePrefix(module + "/internal/controlstore/gapdb")}}
}

// Remove one complete rule, never a positional neighbor. All retained slices
// are copied so widening a mutant cannot alter its positive-control policy.
func m2WithoutExactDirection(policy Policy, want ForbiddenLocalDirection) (Policy, error) {
	index, count := -1, 0
	for i, v := range policy.ForbiddenLocalDirections {
		if slices.Equal(v.Sources, want.Sources) && slices.Equal(v.Targets, want.Targets) && slices.Equal(v.TargetTiers, want.TargetTiers) {
			index = i
			count++
		}
	}
	if count != 1 {
		return Policy{}, fmt.Errorf("expected one complete direction rule, found %d", count)
	}
	result := cloneM2Policy(policy)
	result.ForbiddenLocalDirections = append(result.ForbiddenLocalDirections[:index], result.ForbiddenLocalDirections[index+1:]...)
	return result, nil
}

func m2CurrentHubnotDirections(module string) []ForbiddenLocalDirection {
	return []ForbiddenLocalDirection{
		{Sources: []PackagePattern{PackagePrefix(module + "/internal/tracker/hubnot")}, TargetTiers: []TierName{TierAuthority, TierArtifactAuthority, TierMissionAuthority, TierMissionDefinition}, Targets: []PackagePattern{PackagePrefix(module + "/internal/controlstore"), PackagePrefix(module + "/internal/sidecar"), PackagePrefix(module + "/internal/gitrepo")}},
		{Sources: []PackagePattern{PackagePrefix(module + "/internal/controlstore"), PackagePrefix(module + "/internal/sidecar")}, Targets: []PackagePattern{PackagePrefix(module + "/internal/tracker/hubnot")}},
	}
}

// The 185-case historical census binds actual 94e7c854 fixture bytes. Only the
// three exact, reviewed Hubnot additions are projected out of those fixtures;
// all live policy checking continues to use m2ClosedPolicy/DefaultPolicy.
// Any other policy change remains visible to the frozen fixture digest.
func m2HistoricalPolicyValue() (Policy, error) {
	current, err := m2ClosedPolicyValue(m2ModulePath)
	if err != nil {
		return Policy{}, err
	}
	return m2HistoricalPolicyProjection(current)
}

func m2HistoricalPolicyProjection(current Policy) (Policy, error) {
	result := cloneM2Policy(current)
	want := PackagePrefix(current.ModulePath + "/internal/tracker/hubnot")
	count := 0
	for i, tier := range result.Tiers {
		for j, pattern := range tier.Packages {
			if pattern != want {
				continue
			}
			if tier.Name != TierAdapter {
				return Policy{}, fmt.Errorf("historical projection: Hubnot classification in unexpected tier")
			}
			count++
			result.Tiers[i].Packages = append(result.Tiers[i].Packages[:j], result.Tiers[i].Packages[j+1:]...)
			break
		}
	}
	if count != 1 {
		return Policy{}, fmt.Errorf("historical projection: expected one exact Hubnot classification, found %d", count)
	}
	// A duplicate in the same tier must not disappear behind the first match.
	for _, tier := range result.Tiers {
		for _, pattern := range tier.Packages {
			if pattern == want {
				return Policy{}, fmt.Errorf("historical projection: duplicate Hubnot classification")
			}
		}
	}
	for _, direction := range m2CurrentHubnotDirections(current.ModulePath) {
		var err error
		result, err = m2WithoutExactDirection(result, direction)
		if err != nil {
			return Policy{}, err
		}
	}
	return result, nil
}

func TestHistoricalPolicyFixtureMaterialMatchesBaseline(t *testing.T) {
	// Independently extracted from immutable 94e7c854 source, not row hash fields.
	expected := map[string]string{
		"CLASS":        "d252408390d9afb703f5a32ba6464c523e57aeeb01b69142ad10f94635b177e9",
		"DIRECTION":    "f5c9bbf90b4e564c9ca6289133f0105c644cc67f9876705db4749744ba3571a3",
		"GAPDB-CLASS":  "380b37ebce66f92ac63d89b6b1dc2b6b9160f93c9e8432497c0f226f06b625a8",
		"GAPDB-DENIAL": "1602e23460475de3b9e9422e06830b55ea2fab20b78ad196f54405f3c63e111d",
		"PROVIDER":     "4492a6db65828756de3e3c6ebfb36d64de41aed5ea0e401f051a2b70c6d2d9e7",
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			actual, ok := m2PolicyFixture(name)
			if !ok || m2DigestJSON(actual) != want {
				t.Fatalf("actual historical policy material drift: %s", name)
			}
		})
	}
	if err := validateM2ExecutableMutants(m2ExecutableMutants()); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyDenialMutationExactSelectionAndIsolation(t *testing.T) {
	base := m2ClosedPolicy(t, m2ModulePath)
	saved := cloneM2Policy(base)
	want := m2CapabilityGapDBDirection(m2ModulePath)
	slices.Reverse(base.ForbiddenLocalDirections)
	mutant, err := m2WithoutExactDirection(base, want)
	if err != nil {
		t.Fatal(err)
	}
	if m2HasCapabilityGapDBDenial(mutant) || !m2HasCapabilityGapDBDenial(base) {
		t.Fatal("wrong rule selected")
	}
	// Retained nested direction and tier slices must not alias the input.
	mutant.ForbiddenLocalDirections[0].Sources[0].Path = "mutant"
	mutant.Tiers[0].Packages[0].Path = "mutant"
	mutant.Tiers[0].AllowedLocalTargets = append(mutant.Tiers[0].AllowedLocalTargets, TierAdapter)
	slices.Reverse(base.ForbiddenLocalDirections)
	if !reflect.DeepEqual(base, saved) {
		t.Fatal("mutation changed original policy")
	}
	for _, name := range []string{"missing", "duplicate", "wrong-source", "wrong-target", "wrong-target-tier"} {
		t.Run(name, func(t *testing.T) {
			input := cloneM2Policy(base)
			without, err := m2WithoutExactDirection(input, want)
			if err != nil {
				t.Fatal(err)
			}
			altered := cloneM2Policy(Policy{ForbiddenLocalDirections: []ForbiddenLocalDirection{want}}).ForbiddenLocalDirections[0]
			switch name {
			case "missing":
				input = without
			case "duplicate":
				input.ForbiddenLocalDirections = append(input.ForbiddenLocalDirections, altered)
			case "wrong-source":
				altered.Sources[0] = ExactPackage(m2ModulePath + "/internal/tracker/hubnot")
				input = without
				input.ForbiddenLocalDirections = append(input.ForbiddenLocalDirections, altered)
			case "wrong-target":
				altered.Targets[0] = PackagePrefix(m2ModulePath + "/internal/controlstore/other")
				input = without
				input.ForbiddenLocalDirections = append(input.ForbiddenLocalDirections, altered)
			case "wrong-target-tier":
				altered.TargetTiers = []TierName{TierAdapter}
				input = without
				input.ForbiddenLocalDirections = append(input.ForbiddenLocalDirections, altered)
			}
			if _, err := m2WithoutExactDirection(input, want); err == nil {
				t.Fatal("non-unique or inexact target admitted")
			}
		})
	}
}

func TestHistoricalPolicyProjectionRefusesChangedAdditions(t *testing.T) {
	base := m2ClosedPolicy(t, m2ModulePath)
	for _, name := range []string{"classification-missing", "classification-duplicate", "direction-missing", "direction-duplicate", "direction-changed"} {
		t.Run(name, func(t *testing.T) {
			input := cloneM2Policy(base)
			want := PackagePrefix(m2ModulePath + "/internal/tracker/hubnot")
			switch name {
			case "classification-missing":
				removeM2Pattern(&input, want.Path)
			case "classification-duplicate":
				for i, tier := range input.Tiers {
					if tier.Name == TierAdapter {
						input.Tiers[i].Packages = append(tier.Packages, want)
					}
				}
			case "direction-missing":
				var err error
				input, err = m2WithoutExactDirection(input, m2CurrentHubnotDirections(m2ModulePath)[0])
				if err != nil {
					t.Fatal(err)
				}
			case "direction-duplicate":
				input.ForbiddenLocalDirections = append(input.ForbiddenLocalDirections, m2CurrentHubnotDirections(m2ModulePath)[0])
			case "direction-changed":
				input.ForbiddenLocalDirections[0].Targets[0].Path += "/other"
			}
			if _, err := m2HistoricalPolicyProjection(input); err == nil {
				t.Fatal("changed live additions were erased")
			}
		})
	}
	before := m2DigestJSON(base)
	projected, err := m2HistoricalPolicyProjection(base)
	if err != nil {
		t.Fatal(err)
	}
	projected.ForbiddenImportPrefixes = append(projected.ForbiddenImportPrefixes, "example.invalid/forbidden")
	if m2DigestJSON(base) != before {
		t.Fatal("projection mutated live policy")
	}
	altered := cloneM2Policy(base)
	altered.ForbiddenImportPrefixes = append(altered.ForbiddenImportPrefixes, "example.invalid/forbidden")
	result, err := m2HistoricalPolicyProjection(altered)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.ForbiddenImportPrefixes, "example.invalid/forbidden") {
		t.Fatal("projection erased unrelated changes")
	}
}

func TestLiveHubnotDependencyBoundaries(t *testing.T) {
	for _, target := range []string{"acceptance", "artifact", "execution", "gitrepo", "handoff", "integration", "mission", "review", "workspace"} {
		t.Run(target, func(t *testing.T) {
			candidate := m2DependencyCase("LIVE-HUBNOT-"+target, "TRACKER", m2ModulePath+"/internal/tracker/hubnot", m2ModulePath+"/internal/"+target, ImportModuleLocal, string(RuleLocalDirection))
			if got := observeM2Mutant(t, candidate); got != candidate.Expected {
				t.Fatalf("live Hubnot boundary: %+v want %+v", got, candidate.Expected)
			}
		})
	}
}

func TestLiveHubnotDirectionRulesRemainEffective(t *testing.T) {
	base := m2ClosedPolicy(t, m2ModulePath)
	hubnot := m2ModulePath + "/internal/tracker/hubnot"
	for _, tt := range []struct {
		name, source, target string
		rule                 int
	}{
		{"authority", hubnot, m2ModulePath + "/internal/execution", 0},
		{"store", hubnot, m2ModulePath + "/internal/controlstore/gapdb", 0},
		{"sidecar", hubnot, m2ModulePath + "/internal/sidecar/gapdb", 0},
		{"git", hubnot, m2ModulePath + "/internal/gitrepo", 0},
		{"store-reverse", m2ModulePath + "/internal/controlstore/gapdb", hubnot, 1},
		{"sidecar-reverse", m2ModulePath + "/internal/sidecar/gapdb", hubnot, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			graph := fixtureGraph([]string{tt.source, tt.target}, ImportEdge{SourcePackage: tt.source, TargetPackage: tt.target, Kind: ImportModuleLocal})
			graph.ModulePath = m2ModulePath
			if err := base.Check(graph); err == nil {
				t.Fatal("live denial absent")
			}
			mutant, err := m2WithoutExactDirection(base, m2CurrentHubnotDirections(m2ModulePath)[tt.rule])
			if err != nil {
				t.Fatal(err)
			}
			if err := mutant.Check(graph); err != nil {
				t.Fatalf("removed exact denial does not admit controlled edge: %v", err)
			}
			if !m2HasCapabilityGapDBDenial(mutant) {
				t.Fatal("Hubnot mutation removed GapDB denial")
			}
		})
	}
}
