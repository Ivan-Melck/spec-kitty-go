package nextrt

// Decision requests and answers on the run snapshot
// (_internal_runtime.engine._request_decision_input / provide_decision_answer).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// pydanticNow is now_utc() serialized by pydantic (mode="json"): ISO with "Z".
func pydanticNow() string {
	return strings.TrimSuffix(clock.NowUTCISO(), "+00:00") + "Z"
}

func runtimeActor(id, typ string) *jsonx.Object {
	return jsonx.NewObject("actor_id", id, "actor_type", typ, "display_name", "", "model", nil, "provider", nil, "tool", nil)
}

// requestDecisionInput records an input-keyed decision in pending_decisions
// (first occurrence only) and appends DecisionInputRequested.
func requestDecisionInput(ref *RunRef, s *Snapshot, p planned, agent string) error {
	pd := s.Obj.GetObject("pending_decisions")
	if pd == nil {
		pd = &jsonx.Object{}
		s.Obj.Set("pending_decisions", pd)
	}
	if pd.Has(p.DecisionID) {
		return nil
	}
	opts := []any{}
	for _, o := range p.Options {
		opts = append(opts, o)
	}
	var inputKey any
	if p.InputKey != "" {
		inputKey = p.InputKey
	}
	if err := appendRunEvent(ref.RunDir, "DecisionInputRequested", jsonx.NewObject("actor", agentActor(agent), "decision_id", p.DecisionID,
		"input_key", inputKey, "mission_id", nil, "mission_slug", nil, "options", opts, "question", p.Question,
		"run_id", ref.RunID, "step_id", p.StepID)); err != nil {
		return err
	}
	pd.Set(p.DecisionID, jsonx.NewObject("decision_id", p.DecisionID, "options", opts, "question", p.Question,
		"requested_at", pydanticNow(), "requested_by", agentActor(agent), "step_id", p.StepID))
	return nil
}

// NoPendingDecisionError / AmbiguousPendingDecisionError are the
// resolve_pending_decision_id refusals.
type NoPendingDecisionError struct{}

func (NoPendingDecisionError) Error() string { return "No pending decisions to answer" }

// AmbiguousPendingDecisionError lists the sorted pending ids.
type AmbiguousPendingDecisionError struct{ IDs []string }

func (e AmbiguousPendingDecisionError) Error() string {
	return fmt.Sprintf("Multiple pending decisions (%s). Use --decision-id to specify which one.", strings.Join(e.IDs, ", "))
}

// ResolvePendingDecisionID is resolve_pending_decision_id.
func ResolvePendingDecisionID(ref *RunRef) (string, error) {
	s, err := ReadSnapshot(ref.RunDir)
	if err != nil {
		return "", err
	}
	pd := s.Obj.GetObject("pending_decisions")
	if pd == nil || pd.Len() == 0 {
		return "", NoPendingDecisionError{}
	}
	keys := pd.Keys()
	if len(keys) == 1 {
		return keys[0], nil
	}
	sort.Strings(keys)
	return "", AmbiguousPendingDecisionError{keys}
}

// ProvideDecisionAnswer is engine.provide_decision_answer for input-keyed
// decisions answered by a human actor (the CLI default).
func ProvideDecisionAnswer(ref *RunRef, decisionID, answer, actorID string) error {
	lock, err := runCursorLock(ref.RunDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	s, err := ReadSnapshot(ref.RunDir)
	if err != nil {
		return err
	}
	pd := s.Obj.GetObject("pending_decisions")
	if pd == nil || !pd.Has(decisionID) {
		return fmt.Errorf("Decision '%s' not found in pending_decisions for run '%s'", decisionID, s.Obj.GetString("run_id"))
	}
	decisions := s.Obj.GetObject("decisions")
	if decisions == nil {
		decisions = &jsonx.Object{}
		s.Obj.Set("decisions", decisions)
	}
	if strings.HasPrefix(decisionID, "audit:") {
		return fmt.Errorf("Audit decisions require mission_owner_id to be set in inputs")
	}
	actor := runtimeActor(actorID, "human")
	rec := jsonx.NewObject("actor_id", actorID, "actor_type", "human", "answer", answer, "answered_at", pydanticNow(),
		"answered_by", actor, "authority_role", "human", "decision_id", decisionID, "rationale_linkage", nil)
	if issued := s.issued(); issued != "" && strings.HasPrefix(decisionID, "input:") {
		if r := decisions.GetObject("raci:" + issued); r != nil {
			if v, ok := r.Get("source"); ok {
				rec.Set("raci_source", v)
			}
			if v, ok := r.Get("override_reason"); ok && v != nil {
				rec.Set("override_reason", v)
			}
		}
	}
	decisions.Set(decisionID, rec)
	pd.Delete(decisionID)
	if strings.HasPrefix(decisionID, "input:") {
		inputs := s.Obj.GetObject("inputs")
		if inputs == nil {
			inputs = &jsonx.Object{}
			s.Obj.Set("inputs", inputs)
		}
		inputs.Set(strings.TrimPrefix(decisionID, "input:"), answer)
	}
	if err := writeSnapshot(ref.RunDir, s); err != nil {
		return err
	}
	return appendRunEvent(ref.RunDir, "DecisionInputAnswered", jsonx.NewObject("actor", actor, "answer", answer,
		"decision_id", decisionID, "mission_id", nil, "mission_slug", nil, "run_id", s.Obj.GetString("run_id")))
}

// AnswerDecision is next_cmd._handle_answer after argument validation:
// get-or-start the run, auto-resolve the decision id, answer it. Returns
// the answered decision id.
func AnswerDecision(root, slug, decisionID, answer, agent string) (string, error) {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, slug))
	mt := meta.GetString("mission_type")
	if mt == "" {
		mt = "software-dev"
	}
	ref, err := GetOrStartRun(root, slug, meta.GetString("mission_id"), mt)
	if err != nil {
		return "", err
	}
	if decisionID == "" {
		id, err := ResolvePendingDecisionID(ref)
		if err != nil {
			return "", err
		}
		decisionID = id
	}
	return decisionID, ProvideDecisionAnswer(ref, decisionID, answer, agent)
}
