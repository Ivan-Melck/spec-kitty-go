package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GitignoreMarker heads the block Spec Kitty appends to .gitignore.
const GitignoreMarker = "# Added by Spec Kitty CLI (auto-managed)"

// RuntimeGitignoreEntries is state.contract.get_runtime_gitignore_entries()
// (sorted).
var RuntimeGitignoreEntries = []string{
	".kittify/charter/context-state.json",
	".kittify/derived/",
	".kittify/dossiers/",
	".kittify/encoding-provenance/",
	".kittify/events/",
	".kittify/lint-report.json",
	".kittify/logs/",
	".kittify/merge-state.json",
	".kittify/migrations/",
	".kittify/missions/__pycache__/",
	".kittify/runtime/",
	".kittify/skills-manifest.json",
	".kittify/sync-state.json",
	".kittify/workspaces/",
	".worktrees/",
	"kitty-ops/ops-index.jsonl",
	"kitty-specs/*/.kittify/dossiers/",
}

// ProtectionResult mirrors gitignore_manager.ProtectionResult.
type ProtectionResult struct {
	Success  bool
	Modified bool
	Added    []string
	Skipped  []string
	Warnings []string
	Errors   []string
}

// ProtectedEntries returns agent directories followed by runtime entries.
func ProtectedEntries() []string {
	var out []string
	for _, d := range GitignoreDirectories {
		out = append(out, d.Directory)
	}
	return append(out, RuntimeGitignoreEntries...)
}

func pyLines(s string) []string { return SplitLines(s) }

// EnsureGitignoreEntries is GitignoreManager.ensure_entries.
func EnsureGitignoreEntries(root string, entries []string, forceAppend bool) (bool, error) {
	if len(entries) == 0 {
		return false, nil
	}
	p := filepath.Join(root, ".gitignore")
	if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
		t, _ := os.Readlink(p)
		return false, fmt.Errorf(".gitignore is a symlink to '%s'; refusing to read or write through it: %s", t, p)
	}
	var lines []string
	nl := "\n"
	if runtime.GOOS == "windows" {
		nl = "\r\n"
	}
	if b, err := os.ReadFile(p); err == nil {
		content := strings.TrimPrefix(string(b), "\ufeff")
		nl = "\n"
		if strings.Contains(content, "\r\n") {
			nl = "\r\n"
		}
		lines = pyLines(content)
	}
	existing := map[string]bool{}
	for _, l := range lines {
		existing[l] = true
	}
	changed := false
	need := forceAppend
	for _, e := range entries {
		if !existing[e] {
			need = true
		}
	}
	if need {
		if !existing[GitignoreMarker] {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, GitignoreMarker)
			existing[GitignoreMarker] = true
			changed = true
		}
		for _, e := range entries {
			if forceAppend || !existing[e] {
				lines = append(lines, e)
				existing[e] = true
				changed = true
			}
		}
	}
	if changed {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, nl)), 0o644); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func worktreesWholeRootIgnored(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return false
	}
	found := false
	for _, l := range pyLines(string(b)) {
		t := strings.TrimSpace(l)
		switch t {
		case ".worktrees/", "/.worktrees/", ".worktrees", "/.worktrees":
			found = true
		default:
			if strings.HasPrefix(t, "!") && strings.Contains(t, ".worktrees") {
				found = false
			}
		}
	}
	return found
}

// ProtectAllAgents is GitignoreManager.protect_all_agents.
func ProtectAllAgents(root string, selections ...[]string) ProtectionResult {
	res := ProtectionResult{Success: true}
	entries := RuntimeGitignoreEntries
	before := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		for _, l := range pyLines(string(b)) {
			before[l] = true
		}
	}
	mod, err := EnsureGitignoreEntries(root, entries, false)
	if err != nil {
		res.Success = false
		if os.IsPermission(err) {
			res.Errors = append(res.Errors, "Cannot update .gitignore: Permission denied. Run: chmod u+w "+filepath.Join(root, ".gitignore"))
		} else {
			res.Errors = append(res.Errors, "Error protecting agent directories: "+err.Error())
		}
		return res
	}
	res.Modified = mod
	for _, e := range entries {
		if before[e] {
			res.Skipped = append(res.Skipped, e)
		} else {
			res.Added = append(res.Added, e)
		}
	}
	if !worktreesWholeRootIgnored(root) {
		if _, err := EnsureGitignoreEntries(root, []string{".worktrees/"}, true); err != nil {
			res.Success = false
			res.Errors = append(res.Errors, "Error protecting agent directories: "+err.Error())
			return res
		}
		res.Modified = true
		var sk []string
		for _, s := range res.Skipped {
			if s != ".worktrees/" {
				sk = append(sk, s)
			}
		}
		res.Skipped = sk
		has := false
		for _, a := range res.Added {
			has = has || a == ".worktrees/"
		}
		if !has {
			res.Added = append(res.Added, ".worktrees/")
		}
	}
	var keys []string
	if len(selections) > 0 {
		keys = selections[0]
	}
	before = map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		for _, line := range SplitLines(string(b)) {
			before[line] = true
		}
	}
	changed, err := SyncAgentBlock(root, keys, true)
	if err != nil {
		res.Success = false
		res.Errors = append(res.Errors, "Error protecting agent files: "+err.Error())
		return res
	}
	res.Modified = res.Modified || changed
	if keys == nil {
		keys, _ = IgnoredAgentKeys(root)
	}
	for _, entry := range AgentIgnoreEntries(root, keys) {
		if before[entry] {
			res.Skipped = append(res.Skipped, entry)
		} else {
			res.Added = append(res.Added, entry)
		}
	}
	return res
}
