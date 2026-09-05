package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
	"unicode/utf8"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const acceptanceResultSchemaV2 = uint32(1)

type AcceptanceStageV2 string

const (
	AcceptanceRequestedV2   AcceptanceStageV2 = "requested"
	AcceptanceGrantedV2     AcceptanceStageV2 = "granted"
	AcceptanceRefusedV2     AcceptanceStageV2 = "refused"
	AcceptanceInvalidatedV2 AcceptanceStageV2 = "invalidated"
)

type EvidenceDispositionV2 string

const (
	EvidenceSatisfiedV2    EvidenceDispositionV2 = "satisfied"
	EvidenceInapplicableV2 EvidenceDispositionV2 = "inapplicable"
	EvidenceWaivedV2       EvidenceDispositionV2 = "waived"
)

// AcceptanceEvidenceV2 is a provider-neutral reference to one exact current
// artifact, policy, workgraph, or other capability fact. Inapplicable and
// waived criteria deliberately have no invented artifact coordinate.
type AcceptanceEvidenceV2 struct {
	CriterionID       string                        `json:"criterion_id"`
	Kind              string                        `json:"kind"`
	Disposition       EvidenceDispositionV2         `json:"disposition"`
	Producer          string                        `json:"producer"`
	PolicyBasis       string                        `json:"policy_basis"`
	EvidenceID        string                        `json:"evidence_id,omitempty"`
	ApprovalID        string                        `json:"approval_id,omitempty"`
	Coordinate        journalv2.AggregateCoordinate `json:"coordinate,omitempty"`
	LogicalVersion    journalv2.LogicalVersion      `json:"logical_version,omitempty"`
	RecordRevision    uint64                        `json:"record_revision,omitempty"`
	StateDigestSHA256 string                        `json:"state_digest_sha256,omitempty"`
	WaiverID          string                        `json:"waiver_id,omitempty"`
}

func (e AcceptanceEvidenceV2) validate() error {
	if !acceptanceText(e.CriterionID, journalv2.MaxIdentityBytes) || !acceptanceText(e.Kind, 64) || !acceptanceText(e.Producer, journalv2.MaxIdentityBytes) || !acceptanceText(e.PolicyBasis, 512) {
		return acceptanceErrorV2(KindInvalid, "criterion")
	}
	zeroCoordinate := journalv2.AggregateCoordinate{}
	switch e.Disposition {
	case EvidenceSatisfiedV2:
		if !acceptanceText(e.EvidenceID, journalv2.MaxIdentityBytes) || e.Coordinate == zeroCoordinate || e.LogicalVersion == 0 || e.RecordRevision == 0 || !validAcceptanceDigest(e.StateDigestSHA256) || e.WaiverID != "" {
			return acceptanceErrorV2(KindInvalid, "satisfied_evidence")
		}
		if _, err := journalv2.NewAggregateCoordinate(e.Coordinate.Kind, e.Coordinate.ID, e.Coordinate.Scope); err != nil {
			return acceptanceErrorV2(KindInvalid, "evidence_coordinate")
		}
	case EvidenceInapplicableV2:
		if e.Coordinate != zeroCoordinate || e.LogicalVersion != 0 || e.RecordRevision != 0 || e.StateDigestSHA256 != "" || !acceptanceText(e.EvidenceID, journalv2.MaxIdentityBytes) || e.ApprovalID != "" || e.WaiverID != "" {
			return acceptanceErrorV2(KindInvalid, "inapplicable_evidence")
		}
	case EvidenceWaivedV2:
		if !acceptanceText(e.WaiverID, journalv2.MaxIdentityBytes) || e.Coordinate != zeroCoordinate || e.LogicalVersion != 0 || e.RecordRevision != 0 || e.StateDigestSHA256 != "" || e.EvidenceID != "" || e.ApprovalID != "" {
			return acceptanceErrorV2(KindInvalid, "waived_evidence")
		}
	default:
		return acceptanceErrorV2(KindInvalid, "evidence_disposition")
	}
	return nil
}

// AcceptanceWaiverV2 is immutable, content-addressed, and criterion-scoped.
// It cannot waive identity, currentness, reviewer independence, or repository
// safety because those predicates are evaluated outside criterion evidence.
type AcceptanceWaiverV2 struct {
	SchemaVersion    uint16             `json:"schema_version"`
	WaiverID         string             `json:"waiver_id"`
	CriterionID      string             `json:"criterion_id"`
	ProjectID        ids.ProjectID      `json:"project_id"`
	MissionID        ids.MissionID      `json:"mission_id"`
	WorkRevisionID   ids.WorkRevisionID `json:"work_revision_id"`
	WorkID           ids.WorkID         `json:"work_id"`
	PolicyRevision   string             `json:"policy_revision"`
	PolicyBasis      string             `json:"policy_basis"`
	AccountableActor ids.ActorID        `json:"accountable_actor_id"`
	Rationale        string             `json:"rationale"`
	EffectiveFrom    string             `json:"effective_from"`
	EffectiveThrough string             `json:"effective_through"`
	ContentDigest    string             `json:"content_digest_sha256"`
}

func (w AcceptanceWaiverV2) Validate() error {
	if w.SchemaVersion != 1 || !acceptanceText(w.WaiverID, journalv2.MaxIdentityBytes) || !acceptanceText(w.CriterionID, journalv2.MaxIdentityBytes) || !acceptanceText(w.PolicyRevision, journalv2.MaxIdentityBytes) || !acceptanceText(w.PolicyBasis, 512) || !acceptanceText(w.Rationale, 1024) {
		return acceptanceErrorV2(KindInvalid, "waiver")
	}
	for _, parse := range []func() error{
		func() error { _, err := ids.ParseProjectID(w.ProjectID.String()); return err },
		func() error { _, err := ids.ParseMissionID(w.MissionID.String()); return err },
		func() error { _, err := ids.ParseWorkRevisionID(w.WorkRevisionID.String()); return err },
		func() error { _, err := ids.ParseWorkID(w.WorkID.String()); return err },
		func() error { _, err := ids.ParseActorID(w.AccountableActor.String()); return err },
	} {
		if err := parse(); err != nil {
			return acceptanceErrorV2(KindInvalid, "waiver_identity")
		}
	}
	from, err := time.Parse(time.RFC3339Nano, w.EffectiveFrom)
	if err != nil || from.Location() != time.UTC {
		return acceptanceErrorV2(KindInvalid, "waiver_time")
	}
	through, err := time.Parse(time.RFC3339Nano, w.EffectiveThrough)
	if err != nil || through.Location() != time.UTC || !through.After(from) {
		return acceptanceErrorV2(KindInvalid, "waiver_time")
	}
	if !validAcceptanceDigest(w.ContentDigest) || w.ContentDigest != w.expectedDigest() {
		return acceptanceErrorV2(KindInvalid, "waiver_digest")
	}
	return nil
}

