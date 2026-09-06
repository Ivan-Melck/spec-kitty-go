package architecture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	m2AbsenceMaxFiles = 50000
	m2AbsenceMaxBytes = 256 << 20
	m2LegacyWord      = "sql" + "ite"
	m2LegacyTitle     = "SQL" + "ite"
	m2ModerncDriver   = "modernc.org/" + m2LegacyWord
	m2MattnDriver     = "github.com/mattn/go-" + m2LegacyWord + "3"
	m2ZombiezenDriver = "zombiezen.com/go/" + m2LegacyWord
	m2NcrucesDriver   = "github.com/ncruces/go-" + m2LegacyWord + "3"
	m2GlebarezDriver  = "github.com/glebarez/" + m2LegacyWord
	m2FallbackToken   = m2LegacyWord + "fallback"
	m2LegacyCLI       = m2LegacyWord + "3"
	m2JournalPragma   = "pragma journal_" + "mode"
	m2CreateTable     = "create " + "table"
	m2EmbedDatabase   = "go:embed *" + ".db"
)

var retiredSQLiteBlobs = map[string]string{
	"artifact.go":                  "25da7c575f012e726185d923ae9b0c8547d3822b",
	"artifact_test.go":             "179c460566a55c1f052036ecb53f22d9683d454a",
	"authority.go":                 "9c84f27bb42a6fb5f2b102de63f7b8d84d1cb890",
	"authority_test.go":            "c196dfdb71166e9985fc4f3bc72cbe563fa84162",
	"db.go":                        "bde79be22fb559f507fea628fbc0378c0c9f4d29",
	"decomposition.go":             "449baa5b33439fb17f856c25f3c8d643b1b447d2",
	"decomposition_test.go":        "22d3d458751598627db64d7a1111da40f306c923",
	"external_effect.go":           "e3602db1bed6b9ab30db8b79e840d6c6b58a4ae6",
	"external_effect_test.go":      "b70d27e2fa2c569d1b7c105e5ff005427cec925a",
	"governance_context.go":        "8f7e4b7f6f87fcded3cf0281d4c10d1fd7621a86",
	"governance_context_test.go":   "079f9d1eb4d8ba9ecb22973b13543819352efa66",
	"handoff_review.go":            "3724509d2340c2545d2f1953b1faf1941b4e52a1",
	"handoff_review_test.go":       "bed8c4648a3cb7db9094b533281cb8ec72f467b7",
	"integration.go":               "e2db83bf221fa47bc74ec878609023205ac9f7c1",
	"integration_refusal_test.go":  "610b4da25e708d0ade12f49752392b0cc3eabecd",
	"integration_test.go":          "3ab384c8ca7f9b5b99c15d6ecb4f8968de310e8a",
	"migrations.go":                "7f7edc324e85c3ee3a409be02fac74fdb22ed072",
	"mission.go":                   "f7c0f023a8fed435ef519bfa4b4d653826b5e1cb",
	"mission_action.go":            "01c802bbc6a74a6cab7a932ec14297b95c21f5f2",
	"mission_action_test.go":       "f350348b09d5a8244df18e703216e74287a815c9",
	"mission_method_query.go":      "8fcde16e6adef02cafbf6990f2038750dafd1cb2",
	"mission_method_query_test.go": "f02025b6ff667ba08746121f3fd73759fccbb1e4",
	"mission_test.go":              "c13c11481d63ced588db0539a301c68603f4bdae",
	"reconciliation_v2.go":         "9dc07e42ad7d0c3f712b5ba176890f192e8843a9",
	"reconciliation_v2_test.go":    "12ba58d7647d7996814d14b8fd8cbc096860de8b",
	"recovery.go":                  "b3e1796f814387e47b43b8dc9a75ed8819c1570f",
	"recovery_test.go":             "048eb049b02cbf7f8794d735bc00fb3185bf4260",
	"start_run.go":                 "581bff2f55039c52ef9bcf7cf7d60d6dbacd9a30",
	"start_run_test.go":            "d3df4017397d0e542e033a0606ac786403eb53e6",
	"workgraph.go":                 "9ae27565065a258d852d26e7700b27a4ba21bfc4",
	"workgraph_test.go":            "54b03dee04cfb68a625be96bf6c629a75034ad5c",
	"workrevision_v2.go":           "73c7dde09f2e12df83fced66900800318b347afd",
	"workrevision_v2_test.go":      "d166612fc264d33ba33e19a194cc0faa68ed8e9e",
}

var m2RetiredModules = []string{
	m2ModerncDriver, "modernc.org/libc", "modernc.org/mathutil", "modernc.org/memory",
	"modernc.org/cc/v4", "modernc.org/ccgo/v4", "modernc.org/fileutil", "modernc.org/gc/v2",
	"modernc.org/gc/v3", "modernc.org/goabi0", "modernc.org/opt", "modernc.org/sortutil",
	"modernc.org/strutil", "modernc.org/token", "github.com/ncruces/go-strftime",
	"github.com/remyoudompheng/bigfft",
}

var m2ForbiddenImports = []string{
	"database/sql", m2ModerncDriver, m2MattnDriver,
	m2GlebarezDriver, "github.com/glebarez/go-" + m2LegacyWord,
	m2NcrucesDriver, m2ZombiezenDriver, "crawshaw.io/" + m2LegacyWord,
	"gorm.io/driver/" + m2LegacyWord, "github.com/uptrace/bun/driver/" + m2LegacyWord + "dialect",
}

