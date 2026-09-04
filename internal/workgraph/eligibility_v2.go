package workgraph

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const (
	EligibilityPolicyVersionV2      = "spk.workgraph.eligibility-shards.v1"
	EligibilityHashAlgorithmV2      = "sha256-length-prefixed-mission-work-v1"
	EligibilityShardCountV2         = 64
	EligibilityMaxEntriesPerShardV2 = 256
	EligibilityMaxParallelWritersV2 = 32
	EligibilityRequiredTrialsV2     = 100
	EligibilityContractSHA256V2     = "8ea2387a39847c307e40d55cecc21bf9ef2dad9ad149f352b5317eaa1f3abd85"
	EligibilityAdmittedM1TupleV2    = "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
	eligibilityPersistentTemplateV2 = "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>"
	eligibilityGuardSchemaVersionV2 = 1
)

type EligibilityPolicy struct {
	template, contract, admission, version, hash string
	shards, entries, writers, trials             int
}

func NewEligibilityPolicy() EligibilityPolicy {
	return EligibilityPolicy{eligibilityPersistentTemplateV2, EligibilityContractSHA256V2, EligibilityAdmittedM1TupleV2, EligibilityPolicyVersionV2, EligibilityHashAlgorithmV2, EligibilityShardCountV2, EligibilityMaxEntriesPerShardV2, EligibilityMaxParallelWritersV2, EligibilityRequiredTrialsV2}
}
func (p EligibilityPolicy) PersistentTemplate() string    { return p.template }
func (p EligibilityPolicy) ContractSHA256() string        { return p.contract }
func (p EligibilityPolicy) AdmittedM1TupleSHA256() string { return p.admission }
func (p EligibilityPolicy) Version() string               { return p.version }
func (p EligibilityPolicy) HashAlgorithm() string         { return p.hash }
func (p EligibilityPolicy) ShardCount() int               { return p.shards }
func (p EligibilityPolicy) MaxEntriesPerShard() int       { return p.entries }

