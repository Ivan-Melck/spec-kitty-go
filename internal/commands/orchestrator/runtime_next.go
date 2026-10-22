package orchestrator

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/nextrt"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ── next ──────────────────────────────────────────────────────────────────

// boundedContent is design.context.bounded_content.
func boundedContent(path string) (*jsonx.Object, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, orch.MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > orch.MaxArtifactBytes {
		return nil, derr("DESIGN_CONTEXT_FAILED", "Host content exceeds the inline byte limit", "maximum_bytes", orch.MaxArtifactBytes)
	}
	return jsonx.NewObject("content", string(b), "sha256", orch.SHA256Hex(b), "bytes", len(b)), nil
}

func runNext(ctx *cli.Context) error {
	v := newVerb(ctx, "next")
	result, policy := optString(ctx, "result"), ctx.String("policy")
	if result != nil && policy == "" {
		return v.fail("POLICY_METADATA_REQUIRED", "--policy is required when advancing next", nil)
	}
	if policy != "" {
		if _, err := v.parsePolicy(policy); err != nil {
			return err
		}
	}
	if a := optString(ctx, "answer"); a != nil && len(*a) > 262144 {
		return v.fail("RUNTIME_NEXT_FAILED", "Decision input exceeds the inline byte limit", nil)
	}
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	slug := m.Slug
	argv := []string{"--mission", slug, "--json"}
	for _, f := range []string{"agent", "result", "answer", "decision-id"} {
		if p := optString(ctx, f); p != nil {
			argv = append(argv, "--"+f, *p)
		}
	}
	raw, code := delegate(ctx, []string{"next"}, argv...)
	pv, perr := pyjson.Loads(raw)
	payload, isObj := pv.(*jsonx.Object)
	if perr != nil || !isObj {
		return v.fail("RUNTIME_NEXT_FAILED", "Native next produced no parseable JSON",
			jsonx.NewObject("mission_slug", slug, "reason", "RUNTIME_NEXT_FAILED"))
	}
	setDefault(payload, "mission_slug", slug)
	if code != 0 || payload.GetString("kind") == "error" || payload.Has("error") {
		reason, rok := mustGet(payload, "reason").(string)
		errMsg, eok := mustGet(payload, "error").(string)
		if payload.GetString("kind") == "error" && rok && eok {
			data := jsonx.NewObject("mission_slug", slug, "reason", reason)
			if d, ok := mustGet(payload, "details").(*jsonx.Object); ok {
				for _, kv := range d.Pairs() {
					data.Set(kv.Key, kv.Value)
				}
			}
			return v.fail("RUNTIME_NEXT_FAILED", errMsg, data)
		}
		return v.fail("RUNTIME_NEXT_FAILED", "Native next refused the request", payload)
	}
	if payload.GetString("kind") == "blocked" {
		return v.fail("RUNTIME_BLOCKED", "Native next is blocked", payload)
	}
	pf, has := payload.Get("prompt_file")
	if has && pyjson.Truthy(pf) {
		p, ok := pf.(string)
		if !ok {
			return v.fail("RUNTIME_NEXT_FAILED", "Native prompt identity is invalid", jsonx.NewObject("mission_slug", slug, "reason", "RUNTIME_NEXT_FAILED"))
		}
		content, err := boundedContent(p)
		if err != nil {
			if de, ok := err.(*orch.DesignError); ok {
				return v.fail("RUNTIME_NEXT_FAILED", de.Message, jsonx.NewObject("mission_slug", slug, "reason", de.Code))
			}
			return v.fail("RUNTIME_NEXT_FAILED", "Native prompt is unreadable", jsonx.NewObject("mission_slug", slug, "reason", "RUNTIME_NEXT_FAILED"))
		}
		payload.Set("prompt", content)
		payload.Delete("prompt_file")
	} else if payload.GetString("kind") == "step" {
		return v.fail("RUNTIME_NEXT_FAILED", "Native step omitted its prompt", jsonx.NewObject("mission_slug", slug, "reason", "RUNTIME_NEXT_FAILED"))
	}
	return v.ok(payload)
}