// m2HistoricalExemptions is deliberately closed and token-specific. These are
// immutable evidence, mutation controls, and the bounded header classifier
// that must continue to describe the retired implementation truthfully. Go
// imports are always parsed before this table is consulted, so an executable
// edge cannot be sheltered by a textual exemption.
var m2HistoricalExemptions = map[string]map[string]bool{
	"cmd/spec-kitty/main_test.go":                {m2LegacyWord + " format 3": true, "--db": true, "--backend": true},
	"docs/phase1-evidence/closeout-results.json": {m2ModerncDriver: true},
	"docs/phase1-evidence/results.json":          {m2ModerncDriver: true},
	"internal/architecture/m1_crown_test.go":     {m2ModerncDriver: true, m2FallbackToken: true},
	"internal/architecture/m1_dependency_guard_test.go": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2ZombiezenDriver: true,
		m2GlebarezDriver: true, m2NcrucesDriver: true,
		m2FallbackToken: true, "fallbackto" + m2LegacyWord: true, "use" + m2FallbackToken: true, m2LegacyCLI: true,
	},
	"internal/architecture/m2_dependency_guard_test.go": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2ZombiezenDriver: true,
		m2GlebarezDriver: true, m2NcrucesDriver: true, m2LegacyCLI: true,
	},
	"internal/architecture/m2_mutant_control_test.go": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2ZombiezenDriver: true,
		m2GlebarezDriver: true, m2NcrucesDriver: true, m2LegacyCLI: true,
	},
	"internal/architecture/policy_test.go": {m2ModerncDriver: true},
	"internal/controlroot/classifier_test.go": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2LegacyWord + " format 3": true, m2LegacyCLI: true,
	},
	"internal/controlroot/signatures.go":                     {m2LegacyWord + " format 3": true},
	"internal/productionruntime/host_test.go":                {m2FallbackToken: true},
	"kitty-specs/beads-tracker-integration-01M0YG7E/plan.md": {m2ModerncDriver: true},
	"kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/tasks/WP01-frozen-journal-contracts-and-guards/review-cycle-5.md": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2LegacyCLI: true,
	},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/plan.md":                                                                         {m2ModerncDriver: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/post-approval-defect-12.md":                                                      {m2ModerncDriver: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP01-m2-contracts-and-permanent-architecture-guards/review-cycle-1.md":     {m2ModerncDriver: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP12-historical-control-root-classification-and-refusal.md":                {m2LegacyWord + " format 3": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP14-portable-scenario-harness-and-behavior-parity/review-cycle-5.md":      {m2ModerncDriver: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof.md":                   {m2ModerncDriver: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/review-cycle-2.md":    {m2ModerncDriver: true, m2LegacyCLI: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/review-feedback-2.md": {m2ModerncDriver: true, m2LegacyCLI: true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof/scope-extension-1.md": {m2ModerncDriver: true},
	"kitty-specs/phase-one-architecture-closeout-01M0WZQ9/plan.md":                                                                         {m2ModerncDriver: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/plan.md": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2JournalPragma: true, m2LegacyCLI: true,
	},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research.md": {
		m2ModerncDriver: true, "go-" + m2LegacyWord + "3": true, m2JournalPragma: true, m2LegacyCLI: true,
	},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research/evidence-log.csv":                                             {m2ModerncDriver: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research/source-register.csv":                                          {m2ModerncDriver: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP01-authority-seed-and-real-claim-race.md":                      {m2ModerncDriver: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP02-stupid-tracker-and-attribution-safe-saga/review-cycle-1.md": {m2CreateTable: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP02-stupid-tracker-and-attribution-safe-saga/review-cycle-2.md": {m2CreateTable: true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/quickstart.md":                                                         {m2LegacyCLI: true},
	"kitty-specs/software-development-mission-method-01M11EX2/plan.md":                                                                     {m2ModerncDriver: true},
}

type m2AbsenceFinding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type m2AbsenceEvidence struct {
	SchemaVersion        int                `json:"schema_version"`
	Files                int                `json:"files"`
	Bytes                int64              `json:"bytes"`
	RetiredBlobs         int                `json:"retired_blobs"`
	HistoricalExemptions int                `json:"historical_exemptions"`
	GoPackages           int                `json:"go_packages"`
	Modules              int                `json:"modules"`
	Findings             []m2AbsenceFinding `json:"findings"`
}

func TestM2SQLiteAbsence(t *testing.T) {
	root := m2RepositoryRoot(t)
	evidence := inspectM2Absence(t, root, true)
	if len(retiredSQLiteBlobs) != 33 {
		t.Fatalf("retired SQLite blob census=%d want=33", len(retiredSQLiteBlobs))
	}
	if evidence.RetiredBlobs != 33 || len(evidence.Findings) != 0 {
		encoded, _ := json.Marshal(evidence)
		t.Fatalf("M2 SQLite absence failed: %s", encoded)
	}
	encoded, _ := json.Marshal(evidence)
	t.Logf("M2_SQLITE_ABSENCE %s", encoded)
}

func inspectM2Absence(t *testing.T, root string, resolveGraph bool) m2AbsenceEvidence {
	t.Helper()
	evidence := m2AbsenceEvidence{
		SchemaVersion: 1, RetiredBlobs: len(retiredSQLiteBlobs),
		HistoricalExemptions: len(m2HistoricalExemptions), Findings: []m2AbsenceFinding{},
	}
	add := func(kind, path, detail string) {
		evidence.Findings = append(evidence.Findings, m2AbsenceFinding{kind, filepath.ToSlash(path), detail})
	}
	if info, err := os.Lstat(filepath.Join(root, "internal", "controlstore", "sqlite")); err == nil {
		add("retired-package", "internal/controlstore/sqlite", info.Mode().String())
	} else if !os.IsNotExist(err) {
		add("unreadable", "internal/controlstore/sqlite", "lstat")
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		rel, err := filepath.Rel(root, path)
		if err != nil || walkErr != nil {
			add("unreadable", rel, "walk")
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == ".git" || strings.HasPrefix(rel, ".git/") {
				return filepath.SkipDir
			}
			return nil
		}
		evidence.Files++
		if evidence.Files > m2AbsenceMaxFiles {
			add("budget", rel, "file-count")
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			add("unreadable", rel, "stat")
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := filepath.EvalSymlinks(path)
			if readErr != nil || !m2WithinRoot(root, target) {
				add("symlink", rel, "unresolved-or-escaping")
			} else {
				add("symlink", rel, "executable-material-must-not-be-aliased")
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > 8<<20 {
			add("budget", rel, "file-bytes")
			return nil
		}
		evidence.Bytes += info.Size()
		if evidence.Bytes > m2AbsenceMaxBytes {
			add("budget", rel, "byte-count")
			return fs.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil {
			add("unreadable", rel, "read")
			return nil
		}
		inspectM2Candidate(rel, data, add)
		return nil
	})
	if resolveGraph {
		inspectM2GoGraph(t, root, &evidence, add)
		inspectM2ModuleGraph(t, root, &evidence, add)
	}
	sort.Slice(evidence.Findings, func(i, j int) bool {
		left, right := evidence.Findings[i], evidence.Findings[j]
		return left.Kind+"\x00"+left.Path+"\x00"+left.Detail < right.Kind+"\x00"+right.Path+"\x00"+right.Detail
	})
	return evidence
}

func inspectM2Candidate(path string, data []byte, add func(string, string, string)) {
	lower := strings.ToLower(string(data))
	executable := lower
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ParseComments)
		if err != nil {
			add("malformed-go", path, "parse")
			return
		}
		for _, spec := range parsed.Imports {
			value := strings.Trim(spec.Path.Value, "\"")
			if m2ForbiddenImport(value) {
				add("go-import", path, value)
			}
		}
		executable = strings.ToLower(m2GoExecutableText(data) + "\n" + strings.Join(m2GoResolvedExecutables(parsed), "\n"))
	} else if ext == ".sh" {
		rawShell := string(data)
		shellIdentity := m2ShellIdentityFromSource(rawShell)
		shellSource, ambiguity := m2ShellWithoutHeredocBodies(rawShell)
		if ambiguity != "" {
			add("ambiguous-shell", path, ambiguity)
			return
		}
		lower = strings.ToLower(shellSource)
		executable = lower
		resolved, ambiguity := m2ShellResolvedExecutables(shellSource, shellIdentity)
		if ambiguity != "" {
			add("ambiguous-shell", path, ambiguity)
			return
		}
		executable += "\n" + strings.Join(resolved, "\n")
	}
	patterns := []struct{ kind, token string }{
		{"driver", m2ModerncDriver}, {"driver", "go-" + m2LegacyWord + "3"}, {"driver", m2ZombiezenDriver},
		{"driver", m2GlebarezDriver}, {"driver", m2NcrucesDriver},
		{"copied-schema", m2LegacyWord + "_master"}, {"copied-schema", m2JournalPragma}, {"copied-schema", m2LegacyWord + " format 3"}, {"copied-schema", m2CreateTable},
		{"subprocess", "exec.command(\"" + m2LegacyWord}, {"subprocess", "commandcontext(\"" + m2LegacyWord}, {"subprocess", m2LegacyWord + "3"},
		{"dynamic-load", "plugin" + ".open"}, {"linkname", "go:" + "linkname"}, {"cgo", "-l" + m2LegacyWord},
		{"fallback", m2FallbackToken}, {"fallback", "fallbackto" + m2LegacyWord}, {"fallback", "use" + m2FallbackToken},
		{"comparison-read", "compare" + m2LegacyWord}, {"rescue-read", "rescue" + m2LegacyWord}, {"dual-write", "dualwrite" + m2LegacyWord},
		{"shadow-write", "shadowwrite" + m2LegacyWord}, {"migration", "migrate" + m2LegacyWord}, {"automatic-import", "import" + m2LegacyWord},
		{"runtime-open", "open" + m2LegacyWord},
		{"embedded", "go:embed " + m2LegacyWord}, {"embedded", m2EmbedDatabase},
	}
	for _, pattern := range patterns {
		exempt := m2HistoricalExemptions[path][pattern.token]
		if ext == ".go" && strings.Contains(executable, pattern.token) {
			exempt = false
		}
		present := strings.Contains(lower, pattern.token) || strings.Contains(executable, pattern.token)
		if present &&
			(pattern.kind != "linkname" || strings.Contains(lower, m2LegacyWord)) && !exempt {
			add(pattern.kind, path, pattern.token)
		}
	}
	if ext == ".mod" || filepath.Base(path) == "go.sum" {
		for _, retired := range m2RetiredModules {
			if strings.Contains(lower, strings.ToLower(retired)) {
				add("module", path, retired)
			}
		}
	}
	if strings.HasPrefix(path, "cmd/spec-kitty/") || strings.HasPrefix(path, "internal/helpercmd/") {
		for _, token := range []string{"--db", "--backend", "flags.string(\"backend\""} {
			if strings.Contains(lower, token) && !m2HistoricalExemptions[path][token] {
				add("selector", path, token)
			}
		}
	}
}

func inspectM2GoGraph(t *testing.T, root string, evidence *m2AbsenceEvidence, add func(string, string, string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-json", "-test", "./...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		add("go-list", "go://packages", "failed")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg struct {
			ImportPath                         string
			Imports, TestImports, XTestImports []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			add("go-list", "go://packages", "decode")
			break
		}
		evidence.GoPackages++
		for _, imported := range append(append(pkg.Imports, pkg.TestImports...), pkg.XTestImports...) {
			if m2ForbiddenImport(imported) || imported == m2ModulePath+"/internal/controlstore/sqlite" {
				add("go-graph", pkg.ImportPath, imported)
			}
		}
	}
}

func inspectM2ModuleGraph(t *testing.T, root string, evidence *m2AbsenceEvidence, add func(string, string, string)) {
	t.Helper()
	command := exec.Command("go", "list", "-m", "all")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		add("module-graph", "go.mod", "failed")
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		evidence.Modules++
		for _, retired := range m2RetiredModules {
			if fields[0] == retired {
				add("module", "go.mod", retired)
			}
		}
	}
}

func m2ForbiddenImport(path string) bool {
	for _, forbidden := range m2ForbiddenImports {
		if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
			return true
		}
	}
	return false
}

func m2GoExecutableText(data []byte) string {
	files := token.NewFileSet()
	file := files.AddFile("candidate.go", files.Base(), len(data))
	var scan scanner.Scanner
	scan.Init(file, data, nil, scanner.ScanComments)
	var executable strings.Builder
	for {
		_, tok, literal := scan.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING && tok != token.CHAR && tok != token.COMMENT {
			executable.WriteString(literal)
		}
	}
	return executable.String()
}

func m2GoResolvedExecutables(file *ast.File) []string {
	aliases := map[string]bool{}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "os/exec" {
			continue
		}
		name := "exec"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		aliases[name] = true
	}
	values := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.ValueSpec:
			for index, name := range typed.Names {
				if _, exists := values[name.Name]; !exists && index < len(typed.Values) {
					if value, ok := m2GoConstantString(typed.Values[index], values); ok {
						values[name.Name] = value
					}
				}
			}
		case *ast.AssignStmt:
			for index, left := range typed.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || index >= len(typed.Rhs) {
					continue
				}
				if _, exists := values[name.Name]; !exists {
					if value, resolved := m2GoConstantString(typed.Rhs[index], values); resolved {
						values[name.Name] = value
					}
				}
			}
		}
		return true
	})
	var executables []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext") {
			return true
		}
		owner, ok := selector.X.(*ast.Ident)
		if !ok || !aliases[owner.Name] {
			return true
		}
		index := 0
		if selector.Sel.Name == "CommandContext" {
			index = 1
		}
		if index < len(call.Args) {
			if value, resolved := m2GoConstantString(call.Args[index], values); resolved {
				executables = append(executables, value)
			}
		}
		return true
	})
	sort.Strings(executables)
	return executables
}

