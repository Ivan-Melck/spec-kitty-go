package tracker

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// AttributedDiscussionEntry separates an accountable author claim from the
// transport signer. Neither field grants execution or independent review authority.
type AttributedDiscussionEntry struct {
	entry  DiscussionEntry
	signer string
	proof  ProviderObservation
}

func completeDiscussionProof(p ProviderObservation) bool {
	return p.ExternalID != "" && p.Revision != 0 && p.Digest != "" && p.ProviderToken != "" && !p.ObservedAt.IsZero() && len(p.CanonicalFields()) > 0
}
func NewAttributedDiscussionEntry(entry DiscussionEntry, signer string, proof ProviderObservation) (AttributedDiscussionEntry, error) {
	if entry.ExternalCommentID == "" || strings.TrimSpace(entry.Author) == "" || strings.TrimSpace(entry.Body) == "" || !utf8.ValidString(entry.Author) || !utf8.ValidString(entry.Body) || signer == "" || !utf8.ValidString(signer) || entry.CreatedAt.IsZero() || !completeDiscussionProof(proof) || entry.Observation != proof.Revision {
		return AttributedDiscussionEntry{}, fmt.Errorf("tracker discussion: complete entry, signer and matching proof required")
	}
	return AttributedDiscussionEntry{entry, signer, cloneProviderObservation(proof)}, nil
}
func (e AttributedDiscussionEntry) Entry() DiscussionEntry { return e.entry }
func (e AttributedDiscussionEntry) SigningActor() string   { return e.signer }
func (e AttributedDiscussionEntry) Proof() ProviderObservation {
	return cloneProviderObservation(e.proof)
}

// DiscussionObservation has a discussion-wide proof and a separately scoped
// current-work token suitable for a subsequent optimistic request. Their revisions
// are not interchangeable. Entries are covered by the common discussion proof.
type DiscussionObservation struct {
	entries     []AttributedDiscussionEntry
	proof       ProviderObservation
	currentWork ProviderObservation
}

func NewDiscussionObservation(entries []AttributedDiscussionEntry, proof, currentWork ProviderObservation) (DiscussionObservation, error) {
	if !completeDiscussionProof(proof) || !completeDiscussionProof(currentWork) || proof.ExternalID != currentWork.ExternalID {
		return DiscussionObservation{}, fmt.Errorf("tracker discussion: matching complete observation scopes required")
	}
	copied := make([]AttributedDiscussionEntry, len(entries))
	seen := map[string]bool{}
	for i, e := range entries {
		if e.proof.ExternalID != proof.ExternalID || e.proof.Revision != proof.Revision || e.proof.Digest != proof.Digest || e.proof.ProviderToken != proof.ProviderToken || string(e.proof.CanonicalFields()) != string(proof.CanonicalFields()) || seen[e.entry.ExternalCommentID] {
			return DiscussionObservation{}, fmt.Errorf("tracker discussion: duplicate or incoherent entry")
		}
		var err error
		copied[i], err = NewAttributedDiscussionEntry(e.entry, e.signer, proof)
		if err != nil {
			return DiscussionObservation{}, err
		}
		seen[e.entry.ExternalCommentID] = true
	}
	return DiscussionObservation{copied, cloneProviderObservation(proof), cloneProviderObservation(currentWork)}, nil
}
func (o DiscussionObservation) Entries() []AttributedDiscussionEntry {
	entries := make([]AttributedDiscussionEntry, len(o.entries))
	for i, e := range o.entries {
		entries[i] = AttributedDiscussionEntry{e.entry, e.signer, cloneProviderObservation(e.proof)}
	}
	return entries
}
func (o DiscussionObservation) CommonProof() ProviderObservation {
	return cloneProviderObservation(o.proof)
}
func (o DiscussionObservation) CurrentWorkObservation() ProviderObservation {
	return cloneProviderObservation(o.currentWork)
}

// AttributedDiscussionMutation retains original signed evidence only. There is
// intentionally no fabricated current-work token on a historical append/replay.
type AttributedDiscussionMutation struct {
	Entry         AttributedDiscussionEntry
	Replayed      bool
	Noop          bool
	PriorMutation bool
}
type AttributedDiscussionPort interface {
	AppendAttributedDiscussion(context.Context, AppendDiscussionRequest) (AttributedDiscussionMutation, error)
	ObserveDiscussion(context.Context, ExternalWorkRef) (DiscussionObservation, error)
}