// ── answer-decision ───────────────────────────────────────────────────────

var validResults = []string{"success", "failed", "blocked"}

func runAnswerDecision(ctx *cli.Context) error {
	v := newVerb(ctx, "answer-decision")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for answer-decision"); err != nil {
		return err
	}
	result := optString(ctx, "result")
	if result == nil {
		return v.fail("RESULT_REQUIRED", "--result is required alongside --answer for answer-decision", nil)
	}
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	valid := false
	for _, r := range validResults {
		if r == *result {
			valid = true
		}
	}
	if !valid {
		return v.fail("INVALID_RESULT", fmt.Sprintf("--result must be one of ('success', 'failed', 'blocked'), got '%s'", *result),
			jsonx.NewObject("result", *result, "valid_values", strList(validResults)))
	}
	meta := mission.ReadMetaOrEmpty(m.PrimaryDir)
	ref, err := nextrt.GetOrStartRun(root, m.Slug, meta.GetString("mission_id"), missionart.MissionTypeFor(m.PrimaryDir))
	if err != nil {
		return v.fail("RUNTIME_NEXT_FAILED", err.Error(), jsonx.NewObject("mission_slug", m.Slug, "reason", "RuntimeError"))
	}
	pending := []string{}
	if snap, err := nextrt.ReadSnapshot(ref.RunDir); err == nil {
		if pd := snap.Obj.GetObject("pending_decisions"); pd != nil {
			pending = pd.Keys()
		}
	}
	decisionID := optString(ctx, "decision-id")
	var resolved string
	if decisionID == nil {
		switch len(pending) {
		case 0:
			return v.fail("NO_PENDING_DECISION", "No pending decisions to answer", nil)
		case 1:
			resolved = pending[0]
		default:
			sorted := append([]string{}, pending...)
			sort.Strings(sorted)
			return v.fail("AMBIGUOUS_PENDING_DECISION",
				fmt.Sprintf("Multiple pending decisions (%s). Use --decision-id to specify which one.", strings.Join(sorted, ", ")),
				jsonx.NewObject("pending_decision_ids", strList(sorted)))
		}
	} else {
		resolved = *decisionID
		found := false
		for _, p := range pending {
			if p == resolved {
				found = true
			}
		}
		if !found {
			return v.fail("DECISION_NOT_PENDING", fmt.Sprintf("Decision %s is not currently pending for mission %s", pyjson.ReprString(resolved), pyjson.ReprString(handle)),
				jsonx.NewObject("decision_id", resolved))
		}
	}
	// The five-step composite is native `next --answer` (persist, pair the
	// previous issuance, decide, MissionNextInvoked, new issuance record);
	// its payload minus the echoed answer, plus answered_decision_id.
	raw, code := delegate(ctx, []string{"next"}, "--agent", ctx.String("agent"), "--result", *result, "--mission", m.Slug,
		"--json", "--answer", ctx.String("answer"), "--decision-id", resolved)
	pv, perr := pyjson.Loads(strings.TrimSpace(raw))
	payload, isObj := pv.(*jsonx.Object)
	if code != 0 || perr != nil || !isObj || payload.Has("error") {
		msg := "Native next refused the request"
		if isObj {
			if e := payload.GetString("error"); e != "" {
				msg = e
			}
		}
		return v.fail("RUNTIME_NEXT_FAILED", msg, jsonx.NewObject("mission_slug", m.Slug, "decision_id", resolved, "reason", "RUNTIME_NEXT_FAILED"))
	}
	warnings, hasWarnings := payload.Get("warnings")
	payload.Delete("answered")
	payload.Delete("answer")
	payload.Delete("warnings")
	payload.Set("answered_decision_id", resolved)
	if hasWarnings && pyjson.Truthy(warnings) {
		payload.Set("warnings", warnings)
	}
	return v.ok(payload)
}
