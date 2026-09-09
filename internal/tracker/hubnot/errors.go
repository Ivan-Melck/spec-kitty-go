package hubnot

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// MutationOutcome records what the transport can prove, never global CAS.
type MutationOutcome string

const (
	OutcomeUnstarted  MutationOutcome = "unstarted"
	OutcomeNotApplied MutationOutcome = "not_applied"
	OutcomeApplied    MutationOutcome = "applied"
	OutcomeAmbiguous  MutationOutcome = "ambiguous"
)

// CommandError preserves bounded diagnostics and outcome through errors.As.
// Higher-level sagas must retain their prior applied effects separately.
type CommandError struct {
	Outcome      MutationOutcome
	ProviderCode string
	Diagnostic   string
	Cause        error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("hubnot command %s: %v", e.Outcome, e.Cause)
}
func (e *CommandError) Unwrap() error { return e.Cause }
func providerFailure(code, message string) error {
	kind, recovery := tracker.ErrorUnavailable, "bounded_retry"
	switch code {
	case "invalid_input", "resource_limit":
		kind, recovery = tracker.ErrorInvalidRequest, "repair_request"
	case "actor_mismatch":
		kind, recovery = tracker.ErrorWorkspaceMismatch, "repair_provider_binding"
	case "not_found":
		kind, recovery = tracker.ErrorNotFound, "reconcile_mapping"
	case "operation_conflict":
		kind, recovery = tracker.ErrorAttributionLost, "reconcile_without_mutation"
	case "issue_conflict", "stale_revision", "stale_cursor", "concurrent_update":
		kind, recovery = tracker.ErrorRevisionConflict, "observe_then_reconcile"
	case "invalid_history":
		kind, recovery = tracker.ErrorMalformedProtocol, "repair_provider_binding"
	}
	return failure(kind, recovery, message, nil)
}

// Only errors which the admitted CLI emits before append can prove no write.
// invalid_history and repository_error may also occur after append.
func provesNoWrite(code string) bool {
	switch code {
	case "invalid_input", "actor_mismatch", "not_found", "operation_conflict", "issue_conflict", "stale_revision":
		return true
	}
	return false
}
