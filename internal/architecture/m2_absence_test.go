package architecture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	m2AbsenceMaxFiles = 50000
	m2AbsenceMaxBytes = 256 << 20
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
	"modernc.org/sqlite", "modernc.org/libc", "modernc.org/mathutil", "modernc.org/memory",
	"modernc.org/cc/v4", "modernc.org/ccgo/v4", "modernc.org/fileutil", "modernc.org/gc/v2",
	"modernc.org/gc/v3", "modernc.org/goabi0", "modernc.org/opt", "modernc.org/sortutil",
	"modernc.org/strutil", "modernc.org/token", "github.com/ncruces/go-strftime",
	"github.com/remyoudompheng/bigfft",
}

var m2ForbiddenImports = []string{
	"database/sql", "modernc.org/sqlite", "github.com/mattn/go-sqlite3",
	"github.com/glebarez/sqlite", "github.com/glebarez/go-sqlite",
	"github.com/ncruces/go-sqlite3", "zombiezen.com/go/sqlite", "crawshaw.io/sqlite",
	"gorm.io/driver/sqlite", "github.com/uptrace/bun/driver/sqlitedialect",
}

// m2HistoricalExemptions is deliberately closed and token-specific. These are
// immutable evidence, mutation controls, and the bounded header classifier
// that must continue to describe the retired implementation truthfully. Go
// imports are always parsed before this table is consulted, so an executable
// edge cannot be sheltered by a textual exemption.
var m2HistoricalExemptions = map[string]map[string]bool{
	"cmd/spec-kitty/main_test.go":                {"sqlite format 3": true, "--db": true, "--backend": true},
	"docs/phase1-evidence/closeout-results.json": {"modernc.org/sqlite": true},
	"docs/phase1-evidence/results.json":          {"modernc.org/sqlite": true},
	"internal/architecture/m1_crown_test.go":     {"modernc.org/sqlite": true, "sqlitefallback": true},
	"internal/architecture/m1_dependency_guard_test.go": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "zombiezen.com/go/sqlite": true,
		"github.com/glebarez/sqlite": true, "github.com/ncruces/go-sqlite3": true,
		"sqlitefallback": true, "fallbacktosqlite": true, "usesqlitefallback": true,
	},
	"internal/architecture/m2_dependency_guard_test.go": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "zombiezen.com/go/sqlite": true,
		"github.com/glebarez/sqlite": true, "github.com/ncruces/go-sqlite3": true,
	},
	"internal/architecture/m2_mutant_control_test.go": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "zombiezen.com/go/sqlite": true,
		"github.com/glebarez/sqlite": true, "github.com/ncruces/go-sqlite3": true,
	},
	"internal/architecture/policy_test.go": {"modernc.org/sqlite": true},
	"internal/controlroot/classifier_test.go": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "sqlite format 3": true,
	},
	"internal/controlroot/signatures.go":                     {"sqlite format 3": true},
	"internal/productionruntime/host_test.go":                {"sqlitefallback": true},
	"kitty-specs/beads-tracker-integration-01M0YG7E/plan.md": {"modernc.org/sqlite": true},
	"kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/tasks/WP01-frozen-journal-contracts-and-guards/review-cycle-5.md": {
		"modernc.org/sqlite": true, "go-sqlite3": true,
	},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/plan.md":                                                                     {"modernc.org/sqlite": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/post-approval-defect-12.md":                                                  {"modernc.org/sqlite": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP01-m2-contracts-and-permanent-architecture-guards/review-cycle-1.md": {"modernc.org/sqlite": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP12-historical-control-root-classification-and-refusal.md":            {"sqlite format 3": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP14-portable-scenario-harness-and-behavior-parity/review-cycle-5.md":  {"modernc.org/sqlite": true},
	"kitty-specs/gapdb-only-production-authority-01M1GWDJ/tasks/WP16-structural-sqlite-eradication-and-absence-proof.md":               {"modernc.org/sqlite": true},
	"kitty-specs/phase-one-architecture-closeout-01M0WZQ9/plan.md":                                                                     {"modernc.org/sqlite": true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/plan.md": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "pragma journal_mode": true,
	},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research.md": {
		"modernc.org/sqlite": true, "go-sqlite3": true, "pragma journal_mode": true,
	},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research/evidence-log.csv":                                             {"modernc.org/sqlite": true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/research/source-register.csv":                                          {"modernc.org/sqlite": true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP01-authority-seed-and-real-claim-race.md":                      {"modernc.org/sqlite": true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP02-stupid-tracker-and-attribution-safe-saga/review-cycle-1.md": {"create table": true},
	"kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/tasks/WP02-stupid-tracker-and-attribution-safe-saga/review-cycle-2.md": {"create table": true},
	"kitty-specs/software-development-mission-method-01M11EX2/plan.md":                                                                     {"modernc.org/sqlite": true},
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
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
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
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly)
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
	}
	if m2GateSource(path) {
		return
	}
	patterns := []struct{ kind, token string }{
		{"driver", "modernc.org/sqlite"}, {"driver", "go-sqlite3"}, {"driver", "zombiezen.com/go/sqlite"},
		{"driver", "github.com/glebarez/sqlite"}, {"driver", "github.com/ncruces/go-sqlite3"},
		{"copied-schema", "sqlite_master"}, {"copied-schema", "pragma journal_mode"}, {"copied-schema", "sqlite format 3"}, {"copied-schema", "create table"},
		{"subprocess", "exec.command(\"sqlite"}, {"subprocess", "commandcontext(\"sqlite"},
		{"dynamic-load", "plugin.open"}, {"linkname", "go:linkname"}, {"cgo", "-lsqlite"},
		{"fallback", "sqlitefallback"}, {"fallback", "fallbacktosqlite"}, {"fallback", "usesqlitefallback"},
		{"comparison-read", "comparesqlite"}, {"rescue-read", "rescuesqlite"}, {"dual-write", "dualwritesqlite"},
		{"shadow-write", "shadowwritesqlite"}, {"migration", "migratesqlite"}, {"automatic-import", "importsqlite"},
		{"runtime-open", "opensqlite"},
		{"embedded", "go:embed sqlite"}, {"embedded", "go:embed *.db"},
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern.token) &&
			(pattern.kind != "linkname" || strings.Contains(lower, "sqlite")) &&
			!m2HistoricalExemptions[path][pattern.token] {
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

func m2GateSource(path string) bool {
	return path == "internal/architecture/m2_absence_test.go" ||
		path == "internal/architecture/m2_absence_mutants_test.go" ||
		path == "scripts/m2-absence/inspect.sh" || path == "scripts/m2-absence/mutants.sh"
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
