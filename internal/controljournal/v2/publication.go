package v2

import (
	"bytes"
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
	assertionCount  int
}

func NewCommitObservationV2(publicationID PublicationID, planDigest, recordSetDigest Digest, revision, durableThrough uint64, mutationCount int) (CommitObservationV2, error) {
	return NewCommitObservationWithAssertionsV2(publicationID, planDigest, recordSetDigest, revision, durableThrough, mutationCount, 0)
}

func NewCommitObservationWithAssertionsV2(publicationID PublicationID, planDigest, recordSetDigest Digest, revision, durableThrough uint64, mutationCount, assertionCount int) (CommitObservationV2, error) {
	if _, err := DecodePublicationID(string(publicationID)); err != nil || planDigest.IsZero() || recordSetDigest.IsZero() {
		return CommitObservationV2{}, invalid(ErrorInvalidDigest, "commit_observation")
	}
	if revision == 0 || durableThrough < revision || mutationCount <= 0 || mutationCount > MaxMembers || assertionCount < 0 || assertionCount > MaxMembers || mutationCount+assertionCount > MaxMembers {
		return CommitObservationV2{}, invalid(ErrorInvalidVersion, "commit_observation")
	}
	return CommitObservationV2{publicationID: publicationID, planDigest: planDigest, recordSetDigest: recordSetDigest,
		revision: revision, durableThrough: durableThrough, mutationCount: mutationCount, assertionCount: assertionCount}, nil
}

func (o CommitObservationV2) PublicationID() PublicationID   { return o.publicationID }
func (o CommitObservationV2) PlanDigest() Digest             { return o.planDigest }
func (o CommitObservationV2) RecordSetDigest() Digest        { return o.recordSetDigest }
func (o CommitObservationV2) Revision() uint64               { return o.revision }
func (o CommitObservationV2) DurableThroughRevision() uint64 { return o.durableThrough }
func (o CommitObservationV2) MutationCount() int             { return o.mutationCount }
func (o CommitObservationV2) AssertionCount() int            { return o.assertionCount }

// DurablePublicationPortV2 is the only durable mutation capability accepted
// by the foundation coordinator. Capability packages receive the coordinator,
// not an implementation of this port.
type DurablePublicationPortV2 interface {
	Commit(context.Context, SealedCommitPlanV2) (CommitObservationV2, error)
}

// PublicationBoundaryStageV2 names the three authority-neutral points at
// which a qualification campaign may interrupt publication. These points are
// deliberately inside the coordinator: provider adapters and capability
// services cannot move or reinterpret them.
type PublicationBoundaryStageV2 string

const (
	PublicationRegisteredBeforeCommitV2     PublicationBoundaryStageV2 = "registered_before_commit"
	PublicationDurableObservedBeforeAckV2   PublicationBoundaryStageV2 = "durable_observed_before_ack"
	PublicationAckCreatedBeforeProjectionV2 PublicationBoundaryStageV2 = "ack_created_before_projection"
)

// PublicationBoundarySnapshotV2 contains immutable, already-sealed values.
// It intentionally exposes no provider response bytes, credentials, or
// projection mutation handle. SealedCommitPlanV2's accessors defensively copy
// every variable-sized member.
type PublicationBoundarySnapshotV2 struct {
	Stage       PublicationBoundaryStageV2
	Plan        SealedCommitPlanV2
	Observation *CommitObservationV2
	Ack         *AcknowledgedPublicationV2
}

// QualificationPublicationBoundaryProbeV2 is an internal-module campaign
// capability. Production composition never receives this interface. A probe
// has no authority-bearing return value: it may observe sealed identities or
// return the one typed interruption.
type QualificationPublicationBoundaryProbeV2 interface {
	PublicationBoundary(context.Context, PublicationBoundarySnapshotV2) error
}

// PublicationBoundaryObserverV2 is the narrower runtime seam. It can capture
// immutable evidence, but it cannot influence publication control flow.
type PublicationBoundaryObserverV2 interface {
	ObservePublicationBoundary(context.Context, PublicationBoundarySnapshotV2)
}

type publicationBoundaryObserverProbeV2 struct{ observer PublicationBoundaryObserverV2 }

func (p publicationBoundaryObserverProbeV2) PublicationBoundary(ctx context.Context, snapshot PublicationBoundarySnapshotV2) error {
	p.observer.ObservePublicationBoundary(ctx, snapshot)
	return nil
}

type QualificationPublicationInterruptionV2 struct{ Stage PublicationBoundaryStageV2 }

func (e *QualificationPublicationInterruptionV2) Error() string {
	if e == nil {
		return "<nil>"
	}
	return "publication boundary interrupted: " + string(e.Stage)
}

func InterruptQualificationPublicationV2(stage PublicationBoundaryStageV2) error {
	if !validPublicationBoundaryStageV2(stage) {
		return invalid(ErrorInvalidCanonicalData, "publication_boundary_stage")
	}
	return &QualificationPublicationInterruptionV2{Stage: stage}
}

