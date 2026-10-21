package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// toolLabels is verify.TOOL_LABELS.
var toolLabels = [][2]string{
	{"git", "Git version control"}, {"claude", "Claude Code CLI"}, {"gemini", "Gemini CLI"}, {"llxprt", "LLxprt Code"},
	{"qwen", "Qwen Code CLI"}, {"code", "Visual Studio Code"}, {"code-insiders", "Visual Studio Code Insiders"},
	{"cursor-agent", "Cursor IDE agent"}, {"windsurf", "Windsurf IDE"}, {"kilocode", "Kilo Code IDE"}, {"opencode", "opencode"},
	{"codex", "Codex CLI"}, {"vibe", "Mistral Vibe"}, {"pi", "Pi"}, {"letta", "Letta Code"}, {"auggie", "Auggie CLI"},
	{"q", "Amazon Q Developer CLI (legacy)"}, {"kiro-cli", "Kiro CLI"},
}

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "verify-setup",
			Short: "Verify that the current environment matches Spec Kitty expectations.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug to verify"},
				{Name: "json", Kind: cli.Bool, Help: "Output in JSON format for AI agents"},
				{Name: "check-files", Kind: cli.Bool, Default: true, Help: "Check mission file integrity"},
				{Name: "check-tools", Kind: cli.Bool, Default: true, Help: "Check for installed development tools"},
				{Name: "diagnostics", Kind: cli.Bool, Help: "Show detailed project diagnostics"},
			},
			Run: runVerifySetup,
		})
	})
}

func checkTools(t *tracker) (*jsonx.Object, map[string]bool) {
	tools := &jsonx.Object{}
	status := map[string]bool{}
	for _, tl := range toolLabels {
		_, err := exec.LookPath(tl[0])
		ok := err == nil
		status[tl[0]] = ok
		tools.Set(tl[0], jsonx.NewObject("available", ok))
		if t != nil {
			if ok {
				t.complete(tl[0], "available")
			} else {
				t.fail(tl[0], "not found")
			}
		}
	}
	return tools, status
}

func toolTips(w io.Writer, status map[string]bool) {
	if !status["git"] {
		out(w, "[dim]Tip: Install git for repository management[/dim]")
	}
	any := false
	for k, v := range status {
		if k != "git" && v {
			any = true
		}
	}
	if !any {
		out(w, "[dim]Tip: Install an AI assistant for the best experience[/dim]")
	}
}

func newToolTracker() *tracker {
	t := &tracker{title: "Check Available Tools"}
	for _, tl := range toolLabels {
		t.add(tl[0], tl[1])
	}
	return t
}

func runVerifySetup(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	slug := strings.TrimSpace(ctx.String("mission"))
	if ctx.Bool("diagnostics") {
		return runDiagnosticsMode(w, jsonOut, ctx.Bool("check-tools"), slug)
	}
	var tools *jsonx.Object
	if ctx.Bool("check-tools") {
		if !jsonOut {
			out(w, "[bold]Checking for installed tools...[/bold]\n")
		}
		t := newToolTracker()
		var status map[string]bool
		tools, status = checkTools(t)
		if !jsonOut {
			t.render(w)
			fmt.Fprintln(w)
			toolTips(w, status)
			fmt.Fprintln(w)
		}
	}
	repoRoot := project.Locate("")
	if repoRoot == "" {
		msg := "Unable to locate repository root (missing .git or .kittify)."
		if jsonOut {
			o := jsonError("repository_detection_failed", msg)
			if tools != nil {
				o.Set("tools", tools)
			}
			fmt.Fprintln(w, jsonx.Dumps(o, jsonx.Options{Indent: -1, EnsureASCII: true}))
		} else {
			out(w, "[red]✗[/red] Repository detection failed: "+msg)
			out(w, "\n[yellow]Solution:[/yellow] Run this command from a Spec Kitty project root or from a feature worktree inside .worktrees/<feature>/ (use 'spec-kitty init <name>' to create a project).")
		}
		return cli.Exit(1)
	}
	projectRoot := project.Locate(repoRoot)
	if projectRoot == "" {
		if jsonOut {
			printJSONRaw(w, jsonError("not_in_project", "Unable to locate the Spec Kitty project root (.kittify directory not found)."))
			return cli.Exit(1)
		}
		out(w, "[red]Error:[/red] Unable to locate the Spec Kitty project root (.kittify directory not found).")
		out(w, "[dim]Run this command from the project root or from a mission worktree under .worktrees/<mission>/.[/dim]")
		out(w, "[dim]Tip: Initialize a project with 'spec-kitty init <name>' if one does not exist.[/dim]")
		return cli.Exit(1)
	}
	featureDir, err := existingFeatureDir(projectRoot, slug)
	if err != nil {
		reportAmbiguous(w, err, jsonOut)
		return cli.Exit(1)
	}
	cwd, _ := os.Getwd()
	result := runEnhancedVerify(w, repoRoot, projectRoot, cwd, slug, jsonOut, ctx.Bool("check-files"), featureDir)
	if jsonOut {
		if tools != nil {
			result.Set("tools", tools)
		}
		fmt.Fprintln(w, jsonx.Dumps(result, jsonx.Pretty))
	}
	return nil
}

