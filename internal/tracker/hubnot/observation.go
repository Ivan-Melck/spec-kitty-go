package hubnot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type observationReader interface {
	showIssue(context.Context, *observationBudget, string, string) (issueShow, envelope, error)
	listDetails(context.Context, *observationBudget, string) (page[issueDetail], envelope, error)
	headsPage(context.Context, *observationBudget, string, string) (page[issueHead], envelope, error)
	historyPage(context.Context, *observationBudget, string, string, string) (page[issueHistoryItem], envelope, error)
	graphPage(context.Context, *observationBudget, string, string) (page[issueGraphItem], envelope, error)
}
type nativeDependency struct {
	Source string `json:"source"`
	Target string `json:"target"`
}
type inventoryView struct {
	Project      ids.ProjectID
	Snapshot     string
	nodes        map[string]mappedIssue
	workIDs      map[ids.WorkID]string
	edges        []nativeDependency
	dependencies map[ids.WorkID][]ids.WorkID
}

func (v inventoryView) lookup(id string) (mappedIssue, bool) {
	n, ok := v.nodes[id]
	return cloneMappedIssue(n), ok
}
func (v inventoryView) lookupWork(id ids.WorkID) (mappedIssue, bool) { return v.lookup(v.workIDs[id]) }
func (v inventoryView) currentDependencies(work ids.WorkID) []ids.WorkID {
	return append([]ids.WorkID(nil), v.dependencies[work]...)
}

var errSnapshotChanged = errors.New("accepted history snapshot changed")