func (w AcceptanceWaiverV2) expectedDigest() string {
	w.ContentDigest = ""
	raw, _ := json.Marshal(w)
	canonical, _ := journalv2.CanonicalObject(raw)
	sum := sha256.Sum256(append([]byte("spec-kitty/acceptance-waiver/v1\x00"), canonical...))
	return hex.EncodeToString(sum[:])
}

func SealAcceptanceWaiverV2(w AcceptanceWaiverV2) (AcceptanceWaiverV2, error) {
	w.ContentDigest = w.expectedDigest()
	return w, w.Validate()
}

type AcceptedReviewV2 struct {
	ReviewID       ids.ReviewID `json:"review_id"`
	ReviewerActor  ids.ActorID  `json:"reviewer_actor_id"`
	LogicalVersion uint64       `json:"logical_version"`
	StateDigest    string       `json:"state_digest_sha256"`
}

type AcceptanceStateV2 struct {
	SchemaVersion     uint16                          `json:"schema_version"`
	LogicalVersion    uint64                          `json:"logical_version"`
	Stage             AcceptanceStageV2               `json:"stage"`
	AcceptanceID      string                          `json:"acceptance_id"`
	ProjectID         ids.ProjectID                   `json:"project_id"`
	MissionID         ids.MissionID                   `json:"mission_id"`
	WorkRevisionID    ids.WorkRevisionID              `json:"work_revision_id"`
	WorkID            ids.WorkID                      `json:"work_id"`
	HandoffID         ids.HandoffID                   `json:"handoff_id"`
	RepositoryID      ids.RepositoryID                `json:"repository_id"`
	Candidate         string                          `json:"candidate"`
	ContributorDigest string                          `json:"contributor_digest"`
	CriteriaDigest    string                          `json:"criteria_digest"`
	Reviews           []AcceptedReviewV2              `json:"reviews"`
	Evidence          []AcceptanceEvidenceV2          `json:"evidence"`
	Waivers           []AcceptanceWaiverV2            `json:"waivers"`
	PolicyRevision    string                          `json:"policy_revision"`
	PolicyFactID      string                          `json:"policy_fact_id"`
	RuntimeEpochID    journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	ActorID           ids.ActorID                     `json:"actor_id"`
	ProcessID         ids.ProcessID                   `json:"process_id"`
	Governance        execution.ExecutionGovernanceV2 `json:"governance"`
	ObservedAt        string                          `json:"observed_at"`
}

func (s AcceptanceStateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 || !acceptanceText(s.AcceptanceID, journalv2.MaxIdentityBytes) || !handoff.ValidCommit(s.Candidate) || !handoff.ValidDigest(s.ContributorDigest) || !handoff.ValidDigest(s.CriteriaDigest) || !acceptanceText(s.PolicyRevision, journalv2.MaxIdentityBytes) || !acceptanceText(s.PolicyFactID, journalv2.MaxIdentityBytes) || s.PolicyRevision != s.Governance.PolicyRevision {
		return acceptanceErrorV2(KindInvalid, "acceptance_state")
	}
	for _, parse := range []func() error{
		func() error { _, err := ids.ParseProjectID(s.ProjectID.String()); return err },
		func() error { _, err := ids.ParseMissionID(s.MissionID.String()); return err },
		func() error { _, err := ids.ParseWorkRevisionID(s.WorkRevisionID.String()); return err },
		func() error { _, err := ids.ParseWorkID(s.WorkID.String()); return err },
		func() error { _, err := ids.ParseHandoffID(s.HandoffID.String()); return err },
		func() error { _, err := ids.ParseRepositoryID(s.RepositoryID.String()); return err },
		func() error { _, err := ids.ParseActorID(s.ActorID.String()); return err },
		func() error { _, err := ids.ParseProcessID(s.ProcessID.String()); return err },
		func() error { _, err := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return err },
	} {
		if err := parse(); err != nil {
			return acceptanceErrorV2(KindInvalid, "acceptance_identity")
		}
	}
	governance, err := execution.ExecutionGovernanceFromContextV2(s.Governance.Context)
	if err != nil || !bytes.Equal(acceptanceCanonical(governance), acceptanceCanonical(s.Governance)) {
		return acceptanceErrorV2(KindInvalid, "governance")
	}
	when, err := time.Parse(time.RFC3339Nano, s.ObservedAt)
	if err != nil || when.Location() != time.UTC {
		return acceptanceErrorV2(KindInvalid, "observed_at")
	}
	if len(s.Reviews) == 0 || len(s.Reviews) > 64 || len(s.Evidence) > 256 || len(s.Waivers) > 64 {
		return acceptanceErrorV2(KindInvalid, "acceptance_bounds")
	}
	for i, r := range s.Reviews {
		_, reviewErr := ids.ParseReviewID(r.ReviewID.String())
		_, actorErr := ids.ParseActorID(r.ReviewerActor.String())
		if reviewErr != nil || actorErr != nil || r.LogicalVersion == 0 || !validAcceptanceDigest(r.StateDigest) || i > 0 && s.Reviews[i-1].ReviewID >= r.ReviewID {
			return acceptanceErrorV2(KindInvalid, "reviews")
		}
	}
	for i, e := range s.Evidence {
		if e.validate() != nil || i > 0 && s.Evidence[i-1].CriterionID >= e.CriterionID {
			return acceptanceErrorV2(KindInvalid, "evidence")
		}
	}
	criterionIDs := make([]string, len(s.Evidence))
	for i := range s.Evidence {
		criterionIDs[i] = s.Evidence[i].CriterionID
	}
	criteriaDigest, err := handoff.CriterionSetDigestV2(criterionIDs)
	if err != nil || criteriaDigest != s.CriteriaDigest {
		return acceptanceErrorV2(KindInvalid, "criteria_digest")
	}
	for i, w := range s.Waivers {
		if w.Validate() != nil || w.ProjectID != s.ProjectID || w.MissionID != s.MissionID || w.WorkRevisionID != s.WorkRevisionID || w.WorkID != s.WorkID || w.PolicyRevision != s.PolicyRevision || i > 0 && s.Waivers[i-1].WaiverID >= w.WaiverID {
			return acceptanceErrorV2(KindInvalid, "waivers")
		}
	}
	switch s.Stage {
	case AcceptanceRequestedV2:
		if s.LogicalVersion != 1 {
			return acceptanceErrorV2(KindInvalid, "requested_version")
		}
	case AcceptanceGrantedV2:
		if s.LogicalVersion != 2 {
			return acceptanceErrorV2(KindInvalid, "granted_version")
		}
	case AcceptanceRefusedV2, AcceptanceInvalidatedV2:
	default:
		return acceptanceErrorV2(KindInvalid, "acceptance_stage")
	}
	return nil
}

