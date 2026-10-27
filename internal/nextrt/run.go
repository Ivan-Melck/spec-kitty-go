package nextrt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// TemplateStep is one mission-runtime.yaml step.
type TemplateStep struct {
	ID, Title, Description, PromptTemplate string
	AgentProfile, ContractRef              string
	DependsOn                              []string
	RequiresInputs                         []string
}

// Template is a mission-runtime planning template.
type Template struct {
	Key        string
	Steps      []TemplateStep
	AuditSteps []TemplateStep
}

// ParseTemplate parses mission-runtime.yaml.
func ParseTemplate(b []byte) (*Template, error) {
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	t := &Template{}
	if m := o.GetObject("mission"); m != nil {
		t.Key = m.GetString("key")
	}
	// stepsOf reads PromptStep's agent-profile alias, which the built-in
	// research and documentation templates use.
	t.Steps = stepsOf(o.GetList("steps"))
	return t, nil
}

func strList(o *jsonx.Object, k string) []string {
	v, _ := o.Get(k)
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// HasStep reports whether the template declares step id.
func (t *Template) HasStep(id string) bool {
	for _, s := range t.Steps {
		if s.ID == id {
			return true
		}
	}
	return false
}

// templateKeyOf is load_mission_template_file(path).mission.key ("" on
// any load failure); shorthand templates fall back to key/name/parent dir.
func templateKeyOf(b []byte, p string) string {
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return ""
	}
	if !o.Has("mission") {
		for _, k := range []string{"key", "name"} {
			if s := o.GetString(k); s != "" {
				return s
			}
		}
		return filepath.Base(filepath.Dir(p))
	}
	if m := o.GetObject("mission"); m != nil {
		return m.GetString("key")
	}
	return ""
}

// templateCandidates is _candidate_templates_for_root.
func templateCandidates(root, missionType string) []string {
	st, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		if b := filepath.Base(root); b == "mission-runtime.yaml" || b == "mission.yaml" {
			return []string{root}
		}
		return nil
	}
	return []string{
		filepath.Join(root, missionType, "mission-runtime.yaml"), filepath.Join(root, missionType, "mission.yaml"),
		filepath.Join(root, "missions", missionType, "mission-runtime.yaml"), filepath.Join(root, "missions", missionType, "mission.yaml"),
		filepath.Join(root, "mission-runtime.yaml"), filepath.Join(root, "mission.yaml"),
	}
}

// resolveTemplateInRoot is _resolve_runtime_template_in_root.
func resolveTemplateInRoot(root, missionType string) ([]byte, string) {
	for _, c := range templateCandidates(root, missionType) {
		if st, err := os.Stat(c); err != nil || !st.Mode().IsRegular() {
			continue
		}
		try := []string{c}
		if filepath.Base(c) == "mission.yaml" {
			side := filepath.Join(filepath.Dir(c), "mission-runtime.yaml")
			if st, err := os.Stat(side); err == nil && st.Mode().IsRegular() {
				try = []string{side, c}
			}
		}
		for _, p := range try {
			b, err := os.ReadFile(p)
			if err == nil && templateKeyOf(b, p) == missionType {
				if r, err := filepath.EvalSymlinks(p); err == nil {
					p = r
				}
				if a, err := filepath.Abs(p); err == nil {
					p = a
				}
				return b, p
			}
		}
	}
	return nil, ""
}

// projectConfigPackPaths is _project_config_pack_paths.
func projectConfigPackPaths(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil
	}
	var out []string
	for _, x := range o.GetList("mission_packs") {
		if s, ok := x.(string); ok {
			out = append(out, filepath.Join(root, s))
		}
	}
	return out
}

