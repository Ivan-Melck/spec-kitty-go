package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/wp"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "context", "Query workspace context information")
		g.InvokeWithoutCommand = true
		g.Run = func(ctx *cli.Context) error { return runContextInfo(ctx, "", false) }
		g.Add(&cli.Command{
			Name: "info", Short: "Show context information for current or specified workspace.",
			Long: "Show context information for current or specified workspace.\n\nExamples:\n# Auto-detect from current directory (if inside worktree)\nspec-kitty context info\n\n# Explicit workspace\nspec-kitty context info --workspace 010-mission-lane-a\n\n# JSON output\nspec-kitty context info --json",
			Flags: []*cli.Flag{
				{Name: "workspace", Short: "w", Kind: cli.String, Metavar: "<str>", Help: "Workspace name (auto-detected if inside worktree)"},
				{Name: "json", Kind: cli.Bool, Help: "Output in JSON format"},
			},
			Run: func(ctx *cli.Context) error { return runContextInfo(ctx, ctx.String("workspace"), ctx.Bool("json")) },
		}, &cli.Command{
			Name: "list", Short: "List all workspace contexts.",
			Long: "List all workspace contexts.\n\nExamples:\n# List all contexts\nspec-kitty context list\n\n# List only orphaned contexts (worktree deleted)\nspec-kitty context list --orphaned\n\n# JSON output\nspec-kitty context list --json",
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Output in JSON format"},
				{Name: "orphaned", Kind: cli.Bool, Help: "Show only orphaned contexts"},
			},
			Run: runContextList,
		}, &cli.Command{
			Name: "cleanup", Short: "Clean up orphaned workspace contexts.",
			Long:  "Clean up orphaned workspace contexts.\n\nRemoves context files for workspaces that no longer exist.\n\nExamples:\n    # Preview cleanup\n    spec-kitty context cleanup --dry-run\n\n    # Clean up orphaned contexts\n    spec-kitty context cleanup",
			Flags: []*cli.Flag{{Name: "dry-run", Kind: cli.Bool, Help: "Show what would be cleaned up without deleting"}},
			Run:   runContextCleanup,
		}, &cli.Command{
			Name: "mission-resolve", Short: "Resolve and persist a MissionContext token.",
			Long: "Resolve and persist a MissionContext token.\n\nCreates a new bound context for the given work package and mission,\nwrites it to .kittify/runtime/contexts/, and prints the token.\n\nThe token can be passed to other commands via --context <token>.\n\nExamples:\n    # Resolve and print token for piping\n    TOKEN=$(spec-kitty context mission-resolve --wp WP01 --mission 057-my-mission)\n\n    # Resolve and print full JSON\n    spec-kitty context mission-resolve --wp WP01 --mission 057-my-mission --json",
			Flags: []*cli.Flag{
				{Name: "wp", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Work package code (e.g., WP01)"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., 057-mission-name)"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name (default: 'unknown')"},
				{Name: "json", Kind: cli.Bool, Help: "Output full JSON context (default: token only)"},
			},
			Run: runMissionResolve,
		}, &cli.Command{
			Name: "mission-show", Short: "Show all fields of a persisted MissionContext token.",
			Long: "Show all fields of a persisted MissionContext token.\n\nLoads the context file and pretty-prints its bound fields.\n\nExamples:\n    spec-kitty context mission-show --context ctx-01HVXYZ...\n    spec-kitty context mission-show --context ctx-01HVXYZ... --json",
			Flags: []*cli.Flag{
				{Name: "context", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Context token (e.g., ctx-01HV...)"},
				{Name: "json", Kind: cli.Bool, Help: "Output raw JSON"},
			},
			Run: runMissionShow,
		})
		order := []string{"info", "list", "cleanup", "mission-resolve", "mission-show"}
		sortSubcommands(g, order)
	})
}

func sortSubcommands(g *cli.Command, order []string) {
	rank := map[string]int{}
	for i, n := range order {
		rank[n] = i + 1
	}
	sort.SliceStable(g.Subcommands, func(i, j int) bool {
		ri, rj := rank[g.Subcommands[i].Name], rank[g.Subcommands[j].Name]
		if ri == 0 {
			ri = 1000
		}
		if rj == 0 {
			rj = 1000
		}
		return ri < rj
	})
}

