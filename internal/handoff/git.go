package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const BranchMovementDivergence = "owned_branch_moved"

// CandidateRepository is the semantic repository port required by candidate
// verification. Raw Git commands and transaction protocols stay in adapters.
type CandidateRepository interface {
	ResolveCommit(context.Context, string, string) (string, error)
	IsAncestor(context.Context, string, string, string) error
	CreateEvidenceRefs(context.Context, string, EvidenceRef, EvidenceRef) error
	ReadCommitRef(context.Context, string, string) (string, bool)
	CommitExists(context.Context, string, string) error
	RepairEvidenceRefs(context.Context, string, []EvidenceRef) error
}

type GitVerifier struct {
	Repository CandidateRepository
}

// EvidenceRef describes one immutable product-owned commit reference at the
// repository port boundary.
type EvidenceRef struct {
	Name     string
	Expected string
	Exists   bool
}

// VerifyCandidateAuthority is the selected production-reachability guard. It
// rejects adapter evidence that is not bound to the requested immutable facts.
func VerifyCandidateAuthority(ctx context.Context, verifier CandidateVerifier, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	evidence, err := verifier.Verify(ctx, scope, candidate)
	if err != nil {
		return CandidateEvidence{}, err
	}
	if evidence.Candidate != candidate || evidence.PinnedBase != scope.PinnedBase || evidence.OwnedBranch != scope.OwnedBranch || evidence.ObservedBranchHead != candidate {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "candidate verification did not bind the expected lane branch and pinned base"}
	}
	return evidence, nil
}

type evidenceRefFact struct {
	label, name, expected, actual string
	exists                        bool
}

func (v GitVerifier) Verify(ctx context.Context, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	if v.Repository == nil {
		return CandidateEvidence{}, &Error{Kind: KindInvalid, Message: "candidate verifier repository port is required"}
	}
	if scope.RepositoryPath == "" || scope.OwnedBranch == "" || !ValidCommit(scope.PinnedBase) || !ValidCommit(candidate) {
		return CandidateEvidence{}, &Error{Kind: KindInvalid, Message: "candidate verification scope is incomplete"}
	}
	resolvedCandidate, err := v.Repository.ResolveCommit(ctx, scope.RepositoryPath, candidate)
	if err != nil || resolvedCandidate != candidate {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "candidate is not an exact existing commit", Observed: map[string]string{"candidate": candidate}, Expected: map[string]string{"candidate": "exact commit object"}, Cause: err}
	}
	resolvedBase, err := v.Repository.ResolveCommit(ctx, scope.RepositoryPath, scope.PinnedBase)
	if err != nil || resolvedBase != scope.PinnedBase {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "pinned base is not an exact existing commit", Cause: err}
	}
	if err := v.Repository.IsAncestor(ctx, scope.RepositoryPath, scope.PinnedBase, candidate); err != nil {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "candidate does not descend from the pinned base", Observed: map[string]string{"candidate": candidate}, Expected: map[string]string{"ancestor": scope.PinnedBase}, Cause: err}
	}
	branch := scope.OwnedBranch
	if !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}
	branchHead, err := v.Repository.ResolveCommit(ctx, scope.RepositoryPath, branch)
	if err != nil {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: fmt.Sprintf("owned lane branch %s was not found", scope.OwnedBranch), Cause: err}
	}
	if branchHead != candidate {
		root, preserveErr := v.preserveBranchMovement(ctx, scope, candidate, branchHead)
		observed := map[string]string{
			"candidate": candidate, "branch_head": branchHead, "owned_branch": scope.OwnedBranch,
			"divergence_kind": BranchMovementDivergence, "repository_id": scope.RepositoryID.String(),
			"operation_id": scope.OperationID.String(), "handoff_id": scope.HandoffID.String(),
			"work_revision_id": scope.WorkRevisionID.String(), "work_id": scope.WorkID.String(),
		}
		if root != "" {
			observed["evidence_ref_root"] = root
			observed["evidence_candidate_ref"] = root + "/candidate"
			observed["evidence_observed_ref"] = root + "/observed"
		}
		message := "candidate is not the observed head of the owned lane branch; both Git facts were preserved"
		if preserveErr != nil {
			message = "candidate is not the observed head of the owned lane branch and its recovery evidence could not be preserved"
		}
		return CandidateEvidence{}, &Error{
			Kind: KindCandidateMismatch, Message: message, Observed: observed,
			Expected: map[string]string{"candidate": candidate, "branch_head": candidate, "divergence_kind": "none"},
			RecoveryActions: []string{
				"inspect the product-owned candidate and observed refs before changing the owned branch",
				"choose explicitly whether to restore the owned branch to the candidate or supersede the handoff",
			},
			Cause: preserveErr,
		}
	}
	return CandidateEvidence{Candidate: candidate, PinnedBase: scope.PinnedBase, OwnedBranch: scope.OwnedBranch, ObservedBranchHead: branchHead}, nil
}

