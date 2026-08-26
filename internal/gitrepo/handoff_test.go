package gitrepo

import (
	"context"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
)

func TestNewHandoffVerifierWiresConcreteGitPort(t *testing.T) {
	verifier := NewHandoffVerifier()
	if verifier == nil {
		t.Fatal("nil handoff verifier")
	}
	_, err := verifier.Verify(context.Background(), handoff.AuthorityScope{}, "not-a-commit")
	if !handoff.IsKind(err, handoff.KindInvalid) {
		t.Fatalf("concrete verifier did not execute handoff contract: %v", err)
	}
}