// emitJSON is console.emit_json (indent 2).
func emitJSON(ctx *cli.Context, v any) {
	fmt.Fprintln(ctx.Stdout, jsonx.Dumps(v, jsonx.Pretty))
}

func jsonError(code, message string) *jsonx.Object {
	return jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message))
}

func contextError(ctx *cli.Context, code, message string, jsonMode bool) {
	if jsonMode {
		emitJSON(ctx, jsonError(code, message))
	} else {
		rich.Print(ctx.Stdout, "Error: "+message)
	}
}

func notInProject() string {
	cwd, _ := os.Getwd()
	return fmt.Sprintf("Could not locate project root (no .kittify/ directory found from %s)", cwd)
}

// workspaceFiles lists .kittify/workspaces/*.json (name -> raw object).
func workspaceFiles(root string) ([]string, map[string]*jsonx.Object) {
	m, _ := filepath.Glob(filepath.Join(lanes.WorkspacesDir(root), "*.json"))
	sort.Strings(m)
	out := map[string]*jsonx.Object{}
	var names []string
	for _, p := range m {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		o, err := jsonx.DecodeObject(b)
		if err != nil || o == nil {
			continue
		}
		n := strings.TrimSuffix(filepath.Base(p), ".json")
		names = append(names, n)
		out[n] = o
	}
	return names, out
}

func detectWorkspace(cwd string) string {
	parts := strings.Split(filepath.ToSlash(cwd), "/")
	for i, p := range parts {
		if p == ".worktrees" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, n int) string {
	if l := runeLen(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

// plainTable renders a header-less rich Table(box=None, padding=(0, 2)).
func plainTable(ctx *cli.Context, rows [][2]string) {
	w0, w1 := 0, 0
	for _, r := range rows {
		for _, l := range strings.Split(r[0], "\n") {
			w0 = max(w0, runeLen(l))
		}
		for _, l := range strings.Split(r[1], "\n") {
			w1 = max(w1, runeLen(l))
		}
	}
	for _, r := range rows {
		a, b := strings.Split(r[0], "\n"), strings.Split(r[1], "\n")
		n := max(len(a), len(b))
		for i := 0; i < n; i++ {
			x, y := "", ""
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			fmt.Fprintln(ctx.Stdout, "  "+padRight(x, w0)+"    "+padRight(y, w1)+"  ")
		}
	}
}

func runContextInfo(ctx *cli.Context, workspace string, jsonMode bool) error {
	root := locateRoot()
	if root == "" {
		contextError(ctx, "not_in_project", notInProject(), jsonMode)
		return cli.Exit(1)
	}
	if workspace == "" {
		cwd, _ := os.Getwd()
		workspace = detectWorkspace(cwd)
		if workspace == "" {
			contextError(ctx, "no_worktree", "Not inside a worktree and no --workspace specified", jsonMode)
			if !jsonMode {
				fmt.Fprintln(ctx.Stdout, "\nRun from inside a worktree or use --workspace flag:")
				fmt.Fprintln(ctx.Stdout, "  spec-kitty context info --workspace 010-mission-lane-a")
			}
			return cli.Exit(1)
		}
	}
	p := filepath.Join(lanes.WorkspacesDir(root), workspace+".json")
	b, err := os.ReadFile(p)
	var o *jsonx.Object
	if err == nil {
		o, err = jsonx.DecodeObject(b)
	}
	if err != nil || o == nil {
		contextError(ctx, "workspace_not_found", "No context found for workspace: "+workspace, jsonMode)
		if !jsonMode {
			fmt.Fprintln(ctx.Stdout, "\nContext file not found:")
			fmt.Fprintln(ctx.Stdout, "  "+p)
		}
		return cli.Exit(1)
	}
	if jsonMode {
		emitJSON(ctx, o)
		return nil
	}
	fmt.Fprintln(ctx.Stdout, "\n📍 Workspace Context")
	fmt.Fprintln(ctx.Stdout, strings.Repeat("─", 50))
	bc := o.GetString("base_commit")
	if bc == "" {
		bc = "unknown"
	} else if len(bc) > 12 {
		bc = bc[:12]
	}
	deps := wp.StringList(o, "dependencies")
	d := "none"
	if len(deps) > 0 {
		d = strings.Join(deps, ", ")
	}
	plainTable(ctx, [][2]string{{"Work Package", o.GetString("wp_id")}, {"Mission", o.GetString("mission_slug")},
		{"Base Branch", o.GetString("base_branch")}, {"Base Commit", bc}, {"Dependencies", d},
		{"Created", o.GetString("created_at")}, {"Worktree", o.GetString("worktree_path")},
		{"Branch", o.GetString("branch_name")}, {"VCS Backend", o.GetString("vcs_backend")}})
	fmt.Fprintln(ctx.Stdout)
	return nil
}

func orphanedContexts(root string) ([]string, map[string]*jsonx.Object) {
	names, objs := workspaceFiles(root)
	var out []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, objs[n].GetString("worktree_path"))); err != nil {
			out = append(out, n)
		}
	}
	return out, objs
}

