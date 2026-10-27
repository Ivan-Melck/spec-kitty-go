package setup

// The 3.2.4–3.2.6rc3 gitignore backfills, each with its own upstream
// wording, failure handling and (3.2.5) index untracking.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

func init() {
	migrationImpls["3.2.4_runtime_dirs_gitignore_backfill"] = mRuntimeDirsGitignore()
	migrationImpls["3.2.4_derived_mission_views_gitignore_backfill"] = mDerivedViewsGitignore()
	migrationImpls["3.2.6rc3_lint_report_gitignore_backfill"] = mLintReportGitignore()
	migrationImpls["3.2.6rc3_worktrees_gitignore_backfill"] = mWorktreesGitignore()
}

func missingEquivalents(present map[string]bool, equiv [][]string) []string {
	var out []string
	for _, forms := range equiv {
		found := false
		for _, f := range forms {
			found = found || present[f]
		}
		if !found {
			out = append(out, forms[0])
		}
	}
	return out
}

func projectPathCanApply(root string) (bool, string) {
	if !pathExists(root) {
		return false, "Project path does not exist: " + root
	}
	return true, ""
}

func mRuntimeDirsGitignore() migrationImpl {
	equiv := [][]string{{".kittify/migrations/", ".kittify/migrations"}, {".kittify/logs/", ".kittify/logs"}}
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			p, err := gitignoreEntrySet(root)
			if err != nil {
				return false, err
			}
			return len(missingEquivalents(p, equiv)) > 0, nil
		},
		canApply: projectPathCanApply,
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			p, err := gitignoreEntrySet(root)
			if err != nil {
				return migrationResult{}, err
			}
			missing := missingEquivalents(p, equiv)
			if dryRun {
				var ch []string
				for _, e := range missing {
					ch = append(ch, "Would add "+e+" to .gitignore")
				}
				return migrationResult{Success: true, Changes: ch}, nil
			}
			if len(missing) == 0 {
				return migrationResult{Success: true, Changes: []string{"gitignore entries already present"}}, nil
			}
			if _, err := agents.EnsureGitignoreEntries(root, missing, false); err != nil {
				return migrationResult{}, err
			}
			return migrationResult{Success: true, Changes: []string{"Added gitignore entries: " + strings.Join(missing, ", ")}}, nil
		},
	}
}

func mDerivedViewsGitignore() migrationImpl {
	equiv := [][]string{{".kittify/derived/", ".kittify/derived"}}
	return migrationImpl{
		detectE: func(root string) (bool, error) {
			p, err := gitignoreEntrySet(root)
			if err != nil {
				return false, err
			}
			return len(missingEquivalents(p, equiv)) > 0, nil
		},
		canApply: projectPathCanApply,
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			p, err := gitignoreEntrySet(root)
			if err != nil {
				return migrationResult{}, err
			}
			present := len(missingEquivalents(p, equiv)) == 0
			if dryRun {
				var ch []string
				if !present {
					ch = []string{"Would add .kittify/derived/ to .gitignore"}
				}
				return migrationResult{Success: true, Changes: ch}, nil
			}
			if present {
				return migrationResult{Success: true, Changes: []string{"gitignore entry already present"}}, nil
			}
			modified, err := agents.EnsureGitignoreEntries(root, []string{".kittify/derived/"}, false)
			if err != nil {
				return migrationResult{}, err
			}
			if !modified {
				return migrationResult{Success: true, Changes: []string{"gitignore entry already present"}}, nil
			}
			return migrationResult{Success: true, Changes: []string{"Added gitignore entry: .kittify/derived/"}}, nil
		},
	}
}