func m2GoConstantString(expression ast.Expr, values map[string]string) (string, bool) {
	switch typed := expression.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(typed.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return "", false
		}
		left, leftOK := m2GoConstantString(typed.X, values)
		right, rightOK := m2GoConstantString(typed.Y, values)
		return left + right, leftOK && rightOK
	case *ast.ParenExpr:
		return m2GoConstantString(typed.X, values)
	case *ast.Ident:
		value, ok := values[typed.Name]
		return value, ok
	default:
		return "", false
	}
}

var m2ShellAssignment = regexp.MustCompile(`(?s)^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
var m2ShellCommandVariable = regexp.MustCompile(`^"?\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))"?$`)
var m2ShellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var m2ShellUtilityDispatch = map[string]bool{"command": true, "env": true, "exec": true, "nice": true, "nohup": true, "time": true}
var m2ShellInterpreters = map[string]bool{"bash": true, "dash": true, "ksh": true, "sh": true, "zsh": true}
var m2ShellIndirectDispatch = map[string]bool{"eval": true, "xargs": true}
var m2ShellKnownDataCommands = map[string]bool{"[": true, ":": true, "echo": true, "false": true, "printf": true, "test": true, "true": true}
var m2ShellSpecialBuiltins = map[string]bool{
	".": true, ":": true, "break": true, "continue": true, "eval": true,
	"exec": true, "exit": true, "export": true, "readonly": true, "return": true,
	"set": true, "shift": true, "trap": true, "unset": true,
}
var m2ShellStatefulBuiltins = map[string]bool{
	".": true, "declare": true, "eval": true, "export": true, "local": true,
	"readonly": true, "source": true, "typeset": true, "unset": true,
}

type m2ShellTimeMode uint8

const (
	m2ShellTimeAmbiguous m2ShellTimeMode = iota
	m2ShellTimeReserved
	m2ShellTimeExternal
)

type m2ShellIdentity struct {
	timeMode   m2ShellTimeMode
	prefixMode m2ShellPrefixMode
	detail     string
}

type m2ShellPrefixMode uint8

const (
	m2ShellPrefixAmbiguous m2ShellPrefixMode = iota
	m2ShellPrefixDiscard
	m2ShellPrefixPersist
)

type m2ShellSetModeAction uint8

const (
	m2ShellSetModeNone m2ShellSetModeAction = iota
	m2ShellSetModeEnable
	m2ShellSetModeDisable
	m2ShellSetModeAmbiguous
)

type m2ShellSetModeParser struct {
	optionsEnded bool
	expectMode   m2ShellSetModeAction
}

type m2ShellCommandParseKind uint8

const (
	m2ShellCommandOption m2ShellCommandParseKind = iota
	m2ShellCommandTarget
	m2ShellCommandQueryTarget
)

type m2ShellCommandParseResult struct {
	kind      m2ShellCommandParseKind
	ambiguous bool
	quoted    bool
	expanded  bool
}

type m2ShellCommandParser struct {
	options   bool
	query     bool
	ambiguous bool
}

func (parser *m2ShellCommandParser) consumeWord(word string, resolved, quoted, expanded bool) m2ShellCommandParseResult {
	if !resolved {
		parser.options = false
		parser.ambiguous = true
		kind := m2ShellCommandTarget
		if parser.query {
			kind = m2ShellCommandQueryTarget
		}
		return m2ShellCommandParseResult{kind: kind, ambiguous: true, quoted: quoted, expanded: expanded}
	}
	if parser.options {
		if word == "--" {
			parser.options = false
			return m2ShellCommandParseResult{kind: m2ShellCommandOption, quoted: quoted, expanded: expanded}
		}
		if strings.HasPrefix(word, "-") {
			options := strings.TrimPrefix(word, "-")
			if options == "" || strings.Trim(options, "pVv") != "" {
				parser.ambiguous = true
			} else if strings.ContainsAny(options, "Vv") {
				parser.query = true
			}
			return m2ShellCommandParseResult{kind: m2ShellCommandOption, quoted: quoted, expanded: expanded}
		}
		parser.options = false
	}
	if parser.query {
		return m2ShellCommandParseResult{kind: m2ShellCommandQueryTarget, ambiguous: parser.ambiguous, quoted: quoted, expanded: expanded}
	}
	return m2ShellCommandParseResult{kind: m2ShellCommandTarget, ambiguous: parser.ambiguous, quoted: quoted, expanded: expanded}
}

var m2ShellSafeUnquotedCommandAtom = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

var m2ShellKnownSetOptions = map[string]bool{
	"allexport": true, "braceexpand": true, "emacs": true, "errexit": true,
	"errtrace": true, "functrace": true, "hashall": true, "histexpand": true,
	"history": true, "ignoreeof": true, "interactive-comments": true, "keyword": true,
	"monitor": true, "noclobber": true, "noexec": true, "noglob": true,
	"nolog": true, "notify": true, "nounset": true, "onecmd": true,
	"physical": true, "pipefail": true, "privileged": true, "verbose": true,
	"vi": true, "xtrace": true,
}

func (parser *m2ShellSetModeParser) consume(token string) m2ShellSetModeAction {
	if parser.optionsEnded {
		return m2ShellSetModeNone
	}
	word, resolved := m2StaticShellWord(token)
	if !resolved {
		parser.optionsEnded = true
		parser.expectMode = m2ShellSetModeNone
		return m2ShellSetModeAmbiguous
	}
	if parser.expectMode != m2ShellSetModeNone {
		action := parser.expectMode
		parser.expectMode = m2ShellSetModeNone
		if word == "posix" {
			return action
		}
		if m2ShellKnownSetOptions[word] {
			return m2ShellSetModeNone
		}
		parser.optionsEnded = true
		return m2ShellSetModeAmbiguous
	}
	switch word {
	case "--", "-", "+":
		parser.optionsEnded = true
		return m2ShellSetModeNone
	case "-o":
		parser.expectMode = m2ShellSetModeEnable
		return m2ShellSetModeNone
	case "+o":
		parser.expectMode = m2ShellSetModeDisable
		return m2ShellSetModeNone
	}
	if !strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "+") {
		parser.optionsEnded = true
		return m2ShellSetModeNone
	}
	compact := word[1:]
	if compact == "" {
		parser.optionsEnded = true
		return m2ShellSetModeNone
	}
	const ordinaryOptions = "abefhkmnptuvxBCEHPT"
	for index, option := range compact {
		if option == 'o' {
			if index != len(compact)-1 {
				parser.optionsEnded = true
				return m2ShellSetModeAmbiguous
			}
			if word[0] == '-' {
				parser.expectMode = m2ShellSetModeEnable
			} else {
				parser.expectMode = m2ShellSetModeDisable
			}
			return m2ShellSetModeNone
		}
		if !strings.ContainsRune(ordinaryOptions, option) {
			parser.optionsEnded = true
			return m2ShellSetModeAmbiguous
		}
	}
	return m2ShellSetModeNone
}

const m2ShellShebangMaxBytes = 4096

func m2ShellIdentityFromSource(source string) m2ShellIdentity {
	ambiguous := func(detail string) m2ShellIdentity {
		return m2ShellIdentity{timeMode: m2ShellTimeAmbiguous, prefixMode: m2ShellPrefixAmbiguous, detail: detail}
	}
	if !strings.HasPrefix(source, "#!") {
		return ambiguous("missing-shebang")
	}
	lineEnd := strings.IndexByte(source, '\n')
	if lineEnd < 0 {
		lineEnd = len(source)
	}
	if lineEnd > m2ShellShebangMaxBytes {
		return ambiguous("shebang-budget")
	}
	line := strings.TrimSpace(strings.TrimSuffix(source[2:lineEnd], "\r"))
	if line == "" || strings.IndexByte(line, 0) >= 0 {
		return ambiguous("malformed-shebang")
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || len(fields) > 32 {
		return ambiguous("shebang-field-budget")
	}
	for _, field := range fields {
		if strings.ContainsAny(field, `"'\\$`+"`") {
			return ambiguous("dynamic-or-quoted-shebang")
		}
	}
	interpreter := fields[0]
	interpreterArguments := fields[1:]
	viaEnv := false
	posixEnvironment := false
	if interpreter == "/usr/bin/env" || interpreter == "/bin/env" {
		viaEnv = true
		var detail string
		interpreter, interpreterArguments, posixEnvironment, detail = m2ShellEnvInterpreter(fields[1:])
		if detail != "" {
			return ambiguous(detail)
		}
	}
	switch interpreter {
	case "/bin/bash", "/usr/bin/bash":
		return m2ShellIdentity{timeMode: m2ShellTimeReserved, prefixMode: m2ShellBashPrefixMode(interpreterArguments, posixEnvironment), detail: "bash"}
	case "/bin/dash", "/usr/bin/dash":
		return m2ShellIdentity{timeMode: m2ShellTimeExternal, prefixMode: m2ShellPrefixPersist, detail: "dash"}
	case "/bin/sh", "/usr/bin/sh":
		return ambiguous("generic-sh")
	case "bash":
		if viaEnv {
			return m2ShellIdentity{timeMode: m2ShellTimeReserved, prefixMode: m2ShellBashPrefixMode(interpreterArguments, posixEnvironment), detail: "bash"}
		}
	case "dash":
		if viaEnv {
			return m2ShellIdentity{timeMode: m2ShellTimeExternal, prefixMode: m2ShellPrefixPersist, detail: "dash"}
		}
	case "sh":
		if viaEnv {
			return ambiguous("generic-sh")
		}
	default:
	}
	return ambiguous("unknown-interpreter")
}

func m2ShellEnvInterpreter(arguments []string) (string, []string, bool, string) {
	posixEnvironment := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "-S" || argument == "--split-string" || argument == "--" ||
			argument == "-i" || argument == "--ignore-environment":
			continue
		case argument == "-u" || argument == "--unset" || argument == "-C" || argument == "--chdir":
			index++
			if index >= len(arguments) {
				return "", nil, false, "missing-env-option-argument"
			}
			continue
		case strings.HasPrefix(argument, "--unset=") || strings.HasPrefix(argument, "--chdir="):
			continue
		case m2ShellAssignment.MatchString(argument):
			if strings.HasPrefix(argument, "POSIXLY_CORRECT=") {
				posixEnvironment = true
			}
			continue
		case strings.HasPrefix(argument, "-"):
			return "", nil, false, "unsupported-env-option"
		default:
			return argument, arguments[index+1:], posixEnvironment, ""
		}
	}
	return "", nil, false, "missing-env-interpreter"
}

func m2ShellBashPrefixMode(arguments []string, posixEnvironment bool) m2ShellPrefixMode {
	mode := m2ShellPrefixDiscard
	if posixEnvironment {
		mode = m2ShellPrefixPersist
	}
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--posix":
			mode = m2ShellPrefixPersist
		case "-o", "+o":
			enable := arguments[index] == "-o"
			index++
			if index >= len(arguments) || arguments[index] != "posix" {
				return m2ShellPrefixAmbiguous
			}
			if enable {
				mode = m2ShellPrefixPersist
			} else {
				mode = m2ShellPrefixDiscard
			}
		case "--", "--noprofile", "--norc":
		case "":
		default:
			argument := arguments[index]
			if strings.HasPrefix(argument, "--") ||
				(strings.HasPrefix(argument, "-") || strings.HasPrefix(argument, "+")) &&
					strings.Trim(argument[1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
				return m2ShellPrefixAmbiguous
			}
		}
	}
	return mode
}

type m2ShellHeredoc struct {
	delimiter string
	stripTabs bool
}

// m2ShellWithoutHeredocBodies preserves command bytes but replaces heredoc
// bodies and delimiter lines with newlines. This keeps executable syntax on
// both sides ordered while preventing data from entering either the lexical
// detector or command-state evaluator.
func m2ShellWithoutHeredocBodies(source string) (string, string) {
	lines := strings.SplitAfter(source, "\n")
	var sanitized strings.Builder
	sanitized.Grow(len(source))
	var pending []m2ShellHeredoc
	var commandQuote byte
	for _, line := range lines {
		if len(pending) != 0 {
			candidate := strings.TrimSuffix(line, "\n")
			if pending[0].stripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == pending[0].delimiter {
				pending = pending[1:]
			}
			if strings.HasSuffix(line, "\n") {
				sanitized.WriteByte('\n')
			}
			continue
		}
		hasHeredoc, nextQuote, lexicalAmbiguity := m2ShellLineHeredocState(line, commandQuote)
		if lexicalAmbiguity != "" {
			return "", lexicalAmbiguity
		}
		startedQuoted := commandQuote != 0
		commandQuote = nextQuote
		if !hasHeredoc {
			sanitized.WriteString(line)
			continue
		}
		if startedQuoted || commandQuote != 0 {
			return "", "heredoc-with-multiline-quote"
		}
		documents, ambiguity := m2ShellHeredocsOnCommandLine(line)
		if ambiguity != "" {
			return "", ambiguity
		}
		sanitized.WriteString(line)
		pending = append(pending, documents...)
	}
	if len(pending) != 0 {
		return "", "unterminated-heredoc:" + pending[0].delimiter
	}
	return sanitized.String(), ""
}

func m2ShellLineHeredocState(line string, quote byte) (bool, byte, string) {
	hasHeredoc := false
	comment := false
	wordStart := quote == 0
	for index := 0; index < len(line); index++ {
		current := line[index]
		if comment {
			if current == '\n' {
				comment = false
				wordStart = true
			}
			continue
		}
		if current == '\\' && quote != '\'' {
			if index+1 >= len(line) {
				return false, quote, "unterminated-heredoc-scan-escape"
			}
			index++
			wordStart = false
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			wordStart = false
			continue
		}
		if current == '#' && wordStart {
			comment = true
			continue
		}
		if current == '<' && index+1 < len(line) && line[index+1] == '<' {
			hasHeredoc = true
		}
		if current == ' ' || current == '\t' || current == '\n' || strings.ContainsRune(";(){}|&<>", rune(current)) {
			wordStart = true
		} else {
			wordStart = false
		}
	}
	return hasHeredoc, quote, ""
}

func m2ShellHeredocsOnCommandLine(line string) ([]m2ShellHeredoc, string) {
	statements, ambiguity := m2ShellStatements(line)
	if ambiguity != "" {
		return nil, ambiguity
	}
	var documents []m2ShellHeredoc
	for _, statement := range statements {
		tokens, tokenAmbiguity := m2ShellTokens(statement)
		if tokenAmbiguity != "" {
			return nil, tokenAmbiguity
		}
		for index, token := range tokens {
			if token != "<<" && token != "<<-" {
				continue
			}
			if index+1 >= len(tokens) || tokens[index+1] == "\n" {
				return nil, "missing-heredoc-delimiter"
			}
			delimiter, resolved := m2StaticShellWord(tokens[index+1])
			if !resolved || delimiter == "" {
				return nil, "dynamic-heredoc-delimiter"
			}
			if len(delimiter) > 1024 || strings.ContainsAny(delimiter, "\r\n") {
				return nil, "heredoc-delimiter-budget"
			}
			documents = append(documents, m2ShellHeredoc{delimiter: delimiter, stripTabs: token == "<<-"})
		}
	}
	return documents, ""
}

func m2ShellResolvedExecutables(source string, shellIdentity m2ShellIdentity) ([]string, string) {
	type shellValue struct {
		resolved    bool
		commandSafe bool
		value       string
		conditional bool
	}
	type shellFunctionOp struct {
		call           string
		effects        map[string]shellValue
		specialEffects map[string]shellValue
		prefixMode     m2ShellPrefixMode
		hasPrefixMode  bool
		conditional    bool
	}
	type shellScope struct {
		kind                byte
		parent              map[string]shellValue
		isolated            bool
		functionName        string
		functionOps         []shellFunctionOp
		functionConditional bool
		functionOpaque      bool
		functionReachable   bool
		functionControlBase int
		parentPrefixMode    m2ShellPrefixMode
	}
	type shellFunction struct {
		ops    []shellFunctionOp
		opaque bool
	}
	type shellFunctionSummary struct {
		effects    map[string]shellValue
		prefixMode m2ShellPrefixMode
	}
	var executables []string
	active := map[string]shellValue{}
	prefixMode := shellIdentity.prefixMode
	functions := map[string]shellFunction{}
	cloneValues := func(source map[string]shellValue) map[string]shellValue {
		clone := make(map[string]shellValue, len(source))
		for name, value := range source {
			clone[name] = value
		}
		return clone
	}
	joinValue := func(prior shellValue, priorExists bool, effect shellValue) shellValue {
		if priorExists && prior.resolved && effect.resolved && prior.value == effect.value && prior.commandSafe == effect.commandSafe {
			return prior
		}
		return shellValue{}
	}
	joinPrefixMode := func(left, right m2ShellPrefixMode) m2ShellPrefixMode {
		if left == right {
			return left
		}
		return m2ShellPrefixAmbiguous
	}
	statements, ambiguity := m2ShellStatements(source)
	if ambiguity != "" {
		return nil, ambiguity
	}
	for _, statement := range statements {
		tokens, tokenAmbiguity := m2ShellTokens(statement)
		if tokenAmbiguity != "" {
			return nil, tokenAmbiguity
		}
		tokens = m2ShellFunctionTokens(tokens)
		tokens, tokenAmbiguity = m2ShellExecutableCaseTokens(tokens)
		if tokenAmbiguity != "" {
			return nil, tokenAmbiguity
		}
		isolatedBraces := map[int]bool{}
		var braceOpenings []int
		for index, token := range tokens {
			switch {
			case token == "{" || strings.HasPrefix(token, "function{:"):
				braceOpenings = append(braceOpenings, index)
			case token == "}":
				if len(braceOpenings) == 0 {
					return nil, "unmatched-group-close"
				}
				opening := braceOpenings[len(braceOpenings)-1]
				braceOpenings = braceOpenings[:len(braceOpenings)-1]
				operator, tailAmbiguity := m2ShellCompoundTailOperator(tokens, index+1)
				if tailAmbiguity != "" {
					return nil, tailAmbiguity
				}
				isolatedBraces[opening] = operator == "|" || operator == "&"
			}
		}
		if len(braceOpenings) != 0 {
			return nil, "unterminated-group"
		}
		commandPosition := true
		skipRedirectionTarget := false
		indirectCommand := ""
		dispatchCapable := false
		dispatchMode := ""
		dispatchOptionArgument := false
		commandBuiltinDispatch := false
		commandParser := m2ShellCommandParser{}
		setModeParser := m2ShellSetModeParser{}
		ambiguousTimeLookup := false
		expansionValues := cloneValues(active)
		pendingAssignments := map[string]shellValue{}
		commandStarted := false
		inPipeline := false
		commandConditional := false
		controlDepth := 0
		var scopes []shellScope
		pendingFunctionEffects := map[string]shellValue{}
		pendingFunctionJoin := false
		pendingFunctionReturn := false
		pendingPrefixMode := prefixMode
		pendingPrefixModeSet := false
		pendingPrefixModeUnknown := false
		recordFunctionEffects := func(effects map[string]shellValue) {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					if !scopes[index].functionReachable {
						return
					}
					operation := shellFunctionOp{
						effects:     cloneValues(effects),
						conditional: scopes[index].functionConditional || commandConditional || controlDepth > scopes[index].functionControlBase,
					}
					scopes[index].functionOps = append(scopes[index].functionOps, operation)
					return
				}
				if scopes[index].isolated {
					return
				}
			}
		}
		recordFunctionSpecialEffects := func(effects map[string]shellValue) {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					if !scopes[index].functionReachable {
						return
					}
					scopes[index].functionOps = append(scopes[index].functionOps, shellFunctionOp{
						specialEffects: cloneValues(effects),
						conditional:    scopes[index].functionConditional || commandConditional || controlDepth > scopes[index].functionControlBase,
					})
					return
				}
				if scopes[index].isolated {
					return
				}
			}
		}
		recordFunctionPrefixMode := func(mode m2ShellPrefixMode, conditional bool) {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					if !scopes[index].functionReachable {
						return
					}
					scopes[index].functionOps = append(scopes[index].functionOps, shellFunctionOp{
						prefixMode: mode, hasPrefixMode: true,
						conditional: conditional || scopes[index].functionConditional || commandConditional || controlDepth > scopes[index].functionControlBase,
					})
					return
				}
				if scopes[index].isolated {
					return
				}
			}
		}
		markFunctionOpaque := func() {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					scopes[index].functionOpaque = true
					return
				}
			}
		}
		recordFunctionCall := func(name string, conditional bool) {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					if !scopes[index].functionReachable {
						return
					}
					scopes[index].functionOps = append(scopes[index].functionOps, shellFunctionOp{
						call:        name,
						conditional: conditional || scopes[index].functionConditional || commandConditional || controlDepth > scopes[index].functionControlBase,
					})
					return
				}
			}
		}
		applySpecialBuiltinPrefixes := func() {
			if len(pendingAssignments) == 0 {
				return
			}
			recordFunctionSpecialEffects(pendingAssignments)
			switch prefixMode {
			case m2ShellPrefixDiscard:
				return
			case m2ShellPrefixPersist:
				for name, value := range pendingAssignments {
					active[name] = value
				}
			case m2ShellPrefixAmbiguous:
				for name, value := range pendingAssignments {
					prior, exists := active[name]
					active[name] = joinValue(prior, exists, value)
				}
			}
		}
		insideFunction := func() bool {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					return true
				}
			}
			return false
		}
		markFunctionReturn := func(conditional bool) {
			for index := len(scopes) - 1; index >= 0; index-- {
				if scopes[index].functionName != "" {
					if conditional {
						scopes[index].functionConditional = true
					} else {
						scopes[index].functionReachable = false
					}
					return
				}
				if scopes[index].isolated {
					return
				}
			}
		}
		queuePrefixMode := func(mode m2ShellPrefixMode, unknown bool) {
			if pendingPrefixModeUnknown {
				return
			}
			pendingPrefixMode = mode
			pendingPrefixModeSet = true
			pendingPrefixModeUnknown = unknown
		}
		queueSetPosixMode := func(enable bool) {
			switch shellIdentity.timeMode {
			case m2ShellTimeReserved:
				mode := m2ShellPrefixDiscard
				if enable {
					mode = m2ShellPrefixPersist
				}
				queuePrefixMode(mode, false)
			case m2ShellTimeExternal:
				// Dash rejects this Bash option before subsequent commands;
				// retaining its persistent mode is conservative.
			case m2ShellTimeAmbiguous:
				queuePrefixMode(m2ShellPrefixAmbiguous, true)
			}
		}
		var functionEffects func(string, map[string]bool, m2ShellPrefixMode) (shellFunctionSummary, string)
		functionEffects = func(name string, stack map[string]bool, initialPrefixMode m2ShellPrefixMode) (shellFunctionSummary, string) {
			if stack[name] {
				return shellFunctionSummary{}, "recursive-function-call:" + name
			}
			function, exists := functions[name]
			if !exists {
				return shellFunctionSummary{effects: map[string]shellValue{}, prefixMode: initialPrefixMode}, ""
			}
			if function.opaque {
				return shellFunctionSummary{}, "ambiguous-function-effects:" + name
			}
			stack[name] = true
			defer delete(stack, name)
			effects := map[string]shellValue{}
			resultPrefixMode := initialPrefixMode
			applyEffects := func(operationEffects map[string]shellValue, operationConditional bool) {
				for variable, value := range operationEffects {
					value.conditional = value.conditional || operationConditional
					if value.conditional {
						if prior, exists := effects[variable]; exists {
							value = joinValue(prior, true, value)
						}
					}
					effects[variable] = value
				}
			}
			for _, operation := range function.ops {
				if operation.call != "" {
					called, callAmbiguity := functionEffects(operation.call, stack, resultPrefixMode)
					if callAmbiguity != "" {
						return shellFunctionSummary{}, callAmbiguity
					}
					applyEffects(called.effects, operation.conditional)
					if operation.conditional {
						resultPrefixMode = joinPrefixMode(resultPrefixMode, called.prefixMode)
					} else {
						resultPrefixMode = called.prefixMode
					}
					continue
				}
				if operation.specialEffects != nil {
					switch resultPrefixMode {
					case m2ShellPrefixDiscard:
					case m2ShellPrefixPersist:
						applyEffects(operation.specialEffects, operation.conditional)
					case m2ShellPrefixAmbiguous:
						applyEffects(operation.specialEffects, true)
					}
					continue
				}
				if operation.hasPrefixMode {
					if operation.conditional {
						resultPrefixMode = joinPrefixMode(resultPrefixMode, operation.prefixMode)
					} else {
						resultPrefixMode = operation.prefixMode
					}
					continue
				}
				applyEffects(operation.effects, operation.conditional)
			}
			return shellFunctionSummary{effects: effects, prefixMode: resultPrefixMode}, ""
		}
		finishSimpleCommand := func(persistAssignments bool) {
			if persistAssignments && !commandStarted {
				for name, value := range pendingAssignments {
					active[name] = value
				}
				recordFunctionEffects(pendingAssignments)
			}
			if commandStarted && len(pendingFunctionEffects) != 0 {
				applied := cloneValues(pendingFunctionEffects)
				if pendingFunctionJoin || !persistAssignments {
					for name, value := range applied {
						prior, exists := active[name]
						applied[name] = joinValue(prior, exists, value)
					}
				}
				for name, value := range applied {
					active[name] = value
				}
				recordFunctionEffects(applied)
			}
			if pendingFunctionReturn {
				markFunctionReturn(commandConditional || pendingFunctionJoin || !persistAssignments)
			}
			if pendingPrefixModeSet && persistAssignments {
				conditional := commandConditional || pendingFunctionJoin
				if conditional {
					prefixMode = joinPrefixMode(prefixMode, pendingPrefixMode)
				} else {
					prefixMode = pendingPrefixMode
				}
				recordFunctionPrefixMode(pendingPrefixMode, conditional)
			}
			expansionValues = cloneValues(active)
			pendingAssignments = map[string]shellValue{}
			pendingFunctionEffects = map[string]shellValue{}
			pendingFunctionJoin = false
			pendingFunctionReturn = false
			pendingPrefixMode = prefixMode
			pendingPrefixModeSet = false
			pendingPrefixModeUnknown = false
			commandStarted = false
		}
		for tokenIndex, token := range tokens {
			if skipRedirectionTarget {
				skipRedirectionTarget = false
				continue
			}
			structuralToken := token
			if strings.HasPrefix(token, "function{:") {
				structuralToken = "function{"
			}
			if commandPosition {
				switch structuralToken {
				case "if", "while", "until", "for", "case-control":
					controlDepth++
				case "fi", "done", "case-end":
					if controlDepth == 0 {
						return nil, "unmatched-shell-control:" + structuralToken
					}
					controlDepth--
				}
			}
			switch structuralToken {
			case ";", "\n":
				finishSimpleCommand(!inPipeline)
				inPipeline = false
				commandConditional = controlDepth > 0
				commandPosition = true
				indirectCommand = ""
				dispatchCapable = false
				dispatchMode = ""
				dispatchOptionArgument = false
				commandBuiltinDispatch = false
				commandParser = m2ShellCommandParser{}
				ambiguousTimeLookup = false
				continue
			case "&&", "||":
				pendingFunctionJoin = true
				finishSimpleCommand(!inPipeline)
				inPipeline = false
				commandConditional = true
				commandPosition = true
				indirectCommand = ""
				dispatchCapable = false
				dispatchMode = ""
				dispatchOptionArgument = false
				commandBuiltinDispatch = false
				commandParser = m2ShellCommandParser{}
				ambiguousTimeLookup = false
				continue
			case "|":
				pendingFunctionJoin = true
				finishSimpleCommand(false)
				inPipeline = true
				commandConditional = true
				commandPosition = true
				indirectCommand = ""
				dispatchCapable = false
				dispatchMode = ""
				dispatchOptionArgument = false
				commandBuiltinDispatch = false
				commandParser = m2ShellCommandParser{}
				ambiguousTimeLookup = false
				continue
			case "&":
				pendingFunctionJoin = true
				finishSimpleCommand(false)
				inPipeline = false
				commandConditional = controlDepth > 0
				commandPosition = true
				indirectCommand = ""
				dispatchCapable = false
				dispatchMode = ""
				dispatchOptionArgument = false
				commandBuiltinDispatch = false
				commandParser = m2ShellCommandParser{}
				ambiguousTimeLookup = false
				continue
			case "(":
				if commandStarted || len(pendingAssignments) != 0 {
					return nil, "unsupported-subshell-boundary"
				}
				scopes = append(scopes, shellScope{kind: '(', parent: active, isolated: true, parentPrefixMode: prefixMode})
				active = cloneValues(active)
				expansionValues = cloneValues(active)
				continue
			case ")":
				finishSimpleCommand(!inPipeline)
				if len(scopes) == 0 || scopes[len(scopes)-1].kind != '(' {
					return nil, "unmatched-subshell-close"
				}
				active = scopes[len(scopes)-1].parent
				prefixMode = scopes[len(scopes)-1].parentPrefixMode
				scopes = scopes[:len(scopes)-1]
				expansionValues = cloneValues(active)
				inPipeline = false
				commandStarted = true
				commandPosition = false
				continue
			case "{", "function{":
				if commandStarted || len(pendingAssignments) != 0 {
					return nil, "unsupported-group-boundary"
				}
				functionName := strings.TrimPrefix(token, "function{:")
				if structuralToken != "function{" {
					functionName = ""
				}
				isolated := functionName != "" || isolatedBraces[tokenIndex]
				scopes = append(scopes, shellScope{
					kind: '{', parent: active, isolated: isolated, functionName: functionName, functionReachable: true,
					functionControlBase: controlDepth, parentPrefixMode: prefixMode,
				})
				active = cloneValues(active)
				if functionName != "" {
					prefixMode = m2ShellPrefixAmbiguous
				}
				expansionValues = cloneValues(active)
				continue
			case "}":
				finishSimpleCommand(!inPipeline)
				if len(scopes) == 0 || scopes[len(scopes)-1].kind != '{' {
					return nil, "unmatched-group-close"
				}
				scope := scopes[len(scopes)-1]
				scopes = scopes[:len(scopes)-1]
				if scope.functionName != "" {
					functions[scope.functionName] = shellFunction{
						ops: append([]shellFunctionOp(nil), scope.functionOps...), opaque: scope.functionOpaque,
					}
				}
				if !scope.isolated {
					for name, value := range active {
						scope.parent[name] = value
					}
				}
				active = scope.parent
				if scope.isolated {
					prefixMode = scope.parentPrefixMode
				}
				expansionValues = cloneValues(active)
				inPipeline = false
				commandStarted = true
				commandPosition = false
				continue
			case "<", ">", "<<", "<<-", ">>", "<&", ">&", "&>", "&>>":
				skipRedirectionTarget = true
				continue
			case "<>", ">|":
				return nil, "unsupported-redirection:" + token
			}
			commandWrapperTarget := false
			commandWrapperAmbiguous := false
			commandWrapperResolved := true
			commandWrapperWord := ""
			if commandPosition && dispatchMode == "command" {
				word, resolved := m2StaticShellWord(token)
				expanded := false
				quoted := false
				expansionUnsafe := false
				expansionName := ""
				if !resolved {
					if command := m2ShellCommandVariable.FindStringSubmatch(token); len(command) == 3 {
						expansionName = command[1]
						if expansionName == "" {
							expansionName = command[2]
						}
						expanded = true
						quoted = len(token) >= 2 && token[0] == '"' && token[len(token)-1] == '"'
						if value, exists := expansionValues[expansionName]; exists && value.resolved {
							_, ifsChanged := expansionValues["IFS"]
							if quoted || !ifsChanged && m2ShellSafeUnquotedCommandAtom.MatchString(value.value) {
								word, resolved = value.value, true
							} else {
								expansionUnsafe = true
							}
						} else {
							expansionUnsafe = true
						}
					}
				}
				parsed := commandParser.consumeWord(word, resolved, quoted, expanded)
				switch parsed.kind {
				case m2ShellCommandOption:
					continue
				case m2ShellCommandQueryTarget:
					commandStarted = true
					indirectCommand = "command-query"
					commandPosition = false
					continue
				case m2ShellCommandTarget:
					if parsed.expanded && expansionUnsafe {
						context := "unquoted"
						if parsed.quoted {
							context = "quoted"
						}
						return nil, "ambiguous-command-expansion:" + context + ":" + expansionName
					}
					commandWrapperTarget = true
					commandWrapperAmbiguous = parsed.ambiguous
					commandWrapperResolved = resolved
					commandWrapperWord = word
				}
			}
			if dispatchCapable && !commandWrapperTarget {
				if command := m2ShellCommandVariable.FindStringSubmatch(token); len(command) == 3 {
					name := command[1]
					if name == "" {
						name = command[2]
					}
					if value, exists := expansionValues[name]; exists {
						if !value.resolved {
							return nil, "unresolved-dispatch-assignment:" + name
						}
						executables = append(executables, value.value)
						continue
					}
				} else if name := m2TrackedVariableInWord(token, expansionValues); name != "" {
					return nil, "unsupported-dispatch-word:" + name
				}
			}
			if commandPosition && !commandWrapperTarget {
				switch token {
				case "!", "if", "then", "elif", "else", "while", "until", "for", "do", "case-control":
					indirectCommand = ""
					continue
				case "fi", "done", "case-end":
					commandPosition = true
					continue
				}
				if dispatchOptionArgument {
					dispatchOptionArgument = false
					continue
				}
				if dispatchMode != "" && (token == "--" || strings.HasPrefix(token, "-")) {
					if (dispatchMode == "env" && (token == "-u" || token == "--unset")) ||
						(dispatchMode == "nice" && (token == "-n" || token == "--adjustment")) {
						dispatchOptionArgument = true
					}
					continue
				}
			}
			if commandWrapperTarget && !commandWrapperResolved {
				queuePrefixMode(m2ShellPrefixAmbiguous, true)
			}
			if !commandPosition {
				if indirectCommand == "set-mode" {
					switch setModeParser.consume(token) {
					case m2ShellSetModeEnable:
						queueSetPosixMode(true)
					case m2ShellSetModeDisable:
						queueSetPosixMode(false)
					case m2ShellSetModeAmbiguous:
						queuePrefixMode(m2ShellPrefixAmbiguous, true)
					}
					continue
				}
				if indirectCommand == "stateful-read" {
					if strings.HasPrefix(token, "-") {
						continue
					}
					name, resolved := m2StaticShellWord(token)
					if !resolved || !m2ShellName.MatchString(name) {
						markFunctionOpaque()
						continue
					}
					pendingFunctionEffects[name] = shellValue{}
					continue
				}
				if indirectCommand == "interpreter" {
					if strings.HasPrefix(token, "-") {
						if token != "--" && strings.Contains(strings.TrimLeft(token, "-"), "c") {
							indirectCommand = "command-text"
						}
						continue
					}
					indirectCommand = ""
					continue
				}
				if indirectCommand != "" {
					if name := m2TrackedVariableInWord(token, expansionValues); name != "" {
						return nil, "unsupported-" + indirectCommand + ":" + name
					}
					if indirectCommand == "command-text" {
						indirectCommand = ""
					}
				}
				continue
			}
			if assignment := m2ShellAssignment.FindStringSubmatch(token); !commandWrapperTarget && len(assignment) == 3 {
				value, resolved := m2StaticShellWord(assignment[2])
				if !commandStarted {
					pendingAssignments[assignment[1]] = shellValue{
						resolved:    resolved,
						commandSafe: !resolved && m2StaticShellCommandBasenameSafe(assignment[2]),
						value:       value,
					}
				}
				continue
			}
			commandName, pathSpelled, resolved := m2StaticShellCommandName(token)
			if commandWrapperTarget && commandWrapperResolved {
				commandName = filepath.Base(commandWrapperWord)
				pathSpelled = strings.Contains(commandWrapperWord, "/")
				resolved = commandWrapperWord != ""
			}
			if resolved {
				executables = append(executables, commandName)
				if commandName == "time" && token == "time" && !dispatchCapable {
					commandStarted = true
					dispatchMode = "reserved-time"
					switch shellIdentity.timeMode {
					case m2ShellTimeReserved:
					case m2ShellTimeExternal:
						dispatchCapable = true
					case m2ShellTimeAmbiguous:
						ambiguousTimeLookup = true
					}
					continue
				}
				if !pathSpelled && !dispatchCapable && m2ShellSpecialBuiltins[commandName] {
					applySpecialBuiltinPrefixes()
				}
				commandSet := commandName == "set" && !pathSpelled &&
					(!dispatchCapable || dispatchMode == "command" && commandBuiltinDispatch)
				if commandSet {
					if commandWrapperAmbiguous {
						pendingFunctionJoin = true
					}
					commandStarted = true
					indirectCommand = "set-mode"
					setModeParser = m2ShellSetModeParser{}
					dispatchCapable = false
					dispatchMode = ""
					commandBuiltinDispatch = false
					commandPosition = false
					continue
				}
				if _, exists := functions[commandName]; exists && !dispatchCapable {
					if insideFunction() {
						recordFunctionCall(commandName, ambiguousTimeLookup)
						commandStarted = true
						commandPosition = false
						continue
					}
					summary, effectAmbiguity := functionEffects(commandName, map[string]bool{}, prefixMode)
					if effectAmbiguity != "" {
						return nil, effectAmbiguity
					}
					for name, value := range summary.effects {
						if value.conditional {
							prior, exists := active[name]
							value = joinValue(prior, exists, value)
						}
						pendingFunctionEffects[name] = value
					}
					pendingFunctionJoin = pendingFunctionJoin || commandConditional || ambiguousTimeLookup
					pendingPrefixMode = summary.prefixMode
					pendingPrefixModeSet = true
					commandStarted = true
					commandPosition = false
					continue
				}
				if insideFunction() && commandName == "return" && !dispatchCapable {
					pendingFunctionReturn = true
					commandStarted = true
					commandPosition = false
					continue
				}
				if insideFunction() && !dispatchCapable && (commandName == "exit" || commandName == "break" || commandName == "continue") {
					pendingFunctionReturn = true
				}
				if insideFunction() && commandName == "read" {
					commandStarted = true
					indirectCommand = "stateful-read"
					commandPosition = false
					continue
				}
				if insideFunction() && m2ShellStatefulBuiltins[commandName] {
					markFunctionOpaque()
				}
				if insideFunction() && !dispatchCapable && !pathSpelled && !m2ShellKnownDataCommands[commandName] &&
					!m2ShellUtilityDispatch[commandName] && !m2ShellInterpreters[commandName] && !m2ShellIndirectDispatch[commandName] {
					// An unqualified external-looking word can resolve to a function
					// declared later. Resolve that relationship at invocation time.
					recordFunctionCall(commandName, false)
				}
				switch {
				case m2ShellUtilityDispatch[commandName]:
					priorDispatchCapable := dispatchCapable
					priorDispatchMode := dispatchMode
					priorCommandBuiltin := commandBuiltinDispatch
					commandStarted = true
					dispatchCapable = true
					dispatchMode = commandName
					commandBuiltinDispatch = commandName == "command" && !pathSpelled &&
						(!priorDispatchCapable || priorDispatchMode == "command" && priorCommandBuiltin)
					commandParser = m2ShellCommandParser{options: true, ambiguous: commandWrapperAmbiguous || ambiguousTimeLookup}
					continue
				case m2ShellInterpreters[commandName]:
					commandStarted = true
					dispatchCapable = true
					indirectCommand = "interpreter"
					commandPosition = false
					continue
				case m2ShellIndirectDispatch[commandName]:
					commandStarted = true
					dispatchCapable = true
					indirectCommand = commandName
				case pathSpelled && !m2ShellKnownDataCommands[commandName]:
					commandStarted = true
					indirectCommand = "unknown-path-dispatch"
				}
			}
			if !commandWrapperTarget || !commandWrapperResolved {
				if command := m2ShellCommandVariable.FindStringSubmatch(token); len(command) == 3 {
					name := command[1]
					if name == "" {
						name = command[2]
					}
					if value, exists := expansionValues[name]; exists {
						if !value.resolved {
							if value.commandSafe {
								commandPosition = false
								continue
							}
							return nil, "unresolved-command-assignment:" + name
						}
						if _, function := functions[value.value]; function {
							return nil, "indirect-function-call:" + value.value
						}
						executables = append(executables, value.value)
					}
				} else if name := m2TrackedVariableInWord(token, expansionValues); name != "" {
					return nil, "unsupported-command-word:" + name
				}
			}
			commandStarted = true
			dispatchMode = ""
			commandPosition = false
		}
		finishSimpleCommand(!inPipeline)
		if len(scopes) != 0 {
			if scopes[len(scopes)-1].kind == '(' {
				return nil, "unterminated-subshell"
			}
			return nil, "unterminated-group"
		}
	}
	sort.Strings(executables)
	return executables, ""
}

func m2ShellFunctionTokens(tokens []string) []string {
	normalized := make([]string, 0, len(tokens))
	for index := 0; index < len(tokens); index++ {
		if index+3 < len(tokens) && m2ShellName.MatchString(tokens[index]) && tokens[index+1] == "(" && tokens[index+2] == ")" {
			opening := index + 3
			for opening < len(tokens) && tokens[opening] == "\n" {
				opening++
			}
			if opening < len(tokens) && tokens[opening] == "{" {
				normalized = append(normalized, "function{:"+tokens[index])
				index = opening
				continue
			}
		}
		normalized = append(normalized, tokens[index])
	}
	return normalized
}

func m2ShellCompoundTailOperator(tokens []string, start int) (string, string) {
	for index := start; index < len(tokens); {
		fileDescriptor := strings.Trim(tokens[index], `"'`)
		if fileDescriptor != "" && strings.Trim(fileDescriptor, "0123456789") == "" && index+1 < len(tokens) && m2ShellRedirection(tokens[index+1]) {
			if len(fileDescriptor) > 9 {
				return "", "io-number-budget"
			}
			index++
		}
		if m2ShellUnsupportedRedirection(tokens[index]) {
			return "", "unsupported-compound-redirection:" + tokens[index]
		}
		if m2ShellRedirection(tokens[index]) {
			if index+1 >= len(tokens) || !m2ShellRedirectionTarget(tokens[index], tokens[index+1]) {
				return "", "invalid-compound-redirection:" + tokens[index]
			}
			index += 2
			continue
		}
		return tokens[index], ""
	}
	return "", ""
}

