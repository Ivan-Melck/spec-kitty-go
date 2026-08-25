package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReachabilityRejectsGuardCalledOnlyByTest(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
	writeGoFixture(t, root, "internal/guard/guard_test.go", "package guard\nimport \"testing\"\nfunc TestShadow(t *testing.T) { EnforceAuthority() }\n")
	target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}
	err := CheckReachability(root, module, []ReachabilityTarget{target})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) || !strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
		t.Fatalf("test-only guard caller was not rejected exactly: %v", err)
	}
	t.Logf("mutant=test-only-shadow killed_in=%s", time.Since(started))
}

func TestReachabilityRejectsBuildExcludedCallers(t *testing.T) {
	module := "example.test/reachability"
	excludedGOOS := "windows"
	if runtime.GOOS == excludedGOOS {
		excludedGOOS = "linux"
	}
	tests := []struct {
		name       string
		callerPath string
		buildLine  string
	}{
		{name: "build-ignore", callerPath: "internal/app/caller.go", buildLine: "//go:build ignore\n\n"},
		{name: "platform-excluded", callerPath: "internal/app/caller_" + excludedGOOS + ".go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			root := t.TempDir()
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
			writeGoFixture(t, root, test.callerPath, test.buildLine+
				"package app\nimport guardpkg \"example.test/reachability/internal/guard\"\nfunc useGuard() { guardpkg.EnforceAuthority() }\n")
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerExternalPackage}
			err := CheckReachability(root, module, []ReachabilityTarget{target})
			if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
				!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
				t.Fatalf("build-excluded caller satisfied reachability: %v", err)
			}
			t.Logf("mutant=%s-caller killed_in=%s", test.name, time.Since(started))
		})
	}
}

func TestReachabilityRejectsTargetSelfRecursion(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() { EnforceAuthority() }\n")
	target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerSamePackageProduction}
	err := CheckReachability(root, module, []ReachabilityTarget{target})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
		!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
		t.Fatalf("self-recursive target satisfied reachability: %v", err)
	}
	t.Logf("mutant=self-recursive-target killed_in=%s", time.Since(started))
}

func TestReachabilityCallerPolicies(t *testing.T) {
	module := "example.test/reachability"
	tests := []struct {
		name   string
		class  CallerClass
		caller string
	}{
		{name: "same-package", class: CallerSamePackageProduction, caller: "internal/guard/caller.go"},
		{name: "external-package", class: CallerExternalPackage, caller: "internal/app/caller.go"},
		{name: "either-production", class: CallerEitherProduction, caller: "internal/app/caller.go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
			contents := "package guard\nfunc useGuard() { EnforceAuthority() }\n"
			if strings.Contains(test.caller, "/app/") {
				contents = "package app\nimport guardpkg \"example.test/reachability/internal/guard\"\nfunc useGuard() { guardpkg.EnforceAuthority() }\n"
			}
			writeGoFixture(t, root, test.caller, contents)
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: test.class}
			if err := CheckReachability(root, module, []ReachabilityTarget{target}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReachabilityRejectsShadowedTargetCalls(t *testing.T) {
	module := "example.test/reachability"
	tests := []struct {
		name       string
		callerPath string
		caller     string
	}{
		{
			name:       "external-package-shadow",
			callerPath: "internal/app/caller.go",
			caller: "package app\n" +
				"import guardpkg \"example.test/reachability/internal/guard\"\n" +
				"var _ = guardpkg.Marker\n" +
				"type shadow struct{}\n" +
				"func (shadow) EnforceAuthority() {}\n" +
				"func useGuard() { guardpkg := shadow{}; guardpkg.EnforceAuthority() }\n",
		},
		{
			name:       "same-package-shadow",
			callerPath: "internal/guard/caller.go",
			caller: "package guard\n" +
				"func useGuard() { EnforceAuthority := func() {}; EnforceAuthority() }\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nconst Marker = true\nfunc EnforceAuthority() {}\n")
			writeGoFixture(t, root, test.callerPath, test.caller)
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}
			err := CheckReachability(root, module, []ReachabilityTarget{target})
			if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
				t.Fatalf("shadowed target call satisfied reachability: %v", err)
			}
		})
	}
}

