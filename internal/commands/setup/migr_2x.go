package setup

// Ports of the 2.x upgrade migrations (m_0_16_2 and m_2_*).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	migrationImpls["2.0.0a5_remove_wp_status_gitignore_rule"] = mRemoveWPStatusGitignoreRule()
	migrationImpls["2.0.0_charter_directory"] = neverNeeded("Superseded by charter-rename migration")
	migrationImpls["2.0.0_historical_status_migration"] = neverNeeded("")
	migrationImpls["2.0.0_retire_git_hooks"] = mRetireGitHooks()
	migrationImpls["2.0.1_fix_generated_command_templates"] = mFixGeneratedCommandTemplates()
	migrationImpls["2.0.1_tool_config_key_rename"] = mToolConfigKeyRename()
	migrationImpls["2.0.2_charter_context_bootstrap"] = neverNeeded("Superseded by charter-rename migration")
	// 2.0.6_consistency_sweep is superseded by its residue-safe successor (migr_residue.go, #6669).
	migrationImpls["2.0.7_fix_stale_overrides"] = mFixStaleOverrides()
	migrationImpls["2.0.9_state_gitignore"] = mStateGitignore()
	migrationImpls["2.0.11_install_skills"] = mInstallSkills()
	migrationImpls["2.0.11_remove_clarify_command"] = mRemoveClarifyCommand()
	migrationImpls["2.1.1_repair_skill_pack"] = mRepairSkillPack()
	migrationImpls["2.1.2_fix_charter_doctrine_skill"] = neverNeeded("Superseded by charter-rename migration")
	migrationImpls["2.1.2_fix_glossary_context_skill"] = neverNeeded("Superseded by the charter-pack cutover")
	migrationImpls["2.1.2_fix_orchestrator_api_skill"] = skillMarkerReplacement("spec-kitty-orchestrator-api-operator",
		[]string{"## Step 1: Verify the API Contract\n\n```bash\nspec-kitty orchestrator-api contract-version"},
		"Cannot locate canonical SKILL.md for orchestrator-api", "All orchestrator-api skill files already up to date")
	migrationImpls["2.1.2_fix_runtime_next_skill"] = skillMarkerReplacement("spec-kitty-runtime-next",
		[]string{"## Step 1: Load Runtime Context\n\nBefore invoking the runtime, gather"},
		"Cannot locate canonical SKILL.md for runtime-next", "All runtime-next skill files already up to date")
	migrationImpls["2.1.2_install_git_workflow_skill"] = skillInstall("spec-kitty-git-workflow",
		[]string{"SKILL.md", "references/git-operations-matrix.md"}, "Cannot locate canonical SKILL.md for git-workflow", "Git-workflow skill already installed")
	migrationImpls["2.1.2_install_mission_system_skill"] = skillInstall("spec-kitty-mission-system",
		[]string{"SKILL.md", "references/mission-comparison-matrix.md"}, "Cannot locate canonical SKILL.md for mission-system", "Mission-system skill already installed")
	migrationImpls["2.1.2_remove_release_skill"] = mRemoveReleaseSkill()
	migrationImpls["2.1.3_fix_planning_repository_terminology"] = mFixPlanningRepositoryTerminology()
	migrationImpls["2.1.3_restore_prompt_commands"] = mRestorePromptCommands()
	migrationImpls["2.1.4_enforce_command_file_state"] = mEnforceCommandFileState()
	migrationImpls["2.2.0_profile_context_deployment"] = mProfileContextDeployment()
}

// --- 2.0.0a5 ----------------------------------------------------------------

var wpStatusIgnorePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^kitty-specs/\*\*/tasks/\*\.md$`),
	regexp.MustCompile(`^kitty-specs/\*/tasks/\*\.md$`),
	regexp.MustCompile(`^# Block WP status files.*$`),
	regexp.MustCompile(`^# Research artifacts in kitty-specs/\*\*/research/ are allowed$`),
}

func isWPStatusIgnore(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	for _, r := range wpStatusIgnorePatterns {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

func mRemoveWPStatusGitignoreRule() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			content, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
			if err != nil {
				return true
			}
			for _, l := range pySplitLines(content) {
				if isWPStatusIgnore(l) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if r := gitignoreSymlinkRefusal(filepath.Join(root, ".gitignore")); r != "" {
				return false, r
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			p := filepath.Join(root, ".gitignore")
			if r := gitignoreSymlinkRefusal(p); r != "" {
				return newResult(nil, nil, []string{r})
			}
			if !pathExists(p) {
				return newResult([]string{"No .gitignore file found"}, nil, nil)
			}
			content, err := readIgnoreFileText(p)
			if err != nil {
				return newResult(nil, nil, []string{"Failed to read .gitignore: " + err.Error()})
			}
			var ch, kept []string
			removed := 0
			for i, l := range pySplitLinesKeep(content) {
				if isWPStatusIgnore(l) {
					ch = append(ch, fmt.Sprintf("Line %d: Removed '%s'", i+1, strings.TrimSpace(l)))
					removed++
					continue
				}
				kept = append(kept, l)
			}
			if removed == 0 {
				return newResult([]string{"No stale WP status ignore entries found in .gitignore"}, nil, nil)
			}
			if dryRun {
				return newResult(append([]string{fmt.Sprintf("Would remove %d stale WP status ignore entries from .gitignore", removed)}, ch...), nil, nil)
			}
			nc := regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(kept, ""), "\n\n")
			if err := os.WriteFile(p, []byte(nc), 0o666&^umask()); err != nil {
				return newResult(ch, nil, []string{"Failed to write .gitignore: " + pyOSError(err)})
			}
			return newResult(append([]string{fmt.Sprintf("Removed %d stale WP status ignore entries from .gitignore", removed)}, ch...), nil, nil)
		},
	}
}

// --- 2.0.0 retire git hooks ---------------------------------------------------

var managedHookNames = []string{"pre-commit", "commit-msg", "pre-commit-agent-check", "pre-commit-encoding-check", "pre-commit-markdown-check"}

var managedHookSignatures = map[string][][]string{
	"pre-commit": {{"SPEC_KITTY_MANAGED_HOOK_SHIM=1"},
		{"Main pre-commit hook that orchestrates all pre-commit checks", "pre-commit-encoding-check", "pre-commit-agent-check"}},
	"commit-msg": {{"SPEC_KITTY_MANAGED_HOOK_SHIM=1"},
		{"commit-msg hook to enforce conventional commit messages via commitlint", "feat(doctrine): add markdown and commit quality gates"}},
	"pre-commit-agent-check":    {{"Pre-commit hook to prevent committing agent directories", ".github/copilot"}},
	"pre-commit-encoding-check": {{"Pre-commit hook to validate UTF-8 encoding in markdown files", "spec-kitty validate-encoding --all --fix"}},
	"pre-commit-markdown-check": {{"Pre-commit hook to validate markdown style on staged files", "markdownlint-cli2", "SPEC_KITTY_TEST_MODE"}},
}

func isManagedHook(p string) bool {
	if !isFile(p) {
		return false
	}
	sigs := managedHookSignatures[filepath.Base(p)]
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	c := decodeIgnoreErrors(b)
	for _, sig := range sigs {
		all := true
		for _, m := range sig {
			all = all && strings.Contains(c, m)
		}
		if all {
			return true
		}
	}
	return false
}

