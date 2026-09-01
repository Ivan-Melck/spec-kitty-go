package m1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const OrderedTrialID = "m1-ordered-torture-trial"

var CrownSegmentIDs = []string{
	"admission", "external-14", "concurrent-a-a-w1-b-b-w2",
	"gj-01", "gj-02", "expire-quarantine-w1", "gj-03", "gj-04",
	"replace-a-c-w3", "zombie-write-refusal", "candidate-w2",
	"candidate-w3", "review-w2", "review-w3", "integrate-w2",
	"integrate-w3", "gj-05", "owner-restart", "restore-new-epoch",
	"predecessor-refusal", "playback-equality", "focused-8x100",
	"mutants-19", "terminal-universals",
}

type CrownParticipant struct {
	ActorID     string `json:"actor_id"`
	ProcessID   string `json:"process_id"`
	WorkspaceID string `json:"workspace_id"`
}

type CrownIdentities struct {
	ImplementationA CrownParticipant `json:"implementation_a"`
	ImplementationB CrownParticipant `json:"implementation_b"`
	ReplacementC    CrownParticipant `json:"replacement_c"`
}

type CrownWorkspaceIsolation struct {
	W1Quarantined            bool `json:"w1_quarantined"`
	W2Preserved              bool `json:"w2_preserved"`
	W3Fresh                  bool `json:"w3_fresh"`
	ZombieAuthoritySuccesses int  `json:"zombie_authority_successes"`
}

type CrownOwnerRestart struct {
	SameUmbrellaRun      bool   `json:"same_umbrella_run"`
	BeforeRuntimeEpochID string `json:"before_runtime_epoch_id"`
	AfterRuntimeEpochID  string `json:"after_runtime_epoch_id"`
	NewEpochAdmitted     bool   `json:"new_epoch_admitted"`
}

type CrownOldCapsule struct {
	PredecessorRuntimeEpochID string `json:"predecessor_runtime_epoch_id"`
	Refused                   bool   `json:"refused"`
	ReasonKind                string `json:"reason_kind"`
}

type CrownRestorePlayback struct {
	RestoreDigest         string `json:"restore_digest"`
	PlaybackDigest        string `json:"playback_digest"`
	DigestsEqual          bool   `json:"digests_equal"`
	PlaybackAdapterCalls  uint64 `json:"playback_adapter_calls"`
	PlaybackDurableWrites uint64 `json:"playback_durable_writes"`
}

type CrownReview struct {
	CandidateID         string   `json:"candidate_id"`
	ContributorActorIDs []string `json:"contributor_actor_ids"`
	ReviewerActorID     string   `json:"reviewer_actor_id"`
	ActorIndependent    bool     `json:"actor_independent"`
	Verdict             string   `json:"verdict"`
}

type CrownIntegration struct {
	TargetRef              string   `json:"target_ref"`
	CandidateIDs           []string `json:"candidate_ids"`
	PublicationOrder       []string `json:"publication_order"`
	CompetingAttempts      int      `json:"competing_attempts"`
	Serialized             bool     `json:"serialized"`
	SuccessfulPublications int      `json:"successful_publications"`
	FalseCompletions       int      `json:"false_completions"`
}

// OrderedTrial is the frozen schema surface. SegmentLedger is intentionally
// kept in the content-addressed trial artifact: the frozen public schema stays
// stable while the verifier still proves that one real ordered execution, not
// a collection of summaries, produced the result.
type OrderedTrial struct {
	SchemaVersion      int                     `json:"schema_version"`
	RunID              string                  `json:"run_id"`
	TrialID            string                  `json:"trial_id"`
	Started            bool                    `json:"started"`
	Terminal           bool                    `json:"terminal"`
	Outcome            string                  `json:"outcome"`
	FR044Satisfied     bool                    `json:"fr_044_satisfied,omitempty"`
	SC013Satisfied     bool                    `json:"sc_013_satisfied,omitempty"`
	Identities         CrownIdentities         `json:"identities,omitempty,omitzero"`
	WorkspaceIsolation CrownWorkspaceIsolation `json:"workspace_isolation,omitempty,omitzero"`
	OwnerRestart       CrownOwnerRestart       `json:"owner_restart,omitempty,omitzero"`
	OldCapsule         CrownOldCapsule         `json:"old_capsule,omitempty,omitzero"`
	RestorePlayback    CrownRestorePlayback    `json:"restore_playback,omitempty,omitzero"`
	Reviews            []CrownReview           `json:"reviews,omitempty"`
	Integration        CrownIntegration        `json:"integration,omitempty,omitzero"`
	ArtifactSHA256     string                  `json:"artifact_sha256,omitempty"`
}

type CrownSegment struct {
	RunID                 string `json:"run_id"`
	Sequence              int    `json:"sequence"`
	ID                    string `json:"id"`
	BeforeDurableRevision uint64 `json:"before_durable_revision"`
	AfterDurableRevision  uint64 `json:"after_durable_revision"`
	Started               bool   `json:"started"`
	Terminal              bool   `json:"terminal"`
	Outcome               string `json:"outcome"`
	ArtifactSHA256        string `json:"artifact_sha256"`
}

type CrownArtifact struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	Segments      []CrownSegment `json:"segments"`
	Trial         OrderedTrial   `json:"trial"`
}

