package hubnot

import (
	"context"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// Capabilities describes implemented portable behavior, never host authority.
func (b *BoundAdapter) Capabilities(ctx context.Context) (tracker.ProviderCapabilities, error) {
	if b == nil || ctx == nil {
		return tracker.ProviderCapabilities{}, invalid("bound adapter and context required", nil)
	}
	if err := ctx.Err(); err != nil {
		return tracker.ProviderCapabilities{}, err
	}
	return tracker.ProviderCapabilities{CatalogRead: tracker.SupportSupported, CatalogCreate: tracker.SupportSupported, CatalogUpdate: tracker.UpdateUnguarded, BlockingGraph: tracker.SupportSupported, Discussion: tracker.DiscussionIdempotent, Assignment: tracker.AssignmentUnguarded, ObservationRevision: tracker.ObservationRevisionContentDerived, SafeCompensation: tracker.CompensationUnsupported, ConcurrentWriters: tracker.ConcurrentWritersNone, PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported}, nil
}

var (
	_ tracker.Catalog                  = (*BoundAdapter)(nil)
	_ tracker.WorkDefinitionPort       = (*BoundAdapter)(nil)
	_ tracker.Graph                    = (*BoundAdapter)(nil)
	_ tracker.AssignmentProjection     = (*BoundAdapter)(nil)
	_ tracker.Discussion               = (*BoundAdapter)(nil)
	_ tracker.AttributedDiscussionPort = (*BoundAdapter)(nil)
	_ tracker.InventoryPort            = (*BoundAdapter)(nil)
	_ tracker.Inspector                = (*BoundAdapter)(nil)
)
