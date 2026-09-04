package architecture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const m2ModulePath = "github.com/spec-kitty/go-kitty3"

const m2LegacyExemptionSHA256 = "dc09c98815fb900c0806b76b830d87b7895981875796e136d0c9742d13d6f400"

var m2SQLProviderPrefixes = []string{
	"database/sql",
	"github.com/glebarez/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/mutecomm/go-sqlcipher/v4",
	"github.com/ncruces/go-sqlite3",
	"modernc.org/sqlite",
	"zombiezen.com/go/sqlite",
}

var m2GapDBPrivateSegments = []string{"internal", "private", "private-server", "server", "server-private"}

var m2PureJournalStandardLibrary = []PackagePattern{
	ExactPackage("bytes"),
	ExactPackage("context"),
	ExactPackage("crypto/sha256"),
	PackagePrefix("encoding"),
	ExactPackage("errors"),
	ExactPackage("fmt"),
	ExactPackage("hash"),
	ExactPackage("sort"),
	ExactPackage("strconv"),
	ExactPackage("strings"),
	PackagePrefix("sync"),
	PackagePrefix("unicode"),
}

var m2ExactGapDBCallers = []PackagePattern{
	ExactPackage(m2ModulePath + "/internal/controlstore/gapdb"),
	ExactPackage(m2ModulePath + "/internal/sidecar/gapdb"),
	ExactPackage(m2ModulePath + "/internal/testcampaign/m2/external"),
}

var m2TrackerForbiddenTargets = []PackagePattern{
	PackagePrefix(m2ModulePath + "/internal/artifact"),
	PackagePrefix(m2ModulePath + "/internal/mission"),
	PackagePrefix(m2ModulePath + "/internal/execution"),
	PackagePrefix(m2ModulePath + "/internal/workspace"),
	PackagePrefix(m2ModulePath + "/internal/handoff"),
	PackagePrefix(m2ModulePath + "/internal/review"),
	PackagePrefix(m2ModulePath + "/internal/acceptance"),
	PackagePrefix(m2ModulePath + "/internal/integration"),
	PackagePrefix(m2ModulePath + "/internal/gitrepo"),
}

type m2LegacyExemption struct {
	Source          string
	Target          string
	Kind            ImportKind
	AdmissionTuple  string
	RemovalBoundary string
}

