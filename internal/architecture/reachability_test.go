package architecture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTypedProductionSourceLoaderCancelsStalledGoList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	cancelled := make(chan struct{})
	runner := func(ctx context.Context, _ string, _ ...string) (commandResult, error) {
		<-ctx.Done()
		close(cancelled)
		return commandResult{}, ctx.Err()
	}

	_, err := loadTypedProductionSources(ctx, t.TempDir(), "example.test/stalled", runner)
	var typed *LoadError
	if !errors.As(err, &typed) || typed.Kind != LoadCommandFailed || typed.Command != "go list -e -deps -json ./..." {
		t.Fatalf("stalled production source load did not return a typed command failure: %T %v", err, err)
	}
	if !errors.Is(typed.Cause, context.DeadlineExceeded) {
		t.Fatalf("stalled production source load lost cancellation cause: %+v", typed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stalling Go command runner was not cancelled")
	}
}

func TestTypedProductionSourceLoadFailuresAreTyped(t *testing.T) {
	const module = "example.test/load-failure"
	tests := []struct {
		name      string
		source    string
		operation string
	}{
		{
			name:      "parse",
			source:    "package guard\nfunc Broken( {\n",
			operation: "parse production source",
		},
		{
			name:      "type-check",
			source:    "package guard\nfunc broken() { var number int = \"not-an-int\"; _ = number }\n",
			operation: "type-check production package " + module + "/internal/guard",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", test.source)

			err := CheckReachability(context.Background(), root, module, nil)
			var typed *LoadError
			if !errors.As(err, &typed) || typed.Kind != LoadInvalidSource {
				t.Fatalf("source load failure is not typed: %T %v", err, err)
			}
			if !strings.Contains(typed.Command, test.operation) {
				t.Fatalf("source load failure lacks stable operation context %q: %+v", test.operation, typed)
			}
		})
	}
}

func TestReachabilityRejectsGuardCalledOnlyByTest(t *testing.T) {
	started := time.Now()
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
	writeGoFixture(t, root, "internal/guard/guard_test.go", "package guard\nimport \"testing\"\nfunc TestShadow(t *testing.T) { EnforceAuthority() }\n")
	target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}
	err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
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
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
			writeGoFixture(t, root, test.callerPath, test.buildLine+
				"package app\nimport guardpkg \"example.test/reachability/internal/guard\"\nfunc useGuard() { guardpkg.EnforceAuthority() }\n")
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerExternalPackage}
			err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
			if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
				!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
				t.Fatalf("build-excluded caller satisfied reachability: %v", err)
			}
			t.Logf("mutant=%s-caller killed_in=%s", test.name, time.Since(started))
		})
	}
}

func TestReachabilityHonorsActiveCustomBuildTags(t *testing.T) {
	module := "example.test/reachability"
	tests := []struct {
		name      string
		buildLine string
		wantError bool
	}{
		{name: "active-caller", buildLine: "//go:build reviewactive\n\n"},
		{name: "inactive-caller", buildLine: "//go:build !reviewactive\n\n", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
			writeGoFixture(t, root, "internal/app/caller.go", test.buildLine+
				"package app\nimport guardpkg \"example.test/reachability/internal/guard\"\nfunc useGuard() { guardpkg.EnforceAuthority() }\n")
			t.Setenv("GOFLAGS", "-tags=reviewactive")
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerExternalPackage}
			err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
			if !test.wantError && err != nil {
				t.Fatalf("active custom-tag caller was excluded: %v", err)
			}
			if test.wantError && (err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
				!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority")) {
				t.Fatalf("inactive custom-tag caller satisfied reachability: %v", err)
			}
			t.Logf("mutant=custom-tag-%s checked_in=%s", test.name, time.Since(started))
		})
	}
}

func TestReachabilityRejectsTargetSelfRecursion(t *testing.T) {
	started := time.Now()
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() { EnforceAuthority() }\n")
	target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerSamePackageProduction}
	err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
		!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
		t.Fatalf("self-recursive target satisfied reachability: %v", err)
	}
	t.Logf("mutant=self-recursive-target killed_in=%s", time.Since(started))
}

