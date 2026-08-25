package workgraph

import (
	"bytes"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestFinalizeCanonicalizesEquivalentGraphsAndDefendsImmutability(t *testing.T) {
	t.Parallel()
	first := testDraft(t, false)
	second := testDraft(t, true)
	meta := testMetadata("revision-1", 1)

	revisionA, err := first.Finalize(meta)
	if err != nil {
		t.Fatalf("finalize first: %v", err)
	}
	revisionB, err := second.Finalize(meta)
	if err != nil {
		t.Fatalf("finalize second: %v", err)
	}
	if revisionA.GraphDigest() != revisionB.GraphDigest() || revisionA.CriteriaDigest() != revisionB.CriteriaDigest() {
		t.Fatalf("canonical digests differ: graph %q/%q criteria %q/%q", revisionA.GraphDigest(), revisionB.GraphDigest(), revisionA.CriteriaDigest(), revisionB.CriteriaDigest())
	}
	if !bytes.Equal(revisionA.CanonicalGraph(), revisionB.CanonicalGraph()) {
		t.Fatalf("canonical graph differs:\n%s\n%s", revisionA.CanonicalGraph(), revisionB.CanonicalGraph())
	}
	if err := first.AddLane(Lane{ID: "late"}); !IsKind(err, KindFinalized) {
		t.Fatalf("mutation after finalize error = %v", err)
	}
	if _, err := first.Finalize(meta); !IsKind(err, KindFinalized) {
		t.Fatalf("second finalize error = %v", err)
	}

	items := revisionA.WorkItems()
	items[0].Title = "mutated"
	items[0].Criteria[0].Text = "mutated"
	items[0].Dependencies = append(items[0].Dependencies, ids.WorkID("work-z"))
	canonical := revisionA.CanonicalGraph()
	canonical[0] ^= 0xff
	if revisionA.GraphDigest() != revisionB.GraphDigest() || !bytes.Equal(revisionA.CanonicalGraph(), revisionB.CanonicalGraph()) {
		t.Fatal("caller mutated finalized revision through returned data")
	}
}

func TestCanonicalizationNormalizesNilAndEmptyCollections(t *testing.T) {
	t.Parallel()
	nilDraft := NewDraft("project-1", "mission-1")
	emptyDraft := NewDraft("project-1", "mission-1")
	for _, draft := range []*Draft{nilDraft, emptyDraft} {
		must(t, draft.AddLane(Lane{ID: "lane-a"}))
	}
	must(t, nilDraft.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a"}))
	must(t, emptyDraft.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a", Criteria: []Criterion{}, Dependencies: []ids.WorkID{}, SourceRevisions: []SourceRevision{}}))
	nilRevision, err := nilDraft.Finalize(testMetadata("revision-nil", 1))
	if err != nil {
		t.Fatal(err)
	}
	emptyRevision, err := emptyDraft.Finalize(testMetadata("revision-empty", 1))
	if err != nil {
		t.Fatal(err)
	}
	if nilRevision.GraphDigest() != emptyRevision.GraphDigest() || !bytes.Equal(nilRevision.CanonicalGraph(), emptyRevision.CanonicalGraph()) {
		t.Fatalf("nil and empty collections canonicalized differently:\n%s\n%s", nilRevision.CanonicalGraph(), emptyRevision.CanonicalGraph())
	}
}

func TestDigestIsMutationSensitiveForContractFields(t *testing.T) {
	t.Parallel()
	baseDraft := testDraft(t, false)
	base, err := baseDraft.Finalize(testMetadata("revision-base", 1))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Draft) error{
		"title": func(d *Draft) error {
			return d.ReplaceWork(WorkItem{ID: "work-b", Title: "changed", LaneID: "lane-b", Criteria: []Criterion{{ID: "criterion-b", Text: "test B"}}, Dependencies: []ids.WorkID{"work-a"}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "8"}}})
		},
		"criterion": func(d *Draft) error {
			return d.ReplaceWork(WorkItem{ID: "work-b", Title: "B", LaneID: "lane-b", Criteria: []Criterion{{ID: "criterion-b", Text: "changed"}}, Dependencies: []ids.WorkID{"work-a"}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "8"}}})
		},
		"dependency": func(d *Draft) error {
			return d.ReplaceWork(WorkItem{ID: "work-b", Title: "B", LaneID: "lane-b", Criteria: []Criterion{{ID: "criterion-b", Text: "test B"}}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "8"}}})
		},
		"lane-lineage": func(d *Draft) error { return d.ReplaceLane(Lane{ID: "lane-b"}) },
		"source-revision": func(d *Draft) error {
			return d.ReplaceWork(WorkItem{ID: "work-b", Title: "B", LaneID: "lane-b", Criteria: []Criterion{{ID: "criterion-b", Text: "test B"}}, Dependencies: []ids.WorkID{"work-a"}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "9"}}})
		},
	}
	for name, mutate := range cases {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			draft := testDraft(t, true)
			if err := mutate(draft); err != nil {
				t.Fatal(err)
			}
			got, err := draft.Finalize(testMetadata(ids.WorkRevisionID("revision-"+name), 2))
			if err != nil {
				t.Fatal(err)
			}
			if got.GraphDigest() == base.GraphDigest() {
				t.Fatalf("%s mutation did not alter graph digest", name)
			}
			if name == "criterion" && got.CriteriaDigest() == base.CriteriaDigest() {
				t.Fatal("criterion mutation did not alter criteria digest")
			}
		})
	}
}

