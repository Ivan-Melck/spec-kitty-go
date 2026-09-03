package v2

import (
	"context"
	"sync"
)

const MaxInFlightPublicationsV2 = 1024

type CommitObservationV2 struct {
	publicationID   PublicationID
	planDigest      Digest
	recordSetDigest Digest
	revision        uint64
	durableThrough  uint64
	mutationCount   int
}

func NewCommitObservationV2(publicationID PublicationID, planDigest, recordSetDigest Digest, revision, durableThrough uint64, mutationCount int) (CommitObservationV2, error) {
	if _, err := DecodePublicationID(string(publicationID)); err != nil || planDigest.IsZero() || recordSetDigest.IsZero() {
		return CommitObservationV2{}, invalid(ErrorInvalidDigest, "commit_observation")
	}
	if revision == 0 || durableThrough < revision || mutationCount <= 0 || mutationCount > MaxConditionsPerPlan {
		return CommitObservationV2{}, invalid(ErrorInvalidVersion, "commit_observation")
	}
	return CommitObservationV2{publicationID: publicationID, planDigest: planDigest, recordSetDigest: recordSetDigest,
		revision: revision, durableThrough: durableThrough, mutationCount: mutationCount}, nil
}

func (o CommitObservationV2) PublicationID() PublicationID   { return o.publicationID }
func (o CommitObservationV2) PlanDigest() Digest             { return o.planDigest }
func (o CommitObservationV2) RecordSetDigest() Digest        { return o.recordSetDigest }
func (o CommitObservationV2) Revision() uint64               { return o.revision }
func (o CommitObservationV2) DurableThroughRevision() uint64 { return o.durableThrough }
func (o CommitObservationV2) MutationCount() int             { return o.mutationCount }

// DurablePublicationPortV2 is the only durable mutation capability accepted
// by the foundation coordinator. Capability packages receive the coordinator,
// not an implementation of this port.
type DurablePublicationPortV2 interface {
	Commit(context.Context, SealedCommitPlanV2) (CommitObservationV2, error)
}

type AcknowledgedPublicationV2 struct {
	plan        SealedCommitPlanV2
	observation CommitObservationV2
	seal        Digest
}

func (a AcknowledgedPublicationV2) PublicationID() PublicationID { return a.observation.publicationID }
func (a AcknowledgedPublicationV2) PlanDigest() Digest           { return a.observation.planDigest }
func (a AcknowledgedPublicationV2) RecordSetDigest() Digest      { return a.observation.recordSetDigest }
func (a AcknowledgedPublicationV2) ProviderRevision() uint64     { return a.observation.revision }
func (a AcknowledgedPublicationV2) DurableThroughRevision() uint64 {
	return a.observation.durableThrough
}
func (a AcknowledgedPublicationV2) MutationCount() int       { return a.observation.mutationCount }
func (a AcknowledgedPublicationV2) Plan() SealedCommitPlanV2 { return a.plan }

func acknowledgementSeal(plan SealedCommitPlanV2, observation CommitObservationV2) (Digest, error) {
	if err := validateSealedPlanIdentity(plan); err != nil {
		return Digest{}, err
	}
	canonical, err := marshalCanonical(struct {
		PublicationID   string `json:"publication_id"`
		PlanSHA256      string `json:"plan_sha256"`
		RecordSetSHA256 string `json:"record_set_sha256"`
		Revision        uint64 `json:"revision"`
		DurableThrough  uint64 `json:"durable_through_revision"`
		MutationCount   int    `json:"mutation_count"`
	}{string(observation.publicationID), observation.planDigest.String(), observation.recordSetDigest.String(), observation.revision, observation.durableThrough, observation.mutationCount}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.acknowledged-publication.v2", canonical), nil
}

func validateSealedPlanIdentity(plan SealedCommitPlanV2) error {
	if _, err := DecodePublicationID(string(plan.publicationID)); err != nil || plan.digest.IsZero() || plan.recordSetDigest.IsZero() ||
		len(plan.records) == 0 || len(plan.canonical) == 0 {
		return invalid(ErrorInvalidDigest, "sealed_plan")
	}
	recordDigest, err := sealedRecordSetDigest(plan.records)
	if err != nil || recordDigest != plan.recordSetDigest {
		return invalid(ErrorInvalidDigest, "sealed_record_set")
	}
	return nil
}

func validateAcknowledgement(ack AcknowledgedPublicationV2) error {
	seal, err := acknowledgementSeal(ack.plan, ack.observation)
	if err != nil || seal.IsZero() || seal != ack.seal || ack.observation.publicationID != ack.plan.publicationID ||
		ack.observation.planDigest != ack.plan.digest || ack.observation.recordSetDigest != ack.plan.recordSetDigest ||
		ack.observation.mutationCount != len(ack.plan.records) || ack.observation.revision == 0 ||
		ack.observation.durableThrough < ack.observation.revision {
		return invalid(ErrorInvalidDigest, "acknowledged_publication")
	}
	return nil
}

type publicationProjectionV2 interface {
	registerPublication(SealedCommitPlanV2) error
	applyAcknowledged(AcknowledgedPublicationV2) error
	sealPrefix() (SealedProjectionPrefixV2, error)
}

type registeredPublicationV2 struct {
	plan     SealedCommitPlanV2
	resolved bool
}

type PublicationCoordinatorV2 struct {
	mu         sync.Mutex
	port       DurablePublicationPortV2
	projection publicationProjectionV2
	registered map[PublicationID]registeredPublicationV2
	unresolved int
}

func NewPublicationCoordinatorV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2) (*PublicationCoordinatorV2, error) {
	if port == nil || projection == nil {
		return nil, invalid(ErrorInvalidCanonicalData, "publication_coordinator")
	}
	return &PublicationCoordinatorV2{port: port, projection: projection, registered: make(map[PublicationID]registeredPublicationV2)}, nil
}

