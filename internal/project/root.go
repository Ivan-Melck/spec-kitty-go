// Package project resolves the Spec Kitty project root and its well-known
// paths, mirroring specify_cli.core.paths.
package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// KittifyDir is the project metadata directory name.
const KittifyDir = ".kittify"

// SpecsDir is the mission directory root name.
const SpecsDir = "kitty-specs"

// WorktreesDir is the lane/coord worktree root name.
const WorktreesDir = ".worktrees"

// ErrNotFound is returned when no project root can be located.
var ErrNotFound = errors.New("not inside a spec-kitty project")

func isWorktreeGitdir(gitdir string) bool {
	parent := filepath.Dir(gitdir)
	return filepath.Base(parent) == "worktrees" && strings.HasSuffix(filepath.Base(filepath.Dir(parent)), ".git")
}

func readGitdir(gitFile string) (string, bool) {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	c := strings.TrimSpace(string(b))
	if !strings.HasPrefix(c, "gitdir:") {
		return "", false
	}
	g := strings.TrimSpace(strings.SplitN(c, ":", 2)[1])
	if g == "" {
		return "", false
	}
	if !filepath.IsAbs(g) {
		g = filepath.Join(filepath.Dir(gitFile), g)
	}
	return filepath.Clean(g), true
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// MainRepoRoot follows a linked-worktree .git pointer back to the primary
// checkout (get_main_repo_root).
func MainRepoRoot(p string) string {
	gitFile := filepath.Join(p, ".git")
	if isFile(gitFile) {
		if g, ok := readGitdir(gitFile); ok {
			if !isWorktreeGitdir(g) {
				return resolve(p)
			}
			return filepath.Dir(filepath.Dir(filepath.Dir(g)))
		}
	}
	return resolve(p)
}

// Locate finds the MAIN project root starting at start ("" = cwd), even from
// inside worktrees (locate_project_root). Returns "" when not found.
func Locate(start string) string {
	if env := os.Getenv("SPECIFY_REPO_ROOT"); env != "" {
		if p := resolve(env); isDir(p) {
			return MainRepoRoot(p)
		}
	}
	if start == "" {
		start, _ = os.Getwd()
	}
	cur := resolve(start)
	for {
		gitPath := filepath.Join(cur, ".git")
		if isFile(gitPath) {
			if g, ok := readGitdir(gitPath); ok && isWorktreeGitdir(g) {
				main := filepath.Dir(filepath.Dir(filepath.Dir(g)))
				if isDir(filepath.Join(main, KittifyDir)) {
					return main
				}
			}
		} else if isDir(gitPath) {
			if isDir(filepath.Join(cur, KittifyDir)) || isFile(filepath.Join(gitPath, "HEAD")) {
				return cur
			}
			return ""
		}
		k := filepath.Join(cur, KittifyDir)
		if isDir(k) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// Root returns the project root or ErrNotFound.
func Root() (string, error) {
	r := Locate("")
	if r == "" {
		return "", ErrNotFound
	}
	return r, nil
}

// IsInitialized reports whether root has a .kittify directory.
func IsInitialized(root string) bool { return isDir(filepath.Join(root, KittifyDir)) }

// Kittify returns <root>/.kittify/<parts...>.
func Kittify(root string, parts ...string) string {
	return filepath.Join(append([]string{root, KittifyDir}, parts...)...)
}

// Specs returns <root>/kitty-specs/<parts...>.
func Specs(root string, parts ...string) string {
	return filepath.Join(append([]string{root, SpecsDir}, parts...)...)
}

// Worktrees returns <root>/.worktrees/<parts...>.
func Worktrees(root string, parts ...string) string {
	return filepath.Join(append([]string{root, WorktreesDir}, parts...)...)
}

// CurrentWorktreeRoot returns the top of the checkout containing dir (which
// may be a linked worktree), or "".
func CurrentWorktreeRoot(dir string) string {
	cur := resolve(dir)
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}