func TestReachabilityRejectsUnrootedMutualRecursion(t *testing.T) {
	started := time.Now()
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() { helper() }\nfunc helper() { EnforceAuthority() }\n")
	target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerSamePackageProduction}
	err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) ||
		!strings.Contains(err.Error(), target.Package) || !strings.Contains(err.Error(), "symbol=EnforceAuthority") {
		t.Fatalf("unrooted mutual-recursion cycle satisfied reachability: %v", err)
	}
	t.Logf("mutant=unrooted-mutual-recursion killed_in=%s", time.Since(started))
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
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc EnforceAuthority() {}\n")
			contents := "package guard\nfunc useGuard() { EnforceAuthority() }\n"
			if strings.Contains(test.caller, "/app/") {
				contents = "package app\nimport guardpkg \"example.test/reachability/internal/guard\"\nfunc useGuard() { guardpkg.EnforceAuthority() }\n"
			}
			writeGoFixture(t, root, test.caller, contents)
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: test.class}
			if err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target}); err != nil {
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
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nconst Marker = true\nfunc EnforceAuthority() {}\n")
			writeGoFixture(t, root, test.callerPath, test.caller)
			target := ReachabilityTarget{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}
			err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{target})
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
	if err := CheckReachability(context.Background(), root, module, DefaultReachabilityTargets(module)); err != nil {
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
			root := newReachabilityFixture(t, module)
			declarationPath := strings.TrimPrefix(strings.TrimSuffix(fixture.target.Package, "/")+"/target.go", module+"/")
			writeGoFixture(t, root, declarationPath, fixture.declaration)
			callerPath := filepath.Join(root, filepath.FromSlash(fixture.callerPath))
			writeGoFixture(t, root, fixture.callerPath, fixture.caller)
			if err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{fixture.target}); err != nil {
				t.Fatalf("registered target fixture rejected: %v", err)
			}
			if err := os.WriteFile(callerPath, []byte(strings.Split(fixture.caller, "\n")[0]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{fixture.target})
			if err == nil || !strings.Contains(err.Error(), string(RuleReachability)) || !strings.Contains(err.Error(), "symbol="+fixture.target.Symbol) {
				t.Fatalf("removed sole production caller did not implicate registered target: %v", err)
			}
			t.Logf("mutant=%s-production-caller-removal killed_in=%s", fixture.name, time.Since(started))
		})
	}
}

func TestReachabilityRejectsMissingRegisteredTarget(t *testing.T) {
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nfunc DifferentGuard() {}\n")
	err := CheckReachability(context.Background(), root, module, []ReachabilityTarget{{Package: module + "/internal/guard", Symbol: "EnforceAuthority", RequiredCallerClass: CallerEitherProduction}})
	if err == nil || !strings.Contains(err.Error(), string(RuleReachabilityTarget)) {
		t.Fatalf("missing target accepted: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsProductionPythonButIgnoresTests(t *testing.T) {
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/guard.go", "package guard\nimport cmd \"os/exec\"\nfunc run() { _ = cmd.CommandContext(nil, \"/usr/bin/python3.12\") }\n")
	writeGoFixture(t, root, "internal/guard/guard_test.go", "package guard\nimport \"os/exec\"\nfunc shadow() { _ = exec.Command(\"python\") }\n")
	err := CheckDeferredRuntimeSurfaces(context.Background(), root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) || !strings.Contains(err.Error(), module+"/internal/guard") || !strings.Contains(err.Error(), "runtime:/usr/bin/python3.12") {
		t.Fatalf("production Python runtime was not rejected exactly: %v", err)
	}
	if removeErr := os.WriteFile(filepath.Join(root, "internal", "guard", "guard.go"), []byte("package guard\n"), 0o600); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err := CheckDeferredRuntimeSurfaces(context.Background(), root, module); err != nil {
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
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/guard/guard.go", test.source)
			err := CheckDeferredRuntimeSurfaces(context.Background(), root, module)
			if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Python runtime mutant survived: %v", err)
			}
		})
	}
}

