package nextrt

import (
	"crypto/rand"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

var stateAliases = map[string]string{
	"discovery": "research", "scoping": "specify", "methodology": "plan", "tasks_outline": "tasks-outline",
	"tasks_packages": "tasks-packages", "tasks_finalize": "tasks-finalize", "gathering": "implement",
	"synthesis": "review", "output": "accept", "goals": "specify", "structure": "plan", "draft": "plan",
}

// resolveCommandTemplate is runtime.resolver.resolve_command (org tier
// omitted): returns the template content.
func resolveCommandTemplate(name, root, missionType string) (string, bool) {
	k := filepath.Join(root, ".kittify")
	for _, p := range []string{
		filepath.Join(k, "overrides", "missions", missionType, "command-templates", name),
		filepath.Join(k, "overrides", "command-templates", name),
		filepath.Join(k, "command-templates", name),
		filepath.Join(project.GlobalKittify(), "missions", missionType, "command-templates", name),
		filepath.Join(project.GlobalKittify(), "command-templates", name),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b), true
		}
	}
	for _, rel := range []string{
		path.Join("packs/built-in/missions", missionType, "command-templates", name),
		path.Join("packs/built-in/missions/mission-steps", missionType, strings.TrimSuffix(name, ".md"), "prompt.md"),
	} {
		if b, err := assets.ReadFile(rel); err == nil {
			return string(b), true
		}
	}
	return "", false
}

// templateAction is _template_state_action.
func templateAction(state, root, missionType string) string {
	if _, ok := resolveCommandTemplate(state+".md", root, missionType); ok {
		return state
	}
	if a, ok := stateAliases[state]; ok {
		return a
	}
	return ""
}

// governanceContext is prompt_builder._governance_context.
func governanceContext(root, action, featureDir string, profileID ...string) string {
	var prof *dct.Profile
	if len(profileID) > 0 && profileID[0] != "" {
		prof = dct.LoadProfileCatalog(root).Resolve(profileID[0])
	}
	if action != "" {
		if r, err := charter.BuildContext(root, action, charter.Options{MarkLoaded: true, Profile: prof, MissionType: mission.MissionType(mission.ReadMetaOrEmpty(featureDir))}); err == nil && r.Mode != "missing" {
			return r.Text
		}
	}
	g, err := charter.ResolveGovernance(root)
	if err != nil {
		return fmt.Sprintf("Governance: unavailable (%v)", err)
	}
	join := func(xs []string) string {
		if len(xs) == 0 {
			return "(none)"
		}
		return strings.Join(xs, ", ")
	}
	lines := []string{"Governance:", "  - Template set: " + g.TemplateSet, "  - Paradigms: " + join(g.Paradigms),
		"  - Directives: " + join(g.Directives), "  - Tools: " + join(g.Tools)}
	if len(g.Diagnostics) > 0 {
		lines = append(lines, "  - Diagnostics: "+strings.Join(g.Diagnostics, " | "))
	}
	return strings.Join(lines, "\n")
}

func writeNextPrompt(root, action, wpID, agent, slug, content string) string {
	suffix := ""
	if wpID != "" {
		suffix = "-" + wpID
	}
	p := filepath.Join(workflow.PromptTmpDir(root), fmt.Sprintf("spec-kitty-next-%s-%s-%s%s.md", agent, slug, action, suffix))
	if err := workflow.WritePromptFile(p, content); err != nil {
		return ""
	}
	return p
}

