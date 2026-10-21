package workflow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// AgentAssignment is the resolved agent 4-tuple of a WP.
type AgentAssignment struct {
	Tool, Model, ProfileID, Role string
}

// ResolveAgent is WPMetadata.resolved_agent().
func ResolveAgent(meta *jsonx.Object) AgentAssignment {
	str := func(k string) string {
		v, ok := meta.Get(k)
		if !ok || v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return status.PyStr(v)
	}
	model, profile, role := str("model"), str("agent_profile"), str("role")
	fb := AgentAssignment{Tool: "unknown", Model: model, ProfileID: profile, Role: role}
	if fb.Model == "" {
		fb.Model = "unknown-model"
	}
	v, _ := meta.Get("agent")
	switch a := v.(type) {
	case string:
		if a == "" {
			return fb
		}
		if !strings.Contains(a, ":") {
			fb.Tool = a
			return fb
		}
		seg := strings.Split(a, ":")
		for len(seg) < 4 {
			seg = append(seg, "")
		}
		out := AgentAssignment{Tool: seg[0], Model: seg[1], ProfileID: seg[2], Role: seg[3]}
		if out.Model == "" {
			out.Model = fb.Model
		}
		if out.ProfileID == "" {
			out.ProfileID = profile
			if out.ProfileID == "" {
				out.ProfileID = seg[0] + "-default"
			}
		}
		if out.Role == "" {
			out.Role = "implementer"
		}
		return out
	case *jsonx.Object:
		out := fb
		if t := a.GetString("tool"); t != "" {
			out.Tool = t
		}
		if m := a.GetString("model"); m != "" {
			out.Model = m
		}
		if p := a.GetString("profile_id"); p != "" {
			out.ProfileID = p
		}
		if r := a.GetString("role"); r != "" {
			out.Role = r
		}
		return out
	}
	return fb
}

// RenderResolvedAgentIdentity is render_resolved_agent_identity.
func RenderResolvedAgentIdentity(a AgentAssignment) []string {
	p, r := a.ProfileID, a.Role
	if p == "" {
		p = "(default)"
	}
	if r == "" {
		r = "(default)"
	}
	return []string{"Resolved agent identity:", "  tool       : " + a.Tool, "  model      : " + a.Model,
		"  profile_id : " + p, "  role       : " + r}
}

