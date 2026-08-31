package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	m1CampaignImport = "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	m1CrownImport    = m1CampaignImport + "/crown"
)

var m1CrownGuardPaths = []string{
	"cmd/spk-m1-proving-ground",
	"internal/testcampaign/m1/crown",
	"internal/testcampaign/m1/campaign.go",
	"internal/testcampaign/m1/evidence.go",
	"internal/testcampaign/m1/adoption.go",
	"internal/testcampaign/m1/external",
	"internal/testcampaign/m1/faults",
	"internal/testcampaign/m1/focused",
}

var m1CrownRequiredCalls = map[string][]string{
	m1CrownImport:    {"Run"},
	m1CampaignImport: {"VerifyEvidence", "AdoptionTemplate", "VerifyAdoption"},
}

// The decisive M1 command and every test-campaign surface it composes must
// remain on the admitted GapDB boundary. This narrower crown guard complements
// the general M1 dependency guard and also proves that the operator command
// contains real calls to the crown, evidence, and adoption APIs.
func TestM1CrownSourceBoundaries(t *testing.T) {
	files := collectM1CrownSources(t)
	if violations := inspectM1CrownSources(files); len(violations) != 0 {
		t.Fatalf("M1 crown architecture violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestM1CrownSourceMutantControls(t *testing.T) {
	clean := []m1SourceFile{
		{Path: "cmd/spk-m1-proving-ground/main.go", Source: []byte(`package main
import (
	"context"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	trial "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/crown"
)
func exercise(ctx context.Context) {
	_, _ = trial.Run(ctx)
	_, _, _ = m1.VerifyEvidence(ctx, "/evidence", m1.VerifyOptions{})
	_, _ = m1.AdoptionTemplate(m1.Verification{}, m1.EvidenceManifest{})
	_, _ = m1.VerifyAdoption(m1.AdoptionRecord{}, m1.TrustedSigner{}, m1.Verification{}, m1.EvidenceManifest{}, m1.RecoveryState{})
}
`)},
		{Path: "internal/testcampaign/m1/crown/runner.go", Source: []byte("package crown\nfunc Run() {}\n")},
		{Path: "internal/testcampaign/m1/evidence.go", Source: []byte("package m1\nfunc VerifyEvidence() {}\n")},
		{Path: "internal/testcampaign/m1/adoption.go", Source: []byte("package m1\nfunc AdoptionTemplate() {}\nfunc VerifyAdoption() {}\n")},
		{Path: "internal/testcampaign/m1/external/runner.go", Source: []byte("package external\n")},
		{Path: "internal/testcampaign/m1/faults/runner.go", Source: []byte("package faults\n")},
		{Path: "internal/testcampaign/m1/focused/campaign.go", Source: []byte("package focused\n")},
	}
	if violations := inspectM1CrownSources(clean); len(violations) != 0 {
		t.Fatalf("clean crown fixture rejected: %v", violations)
	}

	mutants := []struct {
		name string
		file m1SourceFile
		want string
	}{
		{
			name: "sqlite import",
			file: m1SourceFile{Path: "internal/testcampaign/m1/crown/runner.go", Source: []byte("package crown\nimport _ \"modernc.org/sqlite\"\n")},
			want: "sqlite-import",
		},
		{
			name: "sqlite fallback identifier",
			file: m1SourceFile{Path: "internal/testcampaign/m1/faults/runner.go", Source: []byte("package faults\nvar SQLiteFallback bool\n")},
			want: "sqlite-fallback",
		},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			files := replaceM1CrownFixture(clean, mutant.file)
			if violations := inspectM1CrownSources(files); !containsM1CrownViolation(violations, mutant.want) {
				t.Fatalf("mutant survived %s guard: %v", mutant.want, violations)
			}
		})
	}

	for importPath, selectors := range m1CrownRequiredCalls {
		for _, selector := range selectors {
			name := filepath.Base(importPath) + "." + selector
			t.Run("missing "+name, func(t *testing.T) {
				mutant := append([]m1SourceFile(nil), clean...)
				mutant[0].Source = []byte(strings.ReplaceAll(string(mutant[0].Source), "\t_, _ = trial.Run(ctx)\n", ""))
				if importPath == m1CampaignImport {
					lines := strings.Split(string(mutant[0].Source), "\n")
					for index, line := range lines {
						if strings.Contains(line, "m1."+selector+"(") {
							lines[index] = ""
						}
					}
					mutant[0].Source = []byte(strings.Join(lines, "\n"))
				}
				want := "command-live-call " + importPath + "." + selector
				if violations := inspectM1CrownSources(mutant); !containsM1CrownViolation(violations, want) {
					t.Fatalf("missing call mutant survived %s guard: %v", want, violations)
				}
			})
		}
	}
}

