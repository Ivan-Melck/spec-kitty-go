package productionruntime

import (
	"context"
	"errors"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// No setter/factory is public. ONLY production start, after successful bootstrap,
// freezes these original concrete objects. Adapter/lifecycle constructor shape
// alone does not establish a Host's original composition.
type retainedHostComposition struct {
	host       *Host
	owner      *productionOwner
	lifecycle  *sidecargapdb.Lifecycle
	adapter    *storegapdb.Adapter
	projection *projection.AuthorityV2
	database   journalv2.DatabaseID
	epoch      journalv2.RuntimeEpochID
	binding    AuthorityBindingV2
}

// RetainedRecordsProof is immutable matched-only scalar evidence for a bounded
// observation interval. No unknown/actual provider count, mutable bytes, pointers
// or private identities escape. This is NOT original ACK-database/writer/CAS,
// historical eligibility, erased-history, physical or restart equivalence.
type RetainedRecordsProof struct {
	attribution           projection.AttributionV2
	publications, private uint64
}

func (p RetainedRecordsProof) Attribution() projection.AttributionV2 { return p.attribution }
func (p RetainedRecordsProof) MatchedPublicationRecords() uint64     { return p.publications }
func (p RetainedRecordsProof) MatchedPrivateRecords() uint64         { return p.private }
func (p RetainedRecordsProof) MatchedRecords() uint64                { return p.publications + p.private }

func retainedRecordsError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return proofIncomplete("retained_records")
}

// capture holds only the existing scalar locks, never a command barrier or
// flat Snapshot. The rolling cursor includes operation-only prefix changes.
func (h *Host) retainedCapture(c *retainedHostComposition) (projection.AttributionV2, bool) {
	if h == nil || c == nil {
		return projection.AttributionV2{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.retained != c || c.host != h || c.owner == nil || c.lifecycle == nil || c.adapter == nil || c.projection == nil || h.state != HostReady || h.owner != c.owner || h.projection != c.projection || h.databaseID != c.database || h.epoch != c.epoch || c.owner.lifecycle != c.lifecycle || c.owner.adapter != c.adapter || c.owner.database != c.database || c.owner.binding != c.binding || c.binding.DatabaseID != c.database {
		return projection.AttributionV2{}, false
	}
	a := c.projection.Attribution()
	if a.DatabaseID != c.database || a.RuntimeEpochID != c.epoch || a.Revision == 0 || a.DurableThrough != a.Revision || a.CursorToken.IsZero() {
		return projection.AttributionV2{}, false
	}
	return a, true
}

// VerifyRetainedRecords takes only context and actual opaque coordinator ACKs.
// No refresh, write, snapshot, retry, admission or projection mutation occurs.
// Thirty seconds is cooperative context cancellation, NOT a hard-wall promise.
func (h *Host) VerifyRetainedRecords(parent context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2) (RetainedRecordsProof, error) {
	refuse := func(ctx context.Context, err error) (RetainedRecordsProof, error) {
		return RetainedRecordsProof{}, retainedRecordsError(ctx, err)
	}
	if parent == nil || parent.Err() != nil || h == nil {
		return refuse(parent, nil)
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	h.mu.Lock()
	composition := h.retained
	h.mu.Unlock()
	attribution, ok := h.retainedCapture(composition)
	if !ok {
		return refuse(ctx, nil)
	}
	captured, err := journalv2.NewStoreRevisionV2(attribution.DatabaseID, attribution.Revision, attribution.DurableThrough)
	if err != nil {
		return refuse(ctx, err)
	}
	session, err := composition.adapter.PrepareRetainedObservationV2(ctx, captured, acknowledgements)
	if err != nil {
		return refuse(ctx, err)
	}
	defer session.Close()
	if err := session.ReadPublications(); err != nil {
		return refuse(ctx, err)
	}
	privateContext, err := session.PrivateContext()
	if err != nil {
		return refuse(ctx, err)
	}
	// Exact ORIGINAL adapter.Get. Its child request context inherits the private
	// budget key; there is no alternative provider/helper callback surface.
	private, err := observeRetainedPrivateRecords(privateContext, composition.adapter, composition.binding, composition.epoch, captured, defaultRetainedPrivatePolicy())
	if err != nil {
		return refuse(ctx, err)
	}
	privateCount := uint64(len(private.rows)) // actually visited rows, never 3*n+3
	pubs, err := session.Finish(privateCount)
	if err != nil {
		return refuse(ctx, err)
	}
	closing, ok := h.retainedCapture(composition)
	if !ok || closing != attribution {
		return refuse(ctx, nil)
	}
	if err := session.CheckLifetime(); err != nil {
		return refuse(ctx, err)
	}
	// Also close over time spent in the final full image hash. No renewal.
	closing, ok = h.retainedCapture(composition)
	if !ok || closing != attribution || ctx.Err() != nil || parent.Err() != nil {
		return refuse(parent, ctx.Err())
	}
	return RetainedRecordsProof{attribution: attribution, publications: pubs, private: privateCount}, nil
}