func TestFinalizeRejectsGraphAndLaneCollapseViolations(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		build func(*Draft)
		kind  ErrorKind
	}{
		"ambiguous work id": {func(d *Draft) {
			must(t, d.AddLane(Lane{ID: "lane-a"}))
			must(t, d.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a", Criteria: []Criterion{{ID: "one", Text: "one"}}}))
			_ = d.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a", Criteria: []Criterion{{ID: "two", Text: "two"}}})
		}, KindAmbiguousID},
		"missing dependency": {func(d *Draft) {
			must(t, d.AddLane(Lane{ID: "lane-a"}))
			must(t, d.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a", Criteria: []Criterion{{ID: "one", Text: "one"}}, Dependencies: []ids.WorkID{"missing"}}))
		}, KindMissingNode},
		"missing lane": {func(d *Draft) {
			must(t, d.AddWork(WorkItem{ID: "work-a", LaneID: "missing", Criteria: []Criterion{{ID: "one", Text: "one"}}}))
		}, KindInvalidLane},
		"work cycle": {func(d *Draft) {
			must(t, d.AddLane(Lane{ID: "lane-a"}))
			must(t, d.AddWork(WorkItem{ID: "work-a", LaneID: "lane-a", Criteria: []Criterion{{ID: "one", Text: "one"}}, Dependencies: []ids.WorkID{"work-b"}}))
			must(t, d.AddWork(WorkItem{ID: "work-b", LaneID: "lane-a", Criteria: []Criterion{{ID: "two", Text: "two"}}, Dependencies: []ids.WorkID{"work-a"}}))
		}, KindCycle},
		"lane collapse cycle": {func(d *Draft) {
			must(t, d.AddLane(Lane{ID: "lane-a"}))
			must(t, d.AddLane(Lane{ID: "lane-b"}))
			must(t, d.AddWork(WorkItem{ID: "a1", LaneID: "lane-a", Criteria: []Criterion{{ID: "a1", Text: "a1"}}}))
			must(t, d.AddWork(WorkItem{ID: "b1", LaneID: "lane-b", Criteria: []Criterion{{ID: "b1", Text: "b1"}}, Dependencies: []ids.WorkID{"a1"}}))
			must(t, d.AddWork(WorkItem{ID: "b2", LaneID: "lane-b", Criteria: []Criterion{{ID: "b2", Text: "b2"}}}))
			must(t, d.AddWork(WorkItem{ID: "a2", LaneID: "lane-a", Criteria: []Criterion{{ID: "a2", Text: "a2"}}, Dependencies: []ids.WorkID{"b2"}}))
		}, KindLaneCycle},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			draft := NewDraft("project-1", "mission-1")
			test.build(draft)
			_, err := draft.Finalize(testMetadata("revision-bad", 1))
			if !IsKind(err, test.kind) {
				t.Fatalf("Finalize error = %v, want %s", err, test.kind)
			}
		})
	}
}

func testDraft(t *testing.T, reverse bool) *Draft {
	t.Helper()
	draft := NewDraft("project-1", "mission-1")
	lanes := []Lane{{ID: "lane-a"}, {ID: "lane-b", ParentID: "lane-a"}}
	items := []WorkItem{
		{ID: "work-a", Title: "A", LaneID: "lane-a", Criteria: []Criterion{{ID: "criterion-a", Text: "test A"}}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "7"}}},
		{ID: "work-b", Title: "B", LaneID: "lane-b", Criteria: []Criterion{{ID: "criterion-b", Text: "test B"}}, Dependencies: []ids.WorkID{"work-a"}, SourceRevisions: []SourceRevision{{Provider: "tracker", Revision: "8"}}},
	}
	if reverse {
		lanes[0], lanes[1] = lanes[1], lanes[0]
		items[0], items[1] = items[1], items[0]
	}
	for _, lane := range lanes {
		must(t, draft.AddLane(lane))
	}
	for _, item := range items {
		must(t, draft.AddWork(item))
	}
	return draft
}

func testMetadata(id ids.WorkRevisionID, number int64) FinalizeMetadata {
	return FinalizeMetadata{ID: id, Number: number, ActorID: "actor-owner", FinalizedAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
