// Package consolidate implements `spec-kitty consolidate`: lane→mission
// merges, the mission→target squash, done bookkeeping, the terminus
// retrospective and the worktree/branch teardown.
package consolidate

import (
	"fmt"
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

var driverDefs = [][3]string{
	{"spec-kitty-event-log", "Spec Kitty event log union merge", "merge-driver-event-log"},
	{"spec-kitty-meta", "Spec Kitty mission meta field merge", "merge-driver-meta"},
	{"spec-kitty-traces", "Spec Kitty mission traces union merge", "merge-driver-traces"},
	{"spec-kitty-acceptance-matrix", "Spec Kitty acceptance matrix filled-side merge", "merge-driver-acceptance-matrix"},
	{"spec-kitty-issue-matrix", "Spec Kitty issue matrix row-aware merge", "merge-driver-issue-matrix"},
	{"spec-kitty-review-cycle", "Spec Kitty review-cycle verdict collision refusal", "merge-driver-review-cycle"},
	{"spec-kitty-decision-index", "Spec Kitty decision-index entry union merge", "merge-driver-decision-index"},
}

// activateDrivers is _ephemeral_merge_driver_activation: ensures the driver
// definitions exist and seeds $GIT_COMMON_DIR/info/attributes; the returned
// func restores the attributes file.
func activateDrivers(root string) func() {
	for _, d := range driverDefs {
		if !gitx.OK(root, "config", "--get", "merge."+d[0]+".driver") {
			gitx.Run(root, "config", "merge."+d[0]+".name", d[1])
			gitx.Run(root, "config", "merge."+d[0]+".driver", "spec-kitty "+d[2]+" %O %A %B")
		}
	}
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
