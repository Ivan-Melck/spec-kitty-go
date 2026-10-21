package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const materializeLong = "Regenerate all derived views from the canonical event log.\n\n" +
	"For each feature (or a single feature when --mission is given),\nwrites the following files to ``.kittify/derived/<slug>/``:\n\n" +
	"- ``status.json`` — full StatusSnapshot\n- ``board-summary.json`` — lane counts and WP lists\n" +
	"- ``progress.json`` — lane-weighted progress percentage\n- ``lifecycle.json`` — canonical active/recent/stale/abandoned mission state\n\n" +
	"A coordination-routed Mission is reduced from its coordination surface's\nevent log. To find that log this command may create the coordination\n" +
	"worktree (one ``git worktree add`` per coordination-routed Mission, done\nonce) and, for a Mission created before the coordination surface was\n" +
	"seeded, seed it. ``lanes`` and ``single_branch`` Missions are reduced from\nthe repository root checkout exactly as before. A Mission whose coordination\n" +
	"branch exists only on a remote, or whose logs diverged, is reported in the\nerror summary and the remaining Missions are still processed.\n\n" +
	"Examples::\n\n    spec-kitty materialize\n    spec-kitty materialize --mission 034-my-feature\n    spec-kitty materialize --json"

func newMaterializeCmd() *cli.Command {
	return &cli.Command{
		Name:  "materialize",
		Short: "Regenerate all derived views from the canonical event log.",
		Long:  materializeLong,
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Help: "Mission slug to materialise (all if omitted)"},
			boolFlag("json", "Output a machine-readable JSON summary"),
		},
		Run: runMaterialize,
	}
}

// statusWriteDir is placement_seam(root, slug).write_dir(STATUS_STATE): the
// coordination mission dir for coordination-routed missions (materializing
// the coordination worktree when needed), else the primary mission dir.
func statusWriteDir(root, slug string) (string, error) {
	s := coord.Resolve(root, slug)
	return s.WriteDir()
}

type materializeTarget struct{ slug, dir string }

func runMaterialize(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	out := ctx.Stdout
	root := project.Locate("")
	if root == "" {
		if jsonOut {
			emitJSON(out, jsonError("not_in_project", "Not in a spec-kitty project"))
		} else {
			cprint(out, "[red]Error:[/red] Not in a spec-kitty project")
		}
		return cli.Exit(1)
	}
	specs := project.Specs(root)
	derived := project.Kittify(root, "derived")
	os.MkdirAll(derived, 0o755)

	handle := strings.TrimSpace(ctx.String("mission"))
	var errs []string
	var targets []materializeTarget
	if handle != "" {
		slug, err := mission.ResolveHandle(root, handle)
		if err != nil {
			var amb *mission.SelectorAmbiguousError
			var ambID *mission.AmbiguousError
			switch {
			case errors.As(err, &amb):
				if jsonOut {
					o := jsonError(amb.Code(), amb.Error())
					o.Set("handle", amb.Handle).Set("candidates", strList(amb.Candidates))
					emitJSON(out, o)
				} else {
					cprint(out, "[red]Error:[/red] "+amb.Error())
				}
				return cli.Exit(1)
			case errors.As(err, &ambID):
				var c []string
				for _, x := range ambID.Candidates {
					c = append(c, x.Slug)
				}
				sa := &mission.SelectorAmbiguousError{Handle: handle, Candidates: c}
				if jsonOut {
					o := jsonError(sa.Code(), sa.Error())
					o.Set("handle", handle).Set("candidates", strList(c))
					emitJSON(out, o)
				} else {
					cprint(out, "[red]Error:[/red] "+sa.Error())
				}
				return cli.Exit(1)
			}
			slug = handle
		}
		dir, err := statusWriteDir(root, slug)
		if err != nil {
			msg := fmt.Sprintf("%s: %s: %v", handle, "StatusReadPathNotFound", err)
			if jsonOut {
				emitJSON(out, jsonError("materialize_failed", msg))
			} else {
				cprint(out, "[red]Error:[/red] "+msg)
			}
			return cli.Exit(1)
		}
		if st, serr := os.Stat(dir); serr != nil || !st.IsDir() {
			if jsonOut {
				emitJSON(out, jsonError("mission_not_found", "Mission not found: "+handle))
			} else {
				cprint(out, "[red]Error:[/red] Mission not found: "+handle)
			}
			return cli.Exit(1)
		}
		targets = append(targets, materializeTarget{filepath.Base(dir), dir})
	} else {
		entries, _ := os.ReadDir(specs)
		var names []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if st, err := os.Stat(filepath.Join(specs, e.Name())); err == nil && st.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			dir, err := statusWriteDir(root, n)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s: %v", n, "StatusReadPathNotFound", err))
				continue
			}
			targets = append(targets, materializeTarget{n, dir})
		}
	}

	processed := []any{}
	var processedSlugs []string
	var processedFiles [][]string
	for _, t := range targets {
		files, err := writeAllDerivedViews(t.dir, derived)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.slug, err))
			continue
		}
		processed = append(processed, jsonx.NewObject("mission_slug", t.slug, "files_written", strList(files), "timestamp", clock.NowUTCISO()))
		processedSlugs = append(processedSlugs, t.slug)
		processedFiles = append(processedFiles, files)
	}

	if jsonOut {
		summary := jsonx.NewObject("processed", len(processed), "errors", strList(errs), "missions", processed, "derived_dir", derived)
		if len(errs) > 0 {
			summary.Set("ok", false)
			summary.Set("error", jsonx.NewObject("code", "materialize_failed", "message", strings.Join(errs, "; ")))
			emitJSON(out, summary)
		} else {
			fmt.Fprintln(out, jsonx.Dumps(summary, jsonx.Pretty))
		}
	} else {
		if len(processed) == 0 {
			cprint(out, "[dim]No features materialised.[/dim]")
		} else {
			for i, slug := range processedSlugs {
				cprintf(out, "[green]OK[/green] %s — %s", slug, strings.Join(processedFiles[i], ", "))
			}
		}
		if len(errs) > 0 {
			fmt.Fprintln(out)
			for _, e := range errs {
				cprint(out, "[red]ERR[/red] "+e)
			}
		} else {
			cprintf(out, "\n[dim]%d mission(s) materialised to %s[/dim]", len(processed), derived)
		}
	}
	if len(errs) > 0 {
		return cli.Exit(1)
	}
	return nil
}

