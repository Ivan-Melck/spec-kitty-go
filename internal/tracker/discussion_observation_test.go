package tracker

import (
	"testing"
	"time"
)

func TestAttributedDiscussionDefensiveCopiesAndProofKinds(t *testing.T) {
	proof, err := NewProviderObservation("issue", []byte(`{"kind":"original"}`), 1, "digest", "original", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewAttributedDiscussionEntry(DiscussionEntry{ExternalCommentID: "comment", Author: "claim", Body: "body", CreatedAt: time.Now(), Observation: 1}, "signer", proof)
	if err != nil {
		t.Fatal(err)
	}
	current := proof
	current.ProviderToken = "current"
	current.Revision = 2
	common := proof
	common.ProviderToken = "discussion"
	common.Revision = 3
	row := entry.Entry()
	row.Observation = 3
	entry, err = NewAttributedDiscussionEntry(row, "signer", common)
	if err != nil {
		t.Fatal(err)
	}
	input := []AttributedDiscussionEntry{entry}
	obs, err := NewDiscussionObservation(input, common, current)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = AttributedDiscussionEntry{}
	out := obs.Entries()
	out[0] = AttributedDiscussionEntry{}
	raw := obs.CommonProof().CanonicalFields()
	raw[0] = '!'
	if obs.Entries()[0].SigningActor() != "signer" || obs.CommonProof().CanonicalFields()[0] != '{' || obs.CurrentWorkObservation().Revision != 2 {
		t.Fatal("mutable alias or proof conflation")
	}
}
