package architecture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadGraphDistinguishesProductionAndTestImports(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/graph\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/ids/ids.go", "package ids\ntype ID string\n")
	writeGoFixture(t, root, "internal/kernel/kernel.go", "package kernel\nimport (\n _ \"fmt\"\n _ \"example.test/graph/internal/ids\"\n)\n")
	writeGoFixture(t, root, "internal/testhelper/helper.go", "package testhelper\nconst Value = 1\n")
	writeGoFixture(t, root, "internal/kernel/kernel_test.go", "package kernel_test\nimport (\n _ \"bytes\"\n _ \"example.test/graph/internal/testhelper\"\n)\n")
	setIsolatedGoPaths(t)

	graph, err := LoadGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[ImportEdge]bool{
		{SourcePackage: "example.test/graph/internal/kernel", TargetPackage: "fmt", Kind: ImportStandardLibrary}:                             true,
		{SourcePackage: "example.test/graph/internal/kernel", TargetPackage: "example.test/graph/internal/ids", Kind: ImportModuleLocal}:     true,
		{SourcePackage: "example.test/graph/internal/kernel", TargetPackage: "bytes", Kind: ImportTestOnly}:                                  true,
		{SourcePackage: "example.test/graph/internal/kernel", TargetPackage: "example.test/graph/internal/testhelper", Kind: ImportTestOnly}: true,
	}
	for edge := range want {
		if !hasEdge(graph.Edges, edge) {
			t.Errorf("missing edge %+v in %+v", edge, graph.Edges)
		}
	}
}

func TestCurrentProductionGraphPassesClosedPolicy(t *testing.T) {
	root := architectureRepositoryRoot(t)
	graph, err := LoadGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := DefaultPolicy(graph.ModulePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.Check(graph); err != nil {
		t.Fatalf("current graph rejected:\n%v", err)
	}
}

func TestLoadedUnknownRootCannotDisappearThroughGraphExemption(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/unknown\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/ids/ids.go", "package ids\ntype ID string\n")
	writeGoFixture(t, root, "internal/unreviewed/new.go", "package unreviewed\nconst Production = true\n")
	setIsolatedGoPaths(t)
	graph, err := LoadGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := DefaultPolicy(graph.ModulePath)
	if err != nil {
		t.Fatal(err)
	}
	violations := policy.Evaluate(graph)
	if len(violations) != 1 || violations[0].Rule != RuleUnknownPackage || violations[0].SourcePackage != "example.test/unknown/internal/unreviewed" {
		t.Fatalf("unknown production root did not fail exactly: %+v", violations)
	}
}

func TestRemovingRealAuthorityToContractRuleNamesRealEdge(t *testing.T) {
	root := architectureRepositoryRoot(t)
	graph, err := LoadGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := DefaultPolicy(graph.ModulePath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range policy.Tiers {
		if policy.Tiers[index].Name != TierAuthority {
			continue
		}
		allowed := policy.Tiers[index].AllowedLocalTargets[:0]
		for _, target := range policy.Tiers[index].AllowedLocalTargets {
			if target != TierProviderContract {
				allowed = append(allowed, target)
			}
		}
		policy.Tiers[index].AllowedLocalTargets = allowed
	}
	violations := policy.Evaluate(graph)
	wantSource := graph.ModulePath + "/internal/execution"
	wantTarget := graph.ModulePath + "/internal/tracker"
	if len(violations) != 1 || violations[0].Rule != RuleLocalDirection || violations[0].SourcePackage != wantSource || violations[0].TargetPackage != wantTarget {
		t.Fatalf("removed rule did not implicate the load-bearing real edge %s -> %s: %+v", wantSource, wantTarget, violations)
	}
}

func TestLoadGraphReturnsTypedAndBoundedFailures(t *testing.T) {
	runner := func(_ context.Context, _ string, arguments ...string) (commandResult, error) {
		if strings.Join(arguments, " ") == "list -m -json" {
			return commandResult{stdout: []byte(`{"Path":"example.test/bad"}`)}, nil
		}
		return commandResult{stderr: []byte("fixture failed"), overflow: true}, errors.New("exit status 1")
	}
	_, err := loadGraph(context.Background(), t.TempDir(), runner)
	var typed *LoadError
	if !errors.As(err, &typed) || typed.Kind != LoadOutputLimit || typed.Command != "go list -deps -json ./..." {
		t.Fatalf("load failure is not typed/bounded: %T %v", err, err)
	}
	if typed.Stderr != "fixture failed" {
		t.Fatalf("bounded stderr missing: %+v", typed)
	}
}

func architectureRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func writeGoFixture(t *testing.T, root, relative, contents string) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setIsolatedGoPaths(t *testing.T) {
	t.Helper()
	cache := filepath.Join(t.TempDir(), "build-cache")
	modules := filepath.Join(t.TempDir(), "module-cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(modules, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", cache)
	t.Setenv("GOMODCACHE", modules)
}

func hasEdge(edges []ImportEdge, target ImportEdge) bool {
	for _, edge := range edges {
		if edge == target {
			return true
		}
	}
	return false
}
