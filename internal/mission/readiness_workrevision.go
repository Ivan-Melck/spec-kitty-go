package mission

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type workRevisionHeadWireV2 struct {
	ID            journalv2.WorkRevisionID `json:"work_revision_id"`
	Generation    uint64                   `json:"generation"`
	PredecessorID journalv2.WorkRevisionID `json:"predecessor_id,omitempty"`
	ContentDigest string                   `json:"content_sha256"`
}

// WorkRevisionAuthorityV2 is a sealed projection of an exact normalized
// WorkRevision. Its private fields prevent a current-revision pointer from
// being minted by a mission-state struct literal.
type WorkRevisionAuthorityV2 struct {
	head      workRevisionHeadWireV2
	canonical []byte
}

// workRevisionPublicationV2 is minted only after a finalization or successor
// publication service has validated the durable store's complete result. A raw
// RevisionV2 is data, not proof that the revision is the published head.
type workRevisionPublicationV2 struct {
	revision      workgraph.RevisionV2
	requestDigest string
	receiptDigest string
	canonical     []byte
}

func newWorkRevisionPublicationV2(revision workgraph.RevisionV2, requestDigest, receiptDigest string) workRevisionPublicationV2 {
	requestDigest, requestOK := normalizedSHA256(requestDigest)
	receiptDigest, receiptOK := normalizedSHA256(receiptDigest)
	if !requestOK || !receiptOK {
		return workRevisionPublicationV2{}
	}
	revisionBytes, err := revision.Encode()
	if err != nil {
		return workRevisionPublicationV2{}
	}
	encoded, err := json.Marshal(struct {
		Revision      json.RawMessage `json:"revision"`
		RequestDigest string          `json:"request_sha256"`
		ReceiptDigest string          `json:"publication_evidence_sha256"`
	}{revisionBytes, requestDigest, receiptDigest})
	if err != nil {
		return workRevisionPublicationV2{}
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		return workRevisionPublicationV2{}
	}
	return workRevisionPublicationV2{revision: revision, requestDigest: requestDigest, receiptDigest: receiptDigest, canonical: canonical}
}

func normalizedSHA256(value string) (string, bool) {
	value = strings.TrimPrefix(value, "sha256:")
	return value, lowerHexDigest(value)
}

func sealWorkRevisionAuthorityV2(revision workgraph.RevisionV2) (WorkRevisionAuthorityV2, error) {
	encoded, err := revision.Encode()
	if err != nil {
		return WorkRevisionAuthorityV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_encode", err)
	}
	decoded, err := workgraph.DecodeRevisionV2(encoded)
	if err != nil {
		return WorkRevisionAuthorityV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_decode", err)
	}
	if decoded.ID() != revision.ID() || decoded.Generation() != revision.Generation() || decoded.PredecessorID() != revision.PredecessorID() || decoded.ContentDigest() != revision.ContentDigest() {
		return WorkRevisionAuthorityV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_round_trip", nil)
	}
	contentDigest := revision.ContentDigest()
	if contentDigest == "" {
		contentDigest = fmt.Sprintf("%x", sha256.Sum256(encoded))
	}
	return sealWorkRevisionHeadV2(workRevisionHeadWireV2{journalv2.WorkRevisionID(revision.ID()), revision.Generation(), journalv2.WorkRevisionID(revision.PredecessorID()), contentDigest})
}

func sealWorkRevisionHeadV2(head workRevisionHeadWireV2) (WorkRevisionAuthorityV2, error) {
	if _, err := journalv2.NewWorkRevisionID(string(head.ID)); err != nil || head.Generation == 0 || !lowerHexDigest(head.ContentDigest) {
		return WorkRevisionAuthorityV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_head", err)
	}
	if head.PredecessorID != "" {
		if _, err := journalv2.NewWorkRevisionID(string(head.PredecessorID)); err != nil {
			return WorkRevisionAuthorityV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_head", err)
		}
	}
	canonical, err := json.Marshal(head)
	if err != nil {
		return WorkRevisionAuthorityV2{}, err
	}
	return WorkRevisionAuthorityV2{head: head, canonical: canonical}, nil
}