func m2ShellRedirection(token string) bool {
	return token == "<" || token == ">" || token == "<<" || token == "<<-" || token == ">>" ||
		token == "<&" || token == ">&" || token == "&>" || token == "&>>"
}

func m2ShellUnsupportedRedirection(token string) bool {
	return token == "<>" || token == ">|"
}

func m2ShellRedirectionTarget(operator, target string) bool {
	if target == "" || target == ";" || target == "\n" || target == "|" || target == "||" || target == "&" || target == "&&" ||
		m2ShellRedirection(target) || m2ShellUnsupportedRedirection(target) {
		return false
	}
	if operator != "<&" && operator != ">&" {
		return true
	}
	value, resolved := m2StaticShellWord(target)
	return resolved && (value == "-" || (value != "" && len(value) <= 9 && strings.Trim(value, "0123456789") == ""))
}

func m2ShellExecutableCaseTokens(tokens []string) ([]string, string) {
	type caseFrame struct{ phase string }
	var executable []string
	var stack []caseFrame
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if len(stack) == 0 {
			if token == "case" {
				executable = append(executable, "case-control")
				stack = append(stack, caseFrame{phase: "header"})
			} else {
				executable = append(executable, token)
			}
			continue
		}
		frame := &stack[len(stack)-1]
		switch frame.phase {
		case "header":
			if token == "in" {
				frame.phase = "pattern"
			}
		case "pattern":
			if token == "esac" {
				executable = append(executable, "case-end")
				stack = stack[:len(stack)-1]
			} else if token == ")" {
				frame.phase = "action"
			}
		case "action":
			switch token {
			case "case":
				executable = append(executable, "case-control")
				stack = append(stack, caseFrame{phase: "header"})
			case "esac":
				executable = append(executable, "case-end")
				stack = stack[:len(stack)-1]
			case ";;":
				executable = append(executable, ";")
				frame.phase = "pattern"
			default:
				executable = append(executable, token)
			}
		}
	}
	if len(stack) != 0 {
		return nil, "unterminated-case"
	}
	return executable, ""
}

