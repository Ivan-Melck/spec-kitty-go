// Package lifecycle writes the mission-level lifecycle envelopes that share
// status.events.jsonl with lane transitions (MissionCreated, SpecifyStarted,
// PlanStarted, TasksCompleted, ...), mirroring upstream
// specify_cli.status.lifecycle_events.
package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Event types.
const (
	MissionCreated   = "MissionCreated"
	SpecifyStarted   = "SpecifyStarted"
	SpecifyCompleted = "SpecifyCompleted"
	PlanStarted      = "PlanStarted"
	PlanCompleted    = "PlanCompleted"
	TasksStarted     = "TasksStarted"
	TasksCompleted   = "TasksCompleted"
)

// SchemaVersion is the envelope schema version.
const SchemaVersion = "5.0.0"

// Envelope builds the local lifecycle envelope.
func Envelope(eventType string, payload *jsonx.Object, aggregateID, aggregateType string, projectUUID, projectSlug any) *jsonx.Object {
	return jsonx.NewObject(
		"event_id", ulid.New(),
		"event_type", eventType,
		"aggregate_id", aggregateID,
		"aggregate_type", aggregateType,
		"schema_version", SchemaVersion,
		"timestamp", clock.NowUTCISO(),
		"payload", payload,
		"project_uuid", projectUUID,
		"project_slug", projectSlug,
	)
}

// ReadEvents returns the lifecycle rows (objects carrying a string
// event_type) of the log at path; unreadable lines are skipped.
func ReadEvents(path string) []*jsonx.Object {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []*jsonx.Object
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		o, err := jsonx.DecodeObject([]byte(line))
		if err != nil || o == nil {
			continue
		}
		if _, ok := o.Get("event_type"); ok && o.GetString("event_type") != "" {
			out = append(out, o)
		}
	}
	return out
}

// HasEvent reports an existing event of eventType whose payload matches every
// dedup key.
func HasEvent(path, eventType string, dedup *jsonx.Object) bool {
	for _, e := range ReadEvents(path) {
		if e.GetString("event_type") != eventType {
			continue
		}
		p := e.GetObject("payload")
		if p == nil {
			continue
		}
		match := true
		for _, kv := range dedup.Pairs() {
			v, _ := p.Get(kv.Key)
			if jsonx.Dumps(v, jsonx.Line) != jsonx.Dumps(kv.Value, jsonx.Line) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Persist appends an envelope to <dir>/status.events.jsonl unless an event
// with the same (type, dedup keys) exists. Returns nil when skipped.
func Persist(dir, eventType string, payload *jsonx.Object, aggregateID, aggregateType string, projectUUID, projectSlug any, dedup *jsonx.Object) (*jsonx.Object, error) {
	path := status.EventsPath(dir)
	if dedup != nil && dedup.Len() > 0 && HasEvent(path, eventType, dedup) {
		return nil, nil
	}
	env := Envelope(eventType, payload, aggregateID, aggregateType, projectUUID, projectSlug)
	if err := status.AppendRows(path, env); err != nil {
		return nil, err
	}
	return env, nil
}

// LocalActor is git config user.email (≤ 1024 bytes), else "cli".
func LocalActor(dir string) string {
	cmd := exec.Command("git", "config", "user.email")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		if e := strings.TrimSpace(string(out)); e != "" && len(e) <= 1024 {
			return e
		}
	}
	return "cli"
}

// PydanticUTC renders t like pydantic's JSON datetime ("...Z", fraction
// omitted when zero).
func PydanticUTC(t time.Time) string {
	t = t.UTC()
	base := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		base += fmt.Sprintf(".%06d", us)
	}
	return base + "Z"
}

// MissionCreatedInput carries the MissionCreated payload facts.
type MissionCreatedInput struct {
	MissionSlug, MissionID, MissionType, TargetBranch string
	FriendlyName, PurposeTLDR, PurposeContext         string
	CreatedAt, Actor                                  string
	WPCount                                           int
	ProjectUUID, ProjectSlug                          string
}

// DefaultPurposeContext is default_mission_purpose_context.
func DefaultPurposeContext(display, branch string) string {
	return fmt.Sprintf("This mission advances %s on %s so stakeholders can track the work from mission creation onward.", display, branch)
}

// MissionCreatedPayload builds the canonical payload (None-valued optionals
// dropped except mission_number).
func MissionCreatedPayload(in MissionCreatedInput) *jsonx.Object {
	p := &jsonx.Object{}
	if in.Actor != "" {
		p.Set("actor", in.Actor)
	}
	created := in.CreatedAt
	if created == "" {
		created = clock.NowUTCISO()
	}
	p.Set("created_at", created)
	p.Set("friendly_name", in.FriendlyName)
	if in.MissionID != "" {
		p.Set("mission_id", in.MissionID)
	}
	p.Set("mission_number", nil)
	p.Set("mission_slug", in.MissionSlug)
	p.Set("mission_type", in.MissionType)
	p.Set("purpose_context", in.PurposeContext)
	p.Set("purpose_tldr", in.PurposeTLDR)
	p.Set("target_branch", in.TargetBranch)
	p.Set("wp_count", in.WPCount)
	return p
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EmitMissionCreated records MissionCreated in dir (dedup on mission_slug).
func EmitMissionCreated(dir string, in MissionCreatedInput) (*jsonx.Object, error) {
	agg := in.MissionID
	if agg == "" {
		agg = in.MissionSlug
	}
	return Persist(dir, MissionCreated, MissionCreatedPayload(in), agg, "Mission",
		nilIfEmpty(in.ProjectUUID), nilIfEmpty(in.ProjectSlug), jsonx.NewObject("mission_slug", in.MissionSlug))
}

// PhaseInput carries an artifact-phase event's facts.
type PhaseInput struct {
	EventType, MissionSlug, Actor, ArtifactPath, Summary string
	WPCount                                              *int
	At                                                   string
}

// EmitArtifactPhase records a Specify/Plan/Tasks Started/Completed event.
func EmitArtifactPhase(dir string, in PhaseInput) (*jsonx.Object, error) {
	at := in.At
	if at == "" {
		at = PydanticUTC(clock.Now())
	}
	actor := in.Actor
	if actor == "" {
		actor = "cli"
	}
	p := jsonx.NewObject("mission_slug", in.MissionSlug, "mission_number", nil, "actor", actor, "at", at)
	completed := strings.HasSuffix(in.EventType, "Completed")
	if completed {
		if in.ArtifactPath != "" {
			p.Set("artifact_path", in.ArtifactPath)
		}
		if in.EventType == TasksCompleted && in.WPCount != nil {
			p.Set("wp_count", *in.WPCount)
		}
		p.Set("summary", nilIfEmpty(in.Summary))
	} else if in.ArtifactPath != "" {
		p.Set("artifact_path", in.ArtifactPath)
	}
	dedup := jsonx.NewObject("mission_slug", in.MissionSlug)
	if in.ArtifactPath != "" {
		dedup.Set("artifact_path", in.ArtifactPath)
	}
	return Persist(dir, in.EventType, p, in.MissionSlug, "Mission", nil, nil, dedup)
}