func mRetireGitHooks() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			hd := filepath.Join(root, ".git", "hooks")
			if !isDir(hd) {
				return false
			}
			for _, n := range managedHookNames {
				if isManagedHook(filepath.Join(hd, n)) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			hd := filepath.Join(root, ".git", "hooks")
			if !isDir(hd) {
				return newResult([]string{"No .git/hooks directory found"}, nil, nil)
			}
			var ch, warn, errs []string
			removed := 0
			for _, n := range managedHookNames {
				p := filepath.Join(hd, n)
				if !pathExists(p) {
					continue
				}
				if !isManagedHook(p) {
					warn = append(warn, "Skipped custom hook: "+n)
					continue
				}
				if dryRun {
					ch = append(ch, "Would remove managed hook: "+n)
					continue
				}
				if err := os.Remove(p); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s: %s", n, pyOSError(err)))
					continue
				}
				removed++
				ch = append(ch, "Removed managed hook: "+n)
			}
			if len(ch) == 0 {
				ch = append(ch, "No managed git hooks found")
			}
			if removed > 0 && !dryRun {
				ch = append([]string{fmt.Sprintf("Removed %d managed git hook(s)", removed)}, ch...)
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 2.0.1 ------------------------------------------------------------------

var generatedCommandReplacements = [][2]string{
	{"spec-kitty agent check-prerequisites", "spec-kitty agent mission check-prerequisites"},
	{"--require-tasks --include-tasks", "--include-tasks"},
	{"--require-tasks", "--include-tasks"},
	{"(Missing script command for sh)", "spec-kitty agent mission check-prerequisites --json --paths-only"},
	{"main repository root", "primary repository checkout root"},
}

var staleMergePatterns = []*regexp.Regexp{
	regexp.MustCompile(`merges each .* into main in sequence`),
	regexp.MustCompile(`Merges each .* into the target branch in sequence`),
	regexp.MustCompile(`merges each lane branch into the mission branch before the target branch`),
}

func generatedPromptFiles(root string) ([]string, error) {
	dirs, err := migrationAgentDirs(root)
	if err != nil {
		return nil, err
	}
	var cmdDirs []string
	for _, d := range dirs {
		cmdDirs = append(cmdDirs, filepath.Join(root, d.root, d.subdir))
	}
	if lc := filepath.Join(root, ".codex", "prompts"); pathExists(lc) {
		cmdDirs = append(cmdDirs, lc)
	}
	seen := map[string]bool{}
	var files []string
	for _, cd := range cmdDirs {
		if !pathExists(cd) || seen[cd] {
			continue
		}
		seen[cd] = true
		files = append(files, globFiles(cd, "spec-kitty.*.md")...)
		files = append(files, globFiles(cd, "spec-kitty.*.toml")...)
	}
	return files, nil
}

func mFixGeneratedCommandTemplates() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			files, err := generatedPromptFiles(root)
			if err != nil {
				return false, err
			}
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				c := string(b)
				if strings.Contains(c, "spec-kitty agent check-prerequisites") || strings.Contains(c, "--require-tasks") || strings.Contains(c, "(Missing script command for sh)") {
					return true, nil
				}
				for _, p := range staleMergePatterns {
					if p.MatchString(c) {
						return true, nil
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			files, err := generatedPromptFiles(root)
			if err != nil {
				return migrationResult{}, err
			}
			var ch, warn, errs []string
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					warn = append(warn, fmt.Sprintf("Skipped unreadable file %s: %s", f, pyOSError(err)))
					continue
				}
				up := string(b)
				for _, r := range generatedCommandReplacements {
					up = strings.ReplaceAll(up, r[0], r[1])
				}
				for _, p := range staleMergePatterns {
					up = p.ReplaceAllLiteralString(up, "merges lane branches into the mission branch before landing the mission branch")
				}
				if up == string(b) {
					continue
				}
				rel, _ := filepath.Rel(root, f)
				if dryRun {
					ch = append(ch, "Would update: "+rel)
					continue
				}
				if err := writeGeneratedFile(f, []byte(up), true); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to update %s: %s", rel, err))
					continue
				}
				ch = append(ch, "Updated: "+rel)
			}
			if len(ch) == 0 && len(errs) == 0 {
				ch = append(ch, "No generated prompt files needed repair")
			}
			return newResult(ch, warn, errs), nil
		},
	}
}

// loadConfigSafe is YAML(typ="safe").load(config.yaml): nil on any failure.
func loadConfigSafe(root string) *jsonx.Object {
	b, err := os.ReadFile(kittify.ConfigPath(root))
	if err != nil {
		return nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil
	}
	if v == nil {
		return &jsonx.Object{}
	}
	o, _ := v.(*jsonx.Object)
	return o
}

func configCanApply(root string) (bool, string) {
	p := kittify.ConfigPath(root)
	if !lexists(p) {
		return ok()
	}
	if !isFile(p) {
		return false, "config path exists but is not a file"
	}
	if _, err := os.ReadFile(p); err != nil {
		return false, "config file not readable: " + pyOSError(err)
	}
	return ok()
}

// writeConfigRoundTrip dumps config.yaml the way ruamel's round-trip
// YAML().dump does for the migrations (open(..., "w")).
func writeConfigRoundTrip(root string, cfg *jsonx.Object) error {
	return kittify.SaveConfig(root, cfg)
}

func mToolConfigKeyRename() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(kittify.ConfigPath(root)) {
				return false
			}
			cfg := loadConfigSafe(root)
			return cfg != nil && cfg.Has("agents") && !cfg.Has("tools")
		},
		canApply: configCanApply,
		apply: func(root string, dryRun bool) migrationResult {
			if !pathExists(kittify.ConfigPath(root)) {
				return newResult([]string{"No config.yaml found"}, nil, nil)
			}
			b, _ := os.ReadFile(kittify.ConfigPath(root))
			v, err := yamlx.Parse(b)
			if err != nil {
				return migrationResult{Errors: []string{fmt.Sprintf("Invalid YAML in config.yaml: %v", err)}}
			}
			cfg, isObj := v.(*jsonx.Object)
			if v == nil {
				cfg, isObj = &jsonx.Object{}, true
			}
			if !isObj {
				return migrationResult{Errors: []string{"config.yaml root must be a mapping"}}
			}
			if cfg.Has("tools") {
				return newResult([]string{"Config already uses canonical `tools` key"}, nil, nil)
			}
			if !cfg.Has("agents") {
				return newResult([]string{"No legacy `agents` key found"}, nil, nil)
			}
			if dryRun {
				return newResult([]string{"Would rename .kittify/config.yaml key `agents` -> `tools`"}, nil, nil)
			}
			a, _ := cfg.Get("agents")
			cfg.Delete("agents")
			cfg.Set("tools", a)
			if err := writeConfigRoundTrip(root, cfg); err != nil {
				return migrationResult{Errors: []string{"Failed writing config.yaml: " + pyOSError(err)}}
			}
			return newResult([]string{"Renamed .kittify/config.yaml key `agents` -> `tools`"}, nil, nil)
		},
	}
}

// --- 2.0.6 consistency sweep ----------------------------------------------------

var promptLineRe = regexp.MustCompile("(?m)^(\\*\\*Prompt\\*\\*:\\s*`?)([^`\\n]+)(`?)$")

