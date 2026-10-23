package setup

// Ports of the 0.x upgrade migrations (specify_cli/upgrade/migrations/m_0_*).

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// migrationStdout receives what a migration prints (the research CSV
// report); upgrade points it at the command's stdout.
var migrationStdout io.Writer = io.Discard

// migrationStderr receives migration notices printed to stderr.
var migrationStderr io.Writer = io.Discard

// panelStderr prints Console(stderr=True).print(Panel(content, title=title,
// border_style=border)) to the migration stderr stream.
func panelStderr(content, title, border string) {
	for _, l := range rich.StyledPanelLines(rich.PanelSpec{
		Content: content, Title: title, BorderStyle: border, Width: rich.StderrWidth(),
		PadY: 0, PadX: 1, Wrap: rich.WrapWords,
	}, rich.ColorSystemOf(migrationStderr)) {
		fmt.Fprintln(migrationStderr, l)
	}
}

func ok() (bool, string) { return true, "" }

func newResult(changes, warnings, errs []string) migrationResult {
	return migrationResult{Success: len(errs) == 0, Changes: changes, Warnings: warnings, Errors: errs}
}

// neverNeeded is a migration whose detect() is always False.
func neverNeeded(reason string) migrationImpl {
	return migrationImpl{
		detect:   func(string) bool { return false },
		canApply: func(string) (bool, string) { return reason == "", reason },
	}
}

const wp10Reason = "Command templates were removed in WP10 (canonical context architecture). Shim generation replaces template-based commands."

func init() {
	migrationImpls["0.2.0_specify_to_kittify"] = mSpecifyToKittify()
	migrationImpls["0.4.8_gitignore_agents"] = mGitignoreAgents()
	migrationImpls["0.6.5_commands_rename"] = mCommandsRename()
	migrationImpls["0.6.7_ensure_missions"] = mEnsureMissions()
	migrationImpls["0.7.2_worktree_commands_dedup"] = mWorktreeCommandsDedup()
	migrationImpls["0.7.3_update_scripts"] = mUpdateScripts()
	migrationImpls["0.8.0_remove_active_mission"] = mRemoveActiveMission()
	migrationImpls["0.8.0_worktree_agents_symlink"] = mWorktreeAgentsSymlink()
	migrationImpls["0.9.0_frontmatter_only_lanes"] = mFrontmatterOnlyLanes()
	migrationImpls["0.9.1_complete_migration"] = mCompleteLaneMigration()
	migrationImpls["0.9.2_research_mission_templates"] = mResearchMissionTemplates()
	migrationImpls["0.10.0_python_only"] = mPythonOnly()
	migrationImpls["0.10.1_populate_slash_commands"] = mPopulateSlashCommands()
	migrationImpls["0.10.2_update_slash_commands"] = mUpdateSlashCommands()
	migrationImpls["0.10.6_workflow_simplification"] = mWorkflowSimplification()
	migrationImpls["0.10.8_fix_memory_structure"] = mFixMemoryStructure()
	migrationImpls["0.10.9_repair_templates"] = neverNeeded("WP10: Command templates were removed. Shim generation replaces template-based commands.")
	migrationImpls["0.10.12_charter_cleanup"] = neverNeeded("Superseded by charter-rename migration")
	migrationImpls["0.10.14_update_implement_slash_command"] = migrationImpl{
		detect: func(string) bool { return false },
		canApply: func(root string) (bool, string) {
			if pkgExists("missions/software-dev/command-templates/implement.md") ||
				pathExists(filepath.Join(root, ".kittify", "missions", "software-dev", "command-templates", "implement.md")) {
				return true, ""
			}
			return false, "Template not found in packaged missions or legacy location"
		},
	}
	migrationImpls["0.11.1_improved_workflow_templates"] = neverNeeded(wp10Reason)
	migrationImpls["0.11.1_update_implement_slash_command"] = neverNeeded(wp10Reason)
	migrationImpls["0.11.2_improved_workflow_templates"] = neverNeeded(wp10Reason)
	migrationImpls["0.11.3_workflow_agent_flag"] = mWorkflowAgentFlag()
	migrationImpls["0.12.0_documentation_mission"] = mDocumentationMission()
	migrationImpls["0.12.1_remove_kitty_specs_from_gitignore"] = mRemoveKittySpecsFromGitignore()
	migrationImpls["0.13.0_research_csv_schema_check"] = mResearchCSVSchemaCheck()
	migrationImpls["0.13.0_update_charter_templates"] = neverNeeded("Superseded by charter-rename migration")
	migrationImpls["0.13.0_update_research_implement_templates"] = neverNeeded(wp10Reason)
	migrationImpls["0.13.1_exclude_worktrees"] = mExcludeWorktrees()
	migrationImpls["0.13.5_add_commit_workflow_to_templates"] = neverNeeded(wp10Reason)
	migrationImpls["0.13.8_target_branch"] = mTargetBranch()
	migrationImpls["0.14.0_centralized_feature_detection"] = neverNeeded(wp10Reason)
}

// --- 0.2.0 ------------------------------------------------------------------

func mSpecifyToKittify() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool { return pathExists(filepath.Join(root, ".specify")) },
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".specify")) {
				return false, ".specify/ directory does not exist"
			}
			if pathExists(filepath.Join(root, ".kittify")) {
				return false, ".kittify/ already exists - manual merge required"
			}
			if pathExists(filepath.Join(root, "specs")) && pathExists(filepath.Join(root, "kitty-specs")) {
				return false, "Both /specs/ and /kitty-specs/ exist - manual merge required"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, errs []string
			sp, kd := filepath.Join(root, ".specify"), filepath.Join(root, ".kittify")
			ss, ks := filepath.Join(root, "specs"), filepath.Join(root, "kitty-specs")
			if pathExists(sp) {
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would rename %s to %s", sp, kd))
				} else if err := mvRename(sp, kd); err != nil {
					errs = append(errs, "Failed to rename .specify/ to .kittify/: "+pyOSError(err))
				} else {
					ch = append(ch, fmt.Sprintf("Renamed %s to %s", sp, kd))
				}
			}
			if pathExists(ss) && !pathExists(ks) {
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would rename %s to %s", ss, ks))
				} else if err := mvRename(ss, ks); err != nil {
					errs = append(errs, "Failed to rename /specs/ to /kitty-specs/: "+pyOSError(err))
				} else {
					ch = append(ch, fmt.Sprintf("Renamed %s to %s", ss, ks))
				}
			}
			return newResult(ch, nil, errs)
		},
	}
}

// pyOSError renders an OSError like Python's str(): "[Errno N] msg: 'path'".
func pyOSError(err error) string {
	var pe *os.PathError
	if e, ok := err.(*os.PathError); ok {
		pe = e
	} else if le, ok := err.(*os.LinkError); ok {
		return fmt.Sprintf("[Errno %d] %s: %s -> %s", errnoOf(le.Err), pyStrerror(le.Err), pyRepr(le.Old), pyRepr(le.New))
	}
	if pe != nil {
		return fmt.Sprintf("[Errno %d] %s: %s", errnoOf(pe.Err), pyStrerror(pe.Err), pyRepr(pe.Path))
	}
	return err.Error()
}

// --- 0.4.8 ------------------------------------------------------------------

var expectedAgentGitignore = []string{".claude/", ".codex/", ".opencode/", ".windsurf/", ".gemini/", ".cursor/", ".qwen/", ".kilocode/", ".augment/", ".roo/", ".amazonq/", ".github/copilot/"}

