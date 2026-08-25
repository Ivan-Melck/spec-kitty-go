package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CallerClass describes the production caller required for a selected symbol.
type CallerClass string

const (
	CallerExternalPackage       CallerClass = "external_package"
	CallerSamePackageProduction CallerClass = "same_package_production"
	CallerEitherProduction      CallerClass = "either_production"
)

// ReachabilityTarget is an intentionally small registry entry for an
// authority-critical guard or concrete port constructor.
type ReachabilityTarget struct {
	Package             string
	Symbol              string
	RequiredCallerClass CallerClass
}

// DefaultReachabilityTargets returns the curated production registry. The
// integration constructor is a real composition seam with one external
// production caller in internal/helpercmd.
func DefaultReachabilityTargets(modulePath string) []ReachabilityTarget {
	return []ReachabilityTarget{{
		Package: modulePath + "/internal/integration", Symbol: "NewService",
		RequiredCallerClass: CallerExternalPackage,
	}}
}

// ReachabilityViolation is a stable symbol-oriented diagnostic.
type ReachabilityViolation struct {
	Rule                RuleID
	Package             string
	Symbol              string
	RequiredCallerClass CallerClass
}

func (v ReachabilityViolation) Error() string {
	return fmt.Sprintf("rule=%s package=%s symbol=%s required_caller=%s", v.Rule, v.Package, v.Symbol, v.RequiredCallerClass)
}

type reachabilityViolations []ReachabilityViolation

func (v reachabilityViolations) Error() string {
	parts := make([]string, len(v))
	for index := range v {
		parts[index] = v[index].Error()
	}
	return strings.Join(parts, "\n")
}

type reachabilityObservation struct {
	declared bool
	same     bool
	external bool
}