func ljust(s string, n int) string {
	if l := len([]rune(s)); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

// IsolationBanner is render_isolation_banner.
func IsolationBanner(wpID, mode string) []string {
	top := "╔" + strings.Repeat("=", 78) + "╗"
	lines := []string{top, "║  🚨 CRITICAL: WORK PACKAGE ISOLATION RULES                              ║", "╠" + strings.Repeat("=", 78) + "╣"}
	if mode == "implement" {
		lines = append(lines, "║  YOU ARE ASSIGNED TO: "+ljust(wpID, 55)+" ║")
	} else {
		lines = append(lines, "║  YOU ARE REVIEWING: "+ljust(wpID, 56)+" ║")
	}
	lines = append(lines, "║                                                                          ║",
		"║  ✅ DO:                                                                  ║",
		"║     • Only modify status of "+ljust(wpID, 47)+" ║")
	if mode == "implement" {
		lines = append(lines, "║     • Only mark subtasks belonging to "+ljust(wpID, 36)+" ║")
	}
	lines = append(lines, "║     • Ignore git commits and status changes from other agents           ║",
		"║                                                                          ║",
		"║  ❌ DO NOT:                                                              ║",
		"║     • Change status of any WP other than "+ljust(wpID, 34)+" ║",
		"║     • React to or investigate other WPs' status changes                 ║")
	if mode == "implement" {
		lines = append(lines, "║     • Mark subtasks that don't belong to "+ljust(wpID, 33)+" ║")
	} else {
		lines = append(lines, "║     • Review or approve any WP other than "+ljust(wpID, 32)+" ║")
	}
	lines = append(lines, "║                                                                          ║",
		"║  WHY: Multiple agents work in parallel. Each owns exactly ONE WP.       ║",
		"║       Git commits from other WPs are other agents - ignore them.        ║",
		"╚"+strings.Repeat("=", 78)+"╝")
	return lines
}

// WPPromptWrapper is render_wp_prompt_wrapper.
func WPPromptWrapper(text string) []string {
	top := "╔" + strings.Repeat("=", 78) + "╗"
	bot := "╚" + strings.Repeat("=", 78) + "╝"
	return []string{top, "║  WORK PACKAGE PROMPT BEGINS                                            ║", bot, "", text, "",
		top, "║  WORK PACKAGE PROMPT ENDS                                              ║", bot, ""}
}

// WorkspaceContractDescription is workspace_contract_description.
func WorkspaceContractDescription(ws *lanes.Workspace, wpID string) string {
	if ws.LaneID != "" {
		shared := strings.Join(ws.LaneWPIDs, ", ")
		if len(ws.LaneWPIDs) == 0 {
			shared = wpID
		}
		return fmt.Sprintf("Workspace contract: lane %s shared by %s", ws.LaneID, shared)
	}
	return "Workspace contract: repository root planning workspace"
}

// SharedArtifactGuidance is shared_artifact_guidance.
func SharedArtifactGuidance(ws *lanes.Workspace, root, slug string) []string {
	if ws.LaneID != "" {
		return []string{"📚 SHARED MISSION ARTIFACTS:",
			fmt.Sprintf("   Spec, plan, and tasks are visible from the primary checkout: %s/kitty-specs/%s/", root, slug),
			"   Status authority resolves through the coordination worktree for modern missions.",
			"   Use this lane workspace for code/tests; do not expect shared mission artifacts here"}
	}
	return []string{"📚 PLANNING ARTIFACTS:",
		"   This WP runs in the repository root: " + root,
		fmt.Sprintf("   Mission artifacts for this WP live here too: %s/kitty-specs/%s/", root, slug),
		"   Do not look for a separate lane worktree or workspace context file"}
}

// SafeCommitRecipe is _commit_recipes.safe_commit_recipe.
func SafeCommitRecipe(paths []string, message, branch string) string {
	s := "spec-kitty safe-commit " + strings.Join(paths, " ") + " -m \"" + message + "\""
	if branch != "" {
		s += " --to-branch " + branch
	}
	return s
}

// CharterContextText is render_charter_context_text.
func CharterContextText(root, action, missionType string) string {
	r, err := charter.BuildContext(root, action, charter.Options{MarkLoaded: true, MissionType: missionType})
	if err != nil {
		return fmt.Sprintf("Governance: unavailable (%v)", err)
	}
	return r.Text
}

// PromptTmpDir is runtime.next._tmp_namespace.prompt_tmp_dir.
func PromptTmpDir(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	sum := sha256.Sum256([]byte(root))
	base := runtimeStateRoot()
	os.MkdirAll(base, 0o700)
	os.Chmod(base, 0o700)
	d := filepath.Join(base, "spec-kitty-prompts", hex.EncodeToString(sum[:])[:16])
	os.MkdirAll(d, 0o700)
	os.Chmod(d, 0o700)
	return d
}

// runtimeStateRoot is kernel.paths.get_runtime_state_root (~/.spec-kitty,
// honouring SPEC_KITTY_HOME).
func runtimeStateRoot() string {
	return project.GlobalSpecKitty()
}

// WritePromptFile writes a prompt file at mode 0600.
func WritePromptFile(path, content string) error {
	os.Remove(path)
	return os.WriteFile(path, []byte(content), 0o600)
}

// WriteWorkflowPrompt is write_prompt_to_file.
func WriteWorkflowPrompt(commandType, slug, wpID, content, root string) string {
	p := filepath.Join(PromptTmpDir(root), fmt.Sprintf("spec-kitty-%s-%s-%s.md", commandType, slug, wpID))
	WritePromptFile(p, content)
	return p
}

// ---------------------------------------------------------------------------
// Worktree topology (core.worktree_topology)
// ---------------------------------------------------------------------------

type topoEntry struct {
	wpID, execMode, kind, laneID, branch, base, lane string
	laneWPs, deps                                    []string
	exists                                           bool
	ahead                                            int
}

// TopologyJSON renders the WORKTREE_TOPOLOGY block (nil when the topology
// has no stacking or cannot be materialized).
func TopologyJSON(root, slug, currentWP string) ([]string, error) {
	primary := project.Specs(root, slug)
	meta := mission.ReadMetaOrEmpty(primary)
	target := meta.GetString("target_branch")
	m, err := lanes.Read(primary)
	if err != nil {
		return nil, err
	}
	graph := map[string][]string{}
	var ids []string
	for _, p := range wp.Files(primary) {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		id := w.String("work_package_id")
		if id == "" {
			id = wp.IDFromFilename(filepath.Base(p))
		}
		if id == "" {
			continue
		}
		graph[id] = w.Dependencies()
		ids = append(ids, id)
	}
	order, err := lanes.TopologicalSort(graph)
	if err != nil {
		order = append([]string{}, ids...)
		sort.Strings(order)
	}
	s := coord.Resolve(root, slug)
	snap := statusSnapshot(s)
	var entries []*topoEntry
	laneSet := map[string]bool{}
	kinds := map[string]bool{}
	for _, id := range order {
		ws, err := lanes.ResolveWorkspace(root, slug, id)
		if err != nil {
			return nil, err
		}
		var le *lanes.Lane
		if m != nil {
			le = m.LaneForWP(id)
		}
		if le == nil && ws.ResolutionKind != "repo_root" {
			return nil, fmt.Errorf("%s is not assigned to any lane in lanes.json", id)
		}
		e := &topoEntry{wpID: id, execMode: ws.ExecutionMode, kind: ws.ResolutionKind, deps: graph[id], exists: ws.Exists()}
		if le != nil {
			e.laneID = le.LaneID
			e.laneWPs = append([]string{}, le.WPIDs...)
			e.branch = lanes.LaneBranchName(slug, le.LaneID, target)
		} else {
			e.laneID = ws.LaneID
			e.laneWPs = ws.LaneWPIDs
			e.branch = ws.BranchName
		}
		if ws.ResolutionKind != "repo_root" && le != nil && m != nil {
			e.base = m.MissionBranch
		}
		if e.exists && e.base != "" {
			if n, err := strconv.Atoi(gitOut(ws.WorktreePath, "rev-list", "--count", e.base+"..HEAD")); err == nil {
				e.ahead = n
			}
		}
		e.lane = "planned"
		if l := snap.Lane(id); l != "" && l != status.Uninitialized {
			e.lane = string(l)
		}
		entries = append(entries, e)
		if e.laneID != "" {
			laneSet[e.laneID] = true
		}
		kinds[e.kind] = true
	}
	stacking := len(laneSet) > 1 || len(kinds) > 1
	for _, e := range entries {
		if len(e.laneWPs) > 1 {
			stacking = true
		}
	}
	if !stacking {
		return nil, nil
	}
	var cur *topoEntry
	for _, e := range entries {
		if e.wpID == currentWP {
			cur = e
		}
	}
	missionBranch := target
	if m != nil {
		missionBranch = m.MissionBranch
	}
	diffBase := target
	if cur != nil && cur.base != "" {
		diffBase = cur.base
	}
	var note string
	switch {
	case cur != nil && cur.kind == "repo_root":
		note = currentWP + " runs in the repository root planning workspace."
	case cur != nil && len(cur.laneWPs) > 1:
		note = fmt.Sprintf("%s shares lane %s with %s. Sequential WPs in the same lane reuse one worktree.", currentWP, cur.laneID, strings.Join(cur.laneWPs, ", "))
	case cur != nil:
		note = fmt.Sprintf("%s owns lane %s alone.", currentWP, cur.laneID)
	default:
		note = currentWP + " is not in the computed topology."
	}
	var items []any
	for _, e := range entries {
		o := jsonx.NewObject("wp", e.wpID, "status", e.lane, "execution_mode", e.execMode, "workspace_kind", e.kind,
			"lane_id", nilIfEmpty(e.laneID), "lane_wp_ids", strList(e.laneWPs), "branch", nilIfEmpty(e.branch), "base", nilIfEmpty(e.base))
		if e.exists {
			o.Set("commits_ahead", e.ahead)
		}
		if len(e.deps) > 0 {
			o.Set("dependencies", strList(e.deps))
		}
		items = append(items, o)
	}
	payload := jsonx.NewObject("mission_slug", slug, "mission_number", mission.MissionNumberString(meta, slug),
		"mission_type", mission.MissionType(meta), "target_branch", target, "mission_branch", missionBranch,
		"current_wp", currentWP, "diff_command", "git diff "+diffBase+"..HEAD",
		"shared_lane", cur != nil && len(cur.laneWPs) > 1, "note", note, "entries", items)
	return []string{"<!-- WORKTREE_TOPOLOGY -->", jsonx.Dumps(payload, jsonx.Pretty), "<!-- /WORKTREE_TOPOLOGY -->"}, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func strList(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// ---------------------------------------------------------------------------
// Fix-mode prompt (review.fix_prompt)
// ---------------------------------------------------------------------------

var extLanguage = map[string]string{".py": "python", ".ts": "typescript", ".tsx": "typescript", ".js": "javascript",
	".jsx": "javascript", ".rs": "rust", ".go": "go", ".java": "java", ".rb": "ruby", ".sh": "bash", ".bash": "bash",
	".zsh": "bash", ".md": "markdown", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".json": "json",
	".html": "html", ".css": "css", ".sql": "sql", ".tf": "hcl", ".hcl": "hcl"}

// ReviewCycleArtifact is the parsed review-cycle-N.md.
type ReviewCycleArtifact struct {
	CycleNumber   int
	Body          string
	Repro         string
	AffectedFiles [][2]string // path, line_range
}

// LoadReviewCycle parses a review-cycle artifact.
func LoadReviewCycle(path string) (*ReviewCycleArtifact, error) {
	d, err := fm.Read(path)
	if err != nil {
		return nil, err
	}
	a := &ReviewCycleArtifact{Body: d.Body}
	if n, ok := d.Meta.GetInt("cycle_number"); ok {
		a.CycleNumber = n
	}
	a.Repro = d.Meta.GetString("reproduction_command")
	if v, ok := d.Meta.Get("affected_files"); ok {
		if list, ok := v.([]any); ok {
			for _, it := range list {
				if o, ok := it.(*jsonx.Object); ok {
					a.AffectedFiles = append(a.AffectedFiles, [2]string{o.GetString("path"), o.GetString("line_range")})
				}
			}
		}
	}
	return a, nil
}

var cycleNameRe = regexp.MustCompile(`^review-cycle-(\d+)\.md$`)

// LatestReviewCycle returns the highest-numbered review-cycle artifact in dir.
func LatestReviewCycle(dir string) (*ReviewCycleArtifact, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	best, bestPath := -1, ""
	for _, e := range entries {
		if m := cycleNameRe.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > best {
				best, bestPath = n, filepath.Join(dir, e.Name())
			}
		}
	}
	if bestPath == "" {
		return nil, ""
	}
	a, err := LoadReviewCycle(bestPath)
	if err != nil {
		return nil, ""
	}
	return a, bestPath
}

// FixPrompt is generate_fix_prompt.
func FixPrompt(a *ReviewCycleArtifact, worktree, slug, wpID string) string {
	var s []string
	s = append(s, fmt.Sprintf("# Fix Mode: %s — Cycle %d", wpID, a.CycleNumber), "", "## Review Findings", "")
	if strings.TrimSpace(a.Body) != "" {
		s = append(s, strings.TrimSpace(a.Body))
	} else {
		s = append(s, "_(No review body provided — see reproduction command below.)_")
	}
	s = append(s, "")
	if len(a.AffectedFiles) > 0 {
		s = append(s, "## Affected Files", "")
		for _, af := range a.AffectedFiles {
			label := ""
			if af[1] != "" {
				label = " (lines " + af[1] + ")"
			}
			s = append(s, "### "+af[0]+label, "")
			snippet, truncated := extractSnippet(filepath.Join(worktree, af[0]), af[1])
			s = append(s, "```"+extLanguage[strings.ToLower(filepath.Ext(af[0]))], snippet, "```")
			if truncated {
				s = append(s, "_(File has more than 100 lines; only the first portion is shown. Read the full file if needed.)_")
			}
			s = append(s, "")
		}
	}
	if a.Repro != "" {
		s = append(s, "## Reproduction", "", "```bash", a.Repro, "```", "")
	}
	s = append(s, "## Instructions", "", "1. Read the review findings above carefully",
		"2. Fix ONLY the issues described — do not refactor or improve unrelated code")
	move := fmt.Sprintf("Move this WP back to for_review:\n   spec-kitty agent tasks move-task %s --to for_review --mission %s", wpID, slug)
	if a.Repro != "" {
		s = append(s, "3. Run the reproduction command to verify your fix", "4. Commit your changes", "5. "+move)
	} else {
		s = append(s, "3. Commit your changes", "4. "+move)
	}
	s = append(s, "")
	return strings.Join(s, "\n")
}

func extractSnippet(path, lineRange string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "[File not found: " + path + "]", false
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	total := len(lines)
	if lineRange != "" {
		parts := strings.Split(lineRange, "-")
		start, err1 := strconv.Atoi(parts[0])
		end := start
		var err2 error
		if len(parts) > 1 {
			end, err2 = strconv.Atoi(parts[1])
		}
		if err1 != nil || err2 != nil {
			start, end = 1, total
		} else {
			if start < 1 {
				start = 1
			}
			start = max(1, start-5)
			end = min(total, end+5)
		}
		if start-1 > end || start-1 > total {
			return "", false
		}
		return strings.Join(lines[start-1:end], "\n"), false
	}
	if total > 100 {
		return strings.Join(lines[:100], "\n"), true
	}
	return strings.Join(lines, "\n"), false
}

// ---------------------------------------------------------------------------
// Review prompt metadata (review.prompt_metadata)
// ---------------------------------------------------------------------------

var unsafeComponent = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeComponent(v string) string {
	s := strings.Trim(unsafeComponent.ReplaceAllString(v, "-"), "-")
	if s == "" {
		return "unknown"
	}
	return s
}

// tempDir is tempfile.gettempdir().
func tempDir() string {
	for _, k := range []string{"TMPDIR", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			if st, err := os.Stat(v); err == nil && st.IsDir() {
				return v
			}
		}
	}
	return "/tmp"
}

func newInvocationID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// ReviewPromptMeta is ReviewPromptMetadata.
type ReviewPromptMeta struct {
	InvocationID, RepoRoot, MissionID, MissionSlug, WPID, LaneWorktree, MissionBranch, LaneBranch, BaseRef, PromptPath, CreatedAt string
}

// NewReviewPromptMeta is build_review_prompt_metadata.
func NewReviewPromptMeta(root, missionID, slug, wpID, laneWorktree, missionBranch, laneBranch, baseRef string) *ReviewPromptMeta {
	inv := newInvocationID()
	sum := sha256.Sum256([]byte(root))
	name := strings.Trim(unsafeComponent.ReplaceAllString(filepath.Base(root), "-"), "-")
	if name == "" {
		name = "repo"
	}
	repoID := name + "-" + hex.EncodeToString(sum[:])[:12]
	p := filepath.Join(tempDir(), "spec-kitty-review-prompts", repoID, safeComponent(slug), safeComponent(wpID), safeComponent(inv)+".md")
	return &ReviewPromptMeta{InvocationID: inv, RepoRoot: root, MissionID: missionID, MissionSlug: slug, WPID: wpID,
		LaneWorktree: laneWorktree, MissionBranch: missionBranch, LaneBranch: laneBranch, BaseRef: baseRef,
		PromptPath: p, CreatedAt: strings.Replace(clock.NowUTCISO(), "+00:00", "Z", 1)}
}

// Write is write_review_prompt_with_metadata (frontmatter + body; keeps the
// newest 20 prompts of the WP directory).
func (m *ReviewPromptMeta) Write(content string) (string, error) {
	dir := filepath.Dir(m.PromptPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	meta := jsonx.NewObject("invocation_id", m.InvocationID, "repo_root", m.RepoRoot, "mission_id", m.MissionID,
		"mission_slug", m.MissionSlug, "work_package_id", m.WPID, "lane_worktree", m.LaneWorktree,
		"mission_branch", m.MissionBranch, "lane_branch", m.LaneBranch, "base_ref", m.BaseRef,
		"prompt_path", m.PromptPath, "created_at", m.CreatedAt)
	body := content
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	text := fm.Render(&fm.Doc{Meta: meta, Body: body}, fm.WPFieldOrder)
	if err := os.WriteFile(m.PromptPath, []byte(text), 0o644); err != nil {
		return "", err
	}
	pruneReviewPrompts(dir, filepath.Base(m.PromptPath), 20)
	return m.PromptPath, nil
}

func pruneReviewPrompts(dir, current string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		mt   int64
		path string
	}
	var others []f
	for _, e := range entries {
		if e.Name() == current || !strings.HasSuffix(e.Name(), ".md") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		others = append(others, f{info.ModTime().UnixNano(), filepath.Join(dir, e.Name())})
	}
	sort.Slice(others, func(i, j int) bool { return others[i].mt > others[j].mt })
	if keep-1 < len(others) {
		for _, o := range others[max(keep-1, 0):] {
			os.Remove(o.path)
		}
	}
}

// ReviewAntipatternChecklist is render_wp_review_antipattern_checklist().
const ReviewAntipatternChecklist = "## Anti-pattern checklist (WP-level cheap version of mission-review)\n\n" +
	"For each item below, state PASS / FAIL / N/A in your verdict. A FAIL on any\n" +
	"item blocks approval.\n\n" +
	"1. **Dead code**: every new public function, class, or module created by this\n" +
	"   WP has at least one live caller from production code, excluding tests. For\n" +
	"   each new module, run targeted import/call-site greps, for example\n" +
	"   `grep -r -e \"from <new_module> import\" -e \"import <new_module>\" src/ --include=\"*.<ext>\"`.\n" +
	"   Zero production hits means dead code.\n\n" +
	"2. **Synthetic-fixture test**: every test marked as covering an FR listed in\n" +
	"   this WP's frontmatter actually invokes the production code path that would\n" +
	"   produce the asserted shape. A test that constructs a literal dict matching\n" +
	"   the assertion is a synthetic fixture. Ask: if I delete the implementation\n" +
	"   code, does this test still pass? If yes, the FR is untested.\n\n" +
	"3. **Silent empty return**: search every new code path for `except ...:\n" +
	"   return \"\"`, `return None`, `return []`, `return {}`, or `pass`. Each hit\n" +
	"   must have a documented reason; absent that, it is a silent failure\n" +
	"   candidate.\n\n" +
	"4. **FR coverage**: every functional requirement in `requirement_refs`\n" +
	"   (`FR-###`, including a letter-suffixed `FR-###a`) has at least one test\n" +
	"   assertion that references the behavior it names, not just a comment or\n" +
	"   frontmatter entry. `SC-###` refs are tracked, not gated, and a\n" +
	"   `<mission-slug>#<ID>` ref cites another mission, so neither needs a test\n" +
	"   here.\n\n" +
	"5. **Frozen surface**: no commit in this WP modifies a file the spec,\n" +
	"   contract, or WP prompt marks as frozen or untouchable. For each frozen file,\n" +
	"   `git log --oneline <base>..HEAD -- <frozen-file>` must be empty.\n\n" +
	"6. **Locked decision**: no new code path contradicts a `MUST NOT` clause in\n" +
	"   `spec.md`, `plan.md`, or `contracts/`. Grep the diff for forbidden patterns\n" +
	"   named by those clauses.\n\n" +
	"7. **Shared-file ownership**: any file modified by this WP that is also\n" +
	"   modified by another WP, visible in `lanes.json`, shared lane metadata, or\n" +
	"   the same mission merge, has an explicit coordination note in the move-task\n" +
	"   reason or review feedback.\n\n" +
	"8. **Production fragility**: any new `raise` in a production code path has a\n" +
	"   documented fail-loud rationale. A bare `raise` in a request handler, worker,\n" +
	"   or CLI path that can fire on a transient race is a fragility risk.\n"
