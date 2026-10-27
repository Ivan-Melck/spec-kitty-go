package decision

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Error codes (DecisionErrorCode).
const (
	CodeMissingStepOrSlot = "DECISION_MISSING_STEP_OR_SLOT"
	CodeAlreadyClosed     = "DECISION_ALREADY_CLOSED"
	CodeEventRepairFailed = "DECISION_EVENT_REPAIR_FAILED"
	CodeTerminalConflict  = "DECISION_TERMINAL_CONFLICT"
	CodeNotFound          = "DECISION_NOT_FOUND"
	CodeMissionNotFound   = "MISSION_NOT_FOUND"
)

// Error is DecisionError.
type Error struct {
	Code    string
	Details *jsonx.Object
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "Decision error: " + e.Code
}

// NewError builds an Error with ordered details pairs.
func NewError(code, msg string, details ...any) *Error {
	return &Error{Code: code, Message: msg, Details: jsonx.NewObject(details...)}
}

// Context is a resolved mission for decision commands.
type Context struct {
	Root string // main repository root
	Slug string // canonical mission dir name (or the raw handle)
	// EntrySlug, when set, is the mission_slug recorded on new entries and
	// their events (the interview flows pass the un-suffixed slug through).
	EntrySlug string
}

func (c *Context) entrySlug() string {
	if c.EntrySlug != "" {
		return c.EntrySlug
	}
	return c.Slug
}

// LedgerDir is the PRIMARY mission dir holding decisions/.
func (c *Context) LedgerDir() string { return project.Specs(c.Root, c.Slug) }

func now() time.Time { return clock.Now().UTC() }

func (c *Context) missionID() (string, error) {
	dir := project.Specs(c.Root, c.Slug)
	meta, err := mission.ReadMeta(dir)
	if err == mission.ErrNoMeta || (err == nil && meta == nil) {
		return "", NewError(CodeMissionNotFound, fmt.Sprintf("meta.json not found for mission %s", mission.PyRepr(c.Slug)), "mission_slug", c.Slug)
	}
	if err != nil {
		return "", NewError(CodeMissionNotFound, fmt.Sprintf("Failed to read meta.json for mission %s: %v", mission.PyRepr(c.Slug), err), "mission_slug", c.Slug)
	}
	id := meta.GetString("mission_id")
	if id == "" {
		return "", NewError(CodeMissionNotFound, fmt.Sprintf("meta.json for %s has no mission_id field", mission.PyRepr(c.Slug)), "mission_slug", c.Slug)
	}
	return id, nil
}

// writeEventsPath is the write_dir(STATUS_STATE)/status.events.jsonl path
// (materializes the coordination worktree for coord topologies).
func (c *Context) writeEventsPath() (string, error) {
	s := coord.Resolve(c.Root, c.Slug)
	dir, err := s.WriteDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "status.events.jsonl"), nil
}

func (c *Context) readEventsPath() string {
	return filepath.Join(coord.Resolve(c.Root, c.Slug).StatusDir(), "status.events.jsonl")
}

func touchLock(missionDir string) {
	os.MkdirAll(Dir(missionDir), 0o755)
	if f, err := os.OpenFile(LockPath(missionDir), os.O_CREATE|os.O_RDWR, 0o644); err == nil {
		f.Close()
	}
}

// OpenRequest carries open_decision arguments.
type OpenRequest struct {
	Flow, InputKey, Question string
	Options                  []string
	StepID, SlotKey          *string
	Actor                    string
	DryRun                   bool
}

// OpenResponse is DecisionOpenResponse.
type OpenResponse struct {
	DecisionID   string
	Idempotent   bool
	MissionID    string
	ArtifactPath string
	Lamport      *int
}