func (v GitVerifier) preserveBranchMovement(ctx context.Context, scope AuthorityScope, candidate, observed string) (string, error) {
	identity := strings.Join([]string{scope.RepositoryID.String(), scope.OperationID.String(), scope.HandoffID.String(), scope.WorkRevisionID.String(), scope.WorkID.String()}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	root := "refs/spec-kitty/handoff-evidence/" + hex.EncodeToString(digest[:])
	candidateRef, observedRef := root+"/candidate", root+"/observed"
	if v.evidenceRefsMatch(ctx, scope.RepositoryPath, candidateRef, observedRef, candidate, observed) {
		return root, nil
	}
	if err := v.Repository.CreateEvidenceRefs(ctx, scope.RepositoryPath,
		EvidenceRef{Name: candidateRef, Expected: candidate},
		EvidenceRef{Name: observedRef, Expected: observed}); err != nil {
		// A concurrent replay can win the create transaction. Exact reread is
		// success; any other state is an attributable preservation conflict.
		if v.evidenceRefsMatch(ctx, scope.RepositoryPath, candidateRef, observedRef, candidate, observed) {
			return root, nil
		}
		return root, fmt.Errorf("create atomic branch-movement evidence refs: %w", err)
	}
	if !v.evidenceRefsMatch(ctx, scope.RepositoryPath, candidateRef, observedRef, candidate, observed) {
		return root, fmt.Errorf("branch-movement evidence refs do not match the preserved facts")
	}
	return root, nil
}

func (v GitVerifier) evidenceRefsMatch(ctx context.Context, repository, candidateRef, observedRef, candidate, observed string) bool {
	gotCandidate, candidateExists := v.Repository.ReadCommitRef(ctx, repository, candidateRef)
	gotObserved, observedExists := v.Repository.ReadCommitRef(ctx, repository, observedRef)
	return candidateExists && observedExists && gotCandidate == candidate && gotObserved == observed
}

// RevalidateRefusal checks the product-owned refs named by an immutable
// refusal. Exact missing refs are recreated together only while their commit
// objects remain available. A ref pointing elsewhere is external state: it is
// reported and never overwritten.
func (v GitVerifier) RevalidateRefusal(ctx context.Context, refusal Refusal) error {
	if v.Repository == nil {
		return &Error{Kind: KindInvalid, Message: "candidate verifier repository port is required"}
	}
	root := refusal.Observed["evidence_ref_root"]
	candidateRef, observedRef := refusal.Observed["evidence_candidate_ref"], refusal.Observed["evidence_observed_ref"]
	candidate, observed := refusal.Observed["candidate"], refusal.Observed["branch_head"]
	if refusal.RepositoryPath == "" || root == "" || !strings.HasPrefix(root, "refs/spec-kitty/handoff-evidence/") || strings.ContainsAny(root, "\n\r \t") || candidateRef != root+"/candidate" || observedRef != root+"/observed" || !ValidCommit(candidate) || !ValidCommit(observed) {
		return &Error{Kind: KindCandidateMismatch, Message: "handoff evidence record is incomplete", Observed: map[string]string{"preservation_kind": EvidenceRepairConflict}, Expected: map[string]string{"evidence_record": "exact product-owned candidate and observed refs"}, RecoveryActions: evidenceConflictActions()}
	}
	facts := []evidenceRefFact{{"candidate", candidateRef, candidate, "", false}, {"observed", observedRef, observed, "", false}}
	for index := range facts {
		facts[index].actual, facts[index].exists = v.readEvidenceRef(ctx, refusal.RepositoryPath, facts[index].name)
		if facts[index].exists && facts[index].actual != facts[index].expected {
			return evidenceConflict(refusal, facts, EvidenceRefMutated, nil)
		}
	}
	missing := false
	for index := range facts {
		missing = missing || !facts[index].exists
		if err := v.Repository.CommitExists(ctx, refusal.RepositoryPath, facts[index].expected); err != nil {
			return evidenceConflict(refusal, facts, EvidenceObjectMissing, err)
		}
	}
	if !missing {
		return nil
	}
	repairs := make([]EvidenceRef, 0, len(facts))
	for _, fact := range facts {
		repairs = append(repairs, EvidenceRef{Name: fact.name, Expected: fact.expected, Exists: fact.exists})
	}
	transactionErr := v.Repository.RepairEvidenceRefs(ctx, refusal.RepositoryPath, repairs)
	for index := range facts {
		facts[index].actual, facts[index].exists = v.readEvidenceRef(ctx, refusal.RepositoryPath, facts[index].name)
	}
	for _, fact := range facts {
		if !fact.exists || fact.actual != fact.expected {
			return evidenceConflict(refusal, facts, EvidenceRepairConflict, transactionErr)
		}
	}
	return nil
}

func (v GitVerifier) readEvidenceRef(ctx context.Context, repository, ref string) (string, bool) {
	value, exists := v.Repository.ReadCommitRef(ctx, repository, ref)
	return value, exists && ValidCommit(value)
}

func evidenceConflict(refusal Refusal, facts []evidenceRefFact, kind string, cause error) error {
	observedState := map[string]string{"preservation_kind": kind, "evidence_ref_root": refusal.Observed["evidence_ref_root"]}
	expectedState := map[string]string{"preservation_kind": "exact_refs"}
	for _, fact := range facts {
		actual := fact.actual
		if !fact.exists {
			actual = EvidenceRefMissing
		}
		observedState["evidence_"+fact.label+"_ref"] = fact.name
		observedState["evidence_"+fact.label+"_ref_head"] = actual
		expectedState["evidence_"+fact.label+"_ref"] = fact.name
		expectedState["evidence_"+fact.label+"_ref_head"] = fact.expected
	}
	return &Error{Kind: KindCandidateMismatch, Message: "handoff evidence ref state conflicts with its immutable refusal", Observed: observedState, Expected: expectedState, RecoveryActions: evidenceConflictActions(), Cause: cause}
}

func evidenceConflictActions() []string {
	return []string{
		"inspect the exact product-owned candidate and observed refs without changing them",
		"choose explicitly whether to restore missing evidence from a trusted object source or supersede the handoff operation",
	}
}
