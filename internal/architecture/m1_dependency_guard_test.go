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

const m1GapDBModule = "github.com/spec-kitty/gapdb"

var m1GapDBImportAllowlist = map[string]bool{
	"internal/controlstore/gapdb": true,
	"internal/sidecar/gapdb":      true,
}

var m1SourceRoots = []string{
	"internal/controljournal",
	"internal/controlstore/gapdb",
	"internal/sidecar/gapdb",
	"internal/provingground",
	"internal/testcampaign/m1",
	"cmd/spk-m1-proving-ground",
}

// These exact identifiers are the frozen source-level constructions forbidden
// by plan.md sections 2.3, 4, and 8.3. The rule is intentionally structural:
// comments and string literals do not match, and historical packages outside
// the M1 roots remain comparison material rather than being reclassified.
var m1ForbiddenIdentifiers = map[string]string{
	"SQLiteFallback":              "sqlite-fallback",
	"UseSQLiteFallback":           "sqlite-fallback",
	"FallbackToSQLite":            "sqlite-fallback",
	"CurrentBatchRevision":        "current-batch-revision",
	"PredictedBatchRevision":      "current-batch-revision",
	"CurrentCommitRevision":       "current-batch-revision",
	"GlobalEpochCAS":              "global-authority-cas",
	"CompareAndSwapGlobalEpoch":   "global-authority-cas",
	"SharedProjectionHead":        "shared-projection-head",
	"GlobalProjectionHead":        "shared-projection-head",
	"LeaseGenerationRuntimeEpoch": "runtime-coordinate-conflation",
	"FenceTokenRuntimeEpoch":      "runtime-coordinate-conflation",
}

type m1SourceFile struct {
	Path   string
	Source []byte
}

type m1ArchitectureViolation struct {
	Path   string
	Kind   string
	Detail string
}

func (v m1ArchitectureViolation) String() string {
	return fmt.Sprintf("%s: %s: %s", v.Path, v.Kind, v.Detail)
}

func TestM1DependencyBoundaries(t *testing.T) {
	files := collectM1RepositorySources(t)
	violations := inspectM1Sources(files)
	if len(violations) != 0 {
		messages := make([]string, len(violations))
		for index, violation := range violations {
			messages[index] = violation.String()
		}
		t.Fatalf("M1 architecture boundary violations:\n%s", strings.Join(messages, "\n"))
	}
}