func envMissionPaths() []string {
	var out []string
	for _, c := range strings.Split(os.Getenv("SPEC_KITTY_MISSION_PATHS"), string(os.PathListSeparator)) {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// templateSource is _runtime_template_key + template loading: the runtime
// template bytes for a mission type and the path recorded as template_path.
// Tier walk: env, project override, project legacy, org packs, project
// config packs, then built-in/global (software-dev) or global/built-in.
func templateSource(root, missionType string) ([]byte, string, error) {
	k := filepath.Join(root, ".kittify")
	tiers := [][]string{envMissionPaths(), {filepath.Join(k, "overrides", "missions")}, {filepath.Join(k, "missions")},
		dct.OrgRoots(root), projectConfigPackPaths(root)}
	global := []string{filepath.Join(project.GlobalKittify(), "missions")}
	builtin := []string{"<builtin>"}
	if missionType == "software-dev" {
		tiers = append(tiers, builtin, global)
	} else {
		tiers = append(tiers, global, builtin)
	}
	for _, roots := range tiers {
		for _, r := range roots {
			if r == "<builtin>" {
				for _, name := range []string{"mission-runtime.yaml", "mission.yaml"} {
					rel := path.Join("packs/built-in/missions", missionType, name)
					if b, err := assets.ReadFile(rel); err == nil && templateKeyOf(b, rel) == missionType {
						return b, rel, nil
					}
				}
				continue
			}
			if b, p := resolveTemplateInRoot(r, missionType); p != "" {
				return b, p, nil
			}
		}
	}
	return nil, "", fmt.Errorf("No runtime template found for mission type '%s'", missionType)
}

// Snapshot is the run state.json.
type Snapshot struct {
	Obj *jsonx.Object
}

// RunRef points at a run directory.
type RunRef struct {
	RunID, RunDir, MissionType string
}

func (s *Snapshot) issued() string { return s.Obj.GetString("issued_step_id") }

func (s *Snapshot) completed() []string { return strList(s.Obj, "completed_steps") }

func (s *Snapshot) untouched() bool {
	pd := s.Obj.GetObject("pending_decisions")
	dd := s.Obj.GetObject("decisions")
	return len(s.completed()) == 0 && (pd == nil || pd.Len() == 0) && (dd == nil || dd.Len() == 0)
}

// ReadSnapshot reads <run>/state.json.
func ReadSnapshot(runDir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(runDir, "state.json"))
	if err != nil {
		return nil, err
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Obj: o}, nil
}

func writeSnapshot(runDir string, s *Snapshot) error {
	return os.WriteFile(filepath.Join(runDir, "state.json"), []byte(jsonx.Dumps(s.Obj, jsonx.SortedPretty)), 0o644)
}

func runIndexPath(root string) string {
	return filepath.Join(root, ".kittify", "runtime", "feature-runs.json")
}

func loadIndex(root string) *jsonx.Object {
	b, err := os.ReadFile(runIndexPath(root))
	if err != nil {
		return &jsonx.Object{}
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

func indexKey(slug, missionID string) string {
	if missionID != "" {
		return missionID
	}
	return "legacy-" + slug
}

func entryFor(idx *jsonx.Object, slug, missionID string) *jsonx.Object {
	if e := idx.GetObject(indexKey(slug, missionID)); e != nil {
		return e
	}
	for _, k := range idx.Keys() {
		if e := idx.GetObject(k); e != nil && e.GetString("mission_slug") == slug {
			return e
		}
	}
	return nil
}

// ExistingRun returns the indexed run of the mission (nil when none).
func ExistingRun(root, slug, missionID, missionType string) *RunRef {
	e := entryFor(loadIndex(root), slug, missionID)
	if e == nil {
		return nil
	}
	dir := e.GetString("run_dir")
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		return nil
	}
	mt := e.GetString("mission_type")
	if mt == "" {
		mt = missionType
	}
	return &RunRef{RunID: e.GetString("run_id"), RunDir: dir, MissionType: mt}
}

func newRunID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

func systemActor() *jsonx.Object {
	return jsonx.NewObject("actor_id", "system", "actor_type", "service", "display_name", "", "model", nil, "provider", nil, "tool", nil)
}

func agentActor(agent string) *jsonx.Object {
	return jsonx.NewObject("actor_id", agent, "actor_type", "llm", "display_name", "", "model", nil, "provider", nil, "tool", nil)
}

// appendRunEvent appends one runtime event to run.events.jsonl
// (engine._append_event). A refusal propagates: it stops the advance, which
// `next` reports as a blocked "Runtime engine error".
func appendRunEvent(runDir, eventType string, payload *jsonx.Object) error {
	o := jsonx.NewObject("event_type", eventType, "payload", payload, "timestamp", clock.NowUTCISO())
	// One locked O_APPEND write plus fsync per event; a torn last line left
	// by a crash is refused rather than extended (#6606).
	return eventlog.Append(filepath.Join(runDir, "run.events.jsonl"),
		[]string{jsonx.Dumps(o, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})}, eventlog.Options{})
}

// startRun creates a run directory under store (frozen template, state,
// MissionRunStarted event).
func startRun(store, root, slug, missionType string) (*RunRef, error) {
	tb, tpath, err := templateSource(root, missionType)
	if err != nil {
		return nil, err
	}
	runID := newRunID()
	dir := filepath.Join(store, "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "mission_template_frozen.yaml"), tb, 0o644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(tb)
	key := missionType
	if k := templateKeyOf(tb, tpath); k != "" {
		key = k
	}
	st := jsonx.NewObject("blocked_reason", nil, "completed_steps", []any{}, "decisions", &jsonx.Object{},
		"inputs", jsonx.NewObject("mission_slug", slug), "issued_step_id", nil, "mission_id", nil, "mission_key", key,
		"mission_slug", nil, "pending_decisions", &jsonx.Object{},
		"policy_snapshot", jsonx.NewObject("default_route", "same_llm_context", "extras", &jsonx.Object{}, "strictness", "medium"),
		"run_id", runID, "template_hash", hex.EncodeToString(sum[:]), "template_path", tpath)
	if err := writeSnapshot(dir, &Snapshot{Obj: st}); err != nil {
		return nil, err
	}
	if err := appendRunEvent(dir, "MissionRunStarted", jsonx.NewObject("actor", systemActor(), "mission_id", nil, "mission_slug", nil,
		"mission_type", missionType, "run_id", runID)); err != nil {
		return nil, err
	}
	return &RunRef{RunID: runID, RunDir: dir, MissionType: key}, nil
}

// GetOrStartRun is runtime_bridge_io.get_or_start_run.
func GetOrStartRun(root, slug, missionID, missionType string) (*RunRef, error) {
	if r := ExistingRun(root, slug, missionID, missionType); r != nil {
		return r, nil
	}
	store := filepath.Join(root, ".kittify", "runtime")
	ref, err := startRun(store, root, slug, missionType)
	if err != nil {
		return nil, err
	}
	idx := loadIndex(root)
	var mid any
	if missionID != "" {
		mid = missionID
	}
	rel, _ := filepath.Rel(root, ref.RunDir)
	idx.Set(indexKey(slug, missionID), jsonx.NewObject("mission_id", mid, "mission_key", ref.MissionType,
		"mission_slug", slug, "mission_type", missionType, "run_dir", filepath.ToSlash(rel), "run_id", ref.RunID))
	os.MkdirAll(filepath.Dir(runIndexPath(root)), 0o755)
	lock := runIndexPath(root) + ".lock"
	if _, err := os.Stat(lock); err != nil {
		os.WriteFile(lock, nil, 0o644)
	}
	if err := os.WriteFile(runIndexPath(root), []byte(jsonx.Dumps(idx, jsonx.SortedPretty)), 0o644); err != nil {
		return nil, err
	}
	return ref, nil
}

// loadTemplate reads the run's frozen template.
func loadTemplate(runDir string) (*Template, error) {
	b, err := os.ReadFile(filepath.Join(runDir, "mission_template_frozen.yaml"))
	if err != nil {
		return nil, err
	}
	return ParseTemplate(b)
}

// planned is a planner result (_internal_runtime.planner.plan_next).
type planned struct {
	Kind, StepID, Reason string
	DecisionID, Question string
	Options              []string
	InputKey             string
}

// planNext is plan_next over a snapshot and template.
func planNext(s *Snapshot, t *Template) planned {
	if r := s.Obj.GetString("blocked_reason"); r != "" {
		return planned{Kind: KindBlocked, Reason: r}
	}
	if pd := s.Obj.GetObject("pending_decisions"); pd != nil && pd.Len() > 0 {
		keys := pd.Keys()
		sort.Strings(keys)
		req := pd.GetObject(keys[0])
		p := planned{Kind: KindDecisionRequired, Reason: "pending_decision"}
		if req != nil {
			p.StepID, p.DecisionID, p.Question = req.GetString("step_id"), req.GetString("decision_id"), req.GetString("question")
			p.Options = strList(req, "options")
			if strings.HasPrefix(p.DecisionID, "input:") {
				p.InputKey = strings.TrimPrefix(p.DecisionID, "input:")
			}
		}
		return p
	}
	done := map[string]bool{}
	for _, c := range s.completed() {
		done[c] = true
	}
	issued := s.issued()
	remaining := false
	for _, st := range t.Steps {
		if done[st.ID] || st.ID == issued {
			continue
		}
		remaining = true
		ok := true
		for _, d := range st.DependsOn {
			if !done[d] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		inputs := s.Obj.GetObject("inputs")
		decisions := s.Obj.GetObject("decisions")
		for _, req := range st.RequiresInputs {
			if (inputs == nil || !inputs.Has(req)) && (decisions == nil || !decisions.Has(req)) {
				return planned{Kind: KindDecisionRequired, StepID: st.ID, DecisionID: "input:" + req, InputKey: req,
					Question: fmt.Sprintf("Input required before step '%s': provide value for '%s'.", st.ID, req), Reason: "missing_required_input"}
			}
		}
		return planned{Kind: KindStep, StepID: st.ID}
	}
	if remaining {
		return planned{Kind: KindBlocked, Reason: "No eligible steps: remaining steps have unmet dependencies."}
	}
	return planned{Kind: KindTerminal, Reason: "All mission steps completed"}
}

// raciDecision is the inferred RACI record of a prompt step.
func raciDecision(stepID string) *jsonx.Object {
	return jsonx.NewObject("accountable", jsonx.NewObject("actor_id", nil, "actor_type", "human"), "consulted", []any{},
		"inferred_rule", "prompt_default", "informed", []any{}, "override_reason", nil,
		"responsible", jsonx.NewObject("actor_id", nil, "actor_type", "llm"), "source", "inferred", "step_id", stepID)
}

// advanceRun completes the issued step (success) and issues the next
// planned step; returns the planner decision after the advance. The whole
// read-plan-append-write runs under the run-cursor lock (run_lock).
// beforeCompleted, when set, is the abort-only guard on the transition into
// terminal: it runs after planning and before anything is appended or
// written, and its error aborts the advance with the run untouched.
func advanceRun(ref *RunRef, agent, result string, beforeCompleted func() error) (planned, error) {
	lock, err := runCursorLock(ref.RunDir)
	if err != nil {
		return planned{}, err
	}
	defer lock.Release()
	s, err := ReadSnapshot(ref.RunDir)
	if err != nil {
		return planned{}, err
	}
	t, err := loadTemplate(ref.RunDir)
	if err != nil {
		return planned{}, err
	}
	completed := s.issued()
	if issued := s.issued(); issued != "" {
		switch result {
		case "success":
			comp := s.completed()
			has := false
			for _, c := range comp {
				has = has || c == issued
			}
			if !has {
				comp = append(comp, issued)
			}
			l := []any{}
			for _, c := range comp {
				l = append(l, c)
			}
			s.Obj.Set("completed_steps", l)
		case "failed":
			s.Obj.Set("blocked_reason", fmt.Sprintf("Previous step '%s' failed; manual intervention required.", issued))
		case "blocked":
			s.Obj.Set("blocked_reason", fmt.Sprintf("Previous step '%s' reported blocked state.", issued))
		}
		s.Obj.Set("issued_step_id", nil)
	}
	p := planNext(s, t)
	if beforeCompleted != nil && p.Kind == KindTerminal && completed != "" {
		if err := beforeCompleted(); err != nil {
			return planned{}, err
		}
	}
	if completed != "" {
		if err := appendRunEvent(ref.RunDir, "NextStepAutoCompleted", jsonx.NewObject("actor", agentActor(agent), "agent_id", agent,
			"mission_id", nil, "mission_slug", nil, "result", result, "run_id", ref.RunID, "step_id", completed)); err != nil {
			return planned{}, err
		}
	}
	if p.Kind == KindStep {
		s.Obj.Set("issued_step_id", p.StepID)
		dec := s.Obj.GetObject("decisions")
		if dec == nil {
			dec = &jsonx.Object{}
			s.Obj.Set("decisions", dec)
		}
		if !dec.Has("raci:" + p.StepID) {
			dec.Set("raci:"+p.StepID, raciDecision(p.StepID))
		}
		err = appendRunEvent(ref.RunDir, "NextStepIssued", jsonx.NewObject("actor", agentActor(agent), "agent_id", agent,
			"mission_id", nil, "mission_slug", nil, "run_id", ref.RunID, "step_id", p.StepID))
	} else if p.Kind == KindDecisionRequired && p.DecisionID != "" {
		err = requestDecisionInput(ref, s, p, agent)
	} else if p.Kind == KindTerminal && completed != "" {
		err = appendRunEvent(ref.RunDir, "MissionRunCompleted", jsonx.NewObject("actor", agentActor(agent), "mission_id", nil,
			"mission_slug", nil, "mission_type", s.Obj.GetString("mission_key"), "run_id", ref.RunID))
	}
	if err != nil {
		return planned{}, err
	}
	if err := writeSnapshot(ref.RunDir, s); err != nil {
		return planned{}, err
	}
	return p, nil
}