func mGitignoreAgents() migrationImpl {
	missing := func(content string) []string {
		var out []string
		for _, d := range expectedAgentGitignore {
			if !strings.Contains(content, d) {
				out = append(out, d)
			}
		}
		return out
	}
	return migrationImpl{
		detect: func(root string) bool {
			content, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
			if err != nil {
				return true
			}
			return len(missing(content)) > 0
		},
		canApply: func(root string) (bool, string) {
			p := filepath.Join(root, ".gitignore")
			if pathExists(p) {
				if _, err := os.ReadFile(p); err != nil {
					return false, ".gitignore is not readable"
				}
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			p := filepath.Join(root, ".gitignore")
			existing := ""
			if pathExists(p) {
				b, err := os.ReadFile(p)
				if err != nil {
					return migrationResult{Errors: []string{"Failed to read .gitignore: " + pyOSError(err)}}
				}
				existing = strings.TrimPrefix(decodeIgnoreErrors(b), "\ufeff")
			}
			m := missing(existing)
			if len(m) == 0 {
				return newResult([]string{"All agent directories already in .gitignore"}, nil, nil)
			}
			if dryRun {
				ch := []string{fmt.Sprintf("Would add %d agent directories to .gitignore", len(m))}
				for _, d := range m {
					ch = append(ch, "  - "+d)
				}
				return newResult(ch, nil, nil)
			}
			add := "\n# AI Agent directories (added by Spec Kitty CLI)\n# These contain auth tokens and should NEVER be committed\n"
			for _, d := range m {
				add += d + "\n"
			}
			if existing != "" && !strings.HasSuffix(existing, "\n") {
				existing += "\n"
			}
			var ch, errs []string
			if err := os.WriteFile(p, []byte(existing+add), 0o666&^umask()); err != nil {
				errs = append(errs, "Failed to write .gitignore: "+pyOSError(err))
			} else {
				ch = append(ch, fmt.Sprintf("Added %d agent directories to .gitignore", len(m)))
			}
			return newResult(ch, nil, errs)
		},
	}
}

// --- 0.6.5 ------------------------------------------------------------------

func mCommandsRename() migrationImpl {
	missionHasCommands := func(missions string) bool {
		for _, m := range subdirs(missions) {
			if pathExists(filepath.Join(m, "commands")) {
				return true
			}
		}
		return false
	}
	return migrationImpl{
		detect: func(root string) bool {
			k := filepath.Join(root, ".kittify")
			if pathExists(filepath.Join(k, "templates", "commands")) || missionHasCommands(filepath.Join(k, "missions")) {
				return true
			}
			for _, wt := range worktreeDirs(root) {
				wk := filepath.Join(wt, ".kittify")
				if pathExists(filepath.Join(wk, "templates", "commands")) || missionHasCommands(filepath.Join(wk, "missions")) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			k := filepath.Join(root, ".kittify")
			t := filepath.Join(k, "templates")
			if pathExists(t) && pathExists(filepath.Join(t, "commands")) && pathExists(filepath.Join(t, "command-templates")) {
				return false, "Both commands/ and command-templates/ exist in templates - manual merge required"
			}
			for _, m := range subdirs(filepath.Join(k, "missions")) {
				if pathExists(filepath.Join(m, "commands")) && pathExists(filepath.Join(m, "command-templates")) {
					return false, fmt.Sprintf("Both directories exist in mission %s - manual merge required", filepath.Base(m))
				}
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			rename := func(old, nw, context string) {
				if pathExists(old) && !pathExists(nw) {
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would rename %s: commands/ -> command-templates/", context))
					} else if err := mvRename(old, nw); err != nil {
						errs = append(errs, fmt.Sprintf("Failed to rename %s: %s", context, pyOSError(err)))
					} else {
						ch = append(ch, fmt.Sprintf("Renamed %s: commands/ -> command-templates/", context))
					}
				}
			}
			k := filepath.Join(root, ".kittify")
			if t := filepath.Join(k, "templates"); pathExists(t) {
				rename(filepath.Join(t, "commands"), filepath.Join(t, "command-templates"), ".kittify/templates")
			}
			for _, m := range subdirs(filepath.Join(k, "missions")) {
				rename(filepath.Join(m, "commands"), filepath.Join(m, "command-templates"), ".kittify/missions/"+filepath.Base(m))
			}
			for _, wt := range worktreeDirs(root) {
				name := filepath.Base(wt)
				wtc := filepath.Join(wt, ".kittify", "templates", "commands")
				if pathExists(wtc) {
					if dryRun {
						ch = append(ch, "Would remove old commands/ from worktree "+name)
					} else if _, err := removeToolOwnedTree(wtc, wtc, "worktree legacy templates/commands"); err != nil {
						warn = append(warn, fmt.Sprintf("Could not remove old commands/ from worktree %s: %s", name, err))
					} else {
						ch = append(ch, "Removed old commands/ from worktree "+name)
					}
				}
				for _, m := range subdirs(filepath.Join(wt, ".kittify", "missions")) {
					rename(filepath.Join(m, "commands"), filepath.Join(m, "command-templates"), fmt.Sprintf(".worktrees/%s/.kittify/missions/%s", name, filepath.Base(m)))
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 0.6.7 ------------------------------------------------------------------

var requiredMissions = []string{"software-dev", "research"}

func mEnsureMissions() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if usesCentralizedRuntime(root) {
				return false
			}
			md := filepath.Join(root, ".kittify", "missions")
			if !pathExists(md) {
				return true
			}
			for _, n := range requiredMissions {
				if !pathExists(filepath.Join(md, n)) || !pathExists(filepath.Join(md, n, "mission.yaml")) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) {
			var missing []string
			for _, n := range requiredMissions {
				if !pkgExists("missions/" + n) {
					missing = append(missing, n)
				}
			}
			if len(missing) > 0 {
				return false, fmt.Sprintf("Package is missing missions: %s. Please upgrade spec-kitty-cli to the latest version.", strings.Join(missing, ", "))
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			md := filepath.Join(root, ".kittify", "missions")
			if !pathExists(md) {
				if dryRun {
					ch = append(ch, "Would create .kittify/missions/ directory")
				} else {
					os.MkdirAll(md, 0o755)
					ch = append(ch, "Created .kittify/missions/ directory")
				}
			}
			for _, n := range requiredMissions {
				dest := filepath.Join(md, n)
				if pathExists(dest) {
					if pathExists(filepath.Join(dest, "mission.yaml")) {
						continue
					}
					if dryRun {
						ch = append(ch, "Would repair incomplete mission: "+n)
						continue
					}
					v, err := guardDestructiveRemoval(dest, root, defaultCanonicalProver(), true, filepath.Join(root, ".kittify"), false)
					if err == nil {
						if v.backupPath != "" {
							warn = append(warn, v.diagnostic)
						}
						err = copyPkgTree("missions/"+n, dest)
					}
					if err != nil {
						errs = append(errs, fmt.Sprintf("Failed to repair mission %s: %s", n, pyOSError(err)))
					} else {
						ch = append(ch, "Repaired incomplete mission: "+n)
					}
				} else if dryRun {
					ch = append(ch, "Would copy missing mission: "+n)
				} else if err := copyPkgTree("missions/"+n, dest); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to copy mission %s: %s", n, pyOSError(err)))
				} else {
					ch = append(ch, "Copied missing mission: "+n)
				}
			}
			for _, wt := range worktreeDirs(root) {
				wm := filepath.Join(wt, ".kittify", "missions")
				if !pathExists(wm) {
					continue
				}
				for _, n := range requiredMissions {
					dest := filepath.Join(wm, n)
					if pathExists(dest) {
						continue
					}
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would copy missing mission to worktree %s: %s", filepath.Base(wt), n))
					} else if err := copyPkgTree("missions/"+n, dest); err != nil {
						warn = append(warn, fmt.Sprintf("Could not copy mission to worktree %s: %s", filepath.Base(wt), pyOSError(err)))
					} else {
						ch = append(ch, fmt.Sprintf("Copied missing mission to worktree %s: %s", filepath.Base(wt), n))
					}
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 0.7.2 / 0.7.3 / 0.8.0 ---------------------------------------------------

func mWorktreeCommandsDedup() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, wt := range worktreeDirs(root) {
				if pathExists(filepath.Join(wt, ".claude", "commands")) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".claude", "commands")) {
				return false, "Main repo .claude/commands/ must exist before removing from worktrees"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, errs []string
			for _, wt := range worktreeDirs(root) {
				c := filepath.Join(wt, ".claude", "commands")
				if !pathExists(c) {
					continue
				}
				name := filepath.Base(wt)
				if dryRun {
					ch = append(ch, "Would remove .claude/commands/ from worktree "+name)
				} else if _, err := removeToolOwnedTree(c, c, "worktree .claude/commands dedup"); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to remove .claude/commands/ from %s: %s", name, err))
				} else {
					ch = append(ch, fmt.Sprintf("Removed .claude/commands/ from worktree %s (inherits from main repo)", name))
				}
			}
			return newResult(ch, nil, errs)
		},
	}
}

func mUpdateScripts() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			b, err := os.ReadFile(filepath.Join(root, ".kittify", "scripts", "bash", "create-new-feature.sh"))
			if err != nil {
				return false
			}
			return !strings.Contains(string(b), "Also scan .worktrees/")
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".kittify")) {
				return false, "No .kittify directory (not a spec-kitty project)"
			}
			return ok()
		},
		apply: func(string, bool) migrationResult {
			// The wheel ships no scripts/bash: always the informational no-op.
			return newResult(nil, []string{"Bash scripts not found in package (removed in later version or never existed). " +
				"If you need script updates, they may have been handled by migration 0.10.0 cleanup. This is not an error."}, nil)
		},
	}
}

func mRemoveActiveMission() migrationImpl {
	return migrationImpl{
		detect:   func(root string) bool { return lexists(filepath.Join(root, ".kittify", "active-mission")) },
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			p := filepath.Join(root, ".kittify", "active-mission")
			if lexists(p) {
				if dryRun {
					ch = append(ch, "Would remove .kittify/active-mission", "  -> Missions are now selected per-feature during /spec-kitty.specify")
				} else if err := os.Remove(p); err != nil {
					errs = append(errs, "Failed to remove .kittify/active-mission: "+pyOSError(err))
				} else {
					ch = append(ch, "Removed deprecated .kittify/active-mission", "  -> Missions are now selected per-feature during /spec-kitty.specify",
						"  -> Existing features will use 'software-dev' mission by default")
				}
			} else {
				warn = append(warn, "No .kittify/active-mission found (already migrated or new project)")
			}
			return newResult(ch, warn, errs)
		},
	}
}

