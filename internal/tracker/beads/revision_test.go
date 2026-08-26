package beads

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"
)

func completeRevisionFixture() catalogObservation {
	title, description, design, acceptance := "Title", "Description", "Design", "Acceptance"
	assignee, marker := "actor-1", "operation-assign"
	updated := time.Date(2026, 8, 26, 11, 2, 3, 400, time.FixedZone("offset", -5*60*60))
	return catalogObservation{
		ExternalID: "bd-a1", Title: &title, Description: &description, DesignContext: &design, Acceptance: &acceptance,
		Metadata: []byte(`{"spec_kitty":{"schema_version":1}}`), Labels: []string{"z", "a"},
		Dependencies: []observationDependency{{Blocked: "work-2", Blocking: "work-1", Kind: "blocks", EdgeID: "edge-1"}},
		Assignee:     &assignee, AssignmentMarker: &marker,
		Comments:  []observationComment{{ID: "comment-1", Author: "human", Body: "body", Marker: "operation-comment", CreatedAt: "2026-08-26T15:00:00Z"}},
		UpdatedAt: &updated,
	}
}

func TestRevisionGoldenCanonicalDigest(t *testing.T) {
	got := deriveObservationIdentity(completeRevisionFixture())
	const wantDigest = "205536dc44a30f57abbdb5499859ca379c1bcf0ffcd5f19e60ffebb86cced332"
	if digestString(got.Digest) != wantDigest {
		t.Fatalf("digest=%s canonical=%s", digestString(got.Digest), hex.EncodeToString(got.Canonical))
	}
	if got.Revision == 0 || got.Revision != revisionFromDigest(got.Digest) {
		t.Fatalf("revision=%d", got.Revision)
	}
}

func TestRevisionUnorderedFieldsAreCanonical(t *testing.T) {
	left := completeRevisionFixture()
	right := completeRevisionFixture()
	right.Labels = []string{"a", "z"}
	right.Dependencies = append([]observationDependency(nil), left.Dependencies...)
	right.Dependencies = append(right.Dependencies, observationDependency{Blocked: "work-3", Blocking: "work-2", Kind: "blocks", EdgeID: "edge-2"})
	left.Dependencies = append([]observationDependency{right.Dependencies[1]}, left.Dependencies...)
	if a, b := deriveObservationIdentity(left), deriveObservationIdentity(right); a.Digest != b.Digest || !reflect.DeepEqual(a.Canonical, b.Canonical) {
		t.Fatalf("unordered input changed identity: %s != %s", digestString(a.Digest), digestString(b.Digest))
	}
}

func TestRevisionProviderSignificantOrderAndPresence(t *testing.T) {
	base := completeRevisionFixture()
	reordered := completeRevisionFixture()
	reordered.Comments = append(reordered.Comments, observationComment{ID: "comment-2"})
	withTwo := deriveObservationIdentity(reordered)
	reordered.Comments[0], reordered.Comments[1] = reordered.Comments[1], reordered.Comments[0]
	if withTwo.Digest == deriveObservationIdentity(reordered).Digest {
		t.Fatal("comment order did not change digest")
	}
	absent := base
	absent.Title = nil
	empty := base
	value := ""
	empty.Title = &value
	if deriveObservationIdentity(absent).Digest == deriveObservationIdentity(empty).Digest {
		t.Fatal("absent and empty collided")
	}
}

func TestRevisionEveryKnownScalarAndEqualTimestampDrift(t *testing.T) {
	base := completeRevisionFixture()
	want := deriveObservationIdentity(base).Digest
	mutations := []func(*catalogObservation){
		func(value *catalogObservation) { value.ExternalID += "x" },
		func(value *catalogObservation) { changed := *value.Title + "x"; value.Title = &changed },
		func(value *catalogObservation) { changed := *value.Description + "x"; value.Description = &changed },
		func(value *catalogObservation) { changed := *value.DesignContext + "x"; value.DesignContext = &changed },
		func(value *catalogObservation) { changed := *value.Acceptance + "x"; value.Acceptance = &changed },
		func(value *catalogObservation) { value.Metadata = []byte(`{"changed":true}`) },
		func(value *catalogObservation) { value.Labels = append(value.Labels, "new") },
		func(value *catalogObservation) { changed := *value.Assignee + "x"; value.Assignee = &changed },
		func(value *catalogObservation) {
			changed := *value.AssignmentMarker + "x"
			value.AssignmentMarker = &changed
		},
		func(value *catalogObservation) { value.UpdatedAt = timePointer(value.UpdatedAt.Add(time.Nanosecond)) },
	}
	for index, mutate := range mutations {
		candidate := completeRevisionFixture()
		mutate(&candidate)
		if deriveObservationIdentity(candidate).Digest == want {
			t.Fatalf("mutation %d did not change digest", index)
		}
	}
}

func TestRevisionTimestampUTCNormalizationAndRepeatability(t *testing.T) {
	left := completeRevisionFixture()
	right := completeRevisionFixture()
	sameInstant := left.UpdatedAt.In(time.FixedZone("other", 9*60*60))
	right.UpdatedAt = &sameInstant
	a, b := deriveObservationIdentity(left), deriveObservationIdentity(right)
	if a.Digest != b.Digest || a.Revision != b.Revision {
		t.Fatalf("timezone changed identity: %s %s", digestString(a.Digest), digestString(b.Digest))
	}
	for index := 0; index < 10; index++ {
		if got := deriveObservationIdentity(left); got.Digest != a.Digest || got.Revision != a.Revision {
			t.Fatalf("run %d changed identity", index)
		}
	}
}

func TestRevisionZeroPrefixFolding(t *testing.T) {
	var zero [sha256.Size]byte
	zero[8] = 9
	if got := revisionFromDigest(zero); got != 1 {
		t.Fatalf("zero prefix revision=%d", got)
	}
	var nonzero [sha256.Size]byte
	nonzero[7] = 2
	if got := revisionFromDigest(nonzero); got != 2 {
		t.Fatalf("network-order revision=%d", got)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
