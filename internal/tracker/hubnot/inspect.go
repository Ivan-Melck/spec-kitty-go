package hubnot

import (
	"context"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// Inspect rechecks the exact binding and public signer without initialization,
// repair or any private key access. Health describes transport discovery only.
func (b *BoundAdapter) Inspect(ctx context.Context, request tracker.InspectRequest) (tracker.Inspection, error) {
	if err := b.checkProject(request.ProjectID); err != nil {
		return tracker.Inspection{}, err
	}
	if err := b.verifyDispatch(); err != nil {
		return tracker.Inspection{}, err
	}
	fresh, err := Open(ctx, b.config)
	if err != nil {
		return tracker.Inspection{}, err
	}
	caps, err := fresh.Capabilities(ctx)
	if err != nil {
		return tracker.Inspection{}, err
	}
	diagnostics := []tracker.Diagnostic{}
	if fresh.binding.ReadOnly {
		diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorCapabilityUnsupported, RecoveryCode: "use_writable_binding", Message: "binding is read-only; mutation is unavailable"})
	}
	return tracker.NewInspection(fresh.Binding(), caps, tracker.HealthHealthy, diagnostics)
}
