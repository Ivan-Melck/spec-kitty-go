package commitguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:   "commit-guard-hook",
			Short:  "Run the commit guard and exit with its result code.",
			Hidden: true,
			Args:   []*cli.Arg{{Name: "args", Variadic: true, Help: "Ignored; accepted for parity with the primary hook path."}},
			Run: func(ctx *cli.Context) error {
				if code := Main(ctx.Stderr); code != 0 {
					return cli.Exit(code)
				}
				return nil
			},
		})
	})
}

func git(dir string, args ...string) (string, int) {
	r, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil {
		return "", -1
	}
	return r.Stdout, r.Code
}

// Main is policy.commit_guard_hook.main: 0 allows the commit, 1 blocks it.
// Diagnostics go to stderr.
func Main(stderr io.Writer) int {
	cwd, _ := os.Getwd()
	out, code := git(cwd, "rev-parse", "--show-toplevel")
	if code != 0 {
		return 0
	}
	worktreeRoot := strings.TrimSpace(out)
	repoRoot := worktreeRoot
	if out, code := git(cwd, "rev-parse", "--git-common-dir"); code == 0 {
		common := strings.TrimSpace(out)
		if !filepath.IsAbs(common) {
			common = filepath.Join(cwd, common)
		}
		repoRoot = filepath.Dir(common)
	}
	out, code = git(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if code != 0 {
		return 0
	}
	branch := strings.TrimSpace(out)

	cfg := LoadConfig(repoRoot, stderr)
	if !cfg.Enabled || cfg.Mode == "off" {
		return 0
	}
	args := []string{"-c", "diff.relative=false", "diff", "--name-only", "-z", "-M", "--cached", "--"}
	r, err := gitx.Exec(worktreeRoot, nil, nil, args...)
	if err != nil || r.Code != 0 {
		detail := "no error output"
		stderrText := r.Stderr
		if err != nil {
			stderrText = err.Error()
		}
		if s := strings.TrimSpace(stderrText); s != "" {
			detail = strings.SplitN(s, "\n", 2)[0]
		}
		fmt.Fprintf(stderr, "[spec-kitty guard] BLOCKED: could not list staged files: git %s failed in %s (exit %d): %s\n",
			strings.Join(args, " "), worktreeRoot, r.Code, detail)
		return 1
	}
	var staged []string
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p != "" {
			staged = append(staged, p)
		}
	}
	if len(staged) == 0 {
		return 0
	}
	scope := ResolveScope(repoRoot, branch, stderr)
	res := ValidateStagedFiles(staged, scope.OwnedFiles, branch, cfg, scope)
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "[spec-kitty guard] WARNING: %s\n", w)
	}
	if !res.Allowed {
		for _, v := range res.Violations {
			fmt.Fprintf(stderr, "[spec-kitty guard] BLOCKED: %s\n", v)
		}
		fmt.Fprintln(stderr, "[spec-kitty guard] Commit blocked by ownership policy.")
		return 1
	}
	return 0
}
