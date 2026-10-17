package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Source-selection / AST evidence only: not a binary, runtime, admission,
// descriptor, resource, expiry, memory or performance qualification certificate.
func TestExpiryQualificationConditionalSurface(t *testing.T) {
	if runtime.Version() != "go1.26.7" {
		t.Fatal("requires pinned toolchain")
	}
	root := architectureRepositoryRoot(t)
	for _, profile := range []struct {
		name, os, arch  string
		tagged, enabled bool
	}{
		{"ordinary", "linux", "amd64", false, false},
		{"enabled", "linux", "amd64", true, true},
		{"arm64", "linux", "arm64", true, false},
		{"windows", "windows", "amd64", true, false},
		{"darwin", "darwin", "amd64", true, false},
	} {
		t.Run(profile.name, func(t *testing.T) {
			for _, pkg := range []string{"internal/sidecar/gapdb", "internal/productionruntime"} {
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				args := []string{"list", "-e", "-json"}
				if profile.tagged {
					args = append(args, "-tags=spec_kitty_expiry_host_qualification")
				}
				args = append(args, "./"+pkg)
				cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin/go"), args...)
				cmd.Dir = root
				for _, e := range os.Environ() {
					key := strings.SplitN(e, "=", 2)[0]
					if strings.HasPrefix(key, "SPEC_KITTY_") || key == "GOOS" || key == "GOARCH" || key == "GOFLAGS" || key == "GOEXPERIMENT" || key == "GODEBUG" || key == "GOAMD64" || key == "CGO_ENABLED" || key == "GOTOOLCHAIN" {
						continue
					}
					cmd.Env = append(cmd.Env, e)
				}
				cmd.Env = append(cmd.Env, "GOOS="+profile.os, "GOARCH="+profile.arch, "GOAMD64=v1", "CGO_ENABLED=1", "GOTOOLCHAIN=local", "GOFLAGS=", "GOEXPERIMENT=", "GODEBUG=")
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				body, err := cmd.Output()
				cancel()
				if err != nil {
					t.Fatalf("go list %s: %v: %s", pkg, err, stderr.String())
				}
				var listed struct {
					Dir                     string
					GoFiles, IgnoredGoFiles []string
				}
				if err := json.Unmarshal(body, &listed); err != nil || listed.Dir != filepath.Join(root, filepath.FromSlash(pkg)) {
					t.Fatalf("bad package selection %s: %v", pkg, err)
				}
				selected, ignored := map[string]bool{}, map[string]bool{}
				for _, f := range listed.GoFiles {
					selected[f] = true
				}
				for _, f := range listed.IgnoredGoFiles {
					ignored[f] = true
				}
				required := []string{"expiry_qualification.go", "expiry_qualification_observation.go"}
				if pkg == "internal/sidecar/gapdb" {
					required = []string{"expiry_qualification_input.go", "expiry_qualification_launch.go"}
				}
				for _, f := range required {
					if selected[f] != profile.enabled || ignored[f] == profile.enabled {
						t.Fatalf("%s selection/complement %s", pkg, f)
					}
				}
				if selected["expiry_qualification_disabled.go"] == profile.enabled || ignored["expiry_qualification_disabled.go"] != profile.enabled {
					t.Fatalf("%s disabled complement drift", pkg)
				}
				exports := map[string]bool{}
				for _, name := range listed.GoFiles {
					file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(listed.Dir, name), nil, 0)
					if err != nil {
						t.Fatal(err)
					}
					for _, decl := range file.Decls {
						switch d := decl.(type) {
						case *ast.FuncDecl:
							if !d.Name.IsExported() {
								continue
							}
							receiver := ""
							if d.Recv != nil {
								x := d.Recv.List[0].Type
								if star, ok := x.(*ast.StarExpr); ok {
									x = star.X
								}
								if id, ok := x.(*ast.Ident); ok {
									receiver = id.Name + "."
								}
							}
							exports[receiver+d.Name.Name] = true
						case *ast.GenDecl:
							for _, spec := range d.Specs {
								if typ, ok := spec.(*ast.TypeSpec); ok && typ.Name.IsExported() {
									exports[typ.Name.Name] = true
								}
							}
						}
					}
					if !profile.enabled {
						ast.Inspect(file, func(n ast.Node) bool {
							literal, ok := n.(*ast.BasicLit)
							if !ok || literal.Kind != token.STRING {
								return true
							}
							value, _ := strconv.Unquote(literal.Value)
							for _, pin := range []string{"53fa2099c5798c6ac7d05ca782deba422412605fc9a9a9696271823652138b57", "9d65f8bd363b4f416688a2038714e531e4bfe7a7a81976decb0c8eaae0f834e7", "1f80dde08a349ccc16e3ffef825cabfe8a8c7c2db50444ee15c9598c68ecffbf", "83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b"} {
								if strings.Contains(value, pin) {
									t.Errorf("disabled source leaks qualification pin: %s", name)
								}
							}
							return true
						})
					}
				}
				for name := range exports {
					isExpiry := strings.HasPrefix(name, "Expiry") || strings.Contains(name, ".Expiry") || strings.Contains(name, "StartExpiry") || strings.Contains(name, "CrashExpiry")
					if isExpiry && !profile.enabled {
						t.Errorf("disabled exported surface: %s.%s", pkg, name)
					}
				}
				if pkg == "internal/sidecar/gapdb" && profile.enabled {
					allowed := map[string]bool{}
					for _, name := range []string{"ExpiryTechnicalIdentity", "ExpiryQualification", "StartExpiryQualification", "ExpiryQualification.WaitHeld", "ExpiryQualification.CheckHeld", "ExpiryQualification.Release", "ExpiryQualification.Close", "ExpiryQualification.Identity", "ExpiryQualification.CleanupError", "ExpiryQualification.RecordedCleanupError", "ExpiryQualification.Done", "Lifecycle.CrashExpiryQualification"} {
						allowed[name] = true
						if !exports[name] {
							t.Errorf("missing required fixed E operation %s", name)
						}
					}
					for name := range exports {
						if strings.HasPrefix(name, "ExpiryQualification.") && !allowed[name] {
							t.Errorf("unexpected fixed E method %s", name)
						}
					}
				}
			}
		})
	}
}

func TestExpiryQualificationConditionalFieldsFirst(t *testing.T) {
	root := architectureRepositoryRoot(t)
	for _, target := range []struct{ file, typ, field, conditional string }{
		{"internal/sidecar/gapdb/provenance.go", "VerifiedExecutable", "expiry", "verifiedExpiryState"},
		{"internal/sidecar/gapdb/lifecycle.go", "Lifecycle", "expiry", "lifecycleExpiryState"},
		{"internal/productionruntime/composition.go", "productionOwner", "expiry", "ownerExpiryState"},
		{"internal/productionruntime/host.go", "Host", "expiry", "hostExpiryState"},
		{"internal/productionruntime/recovery.go", "AuthorityBindingV2", "qualification", "authorityQualificationBinding"},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, target.file), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || spec.Name.Name != target.typ {
				return true
			}
			found = true
			st, ok := spec.Type.(*ast.StructType)
			if !ok || len(st.Fields.List) == 0 {
				t.Errorf("missing struct %s", target.typ)
				return false
			}
			first := st.Fields.List[0]
			id, ok := first.Type.(*ast.Ident)
			if !ok || id.Name != target.conditional || len(first.Names) != 1 || first.Names[0].Name != target.field {
				t.Errorf("conditional field must be FIRST: %s", target.typ)
			}
			return false
		})
		if !found {
			t.Errorf("missing %s", target.typ)
		}
	}
}
