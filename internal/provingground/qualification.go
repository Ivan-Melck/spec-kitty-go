package provingground

import (
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/faults"
)

// QualificationSnapshot is a copied, read-only view of the runtime facts a
// qualification observer must reconcile with the durable control journal and
// Git. It is not a restore or mutation capability.
type QualificationSnapshot struct {
	RuntimeEpochID   controljournal.RuntimeEpochID
	PredecessorEpoch controljournal.RuntimeEpochID
	Authorities      []Authority
	Workspaces       []WorkspaceBinding
}

// WithFaults installs the same production hook used by the composed runtime.
// Qualification callers can therefore cut the real GJ boundaries without a
// package-private test fixture.
func (r *Runtime) WithFaults(hook faults.Hook) *Runtime {
	if r != nil && r.core != nil {
		r.core.WithFaults(hook)
	}
	return r
}

// QualificationSnapshot returns defensive copies in stable identity order.
func (r *Runtime) QualificationSnapshot() QualificationSnapshot {
	if r == nil || r.core == nil {
		return QualificationSnapshot{}
	}
	projection := r.core.Projection()
	result := QualificationSnapshot{RuntimeEpochID: projection.Runtime.RuntimeEpochID, PredecessorEpoch: projection.Runtime.PredecessorEpoch}
	for _, work := range projection.Works {
		if work.Current != nil {
			result.Authorities = append(result.Authorities, *work.Current)
		}
	}
	result.Workspaces = append(result.Workspaces, projection.Workspaces...)
	sort.Slice(result.Authorities, func(i, j int) bool {
		if result.Authorities[i].WorkID != result.Authorities[j].WorkID {
			return result.Authorities[i].WorkID < result.Authorities[j].WorkID
		}
		return result.Authorities[i].LeaseID < result.Authorities[j].LeaseID
	})
	sort.Slice(result.Workspaces, func(i, j int) bool { return result.Workspaces[i].WorkspaceID < result.Workspaces[j].WorkspaceID })
	return result
}
