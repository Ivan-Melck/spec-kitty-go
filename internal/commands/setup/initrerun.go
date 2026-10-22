package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type initError struct{ msg string }

func (e *initError) Error() string { return e.msg }

func resumeCommandDelivery(ctx *cli.Context, root string) (bool, error) {
	pending, ok, err := readPending(root)
	if err != nil || !ok {
		return false, err
	}
	cfg, err := kittify.LoadConfig(root)
	if err != nil {
		return false, err
	}
	if a := cfg.GetObject("agents"); a == nil || !a.Has("available") {
		return false, &initError{"Initialization stopped before agent selection was saved; inspect .kittify/config.yaml before retrying"}
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return false, err
	}
	var keys []string
	for _, p := range pending {
		if contains(ac.Available, p) {
			keys = append(keys, p)
		}
	}
	if r := agents.ProtectAllAgents(root); !r.Success {
		return false, &initError{"Cannot resume command delivery: " + strings.Join(r.Errors, "; ")}
	}
	out(ctx.Stdout, "[yellow]Resuming interrupted command-skill delivery from saved configuration.[/yellow]")
	if !installCommandSkillAgents(ctx.Stdout, root, keys) {
		return false, &initError{"Command delivery remains incomplete; resolve the reported collision or error before retrying init"}
	}
	return true, finishPending(root, pending)
}

func commandSkillsMissing(root string) []string {
	var missing []string
	for _, c := range agents.CommandSkillCommands {
		st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(agents.CommandSkillPath(c))))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			missing = append(missing, c)
		}
	}
	return missing
}

func repairRequestedCommandSkills(ctx *cli.Context, root string, requested []string) error {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return err
	}
	var keys []string
	for _, a := range requested {
		if commandSkillAgentSet[a] && contains(ac.Available, a) {
			keys = append(keys, a)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	missing := len(commandSkillsMissing(root)) > 0
	vibeMissing := contains(keys, "vibe") && !agents.VibeSkillPathConfigured(root)
	if !missing && !vibeMissing {
		return nil
	}
	if r := agents.ProtectAllAgents(root); !r.Success {
		return &initError{"Cannot repair command delivery: " + strings.Join(r.Errors, "; ")}
	}
	if missing {
		out(ctx.Stdout, "[yellow]Restoring missing command skills for this initialized clone.[/yellow]")
		if !installCommandSkillAgents(ctx.Stdout, root, keys) {
			return &initError{"Command-skill repair remains incomplete; resolve the reported collision or error"}
		}
	}
	if vibeMissing && !agents.VibeSkillPathConfigured(root) {
		out(ctx.Stdout, "[yellow]Restoring the missing vibe skill-path pointer for this initialized clone.[/yellow]")
		return agents.EnsureVibeSkillPath(root)
	}
	return nil
}

func validatedAgentSelection(requested string, hasRequested bool, configured []string) ([]string, error) {
	selected := configured
	if hasRequested {
		selected = nil
		seen := map[string]bool{}
		for _, p := range strings.Split(strings.ReplaceAll(requested, ";", ","), ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if p != "" && !seen[p] {
				seen[p] = true
				selected = append(selected, p)
			}
		}
		bad := len(selected) == 0
		for _, s := range selected {
			bad = bad || !agents.IsKnown(s)
		}
		if bad {
			return nil, &initError{"Invalid --ai selection; choose from: " + strings.Join(agents.Keys, ", ")}
		}
	}
	var unconfigured []string
	for _, s := range selected {
		if !contains(configured, s) {
			unconfigured = append(unconfigured, s)
		}
	}
	if len(unconfigured) > 0 {
		return nil, &initError{"Requested agents are not configured. Run: spec-kitty agent config add " + strings.Join(unconfigured, " ")}
	}
	return selected, nil
}

func checkInitializedCommandSkills(root, requested string, hasRequested bool) ([]string, error) {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil, err
	}
	selected, err := validatedAgentSelection(requested, hasRequested, ac.Available)
	if err != nil {
		return nil, err
	}
	any := false
	for _, s := range selected {
		any = any || commandSkillAgentSet[s]
	}
	if !any {
		return selected, nil
	}
	missing := commandSkillsMissing(root)
	vibeMissing := contains(selected, "vibe") && !agents.VibeSkillPathConfigured(root)
	if len(missing) > 0 || vibeMissing {
		var gaps []string
		if len(missing) > 0 {
			gaps = append(gaps, "Configured agent command skills are missing or empty: "+strings.Join(missing, ", "))
		}
		if vibeMissing {
			gaps = append(gaps, "the vibe skill-path pointer (.vibe/config.toml skill_paths) is missing")
		}
		return nil, &initError{strings.Join(gaps, "; ") + ". Run: spec-kitty agent config sync --create-missing --keep-orphaned"}
	}
	return selected, nil
}