func staleLegacyAssets(root string) bool {
	k := filepath.Join(root, ".kittify")
	if !pathExists(k) {
		return false
	}
	n := 0
	for _, item := range listDir(k) {
		name := filepath.Base(item)
		switch {
		case (name == "templates" || name == "missions" || name == "scripts" || name == "command-templates") && isDir(item):
			filepath.Walk(item, func(p string, info os.FileInfo, err error) error {
				if err == nil && p != item {
					if st, serr := os.Stat(p); serr == nil && st.Mode().IsRegular() {
						n++
					}
				}
				return nil
			})
		case name == "AGENTS.md" && isFile(item):
			n++
		}
	}
	return n > 0
}

func worktreeCleanupRoots(root string) []string {
	for _, part := range strings.Split(filepath.ToSlash(root), "/") {
		if part == ".worktrees" {
			return []string{root}
		}
	}
	return worktreeDirs(root)
}

func featureDirs(root string) []string { return subdirs(filepath.Join(root, "kitty-specs")) }

// baselineFeatureMeta is upgrade.feature_meta.build_baseline_feature_meta.
func baselineFeatureMeta(feature, root string, existing *jsonx.Object) *jsonx.Object {
	slug := filepath.Base(feature)
	number, tail, _ := strings.Cut(slug, "-")
	meta := &jsonx.Object{}
	if existing != nil {
		meta = existing.Clone()
	}
	setIfBlank := func(k string, v func() any) {
		cur, ok := meta.Get(k)
		if !ok || cur == nil {
			meta.Set(k, v())
			return
		}
		if s, isS := cur.(string); isS && strings.TrimSpace(s) == "" {
			meta.Set(k, v())
		}
	}
	setIfBlank("mission_number", func() any {
		if number != "" && strings.Trim(number, "0123456789") == "" {
			return number
		}
		return ""
	})
	setIfBlank("slug", func() any { return slug })
	setIfBlank("mission_slug", func() any { return slug })
	setIfBlank("friendly_name", func() any {
		f := strings.TrimSpace(strings.ReplaceAll(tail, "-", " "))
		if f == "" {
			return slug
		}
		return f
	})
	setIfBlank("mission_type", func() any {
		if mt, ok := meta.Get("mission_type"); ok && mt != nil {
			if s := strings.TrimSpace(fmt.Sprint(mt)); s != "" {
				return s
			}
		}
		if pathExists(filepath.Join(feature, "research")) {
			return "research"
		}
		return "software-dev"
	})
	setIfBlank("target_branch", func() any { return inferTargetBranch(feature, root) })
	setIfBlank("created_at", func() any { return inferCreatedAt(feature) })
	return meta
}

func inferCreatedAt(feature string) string {
	var min int64 = -1
	var minT os.FileInfo
	filepath.Walk(feature, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() || p == feature {
			if t := info.ModTime().UnixNano(); min < 0 || t < min {
				min, minT = t, info
			}
		}
		return nil
	})
	if minT == nil {
		return clock.ISO(nowUTC())
	}
	return clock.ISO(minT.ModTime().UTC())
}

func wpNeedsNormalization(feature string) bool {
	tasks := filepath.Join(feature, "tasks")
	if !pathExists(tasks) {
		return false
	}
	for _, wp := range globFiles(tasks, "WP*.md") {
		b, _ := os.ReadFile(wp)
		c := strings.TrimPrefix(decodeIgnoreErrors(b), "\ufeff")
		if regexp.MustCompile(`(?m)^lane:\s*["']`).MatchString(c) || regexp.MustCompile(`(?m)^lane:\s*doing\s*$`).MatchString(c) {
			return true
		}
		front, _, _, err := frontmatterRead(wp)
		if err != nil {
			return true
		}
		l, ok := front.Get("lane")
		if !ok || l == nil || strings.TrimSpace(pyStrValue(l)) == "" {
			return true
		}
	}
	return false
}

func pyStrValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return yamlx.FormatScalar(v)
}

func orphanStatusSnapshot(feature string) bool {
	sp, ep := filepath.Join(feature, status.SnapshotFile), filepath.Join(feature, status.EventsFile)
	if !pathExists(sp) || pathExists(ep) {
		return false
	}
	b, err := os.ReadFile(sp)
	if err != nil {
		return false
	}
	d, err := jsonx.DecodeObject(b)
	if err != nil {
		return false
	}
	ec, _ := d.Get("event_count")
	if n, ok := ec.(int); !ok || n != 0 {
		if f, ok := ec.(float64); !ok || f != 0 {
			return false
		}
	}
	wps := d.GetObject("work_packages")
	if wps == nil || wps.Len() != 0 {
		return false
	}
	sum := d.GetObject("summary")
	if sum == nil || sum.Len() != len(status.CLILanes) {
		return false
	}
	for _, l := range status.CLILanes {
		v, ok := sum.Get(l)
		if !ok {
			return false
		}
		if n, isInt := v.(int); !isInt || n != 0 {
			return false
		}
	}
	fs := d.GetString("feature_slug")
	return fs == "" || fs == filepath.Base(feature)
}

func featureHasEventLog(feature string) bool {
	b, err := os.ReadFile(filepath.Join(feature, status.EventsFile))
	return err == nil && strings.TrimSpace(string(b)) != ""
}

func featureHasStatusDrift(feature string) bool {
	if !featureHasEventLog(feature) {
		return false
	}
	drift, _ := status.ValidateMaterializationDrift(feature)
	return len(drift) > 0
}

func tasksMDLegacyPromptRefs(feature string) bool {
	b, err := os.ReadFile(filepath.Join(feature, "tasks.md"))
	if err != nil {
		return false
	}
	for _, l := range pySplitLines(decodeIgnoreErrors(b)) {
		if !strings.HasPrefix(l, "**Prompt**:") {
			continue
		}
		for _, seg := range []string{"tasks/planned/", "tasks/doing/", "tasks/for_review/", "tasks/done/"} {
			if strings.Contains(l, seg) {
				return true
			}
		}
	}
	return false
}

func featureRequiresRepair(feature, root string) bool {
	meta, err := mission.ReadMeta(feature)
	if err != nil {
		return true
	}
	if meta == nil {
		return true
	}
	if !jsonEqual(meta, baselineFeatureMeta(feature, root, meta)) {
		return true
	}
	return tasksMDLegacyPromptRefs(feature) || orphanStatusSnapshot(feature) || wpNeedsNormalization(feature) || featureHasStatusDrift(feature)
}

func jsonEqual(a, b *jsonx.Object) bool {
	return jsonx.Dumps(a, jsonx.Options{SortKeys: true}) == jsonx.Dumps(b, jsonx.Options{SortKeys: true})
}

