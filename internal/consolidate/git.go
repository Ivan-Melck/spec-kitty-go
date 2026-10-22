// Package consolidate implements `spec-kitty consolidate`: lane→mission
// merges, the mission→target squash, done bookkeeping, the terminus
// retrospective and the worktree/branch teardown.
package consolidate

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/autorebase"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
)

// mergeEnv is _make_merge_env: PATH prefixed with this executable's
// directory so `spec-kitty merge-driver-*` resolves to this binary.
func mergeEnv(twoWay bool) []string {
	dir := ""
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	env := []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	if twoWay {
		return append(env, mergedriver.MetaTwoWayEnv+"=1")
	}
	return append(env, mergedriver.MetaTwoWayEnv+"=")
}

func gitEnv(dir string, env []string, args ...string) (gitx.Result, error) {
	return gitx.Exec(dir, env, nil, args...)
}

func revParse(dir, ref string) string {
	out, err := gitx.Run(dir, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

var driverAttributes = []string{
	"kitty-specs/**/status.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/decisions.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/meta.json merge=spec-kitty-meta",
	"kitty-specs/**/traces/*.md merge=spec-kitty-traces",
	"kitty-specs/**/acceptance-matrix.json merge=spec-kitty-acceptance-matrix",
	"kitty-specs/**/issue-matrix.json merge=spec-kitty-issue-matrix",
	"kitty-specs/**/tasks/*/review-cycle-*.md merge=spec-kitty-review-cycle",
	"kitty-specs/**/decisions/index.json merge=spec-kitty-decision-index",
}

// activateDrivers is _ephemeral_merge_driver_activation: ensures the driver
// definitions exist and seeds $GIT_COMMON_DIR/info/attributes; the returned
// func restores the attributes file.
func activateDrivers(root string) func() {
	ensureDriverConfig(root)
	common, err := gitx.CommonDir(root)
	if err != nil {
		return func() {}
	}
	p := filepath.Join(common, "info", "attributes")
	orig, rerr := os.ReadFile(p)
	existing := string(orig)
	var add []string
	for _, l := range driverAttributes {
		if !strings.Contains(existing, l) {
			add = append(add, l)
		}
	}
	if len(add) == 0 {
		return func() {}
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	text := existing
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	os.WriteFile(p, []byte(text+strings.Join(add, "\n")+"\n"), 0o644)
	return func() {
		if rerr != nil {
			os.Remove(p)
			return
		}
		os.WriteFile(p, orig, 0o644)
	}
}

// ensureDriverConfig is _ensure_merge_driver_git_config: define the
// custom merge drivers without seeding info/attributes.
func ensureDriverConfig(root string) { autorebase.EnsureDriverConfig(root) }

// scratchWorktree creates a detached temp worktree at ref.
func scratchWorktree(root, ref, prefix string) (string, func(), error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, err
	}
	if r, err := gitEnv(root, mergeEnv(false), "worktree", "add", "--detach", dir, ref); err != nil || r.Code != 0 {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("Failed to create merge worktree: %s", strings.TrimSpace(r.Stderr))
	}
	return dir, func() {
		gitx.Run(root, "worktree", "remove", "--force", dir)
		os.RemoveAll(dir)
		gitx.Run(root, "worktree", "prune")
	}, nil
}

// worktreeFor returns the checkout that has branch checked out ("" none).
func worktreeFor(root, branch string) string {
	wts, err := gitx.Worktrees(root)
	if err != nil {
		return ""
	}
	for _, w := range wts {
		if w.Branch == branch || w.Branch == "refs/heads/"+branch {
			return w.Path
		}
	}
	return ""
}

// advanceBranch moves branch to sha and resyncs a checkout that has it.
func advanceBranch(root, branch, sha string) error {
	wt := worktreeFor(root, branch)
	if _, err := gitx.Run(root, "update-ref", "refs/heads/"+branch, sha); err != nil {
		return err
	}
	if wt != "" {
		if _, err := gitx.Run(wt, "reset", "-q", "--hard", sha); err != nil {
			return err
		}
	}
	return nil
}

func unmergedPaths(dir string) []string {
	out, _ := gitx.Run(dir, "diff", "--name-only", "--diff-filter=U")
	var l []string
	for _, s := range strings.Split(strings.TrimSpace(out), "\n") {
		if s != "" {
			l = append(l, s)
		}
	}
	return l
}
