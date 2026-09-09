package tracker

import (
	"context"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// InventoryPort observes complete current definitions under one common proof.
// It deliberately does not promise per-issue signed head or discussion evidence.
type InventoryPort interface {
	Inventory(context.Context, InventoryRequest) (InventoryObservation, error)
}
type InventoryRequest struct{ ProjectID ids.ProjectID }
type InventoryItem struct {
	definition WorkDefinition
	reference  ExternalWorkRef
}

func NewInventoryItem(definition WorkDefinition, reference ExternalWorkRef) (InventoryItem, error) {
	if reference.Validate() != nil || reference.CreatedByOperation == "" || reference.LastObservation == 0 || definition.ProjectID() != reference.ProjectID || definition.WorkID() != reference.WorkID || definition.MetadataSchema() != reference.MetadataSchema {
		return InventoryItem{}, fmt.Errorf("tracker inventory: definition and mapping disagree")
	}
	return InventoryItem{cloneDefinition(definition), reference}, nil
}
func (i InventoryItem) Definition() WorkDefinition   { return cloneDefinition(i.definition) }
func (i InventoryItem) ExternalRef() ExternalWorkRef { return i.reference }

type InventoryObservation struct {
	project ids.ProjectID
	items   []InventoryItem
	proof   ProviderObservation
}

func NewInventoryObservation(project ids.ProjectID, items []InventoryItem, proof ProviderObservation) (InventoryObservation, error) {
	if _, err := ids.ParseProjectID(project.String()); err != nil {
		return InventoryObservation{}, err
	}
	if proof.Revision == 0 || proof.Digest == "" || proof.ProviderToken == "" || len(proof.CanonicalFields()) == 0 || proof.ObservedAt.IsZero() {
		return InventoryObservation{}, fmt.Errorf("tracker inventory: common proof required")
	}
	seen := map[ids.WorkID]bool{}
	copyItems := make([]InventoryItem, len(items))
	for n, item := range items {
		r := item.ExternalRef()
		if r.ProjectID != project || seen[r.WorkID] || r.LastObservation != proof.Revision {
			return InventoryObservation{}, fmt.Errorf("tracker inventory: duplicate or incoherent mapping")
		}
		seen[r.WorkID] = true
		var err error
		copyItems[n], err = NewInventoryItem(item.Definition(), r)
		if err != nil {
			return InventoryObservation{}, err
		}
	}
	return InventoryObservation{project, copyItems, cloneProviderObservation(proof)}, nil
}
func (i InventoryObservation) ProjectID() ids.ProjectID { return i.project }
func (i InventoryObservation) Items() []InventoryItem {
	copyItems := make([]InventoryItem, len(i.items))
	for n, item := range i.items {
		copyItems[n] = InventoryItem{cloneDefinition(item.definition), item.reference}
	}
	return copyItems
}
func (i InventoryObservation) CommonProof() ProviderObservation {
	return cloneProviderObservation(i.proof)
}
