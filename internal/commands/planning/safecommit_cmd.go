package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

func safeCommitCommand() *cli.Command {
	return &cli.Command{
		Name:  "safe-commit",
		Short: "Commit only the requested files via Spec Kitty's safe-commit path.",
		Args: []*cli.Arg{{Name: "files", Metavar: "{files}", Required: true, Variadic: true,
			Type: "<path>",
			Help: "Files or directories to commit, relative to the current worktree root or absolute. Directory arguments expand to their contained changed/untracked files with an explicit expansion report."}},
		Flags: []*cli.Flag{
			{Name: "message", Short: "m", Kind: cli.StringList, Metavar: "<str>", Required: true, Help: messageHelp},
			{Name: "to-branch", Kind: cli.String, Metavar: "<str>", Help: "Short branch name the commit must land on. The helper asserts HEAD matches this branch before staging. When omitted, the current HEAD branch is used (deprecated; --to-branch becomes required in v3.3). This is the only destination authority — no env-var inference."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
		},
		Run: runSafeCommit,
	}
}

// currentWorktreeRoot is _current_worktree_root (the caller's worktree).
func currentWorktreeRoot() string {
	if out, err := gitx.Run(cwd(), "rev-parse", "--show-toplevel"); err == nil && strings.TrimSpace(out) != "" {
		p := strings.TrimSpace(out)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return project.Locate("")
}

func safePayload(success, committed bool, files []string, errMsg string) *jsonx.Object {
	res := "error"
	if success {
		res = "success"
	}
	o := jsonx.NewObject("result", res, "success", success, "committed", committed)
	if files != nil {
		o.Set("files", strList(files))
	}
	if errMsg != "" {
		o.Set("error", errMsg)
	}
	return o
}

func isRealDir(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.IsDir()
}

func relTo(root, p string) (string, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s is not in the subpath of %s", mission.PyRepr(p), mission.PyRepr(root))
	}
	return rel, nil
}

func runSafeCommit(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error {
		if jsonMode {
			printPretty(ctx.Stdout, safePayload(false, false, nil, msg))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	message, err := joinMessage(ctx.Strings("message"))
	if err != nil {
		return fail(err.Error())
	}
	root := currentWorktreeRoot()
	var normalized []string
	for _, f := range ctx.ArgList("files") {
		normalized = append(normalized, safecommit.ResolveCommitPath(root, f))
	}
	// _refuse_unknown_paths
	for _, p := range normalized {
		if isRealDir(p) {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		rel, err := relTo(root, p)
		if err != nil {
			return fail(err.Error())
		}
		known := safecommit.IsTracked(root, rel)
		if !known {
			e, err := safecommit.StatusEntries(root, "all", rel)
			if err != nil {
				return fail(fmt.Sprintf("Unable to inspect '%s' before commit.", rel))
			}
			known = len(e) > 0
		}
		if !known {
			return fail("Unknown path (did not match any files: not on disk and not known to git): " + rel)
		}
	}
	expanded, report, err := expandArguments(root, normalized)
	if err != nil {
		return fail(err.Error())
	}
	var relFiles []string
	for _, p := range expanded {
		rel, err := relTo(root, p)
		if err != nil {
			return fail(err.Error())
		}
		relFiles = append(relFiles, rel)
	}
	ref, err := resolveSafeCommitTarget(ctx, root, expanded)
	if err != nil {
		return fail(err.Error())
	}
	if len(report) > 0 && !jsonMode {
		for _, l := range report {
			say(ctx.Stdout, l)
		}
	}
	committed := false
	if len(expanded) > 0 {
		// _has_candidate_changes: ignored entries count (#6649) — the commit
		// force-stages every requested path, so a named untracked file under
		// an ignored directory is committed, not reported as "no changes".
		entries, err := safecommit.StatusEntriesWithIgnored(root, "all", relFiles...)
		if err != nil {
			return fail("Unable to inspect requested files before commit.")
		}
		if len(entries) > 0 {
			// _resolve_capability_for_target: POST_CONSOLIDATION_WRITE only
			// for the E2 CONSOLIDATED destination, STANDARD otherwise.
			opts := safecommit.Options{}
			if slug := missionSlugFromPaths(root, expanded); slug != "" {
				if kind := missionFileKind(root, expanded); kind != "" {
					opts.PostConsolidation = safecommit.IsPostConsolidationWriteTarget(root, slug, kind, ref)
				}
			}
			if _, err := safecommit.Commit(root, root, ref, message, expanded, opts); err != nil {
				return fail(err.Error())
			}
			committed = true
		}
	}
	if relFiles == nil {
		relFiles = []string{}
	}
	payload := safePayload(true, committed, relFiles, "")
	if jsonMode {
		if len(report) > 0 {
			payload.Set("expansion", strList(report))
		}
		printPretty(ctx.Stdout, payload)
		return nil
	}
	if committed {
		say(ctx.Stdout, "[green]Requested files committed[/green]")
	} else {
		say(ctx.Stdout, "[yellow]No requested changes to commit[/yellow]")
	}
	return nil
}

// expandArguments is _expand_arguments: directory arguments expand to their
// changed / untracked files.
func expandArguments(root string, files []string) ([]string, []string, error) {
	var out, report []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	var entries []safecommit.StatusEntry
	loaded := false
	for _, p := range files {
		if !isRealDir(p) {
			add(p)
			continue
		}
		relDir, err := relTo(root, p)
		if err != nil {
			return nil, nil, err
		}
		relDir = filepath.ToSlash(relDir)
		if !loaded {
			entries, err = safecommit.StatusEntries(root, "all")
			if err != nil {
				return nil, nil, fmt.Errorf("Unable to inspect directory '%s' before commit.", relDir)
			}
			loaded = true
		}
		inside := func(path string) bool {
			return relDir == "." || path == relDir || strings.HasPrefix(path, relDir+"/")
		}
		var contained []string
		for _, e := range entries {
			sides := []string{e.Path}
			if e.OrigPath != "" {
				sides = []string{e.OrigPath, e.Path}
			}
			var in []string
			outside := ""
			for _, s := range sides {
				if inside(s) {
					in = append(in, s)
				} else if outside == "" {
					outside = s
				}
			}
			if len(in) == 0 {
				continue
			}
			if len(in) != len(sides) {
				return nil, nil, fmt.Errorf("Rename crosses the directory argument %s/: %s is outside it; commit both paths explicitly", relDir, outside)
			}
			contained = append(contained, sides...)
		}
		display := "(no changed files)"
		if len(contained) > 0 {
			display = strings.Join(contained, ", ")
		}
		report = append(report, fmt.Sprintf("Expanding %s/ → %d files: %s", relDir, len(contained), display))
		for _, c := range contained {
			add(filepath.Join(root, filepath.FromSlash(c)))
		}
	}
	return out, report, nil
}

// resolveSafeCommitTarget is _resolve_commit_target.
func resolveSafeCommitTarget(ctx *cli.Context, root string, files []string) (string, error) {
	if ref := ctx.String("to-branch"); ref != "" {
		return strings.TrimPrefix(ref, "refs/heads/"), nil
	}
	if slug := missionSlugFromPaths(root, files); slug != "" {
		if kind := missionFileKind(root, files); kind != "" {
			main := project.MainRepoRoot(root)
			if main == "" {
				main = root
			}
			if dir, err := mission.ResolveHandle(main, slug); err == nil {
				ref, err := safecommit.ResolvePlacement(main, dir, kind)
				if err != nil && safecommit.IsCode(err, safecommit.CodeConsolidatedContentAbsent) {
					// MissionAwareCommitRefused: never fall through to HEAD.
					return "", err
				}
				if err == nil {
					return ref, nil
				}
			}
		}
	}
	cur := gitx.CurrentBranch(root)
	if cur == "" || cur == "HEAD" {
		return "", fmt.Errorf("Cannot resolve destination ref: HEAD is detached or not on a branch. Pass --to-branch <ref> explicitly.")
	}
	fmt.Fprintln(ctx.Stderr, "warning: --to-branch will be required in v3.3; pass it explicitly")
	return cur, nil
}

func missionSlugFromPaths(root string, files []string) string {
	for _, p := range files {
		rel, err := relTo(root, safecommit.ResolveCommitPath(root, p))
		if err != nil {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 && parts[0] == "kitty-specs" {
			return parts[1]
		}
	}
	return ""
}

func missionFileKind(root string, files []string) string {
	for _, p := range files {
		rel, err := relTo(root, safecommit.ResolveCommitPath(root, p))
		if err != nil {
			continue
		}
		if k := mission.ArtifactKindForPath(filepath.ToSlash(rel)); k != "" {
			return k
		}
	}
	return ""
}
