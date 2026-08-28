package workgraph

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestLegacyRevisionGoldenBytesAndStrictVersionDispatch(t *testing.T) {
	revision, err := testDraft(t, false).Finalize(testMetadata("revision-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{"id":"revision-1","number":1,"actor_id":"actor-owner","finalized_at":"2026-08-23T12:00:00Z","graph_digest":"sha256:85a50672ca5dd666adc1ab4aa9c961a3096658704a529dc6c660c56424ca3911","criteria_digest":"sha256:3d25dd8b313d8a70d91b8c21d35c9f5323f3859dae64f8040d4c437f703ec057","graph":{"project_id":"project-1","mission_id":"mission-1","lanes":[{"id":"lane-a"},{"id":"lane-b","parent_id":"lane-a"}],"work_items":[{"id":"work-a","title":"A","lane_id":"lane-a","criteria":[{"id":"criterion-a","text":"test A"}],"dependencies":[],"source_revisions":[{"provider":"tracker","revision":"7"}]},{"id":"work-b","title":"B","lane_id":"lane-b","criteria":[{"id":"criterion-b","text":"test B"}],"dependencies":["work-a"],"source_revisions":[{"provider":"tracker","revision":"8"}]}]}}`
	if string(encoded) != golden {
		t.Fatalf("legacy golden mismatch:\n%s", encoded)
	}
	stored, err := DecodeStoredRevision(encoded)
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := stored.Legacy()
	if !ok || stored.SchemaVersion() != 1 || legacy.GraphDigest() != revision.GraphDigest() {
		t.Fatalf("legacy dispatch = %#v", stored)
	}
	reencoded, _ := legacy.Encode()
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("legacy decode/re-encode changed historical bytes")
	}

	v2, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	v2Bytes, _ := v2.Encode()
	stored, err = DecodeStoredRevision(v2Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.V2(); !ok || stored.SchemaVersion() != 2 {
		t.Fatalf("v2 dispatch = %#v", stored)
	}

	mutants := map[string][]byte{
		"unknown legacy field":      append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"unknown":true}`)...),
		"duplicate legacy field":    bytes.Replace(encoded, []byte(`"number":1`), []byte(`"number":1,"number":1`), 1),
		"trailing legacy value":     append(append([]byte(nil), encoded...), []byte(` {}`)...),
		"missing-version malformed": []byte(`{"id":"revision-1","number":1}`),
		"schema zero":               []byte(`{"schema_version":0}`),
		"future schema":             []byte(`{"schema_version":3}`),
		"legacy digest mismatch":    bytes.Replace(encoded, []byte(revision.GraphDigest()), []byte("sha256:"+strings.Repeat("0", 64)), 1),
		"missing dependencies":      bytes.Replace(encoded, []byte(`,"dependencies":[]`), nil, 1),
		"null dependencies":         bytes.Replace(encoded, []byte(`"dependencies":[]`), []byte(`"dependencies":null`), 1),
		"wrong dependencies type":   bytes.Replace(encoded, []byte(`"dependencies":[]`), []byte(`"dependencies":{}`), 1),
		"missing criterion text":    bytes.Replace(encoded, []byte(`,"text":"test A"`), nil, 1),
		"unknown criterion field":   bytes.Replace(encoded, []byte(`"text":"test A"`), []byte(`"text":"test A","unknown":true`), 1),
		"missing source revision":   bytes.Replace(encoded, []byte(`,"revision":"7"`), nil, 1),
		"null lanes":                bytes.Replace(encoded, mustLegacyJSONField(t, encoded, "graph", "lanes"), []byte("null"), 1),
		"explicit empty parent":     bytes.Replace(encoded, []byte(`{"id":"lane-a"}`), []byte(`{"id":"lane-a","parent_id":""}`), 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStoredRevision(mutant); !IsKind(err, KindInvalidRevision) {
				t.Fatalf("error = %v, want %s", err, KindInvalidRevision)
			}
		})
	}
}

func mustLegacyJSONField(t *testing.T, encoded []byte, parent, field string) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(root[parent], &nested); err != nil {
		t.Fatal(err)
	}
	return nested[field]
}

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
