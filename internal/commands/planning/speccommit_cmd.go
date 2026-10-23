package planning

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

const specCommitLong = `Commit spec artifacts to the mission's resolved placement.

SPEC is a primary/planning artifact: it lands on the mission's primary target
branch for every topology. On an unprotected or flattened primary the commit
is direct. On a PROTECTED primary the commit is refused (there is no fallback
surface); recover by either creating/checking out a non-protected feature
branch ('spec-kitty agent mission create --start-branch <topic-branch>') or
setting SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS=1 to commit on the current
branch.

Pass individual FILES, not directories.`

const ownedCheckoutHelp = "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."

func specCommitCommand() *cli.Command {
	return &cli.Command{
		Name:  "spec-commit",
		Short: "Commit spec artifacts to the mission's resolved placement.",
		Long:  specCommitLong,
		Args: []*cli.Arg{{Name: "files", Metavar: "{files}", Required: true, Variadic: true,
			Type: "<path>",
			Help: "Spec artifacts to commit (absolute or relative paths). Must belong to the mission resolved via --mission or the kitty-specs/<slug>/ path."}},
		Flags: []*cli.Flag{
			{Name: "message", Short: "m", Kind: cli.StringList, Metavar: "<str>", Required: true, Help: messageHelp},
			{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g. '001-my-mission'). When omitted, the slug is derived from the first file argument's kitty-specs/<slug>/ path."},
			{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "Short TARGET branch name for this Mission (not the repository-root checkout). Passed to the commit router for placement resolution and, on an --owned-checkout run, as the owned checkout's target override. No longer used for a post-commit fast-forward -- that best-effort advance was retired (FR-008). Optional."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON."},
			{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
		},
		Run: runSpecCommit,
	}
}

func specPayload(success, committed bool) *jsonx.Object {
	res := "error"
	if success {
		res = "success"
	}
	return jsonx.NewObject("result", res, "success", success, "committed", committed)
}

// deriveMissionSlug is _derive_mission_slug.
func deriveMissionSlug(pathArg, missionOpt string) string {
	if missionOpt != "" {
		return strings.TrimSpace(missionOpt)
	}
	if pathArg == "" {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(pathArg)), "/")
	for i, p := range parts {
		if p == "kitty-specs" {
			if i+1 < len(parts) {
				return parts[i+1]
			}
			break
		}
	}
	return parts[len(parts)-1]
}

func runSpecCommit(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error {
		if jsonMode {
			printPretty(ctx.Stdout, specPayload(false, false).Set("error", msg))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	message, err := joinMessage(ctx.Strings("message"))
	if err != nil {
		return fail(err.Error())
	}
	if ctx.String("owned-checkout") != "" {
		return fail("--owned-checkout is not supported by this build")
	}
	root := project.Locate("")
	if root == "" {
		return fail("Could not find repository root")
	}
	files := ctx.ArgList("files")
	first := ""
	if len(files) > 0 {
		first = files[0]
	}
	slug := deriveMissionSlug(first, ctx.String("mission"))
	var abs []string
	for _, f := range files {
		abs = append(abs, safecommit.ResolveCommitPath(root, f))
	}
	var dirs []string
	for _, a := range abs {
		if isRealDir(a) {
			dirs = append(dirs, a)
		}
	}
	if len(dirs) > 0 {
		noun := "directory"
		if len(dirs) > 1 {
			noun = "directories"
		}
		return fail(fmt.Sprintf("spec-commit takes individual file paths, not directories. Received %s: %s. Pass the specific files to commit instead.", noun, strings.Join(dirs, ", ")))
	}
	if slug == "" {
		return fail("Cannot resolve mission slug. Pass --mission <slug> or provide a kitty-specs/<slug>/ path as the first argument.")
	}
	dir, rerr := mission.ResolveHandle(root, slug)
	if _, notFound := rerr.(*mission.NotFoundError); notFound {
		// An unresolvable handle reads no meta.json: placement falls back to
		// the repository default branch (get_feature_target_branch).
		dir, rerr = slug, nil
	}
	if rerr != nil {
		code, msg := actionContextError(rerr, slug)
		if jsonMode {
			printCompact(ctx.Stdout, specPayload(false, false).Set("error", msg).Set("error_code", code))
		} else {
			say(ctx.Stdout, fmt.Sprintf("[red]%s:[/red] %s", code, msg))
		}
		return cli.Exit(1)
	}
	res := safecommit.CommitForMission(root, dir, abs, message, mission.KindSpec, safecommit.RouterOptions{PrimaryRef: ""})
	return renderRouterResult(ctx, jsonMode, res, abs, root)
}

// actionContextError maps a mission resolution failure to the
// ActionContextError (code, message) upstream raises.
func actionContextError(err error, handle string) (string, string) {
	switch e := err.(type) {
	case *mission.NotFoundError:
		return "MISSION_NOT_FOUND", e.Error()
	case *mission.AmbiguousError:
		return "MISSION_AMBIGUOUS_SELECTOR", e.Error()
	case *mission.SelectorAmbiguousError:
		return e.Code(), e.Error()
	}
	return "MISSION_NOT_FOUND", err.Error()
}

type argFate struct {
	path, fate, surface, reason string
	hasReason                   bool
}

// argumentFates is _argument_fates.
func argumentFates(abs []string, res *safecommit.RouterResult, root string) []argFate {
	type entry struct {
		path string
		f    argFate
	}
	var entries []entry
	for _, o := range res.Surfaces {
		for _, p := range o.Committed {
			entries = append(entries, entry{p, argFate{fate: "committed", surface: o.Surface}})
		}
		for _, f := range o.Skipped {
			fate := "unchanged"
			if f.Reason == safecommit.ReasonCoordRecordInRoot {
				fate = "skipped"
			}
			e := argFate{fate: fate, surface: o.Surface, reason: f.Reason, hasReason: true}
			entries = append(entries, entry{f.Path, e})
			if f.OwningPath != "" {
				entries = append(entries, entry{f.OwningPath, e})
			}
		}
		for _, f := range o.Refused {
			e := argFate{fate: "refused", surface: o.Surface, reason: f.Reason, hasReason: true}
			entries = append(entries, entry{f.Path, e})
			if f.OwningPath != "" {
				entries = append(entries, entry{f.OwningPath, e})
			}
		}
	}
	var out []argFate
	for _, a := range abs {
		rel := relForFate(root, a)
		var m *argFate
		for i := range entries {
			if entries[i].path == rel {
				m = &entries[i].f
				break
			}
		}
		if m == nil {
			for i := range entries {
				if strings.HasSuffix(entries[i].path, "/"+rel) {
					m = &entries[i].f
					break
				}
			}
		}
		f := argFate{fate: "refused", reason: safecommit.ReasonPathUnroutable, hasReason: true}
		if m != nil {
			f = *m
		}
		f.path = rel
		out = append(out, f)
	}
	return out
}

func relForFate(root, p string) string {
	r := safecommit.ResolveCommitPath(root, p)
	if rel, err := filepath.Rel(root, r); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func (a argFate) payload() *jsonx.Object {
	o := jsonx.NewObject("path", a.path, "fate", a.fate, "surface", optString(a.surface))
	if a.fate != "committed" {
		o.Set("reason", optString(a.reason))
	}
	return o
}

func renderRouterResult(ctx *cli.Context, jsonMode bool, res *safecommit.RouterResult, abs []string, root string) error {
	args := argumentFates(abs, res, root)
	success := res.Status == safecommit.StatusCommitted || res.Status == safecommit.StatusUnchanged
	if len(res.Surfaces) > 0 {
		for _, s := range res.Surfaces {
			if s.Status == "refused" || s.Status == "error" {
				success = false
			}
		}
		for _, a := range args {
			if a.fate == "refused" {
				success = false
			}
		}
	}
	argsPayload := []any{}
	for _, a := range args {
		argsPayload = append(argsPayload, a.payload())
	}
	var payload *jsonx.Object
	switch res.Status {
	case safecommit.StatusCommitted:
		payload = specPayload(success, true).Set("placement_ref", res.PlacementRef)
		if res.CommitHash != "" {
			payload.Set("commit_hash", res.CommitHash)
		}
	case safecommit.StatusUnchanged:
		reason := res.Reason
		if reason == "" {
			reason = "no_op"
		}
		payload = specPayload(success, false).Set("placement_ref", res.PlacementRef).Set("reason", reason)
	case safecommit.StatusNoOpWrongSurface:
		actionable := res.Diagnostic
		if actionable == "" {
			actionable = "Artifact absent at resolved placement."
		}
		payload = specPayload(false, false).Set("placement_ref", res.PlacementRef).Set("error", actionable)
		if res.Diagnostic != "" {
			payload.Set("diagnostic", res.Diagnostic)
		}
	default:
		d := res.Diagnostic
		if d == "" {
			d = "Commit failed."
		}
		payload = specPayload(false, false).Set("placement_ref", res.PlacementRef).Set("error", d)
	}
	payload.Set("surfaces", res.SurfacesPayload())
	payload.Set("arguments", argsPayload)
	if jsonMode {
		printPretty(ctx.Stdout, reorderSpecPayload(payload))
	} else {
		if len(res.Surfaces) > 0 {
			printRendered(ctx.Stdout, res)
			for _, a := range args {
				if a.fate == "committed" {
					continue
				}
				suffix := ""
				if a.reason != "" {
					suffix = " (" + a.reason + ")"
				}
				sayPlain(ctx.Stdout, fmt.Sprintf("%s: %s%s", a.path, a.fate, suffix))
			}
		}
		switch res.Status {
		case safecommit.StatusCommitted, safecommit.StatusUnchanged:
			if len(res.Surfaces) == 0 {
				if res.Status == safecommit.StatusCommitted {
					say(ctx.Stdout, "[green]✓[/green] Spec artifact(s) committed to "+res.PlacementRef)
				} else {
					say(ctx.Stdout, "[dim]Spec artifact(s) unchanged, no commit needed[/dim]")
				}
			}
		case safecommit.StatusNoOpWrongSurface:
			a := res.Diagnostic
			if a == "" {
				a = "Artifact absent at resolved placement."
			}
			say(ctx.Stdout, "[red]Error:[/red] "+a)
		default:
			d := res.Diagnostic
			if d == "" {
				d = "Commit failed."
			}
			say(ctx.Stdout, "[red]Error:[/red] "+d)
		}
	}
	if !success {
		return cli.Exit(1)
	}
	return nil
}

// reorderSpecPayload keeps _payload's key order: result, success, committed,
// placement_ref, commit_hash, error, diagnostic, reason, surfaces, arguments.
func reorderSpecPayload(p *jsonx.Object) *jsonx.Object {
	out := &jsonx.Object{}
	for _, k := range []string{"result", "success", "committed", "placement_ref", "commit_hash", "error", "diagnostic", "reason", "surfaces", "arguments"} {
		if v, ok := p.Get(k); ok {
			out.Set(k, v)
		}
	}
	return out
}