func m2StaticShellCommandBasenameSafe(word string) bool {
	separator := strings.LastIndexByte(word, '/')
	if separator < 0 {
		return false
	}
	basename, resolved := m2StaticShellWord(strings.Trim(word[separator+1:], `"'`))
	return resolved && basename != "" && basename != m2LegacyCLI
}

func m2StaticShellCommandName(word string) (name string, pathSpelled bool, resolved bool) {
	value, resolved := m2StaticShellWord(word)
	if !resolved || value == "" {
		return "", false, false
	}
	return filepath.Base(value), strings.Contains(value, "/"), true
}

func m2TrackedVariableInWord[T any](word string, active map[string]T) string {
	word = strings.Trim(word, `"`)
	for name := range active {
		for _, reference := range []string{"$" + name, "${" + name + "}"} {
			if strings.HasPrefix(word, reference+"/") {
				suffix := strings.TrimPrefix(word, reference+"/")
				if !strings.ContainsAny(suffix, "$`*?[]~{}();|&<>") && filepath.Base(suffix) != m2LegacyCLI {
					continue
				}
			}
			if strings.Contains(word, reference) {
				return name
			}
		}
	}
	return ""
}

func m2StaticShellWord(word string) (string, bool) {
	var value strings.Builder
	var quote byte
	for index := 0; index < len(word); index++ {
		current := word[index]
		switch quote {
		case '\'':
			if current == '\'' {
				quote = 0
			} else {
				value.WriteByte(current)
			}
		case '"':
			switch current {
			case '"':
				quote = 0
			case '$', '`':
				return "", false
			case '\\':
				if index+1 >= len(word) {
					return "", false
				}
				index++
				next := word[index]
				if strings.ContainsRune(`$\"`, rune(next)) {
					value.WriteByte(next)
				} else {
					value.WriteByte('\\')
					value.WriteByte(next)
				}
			default:
				value.WriteByte(current)
			}
		default:
			switch current {
			case '\'', '"':
				quote = current
			case '\\':
				if index+1 >= len(word) {
					return "", false
				}
				index++
				value.WriteByte(word[index])
			default:
				if current == ' ' || current == '\t' || strings.ContainsRune("$`*?[]~{}();|&<>", rune(current)) {
					return "", false
				}
				value.WriteByte(current)
			}
		}
	}
	return value.String(), quote == 0
}