func (s AcceptanceStateV2) CanonicalBytes() []byte {
	if s.Validate() != nil {
		return nil
	}
	return acceptanceCanonical(s)
}

func DecodeAcceptanceStateV2(encoded []byte) (AcceptanceStateV2, error) {
	var state AcceptanceStateV2
	if err := strictAcceptanceJSON(encoded, &state); err != nil || state.Validate() != nil || !bytes.Equal(encoded, state.CanonicalBytes()) {
		return AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "acceptance_state")
	}
	state.Reviews = append([]AcceptedReviewV2(nil), state.Reviews...)
	state.Evidence = append([]AcceptanceEvidenceV2(nil), state.Evidence...)
	state.Waivers = append([]AcceptanceWaiverV2(nil), state.Waivers...)
	return state, nil
}

type acceptanceEventV2 struct {
	SchemaVersion uint16            `json:"schema_version"`
	Kind          AcceptanceStageV2 `json:"kind"`
	PriorDigest   string            `json:"prior_digest,omitempty"`
	State         AcceptanceStateV2 `json:"state"`
}

func acceptanceCoordinateV2(s AcceptanceStateV2) (journalv2.AggregateCoordinate, error) {
	return journalv2.NewAggregateCoordinate("acceptance", journalv2.AggregateID(s.AcceptanceID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(s.ProjectID), MissionID: journalv2.MissionID(s.MissionID), WorkRevisionID: journalv2.WorkRevisionID(s.WorkRevisionID), WorkID: journalv2.WorkID(s.WorkID)})
}

func ReduceAcceptanceEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "acceptance" || envelope.EventSchemaVersion() != 1 || envelope.ReducerVersion() != 1 {
		return nil, acceptanceErrorV2(KindInvalid, "event_tuple")
	}
	var event acceptanceEventV2
	if err := strictAcceptanceJSON(envelope.Payload(), &event); err != nil || event.SchemaVersion != 1 || event.State.Validate() != nil || event.Kind != event.State.Stage {
		return nil, acceptanceErrorV2(KindInvalid, "event")
	}
	next := event.State
	coordinate, _ := acceptanceCoordinateV2(next)
	if coordinate != envelope.Coordinate() || journalv2.LogicalVersion(next.LogicalVersion) != envelope.LogicalVersion() || envelope.EventType() != "acceptance."+string(next.Stage) || envelope.ActorID() != journalv2.ActorID(next.ActorID) || envelope.ProcessID() != journalv2.ProcessID(next.ProcessID) || envelope.WorkRevisionID() != journalv2.WorkRevisionID(next.WorkRevisionID) || envelope.PolicyRevision() != next.PolicyRevision || envelope.CommittedRuntimeEpochID() != next.RuntimeEpochID {
		return nil, acceptanceErrorV2(KindStaleAuthority, "event_authority")
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.Stage != AcceptanceRequestedV2 || next.LogicalVersion != 1 || event.PriorDigest != "" {
			return nil, acceptanceErrorV2(KindConflict, "initial_transition")
		}
		return next.CanonicalBytes(), nil
	}
	old, err := DecodeAcceptanceStateV2(prior)
	if err != nil || old.Stage != AcceptanceRequestedV2 || next.Stage != AcceptanceGrantedV2 || next.LogicalVersion != old.LogicalVersion+1 || !sameAcceptanceIdentityV2(old, next) {
		return nil, acceptanceErrorV2(KindConflict, "acceptance_transition")
	}
	digest, _ := journalv2.ReducerStateDigestV2(old.CanonicalBytes())
	if event.PriorDigest != digest.String() {
		return nil, acceptanceErrorV2(KindConflict, "prior_digest")
	}
	return next.CanonicalBytes(), nil
}

func AcceptanceReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/acceptance-reducer/1/readiness-not-phase"))
	digest, _ := journalv2.ParseDigest("acceptance_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "acceptance", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/integration", ImplementationDigest: digest, Reduce: ReduceAcceptanceEventV2}}
}

type AcceptanceAuthorityRequestV2 struct {
	OperationID       journalv2.OperationID
	AcceptanceID      string
	ProjectID         ids.ProjectID
	MissionID         ids.MissionID
	WorkRevisionID    ids.WorkRevisionID
	WorkID            ids.WorkID
	HandoffID         ids.HandoffID
	RepositoryID      ids.RepositoryID
	Candidate         string
	ContributorDigest string
	ReviewIDs         []ids.ReviewID
	Evidence          []AcceptanceEvidenceV2
	Waivers           []AcceptanceWaiverV2
	PolicyRevision    string
	PolicyFactID      string
	RuntimeEpochID    journalv2.RuntimeEpochID
	ActorID           ids.ActorID
	ProcessID         ids.ProcessID
	Governance        execution.ExecutionGovernanceV2
	ObservedAt        time.Time
}

