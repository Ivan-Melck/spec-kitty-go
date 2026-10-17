// Package gitx is a thin wrapper around the git executable.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Error is a failed git invocation.
type Error struct {
	Args   []string
	Dir    string
	Code   int
	Stdout string
	Stderr string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Stdout)
	}
	return fmt.Sprintf("git %s failed (exit %d): %s", strings.Join(e.Args, " "), e.Code, msg)
}

// Result holds the output of a git invocation.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Exec runs git in dir and returns raw output; err is non-nil only when git
// could not be started.
func Exec(dir string, env []string, stdin []byte, args ...string) (Result, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// Run runs git and returns trimmed stdout, or *Error on non-zero exit.
func Run(dir string, args ...string) (string, error) {
	return RunEnv(dir, nil, args...)
}

// RunEnv is Run with extra environment.
func RunEnv(dir string, env []string, args ...string) (string, error) {
	res, err := Exec(dir, env, nil, args...)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &Error{Args: args, Dir: dir, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	return strings.TrimRight(res.Stdout, "\n"), nil
}

// RunInput runs git with stdin.
func RunInput(dir string, input []byte, args ...string) (string, error) {
	res, err := Exec(dir, nil, input, args...)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &Error{Args: args, Dir: dir, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	return strings.TrimRight(res.Stdout, "\n"), nil
}

// OK runs git and reports success (exit 0).
func OK(dir string, args ...string) bool {
	res, err := Exec(dir, nil, nil, args...)
	return err == nil && res.Code == 0
}

// Toplevel returns the worktree root containing dir.
func Toplevel(dir string) (string, error) {
	return Run(dir, "rev-parse", "--show-toplevel")
}

// CommonDir returns the absolute git common dir (shared by all worktrees).
func CommonDir(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return filepath.Clean(out), nil
}

// MainRepoRoot returns the primary checkout root even when dir is inside a
// linked worktree.
func MainRepoRoot(dir string) (string, error) {
	common, err := CommonDir(dir)
	if err != nil {
		return "", err
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	return Toplevel(dir)
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// CurrentBranch returns the checked-out branch name ("" when detached).
func CurrentBranch(dir string) string {
	out, err := Run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// HeadSHA returns HEAD's commit id.
func HeadSHA(dir string) (string, error) { return Run(dir, "rev-parse", "HEAD") }

// RevParse resolves rev to a commit id.
func RevParse(dir, rev string) (string, error) {
	return Run(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// BranchExists reports whether refs/heads/name exists.
func BranchExists(dir, name string) bool {
	return OK(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
}

// IsAncestor reports whether a is an ancestor of b.
func IsAncestor(dir, a, b string) bool {
	return OK(dir, "merge-base", "--is-ancestor", a, b)
}

// StatusPorcelain returns `git status --porcelain` lines.
func StatusPorcelain(dir string, paths ...string) ([]string, error) {
	args := []string{"status", "--porcelain", "--untracked-files=all"}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// IsDirty reports uncommitted changes (tracked or untracked).
func IsDirty(dir string) bool {
	lines, err := StatusPorcelain(dir)
	return err == nil && len(lines) > 0
}

// Worktree is an entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name, "" when detached
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// Worktrees lists registered worktrees.
func Worktrees(dir string) ([]Worktree, error) {
	out, err := Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		case strings.HasPrefix(line, "locked"):
			cur.Locked = true
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	return list, nil
}

// Commit commits staged changes with message. Extra args are appended.
func Commit(dir, message string, extra ...string) error {
	args := append([]string{"commit", "-m", message}, extra...)
	_, err := Run(dir, args...)
	return err
}

// HasStagedChanges reports whether the index differs from HEAD.
func HasStagedChanges(dir string) bool {
	return !OK(dir, "diff", "--cached", "--quiet")
}
