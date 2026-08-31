package provingground

import (
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
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

// IntegrationQualificationSnapshot is a defensive census of the durable
// target authorities and item outcomes used to reconcile publication with
// independently observed Git state.
type IntegrationQualificationSnapshot struct {
	Authorities []IntegrationAuthorityQualification
	Records     []baseintegration.Record
}

type IntegrationAuthorityQualification struct {
	RepositoryID    ids.RepositoryID
	TargetRef       string
	Status          string
	ExpectedTarget  string
	CompletedTarget string
}

// QualificationSnapshot returns copied durable integration facts without
// exposing the coordinator's mutable workflow store.
func (c *IntegrationCoordinator) QualificationSnapshot() IntegrationQualificationSnapshot {
	if c == nil || c.reviews == nil || c.reviews.store == nil {
		return IntegrationQualificationSnapshot{}
	}
	c.reviews.store.mu.Lock()
	defer c.reviews.store.mu.Unlock()
	result := IntegrationQualificationSnapshot{}
	for _, authority := range c.reviews.store.planAuthorities {
		result.Authorities = append(result.Authorities, IntegrationAuthorityQualification{
			RepositoryID: authority.Plan.RepositoryID, TargetRef: authority.Plan.TargetRef,
			Status: authority.Status, ExpectedTarget: authority.Plan.ExpectedTarget,
			CompletedTarget: authority.CompletedTarget,
		})
	}
	for _, entry := range c.reviews.store.integrations {
		result.Records = append(result.Records, entry.Record)
	}
	sort.Slice(result.Authorities, func(i, j int) bool {
		if result.Authorities[i].RepositoryID != result.Authorities[j].RepositoryID {
			return result.Authorities[i].RepositoryID < result.Authorities[j].RepositoryID
		}
		return result.Authorities[i].TargetRef < result.Authorities[j].TargetRef
	})
	sort.Slice(result.Records, func(i, j int) bool { return result.Records[i].OperationID < result.Records[j].OperationID })
	return result
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