func collectM1CrownSources(t *testing.T) []m1SourceFile {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	var files []m1SourceFile
	for _, relative := range m1CrownGuardPaths {
		path := filepath.Join(repositoryRoot, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("required crown source surface %s: %v", relative, err)
		}
		before := len(files)
		if info.IsDir() {
			err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				return appendM1CrownSource(repositoryRoot, path, &files)
			})
		} else if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
			err = appendM1CrownSource(repositoryRoot, path, &files)
		}
		if err != nil {
			t.Fatalf("read crown source surface %s: %v", relative, err)
		}
		if len(files) == before {
			t.Fatalf("required crown source surface %s has no production Go files", relative)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func appendM1CrownSource(repositoryRoot, path string, files *[]m1SourceFile) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return err
	}
	*files = append(*files, m1SourceFile{Path: filepath.ToSlash(relative), Source: source})
	return nil
}

func inspectM1CrownSources(files []m1SourceFile) []string {
	foundCalls := make(map[string]bool)
	var violations []string
	for _, source := range files {
		file, err := parser.ParseFile(token.NewFileSet(), source.Path, source.Source, 0)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: parse: %v", source.Path, err))
			continue
		}
		imports := make(map[string]string)
		for _, specification := range file.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				violations = append(violations, fmt.Sprintf("%s: malformed import %s", source.Path, specification.Path.Value))
				continue
			}
			if isM1CrownSQLiteName(importPath) {
				violations = append(violations, source.Path+": sqlite-import "+importPath)
			}
			if _, guarded := m1CrownRequiredCalls[importPath]; guarded {
				name := filepath.Base(importPath)
				if specification.Name != nil {
					name = specification.Name.Name
				}
				if name == "." || name == "_" {
					violations = append(violations, source.Path+": guarded command import must have a named package qualifier "+importPath)
					continue
				}
				imports[name] = importPath
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.Ident:
				if isM1CrownSQLiteName(node.Name) {
					violations = append(violations, source.Path+": sqlite-fallback identifier "+node.Name)
				}
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				qualifier, ok := selector.X.(*ast.Ident)
				if !ok {
					break
				}
				if importPath, guarded := imports[qualifier.Name]; guarded && qualifier.Obj == nil && strings.HasPrefix(source.Path, "cmd/spk-m1-proving-ground/") {
					foundCalls[importPath+"."+selector.Sel.Name] = true
				}
			}
			return true
		})
	}
	for importPath, selectors := range m1CrownRequiredCalls {
		for _, selector := range selectors {
			key := importPath + "." + selector
			if !foundCalls[key] {
				violations = append(violations, "cmd/spk-m1-proving-ground: command-live-call "+key+" missing")
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func isM1CrownSQLiteName(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "sqlite") || strings.Contains(lower, "sqlcipher")
}

func replaceM1CrownFixture(files []m1SourceFile, replacement m1SourceFile) []m1SourceFile {
	result := append([]m1SourceFile(nil), files...)
	for index := range result {
		if result[index].Path == replacement.Path {
			result[index] = replacement
			return result
		}
	}
	return append(result, replacement)
}

func containsM1CrownViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return true
		}
	}
	return false
}
