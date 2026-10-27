package closeout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func init() {
	addCommands(func(root, _, _ *cli.Command) {
		g := app.Group(root, "archive", "Archive a terminal mission (operator-invoked only).")
		g.Add(&cli.Command{
			Name:  "create",
			Short: "Archive a terminal mission (AM-1..AM-5).",
			Args:  []*cli.Arg{{Name: "mission", Metavar: "{mission}", Required: true, Help: "Mission selector (slug or mission_id)."}},
			Flags: []*cli.Flag{
				{Name: "by", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Operator identity performing the archive (required)."},
				{Name: "reason", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Why the mission is being archived (required)."},
				{Name: "json", Kind: cli.Bool, Help: "Emit the archive record as JSON."},
			},
			Run: runArchiveCreate,
		})
		g.Add(&cli.Command{
			Name:  "list",
			Short: "Enumerate archived missions (AM-3).",
			Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Emit the archive registry as JSON."}},
			Run:   runArchiveList,
		})
	})
}

func archiveRegistry(root string) string {
	return filepath.Join(root, ".kittify", "archive", "archived-missions.jsonl")
}

func jsonError(code, msg string) *jsonx.Object {
	return jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", msg))
}

var sortedPretty = jsonx.Options{Indent: 2, SortKeys: true, EnsureASCII: true}

func notInProject(ctx *cli.Context, jsonMode bool) error {
	if jsonMode {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonError("not_in_project", "Not in a spec-kitty project (no project root resolved)."), jsonx.Pretty))
	} else {
		say(ctx.Stdout, "[red]Not in a spec-kitty project (no project root resolved).[/red]")
	}
	return cli.Exit(2)
}