// consistencySweep is the 2.0.6 consistency sweep over the kitty-specs/
// folders missions(root) yields (the residue-safe successor passes Missions only).
func consistencySweep(missions func(root string) []string) migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if staleLegacyAssets(root) {
				return true
			}
			if len(worktreeCleanupRoots(root)) > 0 {
				return true
			}
			for _, f := range missions(root) {
				if featureRequiresRepair(f, root) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if pathExists(filepath.Join(root, ".kittify")) || pathExists(filepath.Join(root, "kitty-specs")) {
				return ok()
			}
			return false, "No .kittify/ or kitty-specs/ directory found"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			if pathExists(filepath.Join(root, ".kittify")) {
				rep := migrate.ExecuteLayoutMigration(root, dryRun)
				moved, removed := len(rep.Moved), len(rep.Removed)
				if moved > 0 || removed > 0 {
					verb := "migrated"
					if dryRun {
						verb = "would migrate"
					}
					ch = append(ch, fmt.Sprintf("%s %d identical and %d customized legacy runtime asset(s)", verb, removed, moved))
				}
				if len(rep.Conflicts) > 0 {
					warn = append(warn, fmt.Sprintf("%d customized legacy runtime asset(s) not moved: an override already exists, "+
						"a non-folder blocks the overrides path, or the file is a symbolic link; review the legacy copies "+
						"(run 'spec-kitty migrate --dry-run --verbose' to list them)", len(rep.Conflicts)))
				}
			}
			cleaned := 0
			for _, wr := range worktreeCleanupRoots(root) {
				did := false
				name := filepath.Base(wr)
				for _, d := range allAgentDirs() {
					cd := filepath.Join(wr, d.root, d.subdir)
					if !lexists(cd) {
						continue
					}
					did = true
					if dryRun {
						ch = append(ch, fmt.Sprintf("[%s] would remove %s/%s/", name, d.root, d.subdir))
						continue
					}
					var err error
					if isSymlink(cd) {
						err = os.Remove(cd)
					} else {
						_, err = removeToolOwnedTree(cd, cd, "worktree agent commands dir")
					}
					if err == nil {
						if parent := filepath.Dir(cd); pathExists(parent) && len(listDir(parent)) == 0 {
							os.Remove(parent)
						}
					} else {
						errs = append(errs, fmt.Sprintf("[%s] failed to remove %s/%s/: %s", name, d.root, d.subdir, err))
					}
				}
				sd := filepath.Join(wr, ".kittify", "scripts")
				if lexists(sd) {
					did = true
					if dryRun {
						ch = append(ch, fmt.Sprintf("[%s] would remove .kittify/scripts/", name))
					} else {
						var err error
						if isSymlink(sd) {
							err = os.Remove(sd)
						} else {
							_, err = removeToolOwnedTree(sd, sd, "worktree .kittify/scripts")
						}
						if err != nil {
							errs = append(errs, fmt.Sprintf("[%s] failed to remove .kittify/scripts/: %s", name, err))
						}
					}
				}
				if did {
					cleaned++
				}
			}
			if cleaned > 0 {
				verb := "cleaned"
				if dryRun {
					verb = "would clean"
				}
				ch = append(ch, fmt.Sprintf("%s %d worktree(s) with legacy command/script assets", verb, cleaned))
			}
			for _, f := range missions(root) {
				c, w := repairFeature(f, root, dryRun)
				ch = append(ch, c...)
				warn = append(warn, w...)
			}
			if len(ch) == 0 && len(warn) == 0 && len(errs) == 0 {
				ch = append(ch, "Project already consistent for 2.0.6")
			}
			return newResult(ch, warn, errs)
		},
	}
}

func repairFeature(feature, root string, dryRun bool) ([]string, []string) {
	var ch, warn []string
	name := filepath.Base(feature)
	meta, err := mission.ReadMeta(feature)
	if err != nil && err != mission.ErrNoMeta {
		warn = append(warn, fmt.Sprintf("%s: invalid meta.json (%s)", name, metaReadError(feature, err)))
		meta = nil
	}
	if err == mission.ErrNoMeta {
		meta = nil
	}
	desired := baselineFeatureMeta(feature, root, meta)
	if meta == nil || !jsonEqual(meta, desired) {
		action := "wrote"
		if dryRun {
			action = "would write"
		}
		ch = append(ch, fmt.Sprintf("%s: %s baseline meta.json", name, action))
		if !dryRun {
			mission.WriteMeta(feature, desired, false)
		}
	}
	norm := 0
	if tasks := filepath.Join(feature, "tasks"); pathExists(tasks) {
		for _, wp := range globFiles(tasks, "WP*.md") {
			front, body, original, err := frontmatterRead(wp)
			if err != nil {
				warn = append(warn, fmt.Sprintf("%s: %s: %s", name, filepath.Base(wp), err))
				continue
			}
			raw := "planned"
			if l, ok := front.Get("lane"); ok && pyjsonTruthy(l) {
				raw = pyStrValue(l)
			}
			canon := status.ResolveLaneAlias(raw)
			for _, l := range status.CLILanes {
				if l == canon {
					front.Set("lane", canon)
				}
			}
			rendered := renderFrontmatter(front, body, original)
			if original == rendered {
				continue
			}
			if !dryRun {
				os.WriteFile(wp, []byte(rendered), 0o666&^umask())
			}
			norm++
		}
	}
	if norm > 0 {
		verb := "normalized"
		if dryRun {
			verb = "would normalize"
		}
		ch = append(ch, fmt.Sprintf("%s: %s %d work package files", name, verb, norm))
	}
	sp := filepath.Join(feature, status.SnapshotFile)
	if orphanStatusSnapshot(feature) {
		backup := status.SnapshotFile + ".orphan.bak." + compactStamp()
		if !dryRun {
			mvRename(sp, filepath.Join(feature, backup))
		}
		ch = append(ch, fmt.Sprintf("%s: archived orphan %s to %s", name, status.SnapshotFile, backup))
	} else if pathExists(sp) && !pathExists(filepath.Join(feature, status.EventsFile)) && !pathExists(filepath.Join(feature, "tasks")) {
		warn = append(warn, name+": status.json has no matching event log and could not be auto-repaired")
	}
	unreadable := false
	if b, err := os.ReadFile(filepath.Join(feature, status.EventsFile)); err == nil && strings.TrimSpace(string(b)) != "" {
		if rows, rerr := status.ReadRows(feature); rerr != nil || func() bool { _, perr := status.ParseRows(rows); return perr != nil }() {
			msg := ""
			if rerr != nil {
				msg = rerr.Error()
			} else {
				_, perr := status.ParseRows(rows)
				msg = perr.Error()
			}
			backup := status.EventsFile + ".unreadable.bak." + compactStamp()
			if !dryRun {
				mvRename(filepath.Join(feature, status.EventsFile), filepath.Join(feature, backup))
			}
			unreadable = true
			ch = append(ch, fmt.Sprintf("%s: archived unreadable %s to %s", name, status.EventsFile, backup))
			warn = append(warn, fmt.Sprintf("%s: status.events.jsonl was unreadable and has been quarantined (%s)", name, msg))
		}
	}
	if featureHasEventLog(feature) || unreadable {
		if dryRun {
			if unreadable || featureHasStatusDrift(feature) {
				ch = append(ch, name+": would regenerate status.json")
			}
		} else {
			status.Materialize(feature)
			if unreadable || featureHasStatusDrift(feature) {
				ch = append(ch, name+": regenerated status.json")
			}
		}
	}
	if n := rewriteTasksPromptRefs(feature, dryRun); n > 0 {
		verb := "rewrote"
		if dryRun {
			verb = "would rewrite"
		}
		ch = append(ch, fmt.Sprintf("%s: %s %d legacy prompt reference(s)", name, verb, n))
	}
	return ch, warn
}

func pyjsonTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case int:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}

var wpCodeRe = regexp.MustCompile(`(WP\d{2})`)