func m2LegacyExemptions(module string) []m2LegacyExemption {
	return []m2LegacyExemption{
		{module + "/internal/controlstore/sqlite", "database/sql", ImportStandardLibrary, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "database/sql", ImportTestOnly, m2TupleSHA, "WP16"},
		{module + "/internal/controlstore/sqlite", "modernc.org/sqlite", ImportTestOnly, m2TupleSHA, "WP16"},
		{module + "/internal/helpercmd", module + "/internal/controlstore/sqlite", ImportModuleLocal, m2TupleSHA, "WP15"},
		{module + "/internal/scenario", "database/sql", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", module + "/internal/controlstore/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "modernc.org/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "database/sql", ImportStandardLibrary, m2TupleSHA, "WP14"},
		{module + "/internal/scenario", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP14"},
		{module + "/internal/testcampaign/m1/external", "github.com/spec-kitty/gapdb/gapdb", ImportExternal, m2TupleSHA, "WP17"},
		{module + "/internal/tracker/beads", module + "/internal/controlstore/sqlite", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/artifact", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/execution", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/mission", ImportTestOnly, m2TupleSHA, "WP14"},
		{module + "/internal/tracker/beads", module + "/internal/workspace", ImportTestOnly, m2TupleSHA, "WP14"},
	}
}

func TestM2DependencyPolicyAcceptsOnlyBoundedProviderComposition(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph := fixtureGraph(
		[]string{
			m2ModulePath + "/internal/ids",
			m2ModulePath + "/internal/controljournal/v2",
			m2ModulePath + "/internal/controlstore/gapdb",
			m2ModulePath + "/internal/sidecar/gapdb",
			m2ModulePath + "/internal/productionruntime",
			m2ModulePath + "/internal/mission",
			m2ModulePath + "/internal/execution",
		},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: m2ModulePath + "/internal/ids", Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: m2ModulePath + "/internal/controljournal/v2", Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/sidecar/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/productionruntime", TargetPackage: m2ModulePath + "/internal/controlstore/gapdb", Kind: ImportModuleLocal},
	)
	graph.ModulePath = m2ModulePath
	if err := policy.Check(graph); err != nil {
		t.Fatalf("evidence=PACKAGE-DIRECTIONS legal M2 composition rejected: %v", err)
	}
}

func TestM2DependencyPositiveBoundaries(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	legal := fixtureGraph(
		[]string{
			m2ModulePath + "/internal/controljournal/v2",
			m2ModulePath + "/internal/controlstore/gapdb",
			m2ModulePath + "/internal/sidecar/gapdb",
			m2ModulePath + "/internal/testcampaign/m2/external",
		},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "context", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "encoding/json", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controljournal/v2", TargetPackage: "crypto/sha256", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/sidecar/gapdb", TargetPackage: "github.com/spec-kitty/gapdb/gapdbapi", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/testcampaign/m2/external", TargetPackage: "github.com/spec-kitty/gapdb/gapdb", Kind: ImportExternal},
	)
	legal.ModulePath = m2ModulePath
	if violations := m2EvaluateClosedGraph(policy, legal); len(violations) != 0 {
		t.Fatalf("evidence=PURE-KERNEL-IMPORTS legal provider-neutral/provider callers rejected:\n%s", violations.Error())
	}
}

func TestM2DependencyRestoreDeadlineIsOneExactEdge(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	restorePackage := m2ModulePath + "/internal/controljournal/v2/restore"
	legal := fixtureGraph(
		[]string{restorePackage},
		ImportEdge{SourcePackage: restorePackage, TargetPackage: "context", Kind: ImportStandardLibrary},
		ImportEdge{SourcePackage: restorePackage, TargetPackage: "time", Kind: ImportStandardLibrary},
	)
	legal.ModulePath = m2ModulePath
	if violations := m2EvaluateClosedGraph(policy, legal); len(violations) != 0 {
		t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY exact restore deadline edges rejected: %v", violations)
	}

	for _, test := range []struct{ name, source string }{
		{"foundation", m2ModulePath + "/internal/controljournal/v2"},
		{"checkpoint", m2ModulePath + "/internal/controljournal/v2/checkpoint"},
		{"playback", m2ModulePath + "/internal/controljournal/v2/playback"},
		{"nested_restore_owner", m2ModulePath + "/internal/controljournal/v2/restore/worker"},
		{"other_v2_package", m2ModulePath + "/internal/controljournal/v2/other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := fixtureGraph([]string{test.source}, ImportEdge{SourcePackage: test.source, TargetPackage: "time", Kind: ImportStandardLibrary})
			graph.ModulePath = m2ModulePath
			violations := m2EvaluateClosedGraph(policy, graph)
			if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface || violations[0].SourcePackage != test.source || violations[0].TargetPackage != "time" {
				t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY source=%s observed=%v expected=exact deferred-surface refusal", test.source, violations)
			}
		})
	}

	missingTime := fixtureGraph([]string{restorePackage}, ImportEdge{SourcePackage: restorePackage, TargetPackage: "context", Kind: ImportStandardLibrary})
	missingTime.ModulePath = m2ModulePath
	violations := m2EvaluateClosedGraph(policy, missingTime)
	if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface || violations[0].SourcePackage != restorePackage || violations[0].TargetPackage != "time" {
		t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY deleted elapsed-attempt capability observed=%v expected=required restore->time edge", violations)
	}
}

func TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		source     string
		wantTarget string
	}{
		{
			name: "bounded_structural_capability",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
func defaultBudget() BudgetsV2 { return BudgetsV2{MaximumDuration: 30 * time.Second} }
`,
		},
		{
			name: "ambient_clock",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _, _ = child, time.Now() }
`,
			wantTarget: "time.Now",
		},
		{
			name: "misplaced_duration_state",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
type ResultV2 struct { FinishedAfter time.Duration }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "time.Duration.state",
		},
		{
			name: "package_scope_inferred_duration",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
var leakedDuration = time.Second
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "time.Duration.state",
		},
		{
			name: "package_scope_inferred_duration_constant",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
const leakedDuration = time.Second
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "time.Duration.state",
		},
		{
			name: "package_scope_erased_duration_initializer",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
var leakedDuration any = time.Second
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "time.Duration.state",
		},
		{
			name: "transitive_result_budget",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
type ResultV2 struct { Budget BudgetsV2 }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "time.Duration.state",
		},
		{
			name: "missing_direct_deadline_call",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
func run(ctx context.Context, budgets BudgetsV2) { _, _ = ctx, budgets }
`,
			wantTarget: "context.WithTimeout",
		},
		{
			name: "deadline_function_escape",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
func consume(value any) { _ = value }
func run(ctx context.Context, budgets BudgetsV2) { consume(context.WithTimeout); child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`,
			wantTarget: "context.WithTimeout.escape",
		},
		{
			name: "control_flow_owned_by_wp04",
			source: `package restore
import ("context"; "time")
type BudgetsV2 struct { MaximumDuration time.Duration }
func attempt(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
func run(ctx context.Context, budgets BudgetsV2, enabled bool) { if enabled { budgets.MaximumDuration = 0 }; invoke := attempt; invoke(ctx, budgets) }
`,
		},
		{
			name: "blank_time_import",
			source: `package restore
import ("context"; _ "time")
func run(ctx context.Context) { child, cancel := context.WithTimeout(ctx, 30); defer cancel(); _ = child }
`,
			wantTarget: "time.import",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "go.mod", "module "+m2ModulePath+"\n\ngo 1.26\n")
			writeGoFixture(t, root, "internal/controljournal/v2/restore/restore.go", tc.source)
			graph, err := LoadGraph(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			violations := m2EvaluateClosedGraph(m2ClosedPolicy(t, m2ModulePath), graph)
			if tc.wantTarget == "" {
				if len(violations) != 0 {
					t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY validated resource budget rejected: %v", violations)
				}
				return
			}
			if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface || violations[0].TargetPackage != tc.wantTarget {
				t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY source-use=%s observed=%v expected=%s", tc.name, violations, tc.wantTarget)
			}
		})
	}
}

func TestM2DependencyRestoreAliasesRemainFileScoped(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+m2ModulePath+"\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/controljournal/v2/restore/restore.go", `package restore
import ("context"; t "time")
type BudgetsV2 struct { MaximumDuration t.Duration }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
`)
	writeGoFixture(t, root, "internal/controljournal/v2/restore/diagnostic.go", `package restore
type localClock struct { Now int }
func diagnostic(t localClock) int { return t.Now }
`)
	graph, err := LoadGraph(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if violations := m2EvaluateClosedGraph(m2ClosedPolicy(t, m2ModulePath), graph); len(violations) != 0 {
		t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY file-scoped alias shadow rejected: %v", violations)
	}
}