// writeAllDerivedViews is write_derived_views + generate_progress_json +
// generate_lifecycle_json over featureDir (the writing materialize: the
// mission's status.json is refreshed too).
func writeAllDerivedViews(featureDir, derived string) ([]string, error) {
	snap, err := status.Materialize(featureDir)
	if err != nil {
		return nil, pyStoreError(featureDir, err)
	}
	slug := snap.MissionSlug
	if slug == "" {
		slug = filepath.Base(featureDir)
	}
	outDir := filepath.Join(derived, slug)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeDerived(filepath.Join(outDir, status.SnapshotFile), snap.ToObject()); err != nil {
		return nil, err
	}
	if err := writeDerived(filepath.Join(outDir, status.BoardSummaryFile), status.BoardSummary(snap)); err != nil {
		return []string{"status.json"}, err
	}
	if err := writeDerived(filepath.Join(outDir, status.DerivedProgressFile), status.ProgressObject(snap)); err != nil {
		return nil, err
	}
	lc := status.LifecycleObject(featureDir, snap, clock.Now())
	lcDir := outDir
	if s := lc.GetString("mission_slug"); s != "" && s != slug {
		lcDir = filepath.Join(derived, s)
		if err := os.MkdirAll(lcDir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeDerived(filepath.Join(lcDir, status.DerivedLifecycle), lc); err != nil {
		return nil, err
	}
	return []string{"status.json", "board-summary.json", "progress.json", "lifecycle.json"}, nil
}

// pyStoreError re-renders an event-log JSON error with CPython's
// JSONDecodeError text ("Invalid JSON on line N: Expecting value: ...").
func pyStoreError(dir string, err error) error {
	var se *status.StoreError
	if !errors.As(err, &se) || !strings.HasPrefix(se.Msg, "Invalid JSON on line ") {
		return err
	}
	b, rerr := os.ReadFile(status.EventsPath(dir))
	if rerr != nil {
		return err
	}
	for i, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if _, perr := pyjson.Loads(l); perr != nil {
			return fmt.Errorf("Invalid JSON on line %d: %v", i+1, perr)
		}
	}
	return err
}

func writeDerived(path string, o *jsonx.Object) error {
	text := jsonx.Dumps(o, status.SnapshotOptions) + "\n"
	tmp := strings.TrimSuffix(path, ".json") + ".json.tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
