package cutover

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "cutover-guard",
			Short: "Diff-scoped fail-closed cut-over gate (pre-merge required check).",
			Flags: []*cli.Flag{
				{Name: "base-ref", Kind: cli.String, Metavar: "<str>", Help: "Diff HEAD against the merge-base with this ref to discover touched kitty-specs/<mission> paths (e.g. origin/main)."},
				{Name: "paths-from", Kind: cli.String, Metavar: "PATH", Help: "Read already-known changed paths from this file (one per line) instead of running git diff. Use when the CI host already provides the diff (e.g. a PR file list)."},
				{Name: "json", Kind: cli.Bool, Help: "Emit the guard verdict as structured JSON."},
			},
			Run: run,
		})
	})
}

const remedyTemplate = "spec-kitty migrate backfill-runtime-state --mission %s"

var remedyByReason = []struct{ reason, text string }{
	{ReasonPhaseMalformed, "remove the malformed status_phase from kitty-specs/{slug}/meta.json (accept and consolidate stamp it), or, for an accepted Mission, run " + strings.Replace(remedyTemplate, "%s", "{slug}", 1)},
	{ReasonTerminalMalformed, "set accepted_at / merged_at in kitty-specs/{slug}/meta.json to an ISO-8601 string (or remove the key), then rerun"},
	{ReasonMetaDuplicateKeys, "remove the repeated key from kitty-specs/{slug}/meta.json so each key appears once, then rerun"},
	{ReasonLegacyUndecidable, "repair the unreadable file the reason names (kitty-specs/{slug}/tasks.md or a WP file under kitty-specs/{slug}/tasks/), then rerun"},
	{ReasonAbsentMissionID, "repair kitty-specs/{slug}/meta.json if it is missing or not valid JSON; otherwise run spec-kitty migrate backfill-identity --mission {slug}"},
}

// RemedyFor is remedy_for: the reason-specific remedy for a failing verdict.
func RemedyFor(v Verdict) string {
	reason := ""
	if len(v.Reasons) > 0 {
		reason = v.Reasons[0]
	}
	for _, r := range remedyByReason {
		if strings.HasPrefix(reason, r.reason) {
			return strings.ReplaceAll(r.text, "{slug}", v.Slug)
		}
	}
	return fmt.Sprintf(remedyTemplate, v.Slug)
}

// GuardVerdict is the decision over every mission a diff touches.
type GuardVerdict struct {
	Passed     bool
	Touched    []string
	Failures   []Verdict
	NonMission []string
	Exempt     []Verdict
}

