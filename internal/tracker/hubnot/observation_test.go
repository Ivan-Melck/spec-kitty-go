package hubnot

import (
	"bytes"
	"context"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type fixtureReader struct {
	rows         []issueDetail
	calls, items int
	alter        func(int, *page[issueDetail], *envelope)
	headCalls    int
}

func (f *fixtureReader) listDetails(ctx context.Context, b *observationBudget, cursor string) (page[issueDetail], envelope, error) {
	f.calls++
	offset := 0
	if cursor != "" {
		fmt.Sscanf(cursor, "page-%d", &offset)
	}
	end := min(offset+8, len(f.rows))
	p := page[issueDetail]{Items: append([]issueDetail{}, f.rows[offset:end]...), Total: len(f.rows)}
	e := envelope{Snapshot: "sha256:" + strings.Repeat("e", 64)}
	if end < len(f.rows) {
		e.NextCursor = fmt.Sprintf("page-%d", end)
	}
	f.items += len(p.Items)
	if err := b.charge(len(p.Items)*1024, len(p.Items)); err != nil {
		return p, e, err
	}
	if f.alter != nil {
		f.alter(f.calls, &p, &e)
	}
	return p, e, nil
}
func (f *fixtureReader) headsPage(context.Context, *observationBudget, string, string) (page[issueHead], envelope, error) {
	f.headCalls++
	return page[issueHead]{}, envelope{}, fmt.Errorf("unexpected heads")
}
func (f *fixtureReader) historyPage(context.Context, *observationBudget, string, string, string) (page[issueHistoryItem], envelope, error) {
	return page[issueHistoryItem]{}, envelope{}, fmt.Errorf("unexpected history")
}
func (f *fixtureReader) graphPage(context.Context, *observationBudget, string, string) (page[issueGraphItem], envelope, error) {
	return page[issueGraphItem]{}, envelope{}, fmt.Errorf("unexpected graph")
}
func TestInventoryThousandCompleteDefinitionsWithCurrentGraph(t *testing.T) {
	f := &fixtureReader{}
	want := map[ids.WorkID]tracker.WorkDefinition{}
	for i := 0; i < 1000; i++ {
		work := fmt.Sprintf("work-%04d", i)
		content := richContent(t, work)
		state, err := encodeWorkState(foreignState(), content)
		if err != nil {
			t.Fatal(err)
		}
		state.Relations = nil
		id := fmt.Sprintf("sha256:%064x", i+1)
		deps := []ids.WorkID{}
		if i > 0 {
			deps = append(deps, ids.WorkID(fmt.Sprintf("work-%04d", i-1)))
			f.rows[i-1].State.Relations = []issueRelation{{Kind: "blocks", Target: id}}
		}
		want[content.WorkID], err = content.definition(deps)
		if err != nil {
			t.Fatal(err)
		}
		f.rows = append(f.rows, issueDetail{ID: id, Creator: strings.Repeat("a", 64), OpeningMetadata: cloneIssueState(state).Metadata, State: &state, HeadCount: 1})
	}
	b := newObservationBudget()
	defer b.close()
	view, err := collectInventory(context.Background(), f, b, "project")
	if err != nil {
		t.Fatal(err)
	}
	result, err := view.inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items()) != 1000 || f.calls != 125 || f.items != 1000 || f.headCalls != 0 {
		t.Fatalf("inventory fanout or omissions: %d %d %d", len(result.Items()), f.calls, f.items)
	}
	for _, item := range result.Items() {
		d := item.Definition()
		w := want[d.WorkID()]
		if !reflect.DeepEqual(d.Criteria(), w.Criteria()) || !reflect.DeepEqual(d.WriteScopes(), w.WriteScopes()) || !reflect.DeepEqual(d.Provenance(), w.Provenance()) || !reflect.DeepEqual(d.Dependencies(), w.Dependencies()) || string(d.CanonicalData()) != string(w.CanonicalData()) {
			t.Fatalf("lost complete fields for %s", d.WorkID())
		}
	}
}
func TestInventoryRefusesIncompleteOrContradictoryViews(t *testing.T) {
	for _, change := range []string{"duplicate-id", "duplicate-work", "missing-supplier", "unmapped-supplier", "conflicted-supplier", "cycle", "snapshot", "repeated-cursor", "missing-page"} {
		t.Run(change, func(t *testing.T) {
			f := &fixtureReader{rows: []issueDetail{fixtureDetail(t, "work", "1"), fixtureDetail(t, "supplier", "2")}}
			switch change {
			case "duplicate-id":
				f.rows[1].ID = f.rows[0].ID
			case "duplicate-work":
				f.rows[1] = fixtureDetail(t, "work", "2")
			case "missing-supplier":
				f.rows[0].State.Relations = []issueRelation{{Kind: "blocks", Target: "sha256:" + strings.Repeat("f", 64)}}
			case "unmapped-supplier":
				f.rows[1].State.Metadata = map[string]string{}
				f.rows[1].OpeningMetadata = map[string]string{}
				f.rows[1].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[0].ID}}
			case "conflicted-supplier":
				f.rows[1].Conflict = true
				f.rows[1].HeadCount = 2
				f.rows[1].State = nil
			case "cycle":
				f.rows[0].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[1].ID}}
				f.rows[1].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[0].ID}}
			case "snapshot", "repeated-cursor", "missing-page":
				for i := 2; i < 9; i++ {
					f.rows = append(f.rows, fixtureDetail(t, fmt.Sprintf("x%d", i), fmt.Sprintf("%x", i+1)))
				}
				f.alter = func(call int, p *page[issueDetail], e *envelope) {
					switch change {
					case "snapshot":
						if call%2 == 0 {
							e.Snapshot = "sha256:" + strings.Repeat("f", 64)
						}
					case "repeated-cursor":
						e.NextCursor = "page-0"
					case "missing-page":
						e.NextCursor = ""
					}
				}
			}
			b := newObservationBudget()
			defer b.close()
			if _, err := collectInventory(context.Background(), f, b, "project"); err == nil {
				t.Fatal("accepted incomplete or contradictory inventory")
			}
			if f.headCalls != 0 {
				t.Fatal("inventory scanned per-issue heads")
			}
		})
	}
}

