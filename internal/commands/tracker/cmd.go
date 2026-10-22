// Package tracker registers the `spec-kitty tracker` command group
// (providers, bind, status, unbind, map add/list, sync pull/push/run/publish),
// a port of specify_cli.cli.commands.tracker over internal/tracker.
package tracker

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	tr "github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	providersLong = "List supported tracker providers.\n\nLocal providers use direct connectors with locally stored credentials. The\nformer SaaS/hosted providers (linear, jira, github, gitlab) are no longer\nsupported (#6164) and are listed only to explain the refusal a bind for one\nof them produces.\n\nThis command is purely informational and prints the hard-coded provider\ncategories."
	bindLong      = "Bind the current project to a local issue tracker (beads, fp).\n\nRequires --provider and --workspace; --credential supplies connector\ncredentials stored locally."
	statusLong    = "Show tracker binding and sync status.\n\nDisplays local cache statistics and configuration for the bound local\nprovider."
	mapAddLong    = "Add or update a WP-to-external issue mapping.\n\nStores the mapping in the local SQLite database."
	publishLong   = "Publish local tracker snapshot.\n\nLocal providers (beads/fp) have no snapshot-publish transport; the facade\nraises a clean error directing the operator to ``tracker sync push``."
)

func jsonFlag(help string) *cli.Flag { return &cli.Flag{Name: "json", Kind: cli.Bool, Help: help} }

// limitFlag is typer's IntRange(min=1, max=10000) option.
func limitFlag(help string) *cli.Flag {
	return &cli.Flag{Name: "limit", Kind: cli.Int, Min: cli.Bound(1), Max: cli.Bound(10000), Default: 100, Help: help}
}

// parseLimit returns --limit (validated by the framework like click.IntRange).
func parseLimit(ctx *cli.Context) (int, error) { return ctx.Int("limit"), nil }

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "tracker", "Task tracker commands")
		g.Long = "Task tracker commands"
		g.Add(
			&cli.Command{Name: "providers", Short: "List supported tracker providers.", Long: providersLong,
				Flags: []*cli.Flag{jsonFlag("Render provider list as JSON")}, Run: runProviders},
			&cli.Command{Name: "bind", Short: "Bind the current project to a local issue tracker (beads, fp).", Long: bindLong,
				Flags: []*cli.Flag{
					{Name: "provider", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Provider name (beads, fp)"},
					{Name: "workspace", Kind: cli.String, Metavar: "<str>", Help: "Provider workspace/team/project identifier"},
					{Name: "ownership-mode", Kind: cli.String, Metavar: "<str>", Help: "Ownership mode: external_authoritative | spec_kitty_authoritative | split_ownership"},
					{Name: "field-owner", Kind: cli.StringList, Metavar: "<str>", Help: "Split ownership mapping: field=owner"},
					{Name: "credential", Kind: cli.StringList, Metavar: "<str>", Help: "Provider credential key/value: key=value"},
				}, Run: runBind},
			&cli.Command{Name: "status", Short: "Show tracker binding and sync status.", Long: statusLong,
				Flags: []*cli.Flag{jsonFlag("Render status as JSON")}, Run: runStatus},
			&cli.Command{Name: "unbind", Short: "Remove tracker binding for this project.", Run: runUnbind},
		)
		m := &cli.Command{Name: "map", Short: "Work-package mapping commands", NoArgsIsError: true}
		m.Add(
			&cli.Command{Name: "add", Short: "Add or update a WP-to-external issue mapping.", Long: mapAddLong,
				Flags: []*cli.Flag{
					{Name: "wp-id", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Work package ID (e.g., WP01)"},
					{Name: "external-id", Kind: cli.String, Metavar: "<str>", Required: true, Help: "External issue ID"},
					{Name: "external-key", Kind: cli.String, Metavar: "<str>", Help: "External issue key"},
					{Name: "external-url", Kind: cli.String, Metavar: "<str>", Help: "External issue URL"},
				}, Run: runMapAdd},
			&cli.Command{Name: "list", Short: "List tracker mappings from the local SQLite database.",
				Flags: []*cli.Flag{jsonFlag("Render mappings as JSON")}, Run: runMapList},
		)
		s := &cli.Command{Name: "sync", Short: "Tracker synchronization commands", NoArgsIsError: true}
		s.Add(
			&cli.Command{Name: "pull", Short: "Pull tracker updates into the local cache via the direct connector.",
				Flags: []*cli.Flag{limitFlag(""), jsonFlag("Render sync result as JSON")}, Run: syncRunner("pull")},
			&cli.Command{Name: "push", Short: "Push local mutations to the upstream provider via the direct connector.",
				Flags: []*cli.Flag{limitFlag("Max items"), jsonFlag("Render sync result as JSON")}, Run: syncRunner("push")},
			&cli.Command{Name: "run", Short: "Run pull+push synchronization in one operation via the direct connector.",
				Flags: []*cli.Flag{limitFlag(""), jsonFlag("Render sync result as JSON")}, Run: syncRunner("run")},
			&cli.Command{Name: "publish", Short: "Publish local tracker snapshot.", Long: publishLong,
				Flags: []*cli.Flag{jsonFlag("Render publish result as JSON")}, Run: runPublish},
		)
		g.Add(m, s)
	})
}