func mWorktreeAgentsSymlink() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if !pathExists(filepath.Join(root, ".kittify", "AGENTS.md")) {
				return false
			}
			for _, wt := range worktreeDirs(root) {
				if !strings.HasPrefix(filepath.Base(wt), ".") && !pathExists(filepath.Join(wt, ".kittify", "AGENTS.md")) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".kittify", "AGENTS.md")) {
				return false, "Main repo .kittify/AGENTS.md must exist before creating symlinks"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			main := filepath.Join(root, ".kittify", "AGENTS.md")
			if !pathExists(main) {
				return newResult(nil, []string{"Main repo .kittify/AGENTS.md not found, skipping"}, nil)
			}
			for _, wt := range worktreeDirs(root) {
				name := filepath.Base(wt)
				if strings.HasPrefix(name, ".") {
					continue
				}
				wk := filepath.Join(wt, ".kittify")
				wa := filepath.Join(wk, "AGENTS.md")
				if pathExists(wa) && !isSymlink(wa) {
					warn = append(warn, fmt.Sprintf("Worktree %s has non-symlink AGENTS.md, skipping", name))
					continue
				}
				if isSymlink(wa) && pathExists(wa) {
					continue
				}
				if dryRun {
					ch = append(ch, "Would create .kittify/AGENTS.md symlink in worktree "+name)
					continue
				}
				os.MkdirAll(wk, 0o755)
				if isSymlink(wa) {
					os.Remove(wa)
				}
				if err := os.Symlink("../../../.kittify/AGENTS.md", wa); err != nil {
					if cerr := copyFile2(main, wa); cerr != nil {
						errs = append(errs, fmt.Sprintf("Failed to create AGENTS.md in %s: %s, copy also failed: %s", name, pyOSError(err), pyOSError(cerr)))
					} else {
						ch = append(ch, fmt.Sprintf("Copied .kittify/AGENTS.md to worktree %s (symlink failed)", name))
					}
				} else {
					ch = append(ch, "Created .kittify/AGENTS.md symlink in worktree "+name)
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 0.9.0 / 0.9.1: lane directories ---------------------------------------

var laneDirs = []string{"planned", "doing", "for_review", "done"}

var laneIgnoreFiles = map[string]bool{".gitkeep": true, ".DS_Store": true, "Thumbs.db": true, "desktop.ini": true, ".directory": true, "._*": true}

func laneShouldIgnore(name string) bool {
	return laneIgnoreFiles[name] || strings.HasPrefix(name, "._")
}

func laneRealContents(dir string) []string {
	if !isDir(dir) {
		return nil
	}
	var out []string
	for _, c := range listDir(dir) {
		if !laneShouldIgnore(filepath.Base(c)) {
			out = append(out, c)
		}
	}
	return out
}

func hasLaneDirs(feature string) bool {
	tasks := filepath.Join(feature, "tasks")
	if !pathExists(tasks) {
		return false
	}
	for _, l := range laneDirs {
		lp := filepath.Join(tasks, l)
		if isDir(lp) && (len(laneRealContents(lp)) > 0 || len(listDir(lp)) > 0) {
			return true
		}
	}
	return false
}

type laneFeature struct{ dir, label string }

func featuresWithLanes(root string) []laneFeature {
	var out []laneFeature
	for _, f := range subdirs(filepath.Join(root, "kitty-specs")) {
		if hasLaneDirs(f) {
			out = append(out, laneFeature{f, "main"})
		}
	}
	for _, wt := range worktreeDirs(root) {
		for _, f := range subdirs(filepath.Join(wt, "kitty-specs")) {
			if hasLaneDirs(f) {
				out = append(out, laneFeature{f, "worktree:" + filepath.Base(wt)})
			}
		}
	}
	return out
}

var laneLineRe = regexp.MustCompile(`^lane:\s*(.*)$`)

// ensureLaneInFrontmatter is FrontmatterOnlyLanesMigration._ensure_lane_in_frontmatter.
func ensureLaneInFrontmatter(content, lane string) string {
	if !strings.HasPrefix(content, "---") {
		return fmt.Sprintf("---\nlane: \"%s\"\n---\n%s", lane, content)
	}
	lines := strings.Split(content, "\n")
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing == -1 {
		return fmt.Sprintf("---\nlane: \"%s\"\n---\n%s", lane, content)
	}
	var updated []string
	found := false
	for _, l := range lines[1:closing] {
		if m := laneLineRe.FindStringSubmatch(l); m != nil {
			found = true
			cur := strings.Trim(strings.TrimSpace(m[1]), "\"'")
			if cur != lane {
				updated = append(updated, fmt.Sprintf("lane: \"%s\"", lane))
			} else {
				updated = append(updated, l)
			}
		} else {
			updated = append(updated, l)
		}
	}
	if !found {
		idx := len(updated)
		for i, l := range updated {
			if strings.HasPrefix(l, "history:") {
				idx = i
				break
			}
		}
		updated = append(updated[:idx], append([]string{fmt.Sprintf("lane: \"%s\"", lane)}, updated[idx:]...)...)
	}
	out := append([]string{"---"}, updated...)
	out = append(out, "---")
	out = append(out, lines[closing+1:]...)
	return strings.Join(out, "\n")
}

func mFrontmatterOnlyLanes() migrationImpl {
	isLegacy := func(feature string) bool { return hasLaneDirs(feature) }
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range subdirs(filepath.Join(root, "kitty-specs")) {
				if isLegacy(f) {
					return true
				}
			}
			for _, wt := range worktreeDirs(root) {
				for _, f := range subdirs(filepath.Join(wt, "kitty-specs")) {
					if isLegacy(f) {
						return true
					}
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			features := featuresWithLanes(root)
			if len(features) == 0 {
				return newResult(nil, []string{"No features need migration - all already use flat structure"}, nil)
			}
			migrated, skipped := 0, 0
			for _, f := range features {
				tasks := filepath.Join(f.dir, "tasks")
				if !pathExists(tasks) {
					continue
				}
				ch = append(ch, fmt.Sprintf("[%s] %s:", f.label, filepath.Base(f.dir)))
				for _, lane := range laneDirs {
					ld := filepath.Join(tasks, lane)
					if !isDir(ld) {
						continue
					}
					for _, md := range globFiles(ld, "*.md") {
						name := filepath.Base(md)
						if name == "README.md" {
							continue
						}
						target := filepath.Join(tasks, name)
						if pathExists(target) {
							warn = append(warn, fmt.Sprintf("  Skip: %s already exists in tasks/", name))
							skipped++
							continue
						}
						if dryRun {
							ch = append(ch, fmt.Sprintf("  Would move: %s/%s → tasks/%s", lane, name, name))
						} else {
							content, err := readTextSig(md)
							if err == nil {
								err = os.WriteFile(target, []byte(ensureLaneInFrontmatter(content, lane)), 0o666&^umask())
							}
							if err == nil {
								err = os.Remove(md)
							}
							if err != nil {
								errs = append(errs, fmt.Sprintf("  Error migrating %s: %s", name, pyOSError(err)))
								continue
							}
							ch = append(ch, fmt.Sprintf("  Moved: %s/%s → tasks/%s", lane, name, name))
						}
						migrated++
					}
				}
				for _, lane := range laneDirs {
					ld := filepath.Join(tasks, lane)
					if !isDir(ld) || len(laneRealContents(ld)) > 0 {
						continue
					}
					if dryRun {
						ch = append(ch, fmt.Sprintf("  Would remove empty: %s/", lane))
					} else if _, err := removeToolOwnedTree(ld, ld, "empty lane directory (system files only)"); err != nil {
						warn = append(warn, fmt.Sprintf("  Could not remove %s/: %s", lane, err))
					} else {
						ch = append(ch, fmt.Sprintf("  Removed empty: %s/", lane))
					}
				}
			}
			verb := "Migrated"
			if dryRun {
				verb = "Would migrate"
			}
			ch = append(ch, fmt.Sprintf("%s %d WP files across %d features", verb, migrated, len(features)))
			if skipped > 0 {
				warn = append(warn, fmt.Sprintf("Skipped %d files (already exist or conflicts)", skipped))
			}
			return newResult(ch, warn, errs)
		},
	}
}

func mCompleteLaneMigration() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range subdirs(filepath.Join(root, "kitty-specs")) {
				if hasLaneDirs(f) {
					return true
				}
			}
			for _, wt := range worktreeDirs(root) {
				for _, f := range subdirs(filepath.Join(wt, "kitty-specs")) {
					if hasLaneDirs(f) {
						return true
					}
				}
				for _, d := range allAgentDirs() {
					if pathExists(filepath.Join(wt, d.root, d.subdir)) {
						return true
					}
				}
				if pathExists(filepath.Join(wt, ".kittify", "scripts")) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			ch = append(ch, "=== Part 1: Complete Lane Migration ===")
			features := featuresWithLanes(root)
			if len(features) > 0 {
				migrated, removed := 0, 0
				for _, f := range features {
					tasks := filepath.Join(f.dir, "tasks")
					if !pathExists(tasks) {
						continue
					}
					ch = append(ch, fmt.Sprintf("[%s] %s:", f.label, filepath.Base(f.dir)))
					for _, lane := range laneDirs {
						ld := filepath.Join(tasks, lane)
						if !isDir(ld) {
							continue
						}
						for _, item := range listDir(ld) {
							name := filepath.Base(item)
							if name == ".gitkeep" {
								continue
							}
							if isFile(item) {
								target := filepath.Join(tasks, name)
								if pathExists(target) {
									warn = append(warn, fmt.Sprintf("  Skip: %s already exists in tasks/", name))
									continue
								}
								if dryRun {
									ch = append(ch, fmt.Sprintf("  Would move: %s/%s → tasks/%s", lane, name, name))
								} else {
									var err error
									if strings.HasSuffix(name, ".md") {
										var content string
										content, err = readTextSig(item)
										if err == nil {
											err = os.WriteFile(target, []byte(ensureLaneInFrontmatter(content, lane)), 0o666&^umask())
										}
									} else {
										var b []byte
										b, err = os.ReadFile(item)
										if err == nil {
											err = os.WriteFile(target, b, 0o666&^umask())
										}
									}
									if err == nil {
										err = os.Remove(item)
									}
									if err != nil {
										errs = append(errs, fmt.Sprintf("  Error migrating %s/%s: %s", lane, name, pyOSError(err)))
										continue
									}
									ch = append(ch, fmt.Sprintf("  Moved: %s/%s → tasks/%s", lane, name, name))
								}
								migrated++
							} else if isDir(item) {
								warn = append(warn, fmt.Sprintf("  Warning: Nested directory %s/%s/ found - please check manually", lane, name))
							}
						}
						if !isDir(ld) || len(laneRealContents(ld)) > 0 {
							continue
						}
						if dryRun {
							ch = append(ch, fmt.Sprintf("  Would remove: %s/", lane))
							removed++
						} else if _, err := removeToolOwnedTree(ld, ld, "empty lane directory (system files only)"); err != nil {
							warn = append(warn, fmt.Sprintf("  Could not remove %s/: %s", lane, err))
						} else {
							ch = append(ch, fmt.Sprintf("  Removed: %s/", lane))
							removed++
						}
					}
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would migrate %d files and remove %d lane directories", migrated, removed))
				} else {
					ch = append(ch, fmt.Sprintf("Migrated %d files and removed %d lane directories", migrated, removed))
				}
			} else {
				ch = append(ch, "No lane subdirectories found")
			}
			ch = append(ch, "", "=== Part 2: Worktree Cleanup ===")
			wc, we := cleanupWorktreeAgentDirs(root, dryRun)
			ch = append(ch, wc...)
			errs = append(errs, we...)
			ch = append(ch, "", "=== Part 3: Normalize Frontmatter ===")
			fc, fw, fe := normalizeAllFrontmatter(root, dryRun)
			ch = append(ch, fc...)
			warn = append(warn, fw...)
			errs = append(errs, fe...)
			return newResult(ch, warn, errs)
		},
	}
}

func cleanupWorktreeAgentDirs(root string, dryRun bool) ([]string, []string) {
	var ch, errs []string
	wtDir := filepath.Join(root, ".worktrees")
	if !pathExists(wtDir) {
		return []string{"No .worktrees/ directory found"}, nil
	}
	cleaned := 0
	for _, wt := range worktreeDirs(root) {
		name := filepath.Base(wt)
		did := false
		for _, d := range allAgentDirs() {
			cd := filepath.Join(wt, d.root, d.subdir)
			if !lexists(cd) {
				continue
			}
			if dryRun {
				kind := "directory"
				if isSymlink(cd) {
					kind = "symlink"
				}
				ch = append(ch, fmt.Sprintf("[%s] Would remove %s/%s/ (%s)", name, d.root, d.subdir, kind))
				continue
			}
			var err error
			if isSymlink(cd) {
				err = os.Remove(cd)
				if err == nil {
					ch = append(ch, fmt.Sprintf("[%s] Removed %s/%s/ symlink (inherits from main)", name, d.root, d.subdir))
				}
			} else if isDir(cd) {
				_, err = removeToolOwnedTree(cd, cd, "worktree agent commands dir")
				if err == nil {
					ch = append(ch, fmt.Sprintf("[%s] Removed %s/%s/ (inherits from main)", name, d.root, d.subdir))
				}
			}
			if err == nil {
				parent := filepath.Dir(cd)
				if pathExists(parent) && len(listDir(parent)) == 0 {
					os.Remove(parent)
				}
				did = true
			} else {
				errs = append(errs, fmt.Sprintf("[%s] Failed to remove %s/%s/: %s", name, d.root, d.subdir, err))
			}
		}
		sd := filepath.Join(wt, ".kittify", "scripts")
		if lexists(sd) {
			if dryRun {
				kind := "directory"
				if isSymlink(sd) {
					kind = "symlink"
				}
				ch = append(ch, fmt.Sprintf("[%s] Would remove .kittify/scripts/ (%s)", name, kind))
			} else {
				var err error
				if isSymlink(sd) {
					if err = os.Remove(sd); err == nil {
						ch = append(ch, fmt.Sprintf("[%s] Removed .kittify/scripts/ symlink (inherits from main)", name))
					}
				} else if isDir(sd) {
					if _, err = removeToolOwnedTree(sd, sd, "worktree .kittify/scripts"); err == nil {
						ch = append(ch, fmt.Sprintf("[%s] Removed .kittify/scripts/ (inherits from main)", name))
					}
				}
				if err == nil {
					did = true
				} else {
					errs = append(errs, fmt.Sprintf("[%s] Failed to remove .kittify/scripts/: %s", name, err))
				}
			}
		}
		if did {
			cleaned++
		}
	}
	if cleaned > 0 {
		if dryRun {
			ch = append(ch, fmt.Sprintf("Would clean up %d worktrees", cleaned))
		} else {
			ch = append(ch, fmt.Sprintf("Cleaned up %d worktrees", cleaned))
		}
	} else {
		ch = append(ch, "No worktrees needed cleanup")
	}
	return ch, errs
}

func rglobMD(dir string) []string {
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(info.Name(), ".md") && p != dir {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// frontmatterRead is FrontmatterManager.read (errors carry upstream text).
func frontmatterRead(p string) (*jsonx.Object, string, string, error) {
	content, err := readTextSig(p)
	if err != nil {
		return nil, "", "", fmt.Errorf("File not found: %s", p)
	}
	if !strings.HasPrefix(content, "---") {
		return nil, "", content, fmt.Errorf("File has no frontmatter: %s", p)
	}
	lines := strings.Split(content, "\n")
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r\n\v\f") == "---" {
			closing = i
			break
		}
	}
	if closing == -1 {
		return nil, "", content, fmt.Errorf("Malformed frontmatter (no closing ---): %s", p)
	}
	doc, perr := fm.Parse(content)
	if perr != nil || doc == nil {
		return nil, "", content, fmt.Errorf("Invalid YAML in %s: %v", p, perr)
	}
	front := doc.Meta
	if front == nil {
		front = &jsonx.Object{}
	}
	if strings.HasPrefix(filepath.Base(p), "WP") && !front.Has("dependencies") {
		front.Set("dependencies", []any{})
	}
	body := strings.Join(lines[closing+1:], "\n")
	return front, body, content, nil
}

func normalizeAllFrontmatter(root string, dryRun bool) ([]string, []string, []string) {
	var ch, warn, errs []string
	var files []string
	if ks := filepath.Join(root, "kitty-specs"); pathExists(ks) {
		files = append(files, rglobMD(ks)...)
	}
	for _, wt := range worktreeDirs(root) {
		if ws := filepath.Join(wt, "kitty-specs"); pathExists(ws) {
			files = append(files, rglobMD(ws)...)
		}
	}
	if len(files) == 0 {
		return []string{"No markdown files found"}, nil, nil
	}
	norm, skipped := 0, 0
	for _, p := range sortedPaths(files) {
		parts := strings.Split(filepath.ToSlash(p), "/")
		inTasks := false
		for _, x := range parts {
			inTasks = inTasks || x == "tasks"
		}
		if !inTasks {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		front, body, original, err := frontmatterRead(p)
		if err != nil {
			if dryRun {
				warn = append(warn, "Skip (no frontmatter): "+rel)
			}
			skipped++
			continue
		}
		rendered := renderFrontmatter(front, body, original)
		if original == rendered {
			skipped++
			continue
		}
		if dryRun {
			ch = append(ch, "Would normalize: "+rel)
		} else {
			if err := os.WriteFile(p, []byte(rendered), 0o666&^umask()); err != nil {
				errs = append(errs, fmt.Sprintf("Failed to normalize %s: %s", rel, pyOSError(err)))
				continue
			}
			ch = append(ch, "Normalized: "+rel)
		}
		norm++
	}
	if dryRun {
		ch = append(ch, fmt.Sprintf("Would normalize %d files, skip %d", norm, skipped))
	} else {
		ch = append(ch, fmt.Sprintf("Normalized %d files, skipped %d", norm, skipped))
	}
	return ch, warn, errs
}

// sortedPaths sorts like sorted(list[Path]) (component-wise).
func sortedPaths(ps []string) []string {
	out := append([]string(nil), ps...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && pathLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func pathLess(a, b string) bool {
	pa, pb := strings.Split(filepath.ToSlash(a), "/"), strings.Split(filepath.ToSlash(b), "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// --- 0.9.2 ------------------------------------------------------------------

func mResearchMissionTemplates() migrationImpl {
	required := func() [][2]string {
		r := [][2]string{{"templates", "task-prompt-template.md"}}
		if pkgExists("missions/research/command-templates/tasks.md") {
			r = append(r, [2]string{"command-templates", "tasks.md"})
		}
		return r
	}
	return migrationImpl{
		detect: func(root string) bool {
			rm := filepath.Join(root, ".kittify", "missions", "research")
			if !pathExists(rm) {
				return false
			}
			for _, f := range required() {
				if !pathExists(filepath.Join(rm, f[0], f[1])) {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) {
			var missing []string
			for _, f := range required() {
				if !pkgExists("missions/research/" + f[0] + "/" + f[1]) {
					missing = append(missing, f[0]+"/"+f[1])
				}
			}
			if len(missing) > 0 {
				return false, fmt.Sprintf("Package research mission is missing: %s. Please upgrade spec-kitty-cli to the latest version.", strings.Join(missing, ", "))
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			rm := filepath.Join(root, ".kittify", "missions", "research")
			if !pathExists(rm) {
				return newResult([]string{"Research mission not present, skipping"}, nil, nil)
			}
			for _, f := range required() {
				dest := filepath.Join(rm, f[0], f[1])
				if pathExists(dest) {
					continue
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would add research/%s/%s", f[0], f[1]))
					continue
				}
				os.MkdirAll(filepath.Dir(dest), 0o755)
				if err := copyPkgFile("missions/research/"+f[0]+"/"+f[1], dest); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to copy %s/%s: %s", f[0], f[1], pyOSError(err)))
				} else {
					ch = append(ch, fmt.Sprintf("Added research/%s/%s", f[0], f[1]))
				}
			}
			for _, wt := range worktreeDirs(root) {
				wr := filepath.Join(wt, ".kittify", "missions", "research")
				if !pathExists(wr) {
					continue
				}
				for _, f := range required() {
					dest := filepath.Join(wr, f[0], f[1])
					if pathExists(dest) {
						continue
					}
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would add research/%s/%s to worktree %s", f[0], f[1], filepath.Base(wt)))
						continue
					}
					os.MkdirAll(filepath.Dir(dest), 0o755)
					if err := copyPkgFile("missions/research/"+f[0]+"/"+f[1], dest); err != nil {
						warn = append(warn, fmt.Sprintf("Could not copy to worktree %s: %s", filepath.Base(wt), pyOSError(err)))
					} else {
						ch = append(ch, fmt.Sprintf("Added research/%s/%s to worktree %s", f[0], f[1], filepath.Base(wt)))
					}
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 0.10.0 -----------------------------------------------------------------

var pythonOnlyReplacements = func() [][2]string {
	var out [][2]string
	for _, s := range []struct{ script, cmd string }{
		{"create-new-feature", "spec-kitty agent mission create-feature"},
		{"check-prerequisites", "spec-kitty agent mission check-prerequisites"},
		{"setup-plan", "spec-kitty agent setup-plan"},
		{"update-agent-context", "spec-kitty agent update-context"},
		{"accept-feature", "spec-kitty agent mission accept"},
		{"merge-feature", "spec-kitty agent mission merge"},
		{"tasks-move-to-lane", "spec-kitty agent move-task"},
		{"tasks-list-lanes", "spec-kitty agent list-tasks"},
		{"mark-task-status", "spec-kitty agent mark-status"},
		{"tasks-add-history-entry", "spec-kitty agent add-history"},
		{"tasks-rollback-move", "spec-kitty agent rollback-move"},
		{"validate-task-workflow", "spec-kitty agent validate-workflow"},
		{"move-task-to-doing", "spec-kitty agent move-task"},
	} {
		esc := strings.ReplaceAll(s.script, "-", "-")
		out = append(out, [2]string{`\.kittify/scripts/bash/` + esc + `\.sh`, s.cmd}, [2]string{`scripts/bash/` + esc + `\.sh`, s.cmd})
	}
	for _, s := range [][2]string{{"move", "spec-kitty agent move-task"}, {"list", "spec-kitty agent list-tasks"}, {"mark", "spec-kitty agent mark-status"},
		{"history", "spec-kitty agent add-history"}, {"rollback", "spec-kitty agent rollback-move"}, {"validate", "spec-kitty agent validate-workflow"}} {
		out = append(out, [2]string{`tasks_cli\.py ` + s[0], s[1]})
	}
	return out
}()

func mPythonOnly() migrationImpl {
	preserve := func(scripts []string, root string, dryRun bool, ch, warn *[]string) int {
		preserved := 0
		for _, s := range scripts {
			v, err := guardDestructiveRemoval(s, root, defaultCanonicalProver(), false, "", dryRun)
			if err != nil {
				*warn = append(*warn, pyOSError(err))
				continue
			}
			if v.owned {
				*ch = append(*ch, v.diagnostic)
			} else {
				preserved++
				*warn = append(*warn, v.diagnostic)
			}
		}
		return preserved
	}
	return migrationImpl{
		detect: func(root string) bool {
			return len(globFiles(filepath.Join(root, ".kittify", "scripts", "bash"), "*.sh")) > 0
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			bash := filepath.Join(root, ".kittify", "scripts", "bash")
			if !pathExists(bash) {
				warn = append(warn, "No .kittify/scripts/bash/ directory found - already migrated?")
			} else {
				preserved := preserve(globFiles(bash, "*.sh"), root, dryRun, &ch, &warn)
				ps := filepath.Join(root, ".kittify", "scripts", "powershell")
				if pathExists(ps) {
					preserved += preserve(globFiles(ps, "*.ps1"), root, dryRun, &ch, &warn)
				}
				if !dryRun {
					if pathExists(bash) && len(listDir(bash)) == 0 {
						os.Remove(bash)
						ch = append(ch, "Removed empty: .kittify/scripts/bash/")
					}
					if pathExists(ps) && len(listDir(ps)) == 0 {
						os.Remove(ps)
						ch = append(ch, "Removed empty: .kittify/scripts/powershell/")
					}
				}
				if preserved > 0 {
					warn = append(warn, fmt.Sprintf("Preserved %d unprovable script(s); left in place (no ownership signal)", preserved))
				} else {
					warn = append(warn, "No bash scripts found to remove - already migrated?")
				}
			}
			for _, wt := range worktreeDirs(root) {
				wb := filepath.Join(wt, ".kittify", "scripts", "bash")
				if !pathExists(wb) {
					continue
				}
				preserved := preserve(globFiles(wb, "*.sh"), root, dryRun, &ch, &warn)
				if !dryRun && pathExists(wb) && len(listDir(wb)) == 0 {
					os.Remove(wb)
				}
				if preserved > 0 {
					warn = append(warn, fmt.Sprintf("Preserved %d unprovable script(s) in worktree: %s", preserved, filepath.Base(wt)))
				}
			}
			if td := filepath.Join(root, ".kittify", "scripts", "tasks"); pathExists(td) {
				v, err := guardDestructiveRemoval(td, root, defaultCanonicalProver(), true, "", dryRun)
				if err == nil {
					if v.owned {
						ch = append(ch, v.diagnostic)
					} else {
						warn = append(warn, v.diagnostic)
					}
				}
			}
			tpl := filepath.Join(root, ".kittify", "templates", "command-templates")
			if !pathExists(tpl) {
				ch = append(ch, "Templates directory not found at .kittify/templates/command-templates/. "+
					"This is expected for projects initialized with older package versions. "+
					"Run 'spec-kitty upgrade' after upgrading to v0.10.9+ to repair templates.")
			} else {
				updated := 0
				for _, t := range globFiles(tpl, "*.md") {
					b, err := os.ReadFile(t)
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error updating %s: %s", filepath.Base(t), pyOSError(err)))
						continue
					}
					content, n := string(b), 0
					for _, r := range pythonOnlyReplacements {
						re := regexp.MustCompile(r[0])
						c := len(re.FindAllStringIndex(content, -1))
						if c > 0 {
							content = re.ReplaceAllLiteralString(content, r[1])
							n += c
						}
					}
					if content == string(b) {
						continue
					}
					if !dryRun {
						if err := writeGeneratedFile(t, []byte(content), true); err != nil {
							errs = append(errs, fmt.Sprintf("Error updating %s: %s", filepath.Base(t), err))
							continue
						}
					}
					updated++
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would update: %s (%d replacements)", filepath.Base(t), n))
					} else {
						ch = append(ch, fmt.Sprintf("Updated: %s (%d replacements)", filepath.Base(t), n))
					}
				}
				if updated > 0 {
					ch = append(ch, fmt.Sprintf("Total templates updated: %d", updated))
				}
			}
			return newResult(ch, warn, errs)
		},
	}
}

// --- 0.10.1 / 0.10.2 / 0.10.6: slash commands --------------------------------

// missionCommandTemplates finds .kittify/missions/<m>/command-templates
// (software-dev first, then the first mission in name order).
func missionCommandTemplates(root string) (string, string) {
	md := filepath.Join(root, ".kittify", "missions")
	if sd := filepath.Join(md, "software-dev", "command-templates"); pathExists(sd) {
		return sd, "software-dev"
	}
	for _, m := range subdirs(md) {
		if t := filepath.Join(m, "command-templates"); pathExists(t) {
			return t, filepath.Base(m)
		}
	}
	return "", ""
}

func missionTemplatesCanApply(root string) (bool, string) {
	md := filepath.Join(root, ".kittify", "missions")
	if !pathExists(md) {
		return false, "No missions directory found"
	}
	if t, _ := missionCommandTemplates(root); t != "" {
		return ok()
	}
	return false, "No mission command templates found"
}

func templateStem(p string) string { return strings.TrimSuffix(filepath.Base(p), ".md") }

func mPopulateSlashCommands() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			cc := filepath.Join(root, ".claude", "commands")
			if !pathExists(cc) {
				return true
			}
			return len(globFiles(cc, "spec-kitty.*.md")) < 8
		},
		canApply: missionTemplatesCanApply,
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch []string
			tpl, mission := missionCommandTemplates(root)
			if tpl == "" {
				return migrationResult{Errors: []string{"No mission command templates found"}}, nil
			}
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				out := filepath.Join(root, d.root, d.subdir)
				if !pathExists(filepath.Dir(out)) {
					continue
				}
				if !pathExists(out) {
					if dryRun {
						ch = append(ch, "Would create: "+out)
					} else {
						os.MkdirAll(out, 0o755)
						ch = append(ch, "Created: "+out)
					}
				}
				created := 0
				for _, t := range globFiles(tpl, "*.md") {
					dest := filepath.Join(out, "spec-kitty."+templateStem(t)+".md")
					if pathExists(dest) {
						continue
					}
					if dryRun {
						ch = append(ch, "Would create: "+filepath.Base(dest))
					} else {
						b, _ := os.ReadFile(t)
						writeGeneratedFile(dest, b, true)
						ch = append(ch, "Created: "+filepath.Base(dest))
					}
					created++
				}
				if created > 0 {
					ch = append(ch, fmt.Sprintf("Created %d slash commands for %s from %s", created, strings.Trim(d.root, "."), mission))
				}
			}
			return newResult(ch, nil, nil), nil
		},
	}
}

func mUpdateSlashCommands() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				for _, f := range globFiles(ad, "spec-kitty.*.md") {
					b, _ := os.ReadFile(f)
					c := string(b)
					if strings.Contains(c, ".kittify/scripts/bash/") || strings.Contains(c, "scripts/bash/") ||
						strings.Contains(c, ".kittify/scripts/powershell/") || strings.Contains(c, "scripts/powershell/") {
						return true, nil
					}
					if strings.Contains(c, "tasks/planned/") || strings.Contains(c, "tasks/doing/") {
						if !strings.Contains(c, "WRONG") || strings.Count(c, "tasks/planned/") > 2 {
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
		canApply: missionTemplatesCanApply,
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn []string
			tpl, mission := missionCommandTemplates(root)
			if tpl == "" {
				return migrationResult{Errors: []string{"No mission command templates found"}}, nil
			}
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			total := 0
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				n := 0
				for _, t := range globFiles(tpl, "*.md") {
					dest := filepath.Join(ad, "spec-kitty."+templateStem(t)+".md")
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would update %s: %s", d.root, filepath.Base(dest)))
						continue
					}
					b, _ := os.ReadFile(t)
					v := guardDestructiveOverwrite(dest, root, strings.TrimSpace(string(b)) != "", false, defaultCanonicalProver(), "")
					if !v.proceed {
						warn = append(warn, v.diagnostic)
						continue
					}
					writeGeneratedFile(dest, b, true)
					n++
				}
				if n > 0 {
					ch = append(ch, fmt.Sprintf("Updated %d slash commands for %s", n, strings.Trim(d.root, ".")))
					total += n
				}
			}
			if total > 0 {
				ch = append(ch, fmt.Sprintf("Total: Updated %d slash commands from %s mission", total, mission),
					"Slash commands now use Python CLI (no bash scripts)", "Slash commands now enforce flat tasks/ structure (feature 007)")
			}
			if cd := filepath.Join(root, ".kittify", "commands"); pathExists(cd) {
				pr := anyProver(manifestProver(false, true), defaultCanonicalProver())
				for _, f := range globFiles(cd, "*.toml") {
					v, err := guardDestructiveRemoval(f, root, pr, false, "", dryRun)
					if err != nil {
						continue
					}
					if v.owned {
						prefix := "Removed"
						if dryRun {
							prefix = "Would remove"
						}
						ch = append(ch, fmt.Sprintf("%s legacy %s", prefix, filepath.Base(f)))
					} else {
						warn = append(warn, v.diagnostic)
					}
				}
				if !dryRun && len(listDir(cd)) == 0 {
					if err := os.Remove(cd); err != nil {
						warn = append(warn, "Failed to remove .kittify/commands/: "+pyOSError(err))
					} else {
						ch = append(ch, "Removed empty .kittify/commands/ directory")
					}
				}
			}
			return newResult(ch, warn, nil), nil
		},
	}
}

func mWorkflowSimplification() migrationImpl {
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				if b, err := os.ReadFile(filepath.Join(ad, "spec-kitty.implement.md")); err == nil {
					c := string(b)
					if strings.Contains(c, "Work Package Selection") || strings.Contains(c, "Setup (Do This First)") {
						return true, nil
					}
					if !strings.Contains(c, "spec-kitty agent action implement") && !strings.Contains(c, "spec-kitty agent workflow implement") {
						return true, nil
					}
				}
				if b, err := os.ReadFile(filepath.Join(ad, "spec-kitty.review.md")); err == nil {
					c := string(b)
					if strings.Contains(c, "Location Pre-flight Check") || strings.Contains(c, "Conduct the review:") {
						return true, nil
					}
					if !strings.Contains(c, "spec-kitty agent action review") && !strings.Contains(c, "spec-kitty agent workflow review") {
						return true, nil
					}
				}
			}
			return false, nil
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".kittify")) {
				return false, "No .kittify directory (not a spec-kitty project)"
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn []string
			sdt := filepath.Join(root, ".kittify", "missions", "software-dev", "command-templates")
			// The wheel ships no software-dev command templates.
			warn = append(warn, "Mission templates not found in package. Slash commands may already be updated or require manual repair.")
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			total := 0
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				n := 0
				for _, name := range []string{"implement.md", "review.md"} {
					src := filepath.Join(sdt, name)
					if !pathExists(src) {
						continue
					}
					dest := filepath.Join(ad, "spec-kitty."+name)
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would update %s: spec-kitty.%s", d.root, name))
						continue
					}
					b, _ := os.ReadFile(src)
					if err := writeGeneratedFile(dest, b, true); err != nil {
						warn = append(warn, fmt.Sprintf("Failed to update %s/spec-kitty.%s: %s", d.root, name, err))
						continue
					}
					n++
				}
				if n > 0 {
					ch = append(ch, fmt.Sprintf("Updated %d templates for %s", n, strings.Trim(d.root, ".")))
					total += n
				}
			}
			if total > 0 {
				ch = append(ch, fmt.Sprintf("Total: Updated %d slash command templates", total), "Templates now use 'spec-kitty agent action' commands",
					"Agents now see prompts directly, no file navigation needed")
			} else if len(ch) == 0 {
				warn = append(warn, "No templates were updated (already updated or mission templates missing)")
			}
			return newResult(ch, warn, nil), nil
		},
	}
}