func TestM2DependencyRestoreAliasShadowUsesObjectIdentity(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+m2ModulePath+"\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/controljournal/v2/restore/restore.go", `package restore
import ("context"; t "time")
type BudgetsV2 struct { MaximumDuration t.Duration }
type localClock struct { Now int }
func run(ctx context.Context, budgets BudgetsV2) { child, cancel := context.WithTimeout(ctx, budgets.MaximumDuration); defer cancel(); _ = child }
func diagnostic(t localClock) int { return t.Now }
`)
	graph, err := LoadGraph(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if violations := m2EvaluateClosedGraph(m2ClosedPolicy(t, m2ModulePath), graph); len(violations) != 0 {
		t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY lexical import shadow rejected: %v", violations)
	}
}

func TestM2DependencyPolicyRejectsForbiddenEdges(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	tests := []struct {
		name   string
		source string
		target string
		kind   ImportKind
		rule   RuleID
	}{
		{"kernel-to-gapdb-adapter", m2ModulePath + "/internal/execution", m2ModulePath + "/internal/controlstore/gapdb", ImportModuleLocal, RuleLocalDirection},
		{"kernel-to-gapdb-provider", m2ModulePath + "/internal/mission", "github.com/spec-kitty/gapdb/gapdb", ImportExternal, RuleExternalDirection},
		{"pure-journal-to-filesystem-capability", m2ModulePath + "/internal/controljournal/v2", m2ModulePath + "/internal/gitrepo", ImportModuleLocal, RuleLocalDirection},
		{"tracker-to-mission-authority", m2ModulePath + "/internal/tracker/beads", m2ModulePath + "/internal/mission", ImportModuleLocal, RuleLocalDirection},
		{"unknown-production-package", m2ModulePath + "/internal/unclassified", m2ModulePath + "/internal/unclassified", ImportModuleLocal, RuleUnknownPackage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packages := []string{test.source}
			if test.kind == ImportModuleLocal && test.target != test.source {
				packages = append(packages, test.target)
			}
			graph := fixtureGraph(packages, ImportEdge{SourcePackage: test.source, TargetPackage: test.target, Kind: test.kind})
			graph.ModulePath = m2ModulePath
			violations := policy.Evaluate(graph)
			if len(violations) != 1 || violations[0].Rule != test.rule || violations[0].SourcePackage != test.source || violations[0].TargetPackage != test.target {
				t.Fatalf("evidence=PACKAGE-DIRECTIONS mutant=%s observed=%v expected=%s:%s->%s", test.name, violations, test.rule, test.source, test.target)
			}
		})
	}

	for _, prefix := range m2SQLProviderPrefixes {
		t.Run("sql-"+strings.NewReplacer("/", "-", ".", "-").Replace(prefix), func(t *testing.T) {
			source := m2ModulePath + "/internal/controljournal/v2"
			graph := fixtureGraph([]string{source}, ImportEdge{SourcePackage: source, TargetPackage: prefix + "/internal/driver", Kind: ImportExternal})
			graph.ModulePath = m2ModulePath
			violations := m2EvaluateClosedGraph(policy, graph)
			if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface {
				t.Fatalf("evidence=SQLITE-SQL-DENIAL provider=%s observed=%v expected=deferred-surface", prefix, violations)
			}
		})
	}
	for _, target := range []string{
		"github.com/spec-kitty/gapdb/internal/authority",
		"github.com/spec-kitty/gapdb/server/authority",
		"github.com/spec-kitty/gapdb/gapdb/internal/authority",
		"github.com/spec-kitty/gapdb/gapdb/private-server/authority",
	} {
		t.Run("gapdb-private-"+strings.NewReplacer("/", "-", ".", "-").Replace(target), func(t *testing.T) {
			source := m2ModulePath + "/internal/controlstore/gapdb"
			graph := fixtureGraph([]string{source}, ImportEdge{SourcePackage: source, TargetPackage: target, Kind: ImportExternal})
			graph.ModulePath = m2ModulePath
			violations := m2EvaluateClosedGraph(policy, graph)
			if len(violations) == 0 || violations[0].Rule != RuleDeferredSurface {
				t.Fatalf("evidence=GAPDB-PRIVATE-DENIAL provider=%s observed=%v expected=deferred-surface", target, violations)
			}
		})
	}
	for _, target := range []string{"github.com/spec-kitty/gapdb/gapdb", "github.com/spec-kitty/gapdb/gapdbapi", "github.com/spec-kitty/gapdb/gapdb/internalized", "github.com/spec-kitty/gapdb/gapdb/serverless"} {
		if m2IsGapDBPrivatePath(target) {
			t.Fatalf("evidence=GAPDB-PRIVATE-DENIAL public-or-lookalike=%s observed=private", target)
		}
	}
}