func TestDeferredRuntimeSurfaceRejectsPackageAliasDeclaredInAnotherFile(t *testing.T) {
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/runtime.go", "package guard\nconst runtimeName = \"python3\"\n")
	writeGoFixture(t, root, "internal/guard/runner.go", "package guard\nimport \"os/exec\"\nfunc run() { _ = exec.Command(runtimeName) }\n")

	err := CheckDeferredRuntimeSurfaces(context.Background(), root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) ||
		!strings.Contains(err.Error(), module+"/internal/guard") ||
		!strings.Contains(err.Error(), "runtime:python3") {
		t.Fatalf("cross-file Python runtime alias survived: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsLocalShortDeclarationAlias(t *testing.T) {
	module := "example.test/reachability"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/guard/runner.go", "package guard\nimport \"os/exec\"\nfunc run() { interpreter := \"python3\"; _ = exec.Command(interpreter) }\n")

	err := CheckDeferredRuntimeSurfaces(context.Background(), root, module)
	if err == nil || !strings.Contains(err.Error(), string(RuleDeferredSurface)) ||
		!strings.Contains(err.Error(), module+"/internal/guard") ||
		!strings.Contains(err.Error(), "runtime:python3") {
		t.Fatalf("local Python runtime alias survived: %v", err)
	}
}

func TestDeferredRuntimeSurfaceRejectsMarkdownAuthorityParsing(t *testing.T) {
	const module = "example.test/markdown-authority"
	tests := []struct {
		name       string
		files      map[string]string
		wantTarget string
	}{
		{
			name: "direct-standard-library-parser",
			files: map[string]string{
				"internal/helpercmd/authority.go":      "package helpercmd\nimport (\n\t\"os\"\n\t\"strings\"\n)\nfunc loadAuthority() { content, _ := os.ReadFile(\"doctrine.md\"); _ = strings.Split(string(content), \"# \" ) }\n",
				"internal/helpercmd/authority_test.go": "package helpercmd\nimport (\n\t\"os\"\n\t\"strings\"\n)\nfunc testOnlyParser() { content, _ := os.ReadFile(\"ignored.md\"); _ = strings.Fields(string(content)) }\n",
			},
			wantTarget: "markdown:doctrine.md",
		},
		{
			name: "cross-file-function-and-path-aliases",
			files: map[string]string{
				"internal/helpercmd/authority_path.go": "package helpercmd\nimport (\n\tsystem \"os\"\n\tpaths \"path/filepath\"\n)\nvar authorityPath = paths.Join(\"rules\", \"governance.md\")\nvar readAuthority = system.ReadFile\n",
				"internal/helpercmd/authority.go":      "package helpercmd\nimport text \"strings\"\nfunc loadAuthority() { content, _ := readAuthority(authorityPath); _ = text.Split(string(content), \"##\") }\n",
			},
			wantTarget: "markdown:rules/governance.md",
		},
		{
			name: "filesystem-function",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport (\n\t\"io/fs\"\n\t\"os\"\n\t\"strings\"\n)\nfunc loadAuthority() { content, _ := fs.ReadFile(os.DirFS(\".\"), \"policy.MD\"); _ = strings.Fields(string(content)) }\n",
			},
			wantTarget: "markdown:policy.MD",
		},
		{
			name: "open-and-scan",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport (\n\t\"bufio\"\n\t\"os\"\n)\nfunc loadAuthority() { file, _ := os.Open(\"decisions.md\"); _ = bufio.NewScanner(file) }\n",
			},
			wantTarget: "markdown:decisions.md",
		},
		{
			name: "standard-library-template-parser",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport \"text/template\"\nfunc loadAuthority() { _, _ = template.ParseFiles(\"authority.md\") }\n",
			},
			wantTarget: "markdown:authority.md",
		},
		{
			name: "reader-wrapper-with-known-argument",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport (\n\t\"os\"\n\t\"strings\"\n)\nfunc readAuthority(path string) []byte { content, _ := os.ReadFile(path); return content }\nfunc loadAuthority() { _ = strings.Fields(string(readAuthority(\"doctrine.md\"))) }\n",
			},
			wantTarget: "markdown:doctrine.md",
		},
		{
			name: "constant-returning-path-helper",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport (\n\t\"os\"\n\t\"strings\"\n)\nfunc authorityPath() string { return \"doctrine.md\" }\nfunc loadAuthority() { content, _ := os.ReadFile(authorityPath()); _ = strings.Split(string(content), \"# \") }\n",
			},
			wantTarget: "markdown:doctrine.md",
		},
		{
			name: "uniquely-reaching-ordinary-assignment",
			files: map[string]string{
				"internal/helpercmd/authority.go": "package helpercmd\nimport (\n\t\"os\"\n\t\"strings\"\n)\nfunc loadAuthority() { var path string; path = \"doctrine.md\"; content, _ := os.ReadFile(path); _ = strings.Fields(string(content)) }\n",
			},
			wantTarget: "markdown:doctrine.md",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			root := newReachabilityFixture(t, module)
			for path, contents := range test.files {
				writeGoFixture(t, root, path, contents)
			}
			graph, err := LoadGraph(context.Background(), root)
			if err != nil {
				t.Fatalf("load classified Markdown mutant: %v", err)
			}
			policy, err := DefaultPolicy(module)
			if err != nil {
				t.Fatal(err)
			}
			if err := policy.Check(graph); err != nil {
				t.Fatalf("mutant must reach the deferred-surface guard from an already classified package: %v", err)
			}

			err = CheckDeferredRuntimeSurfaces(context.Background(), root, module)
			violations, typed := err.(Violations)
			want := Violation{
				Rule: RuleDeferredSurface, SourcePackage: module + "/internal/helpercmd",
				TargetPackage: test.wantTarget,
			}
			if !typed || len(violations) != 1 || violations[0] != want {
				t.Fatalf("Markdown authority parser mutant survived: %v", err)
			}
			if strings.Contains(err.Error(), "ignored.md") {
				t.Fatalf("test-only Markdown parser entered production diagnostics: %v", err)
			}
			t.Logf("mutant=markdown-authority-%s killed_in=%s", test.name, time.Since(started))
		})
	}
}

