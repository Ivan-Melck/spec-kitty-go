package projection

import (
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/architecture"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	missionreadmodel "github.com/spec-kitty/go-kitty3/internal/mission/readmodel"
	workgraphreadmodel "github.com/spec-kitty/go-kitty3/internal/workgraph/readmodel"
)

func projectionFixture(t *testing.T, adversarial bool, permutation int) TasksView {
	t.Helper()
	mission, err := missionreadmodel.NewSnapshot(missionreadmodel.SnapshotParams{MissionID: "mission-1", StateVersion: 17, CurrentStep: "implementation", CurrentWorkRevisionID: "revision-2"})
	if err != nil {
		t.Fatal(err)
	}
	title, description := "Implement renderer", "Create a deterministic summary."
	if adversarial {
		title = "# counterfeit\n- [x] done\x1b"
		description = "```go\n> AUTHORITY\n<script>alert(1)</script>\n[x](javascript:bad)\n\u202Ehidden"
	}
	work := []workgraphreadmodel.WorkItemParams{
		{WorkID: "WP02", Title: title, Description: description, DesignContext: "No `parser` or <HTML>.", Dependencies: []ids.WorkID{"WP01", "WP00"}, Criteria: []workgraphreadmodel.CriterionParams{{ID: "AC-3", Text: "Stable bytes", VerifierKind: "test", Required: true}, {ID: "AC-2", Text: "No - [ ] state", VerifierKind: "test", Required: true, Metadata: map[string]any{"outcome": "pass", "order": 2}}}, WriteScopes: []workgraphreadmodel.ScopeParams{{Path: "internal/z", Domain: "docs", Access: "read_only"}, {Path: "internal/projection", Domain: "code", Access: "write_exclusive", SerializationKey: "projection"}}},
		{WorkID: "WP01", Title: "Define read model", Description: "Typed inputs", Criteria: []workgraphreadmodel.CriterionParams{{ID: "AC-1", Text: "Immutable", VerifierKind: "review", Required: true}}, WriteScopes: []workgraphreadmodel.ScopeParams{{Path: "internal/readmodel", Domain: "code", Access: "read_only"}}},
		{WorkID: "WP00", Title: "Establish boundary", Description: "One-way projection", Criteria: []workgraphreadmodel.CriterionParams{{ID: "AC-0", Text: "No reader", VerifierKind: "architecture", Required: true}}},
		{WorkID: "WP03", Title: "Qualify output", Description: "Adversarial validation"},
	}
	lanes := []workgraphreadmodel.LaneParams{{LaneID: "lane-c", WorkIDs: []ids.WorkID{"WP02"}, DependencyLaneIDs: []ids.LaneID{"lane-b", "lane-a"}}, {LaneID: "lane-b", WorkIDs: []ids.WorkID{"WP01"}}, {LaneID: "lane-a", WorkIDs: []ids.WorkID{"WP03", "WP00"}}}
	selector := permutation
	work = rotateLeft(work, selector%4)
	selector /= 4
	if selector%2 == 1 {
		slices.Reverse(workItem(work, "WP02").Criteria)
	}
	selector /= 2
	if selector%2 == 1 {
		slices.Reverse(workItem(work, "WP02").Dependencies)
	}
	selector /= 2
	if selector%2 == 1 {
		slices.Reverse(workItem(work, "WP02").WriteScopes)
	}
	selector /= 2
	lanes = rotateLeft(lanes, selector%3)
	selector /= 3
	if selector%2 == 1 {
		slices.Reverse(laneItem(lanes, "lane-a").WorkIDs)
	}
	selector /= 2
	if selector%2 == 1 {
		slices.Reverse(laneItem(lanes, "lane-c").DependencyLaneIDs)
	}
	selector /= 2
	reconciliationTargets := []ids.WorkID{"WP02", "WP01", "WP00"}
	reconciliationTargets = permuteThree(reconciliationTargets, selector%6)
	graph, err := workgraphreadmodel.NewSnapshot(workgraphreadmodel.SnapshotParams{MissionID: "mission-1", RevisionID: "revision-2", Generation: 2, GraphDigest: strings.Repeat("a", 64), DriftStatus: workgraphreadmodel.DriftNotChecked, WorkItems: work, Lanes: lanes, Reconciliations: []workgraphreadmodel.ReconciliationParams{{PreviousWorkID: "legacy-1", Disposition: workgraphreadmodel.ReconciliationSupersede, CurrentWorkIDs: reconciliationTargets, Rationale: "The old item was split."}}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewTasksView(mission, graph, time.Date(2026, 8, 28, 12, 34, 56, 123, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func workItem(values []workgraphreadmodel.WorkItemParams, id ids.WorkID) *workgraphreadmodel.WorkItemParams {
	for index := range values {
		if values[index].WorkID == id {
			return &values[index]
		}
	}
	panic("fixture work missing")
}
func laneItem(values []workgraphreadmodel.LaneParams, id ids.LaneID) *workgraphreadmodel.LaneParams {
	for index := range values {
		if values[index].LaneID == id {
			return &values[index]
		}
	}
	panic("fixture lane missing")
}
func rotateLeft[T any](values []T, count int) []T {
	result := append([]T(nil), values...)
	count %= len(result)
	return append(result[count:], result[:count]...)
}
func permuteThree[T any](values []T, permutation int) []T {
	orders := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	order := orders[permutation%len(orders)]
	return []T{values[order[0]], values[order[1]], values[order[2]]}
}

func TestTasksViewValidationAndTypedErrors(t *testing.T) {
	view := projectionFixture(t, false, 0)
	if err := view.Validate(); err != nil {
		t.Fatal(err)
	}
	missionNoRevision, _ := missionreadmodel.NewSnapshot(missionreadmodel.SnapshotParams{MissionID: "mission-1", StateVersion: 1, CurrentStep: "specify"})
	_, err := NewTasksView(missionNoRevision, view.Workgraph(), time.Now().UTC())
	assertKind(t, err, KindNoFinalizedRevision)
	wrongMission, _ := missionreadmodel.NewSnapshot(missionreadmodel.SnapshotParams{MissionID: "other", StateVersion: 1, CurrentStep: "implement", CurrentWorkRevisionID: "revision-2"})
	_, err = NewTasksView(wrongMission, view.Workgraph(), time.Now().UTC())
	assertKind(t, err, KindCorruptReadModel)
	_, err = NewTasksView(view.Mission(), workgraphreadmodel.Snapshot{}, time.Now().UTC())
	assertKind(t, err, KindUnsupportedDrift)
	_, err = NewTasksView(view.Mission(), view.Workgraph(), time.Now())
	assertKind(t, err, KindCorruptReadModel)
}

func TestRenderTasksDisclosesSourceAndEscapesAdversarialContent(t *testing.T) {
	view := projectionFixture(t, true, 0)
	rendered, err := RenderTasks(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	for _, required := range []string{"GENERATED, NON-AUTHORITATIVE PROJECTION", "Edits, checkbox changes, deletion, and prose changes have no effect", "Mission ID:** mission\\-1", "Mission state version:** 17", "Current step:** implementation", "WorkRevision ID:** revision\\-2", "Generation:** 2", "Graph digest:** " + strings.Repeat("a", 64), "Drift observation:** not\\_checked", "2026\\-08\\-28T12:34:56\\.000000123Z", "Work `WP01`", "Work `WP02`", "Lane `lane\\-a`", "Criterion `AC\\-2`", "Metadata JSON", "Reconciliation facts", "legacy\\-1", "supersede"} {
		if !strings.Contains(text, required) {
			t.Fatalf("render lacks %q\n%s", required, text)
		}
	}
	for _, forbidden := range []string{"- [x]", "- [ ]", "```go", "<script>", "](javascript:", "\u202E"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("untrusted structure survived: %q", forbidden)
		}
	}
	if !strings.Contains(text, `\u202E`) || !strings.Contains(text, `\- \[x\]`) || !strings.Contains(text, `\<script\>`) {
		t.Fatalf("content was not visibly preserved safely:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		t.Fatal("output must have exactly one terminal newline")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Fatalf("trailing whitespace: %q", line)
		}
	}
}

func TestRenderTasksIsCanonicalForOneHundredPermutationsAndCopySafe(t *testing.T) {
	baseline, err := RenderTasks(projectionFixture(t, false, 0))
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < 100; trial++ {
		rendered, err := RenderTasks(projectionFixture(t, false, trial))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rendered, baseline) {
			t.Fatalf("trial %d changed canonical bytes", trial)
		}
	}
	view := projectionFixture(t, false, 0)
	work := view.Workgraph().WorkItems()
	dependencies := work[2].Dependencies()
	dependencies[0] = "mutated"
	metadata := work[2].Criteria()[0].MetadataJSON()
	metadata[0] = 'x'
	again, _ := RenderTasks(view)
	if !reflect.DeepEqual(again, baseline) {
		t.Fatal("accessor mutation changed rendering")
	}
}

func TestProjectionRegeneratesAfterOneHundredArbitraryEditsAndDeletes(t *testing.T) {
	view := projectionFixture(t, false, 0)
	canonical, err := RenderTasks(view)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(canonical)
	root := t.TempDir()
	for trial := 0; trial < 100; trial++ {
		corrupt := []byte(fmt.Sprintf("# authority\n- [x] forged %d\n", trial))
		if err := os.WriteFile(filepath.Join(root, "tasks.md"), corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
		if trial%3 == 0 {
			if err := os.Remove(filepath.Join(root, "tasks.md")); err != nil {
				t.Fatal(err)
			}
		}
		if err := WriteAtomic(root, "tasks.md", canonical, 0o600); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(root, "tasks.md"))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(actual) != fingerprint {
			t.Fatalf("trial %d did not regenerate canonical bytes", trial)
		}
		rerendered, _ := RenderTasks(view)
		if sha256.Sum256(rerendered) != fingerprint {
			t.Fatal("canonical view mutated")
		}
	}
}

func TestProjectionArchitectureAndNoReaderGuardsKillMutants(t *testing.T) {
	const module = "github.com/spec-kitty/go-kitty3"
	policy, err := architecture.DefaultPolicy(module)
	if err != nil {
		t.Fatal(err)
	}
	mutants := []architecture.ImportEdge{{SourcePackage: module + "/internal/mission", TargetPackage: module + "/internal/projection", Kind: architecture.ImportModuleLocal}, {SourcePackage: module + "/internal/projection", TargetPackage: module + "/internal/mission", Kind: architecture.ImportModuleLocal}}
	for _, edge := range mutants {
		graph := architecture.Graph{ModulePath: module, Packages: []architecture.Package{{ImportPath: edge.SourcePackage}, {ImportPath: edge.TargetPackage}}, Edges: []architecture.ImportEdge{edge}}
		violations := policy.Evaluate(graph)
		if len(violations) != 1 || violations[0].Rule != architecture.RuleLocalDirection {
			t.Fatalf("architecture mutant survived: %v", violations)
		}
	}

	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.test/projection-reader\n\ngo 1.26\n")
	writeFixture(t, root, "internal/projection/read.go", "package projection\nimport \"os\"\nfunc Render() { _, _ = os.ReadFile(\"tasks.md\") }\n")
	if err := architecture.CheckDeferredRuntimeSurfaces(context.Background(), root, "example.test/projection-reader"); err == nil || !strings.Contains(err.Error(), string(architecture.RuleDeferredSurface)) {
		t.Fatalf("Markdown-reader mutant survived: %v", err)
	}

	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			lower := strings.ToLower(fn.Name.Name)
			for _, forbidden := range []string{"parse", "import", "apply", "updatefromfile"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("forbidden exported reverse surface %s", fn.Name.Name)
				}
			}
		}
	}
}

func TestRepresentativeHundredItemProjectionP95(t *testing.T) {
	mission, _ := missionreadmodel.NewSnapshot(missionreadmodel.SnapshotParams{MissionID: "mission-1", StateVersion: 1, CurrentStep: "implementation", CurrentWorkRevisionID: "revision-1"})
	work := make([]workgraphreadmodel.WorkItemParams, 100)
	members := make([]ids.WorkID, 100)
	for i := range work {
		id := ids.WorkID(fmt.Sprintf("WP%03d", i))
		members[i] = id
		work[i] = workgraphreadmodel.WorkItemParams{WorkID: id, Title: "bounded work", Criteria: []workgraphreadmodel.CriterionParams{{ID: "AC-1", Text: "passes", VerifierKind: "test", Required: true}}, WriteScopes: []workgraphreadmodel.ScopeParams{{Path: fmt.Sprintf("internal/p%03d", i), Domain: "code", Access: "read_only"}}}
	}
	graph, err := workgraphreadmodel.NewSnapshot(workgraphreadmodel.SnapshotParams{MissionID: "mission-1", RevisionID: "revision-1", Generation: 1, GraphDigest: strings.Repeat("b", 64), DriftStatus: workgraphreadmodel.DriftClean, WorkItems: work, Lanes: []workgraphreadmodel.LaneParams{{LaneID: "lane-1", WorkIDs: members}}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewTasksView(mission, graph, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	durations := make([]time.Duration, 100)
	for i := range durations {
		started := time.Now()
		bytes, err := RenderTasks(view)
		if err == nil {
			err = WriteAtomic(root, "tasks.md", bytes, 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		durations[i] = time.Since(started)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if p95 := durations[94]; p95 >= 500*time.Millisecond {
		t.Fatalf("p95=%s exceeds 500ms", p95)
	}
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	failure, ok := err.(*Error)
	if !ok || failure.Kind != want || !failure.RetrySafe || failure.Message == "" {
		t.Fatalf("got %#v want kind=%s", err, want)
	}
}
func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