func TestDefaultReachabilityRegistryPassesCurrentRepository(t *testing.T) {
	root := architectureRepositoryRoot(t)
	module := "github.com/spec-kitty/go-kitty3"
	want := map[string]bool{
		module + "/internal/handoff.VerifyCandidateAuthority": false,
		module + "/internal/gitrepo.NewHandoffVerifier":       false,
	}
	for _, target := range DefaultReachabilityTargets(module) {
		key := target.Package + "." + target.Symbol
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for target, found := range want {
		if !found {
			t.Errorf("mandatory reachability target missing: %s", target)
		}
	}
	if err := CheckReachability(root, module, DefaultReachabilityTargets(module)); err != nil {
		t.Fatalf("current production reachability rejected: %v", err)
	}
}

func TestRemovingEachRegisteredTargetsOnlyProductionCallerReddensFixture(t *testing.T) {
	module := "github.com/spec-kitty/go-kitty3"
	fixtures := []struct {
		name        string
		declaration string
		callerPath  string
		caller      string
		target      ReachabilityTarget
	}{
		{
			name: "authority-guard", declaration: "package handoff\nfunc VerifyCandidateAuthority() {}\n",
			callerPath: "internal/gitrepo/handoff.go", caller: "package gitrepo\nimport \"github.com/spec-kitty/go-kitty3/internal/handoff\"\nfunc verify() { handoff.VerifyCandidateAuthority() }\n",
			target: ReachabilityTarget{Package: module + "/internal/handoff", Symbol: "VerifyCandidateAuthority", RequiredCallerClass: CallerExternalPackage},
		},
		{
			name: "git-adapter", declaration: "package gitrepo\nfunc NewHandoffVerifier() {}\n",
			callerPath: "internal/helpercmd/review.go", caller: "package helpercmd\nimport \"github.com/spec-kitty/go-kitty3/internal/gitrepo\"\nfunc compose() { gitrepo.NewHandoffVerifier() }\n",
			target: ReachabilityTarget{Package: module + "/internal/gitrepo", Symbol: "NewHandoffVerifier", RequiredCallerClass: CallerExternalPackage},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			started := time.Now()
			root := t.TempDir()
			declarationPath := strings.TrimPrefix(strings.TrimSuffix(fixture.target.Package, "/")+"/target.go", module+"/")
			writeGoFixture(t, root, declarationPath, fixture.declaration)
			callerPath := filepath.Join(root, filepath.FromSlash(fixture.callerPath))
			writeGoFixture(t, root, fixture.callerPath, fixture.caller)
			if err := CheckReachability(root, module, []ReachabilityTarget{fixture.target}); err != nil {
				t.Fatalf("registered target fixture rejected: %v", err)
			}
			if err := os.WriteFile(callerPath, []byte(strings.Split(fixture.caller, "func")[0]), 0o600); err != nil {
				t.Fatal(err)
			}
			err := CheckReachability(root, module, []ReachabilityTarget{fixture.target})
			if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) || !strings.Contains(err.Error(), "symbol="+fixture.target.Symbol) {
				t.Fatalf("removed sole production caller did not implicate registered target: %v", err)
			}
			t.Logf("mutant=%s-production-caller-removal killed_in=%s", fixture.name, time.Since(started))
		})
	}
}

func TestReachabilityRejectsMissingRegisteredTarget(t *testing.T) {
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc DifferentGuard() {}\n")
	err := CheckReachability(root, module, []ReachabilityTarget{{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachabilityTarget)) {
		t.Fatalf("missing target accepted: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsProductionPythonButIgnoresTests(t *testing.T) {
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nimport cmd \"os/exec\"\nfunc run() { _ = cmd.CommandContext(nil, \"/usr/bin/python3.12\") }\n")
	writeGoFixture(t, root, "internal/guard/guard_test.go", "package guard\nimport \"os/exec\"\nfunc shadow() { _ = exec.Command(\"python\") }\n")
	err := CheckDeferredRuntimeSurfaces(root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) || !strings.Contains(err.Error(), module+"/internal/guard") || !strings.Contains(err.Error(), "runtime:/usr/bin/python3.12") {
		t.Fatalf("production Python runtime was not rejected exactly: %v", err)
	}
	if removeErr := os.WriteFile(filepath.Join(root, "internal", "guard", "guard.go"), []byte("package guard\n"), 0o600); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err := CheckDeferredRuntimeSurfaces(root, module); err != nil {
		t.Fatalf("test-only Python runtime must not count: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsConstantAliasesAndDotImport(t *testing.T) {
	module := "example.test/reachability"
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "constant-alias",
			source: "package guard\nimport cmd \"os/exec\"\nconst interpreter = pythonRuntime\nconst pythonRuntime = \"/usr/bin/\" + \"python3\"\nfunc run() { _ = cmd.Command(interpreter) }\n",
			want:   "runtime:/usr/bin/python3",
		},
		{
			name:   "dot-import",
			source: "package guard\nimport . \"os/exec\"\nfunc run() { _ = Command(\"pypy3\") }\n",
			want:   "runtime:pypy3",
		},
		{
			name:   "variable-alias",
			source: "package guard\nimport \"os/exec\"\nvar interpreter = pythonRuntime\nvar pythonRuntime = \"python3.12\"\nfunc run() { _ = exec.Command(interpreter) }\n",
			want:   "runtime:python3.12",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "internal/guard/guard.go", test.source)
			err := CheckDeferredRuntimeSurfaces(root, module)
			if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Python runtime mutant survived: %v", err)
			}
		})
	}
}

func TestDeferredRuntimeSurfaceRejectsPackageAliasDeclaredInAnotherFile(t *testing.T) {
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/runtime.go", "package guard\nconst runtimeName = \"python3\"\n")
	writeGoFixture(t, root, "internal/guard/runner.go", "package guard\nimport \"os/exec\"\nfunc run() { _ = exec.Command(runtimeName) }\n")

	err := CheckDeferredRuntimeSurfaces(root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) ||
		!strings.Contains(err.Error(), module+"/internal/guard") ||
		!strings.Contains(err.Error(), "runtime:python3") {
		t.Fatalf("cross-file Python runtime alias survived: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsLocalShortDeclarationAlias(t *testing.T) {
	root := t.TempDir()
	module := "example.test/reachability"
	writeGoFixture(t, root, "internal/guard/runner.go", "package guard\nimport \"os/exec\"\nfunc run() { interpreter := \"python3\"; _ = exec.Command(interpreter) }\n")

	err := CheckDeferredRuntimeSurfaces(root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) ||
		!strings.Contains(err.Error(), module+"/internal/guard") ||
		!strings.Contains(err.Error(), "runtime:python3") {
		t.Fatalf("local Python runtime alias survived: %v", err)
	}
}