type AcceptanceOperationResolutionKindV2 uint8

const (
	AcceptanceOperationAbsentV2 AcceptanceOperationResolutionKindV2 = iota + 1
	AcceptanceOperationExactV2
	AcceptanceOperationConflictV2
)

type AcceptanceOperationResolutionV2 struct {
	Kind           AcceptanceOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}

type AcceptanceObservationV2 struct {
	State             *AcceptanceStateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

type AcceptanceEvidenceObservationV2 struct {
	Evidence  AcceptanceEvidenceV2
	Assertion journalv2.AuthorityAssertionV2
}

type AcceptancePolicyFactObservationV2 struct {
	Fact      review.MissionPolicyFactObservationV2
	Assertion journalv2.AuthorityAssertionV2
}

type AcceptanceAuthorityObservationV2 struct {
	Eligibility workgraph.ObservedEligibilityShardV2
	Handoff     handoff.ObservationV2
	Reviews     []review.ObservationV2
	Evidence    []AcceptanceEvidenceObservationV2
	PolicyFacts []AcceptancePolicyFactObservationV2
	Assertions  []journalv2.AuthorityAssertionV2
}

type AcceptancePlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p AcceptancePlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, acceptanceErrorV2(KindInvalid, "plan")
	}
	return p.sealed, nil
}

type AcceptanceAckV2 struct {
	PublicationID    journalv2.PublicationID
	ProviderRevision uint64
	PlanDigest       journalv2.Digest
	MutationCount    int
}

type AcceptanceAuthorityPortV2 interface {
	ResolveAcceptanceOperation(context.Context, journalv2.OperationBindingV2) (AcceptanceOperationResolutionV2, error)
	ObserveAcceptance(context.Context, journalv2.AggregateCoordinate) (AcceptanceObservationV2, error)
	ObserveAcceptanceAuthority(context.Context, AcceptanceAuthorityRequestV2) (AcceptanceAuthorityObservationV2, error)
	PublishAcceptance(context.Context, AcceptancePlanV2) (AcceptanceAckV2, error)
	ReconcileAcceptance(context.Context, AcceptancePlanV2) (AcceptanceAckV2, error)
}

type AcceptanceEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}

type AcceptanceResultV2 struct {
	State         AcceptanceStateV2
	PublicationID journalv2.PublicationID
	Replayed      bool
}

type AcceptanceAuthorityServiceV2 struct {
	port     AcceptanceAuthorityPortV2
	epochs   AcceptanceEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewAcceptanceAuthorityServiceV2(port AcceptanceAuthorityPortV2, epochs AcceptanceEpochSourceV2, registry journalv2.EventRegistryV2) (*AcceptanceAuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, acceptanceErrorV2(KindInvalid, "dependencies")
	}
	if _, err := registry.Resolve("acceptance", 1, 1); err != nil {
		return nil, err
	}
	return &AcceptanceAuthorityServiceV2{port: port, epochs: epochs, registry: registry}, nil
}

func (s *AcceptanceAuthorityServiceV2) Accept(ctx context.Context, request AcceptanceAuthorityRequestV2) (AcceptanceResultV2, error) {
	binding, seed, err := prepareAcceptanceRequestV2(request)
	if err != nil {
		return AcceptanceResultV2{}, err
	}
	resolution, err := s.port.ResolveAcceptanceOperation(ctx, binding)
	if err != nil {
		return AcceptanceResultV2{}, acceptanceErrorV2(KindStoreFailure, "operation_resolution")
	}
	if resolution.Kind == AcceptanceOperationConflictV2 {
		return AcceptanceResultV2{}, acceptanceErrorV2(KindIdempotencyConflict, "operation_id")
	}
	coordinate, _ := acceptanceCoordinateV2(seed)
	var current *AcceptanceStateV2
	var acceptanceRevision uint64
	var priorOperation *journalv2.OperationStateV2
	var operationRevision uint64
	if resolution.Kind == AcceptanceOperationExactV2 {
		if resolution.State.Status() == journalv2.StatusResolved {
			state, decodeErr := DecodeAcceptanceStateV2(resolution.State.Result())
			if decodeErr != nil || state.Stage != AcceptanceGrantedV2 || !sameAcceptanceRequestV2(seed, state) {
				return AcceptanceResultV2{}, acceptanceErrorV2(KindConflict, "operation_result")
			}
			return AcceptanceResultV2{State: state, PublicationID: resolution.State.LastPublicationID(), Replayed: true}, nil
		}
		observed, observeErr := s.port.ObserveAcceptance(ctx, coordinate)
		if observeErr != nil || observed.State == nil || observed.State.Stage != AcceptanceRequestedV2 || !sameAcceptanceRequestV2(seed, *observed.State) {
			return AcceptanceResultV2{}, acceptanceErrorV2(KindConflict, "acceptance_resume")
		}
		current, acceptanceRevision, priorOperation, operationRevision = observed.State, observed.RecordRevision, &resolution.State, resolution.RecordRevision
	} else if resolution.Kind != AcceptanceOperationAbsentV2 {
		return AcceptanceResultV2{}, acceptanceErrorV2(KindConflict, "operation_resolution")
	}
	for {
		epoch, epochErr := s.epochs.CurrentRuntimeEpoch(ctx)
		if epochErr != nil || epoch != request.RuntimeEpochID {
			return AcceptanceResultV2{}, acceptanceErrorV2(KindStaleAuthority, "runtime_epoch")
		}
		authority, observeErr := s.port.ObserveAcceptanceAuthority(ctx, request)
		if observeErr != nil {
			return AcceptanceResultV2{}, acceptanceErrorV2(KindEvidenceUnavailable, "authority")
		}
		eligibility, assertions, reviews, validateErr := validateAcceptanceAuthorityV2(request, authority)
		if validateErr != nil {
			return AcceptanceResultV2{}, validateErr
		}
		next := seed
		next.Reviews = reviews
		next.LogicalVersion = 1
		next.Stage = AcceptanceRequestedV2
		if current != nil {
			next.LogicalVersion = current.LogicalVersion + 1
			next.Stage = AcceptanceGrantedV2
		}
		plan, planErr := buildAcceptancePlanV2(request, binding, current, acceptanceRevision, priorOperation, operationRevision, next, eligibility, assertions, s.registry)
		if planErr != nil {
			return AcceptanceResultV2{}, planErr
		}
		ack, publishErr := s.port.PublishAcceptance(ctx, plan)
		sealed, _ := plan.Publication()
		if publishErr != nil {
			ack, publishErr = s.port.ReconcileAcceptance(ctx, plan)
			if publishErr != nil {
				return AcceptanceResultV2{}, acceptanceErrorV2(KindStoreFailure, "publication")
			}
		}
		if ack.ProviderRevision == 0 || ack.PublicationID != sealed.PublicationID() || ack.PlanDigest != sealed.Digest() || ack.MutationCount != sealed.MutationCount() {
			return AcceptanceResultV2{}, acceptanceErrorV2(KindConflict, "publication_ack")
		}
		copyState := next
		current = &copyState
		acceptanceRevision = ack.ProviderRevision
		op := sealed.OperationState()
		priorOperation, operationRevision = &op, ack.ProviderRevision
		if next.Stage == AcceptanceGrantedV2 {
			return AcceptanceResultV2{State: next, PublicationID: ack.PublicationID}, nil
		}
	}
}

