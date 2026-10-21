// Package closeout registers the acceptance, merge and closeout commands:
// `agent acceptance-verdict`, `agent mission acceptance-verdict|accept|merge`,
// `accept`, `consolidate`, `review`, `retrospect *`, `agent retrospect *`,
// `archive *` and `agent release prep`.
package closeout

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const (
	agentHelp   = "Commands for AI agents to execute spec-kitty mission actions programmatically"
	missionHelp = "Mission lifecycle commands for AI agents"
)

var registrations []func(root, agent, missionG *cli.Command)

func addCommands(fn func(root, agent, missionG *cli.Command)) {
	registrations = append(registrations, fn)
}

func init() {
	app.Register(func(root *cli.Command) {
		agent := app.Group(root, "agent", agentHelp)
		missionG := app.Group(agent, "mission", missionHelp)
		for _, fn := range registrations {
			fn(root, agent, missionG)
		}
	})
}

// compact is json.dumps(x) defaults.
var compact = jsonx.Options{Indent: -1, EnsureASCII: true}

func printJSON(w io.Writer, v any)       { fmt.Fprintln(w, jsonx.Dumps(v, compact)) }
func printJSONPretty(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty)) }

// say is console.print(markup).
func say(w io.Writer, s string) { rich.Print(w, s) }

// sayLiteral is console.print(prefixMarkup + escape(literal)) / markup=False.
func sayLiteral(w io.Writer, prefixMarkup, literal string) {
	fmt.Fprintln(w, rich.Wrap(rich.Strip(prefixMarkup)+literal, rich.Width()))
}

// locateRoot is find_repo_root / locate_project_root.
func locateRoot() string { return project.Locate("") }

// resolveError renders a mission resolution failure the way
// selector_resolution.resolve_mission_handle does.
func resolveError(ctx *cli.Context, err error, jsonMode bool) error {
	switch e := err.(type) {
	case *mission.SelectorAmbiguousError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", e.Code(), "error", e.Error(),
				"handle", e.Handle, "candidates", e.Candidates))
			return cli.Exit(1)
		}
		sayLiteral(ctx.Stderr, "Error: ", e.Error())
		return cli.Exit(2)
	case *mission.AmbiguousError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_AMBIGUOUS_SELECTOR", "error", e.Error(),
				"handle", e.Handle, "candidates", e.CandidatesJSON()))
			return cli.Exit(1)
		}
		sayLiteral(ctx.Stderr, "", e.Error())
		return cli.Exit(2)
	case *mission.NotFoundError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_NOT_FOUND", "error", e.Error(), "handle", e.Handle))
			return cli.Exit(1)
		}
		say(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		return cli.Exit(2)
	}
	return err
}

// resolveStrict is resolve_mission_handle (identity ladder only).
func resolveStrict(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	r, err := mission.ResolveIdentity(root, handle)
	if err != nil {
		return "", resolveError(ctx, err, jsonMode)
	}
	return r.Slug, nil
}

// resolveWithBareFold is resolve_mission_dir_with_bare_modern_fold: the
// literal primary dir, the bare-modern fold, then resolve_mission_handle.
func resolveWithBareFold(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	if mission.SafePathSegment(handle) {
		if st, err := os.Stat(project.Specs(root, handle)); err == nil && st.IsDir() {
			return handle, nil
		}
		name, err := mission.BareModernDirName(root, handle)
		if err != nil {
			return "", resolveError(ctx, err, jsonMode)
		}
		if name != "" {
			return name, nil
		}
	}
	return resolveStrict(ctx, root, handle, jsonMode)
}

func relPath(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return filepath.ToSlash(r)
}

func joinSorted(xs []string) string {
	c := append([]string{}, xs...)
	sort.Strings(c)
	return strings.Join(c, ", ")
}

func projectSpecs(root, slug string) string { return project.Specs(root, slug) }