func (p EligibilityPolicy) ShardFor(missionID ids.MissionID, workID ids.WorkID) (journalv2.EligibilityShardID, error) {
	if p != NewEligibilityPolicy() {
		return "", newError(KindInvalidReconciliation, "eligibility policy is not the frozen M2 policy", nil)
	}
	if _, err := ids.ParseMissionID(missionID.String()); err != nil {
		return "", newError(KindInvalidReconciliation, "eligibility mission identity is invalid", nil)
	}
	if _, err := ids.ParseWorkID(workID.String()); err != nil {
		return "", newError(KindInvalidReconciliation, "eligibility work identity is invalid", nil)
	}
	hash := sha256.New()
	var length [8]byte
	for _, value := range []string{missionID.String(), workID.String()} {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	shard := int(binary.BigEndian.Uint64(hash.Sum(nil)[:8]) & uint64(p.shards-1))
	return journalv2.NewEligibilityShardID(fmt.Sprintf("%02x", shard))
}

type EligibilityGuardEntryV2 struct {
	workID      ids.WorkID
	generations []eligibilityGenerationV2
}

type eligibilityGenerationV2 struct {
	WorkRevisionID   ids.WorkRevisionID       `json:"work_revision_id"`
	ReconciliationID string                   `json:"reconciliation_id"`
	Disposition      Disposition              `json:"disposition"`
	Permissions      eligibilityPermissionsV2 `json:"permissions"`
}

func (e EligibilityGuardEntryV2) WorkID() ids.WorkID { return e.workID }
func (e EligibilityGuardEntryV2) GenerationCount() int {
	return len(e.generations)
}

type eligibilityPermissionsV2 struct {
	Start       bool `json:"start"`
	Handoff     bool `json:"handoff"`
	Review      bool `json:"review"`
	Acceptance  bool `json:"acceptance"`
	Integration bool `json:"integration"`
}

// Permission projects one member of the closed operation set. The durable
// entry is one compact fact per (WorkRevision, WorkID), not one record per
// operation, so the frozen 64x256 capacity can admit large work graphs.
func (e EligibilityGuardEntryV2) Permission(revisionID ids.WorkRevisionID, operation EligibilityOperation) (permitted bool, status WorkStatus, reason string, ok bool) {
	status, ok = eligibilityStatusForOperationV2(operation)
	if !ok {
		return false, "", "", false
	}
	var generation eligibilityGenerationV2
	found := false
	for _, candidate := range e.generations {
		if candidate.WorkRevisionID == revisionID {
			generation = candidate
			found = true
			break
		}
	}
	if !found {
		return false, "", "", false
	}
	switch operation {
	case EligibilityStart:
		permitted = generation.Permissions.Start
	case EligibilityHandoff:
		permitted = generation.Permissions.Handoff
	case EligibilityReview:
		permitted = generation.Permissions.Review
	case EligibilityAcceptance:
		permitted = generation.Permissions.Acceptance
	case EligibilityIntegration:
		permitted = generation.Permissions.Integration
	}
	return permitted, status, consequenceFor(generation.Disposition, status).Reason, true
}

type eligibilityGuardEntryWireV2 struct {
	WorkID      ids.WorkID                `json:"work_id"`
	Generations []eligibilityGenerationV2 `json:"generations"`
}
type EligibilityGuardShardV2 struct {
	schemaVersion uint16
	policyVersion string
	missionID     ids.MissionID
	shardID       journalv2.EligibilityShardID
	entries       []EligibilityGuardEntryV2
	revocations   []eligibilityRevocationV2
}

type eligibilityRevocationV2 struct {
	WorkID         ids.WorkID
	WorkRevisionID ids.WorkRevisionID
}

func (s EligibilityGuardShardV2) MissionID() ids.MissionID              { return s.missionID }
func (s EligibilityGuardShardV2) ShardID() journalv2.EligibilityShardID { return s.shardID }
func (s EligibilityGuardShardV2) Entries() []EligibilityGuardEntryV2 {
	result := make([]EligibilityGuardEntryV2, len(s.entries))
	for index, entry := range s.entries {
		result[index] = entry
		result[index].generations = append([]eligibilityGenerationV2(nil), entry.generations...)
	}
	return result
}

func (s EligibilityGuardShardV2) revocationSet() []eligibilityRevocationV2 {
	return append([]eligibilityRevocationV2(nil), s.revocations...)
}

type eligibilityGuardShardWireV2 struct {
	SchemaVersion uint16                        `json:"schema_version"`
	PolicyVersion string                        `json:"policy_version"`
	MissionID     ids.MissionID                 `json:"mission_id"`
	ShardID       journalv2.EligibilityShardID  `json:"shard_id"`
	Entries       []eligibilityGuardEntryWireV2 `json:"entries"`
}

func newEligibilityGuardEntryV2(revisionID ids.WorkRevisionID, reconciliationID string, workID ids.WorkID, disposition Disposition) (EligibilityGuardEntryV2, error) {
	generation := eligibilityGenerationV2{WorkRevisionID: revisionID, ReconciliationID: reconciliationID, Disposition: disposition}
	for _, operation := range allEligibilityOperationsV2() {
		status, _ := eligibilityStatusForOperationV2(operation)
		fact, err := NewLegacyEligibilityFact(reconciliationID, revisionID, workID, status, operation, consequenceFor(disposition, status))
		if err != nil {
			return EligibilityGuardEntryV2{}, err
		}
		switch operation {
		case EligibilityStart:
			generation.Permissions.Start = fact.Permitted()
		case EligibilityHandoff:
			generation.Permissions.Handoff = fact.Permitted()
		case EligibilityReview:
			generation.Permissions.Review = fact.Permitted()
		case EligibilityAcceptance:
			generation.Permissions.Acceptance = fact.Permitted()
		case EligibilityIntegration:
			generation.Permissions.Integration = fact.Permitted()
		}
	}
	return EligibilityGuardEntryV2{workID: workID, generations: []eligibilityGenerationV2{generation}}, nil
}

func BuildInitialEligibilityGuardShards(revision RevisionV2, lineageID string, operations []EligibilityOperation) ([]EligibilityGuardShardV2, error) {
	if revision.SchemaVersion() != RevisionSchemaV3 || !completeEligibilityOperationSetV2(operations) {
		return nil, newError(KindInvalidRevision, "initial eligibility requires WorkRevision v3", nil)
	}
	entries := make([]EligibilityGuardEntryV2, 0, len(revision.WorkItems()))
	for _, item := range revision.WorkItems() {
		entry, err := newEligibilityGuardEntryV2(revision.ID(), lineageID, item.WorkID(), Continue)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return groupEligibilityGuardShardsV2(revision.MissionID(), operations, entries)
}

func BuildEligibilityGuardShards(successor RevisionV2, reconciliation MixedReconciliation, operations []EligibilityOperation) ([]EligibilityGuardShardV2, error) {
	if !completeEligibilityOperationSetV2(operations) {
		return nil, newError(KindInvalidReconciliation, "eligibility operation set is incomplete or unbounded", nil)
	}
	entries := make([]EligibilityGuardEntryV2, 0, len(reconciliation.OldWorkIDs())+len(successor.WorkItems()))
	revocations := make([]eligibilityRevocationV2, 0, len(reconciliation.OldWorkIDs()))
	for _, workID := range reconciliation.OldWorkIDs() {
		decision, ok := reconciliation.Decision(workID)
		if !ok {
			return nil, newError(KindInvalidReconciliation, "eligibility decision is absent", nil)
		}
		entry, err := newEligibilityGuardEntryV2(reconciliation.OldRevisionID(), reconciliation.ID(), workID, decision.Disposition())
		if err != nil {
			return nil, err
		}
		if entry.anyPermitted() {
			entries = append(entries, entry)
		} else {
			revocations = append(revocations, eligibilityRevocationV2{WorkID: workID, WorkRevisionID: reconciliation.OldRevisionID()})
		}
	}
	for _, item := range successor.WorkItems() {
		entry, err := newEligibilityGuardEntryV2(successor.ID(), reconciliation.ID(), item.WorkID(), Continue)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return groupEligibilityGuardShardChangesV2(successor.MissionID(), operations, entries, revocations)
}

func groupEligibilityGuardShardsV2(missionID ids.MissionID, operations []EligibilityOperation, entries []EligibilityGuardEntryV2) ([]EligibilityGuardShardV2, error) {
	return groupEligibilityGuardShardChangesV2(missionID, operations, entries, nil)
}

func groupEligibilityGuardShardChangesV2(missionID ids.MissionID, operations []EligibilityOperation, entries []EligibilityGuardEntryV2, revocations []eligibilityRevocationV2) ([]EligibilityGuardShardV2, error) {
	if !completeEligibilityOperationSetV2(operations) {
		return nil, newError(KindInvalidReconciliation, "eligibility operation set is incomplete or unbounded", nil)
	}
	policy := NewEligibilityPolicy()
	byShard := make(map[journalv2.EligibilityShardID]map[ids.WorkID]EligibilityGuardEntryV2)
	for _, entry := range entries {
		shardID, err := policy.ShardFor(missionID, entry.workID)
		if err != nil {
			return nil, err
		}
		if byShard[shardID] == nil {
			byShard[shardID] = make(map[ids.WorkID]EligibilityGuardEntryV2)
		}
		combined, err := combineEligibilityEntriesV2(byShard[shardID][entry.workID], entry)
		if err != nil {
			return nil, err
		}
		byShard[shardID][entry.workID] = combined
	}
	revocationsByShard := make(map[journalv2.EligibilityShardID][]eligibilityRevocationV2)
	for _, revocation := range revocations {
		if _, err := ids.ParseWorkID(revocation.WorkID.String()); err != nil || revocation.WorkRevisionID.String() == "" {
			return nil, newError(KindInvalidReconciliation, "eligibility revocation is incomplete", nil)
		}
		shardID, err := policy.ShardFor(missionID, revocation.WorkID)
		if err != nil {
			return nil, err
		}
		revocationsByShard[shardID] = append(revocationsByShard[shardID], revocation)
		if byShard[shardID] == nil {
			byShard[shardID] = make(map[ids.WorkID]EligibilityGuardEntryV2)
		}
	}
	shardIDs := make([]journalv2.EligibilityShardID, 0, len(byShard))
	for id := range byShard {
		shardIDs = append(shardIDs, id)
	}
	sort.Slice(shardIDs, func(i, j int) bool { return shardIDs[i] < shardIDs[j] })
	result := make([]EligibilityGuardShardV2, 0, len(shardIDs))
	for _, id := range shardIDs {
		values := make([]EligibilityGuardEntryV2, 0, len(byShard[id]))
		for _, entry := range byShard[id] {
			values = append(values, entry)
		}
		sort.Slice(values, func(i, j int) bool { return eligibilityEntryKeyV2(values[i]) < eligibilityEntryKeyV2(values[j]) })
		if len(values) > policy.entries {
			return nil, newError(KindInvalidReconciliation, "eligibility shard exceeds its fixed entry budget", nil)
		}
		revoked := revocationsByShard[id]
		sort.Slice(revoked, func(i, j int) bool {
			if revoked[i].WorkID == revoked[j].WorkID {
				return revoked[i].WorkRevisionID < revoked[j].WorkRevisionID
			}
			return revoked[i].WorkID < revoked[j].WorkID
		})
		for index := 1; index < len(revoked); index++ {
			if revoked[index] == revoked[index-1] {
				return nil, newError(KindInvalidReconciliation, "eligibility revocations are not unique", nil)
			}
		}
		if len(revoked) > policy.entries {
			return nil, newError(KindInvalidReconciliation, "eligibility shard exceeds its fixed revocation budget", nil)
		}
		result = append(result, EligibilityGuardShardV2{schemaVersion: eligibilityGuardSchemaVersionV2, policyVersion: policy.version, missionID: missionID, shardID: id, entries: values, revocations: revoked})
	}
	return result, nil
}

func completeEligibilityOperationSetV2(operations []EligibilityOperation) bool {
	if len(operations) != 5 {
		return false
	}
	seen := map[EligibilityOperation]bool{}
	for _, op := range operations {
		if seen[op] {
			return false
		}
		seen[op] = true
	}
	return seen[EligibilityStart] && seen[EligibilityHandoff] && seen[EligibilityReview] && seen[EligibilityAcceptance] && seen[EligibilityIntegration]
}

func allEligibilityOperationsV2() []EligibilityOperation {
	return []EligibilityOperation{EligibilityStart, EligibilityHandoff, EligibilityReview, EligibilityAcceptance, EligibilityIntegration}
}

func eligibilityStatusForOperationV2(operation EligibilityOperation) (WorkStatus, bool) {
	switch operation {
	case EligibilityStart:
		return StatusActive, true
	case EligibilityHandoff:
		return StatusActive, true
	case EligibilityReview:
		return StatusHandedOff, true
	case EligibilityIntegration:
		return StatusApproved, true
	case EligibilityAcceptance:
		return StatusApproved, true
	default:
		return "", false
	}
}
func eligibilityEntryKeyV2(entry EligibilityGuardEntryV2) string {
	return entry.workID.String()
}

func (s EligibilityGuardShardV2) CanonicalBytes() ([]byte, error) {
	if s.schemaVersion != eligibilityGuardSchemaVersionV2 || s.policyVersion != EligibilityPolicyVersionV2 || len(s.entries) > EligibilityMaxEntriesPerShardV2 || len(s.revocations) != 0 {
		return nil, newError(KindInvalidReconciliation, "eligibility shard is malformed", nil)
	}
	if _, err := ids.ParseMissionID(s.missionID.String()); err != nil {
		return nil, newError(KindInvalidReconciliation, "eligibility shard mission is malformed", nil)
	}
	if _, err := journalv2.NewEligibilityShardID(string(s.shardID)); err != nil {
		return nil, newError(KindInvalidReconciliation, "eligibility shard identity is malformed", nil)
	}
	policy := NewEligibilityPolicy()
	previous := ""
	wireEntries := make([]eligibilityGuardEntryWireV2, len(s.entries))
	for i, e := range s.entries {
		shard, err := policy.ShardFor(s.missionID, e.workID)
		if err != nil || shard != s.shardID || !validEligibilityGuardEntryV2(e) {
			return nil, newError(KindInvalidReconciliation, "eligibility shard entry is incomplete or misplaced", nil)
		}
		key := eligibilityEntryKeyV2(e)
		if key <= previous {
			return nil, newError(KindInvalidReconciliation, "eligibility shard entries are not canonical and unique", nil)
		}
		previous = key
		wireEntries[i] = eligibilityGuardEntryWireV2{e.workID, append([]eligibilityGenerationV2(nil), e.generations...)}
	}
	encoded, err := json.Marshal(eligibilityGuardShardWireV2{s.schemaVersion, s.policyVersion, s.missionID, s.shardID, wireEntries})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}
func DecodeEligibilityGuardShardV2(encoded []byte) (EligibilityGuardShardV2, error) {
	var wire eligibilityGuardShardWireV2
	if err := decodeStrictWorkgraphJSON(encoded, &wire); err != nil {
		return EligibilityGuardShardV2{}, newError(KindInvalidReconciliation, "eligibility shard is malformed", nil)
	}
	entries := make([]EligibilityGuardEntryV2, len(wire.Entries))
	for i, e := range wire.Entries {
		entries[i] = EligibilityGuardEntryV2{e.WorkID, append([]eligibilityGenerationV2(nil), e.Generations...)}
	}
	shard := EligibilityGuardShardV2{
		schemaVersion: wire.SchemaVersion,
		policyVersion: wire.PolicyVersion,
		missionID:     wire.MissionID,
		shardID:       wire.ShardID,
		entries:       entries,
	}
	canonical, err := shard.CanonicalBytes()
	if err != nil || !bytes.Equal(canonical, encoded) {
		return EligibilityGuardShardV2{}, newError(KindInvalidReconciliation, "eligibility shard does not reconstruct exactly", nil)
	}
	return shard, nil
}
func validEligibilityGuardEntryV2(entry EligibilityGuardEntryV2) bool {
	if len(entry.generations) == 0 {
		return false
	}
	previous := ids.WorkRevisionID("")
	for _, generation := range entry.generations {
		if generation.WorkRevisionID <= previous {
			return false
		}
		want, err := newEligibilityGuardEntryV2(generation.WorkRevisionID, generation.ReconciliationID, entry.workID, generation.Disposition)
		if err != nil || len(want.generations) != 1 || want.generations[0] != generation {
			return false
		}
		previous = generation.WorkRevisionID
	}
	return true
}

func (e EligibilityGuardEntryV2) anyPermitted() bool {
	for _, generation := range e.generations {
		permissions := generation.Permissions
		if permissions.Start || permissions.Handoff || permissions.Review || permissions.Acceptance || permissions.Integration {
			return true
		}
	}
	return false
}

func combineEligibilityEntriesV2(left, right EligibilityGuardEntryV2) (EligibilityGuardEntryV2, error) {
	if left.workID == "" {
		left = EligibilityGuardEntryV2{workID: right.workID}
	}
	if left.workID != right.workID || right.workID == "" {
		return EligibilityGuardEntryV2{}, newError(KindInvalidReconciliation, "eligibility work facts cannot be combined", nil)
	}
	byRevision := make(map[ids.WorkRevisionID]eligibilityGenerationV2, len(left.generations)+len(right.generations))
	for _, entry := range left.generations {
		byRevision[entry.WorkRevisionID] = entry
	}
	for _, entry := range right.generations {
		if prior, exists := byRevision[entry.WorkRevisionID]; exists && prior != entry {
			return EligibilityGuardEntryV2{}, newError(KindInvalidReconciliation, "one generation has conflicting eligibility authority", nil)
		}
		byRevision[entry.WorkRevisionID] = entry
	}
	result := EligibilityGuardEntryV2{workID: left.workID, generations: make([]eligibilityGenerationV2, 0, len(byRevision))}
	for _, entry := range byRevision {
		result.generations = append(result.generations, entry)
	}
	// Every non-revoked named generation remains authoritative. There is no
	// invented per-work semantic ceiling: the frozen ordinary-record and batch
	// budgets provide the bounded publication refusal required by the journal.
	sort.Slice(result.generations, func(i, j int) bool {
		return result.generations[i].WorkRevisionID < result.generations[j].WorkRevisionID
	})
	return result, nil
}

func overlayEligibilityEntryV2(base, changed EligibilityGuardEntryV2) (EligibilityGuardEntryV2, error) {
	changedRevisions := make(map[ids.WorkRevisionID]struct{}, len(changed.generations))
	for _, generation := range changed.generations {
		changedRevisions[generation.WorkRevisionID] = struct{}{}
	}
	kept := EligibilityGuardEntryV2{workID: base.workID}
	for _, generation := range base.generations {
		if _, replaced := changedRevisions[generation.WorkRevisionID]; !replaced {
			kept.generations = append(kept.generations, generation)
		}
	}
	return combineEligibilityEntriesV2(kept, changed)
}
func (s EligibilityGuardShardV2) EligibilityGuardDigest() (string, error) {
	canonical, err := s.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("spec-kitty/workgraph/eligibility-guard/v1\x00"), canonical...))
	return hex.EncodeToString(sum[:]), nil
}

type ObservedEligibilityShardV2 struct {
	shard          EligibilityGuardShardV2
	canonical      []byte
	digest         string
	recordRevision uint64
}

func NewObservedEligibilityShardV2(canonical []byte, recordRevision uint64) (ObservedEligibilityShardV2, error) {
	if recordRevision == 0 {
		return ObservedEligibilityShardV2{}, newError(KindInvalidReconciliation, "eligibility observation revision is absent", nil)
	}
	shard, err := DecodeEligibilityGuardShardV2(canonical)
	if err != nil {
		return ObservedEligibilityShardV2{}, err
	}
	digest, _ := shard.EligibilityGuardDigest()
	return ObservedEligibilityShardV2{shard, append([]byte(nil), canonical...), digest, recordRevision}, nil
}
func (o ObservedEligibilityShardV2) ShardID() journalv2.EligibilityShardID { return o.shard.shardID }
func (o ObservedEligibilityShardV2) MissionID() ids.MissionID              { return o.shard.missionID }
func (o ObservedEligibilityShardV2) RecordRevision() uint64                { return o.recordRevision }
func (o ObservedEligibilityShardV2) Digest() string                        { return o.digest }
func (o ObservedEligibilityShardV2) Entries() []EligibilityGuardEntryV2    { return o.shard.Entries() }

// EligibilityShardObservationV2 distinguishes a verified provider absence
// from a caller that simply omitted a changed shard.
type EligibilityShardObservationV2 struct {
	missionID ids.MissionID
	shardID   journalv2.EligibilityShardID
	present   bool
	observed  ObservedEligibilityShardV2
}

func ObservePresentEligibilityShardV2(observed ObservedEligibilityShardV2) (EligibilityShardObservationV2, error) {
	if observed.RecordRevision() == 0 || observed.MissionID() == "" || observed.ShardID() == "" {
		return EligibilityShardObservationV2{}, newError(KindInvalidReconciliation, "present eligibility observation is incomplete", nil)
	}
	return EligibilityShardObservationV2{missionID: observed.MissionID(), shardID: observed.ShardID(), present: true, observed: observed}, nil
}

func ObserveAbsentEligibilityShardV2(missionID ids.MissionID, shardID journalv2.EligibilityShardID) (EligibilityShardObservationV2, error) {
	if _, err := ids.ParseMissionID(missionID.String()); err != nil {
		return EligibilityShardObservationV2{}, newError(KindInvalidReconciliation, "absent eligibility mission identity is invalid", nil)
	}
	if _, err := journalv2.NewEligibilityShardID(string(shardID)); err != nil {
		return EligibilityShardObservationV2{}, newError(KindInvalidReconciliation, "absent eligibility shard identity is invalid", nil)
	}
	return EligibilityShardObservationV2{missionID: missionID, shardID: shardID}, nil
}

func (o EligibilityShardObservationV2) identity() (ids.MissionID, journalv2.EligibilityShardID) {
	return o.missionID, o.shardID
}

func (o EligibilityShardObservationV2) prior() ([]EligibilityGuardEntryV2, uint64) {
	if !o.present {
		return nil, 0
	}
	return o.observed.Entries(), o.observed.RecordRevision()
}

type EligibilityGuardTokenV2 struct {
	transition journalv2.EligibilityGuardTransition
	workID     ids.WorkID
	revisionID ids.WorkRevisionID
	operation  EligibilityOperation
	reason     string
}

func (o ObservedEligibilityShardV2) Token(workID ids.WorkID, revisionID ids.WorkRevisionID, operation EligibilityOperation) (EligibilityGuardTokenV2, error) {
	for _, e := range o.shard.entries {
		if e.workID == workID {
			permitted, _, reason, known := e.Permission(revisionID, operation)
			if !known || !permitted {
				return EligibilityGuardTokenV2{}, newError(KindInvalidReconciliation, "exact-generation operation is not eligible", nil)
			}
			return EligibilityGuardTokenV2{journalv2.EligibilityGuardTransition{MissionID: journalv2.MissionID(o.shard.missionID), ShardID: o.shard.shardID, Canonical: append([]byte(nil), o.canonical...), ExpectedRecordRevision: o.recordRevision}, workID, revisionID, operation, reason}, nil
		}
	}
	return EligibilityGuardTokenV2{}, newError(KindInvalidReconciliation, "complete exact-generation eligibility is absent", nil)
}
func (t EligibilityGuardTokenV2) Reason() string { return t.reason }
func (t EligibilityGuardTokenV2) Transition() journalv2.EligibilityGuardTransition {
	transition := t.transition
	transition.Canonical = append([]byte(nil), t.transition.Canonical...)
	return transition
}