func validPublicationBoundaryStageV2(stage PublicationBoundaryStageV2) bool {
	switch stage {
	case PublicationRegisteredBeforeCommitV2, PublicationDurableObservedBeforeAckV2, PublicationAckCreatedBeforeProjectionV2:
		return true
	default:
		return false
	}
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
func (a AcknowledgedPublicationV2) AssertionCount() int      { return a.observation.assertionCount }
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
		AssertionCount  int    `json:"assertion_count"`
	}{string(observation.publicationID), observation.planDigest.String(), observation.recordSetDigest.String(), observation.revision, observation.durableThrough, observation.mutationCount, observation.assertionCount}, MaxCanonicalPayloadBytes)
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
	witnesses := 0
	for _, record := range plan.records {
		if record.role != RecordPublicationWitness {
			continue
		}
		witnesses++
		decoded, err := DecodePublicationWitnessV2(record.canonical)
		if err != nil || decoded != plan.witness || record.target != (SealedRecordTargetV2{publicationID: plan.publicationID}) ||
			record.valueClass != RecordValueOrdinary || record.condition.kind != ConditionAbsent || record.condition.observedRevision != 0 {
			return invalid(ErrorInvalidDigest, "publication_witness")
		}
	}
	if witnesses != 1 || plan.witness.publicationID != plan.publicationID || plan.witness.operationID != plan.operationState.operationID ||
		plan.witness.logicalVersion != plan.operationState.logicalVersion || plan.witness.planDigest != plan.digest ||
		plan.witness.recordSetDigest != plan.recordSetDigest || int(plan.witness.recordCount)+1 != len(plan.records) ||
		ValidatePublicationWitnessV2(plan.witness) != nil {
		return invalid(ErrorInvalidDigest, "publication_witness")
	}
	recordDigest, err := sealedRecordSetDigest(plan.records)
	if err != nil || recordDigest != plan.recordSetDigest {
		return invalid(ErrorInvalidDigest, "sealed_record_set")
	}
	canonical, err := plan.finalBytes()
	if err != nil || !bytes.Equal(canonical, plan.canonical) {
		return invalid(ErrorInvalidDigest, "sealed_plan")
	}
	return nil
}

func validateAcknowledgement(ack AcknowledgedPublicationV2) error {
	seal, err := acknowledgementSeal(ack.plan, ack.observation)
	if err != nil || seal.IsZero() || seal != ack.seal || ack.observation.publicationID != ack.plan.publicationID ||
		ack.observation.planDigest != ack.plan.digest || ack.observation.recordSetDigest != ack.plan.recordSetDigest ||
		ack.observation.mutationCount != len(ack.plan.records) || ack.observation.assertionCount != len(ack.plan.assertions) || ack.observation.revision == 0 ||
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

// AcknowledgedPublicationSinkV2 receives only a fully validated
// acknowledgement after the coordinator's projection tracker has reduced it.
// It cannot alter the sealed plan, provider observation, acknowledgement, or
// durable result. Production uses this narrow seam to advance query memory
// without replaying the complete journal after every successful command.
type AcknowledgedPublicationSinkV2 interface {
	AdvanceAcknowledgedPublicationV2(context.Context, AcknowledgedPublicationV2) error
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
	probe      QualificationPublicationBoundaryProbeV2
	sink       AcknowledgedPublicationSinkV2
}

func NewPublicationCoordinatorV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2) (*PublicationCoordinatorV2, error) {
	return newPublicationCoordinatorWithProbeV2(port, projection, nil)
}

// NewQualificationPublicationCoordinatorV2 is the only fault-capable
// constructor. Architecture guards confine its callers to test/qualification
// sources; productionruntime uses the observer-only constructor below.
func NewQualificationPublicationCoordinatorV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2, probe QualificationPublicationBoundaryProbeV2) (*PublicationCoordinatorV2, error) {
	return newPublicationCoordinatorWithProbeV2(port, projection, probe)
}

func newPublicationCoordinatorWithProbeV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2, probe QualificationPublicationBoundaryProbeV2) (*PublicationCoordinatorV2, error) {
	if port == nil || projection == nil {
		return nil, invalid(ErrorInvalidCanonicalData, "publication_coordinator")
	}
	return &PublicationCoordinatorV2{port: port, projection: projection, registered: make(map[PublicationID]registeredPublicationV2), probe: probe}, nil
}

func NewPublicationCoordinatorWithObserverV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2, observer PublicationBoundaryObserverV2) (*PublicationCoordinatorV2, error) {
	return NewPublicationCoordinatorWithObserverAndSinkV2(port, projection, observer, nil)
}

