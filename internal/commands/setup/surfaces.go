package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// notApplicableSurfaces is the number of registered tool-surface
// definitions that expand to "not applicable" for each agent in a fresh
// project (measured against the oracle; the count is additive per agent).
var notApplicableSurfaces = map[string]int{
	"antigravity": 2, "auggie": 5, "claude": 2, "codex": 3, "copilot": 2, "cursor": 2,
	"gemini": 2, "kilocode": 5, "kiro": 2, "letta": 3, "llxprt": 2, "opencode": 2,
	"pi": 3, "q": 5, "qwen": 5, "vibe": 2, "windsurf": 2,
}

// RegeneratedSurfaceLine is tool_surface.repair.REGENERATED_SURFACE_LINE.
func RegeneratedSurfaceLine(path string) string {
	return "Regenerated a generated command skill that differed from the current version: " + path
}

// SurfaceSummary is DriftPolicySummary (counts only).
type SurfaceSummary struct {
	Created  []string
	Repaired []string
	Drifted  []string
	Skipped  int
	// Pruned are de-admitted profile projections removed by the repair
	// (project-relative; the profile manifest is rewritten with them).
	Pruned []string
}

// Lines is render_surface_summary_lines (rich markup).
func (s SurfaceSummary) Lines() []string {
	var l []string
	if len(s.Created) > 0 {
		l = append(l, fmt.Sprintf("[dim]Created %d tool surface(s)[/dim]", len(s.Created)))
	}
	if len(s.Repaired) > 0 {
		l = append(l, fmt.Sprintf("[dim]Repaired %d stale tool surface(s)[/dim]", len(s.Repaired)))
	}
	if len(s.Drifted) > 0 {
		l = append(l, fmt.Sprintf("[dim]Note: %d tool surface(s) have local edits — run 'spec-kitty doctor tool-surfaces' to review[/dim]", len(s.Drifted)))
	}
	if s.Skipped > 0 {
		l = append(l, fmt.Sprintf("  %d surface(s) not applicable, skipped.", s.Skipped))
	}
	return l
}

type skillStatus struct{ missing, stale, drifted []string }

// classifySkills observes one agent's project skill surface before repair.
func classifySkills(root, agent string, prev *agents.SkillsManifest) skillStatus {
	var st skillStatus
	skillRoot := agents.ProjectSkillRoot(agent)
	if skillRoot == "" {
		return st
	}
	owned := map[string]string{}
	recorded := map[string]bool{}
	if prev != nil {
		for _, e := range prev.Entries {
			owned[e.InstalledPath] = e.ContentHash
			if e.AgentKey == agent {
				recorded[e.InstalledPath] = true
			}
		}
	}
	for _, s := range agents.SkillOrder(agents.Skills()) {
		for _, rel := range s.Files {
			p := strings.TrimSuffix(skillRoot, "/") + "/" + s.Name + "/" + rel
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			if err != nil {
				st.missing = append(st.missing, p)
				continue
			}
			want, _ := agents.ReadSkillFile(s, rel)
			if rel == "SKILL.md" {
				want = []byte(agents.EnsureSkillFrontmatter(string(want), s.Name))
			}
			switch {
			case string(b) == string(want):
				if !recorded[p] {
					st.missing = append(st.missing, p)
				}
			case owned[p] == "sha256:"+sha256Hex(b):
				st.stale = append(st.stale, p)
			default:
				st.drifted = append(st.drifted, p)
			}
		}
	}
	return st
}