func ValidateCrownArtifact(artifact CrownArtifact) error {
	if artifact.SchemaVersion != 1 || artifact.RunID == "" || len(artifact.Segments) != len(CrownSegmentIDs) {
		return fmt.Errorf("crown artifact header or exact segment census is invalid")
	}
	for i, want := range CrownSegmentIDs {
		segment := artifact.Segments[i]
		if segment.RunID != artifact.RunID || segment.Sequence != i+1 || segment.ID != want || !segment.Started || !segment.Terminal || segment.Outcome != "pass" || !isSHA256String(segment.ArtifactSHA256) || segment.AfterDurableRevision < segment.BeforeDurableRevision {
			return fmt.Errorf("crown segment %d/%s is missing, reordered, foreign, or non-pass", i+1, want)
		}
	}
	wantDigest, err := CrownLedgerDigest(artifact)
	if err != nil || artifact.Trial.ArtifactSHA256 != wantDigest {
		return fmt.Errorf("ordered trial does not bind the canonical crown ledger: %w", err)
	}
	return ValidateOrderedTrial(artifact.Trial, artifact.RunID)
}

// CrownLedgerDigest binds the ordered trial to the complete canonical ledger
// without a circular file hash. The digest field itself is the only excluded
// byte range; every segment, durable position, identity, and result is bound.
func CrownLedgerDigest(artifact CrownArtifact) (string, error) {
	copy := artifact
	copy.Trial.ArtifactSHA256 = ""
	encoded, err := CanonicalJSON(copy)
	if err != nil {
		return "", err
	}
	return SHA256Hex(encoded), nil
}

func SealCrownArtifact(artifact CrownArtifact) (CrownArtifact, error) {
	digest, err := CrownLedgerDigest(artifact)
	if err != nil {
		return CrownArtifact{}, err
	}
	artifact.Trial.ArtifactSHA256 = digest
	return artifact, nil
}

func ValidateOrderedTrial(trial OrderedTrial, runID string) error {
	if trial.SchemaVersion != 1 || trial.RunID != runID || trial.TrialID != OrderedTrialID || !trial.Started || !trial.Terminal || trial.Outcome != "pass" || !trial.FR044Satisfied || !trial.SC013Satisfied || !isSHA256String(trial.ArtifactSHA256) {
		return fmt.Errorf("ordered trial is not a complete pass for the umbrella RunID")
	}
	if trial.Identities.ImplementationA != (CrownParticipant{"A", "A", "W1"}) || trial.Identities.ImplementationB != (CrownParticipant{"B", "B", "W2"}) || trial.Identities.ReplacementC != (CrownParticipant{"A", "C", "W3"}) {
		return fmt.Errorf("ordered trial participant identities are not A/A/W1, B/B/W2, A/C/W3")
	}
	if !trial.WorkspaceIsolation.W1Quarantined || !trial.WorkspaceIsolation.W2Preserved || !trial.WorkspaceIsolation.W3Fresh || trial.WorkspaceIsolation.ZombieAuthoritySuccesses != 0 {
		return fmt.Errorf("workspace isolation crown facts failed")
	}
	if !trial.OwnerRestart.SameUmbrellaRun || !trial.OwnerRestart.NewEpochAdmitted || trial.OwnerRestart.BeforeRuntimeEpochID == "" || trial.OwnerRestart.AfterRuntimeEpochID == "" || trial.OwnerRestart.BeforeRuntimeEpochID == trial.OwnerRestart.AfterRuntimeEpochID || !trial.OldCapsule.Refused || trial.OldCapsule.PredecessorRuntimeEpochID != trial.OwnerRestart.BeforeRuntimeEpochID || trial.OldCapsule.ReasonKind != "runtime_epoch_conflict" {
		return fmt.Errorf("owner restart or predecessor-capsule facts failed")
	}
	if !trial.RestorePlayback.DigestsEqual || trial.RestorePlayback.RestoreDigest != trial.RestorePlayback.PlaybackDigest || !isSHA256String(trial.RestorePlayback.RestoreDigest) || trial.RestorePlayback.PlaybackAdapterCalls != 0 || trial.RestorePlayback.PlaybackDurableWrites != 0 {
		return fmt.Errorf("restore/playback equality or zero-I/O facts failed")
	}
	if len(trial.Reviews) != 2 || len(trial.Integration.CandidateIDs) != 2 || len(trial.Integration.PublicationOrder) != 2 || trial.Integration.CompetingAttempts < 2 || !trial.Integration.Serialized || trial.Integration.SuccessfulPublications != 2 || trial.Integration.FalseCompletions != 0 || trial.Integration.TargetRef == "" {
		return fmt.Errorf("two-candidate review/integration census failed")
	}
	reviewed := make([]string, 0, 2)
	for _, review := range trial.Reviews {
		if review.CandidateID == "" || review.ReviewerActorID == "" || len(review.ContributorActorIDs) == 0 || !review.ActorIndependent || review.Verdict != "approved" || slices.Contains(review.ContributorActorIDs, review.ReviewerActorID) {
			return fmt.Errorf("candidate review is not ActorID-independent")
		}
		reviewed = append(reviewed, review.CandidateID)
	}
	slices.Sort(reviewed)
	candidates := append([]string(nil), trial.Integration.CandidateIDs...)
	order := append([]string(nil), trial.Integration.PublicationOrder...)
	slices.Sort(candidates)
	slices.Sort(order)
	if !slices.Equal(reviewed, candidates) || !slices.Equal(candidates, order) || candidates[0] == candidates[1] {
		return fmt.Errorf("reviewed and integrated candidates differ")
	}
	return nil
}

func CanonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func SHA256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func isSHA256String(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