func m2ShellTokens(statement string) ([]string, string) {
	var tokens []string
	var word strings.Builder
	var quote byte
	flush := func() {
		if word.Len() != 0 {
			tokens = append(tokens, word.String())
			word.Reset()
		}
	}
	for index := 0; index < len(statement); index++ {
		current := statement[index]
		if current == '\\' && quote != '\'' {
			if index+1 >= len(statement) {
				return nil, "unterminated-token-escape"
			}
			word.WriteByte(current)
			index++
			word.WriteByte(statement[index])
			continue
		}
		if quote != 0 {
			word.WriteByte(current)
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			word.WriteByte(current)
			continue
		}
		if current == ' ' || current == '\t' || current == '\n' {
			flush()
			if current == '\n' {
				tokens = append(tokens, "\n")
			}
			continue
		}
		if current == '$' && index+1 < len(statement) && statement[index+1] == '(' {
			closing, ambiguity := m2ShellCommandSubstitutionEnd(statement, index+2)
			if ambiguity != "" {
				return nil, ambiguity
			}
			word.WriteString(statement[index : closing+1])
			index = closing
			continue
		}
		if current == '$' && index+1 < len(statement) && statement[index+1] == '{' {
			closing := strings.IndexByte(statement[index+2:], '}')
			if closing < 0 {
				return nil, "unterminated-parameter-expansion"
			}
			closing += index + 2
			word.WriteString(statement[index : closing+1])
			index = closing
			continue
		}
		if strings.ContainsRune(";!(){}<>|&", rune(current)) {
			flush()
			operator := string(current)
			if current == '&' && index+1 < len(statement) && statement[index+1] == '>' {
				operator = "&>"
				index++
				if index+1 < len(statement) && statement[index+1] == '>' {
					operator = "&>>"
					index++
				}
			} else if current == '<' && index+2 < len(statement) && statement[index+1] == '<' && statement[index+2] == '-' {
				operator = "<<-"
				index += 2
			} else if index+1 < len(statement) {
				next := statement[index+1]
				if (current == '&' && next == '&') || (current == '|' && next == '|') ||
					(current == '<' && next == '<') || (current == '>' && next == '>') ||
					(current == '<' && (next == '&' || next == '>')) ||
					(current == '>' && (next == '&' || next == '|')) ||
					(current == ';' && next == ';') {
					operator += string(next)
					index++
				}
			}
			tokens = append(tokens, operator)
			continue
		}
		word.WriteByte(current)
	}
	if quote != 0 {
		return nil, "unterminated-token-quote"
	}
	flush()
	return tokens, ""
}