// Open is service.open_decision.
func (c *Context) Open(r OpenRequest) (*OpenResponse, error) {
	if r.StepID == nil && r.SlotKey == nil {
		return nil, NewError(CodeMissingStepOrSlot, "Either step_id or slot_key must be provided")
	}
	mid, err := c.missionID()
	if err != nil {
		return nil, err
	}
	dir := c.LedgerDir()
	if r.DryRun {
		return &OpenResponse{DecisionID: "DRY_RUN", MissionID: mid}, nil
	}
	if _, err := c.writeEventsPath(); err != nil {
		return nil, err
	}
	touchLock(dir)
	idx, err := Load(dir)
	if err != nil {
		return nil, err
	}
	if existing := FindByLogicalKey(idx, r.Flow, r.StepID, r.SlotKey, r.InputKey); existing != nil {
		if existing.Status == StatusOpen {
			lam, err := c.repairOpened(existing)
			if err != nil {
				return nil, err
			}
			return &OpenResponse{DecisionID: existing.DecisionID, Idempotent: true, MissionID: mid, ArtifactPath: ArtifactPath(dir, existing.DecisionID), Lamport: lam}, nil
		}
		return nil, NewError(CodeAlreadyClosed, fmt.Sprintf("Decision %s is already in terminal state %s", mission.PyRepr(existing.DecisionID), mission.PyRepr(existing.Status)),
			"decision_id", existing.DecisionID, "status", existing.Status)
	}
	actor := r.Actor
	e := &Entry{DecisionID: ulid.New(), OriginFlow: r.Flow, StepID: r.StepID, SlotKey: r.SlotKey, InputKey: r.InputKey, Question: r.Question,
		Options: r.Options, Status: StatusOpen, CreatedAt: now(), OpenedBy: &actor, MissionID: mid, MissionSlug: c.entrySlug()}
	idx.Entries = append(idx.Entries, e)
	if idx.MissionID == "" || e.MissionID != "" {
		idx.MissionID = e.MissionID
	}
	if err := Save(dir, idx); err != nil {
		return nil, err
	}
	art, err := WriteArtifact(dir, e)
	if err != nil {
		return nil, err
	}
	lam, err := c.emitOpened(e, actor)
	if err != nil {
		return nil, err
	}
	return &OpenResponse{DecisionID: e.DecisionID, MissionID: mid, ArtifactPath: art, Lamport: &lam}, nil
}

func (c *Context) readEvents(path string) ([]*jsonx.Object, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var out []*jsonx.Object
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		o, err := jsonx.DecodeObject([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("malformed event log line %d: %v", n, err)
		}
		out = append(out, o)
	}
	return out, nil
}

func (c *Context) repairOpened(e *Entry) (*int, error) {
	events, err := c.readEvents(c.readEventsPath())
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if p := ev.GetObject("payload"); ev.GetString("event_type") == "DecisionPointOpened" && p != nil && p.GetString("decision_point_id") == e.DecisionID {
			return nil, nil
		}
	}
	if e.OpenedBy == nil {
		return nil, NewError(CodeEventRepairFailed, fmt.Sprintf("Cannot repair opened event for decision %s: opening actor was not persisted", mission.PyRepr(e.DecisionID)),
			"decision_id", e.DecisionID, "mission_slug", c.Slug)
	}
	lam, err := c.emitOpened(e, *e.OpenedBy)
	if err != nil {
		return nil, NewError(CodeEventRepairFailed, fmt.Sprintf("Failed to repair opened event for decision %s: %v", mission.PyRepr(e.DecisionID), err),
			"decision_id", e.DecisionID, "mission_slug", c.Slug)
	}
	return &lam, nil
}

