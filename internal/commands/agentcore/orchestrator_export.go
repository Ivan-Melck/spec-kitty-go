package agentcore

// Exported seams used by the orchestrator-api verbs (area I). Additive only.

import "github.com/spec-kitty/go-kitty3/internal/status"

// ParseReviewResultJSON is status.review_result_parse.parse_review_result_json.
func ParseReviewResultJSON(raw string) (*status.ReviewResult, error) { return parseReviewResultJSON(raw) }

// BuildDoneEvidence is status.emit._build_done_evidence over a decoded
// JSON value (the error text is the TransitionError message).
func BuildDoneEvidence(v any) (*status.DoneEvidence, error) { return buildDoneEvidence(v) }