func rewriteTasksPromptRefs(feature string, dryRun bool) int {
	tmd := filepath.Join(feature, "tasks.md")
	tasks := filepath.Join(feature, "tasks")
	if !pathExists(tmd) || !pathExists(tasks) {
		return 0
	}
	b, _ := os.ReadFile(tmd)
	n := 0
	up := promptLineRe.ReplaceAllStringFunc(string(b), func(m string) string {
		g := promptLineRe.FindStringSubmatch(m)
		prefix, p, suffix := g[1], g[2], g[3]
		if !strings.Contains(p, "tasks/") {
			return m
		}
		w := wpCodeRe.FindString(p)
		if w == "" {
			return m
		}
		c := globFiles(tasks, w+"*.md")
		if len(c) == 0 {
			return m
		}
		i := strings.Index(p, "tasks/")
		np := p[:i] + "tasks/" + filepath.Base(c[0])
		if np == p {
			return m
		}
		n++
		return prefix + np + suffix
	})
	if n > 0 && !dryRun {
		os.WriteFile(tmd, []byte(up), 0o666&^umask())
	}
	return n
}

// --- 2.0.7 ------------------------------------------------------------------

func overrideMatchesPackage(override, rel string) bool {
	for _, p := range []string{"packs/built-in/missions/software-dev/" + rel, "packs/built-in/missions/" + rel} {
		if st, err := assets.Stat(p); err == nil && !st.IsDir() {
			a, _ := os.ReadFile(override)
			b, _ := assets.ReadFile(p)
			return string(a) == string(b)
		}
	}
	return false
}

func isSharedAsset(rel string) bool {
	top := strings.SplitN(rel, "/", 2)[0]
	return top == "templates" || top == "missions" || top == "scripts" || top == "command-templates" || filepath.Base(rel) == "AGENTS.md"
}

func rglobFiles(dir string) []string {
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && p != dir {
			if st, serr := os.Stat(p); serr == nil && st.Mode().IsRegular() {
				out = append(out, p)
			}
		}
		return nil
	})
	return sortedPaths(out)
}

func mFixStaleOverrides() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			od := filepath.Join(root, ".kittify", "overrides")
			for _, f := range rglobFiles(od) {
				rel, _ := filepath.Rel(od, f)
				rel = filepath.ToSlash(rel)
				if isSharedAsset(rel) && overrideMatchesPackage(f, rel) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if pathExists(filepath.Join(root, ".kittify", "overrides")) {
				return ok()
			}
			return false, "No .kittify/overrides/ directory found"
		},
		apply: func(root string, dryRun bool) migrationResult {
			od := filepath.Join(root, ".kittify", "overrides")
			if !pathExists(od) {
				return newResult([]string{"No overrides directory — nothing to do"}, nil, nil)
			}
			var ch []string
			for _, f := range rglobFiles(od) {
				rel, _ := filepath.Rel(od, f)
				rel = filepath.ToSlash(rel)
				if !isSharedAsset(rel) || !overrideMatchesPackage(f, rel) {
					continue
				}
				verb := "Removed"
				if dryRun {
					verb = "Would remove"
				}
				ch = append(ch, fmt.Sprintf("%s stale override: overrides/%s", verb, rel))
				if !dryRun {
					os.Remove(f)
				}
			}
			if !dryRun && pathExists(od) {
				var dirs []string
				filepath.Walk(od, func(p string, info os.FileInfo, err error) error {
					if err == nil && info.IsDir() && p != od {
						dirs = append(dirs, p)
					}
					return nil
				})
				dirs = sortedPaths(dirs)
				for i := len(dirs) - 1; i >= 0; i-- {
					if len(listDir(dirs[i])) == 0 {
						os.Remove(dirs[i])
					}
				}
				if len(listDir(od)) == 0 {
					os.Remove(od)
				}
			}
			if len(ch) == 0 {
				ch = append(ch, "No stale overrides found — nothing to do")
			}
			return newResult(ch, nil, nil)
		},
	}
}

// --- 2.0.9 ------------------------------------------------------------------

var stateRuntimeEntries = []string{".kittify/runtime/", ".kittify/merge-state.json", ".kittify/events/", ".kittify/dossiers/", "kitty-specs/**/.kittify/dossiers/"}

func mStateGitignore() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			content, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
			if err != nil {
				return false, err
			}
			for _, e := range stateRuntimeEntries {
				if !strings.Contains(content, e) {
					return true, nil
				}
			}
			return false, nil
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(root) {
				return false, "Project path does not exist: " + root
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			if dryRun {
				return newResult([]string{fmt.Sprintf("Would add %d runtime state entries to .gitignore", len(stateRuntimeEntries))}, nil, nil), nil
			}
			modified, err := agents.EnsureGitignoreEntries(root, stateRuntimeEntries, false)
			if err != nil {
				return migrationResult{}, failClosed("%s", err.Error())
			}
			if modified {
				return newResult([]string{"Added runtime state entries to .gitignore: " + strings.Join(stateRuntimeEntries, ", ")}, nil, nil), nil
			}
			return newResult([]string{"All runtime state entries already present in .gitignore"}, nil, nil), nil
		},
	}
}

// --- 2.0.11 / 2.1.1: canonical skill installs -------------------------------

// installableSkillAgents filters configured agents to skill-installing ones
// (AGENT_SKILL_CONFIG minus wrapper-only).
func installableSkillAgents(keys []string) []string {
	var out []string
	for _, a := range keys {
		cls, ok := agents.SkillConfigs[a]
		if !ok || cls.Class == agents.SkillClassWrapper {
			continue
		}
		out = append(out, a)
	}
	return out
}

// installAllSkills is skills.installer.install_all_skills for the canonical
// built-in catalog: entries for every agent, in agent order.
func installAllSkills(root string, agentKeys []string, now string) ([]agents.SkillEntry, error) {
	var all []agents.SkillEntry
	for _, a := range agentKeys {
		es, _, err := agents.InstallProjectSkills(root, a, now)
		if err != nil {
			return all, err
		}
		all = append(all, es...)
	}
	return all, nil
}

func mInstallSkills() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool { return !pathExists(agents.SkillsManifestPath(root)) },
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ directory does not exist (not initialized)"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			skills := agents.Skills()
			if len(skills) == 0 {
				return newResult(nil, []string{"No skills found to install"}, nil)
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return migrationResult{Errors: []string{"Could not load agent config: " + err.Error()}}
			}
			if len(ac.Available) == 0 {
				return newResult(nil, []string{"No agents configured; skipping skill installation"}, nil)
			}
			if dryRun {
				return newResult([]string{fmt.Sprintf("Would install %d skill(s) for %d agent(s)", len(skills), len(ac.Available))}, nil, nil)
			}
			now := clock.NowUTCISO()
			m := &agents.SkillsManifest{CreatedAt: now, UpdatedAt: now, SpecKittyVersion: "2.0.11"}
			var errs []string
			for _, a := range ac.Available {
				es, _, err := agents.InstallProjectSkills(root, a, now)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Skill installation failed for %s: %s", a, err))
					continue
				}
				for _, e := range es {
					m.AddEntry(e)
				}
			}
			var ch []string
			if len(m.Entries) > 0 {
				agents.SaveSkillsManifest(root, m, now)
				ch = append(ch, fmt.Sprintf("Installed %d skill(s) for %d agent(s) (%d managed files)", len(skills), len(ac.Available), len(m.Entries)),
					"Created .kittify/skills-manifest.json")
			} else {
				errs = append(errs, "No skill files were installed for any configured agent")
			}
			return newResult(ch, nil, errs)
		},
	}
}