func (c *Context) appendEvent(eventType string, payload *jsonx.Object, at time.Time) (int, error) {
	path, err := c.writeEventsPath()
	if err != nil {
		return 0, err
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	row := jsonx.NewObject("event_id", ulid.New(), "at", ISO(at), "event_type", eventType, "payload", payload)
	// _append_raw_event: append_raw_rows_atomic under the Mission status lock,
	// with the Lamport-proxy line count read under the same acquisition.
	lock, err := status.LockMission(filepath.Dir(path), "", filelock.Forever)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	if err := status.AppendRows(path, row); err != nil {
		return 0, err
	}
	b, _ := os.ReadFile(path)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n, nil
}

func (c *Context) emitOpened(e *Entry, actor string) (int, error) {
	t := now()
	step := ""
	if e.StepID != nil {
		step = *e.StepID
	} else if e.SlotKey != nil {
		step = *e.SlotKey
	}
	opts := []any{}
	for _, o := range e.Options {
		opts = append(opts, o)
	}
	p := jsonx.NewObject("origin_surface", "planning_interview", "decision_point_id", e.DecisionID, "mission_id", e.MissionID,
		"run_id", e.DecisionID, "mission_slug", e.MissionSlug, "mission_type", "software-dev", "phase", strings.ToUpper(e.OriginFlow),
		"origin_flow", e.OriginFlow, "question", e.Question, "options", opts, "input_key", e.InputKey, "step_id", step,
		"actor_id", actor, "actor_type", "human", "state_entered_at", PydanticTime(e.CreatedAt), "recorded_at", PydanticTime(t))
	return c.appendEvent("DecisionPointOpened", p, t)
}

func (c *Context) emitResolved(e *Entry, actor string) (int, error) {
	t := now()
	resolvedBy := actor
	if e.ResolvedBy != nil && *e.ResolvedBy != "" {
		resolvedBy = *e.ResolvedBy
	}
	entered := t
	if e.ResolvedAt != nil {
		entered = *e.ResolvedAt
	}
	var final, rationale any
	if e.Status == StatusResolved {
		fa := ""
		if e.FinalAnswer != nil {
			fa = *e.FinalAnswer
		}
		final = fa
		rationale = strPtr(e.Rationale)
	} else {
		r := "no rationale"
		if e.Rationale != nil && *e.Rationale != "" {
			r = *e.Rationale
		}
		rationale = r
	}
	p := jsonx.NewObject("origin_surface", "planning_interview", "decision_point_id", e.DecisionID, "mission_id", e.MissionID,
		"run_id", e.DecisionID, "mission_slug", e.MissionSlug, "mission_type", "software-dev", "terminal_outcome", e.Status,
		"final_answer", final, "other_answer", e.OtherAnswer && e.Status == StatusResolved, "rationale", rationale, "summary", nil,
		"actual_participants", []any{}, "resolved_by", resolvedBy, "closed_locally_while_widened", false, "closure_message", nil,
		"state_entered_at", PydanticTime(entered), "recorded_at", PydanticTime(t))
	return c.appendEvent("DecisionPointResolved", p, t)
}

// TerminalRequest carries resolve/defer/cancel arguments.
type TerminalRequest struct {
	DecisionID  string
	Status      string
	FinalAnswer *string
	OtherAnswer bool
	Rationale   *string
	ResolvedBy  *string
	Actor       string
	DryRun      bool
}

// TerminalResponse is DecisionTerminalResponse.
type TerminalResponse struct {
	DecisionID, Status, Outcome string
	Idempotent                  bool
	Lamport                     *int
}

func eqPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Terminal is service._terminal_command.
func (c *Context) Terminal(r TerminalRequest) (*TerminalResponse, error) {
	if r.DryRun {
		return &TerminalResponse{DecisionID: r.DecisionID, Status: r.Status, Outcome: r.Status}, nil
	}
	eventsPath, err := c.writeEventsPath()
	if err != nil {
		return nil, err
	}
	dir := c.LedgerDir()
	touchLock(dir)
	idx, err := Load(dir)
	if err != nil {
		return nil, err
	}
	var e *Entry
	for _, x := range idx.Entries {
		if x.DecisionID == r.DecisionID {
			e = x
			break
		}
	}
	if e == nil {
		return nil, NewError(CodeNotFound, fmt.Sprintf("Decision %s not found in index", mission.PyRepr(r.DecisionID)), "decision_id", r.DecisionID)
	}
	terminal := e.Status != StatusOpen
	if terminal && !(e.Status == StatusDeferred && r.Status == StatusResolved) {
		if e.Status == r.Status && eqPtr(e.FinalAnswer, r.FinalAnswer) && e.OtherAnswer == r.OtherAnswer && eqPtr(e.Rationale, r.Rationale) {
			lam, err := c.repairTerminal(e, eventsPath, r.Actor)
			if err != nil {
				return nil, err
			}
			return &TerminalResponse{DecisionID: r.DecisionID, Status: r.Status, Outcome: r.Status, Idempotent: true, Lamport: lam}, nil
		}
		return nil, NewError(CodeTerminalConflict, fmt.Sprintf("Decision %s is already in terminal state %s; cannot transition to %s", mission.PyRepr(r.DecisionID), mission.PyRepr(e.Status), mission.PyRepr(r.Status)),
			"decision_id", r.DecisionID, "existing_status", e.Status, "requested_status", r.Status)
	}
	t := now()
	e.Status, e.FinalAnswer, e.OtherAnswer, e.Rationale, e.ResolvedAt, e.ResolvedBy, e.SummaryJSON = r.Status, r.FinalAnswer, r.OtherAnswer, r.Rationale, &t, r.ResolvedBy, nil
	if err := Save(dir, idx); err != nil {
		return nil, err
	}
	if _, err := WriteArtifact(dir, e); err != nil {
		return nil, err
	}
	lam, err := c.emitResolved(e, r.Actor)
	if err != nil {
		return nil, err
	}
	return &TerminalResponse{DecisionID: r.DecisionID, Status: r.Status, Outcome: r.Status, Lamport: &lam}, nil
}

func (c *Context) repairTerminal(e *Entry, eventsPath, actor string) (*int, error) {
	events, err := c.readEvents(eventsPath)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		p := ev.GetObject("payload")
		if ev.GetString("event_type") == "DecisionPointResolved" && p != nil && p.GetString("decision_point_id") == e.DecisionID && p.GetString("terminal_outcome") == e.Status {
			return nil, nil
		}
	}
	if _, err := WriteArtifact(c.LedgerDir(), e); err != nil {
		return nil, NewError(CodeEventRepairFailed, fmt.Sprintf("Failed to repair terminal event for decision %s: %v", mission.PyRepr(e.DecisionID), err), "decision_id", e.DecisionID, "mission_slug", c.Slug)
	}
	lam, err := c.emitResolved(e, actor)
	if err != nil {
		return nil, NewError(CodeEventRepairFailed, fmt.Sprintf("Failed to repair terminal event for decision %s: %v", mission.PyRepr(e.DecisionID), err), "decision_id", e.DecisionID, "mission_slug", c.Slug)
	}
	return &lam, nil
}