// CheckReachability parses production Go files and verifies the curated
// targets. Files ending in _test.go never satisfy a target.
func CheckReachability(root, modulePath string, targets []ReachabilityTarget) error {
	if modulePath == "" {
		return fmt.Errorf("reachability module path is required")
	}
	ordered := append([]ReachabilityTarget(nil), targets...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Package != ordered[j].Package {
			return ordered[i].Package < ordered[j].Package
		}
		return ordered[i].Symbol < ordered[j].Symbol
	})
	observations := make(map[ReachabilityTarget]*reachabilityObservation, len(ordered))
	for _, target := range ordered {
		if !withinModule(modulePath, target.Package) || target.Symbol == "" || !token.IsIdentifier(target.Symbol) {
			return fmt.Errorf("invalid reachability target package=%q symbol=%q", target.Package, target.Symbol)
		}
		switch target.RequiredCallerClass {
		case CallerExternalPackage, CallerSamePackageProduction, CallerEitherProduction:
		default:
			return fmt.Errorf("invalid reachability caller class %q", target.RequiredCallerClass)
		}
		observations[target] = &reachabilityObservation{}
	}

	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filePath == root {
				return nil
			}
			name := entry.Name()
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") {
			return nil
		}
		relativeDir, err := filepath.Rel(root, filepath.Dir(filePath))
		if err != nil {
			return err
		}
		packagePath := modulePath
		if relativeDir != "." {
			packagePath += "/" + filepath.ToSlash(relativeDir)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, 0)
		if err != nil {
			return fmt.Errorf("parse production source %s: %w", filePath, err)
		}
		aliases := make(map[string]string, len(parsed.Imports))
		for _, declaration := range parsed.Imports {
			importPath, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", filePath, err)
			}
			alias := path.Base(importPath)
			if declaration.Name != nil {
				alias = declaration.Name.Name
			}
			if alias != "_" && alias != "." {
				aliases[alias] = importPath
			}
		}
		for _, target := range ordered {
			observation := observations[target]
			if packagePath == target.Package {
				for _, declaration := range parsed.Decls {
					function, ok := declaration.(*ast.FuncDecl)
					if ok && function.Recv == nil && function.Name.Name == target.Symbol {
						observation.declared = true
					}
				}
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch function := call.Fun.(type) {
				case *ast.Ident:
					if packagePath == target.Package && function.Name == target.Symbol {
						observation.same = true
					}
				case *ast.SelectorExpr:
					identifier, ok := function.X.(*ast.Ident)
					if ok && function.Sel.Name == target.Symbol && aliases[identifier.Name] == target.Package && packagePath != target.Package {
						observation.external = true
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		return err
	}

	violations := make(reachabilityViolations, 0)
	for _, target := range ordered {
		observation := observations[target]
		if !observation.declared {
			violations = append(violations, ReachabilityViolation{Rule: RuleReachabilityTarget, Package: target.Package, Symbol: target.Symbol, RequiredCallerClass: target.RequiredCallerClass})
			continue
		}
		reachable := (target.RequiredCallerClass == CallerExternalPackage && observation.external) ||
			(target.RequiredCallerClass == CallerSamePackageProduction && observation.same) ||
			(target.RequiredCallerClass == CallerEitherProduction && (observation.external || observation.same))
		if !reachable {
			violations = append(violations, ReachabilityViolation{Rule: RuleReachability, Package: target.Package, Symbol: target.Symbol, RequiredCallerClass: target.RequiredCallerClass})
		}
	}
	if len(violations) > 0 {
		return violations
	}
	return nil
}

// CheckDeferredRuntimeSurfaces rejects production code that launches a Python
// interpreter. Import and package-based deferred surfaces are enforced by
// Policy; this targeted AST check covers the runtime dependency that an import
// graph cannot represent without returning to broad textual name matching.
func CheckDeferredRuntimeSurfaces(root, modulePath string) error {
	seen := make(map[Violation]bool)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filePath == root {
				return nil
			}
			name := entry.Name()
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") {
			return nil
		}
		relativeDir, err := filepath.Rel(root, filepath.Dir(filePath))
		if err != nil {
			return err
		}
		packagePath := modulePath
		if relativeDir != "." {
			packagePath += "/" + filepath.ToSlash(relativeDir)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, 0)
		if err != nil {
			return fmt.Errorf("parse production source %s: %w", filePath, err)
		}
		execAliases := make(map[string]bool)
		for _, declaration := range parsed.Imports {
			importPath, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", filePath, err)
			}
			if importPath != "os/exec" {
				continue
			}
			alias := "exec"
			if declaration.Name != nil {
				alias = declaration.Name.Name
			}
			if alias != "_" && alias != "." {
				execAliases[alias] = true
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext") {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok || !execAliases[identifier.Name] {
				return true
			}
			argumentIndex := 0
			if selector.Sel.Name == "CommandContext" {
				argumentIndex = 1
			}
			if len(call.Args) <= argumentIndex {
				return true
			}
			literal, ok := call.Args[argumentIndex].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			command, err := strconv.Unquote(literal.Value)
			if err != nil || !isPythonRuntime(command) {
				return true
			}
			seen[Violation{Rule: RuleDeferredSurface, SourcePackage: packagePath, TargetPackage: "runtime:" + command}] = true
			return true
		})
		return nil
	})
	if err != nil {
		return err
	}
	violations := make(Violations, 0, len(seen))
	for violation := range seen {
		violations = append(violations, violation)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].SourcePackage != violations[j].SourcePackage {
			return violations[i].SourcePackage < violations[j].SourcePackage
		}
		return violations[i].TargetPackage < violations[j].TargetPackage
	})
	if len(violations) > 0 {
		return violations
	}
	return nil
}

func isPythonRuntime(command string) bool {
	name := strings.ToLower(filepath.Base(command))
	return name == "python" || name == "python2" || name == "python3" ||
		name == "pypy" || name == "pypy2" || name == "pypy3" ||
		strings.HasPrefix(name, "python2.") || strings.HasPrefix(name, "python3.") ||
		strings.HasPrefix(name, "pypy2.") || strings.HasPrefix(name, "pypy3.")
}
