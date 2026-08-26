package governancecontext

import (
	"testing"
	"time"
)

func TestAggregateIsDeterministicAcrossOneHundredEvaluations(t *testing.T) {
	t.Parallel()

	entries := contextEntriesFixture(t)
	want, err := ComputeAggregate(SchemaVersion, entries)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		got, err := ComputeAggregate(SchemaVersion, entries)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("iteration %d aggregate=%+v want=%+v", iteration, got, want)
		}
	}
}

func TestAggregateCommitsEverySemanticFieldAndOrder(t *testing.T) {
	t.Parallel()

	base := contextEntriesFixture(t)
	want, err := ComputeAggregate(SchemaVersion, base)
	if err != nil {
		t.Fatal(err)
	}
	changedCommitment := commitmentFixture(t, "changed")
	tests := []struct {
		name    string
		schema  uint16
		entries []Entry
	}{
		{name: "schema", schema: 2, entries: base},
		{name: "entry count", schema: SchemaVersion, entries: base[:1]},
		{name: "order", schema: SchemaVersion, entries: []Entry{base[1], base[0]}},
		{name: "kind", schema: SchemaVersion, entries: []Entry{mustEntry(t, KindDoctrine, base[0].Ref(), base[0].Revision(), base[0].Commitment()), base[1]}},
		{name: "ref", schema: SchemaVersion, entries: []Entry{mustEntry(t, base[0].Kind(), "other-ref", base[0].Revision(), base[0].Commitment()), base[1]}},
		{name: "revision", schema: SchemaVersion, entries: []Entry{mustEntry(t, base[0].Kind(), base[0].Ref(), "other-revision", base[0].Commitment()), base[1]}},
		{name: "algorithm", schema: SchemaVersion, entries: []Entry{unsafeEntry(base[0].Kind(), base[0].Ref(), base[0].Revision(), Commitment{algorithm: "sha256-v2", digest: base[0].Commitment().Digest()}), base[1]}},
		{name: "digest", schema: SchemaVersion, entries: []Entry{mustEntry(t, base[0].Kind(), base[0].Ref(), base[0].Revision(), changedCommitment), base[1]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := computeAggregate(test.schema, test.entries)
			if err != nil && test.name != "algorithm" {
				t.Fatal(err)
			}
			if err == nil && got == want {
				t.Fatalf("aggregate did not change: %+v", got)
			}
		})
	}
}

func TestCreatedAtDoesNotAffectSemanticAggregate(t *testing.T) {
	t.Parallel()

	entries := contextEntriesFixture(t)
	first, err := NewContext("context-1", SchemaVersion, entries, time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewContext("context-1", SchemaVersion, entries, time.Date(2030, 1, 1, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if first.Aggregate() != second.Aggregate() {
		t.Fatalf("timestamps changed semantic identity: first=%+v second=%+v", first.Aggregate(), second.Aggregate())
	}
}

func contextEntriesFixture(t *testing.T) []Entry {
	t.Helper()
	return []Entry{
		entryFixture(t, KindConfig, "project-config", "revision-1", "config"),
		entryFixture(t, KindProfile, "implementer-ivan", "revision-2", "profile"),
	}
}

func mustEntry(t *testing.T, kind EntryKind, ref, revision string, commitment Commitment) Entry {
	t.Helper()
	entry, err := NewEntry(kind, ref, revision, commitment)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// unsafeEntry is deliberately package-local test construction. It proves the
// aggregate commits the algorithm field even before validation rejects it.
func unsafeEntry(kind EntryKind, ref, revision string, commitment Commitment) Entry {
	return Entry{kind: kind, ref: ref, revision: revision, commitment: commitment}
}