func nativeSkillGap(root, skillRoot string) (absent, unusable []string) {
	for _, s := range agents.SkillOrder(agents.Skills()) {
		p := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(skillRoot, "/")), s.Name, "SKILL.md")
		st, err := os.Lstat(p)
		switch {
		case os.IsNotExist(err):
			absent = append(absent, s.Name)
		case err != nil || !st.Mode().IsRegular() || st.Size() == 0:
			unusable = append(unusable, strings.TrimSuffix(skillRoot, "/")+"/"+s.Name+"/SKILL.md")
		}
	}
	return
}

func restoreNativeProjectSkills(ctx *cli.Context, root string, keys []string) error {
	var native []string
	for _, k := range keys {
		if agents.SkillClass(k) == agents.SkillClassNative {
			native = append(native, k)
		}
	}
	if len(native) == 0 {
		return nil
	}
	type gap struct {
		agent, root string
		absent      []string
	}
	var pending []gap
	var unusable []string
	for _, a := range native {
		r := agents.ProjectSkillRoot(a)
		absent, bad := nativeSkillGap(root, r)
		unusable = append(unusable, bad...)
		if len(absent) > 0 {
			pending = append(pending, gap{a, strings.TrimSuffix(r, "/"), absent})
		}
	}
	if len(unusable) > 0 {
		return &initError{"Existing agent skill files are unusable and were left untouched: " + strings.Join(unusable, ", ") + ". Rename or remove the affected files, then re-run: spec-kitty init --ai " + strings.Join(native, ",")}
	}
	if len(pending) == 0 {
		return nil
	}
	if r := agents.ProtectAllAgents(root); !r.Success {
		return &initError{"Cannot restore missing agent skills: " + strings.Join(r.Errors, "; ")}
	}
	for _, g := range pending {
		now := clock.NowUTCISO()
		entries, _, err := agents.InstallProjectSkills(root, g.agent, now)
		if err != nil {
			return err
		}
		m := agents.LoadSkillsManifest(root)
		if m == nil {
			m = &agents.SkillsManifest{CreatedAt: now, UpdatedAt: now, SpecKittyVersion: app.Version}
		}
		idx := map[string]int{}
		for i, e := range m.Entries {
			idx[e.InstalledPath+"\x00"+e.AgentKey] = i
		}
		for _, e := range entries {
			if i, ok := idx[e.InstalledPath+"\x00"+e.AgentKey]; ok {
				m.Entries[i] = e
			} else {
				m.Entries = append(m.Entries, e)
			}
		}
		if err := agents.SaveSkillsManifest(root, m, now); err != nil {
			return err
		}
		undelivered, broken := nativeSkillGap(root, agents.ProjectSkillRoot(g.agent))
		if len(broken) > 0 {
			return &initError{fmt.Sprintf("Skill restoration for %s delivered unusable files: %s", g.agent, strings.Join(broken, ", "))}
		}
		if len(undelivered) > 0 {
			return &initError{fmt.Sprintf("Skill restoration for %s did not deliver: %s", g.agent, strings.Join(undelivered, ", "))}
		}
		outf(ctx.Stdout, "[green]%s:[/green] restored %d missing skills into %s (existing files preserved)", agents.Display(g.agent), len(g.absent), g.root+"/")
	}
	return nil
}

// initRerun is init's idempotent already-initialized branch.
func initRerun(ctx *cli.Context, root, aiRaw string, fromOption []string) error {
	w := ctx.Stdout
	resumed, err := resumeCommandDelivery(ctx, root)
	if err == nil && !resumed {
		if len(fromOption) > 0 {
			err = repairRequestedCommandSkills(ctx, root, fromOption)
		}
		if err == nil {
			var selected []string
			selected, err = checkInitializedCommandSkills(root, aiRaw, aiRaw != "")
			if err == nil {
				err = restoreNativeProjectSkills(ctx, root, selected)
			}
		}
	}
	if err != nil {
		out(w, "[red]Initialization incomplete:[/red] "+err.Error())
		return cli.Exit(1)
	}
	if wireMergeDrivers(w, root) == "installed" {
		out(w, "[dim]Installed clone-local merge-driver settings[/dim]")
	}
	if resumed {
		return nil
	}
	panelStyled(w, "[yellow]Already initialized.[/yellow]\n"+
		"Agent skill surfaces were verified on this re-run: missing per-agent\n"+
		"skill roots were restored. A missing or unusable managed surface exits 1\n"+
		"naming the affected paths — shared command skills with the recovery command\n"+
		"[cyan]spec-kitty agent config sync --create-missing --keep-orphaned[/cyan];\n"+
		"an unusable per-agent skill file is preserved untouched, so rename or\n"+
		"remove it and re-run init.\n"+
		"Run [cyan]spec-kitty upgrade[/cyan] to migrate to the latest version.", "[yellow]Already Initialized[/yellow]", "yellow", 1, 2)
	return nil
}