func runArchiveCreate(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		return notInProject(ctx, jsonMode)
	}
	handle := ctx.Arg("mission")
	dir := ""
	if mission.SafePathSegment(handle) {
		dir = projectSpecs(root, handle)
		if !dirOK(dir) {
			name, err := mission.BareModernDirName(root, handle)
			if amb, ok := err.(*mission.SelectorAmbiguousError); ok {
				if jsonMode {
					o := jsonError(amb.Code(), amb.Error())
					o.Set("handle", amb.Handle)
					o.Set("candidates", amb.Candidates)
					fmt.Fprintln(ctx.Stdout, jsonx.Dumps(o, jsonx.Pretty))
				} else {
					sayLiteral(ctx.Stdout, "[red]Error:[/red] ", amb.Error())
				}
				return cli.Exit(1)
			}
			if name != "" {
				dir = projectSpecs(root, name)
			}
		}
	}
	if !dirOK(dir) {
		msg := "Mission not found: " + handle
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonError("mission_not_found", msg), jsonx.Pretty))
		} else {
			sayLiteral(ctx.Stdout, "", msg)
		}
		return cli.Exit(2)
	}
	refuse := func(code, reason string) error {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonError(code, reason), jsonx.Pretty))
		} else {
			sayLiteral(ctx.Stdout, "[red]Archive refused ("+code+"):[/red] ", reason)
		}
		return cli.Exit(1)
	}
	by, reason := ctx.String("by"), ctx.String("reason")
	if strings.TrimSpace(by) == "" {
		return refuse("AM-4", "archiving requires an operator identity (--by)")
	}
	terminal := ""
	if snap, err := status.MaterializeSnapshot(dir); err == nil && snap.WorkPackages != nil && snap.WorkPackages.Len() > 0 {
		allDone, allTerm, anyCanceled := true, true, false
		for _, kv := range snap.WorkPackages.Pairs() {
			l := kv.Value.(*jsonx.Object).GetString("lane")
			if l != "done" {
				allDone = false
			}
			if l != "done" && l != "canceled" {
				allTerm = false
			}
			if l == "canceled" {
				anyCanceled = true
			}
		}
		if allDone {
			terminal = "merged"
		} else if allTerm && anyCanceled {
			terminal = "canceled"
		}
	}
	var invs [][2]string
	if m, _ := acceptance.ReadMatrix(dir); m != nil {
		for _, n := range m.NegativeInvariants {
			invs = append(invs, [2]string{n.GetString("invariant_id"), n.GetString("result")})
		}
	}
	if strings.TrimSpace(reason) == "" {
		return refuse("AM-1", "a stated reason is required to archive a mission")
	}
	if terminal == "" {
		return refuse("AM-1", "mission is not terminal (must be merged or canceled); resolved terminal state: None")
	}
	for _, iv := range invs {
		if iv[1] == "still_present" {
			return refuse("AM-2", "a still_present invariant must be resolved, not archived past")
		}
	}
	cleared := []any{}
	var clearedIDs []string
	if terminal == "canceled" {
		for _, iv := range invs {
			if iv[1] == acceptance.DeferredToConsolidation {
				cleared = append(cleared, jsonx.NewObject("disposition", "canceled", "invariant_id", iv[0]))
				clearedIDs = append(clearedIDs, iv[0])
			}
		}
	}
	meta := mission.ReadMetaOrEmpty(dir)
	id := meta.GetString("mission_id")
	if id == "" {
		id = meta.GetString("mission_slug")
	}
	if id == "" {
		id = filepath.Base(dir)
	}
	rec := jsonx.NewObject("mission_id", id, "archived_by", strings.TrimSpace(by), "archived_at", clock.NowUTCISO(),
		"reason", strings.TrimSpace(reason), "terminal_state_at_archive", terminal)
	p := archiveRegistry(root)
	// append_archive_record through the interim locked append (#6606).
	if err := eventlog.Append(p, []string{jsonx.Dumps(rec, jsonx.Line)}, eventlog.Options{CreateParents: true}); err != nil {
		return err
	}
	if jsonMode {
		// console.print(json.dumps(...)): markup, repr highlighting, wrapping.
		say(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("record", rec, "cleared_deferrals", cleared), sortedPretty))
		return nil
	}
	say(ctx.Stdout, fmt.Sprintf("[green]Archived[/green] %s (%s) by %s.", id, terminal, strings.TrimSpace(by)))
	if len(clearedIDs) > 0 {
		say(ctx.Stdout, "  Cancellation cleared deferrals to a canceled disposition: "+strings.Join(clearedIDs, ", "))
	}
	return nil
}

func runArchiveList(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		return notInProject(ctx, jsonMode)
	}
	var recs []*jsonx.Object
	if b, err := os.ReadFile(archiveRegistry(root)); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			o, err := jsonx.DecodeObject([]byte(l))
			if err != nil {
				continue
			}
			ok := true
			for _, k := range []string{"mission_id", "archived_by", "archived_at", "reason", "terminal_state_at_archive"} {
				if !o.Has(k) {
					ok = false
				}
			}
			if ok {
				recs = append(recs, jsonx.NewObject("mission_id", o.GetString("mission_id"), "archived_by", o.GetString("archived_by"),
					"archived_at", o.GetString("archived_at"), "reason", o.GetString("reason"), "terminal_state_at_archive", o.GetString("terminal_state_at_archive")))
			}
		}
	}
	if jsonMode {
		l := []any{}
		for _, r := range recs {
			l = append(l, r)
		}
		say(ctx.Stdout, jsonx.Dumps(l, sortedPretty))
		return nil
	}
	if len(recs) == 0 {
		say(ctx.Stdout, "No archived missions.")
		return nil
	}
	for _, r := range recs {
		sayLiteral(ctx.Stdout, "", fmt.Sprintf("%s  %s  %s  by %s  — %s", r.GetString("mission_id"), r.GetString("terminal_state_at_archive"),
			r.GetString("archived_at"), r.GetString("archived_by"), r.GetString("reason")))
	}
	return nil
}