// untrackTrackedPaths is m_3_2_5._untrack_tracked_paths (best effort).
func untrackTrackedPaths(root string, entries []string) []string {
	var cands []string
	for _, e := range entries {
		cands = append(cands, strings.TrimRight(e, "/"))
	}
	if len(cands) == 0 {
		return nil
	}
	r, err := gitx.Exec(root, nil, nil, append([]string{"ls-files", "--full-name", "-z", "--"}, cands...)...)
	if err != nil || r.Code != 0 {
		return nil
	}
	set := map[string]bool{}
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	tracked := sortedKeys(set)
	top, err := gitx.Run(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	topR, _ := filepath.EvalSymlinks(strings.TrimSpace(top))
	rootR, _ := filepath.EvalSymlinks(root)
	prefix, err := filepath.Rel(topR, rootR)
	if err != nil || strings.HasPrefix(prefix, "..") {
		return nil
	}
	prefix = filepath.ToSlash(prefix)
	projectRel := func(p string) (string, bool) {
		if prefix == "." {
			return p, true
		}
		if !strings.HasPrefix(p, prefix+"/") {
			return "", false
		}
		return p[len(prefix)+1:], true
	}
	owned := manifestOwnedSkillsPaths(root)
	var untrack []string
	for _, p := range tracked {
		rel, ok := projectRel(p)
		if !ok {
			return nil
		}
		if !strings.HasPrefix(rel, ".agents/skills/") || isProjectedSkillsPath(rel, owned) {
			untrack = append(untrack, p)
		}
	}
	if len(untrack) == 0 {
		return nil
	}
	rr, err := gitx.Exec(strings.TrimSpace(top), nil, nil, append([]string{"--literal-pathspecs", "rm", "--cached", "-r", "-q", "--"}, untrack...)...)
	if err != nil || rr.Code != 0 {
		return nil
	}
	var out []string
	for _, p := range untrack {
		rel, _ := projectRel(p)
		out = append(out, rel)
	}
	return out
}

// readGitignoreText is gitignore_manager.read_gitignore_text ("", false when absent).
func readGitignoreText(p string) (string, bool, error) {
	if isSymlink(p) {
		return "", false, failClosed("Refusing to read symlinked .gitignore: %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, failClosed("Could not safely open .gitignore: %s", pyOSError(err))
	}
	t, derr := pyDecodeUTF8Strict(b)
	if derr != nil {
		return "", false, failClosed(".gitignore is not valid UTF-8: %s", derr)
	}
	return strings.TrimPrefix(t, "\ufeff"), true, nil
}

func pyDecodeUTF8Strict(b []byte) (string, error) {
	if !utf8.Valid(b) {
		return "", errors.New("'utf-8' codec can't decode bytes")
	}
	return string(b), nil
}

func mLintReportGitignore() migrationImpl {
	const entry = ".kittify/lint-report.json"
	missing := func(root string) (bool, error) {
		text, ok, err := readGitignoreText(filepath.Join(root, ".gitignore"))
		if err != nil {
			return false, err
		}
		if !ok {
			return true, nil
		}
		for _, l := range pySplitLines(text) {
			if strings.TrimSpace(l) == entry && !strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") {
				return false, nil
			}
		}
		return true, nil
	}
	return migrationImpl{
		detect: func(root string) bool {
			m, err := missing(root)
			return err != nil || m
		},
		canApply: func(root string) (bool, string) {
			if ok, r := projectPathCanApply(root); !ok {
				return ok, r
			}
			if _, err := missing(root); err != nil {
				return false, err.Error()
			}
			return true, ""
		},
		apply: func(root string, dryRun bool) migrationResult {
			m, err := missing(root)
			if err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			if dryRun {
				if m {
					return migrationResult{Success: true, Changes: []string{"Would add " + entry + " to .gitignore"}}
				}
				return migrationResult{Success: true}
			}
			if !m {
				return migrationResult{Success: true, Changes: []string{"gitignore entry already present"}}
			}
			if _, err := agents.EnsureGitignoreEntries(root, []string{entry}, false); err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			return migrationResult{Success: true, Changes: []string{"Added gitignore entry: " + entry}}
		},
	}
}

// gitWorkTreeState is gitignore_manager._git_work_tree_state (nil = git failure).
func gitWorkTreeState(root string) *bool {
	t, f := true, false
	r, err := gitx.Exec(root, nil, nil, "rev-parse", "--is-inside-work-tree")
	if err == nil && r.Code == 0 && strings.TrimSpace(r.Stdout) == "true" {
		return &t
	}
	for d := root; ; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		if st, err := os.Stat(g); err == nil {
			if !st.IsDir() || pathExists(filepath.Join(g, "HEAD")) {
				return nil
			}
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return &f
}

// worktreesNeedBackfill is not is_gitignore_root_effectively_ignored(.worktrees).
func worktreesNeedBackfill(root string) (bool, error) {
	wt := filepath.Join(root, ".worktrees")
	if isSymlink(wt) {
		return false, failClosed("Refusing symlinked managed root: %s", wt)
	}
	if pathExists(wt) && !isDir(wt) {
		return false, failClosed("Managed root is not a directory: %s", wt)
	}
	state := gitWorkTreeState(root)
	if state == nil {
		return false, failClosed("Git could not verify the managed root ignore state")
	}
	if *state {
		r, err := gitx.Exec(root, nil, nil, "ls-files", "--full-name", "-z", "--", ".worktrees")
		if err != nil || r.Code != 0 {
			msg := "Could not inspect tracked paths under .worktrees"
			if err == nil && strings.TrimSpace(r.Stderr) != "" {
				msg += ": " + strings.TrimSpace(r.Stderr)
			}
			return false, failClosed("%s", msg)
		}
		if strings.Trim(r.Stdout, "\x00") != "" {
			return false, failClosed("Tracked paths exist under .worktrees; .gitignore cannot protect tracked files. " +
				"Review them, then untrack with `git rm -r --cached -- .worktrees` and rerun " +
				"`spec-kitty upgrade` (or `spec-kitty init` during initialization).")
		}
	}
	text, err := readIgnoreFileText(filepath.Join(root, ".gitignore"))
	if err != nil {
		return false, err
	}
	if text == "" {
		return true, nil
	}
	var entries []string
	for _, l := range pySplitLines(text) {
		e := strings.TrimRight(l, " \t\n\r\v\f")
		if e != "" && !strings.HasPrefix(e, "#") {
			entries = append(entries, e)
		}
	}
	last := -1
	for i, e := range entries {
		switch e {
		case ".worktrees", ".worktrees/", "/.worktrees/", "/.worktrees":
			last = i
		}
	}
	if last < 0 {
		return true, nil
	}
	for _, e := range entries[last+1:] {
		if strings.HasPrefix(e, "!") {
			return true, nil
		}
	}
	if !*state {
		return false, nil
	}
	r, err := gitx.Exec(root, nil, nil, "check-ignore", "--quiet", "--no-index", "--", ".worktrees/.spec-kitty-ignore-probe")
	if err != nil || (r.Code != 0 && r.Code != 1) {
		return false, failClosed("Git could not verify the managed root ignore state")
	}
	return r.Code != 0, nil
}

func mWorktreesGitignore() migrationImpl {
	const entry = ".worktrees/"
	return migrationImpl{
		detect: func(root string) bool {
			m, err := worktreesNeedBackfill(root)
			return err != nil || m
		},
		canApply: func(root string) (bool, string) {
			if ok, r := projectPathCanApply(root); !ok {
				return ok, r
			}
			if _, err := worktreesNeedBackfill(root); err != nil {
				return false, err.Error()
			}
			return true, ""
		},
		apply: func(root string, dryRun bool) migrationResult {
			m, err := worktreesNeedBackfill(root)
			if err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			if dryRun {
				if m {
					return migrationResult{Success: true, Changes: []string{"Would add " + entry + " to .gitignore"}}
				}
				return migrationResult{Success: true}
			}
			if !m {
				return migrationResult{Success: true, Changes: []string{"gitignore entry already present"}}
			}
			if _, err := agents.EnsureGitignoreEntries(root, []string{entry}, true); err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			still, err := worktreesNeedBackfill(root)
			if err != nil {
				return migrationResult{Errors: []string{err.Error()}}
			}
			if still {
				return migrationResult{Errors: []string{".worktrees/ is still not effectively gitignored after backfill"}}
			}
			return migrationResult{Success: true, Changes: []string{"Added gitignore entry: " + entry}}
		},
	}
}