// --- 0.10.8 -----------------------------------------------------------------

func brokenLink(p string) bool {
	if !isSymlink(p) {
		return false
	}
	r, err := filepath.EvalSymlinks(p)
	return err != nil || r == p || !pathExists(r)
}

func mFixMemoryStructure() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			rm := filepath.Join(root, "memory")
			km := filepath.Join(root, ".kittify", "memory")
			if isDir(rm) {
				if !pathExists(km) {
					return true
				}
				if isSymlink(km) && brokenLink(km) {
					return true
				}
			}
			if brokenLink(km) {
				return true
			}
			return brokenLink(filepath.Join(root, ".kittify", "AGENTS.md"))
		},
		canApply: func(root string) (bool, string) {
			k := filepath.Join(root, ".kittify")
			if !pathExists(k) {
				return false, ".kittify directory must exist before migration"
			}
			if isDir(filepath.Join(root, "memory")) {
				return true, "Ready to migrate memory/ to .kittify/"
			}
			if isSymlink(filepath.Join(k, "memory")) || isSymlink(filepath.Join(k, "AGENTS.md")) {
				return true, "Ready to fix broken symlinks"
			}
			return true, "Ready to apply"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn []string
			rm := filepath.Join(root, "memory")
			k := filepath.Join(root, ".kittify")
			km := filepath.Join(k, "memory")
			ka := filepath.Join(k, "AGENTS.md")
			ta := filepath.Join(k, "templates", "AGENTS.md")
			if pathExists(km) && isSymlink(km) {
				if dryRun {
					ch = append(ch, "Would remove broken symlink: "+km)
				} else {
					os.Remove(km)
					ch = append(ch, "Removed broken symlink: "+km)
				}
			}
			if isDir(rm) {
				if !pathExists(km) {
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would move %s -> %s", rm, km))
					} else if err := mvRename(rm, km); err != nil {
						if cerr := copyTree(rm, km); cerr != nil {
							return migrationResult{Changes: ch, Warnings: warn, Errors: []string{"Failed to move or copy memory/: " + pyOSError(cerr)}}
						}
						ch = append(ch, fmt.Sprintf("Copied %s -> %s (move failed: %s)", rm, km, pyOSError(err)))
						warn = append(warn, "Could not move (copied instead): "+pyOSError(err))
					} else {
						ch = append(ch, fmt.Sprintf("Moved %s -> %s", rm, km))
					}
				} else {
					warn = append(warn, km+" already exists, skipping root memory/ migration")
				}
			}
			if !pathExists(km) {
				tc := ""
				if pathExists(filepath.Join(k, "missions", "software-dev")) {
					t := filepath.Join(k, "missions", "software-dev", "command-templates", "charter.md")
					if !pathExists(t) {
						t = filepath.Join(k, "templates", "command-templates", "charter.md")
					}
					if pathExists(t) {
						tc = t
					}
				}
				if tc != "" {
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would create %s from template", km))
					} else {
						os.MkdirAll(km, 0o755)
						copyFile2(tc, filepath.Join(km, "charter.md"))
						ch = append(ch, fmt.Sprintf("Created %s from template", km))
					}
				} else {
					warn = append(warn, km+" doesn't exist and no template found")
				}
			}
			if pathExists(ka) && isSymlink(ka) {
				if dryRun {
					ch = append(ch, "Would remove broken symlink: "+ka)
				} else {
					os.Remove(ka)
					ch = append(ch, "Removed broken symlink: "+ka)
				}
			}
			if !pathExists(ka) || isSymlink(ka) {
				if pathExists(ta) {
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would copy %s -> %s", ta, ka))
					} else {
						copyFile2(ta, ka)
						ch = append(ch, fmt.Sprintf("Copied %s -> %s", ta, ka))
					}
				} else {
					warn = append(warn, "No AGENTS.md template found at "+ta)
				}
			}
			for _, wt := range worktreeDirs(root) {
				wk := filepath.Join(wt, ".kittify")
				if !pathExists(wk) {
					continue
				}
				wm, wa := filepath.Join(wk, "memory"), filepath.Join(wk, "AGENTS.md")
				if isSymlink(wm) && brokenLink(wm) {
					if dryRun {
						ch = append(ch, "Would remove broken worktree symlink: "+wm)
					} else {
						os.Remove(wm)
						ch = append(ch, "Removed broken worktree symlink: "+wm)
					}
				}
				if !pathExists(wm) && pathExists(km) {
					rel := "../../../.kittify/memory"
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would create worktree symlink: %s -> %s", wm, rel))
					} else if err := os.Symlink(rel, wm); err != nil {
						copyTree(km, wm)
						ch = append(ch, "Copied to worktree (symlink failed): "+wm)
					} else {
						ch = append(ch, fmt.Sprintf("Created worktree symlink: %s -> %s", wm, rel))
					}
				}
				if isSymlink(wa) && brokenLink(wa) && !dryRun {
					os.Remove(wa)
				}
				if !pathExists(wa) && pathExists(ka) {
					rel := "../../../.kittify/AGENTS.md"
					if dryRun {
						ch = append(ch, fmt.Sprintf("Would create worktree symlink: %s -> %s", wa, rel))
					} else if err := os.Symlink(rel, wa); err != nil {
						copyFile2(ka, wa)
						ch = append(ch, "Copied to worktree (symlink failed): "+wa)
					} else {
						ch = append(ch, fmt.Sprintf("Created worktree symlink: %s -> %s", wa, rel))
					}
				}
			}
			return migrationResult{Success: true, Changes: ch, Warnings: warn}
		},
	}
}