func TestM1DependencyMutantControls(t *testing.T) {
	clean := []m1SourceFile{
		{Path: "internal/controlstore/gapdb/client.go", Source: []byte("package gapdb\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
		{Path: "internal/controljournal/model.go", Source: []byte("package controljournal\n// CurrentBatchRevision and internal/server are documentation only.\nvar note = \"sqlite fallback\"\n")},
		{Path: "internal/controlstore/sqlite/store.go", Source: []byte("package sqlite\nimport _ \"modernc.org/sqlite\"\n")},
	}
	if violations := inspectM1Sources(clean); len(violations) != 0 {
		t.Fatalf("clean controls rejected: %v", violations)
	}

	tests := []struct {
		id   string
		file m1SourceFile
		kind string
	}{
		{
			id:   "gapdb-import-allowlist-widening",
			file: m1SourceFile{Path: "internal/controljournal/client.go", Source: []byte("package controljournal\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
			kind: "gapdb-import-allowlist",
		},
		{
			id:   "gapdb-import-neutral-package",
			file: m1SourceFile{Path: "internal/neutral/client_test.go", Source: []byte("package neutral\nimport _ \"github.com/spec-kitty/gapdb/client\"\n")},
			kind: "gapdb-import-allowlist",
		},
		{
			id:   "gapdb-internal-server",
			file: m1SourceFile{Path: "internal/sidecar/gapdb/server.go", Source: []byte("package gapdb\nimport _ \"github.com/spec-kitty/gapdb/internal/server\"\n")},
			kind: "internal-server-import",
		},
		{
			id:   "sqlite-fallback",
			file: m1SourceFile{Path: "internal/provingground/runtime.go", Source: []byte("package provingground\nimport _ \"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite\"\nvar UseSQLiteFallback bool\n")},
			kind: "sqlite-import",
		},
		{
			id:   "current-batch-revision-fill-in",
			file: m1SourceFile{Path: "internal/controljournal/application/apply.go", Source: []byte("package application\nvar CurrentBatchRevision uint64\n")},
			kind: "current-batch-revision",
		},
		{
			id:   "global-authority-cas",
			file: m1SourceFile{Path: "internal/controljournal/application/apply.go", Source: []byte("package application\nfunc CompareAndSwapGlobalEpoch() {}\n")},
			kind: "global-authority-cas",
		},
		{
			id:   "shared-projection-head",
			file: m1SourceFile{Path: "internal/controljournal/reducer/reduce.go", Source: []byte("package reducer\ntype SharedProjectionHead struct{}\n")},
			kind: "shared-projection-head",
		},
		{
			id:   "runtime-coordinate-conflation",
			file: m1SourceFile{Path: "internal/controljournal/model.go", Source: []byte("package controljournal\nvar FenceTokenRuntimeEpoch string\n")},
			kind: "runtime-coordinate-conflation",
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			violations := inspectM1Sources([]m1SourceFile{test.file})
			if !containsM1Violation(violations, test.kind) {
				t.Fatalf("mutant survived %s guard: %v", test.kind, violations)
			}
		})
	}
}

func collectM1RepositorySources(t *testing.T) []m1SourceFile {
	t.Helper()
	root := filepath.Clean(filepath.Join(m1ContractsDir(t), "..", "..", ".."))
	var files []m1SourceFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, m1SourceFile{Path: filepath.ToSlash(relative), Source: source})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func inspectM1Sources(files []m1SourceFile) []m1ArchitectureViolation {
	var violations []m1ArchitectureViolation
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, parser.ImportsOnly)
		if err != nil {
			violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: err.Error()})
			continue
		}
		packagePath := filepath.ToSlash(filepath.Dir(file.Path))
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: "invalid import path"})
				continue
			}
			if strings.Contains(path, "/internal/server") || strings.HasSuffix(path, "internal/server") {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "internal-server-import", Detail: path})
				continue
			}
			if path == m1GapDBModule || strings.HasPrefix(path, m1GapDBModule+"/") {
				if !m1GapDBImportAllowlist[packagePath] {
					violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "gapdb-import-allowlist", Detail: path})
				}
			}
			if isM1SourcePath(file.Path) && isSQLiteImport(path) {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "sqlite-import", Detail: path})
			}
		}

		if isM1SourcePath(file.Path) && !strings.HasSuffix(file.Path, "_test.go") {
			whole, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, 0)
			if err != nil {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: err.Error()})
				continue
			}
			seen := make(map[string]bool)
			ast.Inspect(whole, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				kind, forbidden := m1ForbiddenIdentifiers[identifier.Name]
				if forbidden && !seen[kind] {
					seen[kind] = true
					violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: kind, Detail: identifier.Name})
				}
				return true
			})
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Path != violations[j].Path {
			return violations[i].Path < violations[j].Path
		}
		if violations[i].Kind != violations[j].Kind {
			return violations[i].Kind < violations[j].Kind
		}
		return violations[i].Detail < violations[j].Detail
	})
	return violations
}

func isM1SourcePath(path string) bool {
	path = filepath.ToSlash(path)
	for _, root := range m1SourceRoots {
		if path == root+".go" || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func isSQLiteImport(path string) bool {
	return path == "modernc.org/sqlite" || strings.HasSuffix(path, "/controlstore/sqlite")
}

func containsM1Violation(violations []m1ArchitectureViolation, kind string) bool {
	for _, violation := range violations {
		if violation.Kind == kind {
			return true
		}
	}
	return false
}
