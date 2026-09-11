package hubnot

import (
	"context"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"testing"
)

func TestCapabilitiesDescribeOnlyImplementedGuarantees(t *testing.T) {
	b, _ := operationFixture(t)
	caps, err := b.Capabilities(context.Background())
	if err != nil || caps.Validate() != nil {
		t.Fatalf("capabilities: %+v %v", caps, err)
	}
	if caps.CatalogRead != tracker.SupportSupported || caps.CatalogCreate != tracker.SupportSupported || caps.BlockingGraph != tracker.SupportSupported || caps.CatalogUpdate != tracker.UpdateUnguarded || caps.Assignment != tracker.AssignmentUnguarded || caps.Discussion != tracker.DiscussionIdempotent || caps.ObservationRevision != tracker.ObservationRevisionContentDerived || caps.ConcurrentWriters != tracker.ConcurrentWritersNone || caps.SafeCompensation != tracker.CompensationUnsupported || caps.PortableExport != tracker.SupportUnsupported || caps.RestorableBackup != tracker.SupportUnsupported {
		t.Fatalf("overstated capabilities: %+v", caps)
	}
	if caps.RequireCatalogUpdate(tracker.UpdateGuarded) == nil {
		t.Fatal("guard upgrade allowed")
	}
}
