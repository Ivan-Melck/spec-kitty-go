// Package invocation implements governed Ops (profile invocations): the
// deterministic request router, the routing catalog, the advisory
// model-routing recommendation, the kitty-ops/ JSONL audit trail and the
// executor behind `dispatch`, `profile-invocation complete` and
// `invocations list` (upstream specify_cli/invocation).
package invocation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Mode-of-work values (upstream ModeOfWork).
const (
	ModeAdvisory      = "advisory"
	ModeTaskExecution = "task_execution"
	ModeMissionStep   = "mission_step"
	ModeQuery         = "query"
)

var knownModes = map[string]bool{ModeAdvisory: true, ModeTaskExecution: true, ModeMissionStep: true, ModeQuery: true}

// Outcomes accepted by `profile-invocation complete`.
var Outcomes = []string{"done", "failed", "abandoned"}

var ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// ValidateID is validate_invocation_id.
func ValidateID(id string) error {
	if !ulidRe.MatchString(id) {
		return fmt.Errorf("invocation_id must be a 26-char ULID, got %s", pyRepr(id))
	}
	return nil
}

// lineOpts is json.dumps() with default separators (one line, ASCII).
var lineOpts = jsonx.Options{Indent: -1, EnsureASCII: true}

// Started is the v2 `started` event (OpStartedEvent).
type Started struct {
	InvocationID               string
	ProfileID                  string
	Action                     string
	RequestText                string
	Actor                      string
	ModeOfWork                 string
	GovernanceContextHash      string
	GovernanceContextAvailable bool
	RouterConfidence           string // "" = None
	StartedAt                  string
	MissionID                  string // "" = None
	WPID                       string // "" = None
	ModelID                    string // "" = None
}

// Object is model_dump(exclude_none=True) in field order.
func (s *Started) Object() *jsonx.Object {
	o := jsonx.NewObject("event", "started", "invocation_id", s.InvocationID, "profile_id", s.ProfileID,
		"action", s.Action, "request_text", s.RequestText, "actor", s.Actor, "mode_of_work", s.ModeOfWork,
		"governance_context_hash", s.GovernanceContextHash, "governance_context_available", s.GovernanceContextAvailable)
	if s.RouterConfidence != "" {
		o.Set("router_confidence", s.RouterConfidence)
	}
	o.Set("started_at", s.StartedAt)
	for _, kv := range [][2]string{{"mission_id", s.MissionID}, {"wp_id", s.WPID}, {"model_id", s.ModelID}} {
		if kv[1] != "" {
			o.Set(kv[0], kv[1])
		}
	}
	return o
}

// Line is to_jsonl_line.
func (s *Started) Line() string { return jsonx.Dumps(s.Object(), lineOpts) }

// Completed is the v2 `completed` event (OpCompletedEvent).
type Completed struct {
	InvocationID string
	CompletedAt  string
	Outcome      string
	ClosedBy     string // agent | doctor_sweep
	EvidenceRef  string // "" = None
	hasEvidence  bool
}

// Object is model_dump(exclude_none=True).
func (c *Completed) Object() *jsonx.Object {
	o := jsonx.NewObject("event", "completed", "invocation_id", c.InvocationID, "completed_at", c.CompletedAt,
		"outcome", c.Outcome, "closed_by", c.ClosedBy)
	if c.hasEvidence {
		o.Set("evidence_ref", c.EvidenceRef)
	}
	return o
}

// Line is to_jsonl_line.
func (c *Completed) Line() string { return jsonx.Dumps(c.Object(), lineOpts) }

// LegacyRecordError is a pre-v2 kitty-ops line.
type LegacyRecordError struct {
	InvocationID string
	Reason       string
}

func (e *LegacyRecordError) Error() string {
	id := ""
	if e.InvocationID != "" {
		id = " " + e.InvocationID
	}
	return "Legacy Op record" + id + ": " + e.Reason + ". Run 'spec-kitty upgrade' to migrate kitty-ops records to the v2 schema."
}

func str(o *jsonx.Object, k string) (string, bool) {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// ParseStarted validates a decoded line as a v2 started event.
func ParseStarted(o *jsonx.Object) (*Started, error) {
	id, _ := str(o, "invocation_id")
	bad := func() error { return &LegacyRecordError{id, "started event not v2-parseable"} }
	if ev, _ := str(o, "event"); ev != "started" {
		return nil, fmt.Errorf("not an Op lifecycle event")
	}
	s := &Started{InvocationID: id}
	if ValidateID(id) != nil {
		return nil, bad()
	}
	req := func(k string, dst *string, nonEmpty bool) bool {
		v, ok := str(o, k)
		if !ok || (nonEmpty && v == "") {
			return false
		}
		*dst = v
		return true
	}
	if !req("profile_id", &s.ProfileID, true) || !req("action", &s.Action, true) || !req("request_text", &s.RequestText, false) ||
		!req("actor", &s.Actor, true) || !req("mode_of_work", &s.ModeOfWork, true) || !req("governance_context_hash", &s.GovernanceContextHash, false) ||
		!req("started_at", &s.StartedAt, true) {
		return nil, bad()
	}
	if !knownModes[s.ModeOfWork] {
		return nil, bad()
	}
	gv, ok := o.Get("governance_context_available")
	b, isBool := gv.(bool)
	if !ok || !isBool {
		return nil, bad()
	}
	s.GovernanceContextAvailable = b
	s.RouterConfidence, _ = str(o, "router_confidence")
	s.MissionID, _ = str(o, "mission_id")
	s.WPID, _ = str(o, "wp_id")
	s.ModelID, _ = str(o, "model_id")
	return s, nil
}

// ParseCompleted validates a decoded line as a v2 completed event.
func ParseCompleted(o *jsonx.Object) (*Completed, error) {
	id, _ := str(o, "invocation_id")
	if ev, _ := str(o, "event"); ev != "completed" {
		return nil, fmt.Errorf("not an Op lifecycle event")
	}
	if !o.Has("closed_by") {
		return nil, &LegacyRecordError{id, "completed event lacks 'closed_by'"}
	}
	bad := &LegacyRecordError{id, "completed event not v2-parseable"}
	c := &Completed{InvocationID: id}
	var ok bool
	if ValidateID(id) != nil {
		return nil, bad
	}
	if c.CompletedAt, ok = str(o, "completed_at"); !ok || c.CompletedAt == "" {
		return nil, bad
	}
	if c.Outcome, ok = str(o, "outcome"); !ok || (c.Outcome != "done" && c.Outcome != "failed" && c.Outcome != "abandoned") {
		return nil, bad
	}
	if c.ClosedBy, ok = str(o, "closed_by"); !ok || (c.ClosedBy != "agent" && c.ClosedBy != "doctor_sweep") {
		return nil, bad
	}
	if v, ok := str(o, "evidence_ref"); ok {
		c.EvidenceRef, c.hasEvidence = v, true
	}
	return c, nil
}

func decodeLine(line string) (*jsonx.Object, error) {
	if !json.Valid([]byte(line)) {
		return nil, fmt.Errorf("invalid json")
	}
	return jsonx.DecodeObject([]byte(line))
}

// pyRepr renders a Python str repr.
func pyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	var b strings.Builder
	b.WriteString(q)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case string(r) == q:
			b.WriteString(`\` + q)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(q)
	return b.String()
}

// pyListRepr renders a Python list[str] repr.
func pyListRepr(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = pyRepr(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