func runContextList(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		contextError(ctx, "not_in_project", notInProject(), jsonMode)
		return cli.Exit(1)
	}
	if ctx.Bool("orphaned") {
		orphans, objs := orphanedContexts(root)
		if jsonMode {
			items := []any{}
			for _, n := range orphans {
				items = append(items, jsonx.NewObject("workspace", n, "context", objs[n]))
			}
			emitJSON(ctx, items)
			return nil
		}
		if len(orphans) == 0 {
			fmt.Fprintln(ctx.Stdout, "✓ No orphaned contexts found")
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "\n⚠️  Found %d orphaned context(s)\n\n", len(orphans))
		for _, n := range orphans {
			fmt.Fprintln(ctx.Stdout, "  "+n)
			fmt.Fprintf(ctx.Stdout, "    Worktree: %s (deleted)\n", objs[n].GetString("worktree_path"))
			fmt.Fprintf(ctx.Stdout, "    Created: %s\n", objs[n].GetString("created_at"))
			fmt.Fprintln(ctx.Stdout)
		}
		fmt.Fprintln(ctx.Stdout, "Clean up with: spec-kitty context cleanup")
		fmt.Fprintln(ctx.Stdout)
		return nil
	}
	names, objs := workspaceFiles(root)
	if jsonMode {
		items := []any{}
		for _, n := range names {
			items = append(items, objs[n])
		}
		emitJSON(ctx, items)
		return nil
	}
	if len(names) == 0 {
		fmt.Fprintln(ctx.Stdout, "No workspace contexts found")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "\nWorkspace Contexts (%d total)\n\n", len(names))
	sorted := append([]string{}, names...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := objs[sorted[i]], objs[sorted[j]]
		if a.GetString("mission_slug") != b.GetString("mission_slug") {
			return a.GetString("mission_slug") < b.GetString("mission_slug")
		}
		return a.GetString("wp_id") < b.GetString("wp_id")
	})
	var rows [][]string
	for _, n := range sorted {
		o := objs[n]
		st := "Active"
		if _, err := os.Stat(filepath.Join(root, o.GetString("worktree_path"))); err != nil {
			st = "Orphaned"
		}
		deps := wp.StringList(o, "dependencies")
		d := "none"
		if len(deps) > 0 {
			d = strings.Join(deps, ", ")
		}
		rows = append(rows, []string{o.GetString("wp_id"), o.GetString("mission_slug"), o.GetString("base_branch"), d, st})
	}
	rich.Table(ctx.Stdout, "", []string{"WP", "Mission", "Base", "Dependencies", "Status"}, rows)
	fmt.Fprintln(ctx.Stdout)
	return nil
}

func runContextCleanup(ctx *cli.Context) error {
	root := locateRoot()
	if root == "" {
		rich.Print(ctx.Stdout, "Error: "+notInProject())
		return cli.Exit(1)
	}
	orphans, objs := orphanedContexts(root)
	if len(orphans) == 0 {
		fmt.Fprintln(ctx.Stdout, "✓ No orphaned contexts to clean up")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "\nFound %d orphaned context(s):\n\n", len(orphans))
	for _, n := range orphans {
		fmt.Fprintln(ctx.Stdout, "  "+n)
		fmt.Fprintln(ctx.Stdout, "    "+objs[n].GetString("worktree_path"))
	}
	fmt.Fprintln(ctx.Stdout)
	if ctx.Bool("dry-run") {
		fmt.Fprintln(ctx.Stdout, "Dry run - no files deleted")
		fmt.Fprintf(ctx.Stdout, "Would delete %d context file(s)\n", len(orphans))
		return nil
	}
	n := 0
	for _, name := range orphans {
		if os.Remove(filepath.Join(lanes.WorkspacesDir(root), name+".json")) == nil {
			n++
		}
	}
	fmt.Fprintf(ctx.Stdout, "✓ Cleaned up %d orphaned context(s)\n", n)
	return nil
}

