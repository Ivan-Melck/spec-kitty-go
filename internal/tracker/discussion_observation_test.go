package tracker

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"strings"
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

// Measure modest whole-pipeline sizes, including the public proof constructor.
// The allowance is linear in proof bytes + rows, not an elapsed-time threshold.
func TestDiscussionObservationProofAllocationScalesLinearly(t *testing.T) {
	for _, n := range []int{32, 64, 128} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			raw := []byte(`{"body":"` + strings.Repeat("x", n*1024) + `"}`)
			ids := make([]string, n)
			for i := range ids {
				ids[i] = fmt.Sprint(i)
			}
			now := time.Now()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			proof, err := NewProviderObservation("issue", raw, 1, "digest", "common", now)
			if err != nil {
				t.Fatal(err)
			}
			current := proof
			current.Revision, current.ProviderToken = 2, "current"
			entries := make([]AttributedDiscussionEntry, n)
			for i := range entries {
				entries[i], err = NewAttributedDiscussionEntry(DiscussionEntry{ExternalCommentID: ids[i], Author: "author", Body: "body", CreatedAt: now, Observation: 1}, "signer", proof)
				if err != nil {
					t.Fatal(err)
				}
			}
			observation, err := NewDiscussionObservation(entries, proof, current)
			if err != nil {
				t.Fatal(err)
			}
			returned := observation.Entries()
			runtime.ReadMemStats(&after)
			allocated := after.TotalAlloc - before.TotalAlloc
			limit := uint64(8*len(raw) + 4096*n + 65536)
			if allocated > limit {
				t.Errorf("pipeline allocated %d bytes; linear allowance %d for %d rows / %d proof bytes", allocated, limit, n, len(raw))
			}
			retained := map[*byte]int{}
			keep := func(p ProviderObservation) {
				if len(p.canonical) > 0 {
					retained[&p.canonical[0]] = len(p.canonical)
				}
			}
			keep(observation.proof)
			keep(observation.currentWork)
			for i := range entries {
				keep(entries[i].proof)
				keep(observation.entries[i].proof)
				keep(returned[i].proof)
			}
			total := 0
			for _, size := range retained {
				total += size
			}
			if total > 2*len(raw) {
				t.Errorf("retained %d distinct proof bytes for %d-byte common payload", total, len(raw))
			}
			runtime.KeepAlive(observation)
			runtime.KeepAlive(entries)
			runtime.KeepAlive(returned)
			t.Logf("rows=%d proof=%d allocated=%d retained=%d", n, len(raw), allocated, total)
		})
	}
}