// writeTemplatelessPrompt is decision._write_templateless_step_prompt: a
// short actionable prompt in a mkstemp-named file.
func writeTemplatelessPrompt(m *missionCtx, action, agent string) (string, error) {
	content := fmt.Sprintf("# %s — %s\n\nPerform the `%s` step for this Mission according to its charter and spec.\n\nWhen the step is done, advance with:\n\n    spec-kitty next --agent %s --mission %s\n",
		m.MissionType, action, action, agent, m.Slug)
	dir := workflow.PromptTmpDir(m.Root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789_"
	for i := 0; i < 100; i++ {
		var b [8]byte
		rand.Read(b[:])
		suffix := make([]byte, 8)
		for j := range b {
			suffix[j] = chars[int(b[j])%len(chars)]
		}
		p := filepath.Join(dir, "spec-kitty-step-"+action+"-"+string(suffix)+".md")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		_, err = f.WriteString(content)
		f.Close()
		return p, err
	}
	return "", fmt.Errorf("No usable temporary file name found")
}

// buildTemplatePrompt is _build_template_prompt.
func buildTemplatePromptLoud(m *missionCtx, featureDir, action, agent string) (string, error) {
	tpl, ok := resolveCommandTemplate(action+".md", m.Root, m.MissionType)
	if !ok {
		return "", fmt.Errorf("Asset '%s.md' not found in any resolution tier (subdir='command-templates', mission='%s', project=%s)", action, m.MissionType, m.Root)
	}
	bar := strings.Repeat("=", 80)
	header := strings.Join([]string{bar, "Mission: " + m.Slug, "Agent: " + agent, "Mission directory: " + featureDir, bar}, "\n")
	content := header + "\n\n" + governanceContext(m.Root, action, featureDir) + "\n\n" + tpl
	return writeNextPrompt(m.Root, action, "", agent, m.Slug, content), nil
}

// buildWPPrompt is _build_wp_prompt.
func buildWPPromptLoud(m *missionCtx, action, wpID, agent string) (string, error) {
	f, err := wp.Find(m.Primary, wpID)
	if err != nil || f == "" {
		return "", fmt.Errorf("Canonical WORK_PACKAGE_TASK file for %s is missing", wpID)
	}
	w, err := wp.Read(f)
	if err != nil {
		return "", err
	}
	raw, _ := os.ReadFile(f)
	ws, err := lanes.ResolveWorkspace(m.Root, m.Slug, wpID)
	if err != nil {
		return "", err
	}
	bar := strings.Repeat("=", 80)
	box := strings.Repeat("=", 78)
	l := []string{bar, strings.ToUpper(action) + ": " + wpID, bar, "", "Agent: " + agent, "Mission: " + m.Slug,
		"Mission Type: " + m.MissionType, "Workspace: " + ws.WorktreePath}
	if ws.LaneID != "" {
		shared := strings.Join(ws.LaneWPIDs, ", ")
		if shared == "" {
			shared = wpID
		}
		l = append(l, fmt.Sprintf("Workspace contract: lane %s shared by %s", ws.LaneID, shared))
	} else {
		l = append(l, "Workspace contract: repository root planning workspace")
	}
	l = append(l, "")
	payload, perr := missionTypeGovernancePayload(m.Root, m.StatusDir)
	if perr != nil {
		return "", perr
	}
	if payload != "" {
		l = append(l, payload, "")
	}
	l = append(l, governanceContext(m.Root, action, m.StatusDir, strings.TrimSpace(w.String("agent_profile"))),
		"Authority references: project glossary `docs/context/`; architecture ADRs `docs/adr/`.", "")
	verb := "REVIEWING"
	if action == "implement" {
		verb = "IMPLEMENTING"
	}
	l = append(l, box, "  CRITICAL: WORK PACKAGE ISOLATION RULES", box, "  YOU ARE "+verb+": "+wpID, "", "  DO:",
		"    - Only modify status of "+wpID, "    - Ignore git commits and status changes from other agents", "", "  DO NOT:",
		"    - Change status of any WP other than "+wpID, "    - React to or investigate other WPs' status changes", box, "",
		"WORKING DIRECTORY:", "  cd "+ws.WorktreePath)
	if ws.LaneID == "" {
		l = append(l, "  # Planning-artifact work for this WP happens in the repository root")
	}
	l = append(l, "")
	if action == "review" {
		l = append(l, "REVIEW COMMANDS:")
		if ws.LaneID != "" {
			base := ""
			if ws.Context != nil {
				base = ws.Context.BaseBranch
			}
			if base == "" {
				base = m.Meta.GetString("target_branch")
			}
			l = append(l, fmt.Sprintf("  git log %s..HEAD --oneline", base), fmt.Sprintf("  git diff %s..HEAD --stat", base))
		} else {
			l = append(l, "  unavailable: no deterministic implementation claim commit found for this WP")
		}
		l = append(l, "", workflow.ReviewAntipatternChecklist, "")
	}
	l = append(l, box, "  WORK PACKAGE PROMPT BEGINS", box, "", string(raw), "", box, "  WORK PACKAGE PROMPT ENDS", box, "", "WHEN DONE:")
	if action == "implement" {
		if subs := w.Subtasks(); len(subs) > 0 {
			l = append(l, fmt.Sprintf("  spec-kitty agent tasks mark-status %s --status done --mission %s", strings.Join(subs, " "), m.Slug))
		} else {
			l = append(l, "  No subtask completion command is needed; this work package declares no subtasks.")
		}
		l = append(l, fmt.Sprintf("  spec-kitty agent tasks move-task %s --to for_review --mission %s --note \"Ready for review\"", wpID, m.Slug))
	} else {
		l = append(l, fmt.Sprintf("  APPROVE: spec-kitty agent tasks move-task %s --to approved --mission %s --note \"Review passed\"", wpID, m.Slug),
			"           approved means review-passed; merge will later record done",
			fmt.Sprintf("  REJECT:  spec-kitty agent tasks move-task %s --to planned --review-feedback-file <feedback-file> --mission %s", wpID, m.Slug))
	}
	return writeNextPrompt(m.Root, action, wpID, agent, m.Slug, strings.Join(l, "\n")), nil
}