func contextsDir(root string) string { return filepath.Join(root, ".kittify", "runtime", "contexts") }

func runMissionResolve(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		contextError(ctx, "not_in_project", "Could not locate project root (no .kittify/ directory found)", jsonMode)
		return cli.Exit(1)
	}
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		if jsonMode {
			contextError(ctx, "missing_mission", "--mission <slug> is required", true)
			return cli.Exit(2)
		}
		return cli.Usage(ctx.Cmd, "Invalid value: --mission <slug> is required")
	}
	wpCode := ctx.String("wp")
	agent := ctx.String("agent")
	if agent == "" {
		agent = "unknown"
	}
	fail := func(msg string) error {
		contextError(ctx, "context_resolution_failed", msg, jsonMode)
		return cli.Exit(1)
	}
	cfg := filepath.Join(root, ".kittify", "config.yaml")
	cb, err := os.ReadFile(cfg)
	if err != nil {
		return fail(fmt.Sprintf("Project config not found at %s. Run `spec-kitty init` to initialize the project.", cfg))
	}
	co, _ := yamlx.ParseObject(cb)
	puuid := ""
	if co != nil {
		if p := co.GetObject("project"); p != nil {
			puuid = p.GetString("uuid")
		}
	}
	if puuid == "" {
		return fail("project.uuid not found in .kittify/config.yaml. Run `spec-kitty init` to assign a project identity.")
	}
	slug := handle
	if st, err := os.Stat(project.Specs(root, handle)); err != nil || !st.IsDir() {
		if s, err := mission.ResolveHandle(root, handle); err == nil {
			slug = s
		}
	}
	dir := project.Specs(root, slug)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fail(fmt.Sprintf("Feature directory not found: %s. Check that '%s' is the correct feature slug.", dir, slug))
	}
	meta, err := mission.ReadMeta(dir)
	if err != nil {
		return fail(fmt.Sprintf("meta.json not found at %s.", filepath.Join(dir, "meta.json")))
	}
	tasks := filepath.Join(dir, "tasks")
	if _, err := os.Stat(tasks); err != nil {
		return fail(fmt.Sprintf("tasks/ directory not found at %s.", tasks))
	}
	cands, _ := filepath.Glob(filepath.Join(tasks, wpCode+"-*.md"))
	if one, _ := filepath.Glob(filepath.Join(tasks, wpCode+".md")); len(one) > 0 {
		cands = append(cands, one...)
	}
	if len(cands) == 0 {
		return fail(fmt.Sprintf("No task file found for '%s' in %s. Expected a file matching %s-*.md or %s.md.", wpCode, tasks, wpCode, wpCode))
	}
	w, err := wp.Read(cands[0])
	if err != nil {
		return fail(fmt.Sprintf("WP file %s has invalid or missing frontmatter: %v", cands[0], err))
	}
	m, err := lanes.Read(dir)
	if err != nil || m == nil {
		return fail(fmt.Sprintf("lanes.json not found in %s", dir))
	}
	lane := m.LaneForWP(wpCode)
	if lane == nil {
		return fail(fmt.Sprintf("WP '%s' has no lane assignment in %s. Run 'spec-kitty agent mission finalize-tasks --mission %s' to compute lanes.", wpCode, filepath.Join(dir, "lanes.json"), slug))
	}
	mid := meta.GetString("mission_id")
	if mid == "" {
		mid = slug
	}
	target := meta.GetString("target_branch")
	writeBranch := lanes.SingleBranchWriteRef(root, slug, target)
	ref := lanes.LaneBranchName(slug, lane.LaneID, writeBranch)
	wpID := w.String("work_package_id")
	if wpID == "" {
		wpID = wpCode
	}
	mode := w.String("execution_mode")
	if mode == "" {
		mode = "code_change"
	}
	depMode := "independent"
	if len(w.Dependencies()) > 0 {
		depMode = "chained"
	}
	owned := []any{}
	for _, f := range w.List("owned_files") {
		owned = append(owned, f)
	}
	token := "ctx-" + ulid.New()
	obj := jsonx.NewObject("token", token, "project_uuid", puuid, "mission_id", mid, "work_package_id", wpID, "wp_code", wpCode,
		"mission_slug", slug, "mission_number", mission.MissionNumberString(meta, slug), "mission_type", mission.MissionType(meta),
		"target_branch", target, "authoritative_repo", root, "authoritative_ref", ref, "owned_files", owned,
		"execution_mode", mode, "dependency_mode", depMode, "created_at", clock.NowUTCISO(), "created_by", agent)
	os.MkdirAll(contextsDir(root), 0o755)
	atomicfile.WriteMode(filepath.Join(contextsDir(root), token+".json"), []byte(jsonx.Dumps(obj, jsonx.SortedPretty)+"\n"), 0o644)
	if jsonMode {
		emitJSON(ctx, obj)
	} else {
		fmt.Fprintln(ctx.Stdout, token)
	}
	return nil
}