// --- 0.11.3 -----------------------------------------------------------------

func mWorkflowAgentFlag() migrationImpl {
	nameMap := map[string]string{".github": "copilot", ".augment": "auggie", ".amazonq": "q"}
	agentName := func(r string) string {
		if n, ok := nameMap[r]; ok {
			return n
		}
		return strings.TrimLeft(r, ".")
	}
	impl := []string{"spec-kitty agent workflow implement", "spec-kitty agent action implement"}
	rev := []string{"spec-kitty agent workflow review", "spec-kitty agent action review"}
	hasAny := func(l string, ps []string) bool {
		for _, p := range ps {
			if strings.Contains(l, p) {
				return true
			}
		}
		return false
	}
	lineNeeds := func(l string) bool {
		return strings.Contains(l, "__AGENT__") || ((hasAny(l, impl) || hasAny(l, rev)) && !strings.Contains(l, "--agent"))
	}
	targets := []string{"spec-kitty.implement.md", "spec-kitty.review.md"}
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return false, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				for _, f := range targets {
					b, err := os.ReadFile(filepath.Join(ad, f))
					if err != nil {
						continue
					}
					for _, l := range pySplitLines(string(b)) {
						if lineNeeds(l) {
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
		canApply: func(root string) (bool, string) {
			if !pathExists(filepath.Join(root, ".kittify")) {
				return false, "No .kittify directory (not a spec-kitty project)"
			}
			return ok()
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			var ch, warn []string
			dirs, err := migrationAgentDirs(root)
			if err != nil {
				return migrationResult{}, err
			}
			for _, d := range dirs {
				ad := filepath.Join(root, d.root, d.subdir)
				if !pathExists(ad) {
					continue
				}
				name := agentName(d.root)
				n := 0
				for _, f := range targets {
					p := filepath.Join(ad, f)
					b, err := os.ReadFile(p)
					if err != nil {
						continue
					}
					updated := false
					lines := pySplitLines(string(b))
					for i, l := range lines {
						if strings.Contains(l, "__AGENT__") {
							updated = true
							l = strings.ReplaceAll(l, "__AGENT__", name)
						}
						if (hasAny(l, impl) || hasAny(l, rev)) && !strings.Contains(l, "--agent") {
							updated = true
							l = l + " --agent " + name
						}
						lines[i] = l
					}
					if updated {
						if !dryRun {
							writeGeneratedFile(p, []byte(strings.Join(lines, "\n")+"\n"), true)
						}
						n++
					}
				}
				if n > 0 {
					ch = append(ch, fmt.Sprintf("Updated %d action prompts for %s", n, name))
				}
			}
			if len(ch) == 0 {
				warn = append(warn, "No action prompts required updates")
			}
			return newResult(ch, warn, nil), nil
		},
	}
}

// --- 0.12.0 / 0.12.1 --------------------------------------------------------

func mDocumentationMission() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if usesCentralizedRuntime(root) {
				return false
			}
			k := filepath.Join(root, ".kittify")
			if !pathExists(k) {
				return false
			}
			md := filepath.Join(k, "missions")
			if !pathExists(md) {
				return true
			}
			d := filepath.Join(md, "documentation")
			return !(pathExists(d) && pathExists(filepath.Join(d, "mission.yaml")))
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch []string
			md := filepath.Join(root, ".kittify", "missions")
			dest := filepath.Join(md, "documentation")
			if pathExists(dest) && pathExists(filepath.Join(dest, "mission.yaml")) {
				return newResult([]string{"Documentation mission already installed (skipped)"}, nil, nil)
			}
			if !pathExists(md) {
				if dryRun {
					ch = append(ch, "Would create .kittify/missions/ directory")
				} else {
					os.MkdirAll(md, 0o755)
					ch = append(ch, "Created .kittify/missions/ directory")
				}
			}
			if dryRun {
				ch = append(ch, "Would copy documentation mission to .kittify/missions/documentation/")
			} else {
				if err := copyPkgTree("missions/documentation", dest); err != nil {
					return migrationResult{Errors: []string{"Failed to copy documentation mission: " + pyOSError(err)}}
				}
				ch = append(ch, fmt.Sprintf("Copied documentation mission (%d files)", countFiles(dest)))
			}
			return newResult(ch, nil, nil)
		},
	}
}