func prepareAcceptanceRequestV2(r AcceptanceAuthorityRequestV2) (journalv2.OperationBindingV2, AcceptanceStateV2, error) {
	if r.ObservedAt.IsZero() || !handoff.ValidCommit(r.Candidate) || !handoff.ValidDigest(r.ContributorDigest) || r.PolicyRevision == "" || !acceptanceText(r.PolicyFactID, journalv2.MaxIdentityBytes) || r.PolicyRevision != r.Governance.PolicyRevision {
		return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "request")
	}
	reviewIDs := append([]ids.ReviewID(nil), r.ReviewIDs...)
	sort.Slice(reviewIDs, func(i, j int) bool { return reviewIDs[i] < reviewIDs[j] })
	if len(reviewIDs) == 0 {
		return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "reviews")
	}
	for i, id := range reviewIDs {
		if _, err := ids.ParseReviewID(id.String()); err != nil || i > 0 && reviewIDs[i-1] == id {
			return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "reviews")
		}
	}
	evidence := append([]AcceptanceEvidenceV2(nil), r.Evidence...)
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].CriterionID < evidence[j].CriterionID })
	for i, item := range evidence {
		if item.validate() != nil || i > 0 && evidence[i-1].CriterionID == item.CriterionID {
			return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "evidence")
		}
	}
	waivers := append([]AcceptanceWaiverV2(nil), r.Waivers...)
	sort.Slice(waivers, func(i, j int) bool { return waivers[i].WaiverID < waivers[j].WaiverID })
	for i, waiver := range waivers {
		if waiver.Validate() != nil || i > 0 && waivers[i-1].WaiverID == waiver.WaiverID {
			return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "waivers")
		}
	}
	criterionIDs := make([]string, len(evidence))
	for i := range evidence {
		criterionIDs[i] = evidence[i].CriterionID
	}
	criteriaDigest, err := handoff.CriterionSetDigestV2(criterionIDs)
	if err != nil {
		return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "criteria_digest")
	}
	state := AcceptanceStateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: AcceptanceRequestedV2, AcceptanceID: r.AcceptanceID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, HandoffID: r.HandoffID, RepositoryID: r.RepositoryID, Candidate: r.Candidate, ContributorDigest: r.ContributorDigest, CriteriaDigest: criteriaDigest, Evidence: evidence, Waivers: waivers, PolicyRevision: r.PolicyRevision, PolicyFactID: r.PolicyFactID, RuntimeEpochID: r.RuntimeEpochID, ActorID: r.ActorID, ProcessID: r.ProcessID, Governance: r.Governance, ObservedAt: r.ObservedAt.UTC().Format(time.RFC3339Nano)}
	// Reviews are populated from observed immutable review authority so callers
	// cannot smuggle reviewer identity or verdict claims into accepted state.
	state.Reviews = make([]AcceptedReviewV2, len(reviewIDs))
	for i, id := range reviewIDs {
		state.Reviews[i] = AcceptedReviewV2{ReviewID: id, ReviewerActor: r.ActorID, LogicalVersion: 1, StateDigest: stringsOfZerosDigestV2()}
	}
	if state.Validate() != nil {
		return journalv2.OperationBindingV2{}, AcceptanceStateV2{}, acceptanceErrorV2(KindInvalid, "request_state")
	}
	state.Reviews = nil
	coordinate, _ := acceptanceCoordinateV2(AcceptanceStateV2{AcceptanceID: r.AcceptanceID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID})
	command := struct {
		State     AcceptanceStateV2 `json:"state"`
		ReviewIDs []ids.ReviewID    `json:"review_ids"`
	}{state, reviewIDs}
	digest, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "acceptance.decide", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: acceptanceCanonical(command)})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: r.OperationID, CommandKind: "acceptance.decide", CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: coordinate.Scope, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), DoctrineRevision: r.Governance.DoctrineRevision, ProfileRevision: r.Governance.ProfileRevision, PolicyRevision: r.PolicyRevision, OriginatingRuntimeEpochID: r.RuntimeEpochID, ObservedCreatedAt: r.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
	return binding, state, err
}