func (h WorkRevisionAuthorityV2) IsZero() bool                 { return len(h.canonical) == 0 }
func (h WorkRevisionAuthorityV2) ID() journalv2.WorkRevisionID { return h.head.ID }
func (h WorkRevisionAuthorityV2) Generation() uint64           { return h.head.Generation }
func (h WorkRevisionAuthorityV2) PredecessorID() journalv2.WorkRevisionID {
	return h.head.PredecessorID
}
func (h WorkRevisionAuthorityV2) ContentDigest() string  { return h.head.ContentDigest }
func (h WorkRevisionAuthorityV2) CanonicalBytes() []byte { return bytes.Clone(h.canonical) }

type WorkRevisionDispositionV2 struct {
	OldWorkID   ids.WorkID            `json:"old_work_id"`
	Disposition workgraph.Disposition `json:"disposition"`
	NewWorkIDs  []ids.WorkID          `json:"new_work_ids"`
}

// WorkRevisionSelectionV2 binds the exact normalized revision and, for later
// generations, the full reconciliation disposition set. It is included in
// the operation intent and the durable mission event.
type WorkRevisionSelectionV2 struct {
	head                 WorkRevisionAuthorityV2
	revisionCanonical    []byte
	publicationCanonical []byte
	reconciliationID     string
	reconciliationDigest string
	dispositions         []WorkRevisionDispositionV2
	canonical            []byte
}

func SealInitialWorkRevisionSelectionV2(result FinalizationResult) (WorkRevisionSelectionV2, error) {
	publication := result.publication
	if len(publication.canonical) == 0 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "initial_work_revision_publication", nil)
	}
	revision := publication.revision
	head, err := sealWorkRevisionAuthorityV2(revision)
	if err != nil {
		return WorkRevisionSelectionV2{}, err
	}
	if head.Generation() != 1 || head.PredecessorID() != "" {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "initial_work_revision", nil)
	}
	encoded, _ := revision.Encode()
	return sealWorkRevisionSelectionV2(head, encoded, publication.canonical, "", "", nil)
}

// SealAcknowledgedInitialWorkRevisionSelectionV2 bridges the production
// Authority Journal v2 workgraph publication into mission readiness authority.
// The acknowledgement, not a caller-authored success flag, proves that the
// exact canonical generation-one revision reached durable GapDB authority.
func SealAcknowledgedInitialWorkRevisionSelectionV2(revision workgraph.RevisionV2, acknowledged journalv2.AcknowledgedPublicationV2) (WorkRevisionSelectionV2, error) {
	encoded, err := revision.Encode()
	if err != nil || revision.Generation() != 1 || revision.PredecessorID() != "" || revision.SchemaVersion() != workgraph.RevisionSchemaV3 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "acknowledged_initial_revision", err)
	}
	plan := acknowledged.Plan()
	if plan.Digest().IsZero() || acknowledged.PublicationID() != plan.PublicationID() || acknowledged.PlanDigest() != plan.Digest() || acknowledged.RecordSetDigest() != plan.RecordSetDigest() ||
		acknowledged.ProviderRevision() == 0 || acknowledged.DurableThroughRevision() < acknowledged.ProviderRevision() || acknowledged.MutationCount() != plan.MutationCount() || acknowledged.AssertionCount() != plan.AssertionCount() {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_acknowledgement", nil)
	}
	binding := plan.BindingCreate()
	operation := plan.OperationState()
	events := plan.Events()
	if binding == nil || binding.OperationID() != operation.OperationID() || operation.Status() != journalv2.StatusResolved || operation.LastPublicationID() != acknowledged.PublicationID() || len(events) != 2 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_operation", nil)
	}
	var event journalv2.EventEnvelopeV2
	workgraphEvents := 0
	for _, candidate := range events {
		if candidate.OperationID() != operation.OperationID() || candidate.PublicationID() != acknowledged.PublicationID() {
			return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_membership", nil)
		}
		if candidate.Family() == "workgraph" {
			event = candidate
			workgraphEvents++
		} else if candidate.Family() != "operation" {
			return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_membership", nil)
		}
	}
	if workgraphEvents != 1 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_membership", nil)
	}
	coordinate := event.Coordinate()
	if event.Family() != "workgraph" || event.EventType() != "workgraph.finalized" || event.OperationID() != operation.OperationID() || event.PublicationID() != acknowledged.PublicationID() ||
		coordinate.Kind != "workgraph" || coordinate.Scope.ProjectID != journalv2.ProjectID(revision.ProjectID()) || coordinate.Scope.MissionID != journalv2.MissionID(revision.MissionID()) || string(coordinate.ID) != string(revision.MissionID()) {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_event", nil)
	}
	var payload struct {
		SchemaVersion uint16          `json:"schema_version"`
		Kind          string          `json:"kind"`
		Revision      json.RawMessage `json:"revision"`
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload()))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || payload.SchemaVersion != 1 || payload.Kind != "finalized" || !bytes.Equal(payload.Revision, encoded) {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_payload", err)
	}
	receipt, err := json.Marshal(struct {
		PublicationID  journalv2.PublicationID `json:"publication_id"`
		PlanDigest     string                  `json:"plan_sha256"`
		RecordSet      string                  `json:"record_set_sha256"`
		Revision       uint64                  `json:"provider_revision"`
		DurableThrough uint64                  `json:"durable_through_revision"`
		Mutations      int                     `json:"mutation_count"`
		Assertions     int                     `json:"assertion_count"`
	}{acknowledged.PublicationID(), acknowledged.PlanDigest().String(), acknowledged.RecordSetDigest().String(), acknowledged.ProviderRevision(), acknowledged.DurableThroughRevision(), acknowledged.MutationCount(), acknowledged.AssertionCount()})
	if err != nil {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_receipt", err)
	}
	receipt, err = journalv2.CanonicalObject(receipt)
	if err != nil {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_receipt", err)
	}
	receiptDigest := fmt.Sprintf("%x", sha256.Sum256(receipt))
	publication := newWorkRevisionPublicationV2(revision, binding.IntentDigest().String(), receiptDigest)
	if len(publication.canonical) == 0 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "workgraph_publication_evidence", nil)
	}
	head, err := sealWorkRevisionAuthorityV2(revision)
	if err != nil {
		return WorkRevisionSelectionV2{}, err
	}
	return sealWorkRevisionSelectionV2(head, encoded, publication.canonical, "", "", nil)
}