func TestM2CapabilityReachabilityStopsAtFoundationCoordinator(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	capability := m2ModulePath + "/internal/execution"
	coordinator := m2ModulePath + "/internal/controljournal/v2"
	adapter := m2ModulePath + "/internal/controlstore/gapdb"

	legal := fixtureGraph(
		[]string{capability, coordinator, adapter, m2ModulePath + "/internal/productionruntime"},
		ImportEdge{SourcePackage: capability, TargetPackage: coordinator, Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/productionruntime", TargetPackage: adapter, Kind: ImportModuleLocal},
		ImportEdge{SourcePackage: adapter, TargetPackage: coordinator, Kind: ImportModuleLocal},
	)
	legal.ModulePath = m2ModulePath
	if violations := m2CapabilityGapDBReachability(policy, legal); len(violations) != 0 {
		t.Fatalf("evidence=PROVIDER-REACHABILITY legal coordinator/composition graph rejected: %v", violations)
	}

	for _, test := range []struct {
		name  string
		edges []ImportEdge
	}{
		{
			name:  "direct",
			edges: []ImportEdge{{SourcePackage: capability, TargetPackage: adapter, Kind: ImportModuleLocal}},
		},
		{
			name: "wrapped-by-foundation",
			edges: []ImportEdge{
				{SourcePackage: capability, TargetPackage: coordinator, Kind: ImportModuleLocal},
				{SourcePackage: coordinator, TargetPackage: adapter, Kind: ImportModuleLocal},
			},
		},
		{
			name: "kernel-returns-adapter-witness",
			// A Go signature returning any adapter-owned witness necessarily
			// imports the adapter. The dependency edge is rejected independent
			// of the witness type's local name or any wrapper interface.
			edges: []ImportEdge{
				{SourcePackage: capability, TargetPackage: adapter, Kind: ImportModuleLocal},
				{SourcePackage: adapter, TargetPackage: coordinator, Kind: ImportModuleLocal},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := fixtureGraph([]string{capability, coordinator, adapter}, test.edges...)
			graph.ModulePath = m2ModulePath
			violations := m2CapabilityGapDBReachability(policy, graph)
			if len(violations) != 1 || violations[0].Rule != RuleReachability || violations[0].SourcePackage != capability || violations[0].TargetPackage != adapter {
				t.Fatalf("evidence=PROVIDER-REACHABILITY mutant=%s observed=%v expected=%s->%s", test.name, violations, capability, adapter)
			}
		})
	}
}

func TestM2PackageClassificationActivatesOnPresence(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	want := map[string]TierName{
		m2ModulePath + "/internal/controljournal/v2":          TierFoundation,
		m2ModulePath + "/internal/controljournal/v2/playback": TierFoundation,
		m2ModulePath + "/internal/acceptance":                 TierAuthority,
		m2ModulePath + "/internal/productionruntime":          TierComposition,
		m2ModulePath + "/internal/controlroot":                TierFoundation,
		m2ModulePath + "/cmd/spec-kitty":                      TierComposition,
		m2ModulePath + "/cmd/spk-m2-qualification":            TierQuality,
	}
	for path, expected := range want {
		tier, err := policy.Classify(path)
		if err != nil || tier.Name != expected {
			t.Fatalf("evidence=PACKAGE-DIRECTIONS package=%s observed=%+v error=%v expected=%s", path, tier, err, expected)
		}
	}
	if _, err := policy.Classify(m2ModulePath + "/internal/controljournal/v2x"); err == nil {
		t.Fatal("evidence=PACKAGE-DIRECTIONS slash-bounded lookalike was classified")
	}
}

func TestM2CurrentRepositoryActivatesCompleteGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	graph, err := LoadGraph(ctx, m2RepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := m2ClosedPolicy(t, graph.ModulePath)
	violations := m2EvaluateIncrementalRepository(policy, graph)
	if len(violations) != 0 {
		t.Fatalf("evidence=PACKAGE-DIRECTIONS current M2 surface violations:\n%s", violations.Error())
	}
}

func TestM2LaterCapabilityCannotHideBehindLegacySQLite(t *testing.T) {
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph := fixtureGraph(
		[]string{m2ModulePath + "/internal/controlstore/sqlite", m2ModulePath + "/internal/workgraph"},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/sqlite", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/workgraph", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal},
	)
	graph.ModulePath = m2ModulePath
	violations := m2EvaluateIncrementalRepository(policy, graph)
	if len(violations) != 1 || violations[0].SourcePackage != m2ModulePath+"/internal/workgraph" || violations[0].TargetPackage != "modernc.org/sqlite" {
		t.Fatalf("evidence=SQLITE-SQL-DENIAL reviewer-probe observed=%v expected=workgraph SQLite violation", violations)
	}
}

func TestM2LegacyExemptionsAreExactAndProvenanceBound(t *testing.T) {
	exemptions := m2LegacyExemptions(m2ModulePath)
	if err := validateM2LegacyExemptions(exemptions); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(exemptions))
	for _, exemption := range exemptions {
		key := fmt.Sprintf("%s|%s|%s", exemption.Source, exemption.Target, exemption.Kind)
		if seen[key] || exemption.AdmissionTuple != m2TupleSHA || exemption.RemovalBoundary == "" {
			t.Fatalf("evidence=SQLITE-SQL-DENIAL invalid legacy exemption=%+v", exemption)
		}
		seen[key] = true
	}
	mutants := []struct {
		candidate m2LegacyExemption
		list      []m2LegacyExemption
	}{
		{m2LegacyExemption{m2ModulePath + "/internal/workgraph", "modernc.org/sqlite", ImportExternal, m2TupleSHA, "WP16"}, exemptions},
		{m2LegacyExemption{m2ModulePath + "/internal/controlstore/sqlite", "modernc.org/sqlite/lib", ImportExternal, m2TupleSHA, "WP16"}, exemptions},
		{exemptions[0], func() []m2LegacyExemption {
			values := slices.Clone(exemptions)
			values[0].AdmissionTuple = "mutable"
			return values
		}()},
	}
	for _, mutant := range mutants {
		if m2IsLegacyExempt(mutant.candidate.Source, mutant.candidate.Target, mutant.candidate.Kind, mutant.list) {
			t.Fatalf("evidence=SQLITE-SQL-DENIAL broad legacy mutant accepted=%+v", mutant.candidate)
		}
	}
}

