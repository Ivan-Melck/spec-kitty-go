package orchestrator

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const (
	interviewArtifactBytes = 262144
	interviewAnswerBytes   = 16384
	interviewActorBytes    = 1024
)

// ── interview-record ──────────────────────────────────────────────────────

// validateAnswers is design.context._validate_answers.
func validateAnswers(stage string, answers any, actor string) (*jsonx.Object, error) {
	if err := checkStage(stage); err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, q := range questionsFor(stage) {
		keys[q[0]] = true
	}
	obj, ok := answers.(*jsonx.Object)
	if strings.TrimSpace(actor) == "" || !ok || obj.Len() == 0 {
		return nil, derr("DESIGN_INTERVIEW_INVALID", "Actor and a nonempty answer mapping are required")
	}
	for _, kv := range obj.Pairs() {
		s, isS := kv.Value.(string)
		if !keys[kv.Key] || !isS || strings.TrimSpace(s) == "" {
			return nil, derr("DESIGN_INTERVIEW_INVALID", "Answers must name canonical questions and contain nonempty strings")
		}
	}
	if len(actor) > interviewActorBytes {
		return nil, derr("DESIGN_INTERVIEW_INVALID", "Actor or answer exceeds its published byte limit")
	}
	for _, kv := range obj.Pairs() {
		if len(kv.Value.(string)) > interviewAnswerBytes {
			return nil, derr("DESIGN_INTERVIEW_INVALID", "Actor or answer exceeds its published byte limit")
		}
	}
	if len(jsonx.Dumps(obj, jsonx.Options{Indent: -1})) > interviewArtifactBytes {
		return nil, derr("DESIGN_INTERVIEW_INVALID", "Interview answers exceed the inline byte limit")
	}
	return obj, nil
}

// recordInterviewAnswers is design.context.record_interview_answers.
func (d *design) recordInterviewAnswers(stage string, answers any, actor string) (*jsonx.Object, error) {
	validated, err := validateAnswers(stage, answers, actor)
	if err != nil {
		return nil, err
	}
	ids := jsonx.NewObject()
	dc := &decision.Context{Root: d.root, Slug: d.slug}
	fail := func(err error) error {
		code := "RuntimeError"
		if de, ok := err.(*decision.Error); ok {
			code = de.Code
		}
		return orch.NewDesignError("DESIGN_INTERVIEW_FAILED", err.Error(), "reason", code, "mission_slug", d.slug,
			"decision_ids", ids.Clone(), "effect_state", "reconciliation_required")
	}
	for _, q := range questionsFor(stage) {
		key, question := q[0], q[1]
		v, ok := validated.Get(key)
		if !ok {
			continue
		}
		answer := v.(string)
		idx, err := decision.Load(d.m.PrimaryDir)
		if err != nil {
			return nil, fail(err)
		}
		step := stage + "." + key
		var id string
		if e := decision.FindByLogicalKey(idx, stage, &step, nil, key); e != nil {
			id = e.DecisionID
		} else {
			resp, err := dc.Open(decision.OpenRequest{Flow: stage, InputKey: key, Question: question, Options: []string{}, StepID: &step, Actor: actor})
			if err != nil {
				return nil, fail(err)
			}
			id = resp.DecisionID
		}
		if _, err := dc.Terminal(decision.TerminalRequest{DecisionID: id, Status: decision.StatusResolved, FinalAnswer: &answer, Actor: actor}); err != nil {
			return nil, fail(err)
		}
		ids.Set(key, id)
	}
	st, err := d.interviewStatus(stage)
	if err != nil {
		return nil, err
	}
	return jsonx.NewObject("mission_slug", d.slug, "stage", stage, "decision_ids", ids, "interview", st), nil
}

func runInterviewRecord(ctx *cli.Context) error {
	v := newVerb(ctx, "interview-record")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for interview-record"); err != nil {
		return err
	}
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	raw := ctx.String("answers")
	if len(raw) > interviewArtifactBytes {
		return v.fail("DESIGN_INTERVIEW_INVALID", "Interview JSON exceeds the inline byte limit", nil)
	}
	parsed, perr := pyjson.Loads(raw)
	if perr != nil {
		return v.fail("DESIGN_INTERVIEW_INVALID", "--answers must be valid JSON", nil)
	}
	payload, err := newDesign(m).recordInterviewAnswers(ctx.String("stage"), parsed, ctx.String("actor"))
	if err != nil {
		if de, ok := err.(*orch.DesignError); ok {
			return v.fail(de.Code, de.Message, de.Details)
		}
		return v.fail("DESIGN_INTERVIEW_FAILED", err.Error(), nil)
	}
	return v.ok(payload)
}

// contextDigest is design.context.planning_context_digest.
func (d *design) contextDigest(action string) (string, error) {
	ctxObj, err := buildDesignContext(d.root, d.m, action, "")
	if err != nil {
		return "", err
	}
	return ctxObj.GetString("context_sha256"), nil
}