func SealReconciledWorkRevisionSelectionV2(prior WorkRevisionAuthorityV2, result SuccessorPublicationResult) (WorkRevisionSelectionV2, error) {
	publication := result.publication
	if len(publication.canonical) == 0 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "reconciled_work_revision_publication", nil)
	}
	revision, reconciliation := publication.revision, result.Reconciliation
	head, err := sealWorkRevisionAuthorityV2(revision)
	reconciliationDigest, digestOK := normalizedSHA256(reconciliation.RequestDigest())
	if err != nil || prior.IsZero() || head.Generation() != prior.Generation()+1 || head.PredecessorID() != prior.ID() || reconciliation.OldRevisionID() != ids.WorkRevisionID(prior.ID()) || reconciliation.NewRevisionID() != revision.ID() || !digestOK || reconciliation.ID() == "" {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "reconciled_work_revision", err)
	}
	dispositions := make([]WorkRevisionDispositionV2, 0, len(reconciliation.Decisions()))
	for _, decision := range reconciliation.Decisions() {
		disposition := decision.Disposition()
		if disposition != workgraph.Continue && disposition != workgraph.Supersede && disposition != workgraph.Invalidate && disposition != workgraph.Fork {
			return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "reconciliation_disposition", nil)
		}
		dispositions = append(dispositions, WorkRevisionDispositionV2{decision.OldWorkID(), disposition, decision.NewWorkIDs()})
	}
	if len(dispositions) == 0 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "reconciliation_disposition", nil)
	}
	sort.Slice(dispositions, func(i, j int) bool { return dispositions[i].OldWorkID < dispositions[j].OldWorkID })
	encoded, _ := revision.Encode()
	return sealWorkRevisionSelectionV2(head, encoded, publication.canonical, reconciliation.ID(), reconciliationDigest, dispositions)
}