func mRepairSkillPack() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return true
			}
			inst := installableSkillAgents(ac.Available)
			if len(inst) == 0 {
				return false
			}
			skills := agents.Skills()
			if len(skills) == 0 {
				return true
			}
			m := agents.LoadSkillsManifest(root)
			if m == nil || len(m.Entries) == 0 {
				return true
			}
			actual := map[string]bool{}
			for _, e := range m.Entries {
				if e.SourceFile == "SKILL.md" {
					actual[e.AgentKey+"\x00"+e.SkillName] = true
				}
			}
			for _, a := range inst {
				for _, s := range skills {
					if !actual[a+"\x00"+s.Name] {
						return true
					}
				}
			}
			return !verifyInstalledSkills(root, m)
		},
		canApply: func(root string) (bool, string) {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ directory does not exist (not initialized)"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			skills := agents.Skills()
			if len(skills) == 0 {
				return migrationResult{Errors: []string{"No canonical skills discovered for repair"}}
			}
			ac, err := kittify.LoadAgentConfig(root)
			if err != nil {
				return migrationResult{Errors: []string{"Could not load agent config: " + err.Error()}}
			}
			inst := installableSkillAgents(ac.Available)
			if len(inst) == 0 {
				return newResult(nil, []string{"No skill-installing agents configured; skipping repair"}, nil)
			}
			if dryRun {
				return newResult([]string{fmt.Sprintf("Would repair/install %d skill(s) for %d agent(s)", len(skills), len(inst))}, nil, nil)
			}
			now := clock.NowUTCISO()
			entries, err := installAllSkills(root, inst, now)
			if err != nil {
				return migrationResult{Errors: []string{"Skill repair failed: " + err.Error()}}
			}
			if len(entries) == 0 {
				return migrationResult{Errors: []string{"No skill files were installed for any configured agent"}}
			}
			m := &agents.SkillsManifest{CreatedAt: now, UpdatedAt: now, SpecKittyVersion: "2.1.1"}
			for _, e := range entries {
				m.AddEntry(e)
			}
			agents.SaveSkillsManifest(root, m, now)
			return newResult([]string{fmt.Sprintf("Installed %d skill(s) for %d agent(s) (%d managed files)", len(skills), len(inst), len(m.Entries)),
				"Rebuilt .kittify/skills-manifest.json"}, nil, nil)
		},
	}
}

// verifyInstalledSkills is skills.verifier.verify_installed_skills(...).ok:
// every manifest entry's file exists with its recorded hash.
func verifyInstalledSkills(root string, m *agents.SkillsManifest) bool {
	for _, e := range m.Entries {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.InstalledPath)))
		if err != nil {
			return false
		}
		if e.DeliveryMode != "symlink" && "sha256:"+sha256Hex(b) != e.ContentHash {
			return false
		}
	}
	return true
}

// --- 2.1.2 skill text migrations (upgrade.skill_update) ----------------------

var skillRoots = []string{".claude/skills", ".agents/skills", ".qwen/skills", ".kilocode/skills", ".github/skills", ".gemini/skills",
	".llxprt/skills", ".cursor/skills", ".opencode/skills", ".windsurf/skills", ".augment/skills", ".roo/skills", ".agent/skills", ".codex/skills"}

type skillFile struct{ path, root, rel string }

func findSkillFiles(root, name string, patterns []string) []skillFile {
	var out []skillFile
	for _, r := range skillRoots {
		dir := filepath.Join(root, filepath.FromSlash(r), name)
		if !isDir(dir) {
			continue
		}
		if patterns != nil {
			for _, p := range patterns {
				if f := filepath.Join(dir, filepath.FromSlash(p)); isFile(f) {
					out = append(out, skillFile{f, r, p})
				}
			}
			continue
		}
		for _, f := range rglobFiles(dir) {
			rel, _ := filepath.Rel(dir, f)
			out = append(out, skillFile{f, r, filepath.ToSlash(rel)})
		}
	}
	return out
}

func fileContainsAny(p string, markers []string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	for _, m := range markers {
		if strings.Contains(string(b), m) {
			return true
		}
	}
	return false
}

func canonicalSkillText(name, rel string) (string, bool) {
	b, err := assets.ReadFile("charter/offering/skills/" + name + "/" + rel)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// hasSymlinkComponent is skill_update._has_symlink_component.
func hasSymlinkComponent(dest, root string) bool {
	rel, err := filepath.Rel(root, dest)
	if err != nil || strings.HasPrefix(rel, "..") {
		return isSymlink(dest)
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if isSymlink(cur) {
			return true
		}
	}
	return false
}

// writeSkillText is skill_update.write_skill_text: refuse external
// symlinks, otherwise an atomic, mode-preserving write.
func writeSkillText(dest, content, root string) (bool, string, error) {
	if hasSymlinkComponent(dest, root) {
		target := resolvePath(dest)
		rr := resolvePath(root)
		if target != rr && !strings.HasPrefix(target, rr+string(filepath.Separator)) {
			rel, err := filepath.Rel(root, dest)
			if err != nil {
				rel = dest
			}
			return false, fmt.Sprintf("Skipped %s: symlinked path points outside repo (HOME-managed canonical copy)", rel), nil
		}
	}
	safe := resolvePath(dest)
	os.MkdirAll(filepath.Dir(safe), 0o755)
	var mode os.FileMode = 0o600
	existing := false
	if st, err := os.Stat(safe); err == nil && st.Mode().IsRegular() {
		mode, existing = st.Mode().Perm(), true
		os.Chmod(safe, mode|0o222)
	}
	tmp, err := os.CreateTemp(filepath.Dir(safe), "."+filepath.Base(safe)+".*.tmp")
	if err != nil {
		return false, "", err
	}
	tmp.WriteString(content)
	tmp.Close()
	if err := os.Rename(tmp.Name(), safe); err != nil {
		os.Remove(tmp.Name())
		if existing {
			os.Chmod(safe, mode)
		}
		return false, "", err
	}
	if existing {
		os.Chmod(safe, mode)
	}
	return true, "", nil
}

func skillMarkerReplacement(name string, markers []string, missing, upToDate string) migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range findSkillFiles(root, name, []string{"SKILL.md"}) {
				if fileContainsAny(f.path, markers) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			content, found := canonicalSkillText(name, "SKILL.md")
			if !found {
				return migrationResult{Errors: []string{missing}}
			}
			var ch, errs []string
			for _, f := range findSkillFiles(root, name, []string{"SKILL.md"}) {
				if !fileContainsAny(f.path, markers) {
					continue
				}
				rel, _ := filepath.Rel(root, f.path)
				if dryRun {
					ch = append(ch, "Would replace "+rel)
					continue
				}
				wrote, warning, err := writeSkillText(f.path, content, root)
				switch {
				case err != nil:
					errs = append(errs, fmt.Sprintf("Failed to write %s: %s", rel, pyOSError(err)))
				case wrote:
					ch = append(ch, "Replaced "+rel)
				case warning != "":
					ch = append(ch, warning)
				}
			}
			if len(ch) == 0 && len(errs) == 0 {
				ch = append(ch, upToDate)
			}
			return newResult(ch, nil, errs)
		},
	}
}