func reportAmbiguous(w io.Writer, err error, jsonOut bool) {
	amb, _ := err.(*mission.SelectorAmbiguousError)
	if jsonOut && amb != nil {
		o := jsonError("MISSION_AMBIGUOUS_SELECTOR", err.Error())
		o.Set("handle", amb.Handle)
		c := make([]any, len(amb.Candidates))
		for i, x := range amb.Candidates {
			c[i] = x
		}
		o.Set("candidates", c)
		printJSONRaw(w, o)
		return
	}
	out(w, "[red]Error:[/red] "+escapeMarkup(err.Error()))
}

// existingFeatureDir is verify._existing_feature_dir: the mission's
// kitty-specs directory when it exists.
func existingFeatureDir(root, slug string) (string, error) {
	if slug == "" {
		return "", nil
	}
	dir := missionReadDir(root, slug)
	if dir == "" {
		if _, err := mission.BareModernDirName(root, slug); err != nil {
			return "", err
		}
		return "", nil
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, nil
	}
	return "", nil
}

// missionReadDir is placement_seam(root, slug).read_dir(PRIMARY_METADATA).
func missionReadDir(root, slug string) string {
	if name, err := mission.BareModernDirName(root, slug); err == nil && name != "" {
		return project.Specs(root, name)
	} else if err != nil {
		return ""
	}
	return project.Specs(root, slug)
}

func missionTypeOf(featureDir string) string {
	if featureDir == "" {
		return ""
	}
	m := mission.ReadMetaOrEmpty(featureDir)
	if m == nil || m.Len() == 0 {
		return ""
	}
	return mission.MissionType(m)
}

// fileManifest is manifest.FileManifest.
type fileManifest struct {
	kittify, missionDir string
}

var manifestCategories = []string{"commands", "templates", "scripts", "mission_files"}