func sealWorkRevisionSelectionV2(head WorkRevisionAuthorityV2, revision, publication []byte, reconciliationID, reconciliationDigest string, dispositions []WorkRevisionDispositionV2) (WorkRevisionSelectionV2, error) {
	wire := struct {
		Head                 json.RawMessage             `json:"head"`
		Revision             json.RawMessage             `json:"revision"`
		Publication          json.RawMessage             `json:"publication_authority"`
		ReconciliationID     string                      `json:"reconciliation_id,omitempty"`
		ReconciliationDigest string                      `json:"reconciliation_sha256,omitempty"`
		Dispositions         []WorkRevisionDispositionV2 `json:"dispositions"`
	}{head.CanonicalBytes(), revision, publication, reconciliationID, reconciliationDigest, dispositions}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return WorkRevisionSelectionV2{}, err
	}
	canonical, err = journalv2.CanonicalObject(canonical)
	if err != nil {
		return WorkRevisionSelectionV2{}, err
	}
	return WorkRevisionSelectionV2{head: head, revisionCanonical: bytes.Clone(revision), publicationCanonical: bytes.Clone(publication), reconciliationID: reconciliationID, reconciliationDigest: reconciliationDigest, dispositions: append([]WorkRevisionDispositionV2(nil), dispositions...), canonical: canonical}, nil
}

func (s WorkRevisionSelectionV2) IsZero() bool                  { return len(s.canonical) == 0 }
func (s WorkRevisionSelectionV2) Head() WorkRevisionAuthorityV2 { return s.head }
func (s WorkRevisionSelectionV2) CanonicalBytes() []byte        { return bytes.Clone(s.canonical) }
func (s WorkRevisionSelectionV2) Dispositions() []WorkRevisionDispositionV2 {
	result := append([]WorkRevisionDispositionV2(nil), s.dispositions...)
	for index := range result {
		result[index].NewWorkIDs = append([]ids.WorkID(nil), result[index].NewWorkIDs...)
	}
	return result
}

func decodeWorkRevisionSelectionV2(encoded []byte) (WorkRevisionSelectionV2, error) {
	var wire struct {
		Head                 json.RawMessage             `json:"head"`
		Revision             json.RawMessage             `json:"revision"`
		Publication          json.RawMessage             `json:"publication_authority"`
		ReconciliationID     string                      `json:"reconciliation_id,omitempty"`
		ReconciliationDigest string                      `json:"reconciliation_sha256,omitempty"`
		Dispositions         []WorkRevisionDispositionV2 `json:"dispositions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_wire", err)
	}
	var headWire workRevisionHeadWireV2
	if err := json.Unmarshal(wire.Head, &headWire); err != nil {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_head_wire", err)
	}
	head, err := sealWorkRevisionHeadV2(headWire)
	if err != nil {
		return WorkRevisionSelectionV2{}, err
	}
	revision, err := workgraph.DecodeRevisionV2(wire.Revision)
	if err != nil {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_revision", err)
	}
	var publication struct {
		Revision      json.RawMessage `json:"revision"`
		RequestDigest string          `json:"request_sha256"`
		ReceiptDigest string          `json:"publication_evidence_sha256"`
	}
	publicationDecoder := json.NewDecoder(bytes.NewReader(wire.Publication))
	publicationDecoder.DisallowUnknownFields()
	if err := publicationDecoder.Decode(&publication); err != nil || !bytes.Equal(publication.Revision, wire.Revision) || !lowerHexDigest(publication.RequestDigest) || !lowerHexDigest(publication.ReceiptDigest) {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_publication", err)
	}
	wantHead, err := sealWorkRevisionAuthorityV2(revision)
	if err != nil || !bytes.Equal(wantHead.CanonicalBytes(), head.CanonicalBytes()) {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_head_mismatch", err)
	}
	if head.Generation() == 1 {
		if wire.ReconciliationID != "" || wire.ReconciliationDigest != "" || len(wire.Dispositions) != 0 {
			return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "work_revision_selection", nil)
		}
	} else if !boundedAuthorityIdentityV2(wire.ReconciliationID) || !lowerHexDigest(wire.ReconciliationDigest) || len(wire.Dispositions) == 0 {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "work_revision_selection", nil)
	}
	for index, disposition := range wire.Dispositions {
		if disposition.Disposition != workgraph.Continue && disposition.Disposition != workgraph.Supersede && disposition.Disposition != workgraph.Invalidate && disposition.Disposition != workgraph.Fork || index > 0 && wire.Dispositions[index-1].OldWorkID >= disposition.OldWorkID {
			return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "work_revision_selection", nil)
		}
	}
	selection, err := sealWorkRevisionSelectionV2(head, wire.Revision, wire.Publication, wire.ReconciliationID, wire.ReconciliationDigest, wire.Dispositions)
	if err != nil || !bytes.Equal(selection.CanonicalBytes(), encoded) {
		return WorkRevisionSelectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "selection_canonical", err)
	}
	return selection, nil
}