func validateAcceptanceAuthorityV2(r AcceptanceAuthorityRequestV2, observed AcceptanceAuthorityObservationV2) (journalv2.EligibilityGuardTransition, []journalv2.AuthorityAssertionV2, []AcceptedReviewV2, error) {
	token, err := observed.Eligibility.Token(r.WorkID, r.WorkRevisionID, workgraph.EligibilityAcceptance)
	if err != nil {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindRevisionIneligible, "eligibility")
	}
	if observed.Handoff.State == nil || observed.Handoff.RecordRevision == 0 {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "handoff")
	}
	h := *observed.Handoff.State
	evidenceIDs := make([]string, len(r.Evidence))
	for i := range r.Evidence {
		evidenceIDs[i] = r.Evidence[i].CriterionID
	}
	sort.Strings(evidenceIDs)
	criteriaDigest, _ := handoff.CriterionSetDigestV2(evidenceIDs)
	if h.Stage != handoff.StageCandidateRecordedV2 || h.HandoffID != r.HandoffID || h.ProjectID != r.ProjectID || h.MissionID != r.MissionID || h.WorkRevisionID != r.WorkRevisionID || h.WorkID != r.WorkID || h.RepositoryID != r.RepositoryID || h.Candidate != r.Candidate || h.ContributorDigest != r.ContributorDigest || h.CriteriaDigest != criteriaDigest || !acceptanceStringSlicesEqualV2(h.CriterionIDs, evidenceIDs) {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindNotApproved, "handoff")
	}
	wantedReviewIDs := append([]ids.ReviewID(nil), r.ReviewIDs...)
	sort.Slice(wantedReviewIDs, func(i, j int) bool { return wantedReviewIDs[i] < wantedReviewIDs[j] })
	if len(observed.Reviews) != len(wantedReviewIDs) {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindNotApproved, "reviews")
	}
	contributors := make(map[ids.ActorID]struct{}, len(h.Contributors))
	for _, actor := range h.Contributors {
		contributors[actor] = struct{}{}
	}
	acceptedReviews := make([]AcceptedReviewV2, 0, len(observed.Reviews))
	type assertionExpectation struct {
		revision uint64
		logical  journalv2.LogicalVersion
	}
	wantedAssertions := map[journalv2.AggregateCoordinate]assertionExpectation{}
	handoffCoordinate, _ := handoff.CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	wantedAssertions[handoffCoordinate] = assertionExpectation{revision: observed.Handoff.RecordRevision, logical: journalv2.LogicalVersion(h.LogicalVersion)}
	policyFacts := make(map[string]AcceptancePolicyFactObservationV2, len(observed.PolicyFacts))
	for _, fact := range observed.PolicyFacts {
		if _, duplicate := policyFacts[fact.Fact.FactID]; duplicate {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "policy_fact_duplicate")
		}
		policyFacts[fact.Fact.FactID] = fact
	}
	consumePolicyFact := func(factID string, detail journalv2.Digest) error {
		observedFact, ok := policyFacts[factID]
		fact := observedFact.Fact
		if !ok || !fact.Found || fact.ProjectID != journalv2.ProjectID(r.ProjectID) || fact.MissionID != journalv2.MissionID(r.MissionID) || fact.FactID != factID || fact.Kind != "decision" || fact.Status != "resolved" || fact.LogicalVersion == 0 || fact.RecordRevision == 0 || fact.DetailDigest != detail {
			return acceptanceErrorV2(KindPolicyRefused, "policy_fact")
		}
		coordinate, coordinateErr := acceptancePolicyCoordinateV2(r.ProjectID, r.MissionID, factID)
		if coordinateErr != nil {
			return acceptanceErrorV2(KindPolicyRefused, "policy_coordinate")
		}
		assertion := observedFact.Assertion
		if assertion.Target().Coordinate != coordinate || assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() != fact.RecordRevision || assertion.ExpectedLogicalVersion() != fact.LogicalVersion || assertion.ObservedValueDigest().IsZero() {
			return acceptanceErrorV2(KindStaleAuthority, "policy_assertion")
		}
		wantedAssertions[coordinate] = assertionExpectation{revision: fact.RecordRevision, logical: fact.LogicalVersion}
		delete(policyFacts, factID)
		return nil
	}
	if err := consumePolicyFact(r.PolicyFactID, AcceptancePolicyRevisionDigestV2(r)); err != nil {
		return journalv2.EligibilityGuardTransition{}, nil, nil, err
	}
	for i, item := range observed.Reviews {
		if item.State == nil {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindNotApproved, "review")
		}
		state := *item.State
		if state.ReviewID != wantedReviewIDs[i] || state.Stage != review.StageVerdictRecordedV2 || state.Verdict != review.Approved || state.HandoffID != r.HandoffID || state.ProjectID != r.ProjectID || state.MissionID != r.MissionID || state.WorkRevisionID != r.WorkRevisionID || state.WorkID != r.WorkID || state.Candidate != r.Candidate || state.PolicyRevision != r.PolicyRevision {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindNotApproved, "review")
		}
		if _, self := contributors[state.ReviewerActorID]; self && state.Exception == nil {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindNotApproved, "reviewer_independence")
		}
		if state.Exception != nil {
			if err := consumePolicyFact(state.Exception.ExceptionID, state.Exception.Digest()); err != nil {
				return journalv2.EligibilityGuardTransition{}, nil, nil, err
			}
		}
		digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
		acceptedReviews = append(acceptedReviews, AcceptedReviewV2{ReviewID: state.ReviewID, ReviewerActor: state.ReviewerActorID, LogicalVersion: state.LogicalVersion, StateDigest: digest.String()})
		coordinate, _ := review.CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, state.ReviewID)
		wantedAssertions[coordinate] = assertionExpectation{revision: item.RecordRevision, logical: journalv2.LogicalVersion(state.LogicalVersion)}
	}
	sort.Slice(acceptedReviews, func(i, j int) bool { return acceptedReviews[i].ReviewID < acceptedReviews[j].ReviewID })
	observedEvidence := make(map[string]AcceptanceEvidenceObservationV2, len(observed.Evidence))
	for _, item := range observed.Evidence {
		if _, duplicate := observedEvidence[item.Evidence.CriterionID]; duplicate {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "evidence_duplicate")
		}
		observedEvidence[item.Evidence.CriterionID] = item
	}
	waivers := make(map[string]AcceptanceWaiverV2, len(r.Waivers))
	for _, waiver := range r.Waivers {
		waivers[waiver.WaiverID] = waiver
	}
	for _, evidence := range r.Evidence {
		switch evidence.Disposition {
		case EvidenceSatisfiedV2:
			actual, ok := observedEvidence[evidence.CriterionID]
			if !ok || !sameAcceptanceEvidenceV2(actual.Evidence, evidence) {
				return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceEvidenceUnavailableV2(evidence, "evidence head is absent, stale, or substituted")
			}
			if actual.Assertion.Target().Coordinate != evidence.Coordinate || actual.Assertion.ExpectedLogicalVersion() != evidence.LogicalVersion || actual.Assertion.ObservedRevision() != evidence.RecordRevision {
				return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceEvidenceUnavailableV2(evidence, "evidence assertion does not bind the exact current head")
			}
			wantedAssertions[evidence.Coordinate] = assertionExpectation{revision: evidence.RecordRevision, logical: evidence.LogicalVersion}
		case EvidenceWaivedV2:
			waiver, ok := waivers[evidence.WaiverID]
			when := r.ObservedAt.UTC()
			from, _ := time.Parse(time.RFC3339Nano, waiver.EffectiveFrom)
			through, _ := time.Parse(time.RFC3339Nano, waiver.EffectiveThrough)
			if !ok || waiver.CriterionID != evidence.CriterionID || waiver.ProjectID != r.ProjectID || waiver.MissionID != r.MissionID || waiver.WorkRevisionID != r.WorkRevisionID || waiver.WorkID != r.WorkID || waiver.PolicyRevision != r.PolicyRevision || when.Before(from) || !when.Before(through) {
				return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindPolicyRefused, "waiver")
			}
			detail, parseErr := journalv2.ParseDigest("waiver_content", waiver.ContentDigest)
			if parseErr != nil {
				return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindPolicyRefused, "waiver_digest")
			}
			if err := consumePolicyFact(waiver.WaiverID, detail); err != nil {
				return journalv2.EligibilityGuardTransition{}, nil, nil, err
			}
		case EvidenceInapplicableV2:
			if err := consumePolicyFact(evidence.EvidenceID, AcceptanceApplicabilityDigestV2(r, evidence)); err != nil {
				return journalv2.EligibilityGuardTransition{}, nil, nil, err
			}
		default:
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindInvalid, "evidence_disposition")
		}
	}
	if len(policyFacts) != 0 {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "unexpected_policy_fact")
	}
	if len(observed.Assertions) != len(wantedAssertions) {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "assertions")
	}
	for _, assertion := range observed.Assertions {
		target := assertion.Target()
		expectation, ok := wantedAssertions[target.Coordinate]
		if !ok || target.Kind != journalv2.ConditionTargetAggregateHead || assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() != expectation.revision || assertion.ExpectedLogicalVersion() != expectation.logical || assertion.ObservedValueDigest().IsZero() {
			return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "assertions")
		}
		delete(wantedAssertions, target.Coordinate)
	}
	if len(wantedAssertions) != 0 {
		return journalv2.EligibilityGuardTransition{}, nil, nil, acceptanceErrorV2(KindStaleAuthority, "assertions")
	}
	return token.Transition(), append([]journalv2.AuthorityAssertionV2(nil), observed.Assertions...), acceptedReviews, nil
}