func TestM2PolicyDeletionMutantsFailClosed(t *testing.T) {
	base := m2ClosedPolicy(t, m2ModulePath)
	mutants := []struct {
		name   string
		mutate func(*Policy)
	}{
		{"remove-controljournal-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/controljournal/v2") }},
		{"remove-composition-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/productionruntime") }},
		{"remove-sql-forbidden-prefix", func(policy *Policy) {
			policy.ForbiddenImportPrefixes = removeM2String(policy.ForbiddenImportPrefixes, "modernc.org/sqlite")
		}},
		{"remove-gapdb-adapter-classification", func(policy *Policy) { removeM2Pattern(policy, m2ModulePath+"/internal/controlstore/gapdb") }},
		{"remove-capability-gapdb-denial", func(policy *Policy) { policy.ForbiddenLocalDirections = policy.ForbiddenLocalDirections[1:] }},
		{"remove-kernel-direction", func(policy *Policy) {
			for index := range policy.Tiers {
				if policy.Tiers[index].Name == TierAuthority {
					policy.Tiers[index].AllowedLocalTargets = append(policy.Tiers[index].AllowedLocalTargets, TierAdapter)
				}
			}
		}},
		{"widen-foundation-to-adapter", func(policy *Policy) {
			for index := range policy.Tiers {
				if policy.Tiers[index].Name == TierFoundation {
					policy.Tiers[index].AllowedLocalTargets = append(policy.Tiers[index].AllowedLocalTargets, TierAdapter)
				}
			}
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			policy := cloneM2Policy(base)
			mutant.mutate(&policy)
			if err := validateM2PolicyShape(policy); err == nil {
				t.Fatalf("evidence=GUARD-DELETION-MUTANTS mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}
}

func m2ClosedPolicy(t *testing.T, module string) Policy {
	t.Helper()
	policy, err := m2ClosedPolicyValue(module)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func m2ClosedPolicyValue(module string) (Policy, error) {
	policy, err := DefaultPolicy(module)
	if err != nil {
		return Policy{}, err
	}
	policy = cloneM2Policy(policy)
	for _, prefix := range m2SQLProviderPrefixes {
		if !slices.Contains(policy.ForbiddenImportPrefixes, prefix) {
			policy.ForbiddenImportPrefixes = append(policy.ForbiddenImportPrefixes, prefix)
		}
	}
	for index := range policy.Tiers {
		if policy.Tiers[index].Name == TierAdapter || policy.Tiers[index].Name == TierQuality {
			policy.Tiers[index].AllowedExternalPrefixes = removeM2String(policy.Tiers[index].AllowedExternalPrefixes, "modernc.org/sqlite")
		}
	}
	if err := validateM2PolicyShape(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func validateM2PolicyShape(policy Policy) error {
	requiredClasses := map[string]TierName{
		policy.ModulePath + "/internal/controljournal/v2":  TierFoundation,
		policy.ModulePath + "/internal/controlroot":        TierFoundation,
		policy.ModulePath + "/internal/acceptance":         TierAuthority,
		policy.ModulePath + "/internal/controlstore/gapdb": TierAdapter,
		policy.ModulePath + "/internal/productionruntime":  TierComposition,
	}
	for path, expected := range requiredClasses {
		tier, err := policy.Classify(path)
		if err != nil || tier.Name != expected {
			return fmt.Errorf("evidence=PACKAGE-DIRECTIONS law=required-class package=%s observed=%+v error=%v expected=%s", path, tier, err, expected)
		}
	}
	for _, prefix := range m2SQLProviderPrefixes {
		if !slices.Contains(policy.ForbiddenImportPrefixes, prefix) {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=forbidden-prefix observed=missing expected=%s", prefix)
		}
	}
	for _, tier := range policy.Tiers {
		if tier.Name == TierFoundation && slices.Contains(tier.AllowedLocalTargets, TierAdapter) {
			return fmt.Errorf("evidence=PROVIDER-REACHABILITY law=foundation-adapter observed=allowed expected=denied")
		}
		if tier.Name == TierAuthority && slices.Contains(tier.AllowedLocalTargets, TierAdapter) {
			return fmt.Errorf("evidence=PACKAGE-DIRECTIONS law=kernel-adapter observed=allowed expected=denied")
		}
		if (tier.Name == TierAdapter || tier.Name == TierQuality) && slices.Contains(tier.AllowedExternalPrefixes, "modernc.org/sqlite") {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=external-allowance tier=%s observed=sqlite expected=absent", tier.Name)
		}
	}
	if !m2HasCapabilityGapDBDenial(policy) {
		return fmt.Errorf("evidence=PROVIDER-REACHABILITY law=capability-adapter-denial observed=missing")
	}
	return policy.Validate()
}

func m2HasCapabilityGapDBDenial(policy Policy) bool {
	wantSources := m2CapabilitySourcePatterns(policy.ModulePath)
	wantTarget := PackagePrefix(policy.ModulePath + "/internal/controlstore/gapdb")
	for _, direction := range policy.ForbiddenLocalDirections {
		if len(direction.TargetTiers) != 0 || len(direction.Targets) != 1 || direction.Targets[0] != wantTarget || len(direction.Sources) != len(wantSources) {
			continue
		}
		if slices.Equal(direction.Sources, wantSources) {
			return true
		}
	}
	return false
}

func m2CapabilitySourcePatterns(module string) []PackagePattern {
	return []PackagePattern{
		ExactPackage(module + "/internal/artifact"),
		ExactPackage(module + "/internal/mission"),
		ExactPackage(module + "/internal/workgraph"),
		ExactPackage(module + "/internal/execution"),
		ExactPackage(module + "/internal/workspace"),
		ExactPackage(module + "/internal/handoff"),
		ExactPackage(module + "/internal/review"),
		ExactPackage(module + "/internal/integration"),
		PackagePrefix(module + "/internal/acceptance"),
	}
}

func m2CapabilityGapDBReachability(policy Policy, graph Graph) Violations {
	var violations Violations
	target := policy.ModulePath + "/internal/controlstore/gapdb"
	for _, pkg := range graph.Packages {
		tier, err := policy.Classify(pkg.ImportPath)
		if err != nil || !slices.Contains([]TierName{TierAuthority, TierArtifactAuthority, TierMissionAuthority}, tier.Name) {
			continue
		}
		if len(m2ForbiddenPaths(graph, pkg.ImportPath, []string{target})) == 0 {
			continue
		}
		violations = append(violations, Violation{
			Rule: RuleReachability, SourcePackage: pkg.ImportPath, SourceTier: tier.Name,
			TargetPackage: target, TargetTier: TierAdapter,
		})
	}
	slices.SortFunc(violations, func(left, right Violation) int { return strings.Compare(left.Error(), right.Error()) })
	return violations
}

func m2EvaluateClosedGraph(policy Policy, graph Graph) Violations {
	violations := policy.Evaluate(graph)
	for _, edge := range graph.Edges {
		sourceTier, _ := policy.Classify(edge.SourcePackage)
		if matchesPrefix(edge.TargetPackage, m2SQLProviderPrefixes) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		}
		if m2IsGapDBPrivatePath(edge.TargetPackage) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		} else if matchesPrefix(edge.TargetPackage, []string{"github.com/spec-kitty/gapdb"}) && !forbidden(edge.SourcePackage, m2ExactGapDBCallers) {
			violations = appendM2Violation(violations, violationForEdge(RuleExternalDirection, edge, sourceTier.Name, ""))
		}
		if edge.Kind != ImportTestOnly && matchesPrefix(edge.SourcePackage, []string{policy.ModulePath + "/internal/controljournal/v2"}) && edge.Kind == ImportStandardLibrary && !m2AllowedJournalStandardLibraryEdge(policy.ModulePath, edge) {
			violations = appendM2Violation(violations, violationForEdge(RuleDeferredSurface, edge, sourceTier.Name, ""))
		}
		if sourceTier.Name == TierAdapter && matchesPrefix(edge.SourcePackage, []string{policy.ModulePath + "/internal/tracker"}) && forbidden(edge.TargetPackage, m2TrackerForbiddenTargets) {
			targetTier, _ := policy.Classify(edge.TargetPackage)
			violations = appendM2Violation(violations, violationForEdge(RuleLocalDirection, edge, sourceTier.Name, targetTier.Name))
		}
	}
	violations = append(violations, m2RestoreDeadlineContractViolations(graph)...)
	violations = append(violations, m2RestoreDeadlineSourceViolations(graph)...)
	slices.SortFunc(violations, func(left, right Violation) int { return strings.Compare(left.Error(), right.Error()) })
	return violations
}

func m2AllowedJournalStandardLibraryEdge(modulePath string, edge ImportEdge) bool {
	if forbidden(edge.TargetPackage, m2PureJournalStandardLibrary) {
		return true
	}
	return edge.SourcePackage == modulePath+"/internal/controljournal/v2/restore" && edge.TargetPackage == "time"
}

func m2RestoreDeadlineContractViolations(graph Graph) Violations {
	restorePackage := graph.ModulePath + "/internal/controljournal/v2/restore"
	present := false
	for _, pkg := range graph.Packages {
		if pkg.ImportPath == restorePackage {
			present = true
			break
		}
	}
	if !present {
		return nil
	}
	required := map[string]bool{"context": false, "time": false}
	for _, edge := range graph.Edges {
		if edge.SourcePackage == restorePackage && edge.Kind == ImportStandardLibrary {
			if _, tracked := required[edge.TargetPackage]; tracked {
				required[edge.TargetPackage] = true
			}
		}
	}
	var violations Violations
	for _, target := range []string{"context", "time"} {
		if !required[target] {
			violations = appendM2Violation(violations, Violation{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: target})
		}
	}
	return violations
}

func m2RestoreDeadlineSourceViolations(graph Graph) Violations {
	restorePackage := graph.ModulePath + "/internal/controljournal/v2/restore"
	var restoreDir string
	for _, pkg := range graph.Packages {
		if pkg.ImportPath == restorePackage {
			restoreDir = pkg.Dir
			break
		}
	}
	// Synthetic dependency graphs intentionally have no source directory; the
	// exact-edge and required-import laws remain active for those fixtures.
	if restoreDir == "" {
		return nil
	}
	entries, err := os.ReadDir(restoreDir)
	if err != nil {
		return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "restore-source"}}
	}
	invalidTimeImport := false
	files := make([]*ast.File, 0)
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(restoreDir, entry.Name()), nil, 0)
		if parseErr != nil {
			return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "restore-source"}}
		}
		files = append(files, file)
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, "`\"") == "time" && imported.Name != nil &&
				(imported.Name.Name == "." || imported.Name.Name == "_") {
				invalidTimeImport = true
			}
		}
	}
	if invalidTimeImport {
		return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "time.import"}}
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	config := types.Config{Importer: m2RestoreTypeImporter{fallback: importer.Default()}, Error: func(error) {}}
	checked, _ := config.Check(restorePackage, fset, files, info)

	budgetDuration := m2RestoreBudgetDurationSelector(files, info)
	durationUses := 0
	for _, file := range files {
		var violation string
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			object := info.Uses[identifier]
			if !m2RestoreObjectFromPackage(object, "time") {
				return true
			}
			if object.Name() == "Duration" {
				durationUses++
				if budgetDuration == nil || identifier != budgetDuration.Sel {
					violation = "time.Duration.state"
					return false
				}
			} else if !slices.Contains([]string{"Second", "Minute"}, object.Name()) {
				violation = "time." + object.Name()
				return false
			}
			return true
		})
		if violation != "" {
			return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: violation}}
		}
	}
	if durationUses != 1 || budgetDuration == nil {
		return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "time.Duration.state"}}
	}
	if checked == nil || m2RestorePackageInitializerUsesTimeUnit(files, info) || !m2RestoreDurationStateConfined(checked) {
		return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "time.Duration.state"}}
	}
	directUses := make(map[*ast.Ident]bool)
	directCalls := 0
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !m2RestoreCallTargetsPackageFunction(call, info, "context", "WithTimeout") {
				return true
			}
			if identifier := m2RestoreCalledFunctionIdentifier(call); identifier != nil {
				directUses[identifier] = true
				directCalls++
			}
			return true
		})
	}
	for _, file := range files {
		escaped := false
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && m2RestoreObjectFromPackage(info.Uses[identifier], "context") && identifier.Name == "WithTimeout" && !directUses[identifier] {
				escaped = true
				return false
			}
			return !escaped
		})
		if escaped {
			return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "context.WithTimeout.escape"}}
		}
	}
	if directCalls == 0 {
		return Violations{{Rule: RuleDeferredSurface, SourcePackage: restorePackage, SourceTier: TierFoundation, TargetPackage: "context.WithTimeout"}}
	}
	return nil
}