func runMissionShow(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		contextError(ctx, "not_in_project", "Could not locate project root (no .kittify/ directory found)", jsonMode)
		return cli.Exit(1)
	}
	tok := ctx.String("context")
	p := filepath.Join(contextsDir(root), tok+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		contextError(ctx, "context_not_found", fmt.Sprintf("Context token '%s' not found at %s. Run `spec-kitty agent context resolve --wp <WP> --mission <slug>` to create a new context.", tok, p), jsonMode)
		return cli.Exit(1)
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		contextError(ctx, "context_corrupted", fmt.Sprintf("Context token '%s' is corrupted at %s: %v", tok, p, err), jsonMode)
		return cli.Exit(1)
	}
	for _, k := range []string{"token", "project_uuid", "mission_id", "work_package_id", "wp_code", "mission_slug", "target_branch",
		"authoritative_repo", "authoritative_ref", "owned_files", "execution_mode", "dependency_mode", "created_at", "created_by"} {
		if !o.Has(k) {
			contextError(ctx, "context_corrupted", fmt.Sprintf("Context token '%s' is missing required field: '%s'", tok, k), jsonMode)
			return cli.Exit(1)
		}
	}
	slug := o.GetString("mission_slug")
	num, _ := o.Get("mission_number")
	typ, _ := o.Get("mission_type")
	out := jsonx.NewObject("token", o.GetString("token"), "project_uuid", o.GetString("project_uuid"), "mission_id", o.GetString("mission_id"),
		"work_package_id", o.GetString("work_package_id"), "wp_code", o.GetString("wp_code"), "mission_slug", slug,
		"mission_number", strOrEmpty(num), "mission_type", typ)
	for _, k := range []string{"target_branch", "authoritative_repo", "authoritative_ref", "owned_files", "execution_mode", "dependency_mode", "created_at", "created_by"} {
		v, _ := o.Get(k)
		out.Set(k, v)
	}
	if jsonMode {
		emitJSON(ctx, out)
		return nil
	}
	fmt.Fprintln(ctx.Stdout, "\nMissionContext")
	fmt.Fprintln(ctx.Stdout, strings.Repeat("─", 60))
	ref := o.GetString("authoritative_ref")
	if ref == "" {
		ref = "none"
	}
	owned := wp.StringList(o, "owned_files")
	of := "none"
	if len(owned) > 0 {
		of = strings.Join(owned, "\n")
	}
	plainTable(ctx, [][2]string{{"Token", o.GetString("token")}, {"WP Code", o.GetString("wp_code")}, {"Mission Slug", slug},
		{"Mission ID", o.GetString("mission_id")}, {"Work Package ID", o.GetString("work_package_id")}, {"Project UUID", o.GetString("project_uuid")},
		{"Target Branch", o.GetString("target_branch")}, {"Authoritative Repo", o.GetString("authoritative_repo")}, {"Authoritative Ref", ref},
		{"Execution Mode", o.GetString("execution_mode")}, {"Dependency Mode", o.GetString("dependency_mode")},
		{"Created At", o.GetString("created_at")}, {"Created By", o.GetString("created_by")}, {"Owned Files", of}})
	fmt.Fprintln(ctx.Stdout)
	return nil
}

func strOrEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}
