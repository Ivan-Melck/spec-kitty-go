package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const nativeRepairHint = "spec-kitty doctor tool-surfaces --kind native_config --fix"

type nativeConfigProvider struct{}

func (nativeConfigProvider) key() string              { return "native_config" }
func (nativeConfigProvider) handles(kind string) bool { return kind == KindNativeConfig }
func (nativeConfigProvider) definitions() []Definition {
	return []Definition{{Kind: KindNativeConfig, SourceKind: "generated", Scope: "project", PathPattern: ".vibe/config.toml",
		Required: "repairable_required", Activation: "always", Provider: "native_config", RepairHint: nativeRepairHint}}
}

func (nativeConfigProvider) expand(def Definition, tool, root string) []Instance {
	if tool != "vibe" {
		return []Instance{{Def: def, Path: "<unsupported>", Owner: tool}}
	}
	return []Instance{{Def: def, Path: filepath.Join(root, ".vibe", "config.toml"), Owner: tool}}
}

func (nativeConfigProvider) probe(c *collectCtx, inst Instance) Status {
	if inst.Path == "<unsupported>" {
		f := Finding{Code: "research-gap-surface", Severity: "info", Message: "No known native-config glue for " + inst.Owner + ".",
			Tool: inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true}
		return Status{inst, StateNotApplicable, []Finding{f}}
	}
	root := strings.TrimSuffix(inst.Path, string(filepath.Separator)+filepath.Join(".vibe", "config.toml"))
	if agents.VibeSkillPathConfigured(root) {
		return Status{inst, StatePresent, nil}
	}
	return Status{inst, StateMissing, []Finding{instFinding("native-config-missing", "error",
		fmt.Sprintf("Native-config glue missing for %s: %s", inst.Owner, inst.Path), inst, true, nativeRepairHint)}}
}

func (p nativeConfigProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	selected := false
	for _, s := range sels {
		selected = selected || (s.Tool == "vibe" && s.Def.enabled() && s.Def.Required == "repairable_required")
	}
	na := func(reason string) *Assessment {
		return &Assessment{OwnerKey: "native_config", Root: in.Root, Complete: true,
			Dispositions: []Disposition{NewDisposition("native_config", in.Root.ID, "", false, "not_applicable", reason)}}
	}
	if !selected {
		return na("No selected verified native glue")
	}
	root := in.Root.Path
	obs, err := observePresencePath(root, ".kittify/config.yaml")
	if err != nil {
		return Incomplete("native_config", in.Root, "native_config_unreadable", err.Error())
	}
	cfg := obs[len(obs)-1][1].(FileState)
	if cfg.Kind != "file" && cfg.Kind != "absent" {
		return Incomplete("native_config", in.Root, "native_config_unreadable", "Agent config is not a regular file")
	}
	if cfg.Kind == "file" {
		ac, err := kittify.LoadAgentConfig(root)
		if err != nil {
			return Incomplete("native_config", in.Root, "native_config_unreadable", err.Error())
		}
		if !containsStr(ac.Available, "vibe") {
			return na("Vibe is disabled")
		}
	}
	vobs, err := observePresencePath(root, ".vibe/config.toml")
	if err != nil {
		return Incomplete("native_config", in.Root, "native_config_unreadable", err.Error())
	}
	prep, err := agents.PrepareVibeSkillPath(root)
	if err != nil {
		return Incomplete("native_config", in.Root, "native_config_unreadable", err.Error())
	}
	before := Absent()
	if prep.Exists {
		before = FileOf(Digest(prep.Before), prep.Mode)
	}
	a := &Assessment{OwnerKey: "native_config", Root: in.Root, Complete: true, Prepared: &prep}
	if prep.Changed() {
		ids := idsOf(statuses)
		proof := []Proof{{"managed_path", ".vibe/config.toml#skill_paths"}}
		for _, o := range vobs[1 : len(vobs)-1] {
			if o[1].(FileState).Kind == "absent" {
				a.Effects = append(a.Effects, NewEffect("native_config", "surface_repair", in.Root, o[0].(string), "create", Absent(), DirOf(0o755),
					"Native config parent", proof, []string{"vibe"}, ids))
			}
		}
		action := "update"
		mode := 0o644
		if !prep.Exists {
			action = "create"
		} else {
			mode = prep.Mode
		}
		a.Effects = append(a.Effects, NewEffect("native_config", "surface_repair", in.Root, ".vibe/config.toml", action, before, FileOf(Digest(prep.Desired), mode),
			"Add shared skill discovery", proof, []string{"vibe"}, ids))
		a.Artifacts = []Artifact{{".vibe", ".config.toml." + strings.Repeat("[0-9a-f]", 32) + ".tmp", "atomic replacement; removed before return"}}
	} else {
		a.Dispositions = []Disposition{NewDisposition("native_config", in.Root.ID, ".vibe/config.toml", true, "unchanged", "Native discovery is current")}
	}
	return a
}

func (p nativeConfigProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing)
	skipped := idsOf(statusesOf(statuses, StateNotApplicable))
	if len(actionable) == 0 {
		return RepairResult{Skipped: skipped}
	}
	a := p.assess(c, AssessInputs{Root: Root{"project", "project", root}, Automatic: true}, actionable,
		[]Selection{{"vibe", p.definitions()[0]}})
	if !a.Complete {
		var msgs []string
		for _, d := range a.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		return RepairResult{Skipped: skipped, Failed: msgs}
	}
	for _, d := range a.Dispositions {
		if d.State == "not_applicable" {
			return RepairResult{Skipped: append(skipped, idsOf(actionable)...)}
		}
	}
	var failed []string
	if len(a.Effects) > 0 {
		if err := applyNative(a); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return RepairResult{Skipped: skipped, Failed: failed}
	}
	return RepairResult{Repaired: idsOf(actionable), Skipped: skipped}
}

func applyNative(a *Assessment) error {
	prep, ok := a.Prepared.(*agents.PreparedVibeConfig)
	if !ok {
		return errors.New("Expected native preparation")
	}
	target := filepath.Join(a.Root.Path, filepath.FromSlash(prep.Rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if prep.Mode >= 0 {
		mode = os.FileMode(prep.Mode)
	}
	return writeFileAtomic(target, prep.Desired, mode)
}