type m2RestoreTypeImporter struct{ fallback types.Importer }

func (i m2RestoreTypeImporter) Import(path string) (*types.Package, error) {
	if path == "context" || path == "time" {
		return i.fallback.Import(path)
	}
	stub := types.NewPackage(path, filepath.Base(path))
	stub.MarkComplete()
	return stub, nil
}

func m2RestoreBudgetDurationSelector(files []*ast.File, info *types.Info) *ast.SelectorExpr {
	var admitted *ast.SelectorExpr
	for _, file := range files {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || typeSpec.Name.Name != "BudgetsV2" {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					return nil
				}
				for _, field := range structure.Fields.List {
					if len(field.Names) != 1 || field.Names[0].Name != "MaximumDuration" {
						continue
					}
					selector, ok := field.Type.(*ast.SelectorExpr)
					if !ok {
						return nil
					}
					object := info.Uses[selector.Sel]
					if !m2RestoreObjectFromPackage(object, "time") || object.Name() != "Duration" || admitted != nil {
						return nil
					}
					admitted = selector
				}
			}
		}
	}
	return admitted
}

func m2RestoreDurationStateConfined(pkg *types.Package) bool {
	scope := pkg.Scope()
	budget, budgetOK := scope.Lookup("BudgetsV2").(*types.TypeName)
	input, _ := scope.Lookup("InputV2").(*types.TypeName)
	if !budgetOK {
		return false
	}
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		switch state := object.(type) {
		case *types.Var:
			if m2RestoreTypeReachesDuration(state.Type(), make(map[types.Type]bool)) {
				return false
			}
		case *types.Const:
			if m2RestoreTypeReachesDuration(state.Type(), make(map[types.Type]bool)) {
				return false
			}
		}
		typeName, ok := object.(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := types.Unalias(typeName.Type()).(*types.Named)
		if !ok {
			continue
		}
		structure, ok := named.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for index := 0; index < structure.NumFields(); index++ {
			field := structure.Field(index)
			if !m2RestoreTypeReachesDuration(field.Type(), make(map[types.Type]bool)) {
				continue
			}
			budgetDuration := typeName == budget && field.Name() == "MaximumDuration" && m2RestoreIsExactDuration(field.Type())
			inputBudget := input != nil && typeName == input && field.Name() == "Budgets" &&
				types.Identical(types.Unalias(field.Type()), types.Unalias(budget.Type()))
			if !budgetDuration && !inputBudget {
				return false
			}
		}
	}
	return true
}