func TestDiscussionObservationAllPublicMutationBoundaries(t *testing.T) {
	raw := []byte(`{"body":"original"}`)
	proof, err := NewProviderObservation("issue", raw, 1, "digest", "original", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	original := proof
	row := DiscussionEntry{ExternalCommentID: "comment", Author: "author", Body: "body", CreatedAt: time.Now(), Observation: 1}
	entry, err := NewAttributedDiscussionEntry(row, "signer", proof)
	if err != nil {
		t.Fatal(err)
	}
	common := proof
	common.Revision, common.ProviderToken = 2, "common"
	current := proof
	current.Revision, current.ProviderToken = 3, "current"
	row.Observation = 2
	observedEntry, err := NewAttributedDiscussionEntry(row, "signer", common)
	if err != nil {
		t.Fatal(err)
	}
	input := []AttributedDiscussionEntry{observedEntry}
	observation, err := NewDiscussionObservation(input, common, current)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = AttributedDiscussionEntry{}
	raw[0] = '!'
	proof.ExternalID, proof.Revision, proof.ProviderToken = "changed", 99, "changed"
	mutate := func(p ProviderObservation) {
		p.ExternalID, p.Revision, p.Digest, p.ProviderToken = "changed", 99, "changed", "changed"
		p.Assignee, p.OperationMarker = "changed", "changed"
		p.ProviderUpdatedAt, p.ObservedAt = time.Time{}, time.Time{}
		p.ProviderUpdatedAtPresent = !p.ProviderUpdatedAtPresent
		p.CanonicalFields()[0] = '!'
	}
	mutate(entry.Proof())
	mutate(observation.CommonProof())
	mutate(observation.CurrentWorkObservation())
	out := observation.Entries()
	mutate(out[0].Proof())
	returnedRow := out[0].Entry()
	returnedRow.Author, returnedRow.Body = "changed", "changed"
	out[0] = AttributedDiscussionEntry{}
	if !reflect.DeepEqual(entry.Proof(), original) || !reflect.DeepEqual(observation.CommonProof(), common) || !reflect.DeepEqual(observation.CurrentWorkObservation(), current) || !reflect.DeepEqual(observation.Entries()[0], observedEntry) || !bytes.Equal(original.CanonicalFields(), []byte(`{"body":"original"}`)) {
		t.Fatal("public input or returned value mutation changed stored proof/entry")
	}
}

func TestDiscussionObservationCoherenceGuards(t *testing.T) {
	proof, err := NewProviderObservation("issue", []byte(`{"body":"original"}`), 1, "digest", "token", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewAttributedDiscussionEntry(DiscussionEntry{ExternalCommentID: "comment", Author: "author", Body: "body", CreatedAt: time.Now(), Observation: 1}, "signer", proof)
	if err != nil {
		t.Fatal(err)
	}
	// Equal independently allocated payloads remain valid; pointer identity is only a fast path.
	independent, err := NewProviderObservation(proof.ExternalID, proof.CanonicalFields(), proof.Revision, proof.Digest, proof.ProviderToken, proof.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDiscussionObservation([]AttributedDiscussionEntry{entry}, independent, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDiscussionObservation([]AttributedDiscussionEntry{entry, entry}, proof, proof); err == nil {
		t.Fatal("accepted duplicate entry")
	}
	for name, change := range map[string]func(*AttributedDiscussionEntry){
		"identity":       func(e *AttributedDiscussionEntry) { e.proof.ExternalID = "different" },
		"revision":       func(e *AttributedDiscussionEntry) { e.proof.Revision++ },
		"entry-revision": func(e *AttributedDiscussionEntry) { e.entry.Observation++ },
		"digest":         func(e *AttributedDiscussionEntry) { e.proof.Digest = "different" },
		"token":          func(e *AttributedDiscussionEntry) { e.proof.ProviderToken = "different" },
		"content":        func(e *AttributedDiscussionEntry) { e.proof.canonical = []byte(`{"body":"differnt"}`) },
		"length":         func(e *AttributedDiscussionEntry) { e.proof.canonical = append(e.proof.CanonicalFields(), ' ') },
		"absent-content": func(e *AttributedDiscussionEntry) { e.proof.canonical = nil },
		"signer":         func(e *AttributedDiscussionEntry) { e.signer = "" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := entry
			change(&invalid)
			if _, err := NewDiscussionObservation([]AttributedDiscussionEntry{invalid}, proof, proof); err == nil {
				t.Fatal("accepted incoherent entry")
			}
		})
	}
	for name, change := range map[string]func(*ProviderObservation){
		"identity": func(p *ProviderObservation) { p.ExternalID = "" },
		"revision": func(p *ProviderObservation) { p.Revision = 0 },
		"digest":   func(p *ProviderObservation) { p.Digest = "" },
		"token":    func(p *ProviderObservation) { p.ProviderToken = "" },
		"time":     func(p *ProviderObservation) { p.ObservedAt = time.Time{} },
		"content":  func(p *ProviderObservation) { p.canonical = nil },
	} {
		t.Run("incomplete-"+name, func(t *testing.T) {
			invalid := proof
			change(&invalid)
			if _, err := NewAttributedDiscussionEntry(entry.Entry(), "signer", invalid); err == nil {
				t.Fatal("accepted incomplete original proof")
			}
			if _, err := NewDiscussionObservation(nil, invalid, proof); err == nil {
				t.Fatal("accepted incomplete common proof")
			}
			if _, err := NewDiscussionObservation(nil, proof, invalid); err == nil {
				t.Fatal("accepted incomplete current proof")
			}
		})
	}
	different := proof
	different.ExternalID = "another-issue"
	if _, err := NewDiscussionObservation(nil, proof, different); err == nil {
		t.Fatal("accepted mismatched current work")
	}
}
