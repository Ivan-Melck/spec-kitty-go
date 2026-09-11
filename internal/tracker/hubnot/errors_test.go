package hubnot

import (
	"errors"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"testing"
)

func TestErrorsPreserveRepairAndMutationKnowledge(t *testing.T) {
	for _, tc := range []struct {
		code    string
		kind    tracker.ErrorKind
		repair  string
		noWrite bool
	}{
		{"actor_mismatch", tracker.ErrorWorkspaceMismatch, "repair_provider_binding", true},
		{"not_found", tracker.ErrorNotFound, "reconcile_mapping", true},
		{"operation_conflict", tracker.ErrorAttributionLost, "reconcile_without_mutation", true},
		{"invalid_history", tracker.ErrorMalformedProtocol, "repair_provider_binding", false},
		{"repository_error", tracker.ErrorUnavailable, "bounded_retry", false},
		{"concurrent_update", tracker.ErrorRevisionConflict, "observe_then_reconcile", false},
		{"unknown_future_code", tracker.ErrorUnavailable, "bounded_retry", false},
	} {
		err := &CommandError{Outcome: OutcomeAmbiguous, ProviderCode: tc.code, Cause: providerFailure(tc.code, "original provider cause")}
		var portable *tracker.Error
		if !errors.As(err, &portable) || portable.Kind != tc.kind || portable.RecoveryAction() != tc.repair || provesNoWrite(tc.code) != tc.noWrite {
			t.Fatalf("bad classification %s: %v", tc.code, err)
		}
	}
}