var (
	kittySpecsRemove = []*regexp.Regexp{regexp.MustCompile(`^kitty-specs/?$`), regexp.MustCompile(`^/kitty-specs/?$`)}
	kittySpecsKeep   = []*regexp.Regexp{regexp.MustCompile(`kitty-specs/\*\*/tasks/`), regexp.MustCompile(`kitty-specs/.*/tasks/`)}
)

func isBlockingKittySpecs(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return false
	}
	for _, k := range kittySpecsKeep {
		if k.MatchString(s) {
			return false
		}
	}
	for _, r := range kittySpecsRemove {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

func gitignoreSymlinkRefusal(p string) string {
	if !isSymlink(p) {
		return ""
	}
	t, err := os.Readlink(p)
	if err != nil {
		return ".gitignore is a symlink; refusing to follow it (could not resolve target: " + pyOSError(err) + ")"
	}
	return ".gitignore is a symlink to " + pyRepr(t) + "; refusing to follow it"
}

func mRemoveKittySpecsFromGitignore() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			content, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
			if err != nil {
				return true
			}
			for _, l := range pySplitLines(content) {
				if isBlockingKittySpecs(l) {
					return true
				}
			}
			return false
		},
		canApply: func(root string) (bool, string) {
			p := filepath.Join(root, ".gitignore")
			if r := gitignoreSymlinkRefusal(p); r != "" {
				return false, r
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			p := filepath.Join(root, ".gitignore")
			var ch, errs []string
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
			var kept []string
			removed := 0
			for i, l := range pySplitLinesKeep(content) {
				if isBlockingKittySpecs(l) {
					ch = append(ch, fmt.Sprintf("Line %d: Removed '%s'", i+1, strings.TrimSpace(l)))
					removed++
				} else {
					kept = append(kept, l)
				}
			}
			if removed == 0 {
				return newResult([]string{"No blocking kitty-specs/ entries found in .gitignore"}, nil, nil)
			}
			if dryRun {
				return newResult(append([]string{fmt.Sprintf("Would remove %d blocking entries from .gitignore", removed)}, ch...), nil, nil)
			}
			nc := regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(kept, ""), "\n\n")
			if err := os.WriteFile(p, []byte(nc), 0o666&^umask()); err != nil {
				errs = append(errs, "Failed to write .gitignore: "+pyOSError(err))
			} else {
				ch = append([]string{fmt.Sprintf("Removed %d blocking entries from .gitignore", removed)}, ch...)
			}
			return newResult(ch, nil, errs)
		},
	}
}

