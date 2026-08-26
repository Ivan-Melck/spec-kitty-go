package tracker

import (
	"context"
	"errors"
	"fmt"
)

// Revision is an opaque provider observation identity. Consumers may compare
// revisions for equality and validate the zero value, but must not infer
// ordering, freshness, timestamps, or conditional-mutation support from its
// numeric representation.
type Revision uint64

type Comment struct {
	Body     string   `json:"body"`
	Marker   string   `json:"marker"`
	Revision Revision `json:"revision"`
}

type Work struct {
	Key              string
	Title            string
	Description      string
	Criteria         []string
	Dependencies     []string
	Assignee         string
	AssignmentMarker string
	Comments         []Comment
	Revision         Revision
}

type AssignmentRequest struct {
	WorkKey          string
	ExpectedRevision Revision
	Assignee         string
	Marker           string
}

type CommentRequest struct {
	WorkKey          string
	ExpectedRevision Revision
	Body             string
	Marker           string
}

type CompensationRequest struct {
	WorkKey          string
	ExpectedRevision Revision
	ExpectedAssignee string
	Marker           string
	RestoreAssignee  string
}

type Mutation struct {
	WorkKey  string
	Assignee string
	Marker   string
	Revision Revision
	Replayed bool
	Noop     bool
}

type WorkReader interface {
	Read(context.Context, string) (Work, error)
}

type AssignmentProjector interface {
	CompareAndSetAssignment(context.Context, AssignmentRequest) (Mutation, error)
}

type DiscussionAppender interface {
	CompareAndAddComment(context.Context, CommentRequest) (Mutation, error)
}

type GuardedCompensator interface {
	CompareBeforeCompensate(context.Context, CompensationRequest) (Mutation, error)
}

// Provider retains the exact Phase 1 method set as a compatibility composition.
// Capability discovery remains separate so legacy collaborators do not acquire
// unrelated methods.
type Provider interface {
	WorkReader
	AssignmentProjector
	DiscussionAppender
	GuardedCompensator
}

type CapabilityReporter interface {
	Capabilities(context.Context) (ProviderCapabilities, error)
}

type ErrorKind string

const (
	ErrorUnavailable                 ErrorKind = "unavailable"
	ErrorAmbiguous                   ErrorKind = "ambiguous"
	ErrorRevisionConflict            ErrorKind = "revision_conflict"
	ErrorAttributionLost             ErrorKind = "attribution_lost"
	ErrorNotFound                    ErrorKind = "not_found"
	ErrorInvalidRequest              ErrorKind = "invalid_request"
	ErrorTimeout                     ErrorKind = "timeout"
	ErrorVersionSkew                 ErrorKind = "version_skew"
	ErrorSchemaSkew                  ErrorKind = "schema_skew"
	ErrorMalformedProtocol           ErrorKind = "malformed_protocol"
	ErrorWorkspaceMismatch           ErrorKind = "workspace_mismatch"
	ErrorLockContended               ErrorKind = "lock_contended"
	ErrorSafeCompensationUnsupported ErrorKind = "safe_compensation_unsupported"
	ErrorGuardedUpdateUnsupported    ErrorKind = "guarded_update_unsupported"
	ErrorCapabilityUnsupported       ErrorKind = "capability_unsupported"
)

type Error struct {
	Kind         ErrorKind
	Message      string
	RecoveryCode string
	Capability   CapabilityName
	Cause        error
}

func (e *Error) Error() string {
	message := e.Message
	if message == "" {
		message = "operation failed"
	}
	return fmt.Sprintf("tracker %s: %s", e.Kind, message)
}

func (e *Error) Unwrap() error { return e.Cause }

// RecoveryAction returns a stable machine-readable action class even for
// legacy Error literals that predate the RecoveryCode field.
func (e *Error) RecoveryAction() string {
	if e.RecoveryCode != "" {
		return e.RecoveryCode
	}
	switch e.Kind {
	case ErrorUnavailable, ErrorLockContended:
		return "bounded_retry"
	case ErrorTimeout, ErrorAmbiguous:
		return "observe_then_reconcile"
	case ErrorRevisionConflict, ErrorAttributionLost, ErrorSafeCompensationUnsupported, ErrorGuardedUpdateUnsupported:
		return "reconcile_without_mutation"
	case ErrorVersionSkew, ErrorSchemaSkew, ErrorMalformedProtocol, ErrorWorkspaceMismatch:
		return "repair_provider_binding"
	case ErrorNotFound:
		return "reconcile_mapping"
	case ErrorInvalidRequest:
		return "repair_request"
	case ErrorCapabilityUnsupported:
		return "use_supported_capability"
	default:
		return "operator_decision"
	}
}

func IsKind(err error, kind ErrorKind) bool {
	var providerError *Error
	return errors.As(err, &providerError) && providerError.Kind == kind
}