func printJSON(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.SortedPretty))
}

// fail renders an error through the shared CLI boundary (_run_or_exit):
// RuntimeError/ValueError become one stderr line (+ a JSON failure object
// on stdout in --json mode); anything else is an uncaught traceback.
func fail(ctx *cli.Context, err error, jsonMode bool) error {
	var te *tr.Error
	var ve *tr.ValueError
	if errors.As(err, &te) || errors.As(err, &ve) {
		msg := err.Error()
		fmt.Fprintln(ctx.Stderr, msg)
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("ok", false, "error", msg))
		}
		return cli.Exit(1)
	}
	return crash(ctx, err)
}

// commandFrames are the upstream command-level traceback frames
// (specify_cli/cli/commands/tracker.py): the command function at its
// _run_or_exit call, and the line of its pre-boundary require_repo_root().
var commandFrames = map[string]struct {
	fn       string
	boundary int
	root     int
}{
	"providers":    {"providers_command", 142, 0},
	"bind":         {"bind_command", 233, 0},
	"status":       {"status_command", 275, 0},
	"map add":      {"map_add_command", 304, 0},
	"map list":     {"map_list_command", 333, 0},
	"sync pull":    {"sync_pull_command", 363, 347},
	"sync push":    {"sync_push_command", 393, 377},
	"sync run":     {"sync_run_command", 425, 407},
	"sync publish": {"sync_publish_command", 455, 442},
	"unbind":       {"unbind_command", 471, 0},
}

func commandKey(ctx *cli.Context) string {
	path := ctx.Cmd.Path()
	if i := strings.Index(path, "tracker "); i >= 0 {
		return path[i+len("tracker "):]
	}
	return path
}

// crash renders an uncaught exception raised inside the command's
// _run_or_exit boundary as upstream's rich traceback.
func crash(ctx *cli.Context, err error) error {
	var fr []rich.Frame
	if cf, ok := commandFrames[commandKey(ctx)]; ok {
		fr = []rich.Frame{{Name: cf.fn, Line: cf.boundary}, {Name: "_run_or_exit", Line: 98}}
	}
	var c *tr.Crash
	if errors.As(tr.AsCrash(err), &c) {
		rich.PrintTraceback(ctx.Stderr, c.Exception(fr...))
	} else {
		rich.PrintTraceback(ctx.Stderr, rich.ParseException(err.Error(), fr...))
	}
	return cli.Exit(1)
}

func service(ctx *cli.Context, jsonMode bool) (*tr.Service, error) {
	root, err := tr.RequireRepoRoot()
	if err != nil {
		return nil, fail(ctx, err, jsonMode)
	}
	return tr.NewService(root), nil
}

