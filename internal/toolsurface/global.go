package toolsurface

import (
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

func fromBootstrap(s bootstrap.FileState) FileState {
	if s.Kind == "absent" {
		return Absent()
	}
	return FileState{Kind: s.Kind, SHA256: s.SHA256, Target: s.Target, Mode: s.Mode}
}

// commonPath is os.path.commonpath for absolute paths.
func commonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	parts := strings.Split(filepath.Clean(paths[0]), string(filepath.Separator))
	for _, p := range paths[1:] {
		q := strings.Split(filepath.Clean(p), string(filepath.Separator))
		n := 0
		for n < len(parts) && n < len(q) && parts[n] == q[n] {
			n++
		}
		parts = parts[:n]
	}
	r := strings.Join(parts, string(filepath.Separator))
	if r == "" {
		return string(filepath.Separator)
	}
	return r
}

// AssessGlobalAssets is asset_preparation.assess_global_assets for the
// upgrade finalizer: runtime (optional), slash commands for commandKeys
// (nil = none selected family when commands is false) and the built-in
// skill selection for skillAgents, combined into the one "global_assets"
// owner.
func AssessGlobalAssets(runtime, commands bool, commandKeys []string, skillAgents []string) *Assessment {
	var families []bootstrap.Family
	if runtime {
		families = append(families, bootstrap.AssessRuntimeFamily())
	}
	if commands {
		keys := commandKeys
		if keys == nil {
			keys = []string{}
		}
		families = append(families, bootstrap.AssessCommandsFamily(keys))
	}
	families = append(families, bootstrap.AssessSkillsFamily(skillAgents))
	var anchors []string
	for _, f := range families {
		if f.Err == nil {
			anchors = append(anchors, f.Anchor)
		}
	}
	anchor := filepath.Dir(project.UserHome())
	if len(anchors) > 0 {
		anchor = commonPath(anchors)
	}
	a := &Assessment{OwnerKey: "global_assets", Root: Root{ID: "global_assets", Scope: "global", Path: anchor}, Complete: true}
	for _, f := range families {
		if f.Err != nil {
			a.Complete = false
			a.Diagnostics = append(a.Diagnostics, Diag("global_assets_unavailable", f.Owner, "error", f.Err.Error()))
			continue
		}
		for _, d := range f.Dispositions {
			a.Dispositions = append(a.Dispositions, NewDisposition(f.Owner, f.Owner, d.Path, true, d.State, d.Reason))
		}
	}
	if !a.Complete {
		a.Effects = nil
		return a
	}
	var effects []Effect
	for _, f := range families {
		root := Root{ID: f.Owner, Scope: "global", Path: f.Anchor}
		for _, e := range f.Effects {
			logical := e.Logical
			if len(logical) == 0 {
				logical = []string{f.Owner}
			}
			effects = append(effects, NewEffect("global_assets", "global_bootstrap", root, e.Rel, e.Action, fromBootstrap(e.Before), fromBootstrap(e.After),
				"Refresh canonical global asset", []Proof{{e.Proof.Kind, e.Proof.Reference}}, logical, nil))
		}
	}
	co, err := Coalesce(effects)
	if err != nil {
		a.Complete = false
		a.Diagnostics = append(a.Diagnostics, Diag("global_assets_unavailable", "global_assets", "error", err.Error()))
		return a
	}
	a.Effects = co
	return a
}
