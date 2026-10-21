// Package decision ports the Decision Moment ledger (specify_cli.decisions):
// decisions/index.json + DM-<id>.md artifacts, the open/resolve/defer/cancel
// service with its DecisionPointOpened/Resolved status-log events, the
// marker verifier and the runtime lock file.
package decision

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Statuses.
const (
	StatusOpen     = "open"
	StatusResolved = "resolved"
	StatusDeferred = "deferred"
	StatusCanceled = "canceled"
)

// Flows (OriginFlow).
var Flows = []string{"charter", "specify", "plan"}

// AllStatuses in enum order.
var AllStatuses = []string{StatusOpen, StatusResolved, StatusDeferred, StatusCanceled}

// Entry is one IndexEntry.
type Entry struct {
	DecisionID  string
	OriginFlow  string
	StepID      *string
	SlotKey     *string
	InputKey    string
	Question    string
	Options     []string
	Status      string
	FinalAnswer *string
	Rationale   *string
	OtherAnswer bool
	SummaryJSON *jsonx.Object
	CreatedAt   time.Time
	ResolvedAt  *time.Time
	ResolvedBy  *string
	OpenedBy    *string
	MissionID   string
	MissionSlug string
}

// Index is DecisionIndex.
type Index struct {
	Version   int
	MissionID string
	Entries   []*Entry
}

// ReadError is DecisionIndexReadError.
type ReadError struct {
	Path  string
	Cause string
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("Cannot read %s: %s — fail-closed (index.json exists but is corrupt or unreadable); run: spec-kitty doctor", e.Path, e.Cause)
}

// Dir is <mission>/decisions.
func Dir(missionDir string) string { return filepath.Join(missionDir, "decisions") }

// IndexPath is decisions/index.json.
func IndexPath(missionDir string) string { return filepath.Join(Dir(missionDir), "index.json") }

// ArtifactPath is decisions/DM-<id>.md.
func ArtifactPath(missionDir, id string) string {
	return filepath.Join(Dir(missionDir), "DM-"+id+".md")
}

// LockPath is decisions/index.json.lock.
func LockPath(missionDir string) string { return filepath.Join(Dir(missionDir), "index.json.lock") }

func optStr(o *jsonx.Object, k string) (*string, error) {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: Input should be a valid string", k)
	}
	return &s, nil
}

// pydanticTime parses a pydantic/ISO datetime string.
func pydanticTime(s string) (time.Time, error) {
	return clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
}

// Load is store.load_index (empty index when missing).
func Load(missionDir string) (*Index, error) {
	p := IndexPath(missionDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{Version: 1}, nil
		}
		return nil, &ReadError{Path: p, Cause: err.Error()}
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, &ReadError{Path: p, Cause: err.Error()}
	}
	idx := &Index{Version: 1, MissionID: o.GetString("mission_id")}
	for _, raw := range o.GetList("entries") {
		eo := jsonx.ToObject(raw)
		if eo == nil {
			return nil, &ReadError{Path: p, Cause: "entries: invalid entry"}
		}
		e, err := entryFromObject(eo)
		if err != nil {
			return nil, &ReadError{Path: p, Cause: err.Error()}
		}
		idx.Entries = append(idx.Entries, e)
	}
	return idx, nil
}

func entryFromObject(o *jsonx.Object) (*Entry, error) {
	e := &Entry{DecisionID: o.GetString("decision_id"), OriginFlow: o.GetString("origin_flow"), InputKey: o.GetString("input_key"),
		Question: o.GetString("question"), Status: o.GetString("status"), OtherAnswer: o.GetBool("other_answer"),
		MissionID: o.GetString("mission_id"), MissionSlug: o.GetString("mission_slug")}
	var err error
	for k, dst := range map[string]**string{"step_id": &e.StepID, "slot_key": &e.SlotKey, "final_answer": &e.FinalAnswer,
		"rationale": &e.Rationale, "resolved_by": &e.ResolvedBy, "opened_by": &e.OpenedBy} {
		if *dst, err = optStr(o, k); err != nil {
			return nil, err
		}
	}
	for _, v := range o.GetList("options") {
		if s, ok := v.(string); ok {
			e.Options = append(e.Options, s)
		}
	}
	e.SummaryJSON = o.GetObject("summary_json")
	if e.CreatedAt, err = pydanticTime(o.GetString("created_at")); err != nil {
		return nil, fmt.Errorf("created_at: %v", err)
	}
	if s := o.GetString("resolved_at"); s != "" {
		t, err := pydanticTime(s)
		if err != nil {
			return nil, fmt.Errorf("resolved_at: %v", err)
		}
		e.ResolvedAt = &t
	}
	return e, nil
}

// PydanticTime renders a UTC datetime like pydantic's JSON mode ("Z").
func PydanticTime(t time.Time) string {
	s := clock.ISO(t.UTC())
	return strings.TrimSuffix(s, "+00:00") + "Z"
}

// ISO renders datetime.isoformat().
func ISO(t time.Time) string { return clock.ISO(t.UTC()) }

func strPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Object renders the entry for index.json (model_dump(mode="json")).
func (e *Entry) Object() *jsonx.Object {
	opts := []any{}
	for _, s := range e.Options {
		opts = append(opts, s)
	}
	var resolved any
	if e.ResolvedAt != nil {
		resolved = PydanticTime(*e.ResolvedAt)
	}
	var summary any
	if e.SummaryJSON != nil {
		summary = e.SummaryJSON
	}
	return jsonx.NewObject("decision_id", e.DecisionID, "origin_flow", e.OriginFlow, "step_id", strPtr(e.StepID), "slot_key", strPtr(e.SlotKey),
		"input_key", e.InputKey, "question", e.Question, "options", opts, "status", e.Status, "final_answer", strPtr(e.FinalAnswer),
		"rationale", strPtr(e.Rationale), "other_answer", e.OtherAnswer, "summary_json", summary, "created_at", PydanticTime(e.CreatedAt),
		"resolved_at", resolved, "resolved_by", strPtr(e.ResolvedBy), "opened_by", strPtr(e.OpenedBy), "mission_id", e.MissionID,
		"mission_slug", e.MissionSlug)
}

func sortEntries(entries []*Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := ISO(entries[i].CreatedAt), ISO(entries[j].CreatedAt)
		if a != b {
			return a < b
		}
		return entries[i].DecisionID < entries[j].DecisionID
	})
}

// Save is store.save_index.
func Save(missionDir string, idx *Index) error {
	if err := os.MkdirAll(Dir(missionDir), 0o755); err != nil {
		return err
	}
	entries := append([]*Entry(nil), idx.Entries...)
	sortEntries(entries)
	list := []any{}
	for _, e := range entries {
		list = append(list, e.Object())
	}
	o := jsonx.NewObject("version", 1, "mission_id", idx.MissionID, "entries", list)
	return atomicWrite(IndexPath(missionDir), []byte(jsonx.Dumps(o, jsonx.SortedPretty)+"\n"))
}

func atomicWrite(dest string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dest), "tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	f.Close()
	os.Chmod(name, 0o600)
	return os.Rename(name, dest)
}

// RenderArtifact is store._render_artifact.
func RenderArtifact(e *Entry) string {
	var l []string
	l = append(l, fmt.Sprintf("# Decision Moment `%s`", e.DecisionID), "",
		fmt.Sprintf("- **Mission:** `%s`", e.MissionSlug), fmt.Sprintf("- **Origin flow:** `%s`", e.OriginFlow))
	if e.StepID != nil {
		l = append(l, fmt.Sprintf("- **Step id:** `%s`", *e.StepID))
	}
	if e.SlotKey != nil {
		l = append(l, fmt.Sprintf("- **Slot key:** `%s`", *e.SlotKey))
	}
	l = append(l, fmt.Sprintf("- **Input key:** `%s`", e.InputKey), fmt.Sprintf("- **Status:** `%s`", e.Status),
		fmt.Sprintf("- **Created:** `%s`", ISO(e.CreatedAt)))
	if e.ResolvedAt != nil {
		l = append(l, fmt.Sprintf("- **Resolved:** `%s`", ISO(*e.ResolvedAt)))
	}
	if e.ResolvedBy != nil {
		l = append(l, fmt.Sprintf("- **Resolved by:** `%s`", *e.ResolvedBy))
	}
	if e.OpenedBy != nil {
		l = append(l, fmt.Sprintf("- **Opened by:** `%s`", *e.OpenedBy))
	}
	other := "false"
	if e.OtherAnswer {
		other = "true"
	}
	l = append(l, fmt.Sprintf("- **Other answer:** `%s`", other), "", "## Question", "", e.Question, "", "## Options", "")
	if len(e.Options) > 0 {
		for _, o := range e.Options {
			l = append(l, "- "+o)
		}
	} else {
		l = append(l, "_(none)_")
	}
	none := func(p *string) string {
		if p == nil {
			return "_(none)_"
		}
		return *p
	}
	l = append(l, "", "## Final answer", "", none(e.FinalAnswer), "", "## Rationale", "", none(e.Rationale), "", "## Change log", "",
		fmt.Sprintf("- `%s` — opened", ISO(e.CreatedAt)))
	if e.ResolvedAt != nil && (e.Status == StatusResolved || e.Status == StatusDeferred || e.Status == StatusCanceled) {
		if e.FinalAnswer != nil {
			l = append(l, fmt.Sprintf("- `%s` — %s (final_answer=\"%s\")", ISO(*e.ResolvedAt), e.Status, *e.FinalAnswer))
		} else {
			l = append(l, fmt.Sprintf("- `%s` — %s", ISO(*e.ResolvedAt), e.Status))
		}
	}
	return strings.Join(l, "\n") + "\n"
}

// WriteArtifact writes DM-<id>.md and returns its path.
func WriteArtifact(missionDir string, e *Entry) (string, error) {
	if err := os.MkdirAll(Dir(missionDir), 0o755); err != nil {
		return "", err
	}
	p := ArtifactPath(missionDir, e.DecisionID)
	return p, atomicWrite(p, []byte(RenderArtifact(e)))
}

// FindByLogicalKey is store.find_by_logical_key.
func FindByLogicalKey(idx *Index, flow string, stepID, slotKey *string, inputKey string) *Entry {
	var best *Entry
	for _, e := range idx.Entries {
		if e.OriginFlow != flow || e.InputKey != inputKey {
			continue
		}
		match := (stepID != nil && e.StepID != nil && *e.StepID == *stepID) || (slotKey != nil && e.SlotKey != nil && *e.SlotKey == *slotKey)
		if !match {
			continue
		}
		if best == nil || ISO(e.CreatedAt) > ISO(best.CreatedAt) || (ISO(e.CreatedAt) == ISO(best.CreatedAt) && e.DecisionID > best.DecisionID) {
			best = e
		}
	}
	return best
}

// keep mission import used for PyRepr helpers in service.go
var _ = mission.PyRepr