func (f fileManifest) expected() (map[string][]string, bool) {
	if f.missionDir == "" {
		return nil, false
	}
	if _, err := os.Stat(f.missionDir); err != nil {
		return nil, false
	}
	m := map[string][]string{}
	rel := func(p string) string { r, _ := filepath.Rel(f.kittify, p); return r }
	if _, err := os.Stat(filepath.Join(f.missionDir, "mission.yaml")); err == nil {
		m["mission_files"] = append(m["mission_files"], rel(filepath.Join(f.missionDir, "mission.yaml")))
	}
	cmds, _ := filepath.Glob(filepath.Join(f.missionDir, "command-templates", "*.md"))
	for _, c := range cmds {
		m["commands"] = append(m["commands"], rel(c))
	}
	tpls, _ := filepath.Glob(filepath.Join(f.missionDir, "templates", "*.md"))
	for _, c := range tpls {
		m["templates"] = append(m["templates"], rel(c))
	}
	key := "sh:"
	if runtime.GOOS == "windows" {
		key = "ps:"
	}
	scripts := map[string]bool{}
	for _, c := range cmds {
		b, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		in := false
		for _, line := range strings.Split(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n") {
			if strings.TrimSpace(line) == "---" {
				if in {
					break
				}
				in = true
				continue
			}
			if !in || !strings.Contains(line, key) {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
				continue
			}
			v := strings.Trim(strings.Trim(strings.TrimSpace(parts[1]), `"`), "'")
			if fs := strings.Fields(v); len(fs) > 0 {
				v = fs[0]
			}
			if r, ok := strings.CutPrefix(v, ".kittify/scripts/"); ok && r != "" && !strings.Contains(r, `\`) && !strings.HasPrefix(r, "/") {
				safe := true
				for _, p := range strings.Split(r, "/") {
					if p == "." || p == ".." {
						safe = false
					}
				}
				if safe {
					scripts["scripts/"+r] = true
				}
			}
		}
	}
	m["scripts"] = sortedKeys(scripts)
	return m, true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// worktreeStatus is manifest.WorktreeStatus.
type worktreeStatus struct{ root string }

var (
	newMissionBranchRe    = regexp.MustCompile(`^kitty/mission-(.+)-[0-9A-HJKMNP-TV-Z]{8}(-lane-[a-z0-9-]+)?$`)
	legacyMissionBranchRe = regexp.MustCompile(`^kitty/mission-(.+?)(-lane-[a-z0-9]+)?$`)
)

func (s worktreeStatus) allFeatures() []string {
	set := map[string]bool{}
	if r, err := gitx.Exec(s.root, nil, nil, "branch", "-a"); err == nil && r.Code == 0 {
		for _, line := range strings.Split(r.Stdout, "\n") {
			b := strings.ReplaceAll(strings.TrimSpace(line), "* ", "")
			if b == "" || strings.HasPrefix(b, "remotes/") {
				continue
			}
			if strings.HasPrefix(b, "kitty/mission-") {
				if m := newMissionBranchRe.FindStringSubmatch(b); m != nil {
					set[m[1]] = true
				} else if m := legacyMissionBranchRe.FindStringSubmatch(b); m != nil {
					set[m[1]] = true
				}
				continue
			}
			parts := strings.Split(b, "/")
			br := parts[len(parts)-1]
			if br != "" && br[0] >= '0' && br[0] <= '9' && strings.Contains(br, "-") {
				set[br] = true
			}
		}
	}
	if ents, err := os.ReadDir(project.Specs(s.root)); err == nil {
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() && n[0] >= '0' && n[0] <= '9' && strings.Contains(n, "-") {
				set[n] = true
			}
		}
	}
	return sortedKeys(set)
}

func mdNames(dir string) []any {
	out := []any{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

func primaryBranch(root string) string {
	if r, err := gitx.Exec(root, nil, nil, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil && r.Code == 0 {
		if b := strings.TrimPrefix(strings.TrimSpace(r.Stdout), "refs/remotes/origin/"); b != "" {
			return b
		}
	}
	if r, err := gitx.Exec(root, nil, nil, "branch", "--show-current"); err == nil && r.Code == 0 && strings.TrimSpace(r.Stdout) != "" {
		return strings.TrimSpace(r.Stdout)
	}
	for _, b := range []string{"main", "master", "develop"} {
		if r, err := gitx.Exec(root, nil, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil && r.Code == 0 {
			return b
		}
	}
	return "main"
}

func (s worktreeStatus) featureStatus(feature string) *jsonx.Object {
	branchExists := false
	if r, err := gitx.Exec(s.root, nil, nil, "show-ref", "refs/heads/"+feature); err == nil && r.Code == 0 {
		branchExists = true
	}
	merged := false
	if branchExists {
		if r, err := gitx.Exec(s.root, nil, nil, "branch", "--merged", primaryBranch(s.root)); err == nil && r.Code == 0 {
			merged = strings.Contains(r.Stdout, feature)
		}
	}
	wt := filepath.Join(s.root, ".worktrees", feature)
	wtExists := exists(wt)
	var wtPath any
	if wtExists {
		wtPath = wt
	}
	main := mdNames(missionReadDir(s.root, feature))
	inWT := []any{}
	if wtExists {
		inWT = mdNames(filepath.Join(wt, "kitty-specs", feature))
	}
	state := "unknown"
	switch {
	case !branchExists && len(main) == 0:
		state = "not_started"
	case merged && len(main) > 0:
		state = "merged"
	case wtExists || len(inWT) > 0:
		state = "in_development"
	case branchExists && !wtExists:
		state = "ready_to_merge"
	case !branchExists && len(main) > 0:
		state = "merged"
	}
	return jsonx.NewObject("name", feature, "branch_exists", branchExists, "branch_merged", merged, "worktree_exists", wtExists,
		"worktree_path", wtPath, "artifacts_in_main", main, "artifacts_in_worktree", inWT, "last_activity", nil, "state", state)
}

func (s worktreeStatus) summary() *jsonx.Object {
	fs := s.allFeatures()
	active, mergedN, dev, notStarted := 0, 0, 0, 0
	for _, f := range fs {
		st := s.featureStatus(f)
		if st.GetBool("worktree_exists") {
			active++
		}
		switch st.GetString("state") {
		case "merged":
			mergedN++
		case "in_development":
			dev++
		case "not_started":
			notStarted++
		}
	}
	return jsonx.NewObject("total_features", len(fs), "active_worktrees", active, "merged_features", mergedN, "in_development", dev, "not_started", notStarted)
}

func currentBranch(dir string) (string, bool) {
	r, err := gitx.Exec(dir, nil, nil, "branch", "--show-current")
	if err != nil || r.Code != 0 {
		return "", false
	}
	return strings.TrimSpace(r.Stdout), true
}

func fileIntegrity(projectRoot, missionType string) (map[string][]string, map[string]string, map[string]string, int) {
	fm := fileManifest{kittify: filepath.Join(projectRoot, ".kittify")}
	if missionType != "" {
		fm.missionDir = filepath.Join(fm.kittify, "missions", missionType)
	}
	expected, _ := fm.expected()
	present, missing := map[string]string{}, map[string]string{}
	total := 0
	for _, cat := range manifestCategories {
		for _, f := range expected[cat] {
			total++
			if exists(filepath.Join(fm.kittify, f)) {
				present[f] = cat
			} else {
				missing[f] = cat
			}
		}
	}
	return expected, present, missing, total
}

func runEnhancedVerify(w io.Writer, repoRoot, projectRoot, cwd, feature string, jsonOut, checkFiles bool, featureDir string) *jsonx.Object {
	o := jsonx.NewObject("environment", &jsonx.Object{}, "feature_detection", &jsonx.Object{}, "worktree_status", &jsonx.Object{},
		"file_integrity", &jsonx.Object{}, "feature_analysis", &jsonx.Object{}, "recommendations", []any{})
	missionType := missionTypeOf(featureDir)
	if featureDir == "" && feature != "" {
		if d := missionReadDir(projectRoot, feature); d != "" && exists(d) {
			missionType = missionTypeOf(d)
		}
	}
	activeMission := missionType
	if activeMission == "" {
		activeMission = "no mission context"
	}
	ws := worktreeStatus{root: repoRoot}
	inWorktree := strings.Contains(cwd, ".worktrees")
	branch, ok := currentBranch(cwd)
	var branchV any
	if ok {
		branchV = branch
	}
	o.Set("environment", jsonx.NewObject("working_directory", cwd, "repo_root", repoRoot, "project_root", projectRoot,
		"in_worktree", inWorktree, "current_branch", branchV, "active_mission", activeMission))
	if !jsonOut {
		out(w, "\n[bold]System Integrity Check[/bold]\n")
		out(w, "[cyan]1. Environment[/cyan]")
		out(w, "   Working directory: "+cwd)
		out(w, "   Repository root: "+repoRoot)
		if inWorktree {
			out(w, "   [green]✓[/green] In worktree")
		} else {
			out(w, "   [dim]○[/dim] Not in worktree")
		}
		if branch != "" {
			out(w, "   Current branch: "+branch)
			if branch == "main" || branch == "master" {
				out(w, "   [yellow]⚠[/yellow] On "+branch+" branch")
			}
		} else {
			out(w, "   [yellow]⚠[/yellow] Could not detect branch")
		}
	}
	totalMissing := 0
	if checkFiles {
		expected, present, missing, total := fileIntegrity(projectRoot, missionType)
		totalMissing = len(missing)
		missObj := &jsonx.Object{}
		for _, cat := range manifestCategories {
			for _, f := range expected[cat] {
				if c, ok := missing[f]; ok {
					missObj.Set(f, c)
				}
			}
		}
		cats := &jsonx.Object{}
		for _, cat := range manifestCategories {
			files, ok := expected[cat]
			if !ok {
				continue
			}
			p := 0
			for _, f := range files {
				if _, ok := present[f]; ok {
					p++
				}
			}
			cats.Set(cat, jsonx.NewObject("expected", len(files), "present", p, "missing", len(files)-p))
		}
		o.Set("file_integrity", jsonx.NewObject("active_mission", activeMission, "total_expected", total, "total_present", len(present),
			"total_missing", totalMissing, "missing_files", missObj, "categories", cats))
		if !jsonOut {
			out(w, "\n[cyan]2. Mission File Integrity[/cyan]")
			out(w, "   Active mission: "+activeMission)
			if totalMissing == 0 {
				out(w, fmt.Sprintf("   [green]✓[/green] All %d expected files present", total))
			} else {
				out(w, fmt.Sprintf("   [yellow]⚠[/yellow] %d of %d files missing", totalMissing, total))
				for _, cat := range []string{"commands", "templates", "scripts"} {
					var cm []string
					for _, kv := range missObj.Pairs() {
						if kv.Value == cat {
							cm = append(cm, kv.Key)
						}
					}
					if len(cm) == 0 {
						continue
					}
					out(w, "   Missing "+cat+":")
					for i, f := range cm {
						if i == 3 {
							break
						}
						out(w, "     - "+f)
					}
					if len(cm) > 3 {
						out(w, fmt.Sprintf("     ... and %d more", len(cm)-3))
					}
				}
			}
		}
	}
	sum := ws.summary()
	o.Set("worktree_status", sum)
	if !jsonOut {
		out(w, "\n[cyan]3. Worktree Overview[/cyan]")
		for _, kv := range [][2]string{{"Total features", "total_features"}, {"Active worktrees", "active_worktrees"}, {"Merged features", "merged_features"}, {"In development", "in_development"}} {
			n, _ := sum.GetInt(kv[1])
			out(w, fmt.Sprintf("   %s: %d", kv[0], n))
		}
	}
	var featureState *jsonx.Object
	if feature == "" {
		o.Set("feature_detection", jsonx.NewObject("detected", false, "error", "No --mission provided; skipping feature analysis."))
		if !jsonOut {
			out(w, "\n[cyan]4. Feature Analysis[/cyan]")
			out(w, "   [yellow]⚠[/yellow] Skipped: No --mission provided; skipping feature analysis.")
		}
	} else {
		dir := featureDir
		if dir == "" {
			dir = missionReadDir(projectRoot, feature)
		}
		meta := mission.ReadMetaOrEmpty(dir)
		slug := filepath.Base(dir)
		if s := meta.GetString("mission_slug"); s != "" && mission.SafePathSegment(s) {
			slug = s
		} else if s := meta.GetString("slug"); s != "" && mission.SafePathSegment(s) {
			slug = s
		}
		var num any
		if n, ok := mission.MissionNumber(meta); ok {
			num = n
		}
		o.Set("feature_detection", jsonx.NewObject("detected", true, "mission_slug", slug, "mission_number", num, "mission_type", mission.MissionType(meta)))
		featureState = ws.featureStatus(feature)
		o.Set("feature_analysis", featureState)
		if !jsonOut {
			out(w, "\n[cyan]4. Current Mission Status[/cyan]")
			out(w, "   Mission: "+feature)
			out(w, "   State: "+strings.ToUpper(featureState.GetString("state")))
			if featureState.GetBool("branch_exists") {
				t := "active"
				if featureState.GetBool("branch_merged") {
					t = "merged"
				}
				out(w, "   [green]✓[/green] Branch exists ("+t+")")
			} else {
				out(w, "   [dim]○[/dim] No branch")
			}
			if featureState.GetBool("worktree_exists") {
				out(w, "   [green]✓[/green] Worktree at: "+featureState.GetString("worktree_path"))
			} else {
				out(w, "   [dim]○[/dim] No worktree")
			}
			for _, k := range [][2]string{{"artifacts_in_main", "Artifacts in main"}, {"artifacts_in_worktree", "Artifacts in worktree"}} {
				if l := featureState.GetList(k[0]); len(l) > 0 {
					var s []string
					for _, x := range l {
						s = append(s, x.(string))
					}
					out(w, "   "+k[1]+": "+strings.Join(s, ", "))
				}
			}
			switch featureState.GetString("state") {
			case "merged":
				out(w, "   [green]✓[/green] Feature appears to be merged")
			case "in_development":
				out(w, "   [blue]●[/blue] Feature is in active development")
			case "not_started":
				out(w, "   [dim]○[/dim] Feature not yet started")
			}
		}
	}
	all := ws.allFeatures()
	if !jsonOut && len(all) > 0 {
		out(w, "\n[cyan]5. All Features Status[/cyan]")
		var rows [][]string
		for i, f := range all {
			if i == 10 {
				break
			}
			st := ws.featureStatus(f)
			stateDisp := map[string]string{"merged": "MERGED", "in_development": "ACTIVE", "ready_to_merge": "READY", "not_started": "NOT STARTED", "unknown": "?"}[st.GetString("state")]
			br := "-"
			if st.GetBool("branch_exists") {
				br = "✓"
			}
			if st.GetBool("branch_merged") {
				br = "merged"
			}
			wt := "-"
			if st.GetBool("worktree_exists") {
				wt = "✓"
			}
			n := len(st.GetList("artifacts_in_main")) + len(st.GetList("artifacts_in_worktree"))
			art := "-"
			if n > 0 {
				art = strconv.Itoa(n)
			}
			rows = append(rows, []string{f, stateDisp, br, wt, art})
		}
		richTable(w, "", []string{"Feature", "State", "Branch", "Worktree", "Artifacts"}, nil, rows)
		if len(all) > 10 {
			out(w, fmt.Sprintf("   [dim]... and %d more features[/dim]", len(all)-10))
		}
	}
	skills, skillIssues := managedSkillsSection(w, projectRoot, jsonOut)
	o.Set("managed_skills", skills)
	var obs []any
	if (branch == "main" || branch == "master") && inWorktree {
		obs = append(obs, "Unusual: In worktree but on main branch")
	}
	if featureState != nil && featureState.GetString("state") == "in_development" && !featureState.GetBool("worktree_exists") {
		obs = append(obs, fmt.Sprintf("Feature %s has no worktree but has development artifacts", feature))
	}
	if totalMissing > 0 && checkFiles {
		obs = append(obs, fmt.Sprintf("Mission integrity: %d expected files not found", totalMissing))
	}
	if skillIssues {
		obs = append(obs, "Managed skills: some files are missing or drifted (run spec-kitty init --here to restore)")
	}
	if obs == nil {
		obs = []any{}
	}
	o.Set("observations", obs)
	if !jsonOut && len(obs) > 0 {
		out(w, "\n[cyan]7. Observations[/cyan]")
		for _, x := range obs {
			out(w, "   • "+x.(string))
		}
	}
	if !jsonOut {
		out(w, "\n[bold green]✓ Verification complete[/bold green]\n")
	}
	return o
}

// SkillVerifyResult is skills.verifier.VerifyResult.
type SkillVerifyResult struct {
	Missing []agents.SkillEntry
	Drifted []agents.SkillEntry
	Errors  []string
}

// OK reports no issues.
func (r SkillVerifyResult) OK() bool { return len(r.Missing)+len(r.Drifted)+len(r.Errors) == 0 }

// VerifyInstalledSkills is skills.verifier.verify_installed_skills.
func VerifyInstalledSkills(root string) SkillVerifyResult {
	var r SkillVerifyResult
	m := agents.LoadSkillsManifest(root)
	if m == nil {
		return r
	}
	canon := map[string]agents.Skill{}
	for _, s := range agents.Skills() {
		canon[s.Name] = s
	}
	for _, e := range m.Entries {
		p := filepath.Join(root, filepath.FromSlash(e.InstalledPath))
		st, err := os.Lstat(p)
		if err != nil {
			r.Missing = append(r.Missing, e)
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			r.Drifted = append(r.Drifted, e)
			continue
		}
		if !st.Mode().IsRegular() {
			r.Errors = append(r.Errors, fmt.Sprintf("Cannot read %s: not a regular skill file", e.InstalledPath))
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("Cannot read %s: %v", e.InstalledPath, err))
			continue
		}
		want := e.ContentHash
		if s, ok := canon[e.SkillName]; ok {
			if src, err := agents.ReadSkillFile(s, e.SourceFile); err == nil {
				if e.SourceFile == "SKILL.md" {
					src = []byte(agents.EnsureSkillFrontmatter(string(src), s.Name))
				}
				want = "sha256:" + sha256Hex(src)
			}
		}
		if "sha256:"+sha256Hex(b) != want {
			r.Drifted = append(r.Drifted, e)
		}
	}
	return r
}

func parseSkillName(content string) string {
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(content[3:3+end], "\n") {
		s := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(s, "name:"); ok {
			v = strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			return v
		}
	}
	return ""
}

func managedSkillsSection(w io.Writer, root string, jsonOut bool) (*jsonx.Object, bool) {
	m := agents.LoadSkillsManifest(root)
	if m == nil {
		if !jsonOut {
			out(w, "\n[cyan]6. Managed Skills[/cyan]")
			out(w, "   [dim]○[/dim] No skill manifest found")
		}
		return jsonx.NewObject("status", "no_manifest"), false
	}
	r := VerifyInstalledSkills(root)
	total := len(m.Entries)
	nm, nd, ne := len(r.Missing), len(r.Drifted), len(r.Errors)
	status := "ok"
	if !r.OK() {
		status = "issues_found"
	}
	missing, drifted, errs := []any{}, []any{}, []any{}
	for _, e := range r.Missing {
		missing = append(missing, e.InstalledPath)
	}
	for _, e := range r.Drifted {
		drifted = append(drifted, jsonx.NewObject("path", e.InstalledPath, "skill", e.SkillName))
	}
	for _, e := range r.Errors {
		errs = append(errs, e)
	}
	o := jsonx.NewObject("status", status, "total_files", total, "ok", total-nm-nd-ne, "missing", nm, "drifted", nd, "errors", ne,
		"missing_files", missing, "drifted_files", drifted, "error_messages", errs)
	if !jsonOut {
		out(w, "\n[cyan]6. Managed Skills[/cyan]")
		if r.OK() {
			out(w, fmt.Sprintf("   [green]✓[/green] All %d files intact", total))
		} else {
			out(w, fmt.Sprintf("   [yellow]⚠[/yellow] %d issue(s) in %d managed files", nm+nd+ne, total))
			if nm > 0 {
				out(w, fmt.Sprintf("   Missing (%d):", nm))
				for _, e := range r.Missing {
					out(w, fmt.Sprintf("     - %s (skill: %s)", e.InstalledPath, e.SkillName))
				}
			}
			if nd > 0 {
				out(w, fmt.Sprintf("   Drifted (%d):", nd))
				for _, e := range r.Drifted {
					out(w, fmt.Sprintf("     - %s (skill: %s)", e.InstalledPath, e.SkillName))
				}
			}
			if ne > 0 {
				out(w, fmt.Sprintf("   Errors (%d):", ne))
				for _, e := range r.Errors {
					out(w, "     - "+e)
				}
			}
		}
	}
	// Duplicate skill names across roots.
	roots := map[string]bool{}
	for _, e := range m.Entries {
		parts := strings.Split(e.InstalledPath, "/")
		if len(parts) >= 3 {
			roots[parts[0]+"/"+parts[1]] = true
		}
	}
	byName := map[string][]string{}
	for _, rk := range sortedKeys(roots) {
		ents, err := os.ReadDir(filepath.Join(root, rk))
		if err != nil {
			continue
		}
		for _, d := range ents {
			if !d.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, rk, d.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			if n := parseSkillName(string(b)); n != "" {
				byName[n] = append(byName[n], rk)
			}
		}
	}
	var warnings []any
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rs := byName[n]
		if len(rs) <= 1 {
			continue
		}
		shared, nonShared := false, 0
		for _, x := range rs {
			if x == ".agents/skills" {
				shared = true
			} else {
				nonShared++
			}
		}
		if shared && nonShared <= 1 {
			continue
		}
		s := append([]string(nil), rs...)
		sort.Strings(s)
		warnings = append(warnings, fmt.Sprintf("Duplicate skill '%s' found in: %s", n, strings.Join(s, ", ")))
	}
	if len(warnings) > 0 {
		o.Set("duplicate_warnings", warnings)
		if !jsonOut {
			for _, x := range warnings {
				out(w, "   [yellow]⚠[/yellow] "+x.(string))
			}
		}
	}
	return o, !r.OK()
}

func runDiagnosticsMode(w io.Writer, jsonOut, withTools bool, feature string) error {
	cwd, _ := os.Getwd()
	root := project.Locate("")
	if root == "" {
		root = cwd
	}
	featureDir, err := existingFeatureDir(root, feature)
	if err != nil {
		reportAmbiguous(w, err, jsonOut)
		return cli.Exit(1)
	}
	missionType := missionTypeOf(featureDir)
	active := missionType
	if active == "" {
		active = "no mission context"
	}
	ws := worktreeStatus{root: root}
	d := jsonx.NewObject("project_path", root, "current_working_directory", cwd, "git_branch", nil,
		"in_worktree", strings.Contains(cwd, ".worktrees"), "worktrees_exist", exists(filepath.Join(root, ".worktrees")),
		"active_mission", active, "file_integrity", &jsonx.Object{}, "worktree_overview", &jsonx.Object{}, "current_feature", &jsonx.Object{},
		"all_features", []any{}, "observations", []any{}, "issues", []any{})
	if b, ok := currentBranch(root); ok {
		d.Set("git_branch", b)
	} else {
		d.Set("issues", []any{"Could not detect git branch"})
	}
	expected, present, missing, total := fileIntegrity(root, missionType)
	mf := []any{}
	for _, cat := range manifestCategories {
		for _, f := range expected[cat] {
			if _, ok := missing[f]; ok {
				mf = append(mf, f)
			}
		}
	}
	d.Set("file_integrity", jsonx.NewObject("total_expected", total, "total_present", len(present), "total_missing", len(missing), "missing_files", mf))
	sum := ws.summary()
	d.Set("worktree_overview", sum)
	all := []any{}
	for _, f := range ws.allFeatures() {
		st := ws.featureStatus(f)
		all = append(all, jsonx.NewObject("name", f, "state", st.GetString("state"), "branch_exists", st.GetBool("branch_exists"),
			"branch_merged", st.GetBool("branch_merged"), "worktree_exists", st.GetBool("worktree_exists"), "worktree_path", mustGet(st, "worktree_path"),
			"artifacts_in_main", st.GetList("artifacts_in_main"), "artifacts_in_worktree", st.GetList("artifacts_in_worktree")))
	}
	d.Set("all_features", all)
	if featureDir != "" {
		name := filepath.Base(featureDir)
		st := ws.featureStatus(name)
		d.Set("current_feature", jsonx.NewObject("detected", true, "name", name, "state", st.GetString("state"), "branch_exists", st.GetBool("branch_exists"),
			"branch_merged", st.GetBool("branch_merged"), "worktree_exists", st.GetBool("worktree_exists"), "worktree_path", mustGet(st, "worktree_path"),
			"artifacts_in_main", st.GetList("artifacts_in_main"), "artifacts_in_worktree", st.GetList("artifacts_in_worktree")))
	}
	var obs []any
	cf := d.GetObject("current_feature")
	if d.GetString("git_branch") == primaryBranch(root) && d.GetBool("in_worktree") {
		obs = append(obs, "Unusual: In worktree but on main branch")
	}
	if cf.GetBool("detected") && cf.GetString("state") == "in_development" && !cf.GetBool("worktree_exists") {
		obs = append(obs, fmt.Sprintf("Mission %s has no worktree but has development artifacts", cf.GetString("name")))
	}
	if len(missing) > 0 {
		obs = append(obs, fmt.Sprintf("Mission integrity: %d expected files not found", len(missing)))
	}
	if n, _ := sum.GetInt("active_worktrees"); n > 5 {
		obs = append(obs, fmt.Sprintf("Multiple worktrees active: %d", n))
	}
	if obs == nil {
		obs = []any{}
	}
	d.Set("observations", obs)
	var status map[string]bool
	if withTools {
		var tools *jsonx.Object
		tools, status = checkTools(nil)
		d.Set("tools", tools)
	}
	if jsonOut {
		printJSONRaw(w, d)
		return nil
	}
	printDiagnostics(w, d, withTools, status)
	return nil
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func yesNo(b bool) string {
	if b {
		return "[green]Yes[/green]"
	}
	return "[red]No[/red]"
}

func printDiagnostics(w io.Writer, d *jsonx.Object, withTools bool, status map[string]bool) {
	if withTools && status != nil {
		out(w, "[bold]Checking for installed tools...[/bold]\n")
		t := newToolTracker()
		for _, tl := range toolLabels {
			if status[tl[0]] {
				t.complete(tl[0], "available")
			} else {
				t.set(tl[0], "skipped", "not found")
			}
		}
		t.render(w)
		fmt.Fprintln(w)
		toolTips(w, status)
		fmt.Fprintln(w)
	}
	branch := d.GetString("git_branch")
	if branch == "" {
		branch = "[yellow]Not detected[/yellow]"
	}
	active := d.GetString("active_mission")
	if active == "" {
		active = "[yellow]None[/yellow]"
	}
	rich.Panel(w, fmt.Sprintf("[bold]Project Path:[/bold] %s\n[bold]Current Directory:[/bold] %s\n[bold]Git Branch:[/bold] %s\n[bold]Active Mission:[/bold] %s",
		d.GetString("project_path"), d.GetString("current_working_directory"), branch, active), "Project Information")
	fi := d.GetObject("file_integrity")
	te, _ := fi.GetInt("total_expected")
	tp, _ := fi.GetInt("total_present")
	tm, _ := fi.GetInt("total_missing")
	integ := "[green]✓ All files present[/green]"
	if tm != 0 {
		integ = fmt.Sprintf("[yellow]⚠ %d files missing[/yellow]", tm)
	}
	info := fmt.Sprintf("[bold]Files:[/bold] %d/%d present %s", tp, te, integ)
	if mf := fi.GetList("missing_files"); len(mf) > 0 {
		info += "\n\n[red]Missing:[/red]"
		for i, f := range mf {
			if i == 5 {
				break
			}
			info += fmt.Sprintf("\n  • %v", f)
		}
		if len(mf) > 5 {
			info += fmt.Sprintf("\n  ... and %d more", len(mf)-5)
		}
	}
	rich.Panel(w, info, "File Integrity")
	wo := d.GetObject("worktree_overview")
	aw, _ := wo.GetInt("active_worktrees")
	tf, _ := wo.GetInt("total_features")
	rich.Panel(w, fmt.Sprintf("[bold]Worktrees Exist:[/bold] %s\n[bold]Currently in Worktree:[/bold] %s\n[bold]Active Worktrees:[/bold] %d\n[bold]Total Features:[/bold] %d",
		yesNo(d.GetBool("worktrees_exist")), yesNo(d.GetBool("in_worktree")), aw, tf), "Worktrees")
	cf := d.GetObject("current_feature")
	if cf.GetBool("detected") {
		rich.Panel(w, fmt.Sprintf("[bold]Detected Feature:[/bold] %s\n[bold]State:[/bold] %s\n[bold]Branch Exists:[/bold] %s\n[bold]Worktree Exists:[/bold] %s",
			cf.GetString("name"), cf.GetString("state"), yesNo(cf.GetBool("branch_exists")), yesNo(cf.GetBool("worktree_exists"))), "Current Feature")
	} else {
		rich.Panel(w, "[yellow]No feature detected in current context[/yellow]", "Current Feature")
	}
	if all := d.GetList("all_features"); len(all) > 0 {
		var rows [][]string
		for _, x := range all {
			f := x.(*jsonx.Object)
			b, m, wt := "✗", "○", "✗"
			if f.GetBool("branch_exists") {
				b = "✓"
			}
			if f.GetBool("branch_merged") {
				m = "✓"
			}
			if f.GetBool("worktree_exists") {
				wt = "✓"
			}
			rows = append(rows, []string{f.GetString("name"), f.GetString("state"), b, m, wt})
		}
		richTable(w, "All Features", []string{"Feature", "State", "Branch", "Merged", "Worktree"}, []byte("llccc"), rows)
	} else {
		out(w, "[yellow]No features found[/yellow]")
	}
	obs, issues := d.GetList("observations"), d.GetList("issues")
	if len(obs) > 0 || len(issues) > 0 {
		fmt.Fprintln(w)
		if len(obs) > 0 {
			out(w, "[bold cyan]📝 Observations:[/bold cyan]")
			for _, o := range obs {
				out(w, fmt.Sprintf("  • %v", o))
			}
		}
		if len(issues) > 0 {
			out(w, "[bold red]⚠️  Issues:[/bold red]")
			for _, o := range issues {
				out(w, fmt.Sprintf("  • %v", o))
			}
		}
	} else {
		out(w, "\n[bold green]✓ No issues or observations[/bold green]")
	}
}