func acceptancePolicyCoordinateV2(projectID ids.ProjectID, missionID ids.MissionID, factID string) (journalv2.AggregateCoordinate, error) {
	return journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+factID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID)})
}

// AcceptancePolicyRevisionDigestV2 is the exact semantic payload a resolved
// mission policy fact must authorize. It is exported for provider adapters and
// qualification fixtures; callers cannot grant authority by computing it.
func AcceptancePolicyRevisionDigestV2(r AcceptanceAuthorityRequestV2) journalv2.Digest {
	payload := acceptanceCanonical(struct {
		ProjectID      ids.ProjectID                   `json:"project_id"`
		MissionID      ids.MissionID                   `json:"mission_id"`
		WorkRevisionID ids.WorkRevisionID              `json:"work_revision_id"`
		WorkID         ids.WorkID                      `json:"work_id"`
		PolicyRevision string                          `json:"policy_revision"`
		PolicyFactID   string                          `json:"policy_fact_id"`
		Governance     execution.ExecutionGovernanceV2 `json:"governance"`
	}{r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.PolicyRevision, r.PolicyFactID, r.Governance})
	digest, _ := journalv2.ReducerStateDigestV2(payload)
	return digest
}

// AcceptanceApplicabilityDigestV2 binds a producer's inapplicability result
// to the exact criterion, scope, policy revision, and durable policy fact.
func AcceptanceApplicabilityDigestV2(r AcceptanceAuthorityRequestV2, evidence AcceptanceEvidenceV2) journalv2.Digest {
	payload := acceptanceCanonical(struct {
		ProjectID      ids.ProjectID      `json:"project_id"`
		MissionID      ids.MissionID      `json:"mission_id"`
		WorkRevisionID ids.WorkRevisionID `json:"work_revision_id"`
		WorkID         ids.WorkID         `json:"work_id"`
		PolicyRevision string             `json:"policy_revision"`
		CriterionID    string             `json:"criterion_id"`
		Kind           string             `json:"kind"`
		Producer       string             `json:"producer"`
		PolicyBasis    string             `json:"policy_basis"`
		PolicyFactID   string             `json:"policy_fact_id"`
	}{r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.PolicyRevision, evidence.CriterionID, evidence.Kind, evidence.Producer, evidence.PolicyBasis, evidence.EvidenceID})
	digest, _ := journalv2.ReducerStateDigestV2(payload)
	return digest
}