// --- 0.13.0 research CSV ------------------------------------------------------

var (
	evidenceColumns = []string{"timestamp", "source_type", "citation", "key_finding", "confidence", "notes"}
	sourceColumns   = []string{"source_id", "citation", "url", "accessed_date", "relevance", "status"}
)

// csvHeader is validators.csv_schema.validate_csv_schema's header read:
// (actual columns or nil, schema valid).
func csvHeader(p string, expected []string) ([]string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff")))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	row, err := r.Read()
	if err != nil || len(row) == 0 {
		return nil, false
	}
	var cols []string
	for _, c := range row {
		c = strings.TrimSpace(c)
		if c != "" && !strings.HasPrefix(c, "#") {
			cols = append(cols, c)
		}
	}
	if cols == nil {
		cols = []string{}
	}
	return cols, strings.Join(cols, "\x00") == strings.Join(expected, "\x00") && len(cols) == len(expected)
}

func researchFeatures(root string) []string {
	var out []string
	for _, f := range subdirs(filepath.Join(root, "kitty-specs")) {
		if !pathExists(filepath.Join(f, "meta.json")) {
			continue
		}
		meta, err := mission.ReadMeta(f)
		if err != nil || meta == nil || meta.GetString("mission") != "research" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func mResearchCSVSchemaCheck() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range researchFeatures(root) {
				for _, c := range []struct {
					name string
					cols []string
				}{{"evidence-log.csv", evidenceColumns}, {"source-register.csv", sourceColumns}} {
					p := filepath.Join(f, "research", c.name)
					if pathExists(p) {
						if _, valid := csvHeader(p, c.cols); !valid {
							return true
						}
					}
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, _ bool) migrationResult {
			if !pathExists(filepath.Join(root, "kitty-specs")) {
				return newResult([]string{"No kitty-specs/ directory found - nothing to check"}, nil, nil)
			}
			type mm struct{ feature, name, expected, actual string }
			var mismatches []mm
			for _, f := range researchFeatures(root) {
				for _, c := range []struct {
					name string
					cols []string
				}{{"evidence-log.csv", evidenceColumns}, {"source-register.csv", sourceColumns}} {
					p := filepath.Join(f, "research", c.name)
					if !pathExists(p) {
						continue
					}
					actual, valid := csvHeader(p, c.cols)
					if valid {
						continue
					}
					a := strings.Join(actual, ",")
					if len(actual) == 0 {
						a = "<missing>"
					}
					mismatches = append(mismatches, mm{filepath.Base(f), c.name, strings.Join(c.cols, ","), a})
				}
			}
			if len(mismatches) == 0 {
				return newResult([]string{"All research CSV schemas are correct ✅"}, nil, nil)
			}
			bar := strings.Repeat("=", 70)
			lines := []string{"", bar, "📋 Research CSV Schema Check (Informational)", bar, "", fmt.Sprintf("Found %d CSV file(s) with non-standard schemas:", len(mismatches)), ""}
			for _, m := range mismatches {
				lines = append(lines, fmt.Sprintf("%s/research/%s:", m.feature, m.name), "  Expected: "+m.expected, "  Actual:   "+m.actual, "",
					"  💡 To fix this schema mismatch:", "     1. Read canonical schema in .claude/commands/spec-kitty.implement.md",
					"     2. Create new CSV with correct headers", "     3. Map old data → new schema (LLM agents can help)", "     4. Replace old file", "")
			}
			lines = append(lines, bar, "ℹ️  This is INFORMATIONAL only - no files were modified.", "   Use LLM agents to help migrate data to canonical schemas.", bar, "")
			for _, l := range lines {
				fmt.Fprintln(migrationStdout, l)
			}
			return newResult([]string{fmt.Sprintf("Detected %d schema mismatch(es) - see report above", len(mismatches))}, nil, nil)
		},
	}
}

// --- 0.13.1 -----------------------------------------------------------------

func mExcludeWorktrees() migrationImpl {
	isRepo := func(root string) bool { return gitCommonDir(root) != "" }
	exclude := func(root string) string { return filepath.Join(root, ".git", "info", "exclude") }
	return migrationImpl{
		detect: func(root string) bool {
			if !isRepo(root) {
				return false
			}
			b, err := os.ReadFile(exclude(root))
			if err != nil {
				return false
			}
			return !strings.Contains(string(b), ".worktrees/")
		},
		canApply: func(root string) (bool, string) {
			if !isRepo(root) {
				return false, "Not a git repository"
			}
			if !pathExists(exclude(root)) {
				return false, ".git/info/exclude file not found"
			}
			return ok()
		},
		apply: func(root string, dryRun bool) migrationResult {
			if !isRepo(root) {
				return newResult([]string{"Skipped: not a git repository"}, nil, nil)
			}
			if dryRun {
				return newResult([]string{"Would add .worktrees/ to .git/info/exclude"}, nil, nil)
			}
			ExcludeFromGitIndex(root, []string{".worktrees/"})
			return newResult([]string{"Added .worktrees/ to .git/info/exclude"}, nil, nil)
		},
	}
}

// --- 0.13.8 -----------------------------------------------------------------

var branchPatterns = []*regexp.Regexp{
	regexp.MustCompile("(?im)^\\*\\*target branch\\*\\*:\\s*`?([^\\n`]+)`?\\s*$"),
	regexp.MustCompile("(?im)^\\*\\*base branch\\*\\*:\\s*`?([^\\n`]+)`?\\s*$"),
	regexp.MustCompile("(?im)^target repo branch:\\s*`?([^\\n`]+)`?\\s*$"),
	regexp.MustCompile("(?im)^branch:\\s*`?([^\\n`]+)`?\\s*$"),
	regexp.MustCompile("(?i)must be done on .*?`([^`]+)` branch"),
	regexp.MustCompile("(?i)all work packages branch from and merge back to [`“]?([^`”\\n]+)[`”]?"),
	regexp.MustCompile("(?i)merge back to [`“]?([^`”\\n]+)[`”]?"),
	regexp.MustCompile("(?i)repository[^\\n]*branch [`(]?([A-Za-z0-9._/-]+)"),
}

var branchTokenRe = regexp.MustCompile(`[A-Za-z0-9._/-]+`)

func normalizeBranchCandidate(v string) string {
	c := strings.TrimSpace(v)
	if c == "" || strings.Contains(strings.ToLower(c), " or ") {
		return ""
	}
	c = strings.Trim(c, "`'\"*[]() ")
	return branchTokenRe.FindString(c)
}

// inferTargetBranch is upgrade.feature_meta.infer_target_branch.
func inferTargetBranch(feature, root string) string {
	fallback := safecommit.PrimaryBranch(root)
	var cands []string
	seen := map[string]bool{}
	for _, name := range []string{"spec.md", "plan.md", "tasks.md", "quickstart.md"} {
		b, err := os.ReadFile(filepath.Join(feature, name))
		if err != nil {
			continue
		}
		content := decodeIgnoreErrors(b)
		for _, re := range branchPatterns {
			for _, m := range re.FindAllStringSubmatch(content, -1) {
				if c := normalizeBranchCandidate(m[1]); c != "" && !seen[c] {
					seen[c] = true
					cands = append(cands, c)
				}
			}
		}
	}
	if len(cands) == 1 {
		return cands[0]
	}
	return fallback
}

func numberedFeatures(root string) []string {
	var out []string
	for _, f := range subdirs(filepath.Join(root, "kitty-specs")) {
		n := filepath.Base(f)
		if len(n) >= 4 && n[3] == '-' && isDigit(n[0]) && isDigit(n[1]) && isDigit(n[2]) {
			out = append(out, f)
		}
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func mTargetBranch() migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			for _, f := range numberedFeatures(root) {
				if !pathExists(filepath.Join(f, "meta.json")) {
					continue
				}
				if meta, err := mission.ReadMeta(f); err == nil && meta != nil && !meta.Has("target_branch") {
					return true
				}
			}
			return false
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			var ch, warn, errs []string
			if !pathExists(filepath.Join(root, "kitty-specs")) {
				return newResult([]string{"No features found, skipping target_branch addition"}, nil, nil)
			}
			for _, f := range numberedFeatures(root) {
				if !pathExists(filepath.Join(f, "meta.json")) {
					continue
				}
				name := filepath.Base(f)
				meta, err := mission.ReadMeta(f)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Malformed JSON in %s/meta.json: %s", name, metaReadError(f, err)))
					continue
				}
				if meta == nil || meta.Has("target_branch") {
					continue
				}
				tb := inferTargetBranch(f, root)
				if tb != "main" {
					warn = append(warn, fmt.Sprintf("%s auto-detected target_branch=%s", name, tb))
				}
				if dryRun {
					ch = append(ch, fmt.Sprintf("Would add target_branch=%s to %s", tb, name))
					continue
				}
				meta.Set("target_branch", tb)
				if err := mission.WriteMeta(f, meta, false); err != nil {
					errs = append(errs, fmt.Sprintf("Failed to update %s/meta.json: %s", name, pyOSError(err)))
					continue
				}
				ch = append(ch, fmt.Sprintf("Added target_branch=%s to %s", tb, name))
			}
			return newResult(ch, warn, errs)
		},
	}
}

// renderFrontmatter is FrontmatterManager.render (WP field order; an empty
// mapping dumps as "{}").
func renderFrontmatter(front *jsonx.Object, body string, original ...string) string {
	if front.Len() == 0 {
		return "---\n{}\n---\n" + body
	}
	out := fm.Render(&fm.Doc{Meta: front, Body: body}, fm.WPFieldOrder)
	if len(original) > 0 {
		// ruamel round trip: a flow sequence read from the file stays flow.
		out = fm.RestoreFlow(out, fm.FlowKeys(original[0]), nil)
	}
	return out
}

// metaReadError is MissionMetaReadError's text for a malformed meta.json.
func metaReadError(feature string, err error) string {
	return err.Error()
}