func m2RestorePackageInitializerUsesTimeUnit(files []*ast.File, info *types.Info) bool {
	for _, file := range files {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.VAR && generic.Tok != token.CONST {
				continue
			}
			for _, specification := range generic.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, value := range values.Values {
					found := false
					ast.Inspect(value, func(node ast.Node) bool {
						identifier, ok := node.(*ast.Ident)
						object := info.Uses[identifier]
						if ok && m2RestoreObjectFromPackage(object, "time") && (object.Name() == "Second" || object.Name() == "Minute") {
							found = true
							return false
						}
						return !found
					})
					if found {
						return true
					}
				}
			}
		}
	}
	return false
}

func m2RestoreTypeReachesDuration(valueType types.Type, visited map[types.Type]bool) bool {
	valueType = types.Unalias(valueType)
	if valueType == nil || visited[valueType] {
		return false
	}
	if m2RestoreIsExactDuration(valueType) {
		return true
	}
	visited[valueType] = true
	switch typed := valueType.(type) {
	case *types.Named:
		return m2RestoreTypeReachesDuration(typed.Underlying(), visited)
	case *types.Pointer:
		return m2RestoreTypeReachesDuration(typed.Elem(), visited)
	case *types.Array:
		return m2RestoreTypeReachesDuration(typed.Elem(), visited)
	case *types.Slice:
		return m2RestoreTypeReachesDuration(typed.Elem(), visited)
	case *types.Map:
		return m2RestoreTypeReachesDuration(typed.Key(), visited) || m2RestoreTypeReachesDuration(typed.Elem(), visited)
	case *types.Chan:
		return m2RestoreTypeReachesDuration(typed.Elem(), visited)
	case *types.Struct:
		for index := 0; index < typed.NumFields(); index++ {
			if m2RestoreTypeReachesDuration(typed.Field(index).Type(), visited) {
				return true
			}
		}
	case *types.Signature:
		return m2RestoreTupleReachesDuration(typed.Params(), visited) || m2RestoreTupleReachesDuration(typed.Results(), visited)
	case *types.Interface:
		for index := 0; index < typed.NumMethods(); index++ {
			if m2RestoreTypeReachesDuration(typed.Method(index).Type(), visited) {
				return true
			}
		}
	case *types.TypeParam:
		return m2RestoreTypeReachesDuration(typed.Constraint(), visited)
	case *types.Union:
		for index := 0; index < typed.Len(); index++ {
			if m2RestoreTypeReachesDuration(typed.Term(index).Type(), visited) {
				return true
			}
		}
	}
	return false
}

