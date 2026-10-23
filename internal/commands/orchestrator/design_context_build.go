package orchestrator

import (
	"os"
	"path"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const designEntryLimit = 64

// stableValue drops presentation / location / session fields
// (design.context._stable).
func stableValue(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		out := jsonx.NewObject()
		for _, kv := range t.Pairs() {
			switch kv.Key {
			case "path", "source_path", "first_load", "references_count":
				continue
			}
			out.Set(kv.Key, stableValue(kv.Value))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = stableValue(x)
		}
		return out
	}
	return v
}

// buildDesignContext is design.context.build_design_context; m is nil
// before mission creation.
func buildDesignContext(root string, m *orch.Mission, stage, missionTypeFlag string) (*jsonx.Object, error) {
	if err := checkStage(stage); err != nil {
		return nil, err
	}
	activated, _ := doctrine.ActivatedMissionTypes(root)
	selected := missionTypeFlag
	if m != nil {
		mt := missionart.MissionTypeFor(m.PrimaryDir)
		charter.WarnMissionTypeContext(root, mt)
		if missionTypeFlag != "" && missionTypeFlag != mt {
			return nil, derr("DESIGN_CONTEXT_FAILED", "The mission type is immutable; --mission-type selects only pre-creation context")
		}
		selected = mt
	} else {
		charter.WarnMissionTypeContext(root, missionTypeFlag)
	}
	if selected == "" {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Choose an activated mission type before discovery")
	}
	isActivated := false
	for _, a := range activated {
		if a == selected {
			isActivated = true
		}
	}
	roster := doctrine.MissionTypeRoster(root)
	profile := roster[selected]
	if !isActivated || profile == nil {
		return nil, derr("DESIGN_CONTEXT_FAILED", (&doctrine.UnknownMissionTypeError{ID: selected, Registered: activated}).Error())
	}
	if profile.TemplateSet == nil {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Activated mission has no template mapping")
	}
	if profile.TemplateSet.Len() > designEntryLimit {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Template count exceeds the inline entry limit")
	}
	templates := jsonx.NewObject()
	keys := profile.TemplateSet.Keys()
	sortStrings(keys)
	for _, kind := range keys {
		file := profile.TemplateSet.GetString(kind)
		b, ok := missionart.ResolveTemplate(path.Base(file), root, selected)
		if !ok {
			return nil, derr("DESIGN_CONTEXT_FAILED", "Template not found: "+file)
		}
		if len(b) > orch.MaxArtifactBytes {
			return nil, derr("DESIGN_CONTEXT_FAILED", "Host content exceeds the inline byte limit", "maximum_bytes", orch.MaxArtifactBytes)
		}
		digest := orch.SHA256Hex(b)
		templates.Set(kind, jsonx.NewObject("name", path.Base(file), "content", string(b), "sha256", digest, "digest", digest))
	}
	var charterDigest any
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(charter.CharterYAML))); err == nil {
		charterDigest = orch.SHA256Hex(b)
	}
	var questions []slot
	for _, q := range questionsFor(stage) {
		questions = append(questions, slot{stage, q[0], q[1]})
	}
	actionSeq := make([]any, len(profile.ActionSequence))
	for i, a := range profile.ActionSequence {
		actionSeq[i] = a
	}
	types := make([]any, len(activated))
	for i, a := range activated {
		types[i] = a
	}
	governance := charter.ActionGovernanceDisclosure(root, stage, selected)
	var required any
	if ra, err := orch.LoadExpectedArtifacts(root, selected); err != nil {
		return nil, err
	} else if ra != nil {
		required = ra
	}
	ctxObj := jsonx.NewObject(
		"mission_type", selected,
		"stage", stage,
		"mission_types", types,
		"action_sequence", actionSeq,
		"templates", templates,
		"questions", questionObjects(questions, false),
		"governance", governance,
		"required_artifacts", required,
		"charter_sha256", charterDigest,
	)
	semantic := jsonx.Dumps(stableValue(ctxObj), jsonx.Options{Indent: -1, SortKeys: true, Compact: true})
	if len(semantic) > orch.ContextBytes {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Resolved context exceeds the inline batch limit", "maximum_bytes", orch.ContextBytes)
	}
	ctxObj.Set("context_sha256", orch.SHA256Hex([]byte(semantic)))
	var slug any
	if m != nil {
		slug = m.Slug
	}
	ctxObj.Set("mission_slug", slug)
	ctxObj.Set("limits", jsonx.NewObject("artifact_bytes", orch.MaxArtifactBytes, "batch_bytes", orch.ContextBytes,
		"entries", designEntryLimit, "answer_bytes", interviewAnswerBytes, "actor_bytes", interviewActorBytes))
	if m != nil {
		st, err := newDesign(m).interviewStatus(stage)
		if err != nil {
			return nil, err
		}
		ctxObj.Set("interview", st)
	} else {
		qs := questionObjects(questions, false)
		ids := []any{}
		for _, q := range questions {
			ids = append(ids, q.key)
		}
		ctxObj.Set("interview", jsonx.NewObject("questions", qs, "complete", false, "incomplete", ids, "decision_ids", jsonx.NewObject()))
	}
	if len(jsonx.Dumps(ctxObj, orch.JSON)) > orch.ContextBytes {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Resolved response including interviews exceeds the inline batch limit", "maximum_bytes", orch.ContextBytes)
	}
	return ctxObj, nil
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func runDesignContext(ctx *cli.Context) error {
	v := newVerb(ctx, "design-context")
	root := v.root()
	var m *orch.Mission
	if h := ctx.String("mission"); h != "" {
		var err error
		if m, err = v.mission(root, h); err != nil {
			return err
		}
	}
	payload, err := buildDesignContext(root, m, ctx.String("stage"), ctx.String("mission-type"))
	if err != nil {
		if de, ok := err.(*orch.DesignError); ok {
			return v.fail(de.Code, de.Message, de.Details)
		}
		if me, ok := err.(*orch.ManifestLoadError); ok {
			// Uncaught upstream: the loader exception escapes as a traceback.
			rich.Traceback(ctx.Stderr, me.Type, me.Msg, rich.Frame{Name: "design_context", Line: 16})
			return cli.Exit(1)
		}
		return v.fail("DESIGN_CONTEXT_FAILED", err.Error(), nil)
	}
	return v.ok(payload)
}