func runProviders(ctx *cli.Context) error {
	local, removed := tr.SortedCopy(tr.LocalProviders), tr.SortedCopy(tr.RemovedProviders)
	if ctx.Bool("json") {
		printJSON(ctx.Stdout, jsonx.NewObject("local", strList(local), "removed", strList(removed)))
		return nil
	}
	w := ctx.Stdout
	fmt.Fprintln(w, "Supported providers:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Local (direct connectors, credentials stored locally):")
	for _, p := range local {
		fmt.Fprintf(w, "    - %s\n", p)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Removed (no longer supported):")
	for _, p := range removed {
		fmt.Fprintf(w, "    - %s\n", p)
	}
	return nil
}

func strList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func runBind(ctx *cli.Context) error {
	provider := tr.NormalizeProvider(ctx.String("provider"))
	if tr.IsRemoved(provider) {
		fmt.Fprintf(ctx.Stderr, "Error: Provider '%s' is no longer supported.\n\n", provider)
		return cli.Exit(1)
	}
	if !tr.IsLocal(provider) {
		return fail(ctx, &tr.Error{Msg: fmt.Sprintf("Unknown provider '%s'. Supported: %s", provider, strings.Join(tr.SortedCopy(tr.LocalProviders), ", "))}, false)
	}
	workspace := ctx.String("workspace")
	if workspace == "" {
		fmt.Fprint(ctx.Stderr, "Error: --workspace is required for local providers.\n\n")
		return cli.Exit(1)
	}
	selected := "external_authoritative"
	if v := ctx.String("ownership-mode"); v != "" {
		selected = v
	}
	mode := strings.ToLower(strings.TrimSpace(selected))
	valid := false
	for _, m := range tr.OwnershipModes {
		valid = valid || m == mode
	}
	if !valid {
		return fail(ctx, &tr.Error{Msg: fmt.Sprintf("Invalid ownership mode '%s'. Expected one of: %s", selected, strings.Join(tr.OwnershipModes, ", "))}, false)
	}
	owners, err := tr.ParseKVPairs(ctx.Strings("field-owner"))
	if err != nil {
		return fail(ctx, err, false)
	}
	creds, err := tr.ParseKVPairs(ctx.Strings("credential"))
	if err != nil {
		return fail(ctx, err, false)
	}
	svc, err := service(ctx, false)
	if err != nil {
		return err
	}
	cfg, err := svc.Bind(provider, workspace, mode, owners, creds)
	if err != nil {
		return fail(ctx, err, false)
	}
	w := ctx.Stdout
	fmt.Fprintln(w, "Tracker binding saved")
	fmt.Fprintf(w, "- provider: %s\n", cfg.Provider)
	fmt.Fprintf(w, "- workspace: %s\n", cfg.Workspace)
	fmt.Fprintf(w, "- ownership_mode: %s\n", cfg.OwnershipMode)
	fmt.Fprintf(w, "- field_owners: %d\n", cfg.FieldOwners.Len())
	fmt.Fprintf(w, "- credentials_saved: %s\n", yesNo(creds.Len() > 0))
	return nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// pyStr renders a payload value like an f-string interpolation.
func pyStr(o *jsonx.Object, key string) string {
	v, ok := o.Get(key)
	if !ok || v == nil {
		return "None"
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}

func runStatus(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	svc, err := service(ctx, jsonMode)
	if err != nil {
		return err
	}
	payload, err := svc.Status()
	if err != nil {
		return fail(ctx, err, jsonMode)
	}
	if jsonMode {
		printJSON(ctx.Stdout, payload)
		return nil
	}
	w := ctx.Stdout
	if payload.GetBool("inert") {
		fmt.Fprintf(w, "Tracker %s\n", payload.GetString("message"))
		return nil
	}
	if !payload.GetBool("configured") {
		fmt.Fprintln(w, "Tracker is not configured")
		return nil
	}
	fmt.Fprintln(w, "Tracker status")
	for _, k := range []string{"provider", "workspace", "ownership_mode", "db_path", "issue_count", "mapping_count"} {
		fmt.Fprintf(w, "- %s: %s\n", k, pyStr(payload, k))
	}
	fmt.Fprintf(w, "- credentials_present: %s\n", yesNo(payload.GetBool("credentials_present")))
	return nil
}

func runUnbind(ctx *cli.Context) error {
	svc, err := service(ctx, false)
	if err != nil {
		return err
	}
	if err := svc.Unbind(); err != nil {
		return fail(ctx, err, false)
	}
	fmt.Fprintln(ctx.Stdout, "Tracker binding removed")
	return nil
}

func optString(ctx *cli.Context, name string) *string {
	if !ctx.IsSet(name) {
		return nil
	}
	v := ctx.String(name)
	return &v
}

func runMapAdd(ctx *cli.Context) error {
	svc, err := service(ctx, false)
	if err != nil {
		return err
	}
	wp, ext := ctx.String("wp-id"), ctx.String("external-id")
	if err := svc.MapAdd(wp, ext, optString(ctx, "external-key"), optString(ctx, "external-url")); err != nil {
		return fail(ctx, err, false)
	}
	fmt.Fprintf(ctx.Stdout, "Mapping saved: %s -> %s\n", wp, ext)
	return nil
}

func runMapList(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	svc, err := service(ctx, jsonMode)
	if err != nil {
		return err
	}
	maps, err := svc.MapList()
	if err != nil {
		return fail(ctx, err, jsonMode)
	}
	if jsonMode {
		rows := []any{}
		for _, m := range maps {
			rows = append(rows, m.JSON())
		}
		printJSON(ctx.Stdout, jsonx.NewObject("mappings", rows))
		return nil
	}
	if len(maps) == 0 {
		fmt.Fprintln(ctx.Stdout, "No mappings found")
		return nil
	}
	fmt.Fprintln(ctx.Stdout, "Mappings")
	for _, m := range maps {
		key := m.ExternalID
		if m.ExternalKey != nil && *m.ExternalKey != "" {
			key = *m.ExternalKey
		}
		fmt.Fprintf(ctx.Stdout, "- %s: %s:%s\n", m.WPID, m.System, key)
	}
	return nil
}

// syncRoot pins the project root before the error boundary, as upstream's
// sync commands do (a failure here is an uncaught TrackerConfigError).
func syncRoot(ctx *cli.Context) (string, error) {
	root, err := tr.RequireRepoRoot()
	if err != nil {
		var fr []rich.Frame
		if cf, ok := commandFrames[commandKey(ctx)]; ok {
			fr = []rich.Frame{{Name: cf.fn, Line: cf.root}, {Name: "require_repo_root", Line: 289}}
		}
		rich.Traceback(ctx.Stderr, "TrackerConfigError", err.Error(), fr...)
		return "", cli.Exit(1)
	}
	return root, nil
}

func syncRunner(op string) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		limit, err := parseLimit(ctx)
		if err != nil {
			return err
		}
		root, err := syncRoot(ctx)
		if err != nil {
			return err
		}
		jsonMode := ctx.Bool("json")
		payload, err := tr.NewService(root).Sync(op, limit)
		if err != nil {
			return fail(ctx, err, jsonMode)
		}
		if jsonMode {
			printJSON(ctx.Stdout, payload)
			return nil
		}
		stats := payload.GetObject("stats")
		n := func(k string) int { v, _ := stats.GetInt(k); return v }
		conflicts, errs := len(payload.GetList("conflicts")), len(payload.GetList("errors"))
		provider := payload.GetString("provider")
		w := ctx.Stdout
		switch op {
		case "pull":
			fmt.Fprintf(w, "Pulled from %s\n- created: %d\n- updated: %d\n", provider, n("pulled_created"), n("pulled_updated"))
		case "push":
			fmt.Fprintf(w, "Pushed to %s\n- created: %d\n- updated: %d\n", provider, n("pushed_created"), n("pushed_updated"))
		default:
			fmt.Fprintf(w, "Sync run completed (%s)\n- pulled_created: %d\n- pulled_updated: %d\n- pushed_created: %d\n- pushed_updated: %d\n",
				provider, n("pulled_created"), n("pulled_updated"), n("pushed_created"), n("pushed_updated"))
		}
		fmt.Fprintf(w, "- skipped: %d\n- conflicts: %d\n- errors: %d\n", n("skipped"), conflicts, errs)
		return nil
	}
}

func runPublish(ctx *cli.Context) error {
	root, err := syncRoot(ctx)
	if err != nil {
		return err
	}
	jsonMode := ctx.Bool("json")
	if _, err := tr.NewService(root).SyncPublish(); err != nil {
		return fail(ctx, err, jsonMode)
	}
	return nil
}
