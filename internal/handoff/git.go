package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
)

const BranchMovementDivergence = "owned_branch_moved"

type GitVerifier struct {
	Runner gitrepo.Runner
}

type evidenceRefFact struct {
	label, name, expected, actual string
	exists                        bool
}

func (v GitVerifier) Verify(ctx context.Context, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	if scope.RepositoryPath == "" || scope.OwnedBranch == "" || !ValidCommit(scope.PinnedBase) || !ValidCommit(candidate) {
		return CandidateEvidence{}, &Error{Kind: KindInvalid, Message: "candidate verification scope is incomplete"}
	}
	resolvedCandidate, err := v.Runner.Run(ctx, scope.RepositoryPath, "rev-parse", "--verify", candidate+"^{commit}")
	if err != nil || resolvedCandidate != candidate {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "candidate is not an exact existing commit", Observed: map[string]string{"candidate": candidate}, Expected: map[string]string{"candidate": "exact commit object"}, Cause: err}
	}
	resolvedBase, err := v.Runner.Run(ctx, scope.RepositoryPath, "rev-parse", "--verify", scope.PinnedBase+"^{commit}")
	if err != nil || resolvedBase != scope.PinnedBase {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "pinned base is not an exact existing commit", Cause: err}
	}
	if _, err := v.Runner.Run(ctx, scope.RepositoryPath, "merge-base", "--is-ancestor", scope.PinnedBase, candidate); err != nil {
		return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: "candidate does not descend from the pinned base", Observed: map[string]string{"candidate": candidate}, Expected: map[string]string{"ancestor": scope.PinnedBase}, Cause: err}
	}
	branch := scope.OwnedBranch
	if !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}
	branchHead, err := v.Runner.Run(ctx, scope.RepositoryPath, "rev-parse", "--verify", branch+"^{commit}")
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
	input := fmt.Sprintf("start\ncreate %s %s\ncreate %s %s\nprepare\ncommit\n", candidateRef, candidate, observedRef, observed)
	if _, err := v.Runner.RunInput(ctx, scope.RepositoryPath, input, "update-ref", "--stdin"); err != nil {
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
	gotCandidate, candidateErr := v.Runner.Run(ctx, repository, "rev-parse", "--verify", candidateRef+"^{commit}")
	gotObserved, observedErr := v.Runner.Run(ctx, repository, "rev-parse", "--verify", observedRef+"^{commit}")
	return candidateErr == nil && observedErr == nil && gotCandidate == candidate && gotObserved == observed
}

// RevalidateRefusal checks the product-owned refs named by an immutable
// refusal. Exact missing refs are recreated together only while their commit
// objects remain available. A ref pointing elsewhere is external state: it is
// reported and never overwritten.
func (v GitVerifier) RevalidateRefusal(ctx context.Context, refusal Refusal) error {
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
		if _, err := v.Runner.Run(ctx, refusal.RepositoryPath, "cat-file", "-e", facts[index].expected+"^{commit}"); err != nil {
			return evidenceConflict(refusal, facts, EvidenceObjectMissing, err)
		}
	}
	if !missing {
		return nil
	}
	var transaction strings.Builder
	transaction.WriteString("start\n")
	for _, fact := range facts {
		if fact.exists {
			_, _ = fmt.Fprintf(&transaction, "verify %s %s\n", fact.name, fact.expected)
		} else {
			_, _ = fmt.Fprintf(&transaction, "create %s %s\n", fact.name, fact.expected)
		}
	}
	transaction.WriteString("prepare\ncommit\n")
	_, transactionErr := v.Runner.RunInput(ctx, refusal.RepositoryPath, transaction.String(), "update-ref", "--stdin")
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
	value, err := v.Runner.Run(ctx, repository, "show-ref", "--hash", "--verify", ref)
	return value, err == nil && ValidCommit(value)
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