// TouchedMissionSlugs is touched_mission_slugs.
func TouchedMissionSlugs(paths []string) []string {
	set := map[string]bool{}
	for _, raw := range paths {
		var parts []string
		if strings.HasPrefix(raw, "/") {
			parts = append(parts, "/")
		}
		for _, s := range strings.Split(raw, "/") {
			if s != "" && s != "." {
				parts = append(parts, s)
			}
		}
		if len(parts) >= 2 && parts[0] == "kitty-specs" {
			set[parts[1]] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// AssertSafePathSegment is core.paths.assert_safe_path_segment.
func AssertSafePathSegment(v string) error {
	stripped := strings.TrimFunc(v, isPySpace)
	r := pyjson.ReprString(v)
	switch {
	case stripped == "":
		return fmt.Errorf("Not a safe path segment: %s — value must not be empty or whitespace-only.", r)
	case v != stripped:
		return fmt.Errorf("Not a safe path segment: %s — value must not contain leading or trailing whitespace.", r)
	case strings.Contains(stripped, ".."):
		return fmt.Errorf("Not a safe path segment: %s — value must not contain '..' (traversal guard).", r)
	case strings.HasPrefix(stripped, "."):
		return fmt.Errorf("Not a safe path segment: %s — value must not begin with '.' (traversal guard).", r)
	case strings.ContainsAny(stripped, `/\`):
		return fmt.Errorf("Not a safe path segment: %s — value must not contain path separators.", r)
	case !safeSegment.MatchString(stripped) || strings.HasSuffix(stripped, "\n"):
		return fmt.Errorf("Not a safe path segment: %s — value must match the canonical segment grammar "+
			"(ASCII alphanumerics, hyphens, underscores, and interior dots only; must begin with an alphanumeric character).", r)
	}
	return nil
}

func isPySpace(r rune) bool {
	return r == ' ' || (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f) || r == 0x85 || r == 0xa0 || r == 0x1680 ||
		(r >= 0x2000 && r <= 0x200a) || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}

func gitLines(dir string, args ...string) ([]string, bool) {
	r, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil || r.Code != 0 {
		return nil, false
	}
	var out []string
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, true
}

func removedDirectoryVerdict(root, slug, mergeBase string) *Verdict {
	removed := "mission directory not found in corpus (ambiguous or removed)"
	if mergeBase == "" {
		return &Verdict{Slug: slug, Reasons: []string{removed}}
	}
	entries, ok := gitLines(root, "ls-tree", "--full-tree", "-r", "--name-only", "-z", mergeBase, "--", "kitty-specs/"+slug+"/")
	if !ok {
		return &Verdict{Slug: slug, Reasons: []string{"could not read merge-base tree " + mergeBase + " for the removed directory"}}
	}
	if len(entries) == 0 {
		return &Verdict{Slug: slug, Reasons: []string{removed}}
	}
	var carried []string
	for _, e := range entries {
		if n := filepath.Base(e); n == "meta.json" || n == "status.events.jsonl" {
			carried = append(carried, n)
		}
	}
	if len(carried) > 0 {
		sort.Strings(carried)
		short := mergeBase
		if len(short) > 12 {
			short = short[:12]
		}
		return &Verdict{Slug: slug, Reasons: []string{fmt.Sprintf("mission directory removed from corpus but carried %s at merge-base %s", strings.Join(carried, ", "), short)}}
	}
	return nil
}

// Evaluate is evaluate_touched_missions.
func Evaluate(root string, changed []string, mergeBase string) GuardVerdict {
	corpus := filepath.Join(root, "kitty-specs")
	gv := GuardVerdict{Touched: TouchedMissionSlugs(changed)}
	for _, slug := range gv.Touched {
		if err := AssertSafePathSegment(slug); err != nil {
			gv.Failures = append(gv.Failures, Verdict{Slug: slug, Reasons: []string{"unsafe mission slug in diff: " + err.Error()}})
			continue
		}
		dir := filepath.Join(corpus, slug)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			if v := removedDirectoryVerdict(root, slug, mergeBase); v != nil {
				gv.Failures = append(gv.Failures, *v)
			} else {
				gv.NonMission = append(gv.NonMission, slug)
			}
			continue
		}
		v := IsCutOver(dir)
		switch {
		case !v.CutOver:
			gv.Failures = append(gv.Failures, v)
		case v.Exempt:
			gv.Exempt = append(gv.Exempt, v)
		}
	}
	gv.Passed = len(gv.Failures) == 0
	return gv
}

func strs(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func payload(gv GuardVerdict) *jsonx.Object {
	failures := []any{}
	for _, f := range gv.Failures {
		failures = append(failures, jsonx.NewObject("slug", f.Slug, "reasons", strs(f.Reasons), "remedy", RemedyFor(f)))
	}
	exempt := []any{}
	for _, e := range gv.Exempt {
		exempt = append(exempt, jsonx.NewObject("slug", e.Slug, "reasons", strs(e.Reasons)))
	}
	return jsonx.NewObject("passed", gv.Passed, "touched_slugs", strs(gv.Touched), "non_mission_slugs", strs(gv.NonMission),
		"failures", failures, "exempt", exempt)
}

func report(w io.Writer, gv GuardVerdict) {
	p := func(s string) { uconsole.Print(w, s) }
	p("\n[bold]cutover-guard report[/bold]")
	p(fmt.Sprintf("  Missions touched by diff : %d", len(gv.Touched)))
	p(fmt.Sprintf("  Pre-accept (exempt)      : %d", len(gv.Exempt)))
	p(fmt.Sprintf("  Un-cut-over              : %d", len(gv.Failures)))
	if len(gv.NonMission) > 0 {
		p("  Removed non-mission dirs : " + strings.Join(gv.NonMission, ", "))
	}
	if len(gv.Touched) == 0 {
		p("\n[green]No kitty-specs/ missions touched by this diff.[/green]")
		return
	}
	if len(gv.Exempt) > 0 {
		p("\n[bold]Pre-accept Mission(s), exempt from the stamp check:[/bold]")
		for _, e := range gv.Exempt {
			p("  " + e.Slug + ": " + PreAcceptExemptNote)
		}
	}
	if gv.Passed {
		p("\n[green]All diff-touched Missions pass the cut-over check.[/green]")
		return
	}
	p("\n[red]Un-cut-over Mission(s) block this diff:[/red]")
	for _, f := range gv.Failures {
		reason := strings.Join(f.Reasons, "; ")
		if reason == "" {
			reason = "not cut over"
		}
		p("  [red]" + f.Slug + "[/red]: " + reason)
		p("    remedy: " + RemedyFor(f))
	}
}

func fail(ctx *cli.Context, msg string) error {
	uconsole.Print(ctx.Stderr, "[red]Error:[/red] "+msg)
	return cli.Exit(1)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

func run(ctx *cli.Context) error {
	baseSet, pathsSet := ctx.IsSet("base-ref"), ctx.IsSet("paths-from")
	if baseSet == pathsSet {
		return fail(ctx, "exactly one of --base-ref or --paths-from is required.")
	}
	root := project.Locate("")
	if root == "" {
		return fail(ctx, "Could not locate project root. No .kittify/ directory found in any parent directory.")
	}
	var changed []string
	mergeBase := ""
	if pathsSet {
		pf := ctx.String("paths-from")
		b, err := os.ReadFile(pf)
		if err != nil {
			return fail(ctx, fmt.Sprintf("could not read --paths-from file %s: %s", pf, pyOSError(err, pf)))
		}
		text, derr := pyjson.DecodeUTF8(b)
		if derr != nil {
			fmt.Fprintf(ctx.Stderr, "Traceback (most recent call last):\nUnicodeDecodeError: %v\n", derr)
			return cli.Exit(1)
		}
		for _, l := range splitLines(text) {
			if t := strings.TrimFunc(l, isPySpace); t != "" {
				changed = append(changed, t)
			}
		}
	} else {
		base := ctx.String("base-ref")
		r, err := gitx.Exec(root, nil, nil, "merge-base", base, "HEAD")
		mb := strings.TrimSpace(r.Stdout)
		if err != nil || r.Code != 0 || mb == "" {
			return fail(ctx, fmt.Sprintf("could not resolve a merge-base between %s and HEAD (unknown ref, or the repository has no common ancestor)", pyjson.ReprString(base)))
		}
		paths, ok := gitLines(root, "-c", "diff.relative=false", "diff", "--name-only", "-z", "-M", mb, "HEAD", "--", "kitty-specs")
		if !ok {
			return fail(ctx, "git diff failed against merge-base "+mb)
		}
		changed, mergeBase = paths, mb
	}
	gv := Evaluate(root, changed, mergeBase)
	if ctx.Bool("json") {
		ctx.Println(jsonx.Dumps(payload(gv), jsonx.Pretty))
	} else {
		report(ctx.Stdout, gv)
	}
	if !gv.Passed {
		return cli.Exit(1)
	}
	return nil
}

func pyOSError(err error, p string) string {
	switch {
	case os.IsNotExist(err):
		return "[Errno 2] No such file or directory: " + pyjson.ReprString(p)
	case os.IsPermission(err):
		return "[Errno 13] Permission denied: " + pyjson.ReprString(p)
	case strings.Contains(err.Error(), "is a directory"):
		return "[Errno 21] Is a directory: " + pyjson.ReprString(p)
	}
	return err.Error()
}