func NewPublicationCoordinatorWithObserverAndSinkV2(port DurablePublicationPortV2, projection *ProjectionTrackerV2, observer PublicationBoundaryObserverV2, sink AcknowledgedPublicationSinkV2) (*PublicationCoordinatorV2, error) {
	var coordinator *PublicationCoordinatorV2
	var err error
	if observer == nil {
		coordinator, err = NewPublicationCoordinatorV2(port, projection)
	} else {
		coordinator, err = newPublicationCoordinatorWithProbeV2(port, projection, publicationBoundaryObserverProbeV2{observer: observer})
	}
	if err != nil {
		return nil, err
	}
	coordinator.sink = sink
	return coordinator, nil
}

// Publish registers before invoking the port and never holds the coordinator
// mutex across provider I/O. Any uncertain or malformed outcome remains
// registered and therefore blocks a checkpoint barrier.
func (c *PublicationCoordinatorV2) Publish(ctx context.Context, plan SealedCommitPlanV2) (AcknowledgedPublicationV2, error) {
	if err := c.register(plan); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	if err := c.probeBoundary(ctx, PublicationBoundarySnapshotV2{Stage: PublicationRegisteredBeforeCommitV2, Plan: plan}); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	observation, err := c.port.Commit(ctx, plan)
	if err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	return c.applyObservation(ctx, plan, observation)
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
	return c.applyObservation(context.Background(), plan, observation)
}

func (c *PublicationCoordinatorV2) applyObservation(ctx context.Context, plan SealedCommitPlanV2, observation CommitObservationV2) (AcknowledgedPublicationV2, error) {
	if err := c.validateObservation(plan, observation); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	if err := c.probeBoundary(ctx, PublicationBoundarySnapshotV2{Stage: PublicationDurableObservedBeforeAckV2, Plan: plan, Observation: &observation}); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	seal, err := acknowledgementSeal(plan, observation)
	if err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	ack := AcknowledgedPublicationV2{plan: plan, observation: observation, seal: seal}
	if err := c.probeBoundary(ctx, PublicationBoundarySnapshotV2{Stage: PublicationAckCreatedBeforeProjectionV2, Plan: plan, Observation: &observation, Ack: &ack}); err != nil {
		return AcknowledgedPublicationV2{}, err
	}
	return c.applyAcknowledgement(ctx, plan, ack)
}

func (c *PublicationCoordinatorV2) validateObservation(plan SealedCommitPlanV2, observation CommitObservationV2) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	registered, exists := c.registered[plan.publicationID]
	if !exists || registered.resolved || registered.plan.digest != plan.digest || registered.plan.recordSetDigest != plan.recordSetDigest {
		return invalid(ErrorAmbiguous, "publication_not_registered")
	}
	if observation.publicationID != plan.publicationID || observation.planDigest != plan.digest ||
		observation.recordSetDigest != plan.recordSetDigest || observation.mutationCount != len(plan.records) ||
		observation.assertionCount != len(plan.assertions) ||
		observation.revision == 0 || observation.durableThrough < observation.revision {
		return invalid(ErrorInvalidDigest, "commit_observation_attribution")
	}
	return nil
}

func (c *PublicationCoordinatorV2) applyAcknowledgement(ctx context.Context, plan SealedCommitPlanV2, ack AcknowledgedPublicationV2) (AcknowledgedPublicationV2, error) {
	c.mu.Lock()
	registered, exists := c.registered[plan.publicationID]
	if !exists || registered.resolved || registered.plan.digest != plan.digest || registered.plan.recordSetDigest != plan.recordSetDigest || validateAcknowledgement(ack) != nil {
		c.mu.Unlock()
		return AcknowledgedPublicationV2{}, invalid(ErrorAmbiguous, "publication_not_registered")
	}
	if err := c.projection.applyAcknowledged(ack); err != nil {
		c.mu.Unlock()
		return AcknowledgedPublicationV2{}, err
	}
	registered.resolved = true
	c.registered[plan.publicationID] = registered
	c.unresolved--
	sink := c.sink
	c.mu.Unlock()
	// The tracker owns durable acknowledgement validity. The query sink runs
	// outside the coordinator lock so a later provider revision may wait for an
	// earlier concurrent acknowledgement without preventing it from draining.
	if sink != nil {
		if err := sink.AdvanceAcknowledgedPublicationV2(ctx, ack); err != nil {
			return AcknowledgedPublicationV2{}, err
		}
	}
	return ack, nil
}

func (c *PublicationCoordinatorV2) probeBoundary(ctx context.Context, snapshot PublicationBoundarySnapshotV2) error {
	if c.probe == nil {
		return nil
	}
	err := c.probe.PublicationBoundary(ctx, snapshot)
	if err == nil {
		return nil
	}
	interruption, ok := err.(*QualificationPublicationInterruptionV2)
	if !ok || interruption == nil || interruption.Stage != snapshot.Stage || !validPublicationBoundaryStageV2(interruption.Stage) {
		return invalid(ErrorInvalidCanonicalData, "publication_boundary_probe")
	}
	return interruption
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
