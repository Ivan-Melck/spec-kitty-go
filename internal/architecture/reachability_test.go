package architecture

import (
	"os"
	"path/filepath"
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

func TestDefaultReachabilityRegistryPassesCurrentRepository(t *testing.T) {
	root := architectureRepositoryRoot(t)
	module := "github.com/spec-kitty/go-kitty3"
	if err := CheckReachability(root, module, DefaultReachabilityTargets(module)); err != nil {
		t.Fatalf("current production reachability rejected: %v", err)
	}
}

func TestRemovingRegisteredTargetsOnlyProductionCallerReddensFixture(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	module := "github.com/spec-kitty/go-kitty3"
	writeGoFixture(t, root, "internal/integration/service.go", "package integration\nfunc NewService() {}\n")
	callerPath := filepath.Join(root, "internal", "helpercmd", "integration.go")
	writeGoFixture(t, root, "internal/helpercmd/integration.go", "package helpercmd\nimport \"github.com/spec-kitty/go-kitty3/internal/integration\"\nfunc compose() { integration.NewService() }\n")
	targets := DefaultReachabilityTargets(module)
	if err := CheckReachability(root, module, targets); err != nil {
		t.Fatalf("registered target fixture rejected: %v", err)
	}
	if err := os.WriteFile(callerPath, []byte("package helpercmd\nfunc compose() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := CheckReachability(root, module, targets)
	if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) || !strings.Contains(err.Error(), "symbol=NewService") {
		t.Fatalf("removed sole production caller did not implicate registered target: %v", err)
	}
	t.Logf("mutant=registered-production-caller-removal killed_in=%s", time.Since(started))
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