func skillInstall(name string, files []string, missing, already string) migrationImpl {
	return migrationImpl{
		detect:   func(root string) bool { return len(findSkillFiles(root, name, []string{"SKILL.md"})) == 0 },
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			contents := map[string]string{}
			for _, rel := range files {
				if c, found := canonicalSkillText(name, rel); found {
					contents[rel] = c
				}
			}
			if contents["SKILL.md"] == "" {
				return migrationResult{Errors: []string{missing}}
			}
			targets := []string{".claude/skills", ".agents/skills"}
			for _, r := range skillRoots {
				if isDir(filepath.Join(root, filepath.FromSlash(r))) && r != ".claude/skills" && r != ".agents/skills" {
					targets = append(targets, r)
				}
			}
			var ch, errs []string
			for _, r := range targets {
				for _, rel := range files {
					c := contents[rel]
					if c == "" {
						continue
					}
					dest := filepath.Join(root, filepath.FromSlash(r), name, filepath.FromSlash(rel))
					if pathExists(dest) {
						continue
					}
					disp, _ := filepath.Rel(root, dest)
					if dryRun {
						ch = append(ch, "Would install "+disp)
						continue
					}
					os.MkdirAll(filepath.Dir(dest), 0o755)
					if err := writeGeneratedFile(dest, []byte(c), true); err != nil {
						errs = append(errs, fmt.Sprintf("Failed to install %s: %s", disp, err))
						continue
					}
					ch = append(ch, "Installed "+disp)
				}
			}
			if len(ch) == 0 && len(errs) == 0 {
				ch = append(ch, already)
			}
			return newResult(ch, nil, errs)
		},
	}
}

func mRemoveReleaseSkill() migrationImpl {
	const rel = ".claude/skills/release"
	return migrationImpl{
		detect: func(root string) bool { return isDir(filepath.Join(root, rel)) },
		canApply: func(root string) (bool, string) {
			if isDir(filepath.Join(root, rel)) {
				return ok()
			}
			return false, "No release skill found — nothing to remove"
		},
		apply: func(root string, dryRun bool) migrationResult {
			dir := filepath.Join(root, rel)
			if !isDir(dir) {
				return newResult([]string{"No release skill found — nothing to do"}, nil, nil)
			}
			if dryRun {
				return newResult([]string{"Would remove " + rel + "/"}, nil, nil)
			}
			v, err := guardDestructiveRemoval(dir, root, manifestProver(true, true), true, filepath.Join(root, ".kittify"), false)
			if err != nil {
				return migrationResult{Errors: []string{pyOSError(err)}}
			}
			if v.owned {
				return newResult([]string{"Removed " + rel + "/"}, nil, nil)
			}
			r := newResult([]string{v.diagnostic}, []string{v.diagnostic}, nil)
			if v.preserved != "" {
				r.Preserved = []string{v.preserved}
			}
			return r
		},
	}
}

// --- 2.1.3 / 2.1.4 / 2.2.0: command files ------------------------------------

func commandFilesMatching(root string, pred func(content string) bool) ([]string, error) {
	dirs, err := migrationAgentDirs(root)
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, d := range dirs {
		cd := filepath.Join(root, d.root, d.subdir)
		if !isDir(cd) {
			continue
		}
		for _, f := range globFiles(cd, "spec-kitty.*.md") {
			b, err := os.ReadFile(f)
			if err == nil && pred(string(b)) {
				hits = append(hits, f)
			}
		}
	}
	return hits, nil
}

func mFixPlanningRepositoryTerminology() migrationImpl {
	needs := func(root string) ([]string, error) {
		return commandFilesMatching(root, func(c string) bool { return strings.Contains(strings.ToLower(c), "planning repository") })
	}
	terms := [][2]string{{"planning repository", "project root checkout"}, {"planning repo", "project root"}, {"Planning repository", "Project root checkout"}, {"Planning repo", "Project root"}}
	return migrationImpl{
		detectE: func(root string) (bool, error) { h, err := needs(root); return len(h) > 0, err },
		canApply: func(root string) (bool, string) {
			if h, _ := needs(root); len(h) > 0 {
				return ok()
			}
			return false, "No agent command files contain 'planning repository'"
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			hits, err := needs(root)
			if err != nil {
				return migrationResult{}, err
			}
			if len(hits) == 0 {
				return newResult([]string{"No files needed updating"}, nil, nil), nil
			}
			var changed []string
			for _, f := range hits {
				b, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				c := string(b)
				for _, t := range terms {
					c = strings.ReplaceAll(c, t[0], t[1])
				}
				if c != string(b) {
					if !dryRun {
						writeGeneratedFile(f, []byte(c), true)
					}
					rel, _ := filepath.Rel(root, f)
					changed = append(changed, rel)
				}
			}
			if len(changed) == 0 {
				changed = []string{"Updated 0 file(s): 'planning repository' → 'project root checkout'"}
			}
			return migrationResult{Success: true, Changes: changed}, nil
		},
	}
}

// commandTemplateFile is _resolve_template_path against the packaged
// mission-step templates (<command>/prompt.md).
func commandTemplateExists(command string) bool {
	return assets.Exists(agents.CommandTemplatesDir + "/" + command + "/prompt.md")
}

func renderFullPrompt(command, agentKey string) (string, bool) {
	if _, ok := agents.CommandConfigs[agentKey]; !ok {
		return "", false
	}
	tpl, err := agents.ReadTemplate(command)
	if err != nil {
		return "", false
	}
	out, err := agents.RenderCommandTemplate(tpl, agentKey, appVersion())
	if err != nil {
		return "", false
	}
	return out, true
}

// commandOutputFilename is _compute_output_filename.
func commandOutputFilename(command, agentKey string) string {
	cfg, ok := agents.CommandConfigs[agentKey]
	if !ok {
		return "spec-kitty." + command + ".md"
	}
	stem := command
	if agentKey == "codex" {
		stem = strings.ReplaceAll(stem, "-", "_")
	}
	if cfg.Ext != "" {
		return "spec-kitty." + stem + "." + cfg.Ext
	}
	return "spec-kitty." + stem
}

func isThinShim(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	n := 0
	for _, l := range pySplitLines(string(b)) {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n < 10 && strings.Contains(string(b), "spec-kitty agent shim")
}

func candidateShimPaths(agentDirPath, command, agentRoot string) []string {
	var c []string
	if key, ok := agents.AgentDirToKey[agentRoot]; ok {
		c = append(c, filepath.Join(agentDirPath, commandOutputFilename(command, key)))
	}
	legacy := filepath.Join(agentDirPath, "spec-kitty."+command+".md")
	if len(c) == 0 || c[0] != legacy {
		c = append(c, legacy)
	}
	return c
}

func sortedStrings(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

const runtimeTemplatesMissing = "Runtime command templates not found. Run 'spec-kitty upgrade' again after reinstalling spec-kitty-cli."

func mRestorePromptCommands() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				for _, cmd := range agents.PromptDrivenCommands {
					for _, c := range candidateShimPaths(ad, cmd, d.root) {
						if pathExists(c) && isThinShim(c) {
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn, errs []string
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				key, known := agents.AgentDirToKey[d.root]
				if !known {
					continue
				}
				for _, cmd := range sortedStrings(agents.PromptDrivenCommands) {
					if !commandTemplateExists(cmd) {
						warn = append(warn, fmt.Sprintf("Template not found for command '%s' in %s — skipping", cmd, agents.CommandTemplatesDir))
						continue
					}
					thin := ""
					for _, c := range candidateShimPaths(ad, cmd, d.root) {
						if pathExists(c) && isThinShim(c) {
							thin = c
							break
						}
					}
					if thin == "" {
						continue
					}
					out := filepath.Join(ad, commandOutputFilename(cmd, key))
					relShim, _ := filepath.Rel(root, thin)
					relOut, _ := filepath.Rel(root, out)
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would restore: %s → %s", relShim, relOut))
						continue
					}
					rendered, okr := renderFullPrompt(cmd, key)
					if !okr {
						errs = append(errs, fmt.Sprintf("Failed to render %s for %s", cmd, key))
						continue
					}
					if err := writeGeneratedFile(out, []byte(rendered), true); err != nil {
						errs = append(errs, fmt.Sprintf("Failed to write %s: %s", relOut, err))
						continue
					}
					if thin != out {
						if err := os.Remove(thin); err != nil {
							warn = append(warn, fmt.Sprintf("Could not remove stale shim %s: %s", relShim, pyOSError(err)))
							ch = append(ch, fmt.Sprintf("Restored: %s → %s", relShim, relOut))
						} else {
							ch = append(ch, fmt.Sprintf("Restored: %s → %s (removed stale shim)", relShim, relOut))
						}
					} else {
						ch = append(ch, "Restored: "+relOut)
					}
				}
			}
			if len(ch) == 0 && len(errs) == 0 && len(warn) == 0 {
				ch = append(ch, "No thin shims found for prompt-driven commands — nothing to do")
			}
			return newResult(ch, warn, errs), nil
		},
	}
}