func m2RestoreTupleReachesDuration(tuple *types.Tuple, visited map[types.Type]bool) bool {
	if tuple == nil {
		return false
	}
	for index := 0; index < tuple.Len(); index++ {
		if m2RestoreTypeReachesDuration(tuple.At(index).Type(), visited) {
			return true
		}
	}
	return false
}

func m2RestoreIsExactDuration(valueType types.Type) bool {
	named, ok := types.Unalias(valueType).(*types.Named)
	return ok && named.Obj().Name() == "Duration" && m2RestoreObjectFromPackage(named.Obj(), "time")
}

func m2RestoreCalledFunctionIdentifier(call *ast.CallExpr) *ast.Ident {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function
	case *ast.SelectorExpr:
		return function.Sel
	default:
		return nil
	}
}

func m2RestoreCalledFunction(call *ast.CallExpr, info *types.Info) *types.Func {
	identifier := m2RestoreCalledFunctionIdentifier(call)
	if identifier == nil {
		return nil
	}
	function, _ := info.Uses[identifier].(*types.Func)
	return function
}

func m2RestoreCallTargetsPackageFunction(call *ast.CallExpr, info *types.Info, packagePath, name string) bool {
	function := m2RestoreCalledFunction(call, info)
	return function != nil && function.Name() == name && m2RestoreObjectFromPackage(function, packagePath)
}

func m2RestoreObjectFromPackage(object types.Object, packagePath string) bool {
	return object != nil && object.Pkg() != nil && object.Pkg().Path() == packagePath
}

func m2IsGapDBPrivatePath(target string) bool {
	const root = "github.com/spec-kitty/gapdb"
	if target != root && !strings.HasPrefix(target, root+"/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(target, root+"/"), "/") {
		if slices.Contains(m2GapDBPrivateSegments, segment) {
			return true
		}
	}
	return false
}

func m2EvaluateIncrementalRepository(policy Policy, graph Graph) Violations {
	exemptions := m2LegacyExemptions(graph.ModulePath)
	filtered := graph
	filtered.Edges = make([]ImportEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if m2IsLegacyExempt(edge.SourcePackage, edge.TargetPackage, edge.Kind, exemptions) {
			continue
		}
		filtered.Edges = append(filtered.Edges, edge)
	}
	violations := m2EvaluateClosedGraph(policy, filtered)
	for _, violation := range m2CapabilityGapDBReachability(policy, filtered) {
		violations = appendM2Violation(violations, violation)
	}
	slices.SortFunc(violations, func(left, right Violation) int { return strings.Compare(left.Error(), right.Error()) })
	return violations
}

func appendM2Violation(values Violations, candidate Violation) Violations {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func m2IsLegacyExempt(source, target string, kind ImportKind, exemptions []m2LegacyExemption) bool {
	for _, exemption := range exemptions {
		if exemption.AdmissionTuple == m2TupleSHA && exemption.Source == source && exemption.Target == target && exemption.Kind == kind {
			return true
		}
	}
	return false
}

func validateM2LegacyExemptions(exemptions []m2LegacyExemption) error {
	identities := make([]string, 0, len(exemptions))
	seen := make(map[string]bool, len(exemptions))
	for _, exemption := range exemptions {
		identity := fmt.Sprintf("%s|%s|%s|%s|%s", exemption.Source, exemption.Target, exemption.Kind, exemption.AdmissionTuple, exemption.RemovalBoundary)
		if exemption.AdmissionTuple != m2TupleSHA || exemption.RemovalBoundary == "" || seen[identity] {
			return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=legacy-exemption-provenance observed=%+v", exemption)
		}
		seen[identity] = true
		identities = append(identities, identity)
	}
	slices.Sort(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	got := hex.EncodeToString(digest[:])
	if got != m2LegacyExemptionSHA256 {
		return fmt.Errorf("evidence=SQLITE-SQL-DENIAL law=legacy-exemption-census observed-sha256=%s expected=%s", got, m2LegacyExemptionSHA256)
	}
	return nil
}

func cloneM2Policy(policy Policy) Policy {
	clone := policy
	clone.ForbiddenPackages = slices.Clone(policy.ForbiddenPackages)
	clone.ForbiddenImportPrefixes = slices.Clone(policy.ForbiddenImportPrefixes)
	clone.AllowedNeutralExternalPrefixes = slices.Clone(policy.AllowedNeutralExternalPrefixes)
	clone.Tiers = slices.Clone(policy.Tiers)
	for index := range clone.Tiers {
		clone.Tiers[index].Packages = slices.Clone(policy.Tiers[index].Packages)
		clone.Tiers[index].AllowedLocalTargets = slices.Clone(policy.Tiers[index].AllowedLocalTargets)
		clone.Tiers[index].AllowedExternalPrefixes = slices.Clone(policy.Tiers[index].AllowedExternalPrefixes)
	}
	clone.ForbiddenLocalDirections = slices.Clone(policy.ForbiddenLocalDirections)
	return clone
}

func removeM2Pattern(policy *Policy, path string) {
	for tierIndex := range policy.Tiers {
		patterns := policy.Tiers[tierIndex].Packages[:0]
		for _, pattern := range policy.Tiers[tierIndex].Packages {
			if pattern.Path != path {
				patterns = append(patterns, pattern)
			}
		}
		policy.Tiers[tierIndex].Packages = patterns
	}
}

func removeM2String(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