func m2ShellCommandSubstitutionEnd(source string, start int) (int, string) {
	depth := 1
	var quote byte
	for index := start; index < len(source); index++ {
		current := source[index]
		if current == '\\' && quote != '\'' {
			if index+1 >= len(source) {
				return 0, "unterminated-command-substitution-escape"
			}
			index++
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, ""
			}
		}
	}
	return 0, "unterminated-command-substitution"
}

// m2ShellStatements preserves one ordered command stream, including explicit
// semicolon/newline boundaries, so compound scope can be classified before a
// pending assignment commits. The repository byte budgets bound this pass.
func m2ShellStatements(source string) ([]string, string) {
	statements := make([]string, 0, strings.Count(source, "\n")+strings.Count(source, ";")+1)
	var statement strings.Builder
	statement.Grow(len(source))
	var quote byte
	comment := false
	wordStart := true
	for index := 0; index < len(source); index++ {
		current := source[index]
		if comment {
			if current == '\n' {
				comment = false
				statement.WriteByte('\n')
				wordStart = true
			}
			continue
		}
		if current == '\\' && quote != '\'' {
			if index+1 >= len(source) {
				return nil, "unterminated-escape"
			}
			if source[index+1] == '\n' {
				index++
				continue
			}
			statement.WriteByte(current)
			index++
			statement.WriteByte(source[index])
			wordStart = false
			continue
		}
		if current == '`' && quote != '\'' {
			return nil, "unsupported-backtick-substitution"
		}
		if quote != 0 {
			statement.WriteByte(current)
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			statement.WriteByte(current)
			wordStart = false
			continue
		}
		if current == '$' && index+1 < len(source) && source[index+1] == '(' {
			closing, ambiguity := m2ShellCommandSubstitutionEnd(source, index+2)
			if ambiguity != "" {
				return nil, ambiguity
			}
			statement.WriteString(source[index : closing+1])
			index = closing
			wordStart = false
			continue
		}
		if current == '$' && index+1 < len(source) && source[index+1] == '{' {
			closing := strings.IndexByte(source[index+2:], '}')
			if closing < 0 {
				return nil, "unterminated-parameter-expansion"
			}
			closing += index + 2
			statement.WriteString(source[index : closing+1])
			index = closing
			wordStart = false
			continue
		}
		if current == '#' && wordStart {
			comment = true
			continue
		}
		if current == ';' || current == '\n' {
			if current == ';' {
				statement.WriteByte(';')
			} else {
				statement.WriteByte('\n')
			}
			wordStart = true
			continue
		}
		statement.WriteByte(current)
		if current == ' ' || current == '\t' || strings.ContainsRune("|&()<>", rune(current)) {
			wordStart = true
		} else {
			wordStart = false
		}
	}
	if quote == '\'' {
		return nil, "unterminated-single-quote"
	}
	if quote == '"' {
		return nil, "unterminated-double-quote"
	}
	statements = append(statements, statement.String())
	return statements, ""
}

func m2WithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func m2RetiredBlobDigest() string {
	keys := make([]string, 0, len(retiredSQLiteBlobs))
	for key := range retiredSQLiteBlobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var rows []string
	for _, key := range keys {
		rows = append(rows, key+"\x00"+retiredSQLiteBlobs[key])
	}
	digest := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(digest[:])
}

func formatM2Findings(findings []m2AbsenceFinding) string {
	parts := make([]string, len(findings))
	for index, finding := range findings {
		parts[index] = fmt.Sprintf("%s:%s:%s", finding.Kind, finding.Path, finding.Detail)
	}
	return strings.Join(parts, "\n")
}