// RunSurfaceRepair is tool_surface.repair.run_surface_repair for init: it
// auto-creates missing and repairs stale managed surfaces (project skills
// and native agent-profile projections) for every configured agent and
// reports drifted ones without overwriting them.
func RunSurfaceRepair(root string) (SurfaceSummary, error) {
	var sum SurfaceSummary
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return sum, err
	}
	configured := ac.Available
	if len(configured) == 0 {
		return sum, nil
	}
	prev := agents.LoadSkillsManifest(root)
	statuses := map[string]skillStatus{}
	for _, a := range configured {
		sum.Skipped += notApplicableSurfaces[a]
		statuses[a] = classifySkills(root, a, prev)
	}
	now := clock.NowUTCISO()
	var newEntries []agents.SkillEntry
	for _, a := range configured {
		st := statuses[a]
		sum.Created = append(sum.Created, st.missing...)
		sum.Repaired = append(sum.Repaired, st.stale...)
		sum.Drifted = append(sum.Drifted, st.drifted...)
		if len(st.missing)+len(st.stale) == 0 {
			continue
		}
		entries, _, err := agents.InstallProjectSkills(root, a, now)
		if err != nil {
			return sum, err
		}
		newEntries = append(newEntries, entries...)
	}
	if len(newEntries) > 0 {
		// _prepare_project_skills: a fresh manifest carries no version; each
		// reconciled (path, agent) is re-added in (path, agent) order.
		m := agents.LoadSkillsManifest(root)
		if m == nil {
			m = &agents.SkillsManifest{}
		}
		sort.SliceStable(newEntries, func(i, j int) bool {
			if newEntries[i].InstalledPath != newEntries[j].InstalledPath {
				return newEntries[i].InstalledPath < newEntries[j].InstalledPath
			}
			return newEntries[i].AgentKey < newEntries[j].AgentKey
		})
		for _, e := range newEntries {
			m.AddEntry(e)
		}
		if err := agents.SaveSkillsManifest(root, m, now); err != nil {
			return sum, err
		}
	}
	pr, err := agents.ProjectProfiles(root, configured)
	if err != nil {
		return sum, err
	}
	sum.Created = append(sum.Created, pr.Created...)
	sum.Repaired = append(sum.Repaired, pr.Repaired...)
	sum.Drifted = append(sum.Drifted, pr.Drifted...)
	sum.Pruned = append(sum.Pruned, pr.Pruned...)
	return sum, nil
}

func init() { agents.ProfileAdmit = admitProfile }

// provisionMissionTypeActivations is
// charter.activation.compiler.provision_mission_type_activations: seed the
// default preset's mission types into the activation write target
// (charter.yaml behind a `charter:` pointer, else config.yaml) when the key
// is entirely absent.
func provisionMissionTypeActivations(root string) error {
	t, err := charter.LoadWriteTarget(root)
	if err != nil {
		return err
	}
	if t.Data.Has("mission_type_activations") {
		return nil
	}
	types, err := kittify.DefaultPresetMissionTypes()
	if err != nil {
		return err
	}
	if !t.IsCharter {
		_, err := kittify.ProvisionDefaultMissionTypeActivations(root)
		return err
	}
	l := make([]any, len(types))
	for i, x := range types {
		l[i] = x
	}
	t.Data.Set("mission_type_activations", l)
	return t.Save()
}

// activationProvisioningPending is _mission_type_activation_provisioning_pending.
func activationProvisioningPending(root string) bool {
	t, err := charter.LoadWriteTarget(root)
	if err != nil {
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			return true
		}
		return false
	}
	return !t.Data.Has("mission_type_activations")
}

// admitProfile is the active charter service's profile gate: the activation
// list (activated_agent_profiles) when present, then the language scope
// (applies_to_languages against infer_repo_languages; no signal admits all).
func admitProfile(root string, p agents.Profile) bool {
	if lists, present, err := charter.ListActivated(root); err == nil && present["agent-profile"] {
		found := false
		for _, id := range lists["agent-profile"] {
			found = found || id == p.ID
		}
		if !found {
			return false
		}
	}
	if len(p.AppliesToLanguages) == 0 {
		return true
	}
	langs, known := charter.InferRepoLanguages(root, nil, false)
	if !known {
		return true
	}
	have := map[string]bool{}
	for _, l := range charter.NormalizeLanguages(langs) {
		have[l] = true
	}
	for _, l := range charter.NormalizeLanguages(p.AppliesToLanguages) {
		if have[l] {
			return true
		}
	}
	return false
}

// presenceContent builds the orientation content for root.
func presenceContent(root string) agents.Presence {
	slug := kittify.LoadIdentity(root).Slug
	if slug == "" {
		slug = "unknown"
	}
	return agents.Presence{Version: app.Version, ProjectSlug: slug, Health: "healthy"}
}

// InstallPresence is SessionPresenceManager.install: writes orientation for
// configured agents lacking it; returns the change lines.
func InstallPresence(root string, keys []string) []string {
	c := presenceContent(root)
	var changes []string
	for _, k := range keys {
		w := agents.WriterFor(k)
		if w.CanWrite(root) && !w.HasPresence(root) {
			if err := w.WriteOrientation(root, c); err == nil {
				changes = append(changes, "Wrote orientation for "+k)
			}
		}
	}
	return changes
}