func (f *fixtureReader) showIssue(context.Context, *observationBudget, string, string) (issueShow, envelope, error) {
	return issueShow{}, envelope{}, fmt.Errorf("unexpected show")
}

type knownReader struct {
	*fixtureReader
	heads    map[string]issueHead
	graph    []issueGraphItem
	comments []issueHistoryItem
	bad      string
}

func (r *knownReader) headsPage(_ context.Context, b *observationBudget, id, cursor string) (page[issueHead], envelope, error) {
	r.headCalls++
	h := r.heads[id]
	if r.bad == "head-state" {
		h.State.Title = "contradiction"
	}
	return page[issueHead]{Items: []issueHead{h}, IssueID: id, Total: 1, HeadCount: 1}, envelope{Snapshot: "sha256:" + strings.Repeat("e", 64)}, b.charge(1024, 1)
}
func (r *knownReader) graphPage(_ context.Context, b *observationBudget, id, cursor string) (page[issueGraphItem], envelope, error) {
	g := append([]issueGraphItem{}, r.graph...)
	if r.bad == "edge-actor" {
		edge := *g[0].Edge
		edge.Actor = strings.Repeat("f", 64)
		g[0].Edge = &edge
	}
	e := envelope{Snapshot: "sha256:" + strings.Repeat("e", 64)}
	if r.bad == "snapshot" {
		e.Snapshot = "sha256:" + strings.Repeat("f", 64)
	}
	return page[issueGraphItem]{Items: g, IssueID: id, Total: len(g), HeadCount: 1}, e, b.charge(1024, len(g))
}
func (r *knownReader) historyPage(_ context.Context, b *observationBudget, id, kind, cursor string) (page[issueHistoryItem], envelope, error) {
	items := append([]issueHistoryItem{}, r.comments...)
	if r.bad == "comments-missing" {
		items = []issueHistoryItem{}
	}
	return page[issueHistoryItem]{Items: items, IssueID: id, Total: len(items), HeadCount: 1}, envelope{Snapshot: "sha256:" + strings.Repeat("e", 64)}, b.charge(1024, len(items))
}
func (r *knownReader) showIssue(_ context.Context, b *observationBudget, id, cursor string) (issueShow, envelope, error) {
	for _, n := range r.rows {
		if n.ID == id {
			return issueShow{ID: id, Creator: n.Creator, State: n.State, HeadIDs: []string{r.heads[id].ID}, HeadCount: 1, RevisionCount: 2, CommentCount: len(r.comments)}, envelope{Snapshot: "sha256:" + strings.Repeat("e", 64)}, b.charge(1024, 1)
		}
	}
	return issueShow{}, envelope{}, fmt.Errorf("missing show")
}
func TestKnownObservationRequiresCurrentSignerAndCompletePages(t *testing.T) {
	for _, bad := range []string{"", "head-state", "edge-actor", "snapshot", "comments-missing"} {
		t.Run("case-"+bad, func(t *testing.T) {
			a := fixtureDetail(t, "work", "1")
			supplier := fixtureDetail(t, "supplier", "2")
			supplier.State.Relations = []issueRelation{{Kind: "blocks", Target: a.ID}}
			r := &knownReader{fixtureReader: &fixtureReader{rows: []issueDetail{a, supplier}}, heads: map[string]issueHead{}, bad: bad}
			r.heads[a.ID] = issueHead{ID: "sha256:" + strings.Repeat("3", 64), Actor: strings.Repeat("c", 64), State: *a.State}
			r.heads[supplier.ID] = issueHead{ID: "sha256:" + strings.Repeat("4", 64), Actor: strings.Repeat("d", 64), State: *supplier.State}
			r.graph = []issueGraphItem{{Type: "edge", Edge: &issueEdge{Source: supplier.ID, Target: a.ID, Kind: "blocks", Revision: r.heads[supplier.ID].ID, Actor: r.heads[supplier.ID].Actor}}}
			r.comments = []issueHistoryItem{{ID: "sha256:" + strings.Repeat("5", 64), Actor: strings.Repeat("f", 64), Kind: "issue.comment", Timestamp: "2026-09-09T00:00:00Z", Parents: []string{}, Body: "Native exact body\n"}}
			b := newObservationBudget()
			defer b.close()
			reference := tracker.ExternalWorkRef{ProjectID: "project", WorkID: "work", Provider: "hubnot", ExternalID: a.ID, MetadataSchema: 1, CreatedByOperation: "create-work"}
			got, err := observeWithReader(context.Background(), r, b, reference)
			if bad != "" {
				if err == nil {
					t.Fatal("accepted inconsistent known observation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Heads[0].Actor == a.Creator || got.Heads[0].Actor != strings.Repeat("c", 64) || got.Graph[0].Edge.Actor != strings.Repeat("d", 64) || got.Comments[0].Body != r.comments[0].Body {
				t.Fatal("current provenance/body lost")
			}
			got.Graph[0].Edge.Actor = "changed"
			if r.graph[0].Edge.Actor == "changed" {
				t.Fatal("returned graph aliases reader storage")
			}
			if !reflect.DeepEqual(got.Dependencies, []ids.WorkID{"supplier"}) {
				t.Fatal("historical creation dependencies leaked into current read")
			}
		})
	}
}

func TestPinnedPublicCLICompleteInventoryAndObservation(t *testing.T) {
	binary := os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if binary == "" {
		t.Skip("set GO_KITTY_TEST_HUBNOT_BINARY to the admitted public executable")
	}
	dir := t.TempDir()
	run := func(executable string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(executable, args...)
		cmd.Dir = dir
		cmd.Env = processEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("public fixture %v: %v %s", args, err, out)
		}
		return out
	}
	run("/usr/bin/git", "init", "-q")
	run("/usr/bin/git", "-c", "user.name=Readmodel Test", "-c", "user.email=readmodel@test.invalid", "commit", "--allow-empty", "-q", "-m", "fixture")
	run(binary, "init", "--name", "Read model fixture")
	raw := run(binary, "identity", "show", "--json")
	env, err := decodeEnvelope(raw, identitySchema, 0)
	if err != nil {
		t.Fatal(err)
	}
	var identity PublicIdentity
	if err := decodeStrict(env.Data, &identity); err != nil {
		t.Fatal(err)
	}
	adapter, err := Open(context.Background(), BindingConfig{Schema: bindingSchema, Provider: "hubnot", ProjectID: "project", WorkspacePath: dir, ExecutablePath: binary, ExpectedActor: identity.Actor, Identity: QualifiedProviderIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	budget := newObservationBudget()
	defer budget.close()
	targetContent := richContent(t, "target")
	targetState, err := encodeWorkState(issueState{}, targetContent)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := adapter.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "fixture-target", state: &targetState})
	if err != nil {
		t.Fatalf("public target open: %v", err)
	}
	supplierContent := richContent(t, "supplier")
	supplierState, err := encodeWorkState(issueState{}, supplierContent)
	if err != nil {
		t.Fatal(err)
	}
	supplierState.Relations = []issueRelation{{Kind: "blocks", Target: target.IssueID}}
	supplier, _, err := adapter.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "fixture-supplier", state: &supplierState})
	if err != nil {
		t.Fatalf("public supplier open: %v", err)
	}
	if _, _, err := adapter.mutate(context.Background(), budget, mutationRequest{intent: "comment", id: target.IssueID, key: "fixture-comment", body: "native exact body\n雪"}); err != nil {
		t.Fatal(err)
	}
	before := run("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)")
	inventory, err := adapter.Inventory(context.Background(), tracker.InventoryRequest{ProjectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Items()) != 2 {
		t.Fatal("public inventory incomplete")
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project", WorkID: "target", Provider: "hubnot", ExternalID: target.IssueID, MetadataSchema: 1, CreatedByOperation: targetContent.CreatedBy}
	observed, err := adapter.observe(context.Background(), budget, reference)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.Dependencies, []ids.WorkID{"supplier"}) || observed.Graph[0].Edge.Source != supplier.IssueID || observed.Comments[0].Body != "native exact body\n雪" {
		t.Fatal("public graph/comment facts lost")
	}
	def, err := observed.Content.definition(observed.Dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(def.Criteria(), targetContent.Criteria) || !bytes.Equal(def.ProviderMetadata().Bytes(), targetContent.Opaque) {
		t.Fatal("public complete work fidelity lost")
	}
	after := run("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)")
	if !bytes.Equal(before, after) {
		t.Fatal("read model changed accepted refs")
	}
}
