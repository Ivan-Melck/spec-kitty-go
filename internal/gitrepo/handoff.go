package gitrepo

import (
	"context"
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
)

// NewHandoffVerifier composes the handoff authority guard with the concrete
// Git subprocess adapter. Authority packages depend only on handoff's port.
func NewHandoffVerifier() handoff.CandidateVerifier {
	return guardedHandoffVerifier{verifier: handoff.GitVerifier{Repository: handoffRepository{runner: Runner{}}}}
}

type guardedHandoffVerifier struct{ verifier handoff.GitVerifier }

func (v guardedHandoffVerifier) Verify(ctx context.Context, scope handoff.AuthorityScope, candidate string) (handoff.CandidateEvidence, error) {
	return handoff.VerifyCandidateAuthority(ctx, v.verifier, scope, candidate)
}

func (v guardedHandoffVerifier) RevalidateRefusal(ctx context.Context, refusal handoff.Refusal) error {
	return v.verifier.RevalidateRefusal(ctx, refusal)
}

type handoffRepository struct{ runner Runner }

func (r handoffRepository) ResolveCommit(ctx context.Context, repository, revision string) (string, error) {
	return r.runner.Run(ctx, repository, "rev-parse", "--verify", revision+"^{commit}")
}

func (r handoffRepository) IsAncestor(ctx context.Context, repository, ancestor, descendant string) error {
	_, err := r.runner.Run(ctx, repository, "merge-base", "--is-ancestor", ancestor, descendant)
	return err
}

func (r handoffRepository) CreateEvidenceRefs(ctx context.Context, repository string, candidate, observed handoff.EvidenceRef) error {
	input := fmt.Sprintf("start\ncreate %s %s\ncreate %s %s\nprepare\ncommit\n", candidate.Name, candidate.Expected, observed.Name, observed.Expected)
	_, err := r.runner.RunInput(ctx, repository, input, "update-ref", "--stdin")
	return err
}

func (r handoffRepository) ReadCommitRef(ctx context.Context, repository, ref string) (string, bool) {
	value, err := r.runner.Run(ctx, repository, "show-ref", "--hash", "--verify", ref)
	return value, err == nil
}

func (r handoffRepository) CommitExists(ctx context.Context, repository, commit string) error {
	_, err := r.runner.Run(ctx, repository, "cat-file", "-e", commit+"^{commit}")
	return err
}

func (r handoffRepository) RepairEvidenceRefs(ctx context.Context, repository string, refs []handoff.EvidenceRef) error {
	var input strings.Builder
	input.WriteString("start\n")
	for _, ref := range refs {
		if ref.Exists {
			_, _ = fmt.Fprintf(&input, "verify %s %s\n", ref.Name, ref.Expected)
		} else {
			_, _ = fmt.Fprintf(&input, "create %s %s\n", ref.Name, ref.Expected)
		}
	}
	input.WriteString("prepare\ncommit\n")
	_, err := r.runner.RunInput(ctx, repository, input.String(), "update-ref", "--stdin")
	return err
}