func staleObservation(err error) bool {
	var command *CommandError
	return errors.Is(err, errSnapshotChanged) || (errors.As(err, &command) && command.ProviderCode == "stale_cursor")
}
func collectInventory(ctx context.Context, reader observationReader, budget *observationBudget, project ids.ProjectID) (inventoryView, error) {
	if ctx == nil || reader == nil || budget == nil {
		return inventoryView{}, invalid("observation context, reader and budget required", nil)
	}
	if _, err := ids.ParseProjectID(project.String()); err != nil {
		return inventoryView{}, invalid("invalid inventory project", err)
	}
	for {
		v, err := inventoryAttempt(ctx, reader, budget, project)
		if !staleObservation(err) {
			return v, err
		}
		if err := budget.restart(); err != nil {
			return inventoryView{}, failure(tracker.ErrorRevisionConflict, "bounded_retry", "coherent inventory restart budget exhausted", err)
		}
	}
}
func inventoryAttempt(ctx context.Context, reader observationReader, budget *observationBudget, project ids.ProjectID) (inventoryView, error) {
	v := inventoryView{Project: project, nodes: map[string]mappedIssue{}, workIDs: map[ids.WorkID]string{}, dependencies: map[ids.WorkID][]ids.WorkID{}}
	cursor := ""
	cursors := map[string]bool{}
	total := -1
	for {
		if err := ctx.Err(); err != nil {
			return v, err
		}
		if err := budget.charge(0, 0); err != nil {
			return v, err
		}
		p, e, err := reader.listDetails(ctx, budget, cursor)
		if err != nil {
			return v, err
		}
		if !validEventID(e.Snapshot) {
			return v, protocolFailure(fmt.Errorf("inventory snapshot missing"))
		}
		if v.Snapshot == "" {
			v.Snapshot = e.Snapshot
		} else if v.Snapshot != e.Snapshot {
			return v, errSnapshotChanged
		}
		if total < 0 {
			total = p.Total
		}
		if p.Total != total || p.Items == nil || len(p.Items) > 8 || total < 0 || len(v.nodes)+len(p.Items) > total {
			return v, protocolFailure(fmt.Errorf("inconsistent inventory page census"))
		}
		for _, d := range p.Items {
			if _, exists := v.nodes[d.ID]; exists {
				return v, protocolFailure(fmt.Errorf("duplicate issue ID"))
			}
			n, err := mapDetail(d, project)
			if err != nil {
				return v, err
			}
			v.nodes[d.ID] = n
			if n.Mapped {
				if _, exists := v.workIDs[n.Content.WorkID]; exists {
					return v, failure(tracker.ErrorAttributionLost, "reconcile_mapping", "duplicate signed work mapping", nil)
				}
				v.workIDs[n.Content.WorkID] = d.ID
			}
		}
		if e.NextCursor == "" {
			if len(v.nodes) != total {
				return v, protocolFailure(fmt.Errorf("incomplete inventory without continuation"))
			}
			break
		}
		if len(p.Items) == 0 || cursors[e.NextCursor] || len(v.nodes) == total {
			return v, protocolFailure(fmt.Errorf("repeated or contradictory inventory cursor"))
		}
		cursors[e.NextCursor] = true
		cursor = e.NextCursor
	}
	if err := v.deriveGraph(); err != nil {
		return v, err
	}
	return v, nil
}
func (v *inventoryView) deriveGraph() error {
	outgoing := map[string][]string{}
	indegree := map[string]int{}
	for id := range v.nodes {
		indegree[id] = 0
	}
	for id, n := range v.nodes {
		for _, relation := range n.Detail.State.Relations {
			if relation.Kind != "blocks" {
				continue
			}
			target, exists := v.nodes[relation.Target]
			if !exists {
				return failure(tracker.ErrorNotFound, "admit_supplier_history", "blocking relationship references an absent issue", nil)
			}
			if n.Mapped != target.Mapped {
				return failure(tracker.ErrorAttributionLost, "reconcile_mapping", "blocking relationship lacks a portable supplier mapping", nil)
			}
			v.edges = append(v.edges, nativeDependency{id, relation.Target})
			outgoing[id] = append(outgoing[id], relation.Target)
			indegree[relation.Target]++
			if n.Mapped {
				v.dependencies[target.Content.WorkID] = append(v.dependencies[target.Content.WorkID], n.Content.WorkID)
			}
		}
	}
	ready := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	visited := 0
	for len(ready) > 0 {
		id := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		visited++
		for _, target := range outgoing[id] {
			indegree[target]--
			if indegree[target] == 0 {
				ready = append(ready, target)
			}
		}
	}
	if visited != len(v.nodes) {
		return failure(tracker.ErrorRevisionConflict, "reconcile_graph", "blocking relationships contain a cycle", nil)
	}
	sort.Slice(v.edges, func(i, j int) bool {
		if v.edges[i].Source == v.edges[j].Source {
			return v.edges[i].Target < v.edges[j].Target
		}
		return v.edges[i].Source < v.edges[j].Source
	})
	for _, deps := range v.dependencies {
		sort.Slice(deps, func(i, j int) bool { return deps[i] < deps[j] })
	}
	return nil
}
func (v inventoryView) inventory() (tracker.InventoryObservation, error) {
	rows := make([]issueDetail, 0, len(v.nodes))
	for _, n := range v.nodes {
		d := n.Detail
		s := normalizedState(*d.State)
		d.State = &s
		rows = append(rows, d)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	proof, err := observationProof("inventory:"+v.Project.String(), v.Snapshot, struct {
		Kind     string             `json:"kind"`
		Project  ids.ProjectID      `json:"project"`
		Snapshot string             `json:"snapshot"`
		Rows     []issueDetail      `json:"rows"`
		Edges    []nativeDependency `json:"edges"`
	}{"complete-current-inventory/1", v.Project, v.Snapshot, rows, v.edges})
	if err != nil {
		return tracker.InventoryObservation{}, err
	}
	workIDs := make([]ids.WorkID, 0, len(v.workIDs))
	for id := range v.workIDs {
		workIDs = append(workIDs, id)
	}
	sort.Slice(workIDs, func(i, j int) bool { return workIDs[i] < workIDs[j] })
	items := []tracker.InventoryItem{}
	for _, id := range workIDs {
		node, _ := v.lookupWork(id)
		if !node.Content.Complete {
			continue
		}
		def, err := node.Content.definition(v.currentDependencies(id))
		if err != nil {
			return tracker.InventoryObservation{}, protocolFailure(err)
		}
		item, err := tracker.NewInventoryItem(def, node.reference(proof.Revision))
		if err != nil {
			return tracker.InventoryObservation{}, protocolFailure(err)
		}
		items = append(items, item)
	}
	return tracker.NewInventoryObservation(v.Project, items, proof)
}
func normalizedState(s issueState) issueState {
	s = cloneIssueState(s)
	sort.Strings(s.Labels)
	sort.Strings(s.Assignees)
	sort.Slice(s.Relations, func(i, j int) bool {
		if s.Relations[i].Kind == s.Relations[j].Kind {
			return s.Relations[i].Target < s.Relations[j].Target
		}
		return s.Relations[i].Kind < s.Relations[j].Kind
	})
	return s
}
func (b *BoundAdapter) Inventory(ctx context.Context, request tracker.InventoryRequest) (tracker.InventoryObservation, error) {
	if b == nil || request.ProjectID != b.binding.ProjectID {
		return tracker.InventoryObservation{}, invalid("inventory project must match binding", nil)
	}
	budget := newObservationBudget()
	defer budget.close()
	view, err := collectInventory(ctx, b, budget, request.ProjectID)
	if err != nil {
		return tracker.InventoryObservation{}, err
	}
	return view.inventory()
}

var _ tracker.InventoryPort = (*BoundAdapter)(nil)

// workObservation is a fresh value, not an adapter cache. Every nested slice/map
// comes from the current call. SourceHeads proves incoming-edge attribution;
// opening creators never substitute for current signed revision actors.
type workObservation struct {
	Content      workContent
	State        issueState
	Heads        []issueHead
	SourceHeads  map[string][]issueHead
	Graph        []issueGraphItem
	Comments     []issueHistoryItem
	Dependencies []ids.WorkID
	Reference    tracker.ExternalWorkRef
	Provider     tracker.ProviderObservation
	Snapshot     string
	Inventory    inventoryView
}

func (b *BoundAdapter) observe(ctx context.Context, budget *observationBudget, reference tracker.ExternalWorkRef) (workObservation, error) {
	if b == nil || reference.ProjectID != b.binding.ProjectID {
		return workObservation{}, invalid("observation project must match binding", nil)
	}
	return observeWithReader(ctx, b, budget, reference)
}
func observeWithReader(ctx context.Context, reader observationReader, budget *observationBudget, reference tracker.ExternalWorkRef) (workObservation, error) {
	if ctx == nil || reader == nil || budget == nil || reference.Validate() != nil || reference.Provider != "hubnot" || reference.MetadataSchema != 1 || reference.CreatedByOperation == "" || !validEventID(reference.ExternalID) {
		return workObservation{}, invalid("complete Hubnot reference and context required", nil)
	}
	for {
		result, err := observeAttempt(ctx, reader, budget, reference)
		if !staleObservation(err) {
			return result, err
		}
		if err := budget.restart(); err != nil {
			return workObservation{}, failure(tracker.ErrorRevisionConflict, "bounded_retry", "known observation restart budget exhausted", err)
		}
	}
}
func observedPages[T any](ctx context.Context, budget *observationBudget, snapshot, id string, expected int, read func(string) (page[T], envelope, error)) ([]T, int, error) {
	items := []T{}
	cursor := ""
	seen := map[string]bool{}
	total := -1
	cycles := -1
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if err := budget.charge(0, 0); err != nil {
			return nil, 0, err
		}
		p, e, err := read(cursor)
		if err != nil {
			return nil, 0, err
		}
		if e.Snapshot != snapshot {
			return nil, 0, errSnapshotChanged
		}
		if total < 0 {
			total = p.Total
			cycles = p.CycleCount
		}
		if p.Items == nil || p.IssueID != id || p.Conflict || p.HeadCount != 1 || p.Total != total || total < 0 || p.CycleCount != cycles || cycles < 0 || len(p.Items) > 8 || (expected >= 0 && total != expected) || len(items)+len(p.Items) > total {
			return nil, 0, protocolFailure(fmt.Errorf("inconsistent observed page census"))
		}
		items = append(items, p.Items...)
		if e.NextCursor == "" {
			if len(items) != total {
				return nil, 0, protocolFailure(fmt.Errorf("incomplete observed page"))
			}
			return items, cycles, nil
		}
		if len(p.Items) == 0 || len(items) == total || seen[e.NextCursor] {
			return nil, 0, protocolFailure(fmt.Errorf("repeated observed cursor"))
		}
		seen[e.NextCursor] = true
		cursor = e.NextCursor
	}
}
func observeAttempt(ctx context.Context, reader observationReader, budget *observationBudget, reference tracker.ExternalWorkRef) (workObservation, error) {
	v, err := inventoryAttempt(ctx, reader, budget, reference.ProjectID)
	if err != nil {
		return workObservation{}, err
	}
	node, ok := v.lookup(reference.ExternalID)
	if !ok {
		return workObservation{}, failure(tracker.ErrorNotFound, "reconcile_mapping", "mapped issue is not in accepted inventory", nil)
	}
	if !node.Mapped || node.Content.WorkID != reference.WorkID || node.Content.CreatedBy != reference.CreatedByOperation {
		return workObservation{}, failure(tracker.ErrorAttributionLost, "reconcile_mapping", "external reference disagrees with signed mapping", nil)
	}
	show, e, err := reader.showIssue(ctx, budget, reference.ExternalID, "")
	if err != nil {
		return workObservation{}, err
	}
	if e.Snapshot != v.Snapshot {
		return workObservation{}, errSnapshotChanged
	}
	if e.NextCursor != "" || show.ID != node.Detail.ID || show.Creator != node.Detail.Creator || show.Conflict || show.HeadCount != 1 || len(show.HeadIDs) != 1 || show.State == nil || !equalState(*show.State, *node.Detail.State) || show.CommentCount < 0 || show.RevisionCount < 1 {
		return workObservation{}, protocolFailure(fmt.Errorf("show disagrees with detailed inventory"))
	}
	heads, _, err := observedPages(ctx, budget, v.Snapshot, node.Detail.ID, 1, func(cursor string) (page[issueHead], envelope, error) {
		return reader.headsPage(ctx, budget, node.Detail.ID, cursor)
	})
	if err != nil {
		return workObservation{}, err
	}
	if err := validateObservedHeads(heads, node); err != nil {
		return workObservation{}, err
	}
	if show.HeadIDs[0] != heads[0].ID {
		return workObservation{}, protocolFailure(fmt.Errorf("show and signed heads disagree"))
	}
	graph, cycleCount, err := observedPages(ctx, budget, v.Snapshot, node.Detail.ID, -1, func(cursor string) (page[issueGraphItem], envelope, error) {
		return reader.graphPage(ctx, budget, node.Detail.ID, cursor)
	})
	if err != nil {
		return workObservation{}, err
	}
	for i := range graph {
		if graph[i].Edge != nil {
			edge := *graph[i].Edge
			graph[i].Edge = &edge
		}
	}
	sourceHeads := map[string][]issueHead{node.Detail.ID: heads}
	expectedEdges := map[string]bool{}
	for id, source := range v.nodes {
		for _, relation := range source.Detail.State.Relations {
			if id == node.Detail.ID || relation.Target == node.Detail.ID {
				expectedEdges[edgeKey(id, relation.Target, relation.Kind)] = true
			}
		}
	}
	seenEdges := map[string]bool{}
	components := map[string]map[string]bool{}
	for _, row := range graph {
		switch row.Type {
		case "edge":
			edge := row.Edge
			if edge == nil || row.Kind != "" || row.Component != "" || row.IssueID != "" || edge.Ambiguous || !validEventID(edge.Source) || !validEventID(edge.Target) || !validEventID(edge.Revision) || !validHex(edge.Actor, 32) {
				return workObservation{}, protocolFailure(fmt.Errorf("malformed current graph provenance"))
			}
			key := edgeKey(edge.Source, edge.Target, edge.Kind)
			if !expectedEdges[key] || seenEdges[key] {
				return workObservation{}, protocolFailure(fmt.Errorf("unexpected or duplicate graph edge"))
			}
			seenEdges[key] = true
			if _, ok := sourceHeads[edge.Source]; !ok {
				source, exists := v.lookup(edge.Source)
				if !exists {
					return workObservation{}, protocolFailure(fmt.Errorf("edge source absent"))
				}
				h, _, err := observedPages(ctx, budget, v.Snapshot, edge.Source, 1, func(cursor string) (page[issueHead], envelope, error) {
					return reader.headsPage(ctx, budget, edge.Source, cursor)
				})
				if err != nil {
					return workObservation{}, err
				}
				if err := validateObservedHeads(h, source); err != nil {
					return workObservation{}, err
				}
				sourceHeads[edge.Source] = h
			}
			head := sourceHeads[edge.Source][0]
			if head.ID != edge.Revision || head.Actor != edge.Actor {
				return workObservation{}, protocolFailure(fmt.Errorf("graph attribution disagrees with current source head"))
			}
		case "cycle_member":
			if row.Edge != nil || (row.Kind != "parent" && row.Kind != "blocks") || !validEventID(row.Component) || !validEventID(row.IssueID) {
				return workObservation{}, protocolFailure(fmt.Errorf("malformed graph cycle member"))
			}
			if _, ok := v.nodes[row.IssueID]; !ok {
				return workObservation{}, protocolFailure(fmt.Errorf("cycle member absent from inventory"))
			}
			key := row.Kind + ":" + row.Component
			if components[key] == nil {
				components[key] = map[string]bool{}
			}
			if components[key][row.IssueID] {
				return workObservation{}, protocolFailure(fmt.Errorf("duplicate cycle member"))
			}
			components[key][row.IssueID] = true
		default:
			return workObservation{}, protocolFailure(fmt.Errorf("unknown graph row"))
		}
	}
	if len(seenEdges) != len(expectedEdges) || len(components) != cycleCount {
		return workObservation{}, protocolFailure(fmt.Errorf("incomplete current graph"))
	}
	for _, members := range components {
		if !members[node.Detail.ID] {
			return workObservation{}, protocolFailure(fmt.Errorf("cycle does not include observed issue"))
		}
	}
	comments, _, err := observedPages(ctx, budget, v.Snapshot, node.Detail.ID, show.CommentCount, func(cursor string) (page[issueHistoryItem], envelope, error) {
		return reader.historyPage(ctx, budget, node.Detail.ID, "comments", cursor)
	})
	if err != nil {
		return workObservation{}, err
	}
	commentIDs := map[string]bool{}
	for _, comment := range comments {
		_, timeErr := time.Parse(time.RFC3339Nano, comment.Timestamp)
		if !validEventID(comment.ID) || !validHex(comment.Actor, 32) || comment.Kind != "issue.comment" || timeErr != nil || comment.Parents == nil || len(comment.Parents) != 0 || comment.State != nil || !utf8.ValidString(comment.Body) || strings.TrimSpace(comment.Body) == "" || len(comment.Body) > 65536 || commentIDs[comment.ID] || (comment.Operation != "" && !validOperation(comment.Operation)) {
			return workObservation{}, protocolFailure(fmt.Errorf("malformed or duplicate comment provenance"))
		}
		commentIDs[comment.ID] = true
	}
	sort.Slice(comments, func(i, j int) bool {
		if comments[i].Timestamp == comments[j].Timestamp {
			return comments[i].ID < comments[j].ID
		}
		return comments[i].Timestamp < comments[j].Timestamp
	})
	sort.Slice(graph, func(i, j int) bool {
		a, _ := json.Marshal(graph[i])
		b, _ := json.Marshal(graph[j])
		return bytes.Compare(a, b) < 0
	})
	inventoryProof, err := v.inventory()
	if err != nil {
		return workObservation{}, err
	}
	state := normalizedState(*node.Detail.State)
	proof, err := observationProof(node.Detail.ID, v.Snapshot, struct {
		Kind            string                 `json:"kind"`
		Snapshot        string                 `json:"snapshot"`
		InventoryDigest string                 `json:"inventory_digest"`
		State           issueState             `json:"state"`
		Heads           map[string][]issueHead `json:"source_heads"`
		Graph           []issueGraphItem       `json:"graph"`
		Comments        []issueHistoryItem     `json:"comments"`
	}{"known-work-observation/1", v.Snapshot, inventoryProof.CommonProof().Digest, state, sourceHeads, graph, comments})
	if err != nil {
		return workObservation{}, err
	}
	return workObservation{Content: node.Content, State: cloneIssueState(*node.Detail.State), Heads: heads, SourceHeads: sourceHeads, Graph: graph, Comments: comments, Dependencies: v.currentDependencies(reference.WorkID), Reference: node.reference(proof.Revision), Provider: proof, Snapshot: v.Snapshot, Inventory: v}, nil
}
func edgeKey(source, target, kind string) string { return source + "\x00" + target + "\x00" + kind }
func equalState(a, b issueState) bool {
	left, _ := json.Marshal(normalizedState(a))
	right, _ := json.Marshal(normalizedState(b))
	return bytes.Equal(left, right)
}
func validateObservedHeads(heads []issueHead, node mappedIssue) error {
	if len(heads) != 1 || !validEventID(heads[0].ID) || !validHex(heads[0].Actor, 32) || validateState(heads[0].State) != nil || !equalState(heads[0].State, *node.Detail.State) {
		return protocolFailure(fmt.Errorf("signed head disagrees with current inventory state"))
	}
	heads[0].State = normalizedState(heads[0].State)
	return nil
}