func hasCurrentVersionMarker15(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	want := agents.VersionMarker(appVersion())
	lines := pySplitLines(string(b))
	if len(lines) > 15 {
		lines = lines[:15]
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

func mEnforceCommandFileState() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				key, known := agents.AgentDirToKey[d.root]
				if !known {
					continue
				}
				for _, cmd := range agents.ConsumerCommands {
					f := filepath.Join(ad, commandOutputFilename(cmd, key))
					if !pathExists(f) || !hasCurrentVersionMarker15(f) {
						return true, nil
					}
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn, errs []string
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !isDir(ad) {
					continue
				}
				key, known := agents.AgentDirToKey[d.root]
				if !known {
					continue
				}
				for _, cmd := range sortedStrings(agents.PromptDrivenCommands) {
					if !commandTemplateExists(cmd) {
						warn = append(warn, fmt.Sprintf("Template not found for command '%s' in %s — skipping", cmd, agents.CommandTemplatesDir))
						continue
					}
					out := filepath.Join(ad, commandOutputFilename(cmd, key))
					rel, _ := filepath.Rel(root, out)
					if dryRun {
						ch = append(ch, "Would write prompt: "+rel)
						continue
					}
					rendered, okr := renderFullPrompt(cmd, key)
					if !okr {
						errs = append(errs, fmt.Sprintf("Failed to render %s for %s", cmd, key))
						continue
					}
					if err := writeGeneratedFile(out, []byte(rendered), true); err != nil {
						errs = append(errs, fmt.Sprintf("Failed to write %s: %s", rel, err))
						continue
					}
					ch = append(ch, "Wrote prompt: "+rel)
				}
				for _, cmd := range sortedStrings(agents.CLIDrivenCommands) {
					out := filepath.Join(ad, commandOutputFilename(cmd, key))
					rel, _ := filepath.Rel(root, out)
					if dryRun {
						ch = append(ch, "Would write shim: "+rel)
						continue
					}
					content, err := agents.ShimContent(cmd, key, appVersion())
					if err == nil {
						err = writeGeneratedFile(out, []byte(content), true)
					}
					if err != nil {
						errs = append(errs, fmt.Sprintf("Failed to write %s: %s", rel, err))
						continue
					}
					ch = append(ch, "Wrote shim: "+rel)
				}
			}
			if len(ch) == 0 && len(errs) == 0 && len(warn) == 0 {
				ch = append(ch, "No agent command directories found — nothing to do")
			}
			return newResult(ch, warn, errs), nil
		},
	}
}

func mProfileContextDeployment() migrationImpl {
	const dest = "spec-kitty.profile-context.md"
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if pathExists(ad) && pathExists(filepath.Join(ad, dest)) {
					return true, nil
				}
			}
			return false, nil
		},
		canApply: func(string) (bool, string) { return ok() },
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn, errs, preserved []string
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				p := filepath.Join(ad, dest)
				if !pathExists(ad) || !pathExists(p) {
					continue
				}
				rel, _ := filepath.Rel(root, p)
				if dryRun {
					ch = append(ch, "Would remove retired "+rel)
					continue
				}
				v, err := guardDestructiveRemoval(p, root, defaultCanonicalProver(), false, "", false)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s: %s", rel, pyOSError(err)))
					continue
				}
				if v.owned {
					ch = append(ch, "Removed retired "+rel)
				} else {
					warn = append(warn, v.diagnostic)
					preserved = append(preserved, p)
				}
			}
			if len(ch) == 0 && len(errs) == 0 && len(preserved) == 0 {
				ch = append(ch, dest+" absent from all configured agent dirs")
			}
			r := newResult(ch, warn, errs)
			r.Preserved = preserved
			return r, nil
		},
	}
}

// --- 2.0.11 remove clarify -----------------------------------------------------

func clarifyTargets(root string) []string {
	var cands []string
	tr := filepath.Join(root, ".kittify", "templates")
	mr := filepath.Join(root, ".kittify", "missions")
	cands = append(cands, filepath.Join(tr, "command-templates", "clarify.md"))
	if pathExists(tr) {
		for _, d := range listDir(tr) {
			if strings.HasPrefix(filepath.Base(d), ".merged-") {
				if f := filepath.Join(d, "clarify.md"); lexists(f) {
					cands = append(cands, f)
				}
			}
		}
	}
	if pathExists(mr) {
		for _, m := range listDir(mr) {
			if f := filepath.Join(m, "command-templates", "clarify.md"); lexists(f) {
				cands = append(cands, f)
			}
		}
	}
	for _, d := range allAgentDirs() {
		ad := filepath.Join(root, d.root, d.subdir)
		if !pathExists(ad) {
			continue
		}
		for _, f := range globFiles(ad, "spec-kitty.clarify*") {
			if isFile(f) {
				cands = append(cands, f)
			}
		}
	}
	key := func(p string) string {
		if pathExists(p) {
			r, _ := filepath.Rel(root, p)
			return r
		}
		return p
	}
	sort.SliceStable(cands, func(i, j int) bool { return key(cands[i]) < key(cands[j]) })
	seen := map[string]bool{}
	var out []string
	for _, p := range cands {
		if !pathExists(p) {
			continue
		}
		r := resolvePath(p)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, p)
	}
	return out
}

func mRemoveClarifyCommand() migrationImpl {
	return migrationImpl{
		detect:   func(root string) bool { return len(clarifyTargets(root)) > 0 },
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			targets := clarifyTargets(root)
			if len(targets) == 0 {
				return newResult([]string{"No clarify command artifacts found"}, nil, nil)
			}
			var ch, warn, errs, preserved []string
			removed := 0
			for _, t := range targets {
				rel, _ := filepath.Rel(root, t)
				if dryRun {
					ch = append(ch, "Would remove: "+rel)
					continue
				}
				v, err := guardDestructiveRemoval(t, root, defaultCanonicalProver(), false, "", false)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove %s: %s", rel, pyOSError(err)))
					continue
				}
				if v.owned {
					removed++
					ch = append(ch, "Removed: "+rel)
				} else {
					warn = append(warn, v.diagnostic)
					preserved = append(preserved, t)
				}
			}
			if len(errs) == 0 {
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would remove %d clarify command artifacts", len(targets)))
				} else {
					ch = append(ch, fmt.Sprintf("Removed %d clarify command artifacts", removed))
				}
			}
			r := newResult(ch, warn, errs)
			r.Preserved = preserved
			return r
		},
	}
}