// --- verify -----------------------------------------------------------------

var sentinelRe = regexp.MustCompile(`\[NEEDS CLARIFICATION: [^\]]*\]\s*<!--\s*decision_id:\s*(?P<did>[0-9A-HJKMNP-TV-Z]{26})\s*-->`)

// Finding is one VerifyFinding.
type Finding struct {
	Kind, Ref string
	Location  *string
	Detail    string
}

// VerifyResult is VerifyResponse.
type VerifyResult struct {
	DeferredCount, MarkerCount int
	Findings                   []Finding
}

type marker struct{ id, loc string }

func scanMarkers(path string) []marker {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []marker
	text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		for _, m := range sentinelRe.FindAllStringSubmatch(line, -1) {
			out = append(out, marker{m[1], fmt.Sprintf("%s:L%d", filepath.Base(path), i+1)})
		}
	}
	return out
}

// Verify is decisions.verify.verify.
func Verify(missionDir string) (*VerifyResult, error) {
	idx, err := Load(missionDir)
	if err != nil {
		return nil, err
	}
	known := map[string]*Entry{}
	var deferred []string
	for _, e := range idx.Entries {
		known[e.DecisionID] = e
		if e.Status == StatusDeferred {
			deferred = append(deferred, e.DecisionID)
		}
	}
	var all []marker
	for _, d := range []string{"spec.md", "plan.md"} {
		all = append(all, scanMarkers(filepath.Join(missionDir, d))...)
	}
	first := map[string]string{}
	for _, m := range all {
		if _, ok := first[m.id]; !ok {
			first[m.id] = m.loc
		}
	}
	res := &VerifyResult{DeferredCount: len(deferred), MarkerCount: len(all)}
	sort.Strings(deferred)
	seen := map[string]bool{}
	for _, d := range deferred {
		if seen[d] {
			continue
		}
		seen[d] = true
		if _, ok := first[d]; !ok {
			res.Findings = append(res.Findings, Finding{Kind: "DEFERRED_WITHOUT_MARKER", Ref: d, Detail: "No inline marker found in spec.md or plan.md"})
		}
	}
	var ids []string
	for id := range first {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			loc := first[id]
			res.Findings = append(res.Findings, Finding{Kind: "MARKER_WITHOUT_DECISION", Ref: id, Location: &loc, Detail: "Marker references unknown decision_id"})
		}
	}
	for _, id := range ids {
		if e, ok := known[id]; ok && e.Status != StatusDeferred && e.Status != StatusResolved {
			loc := first[id]
			res.Findings = append(res.Findings, Finding{Kind: "STALE_MARKER", Ref: id, Location: &loc, Detail: fmt.Sprintf("Decision is in status '%s', not 'deferred' or 'resolved'", e.Status)})
		}
	}
	return res, nil
}