func buildAcceptancePlanV2(r AcceptanceAuthorityRequestV2, binding journalv2.OperationBindingV2, prior *AcceptanceStateV2, priorAcceptanceRevision uint64, priorOperation *journalv2.OperationStateV2, priorOperationRevision uint64, next AcceptanceStateV2, eligibility journalv2.EligibilityGuardTransition, assertions []journalv2.AuthorityAssertionV2, registry journalv2.EventRegistryV2) (AcceptancePlanV2, error) {
	version := journalv2.LogicalVersion(next.LogicalVersion)
	opVersion := journalv2.LogicalVersion(1)
	if priorOperation != nil {
		opVersion = priorOperation.LogicalVersion() + 1
	}
	opEventID := acceptanceEventIDV2("operation", r.OperationID, uint64(opVersion))
	acceptanceEventID := acceptanceEventIDV2("acceptance", r.OperationID, uint64(version))
	eventIDs := []journalv2.EventID{opEventID, acceptanceEventID}
	if priorOperation != nil {
		eventIDs = append(priorOperation.EventIDs(), eventIDs...)
	}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	status := journalv2.StatusInProgress
	resultKind := ""
	var result []byte
	if next.Stage == AcceptanceGrantedV2 {
		status, resultKind, result = journalv2.StatusResolved, "acceptance.result", next.CanonicalBytes()
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: r.OperationID, LogicalVersion: opVersion, Status: status, ResultKind: resultKind, ResultSchemaVersion: func() uint32 {
		if resultKind != "" {
			return acceptanceResultSchemaV2
		}
		return 0
	}(), CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return AcceptancePlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	priorDigest := ""
	if prior != nil {
		digest, _ := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
		priorDigest = digest.String()
	}
	eventPayload := acceptanceCanonical(acceptanceEventV2{SchemaVersion: 1, Kind: next.Stage, PriorDigest: priorDigest, State: next})
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(r.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	acceptanceCoordinate, _ := acceptanceCoordinateV2(next)
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	acceptanceDigest, _ := journalv2.ReducerStateDigestV2(next.CanonicalBytes())
	governance := []string{"config." + r.Governance.PolicyRevision, "doctrine." + r.Governance.DoctrineRevision, "mission_type." + r.Governance.MissionTypeRevision, "profile." + r.Governance.ProfileRevision}
	sort.Strings(governance)
	events := []journalv2.EventDraftV2{
		{EventID: opEventID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: opVersion, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
		{EventID: acceptanceEventID, EventType: "acceptance." + string(next.Stage), Family: "acceptance", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: acceptanceCoordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.PolicyRevision, GovernanceRevisions: governance, Payload: eventPayload},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
			if priorOperation != nil {
				return priorOperation.LogicalVersion()
			}
			return 0
		}(), NextLogicalVersion: opVersion, EventID: opEventID, ExpectedRecordRevision: priorOperationRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload},
		{Coordinate: acceptanceCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
			if prior != nil {
				return journalv2.LogicalVersion(prior.LogicalVersion)
			}
			return 0
		}(), NextLogicalVersion: version, EventID: acceptanceEventID, ExpectedRecordRevision: priorAcceptanceRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: acceptanceDigest, AuthoritySummary: next.CanonicalBytes()},
	}
	conditions := make([]journalv2.ProviderCondition, 0, 3)
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			return AcceptancePlanV2{}, conditionErr
		}
		conditions = append(conditions, condition)
	}
	eligibilityCondition, err := journalv2.ConditionForEligibilityGuardTransitionV2(eligibility)
	if err != nil {
		return AcceptancePlanV2{}, err
	}
	conditions = append(conditions, eligibilityCondition)
	input := journalv2.CommitPlanInput{OperationState: operation, Events: events, Heads: heads, Conditions: conditions, EligibilityGuards: []journalv2.EligibilityGuardTransition{eligibility}, Assertions: assertions}
	if priorOperation == nil {
		input.OperationBinding = &binding
	} else {
		input.PriorOperationState = priorOperation
	}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return AcceptancePlanV2{}, err
	}
	return AcceptancePlanV2{sealed: sealed}, nil
}

func sameAcceptanceIdentityV2(left, right AcceptanceStateV2) bool {
	left.LogicalVersion, right.LogicalVersion = 0, 0
	left.Stage, right.Stage = "", ""
	return bytes.Equal(acceptanceCanonical(left), acceptanceCanonical(right))
}

func sameAcceptanceRequestV2(seed, state AcceptanceStateV2) bool {
	seed.Reviews = append([]AcceptedReviewV2(nil), state.Reviews...)
	return sameAcceptanceIdentityV2(seed, state)
}

func sameAcceptanceEvidenceV2(left, right AcceptanceEvidenceV2) bool {
	return bytes.Equal(acceptanceCanonical(left), acceptanceCanonical(right))
}

func acceptanceEventIDV2(kind string, operation journalv2.OperationID, version uint64) journalv2.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spec-kitty/acceptance/%s/%s/%d", kind, operation, version)))
	id, _ := journalv2.NewEventID("event-" + hex.EncodeToString(sum[:20]))
	return id
}

func acceptanceCanonical(value any) []byte {
	raw, _ := json.Marshal(value)
	canonical, _ := journalv2.CanonicalObject(raw)
	return canonical
}

func strictAcceptanceJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return acceptanceErrorV2(KindInvalid, "payload_trailing")
	}
	return nil
}

func acceptanceText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value)
}

func validAcceptanceDigest(value string) bool {
	_, err := journalv2.ParseDigest("acceptance_digest", value)
	return err == nil
}

func acceptanceStringSlicesEqualV2(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stringsOfZerosDigestV2() string { return hex.EncodeToString(make([]byte, 32)) }

func acceptanceErrorV2(kind ErrorKind, field string) error {
	return &Error{Kind: kind, Message: "acceptance authority refused", Observed: map[string]string{"field": field}}
}

func acceptanceEvidenceUnavailableV2(evidence AcceptanceEvidenceV2, reason string) error {
	return &Error{
		Kind:    KindEvidenceUnavailable,
		Message: "acceptance criterion evidence is unavailable",
		Observed: map[string]string{
			"criterion_id": evidence.CriterionID,
			"kind":         evidence.Kind,
			"producer":     evidence.Producer,
			"reason":       reason,
		},
		Expected: map[string]string{
			"recovery": "run the typed producer and resubmit its exact current authority coordinate",
		},
	}
}