func TestDeferredRuntimeSurfaceDoesNotGuessAmbiguousMarkdownFlows(t *testing.T) {
	const module = "example.test/markdown-ambiguous"
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "dynamic-reader-wrapper-argument",
			source: "package helpercmd\nimport \"os\"\nfunc readAuthority(path string) []byte { content, _ := os.ReadFile(path); return content }\nfunc loadAuthority(path string) { _ = readAuthority(path) }\n",
		},
		{
			name:   "multiple-return-path-helper",
			source: "package helpercmd\nimport \"os\"\nfunc authorityPath(markdown bool) string { if markdown { return \"doctrine.md\" }; return \"policy.json\" }\nfunc loadAuthority(markdown bool) { _, _ = os.ReadFile(authorityPath(markdown)) }\n",
		},
		{
			name:   "multiple-ordinary-assignments",
			source: "package helpercmd\nimport \"os\"\nfunc loadAuthority(markdown bool) { var path string; if markdown { path = \"doctrine.md\" } else { path = \"policy.json\" }; _, _ = os.ReadFile(path) }\n",
		},
		{
			name:   "use-before-sole-assignment",
			source: "package helpercmd\nimport \"os\"\nfunc loadAuthority() { var path string; _, _ = os.ReadFile(path); path = \"doctrine.md\"; _ = path }\n",
		},
		{
			name:   "short-declaration-reassigned-before-use",
			source: "package helpercmd\nimport \"os\"\nfunc loadAuthority() { path := \"doctrine.md\"; path = \"policy.json\"; _, _ = os.ReadFile(path) }\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newReachabilityFixture(t, module)
			writeGoFixture(t, root, "internal/helpercmd/authority.go", test.source)
			if err := CheckDeferredRuntimeSurfaces(context.Background(), root, module); err != nil {
				t.Fatalf("ambiguous Markdown flow must remain unresolved: %v", err)
			}
		})
	}
}

func TestDeferredRuntimeSurfaceDoesNotGrepMarkdownProse(t *testing.T) {
	const module = "example.test/markdown-prose"
	root := newReachabilityFixture(t, module)
	writeGoFixture(t, root, "internal/helpercmd/config.go", `package helpercmd

import (
	"os"
	"strings"
)

// The authority file used to be policy.md; prose is not runtime authority.
const migrationNote = "do not parse governance.md in production"

func loadJSON() {
	content, _ := os.ReadFile("policy.json")
	_ = strings.Contains(string(content), ".md")
}
`)
	writeGoFixture(t, root, "internal/helpercmd/config_test.go", `package helpercmd

import "os"

func loadMarkdownFixture() { _, _ = os.ReadFile("fixture.md") }
`)

	if err := CheckDeferredRuntimeSurfaces(context.Background(), root, module); err != nil {
		t.Fatalf("Markdown prose, non-Markdown reads, or test-only parsing triggered the production guard: %v", err)
	}
}

func newReachabilityFixture(t *testing.T, module string) string {
	t.Helper()
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	setIsolatedGoPaths(t)
	return root
}
