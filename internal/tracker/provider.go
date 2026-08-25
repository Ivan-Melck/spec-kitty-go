package tracker

import (
	"context"
	"errors"
	"fmt"
)

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

type Provider interface {
	Read(context.Context, string) (Work, error)
	CompareAndSetAssignment(context.Context, AssignmentRequest) (Mutation, error)
	CompareAndAddComment(context.Context, CommentRequest) (Mutation, error)
	CompareBeforeCompensate(context.Context, CompensationRequest) (Mutation, error)
}

type ErrorKind string

const (
	ErrorUnavailable      ErrorKind = "unavailable"
	ErrorAmbiguous        ErrorKind = "ambiguous"
	ErrorRevisionConflict ErrorKind = "revision_conflict"
	ErrorAttributionLost  ErrorKind = "attribution_lost"
	ErrorNotFound         ErrorKind = "not_found"
	ErrorInvalidRequest   ErrorKind = "invalid_request"
)

type Error struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("tracker %s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("tracker %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsKind(err error, kind ErrorKind) bool {
	var providerError *Error
	return errors.As(err, &providerError) && providerError.Kind == kind
}