// Publish registers before invoking the port and never holds the coordinator
// mutex across provider I/O. Any uncertain or malformed outcome remains
// registered and therefore blocks a checkpoint barrier.
func (c *PublicationCoordinatorV2) Publish(ctx context.Context, plan SealedCommitPlanV2) (AcknowledgedPublicationV2, error) {
	if err := c.register(plan); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	observation, err := c.port.Commit(ctx, plan)
	if err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	return c.applyObservation(plan, observation)
}

func (c *PublicationCoordinatorV2) register(plan SealedCommitPlanV2) error {
	if err := validateSealedPlanIdentity(plan); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unresolved >= MaxInFlightPublicationsV2 {
		return invalid(ErrorBudgetExceeded, "in_flight_publications")
	}
	if _, exists := c.registered[plan.publicationID]; exists {
		return invalid(ErrorAmbiguous, "publication_registered")
	}
	if err := c.projection.registerPublication(plan); err != nil {
		return err
	}
	c.registered[plan.publicationID] = registeredPublicationV2{plan: plan}
	c.unresolved++
	return nil
}

// ReconcileComplete accepts the same exact observation proof as a successful
// delivery response. It cannot acknowledge an unregistered or substituted
// plan.
func (c *PublicationCoordinatorV2) ReconcileComplete(plan SealedCommitPlanV2, observation CommitObservationV2) (AcknowledgedPublicationV2, error) {
	return c.applyObservation(plan, observation)
}

func (c *PublicationCoordinatorV2) applyObservation(plan SealedCommitPlanV2, observation CommitObservationV2) (AcknowledgedPublicationV2, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	registered, exists := c.registered[plan.publicationID]
	if !exists || registered.resolved || registered.plan.digest != plan.digest || registered.plan.recordSetDigest != plan.recordSetDigest {
		return AcknowledgedPublicationV2{}, invalid(ErrorAmbiguous, "publication_not_registered")
	}
	if observation.publicationID != plan.publicationID || observation.planDigest != plan.digest ||
		observation.recordSetDigest != plan.recordSetDigest || observation.mutationCount != len(plan.records) ||
		observation.revision == 0 || observation.durableThrough < observation.revision {
		return AcknowledgedPublicationV2{}, invalid(ErrorInvalidDigest, "commit_observation_attribution")
	}
	seal, err := acknowledgementSeal(plan, observation)
	if err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	ack := AcknowledgedPublicationV2{plan: plan, observation: observation, seal: seal}
	if err := c.projection.applyAcknowledged(ack); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	registered.resolved = true
	c.registered[plan.publicationID] = registered
	c.unresolved--
	return ack, nil
}

func (c *PublicationCoordinatorV2) UnresolvedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unresolved
}

// SealProjectionPrefix is a short local barrier. It serializes registration,
// acknowledgement application, and sealing, but never provider delivery.
func (c *PublicationCoordinatorV2) SealProjectionPrefix() (SealedProjectionPrefixV2, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unresolved != 0 {
		return SealedProjectionPrefixV2{}, invalid(ErrorAmbiguous, "unresolved_publication")
	}
	sealed, err := c.projection.sealPrefix()
	if err != nil {
		return SealedProjectionPrefixV2{}, err
	}
	for id, registered := range c.registered {
		if registered.resolved {
			delete(c.registered, id)
		}
	}
	return sealed, nil
}
