// Package create implements mission creation (upstream
// specify_cli.core.mission_creation): identity minting, the kitty-specs
// scaffold, meta.json, the coordination branch + worktree seed, the
// creation lifecycle events and the scaffold commits.
package create

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/lifecycle"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Topologies.
const (
	SingleBranch   = "single_branch"
	Lanes          = "lanes"
	Coord          = "coord"
	LanesWithCoord = "lanes_with_coord"
)

// MintsCoordination reports the coordination-bearing topologies.
func MintsCoordination(topology string) bool { return topology == Coord || topology == LanesWithCoord }

// Error is MissionCreationError (ErrorCode optional).
type Error struct {
	Msg       string
	ErrorCode string
}

func (e *Error) Error() string { return e.Msg }

func fail(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// DivergedError is CoordinationBranchDiverged.
type DivergedError struct{ CoordinationBranch, TargetBranch, NextStep string }

func (e *DivergedError) Error() string {
	return fmt.Sprintf("Coordination branch '%s' has diverged from target '%s'. %s", e.CoordinationBranch, e.TargetBranch, e.NextStep)
}

// ToObject renders to_dict().
func (e *DivergedError) ToObject() *jsonx.Object {
	return jsonx.NewObject("error_code", "COORDINATION_BRANCH_DIVERGED", "coordination_branch", e.CoordinationBranch,
		"target_branch", e.TargetBranch, "next_step", e.NextStep)
}

// Options are the create inputs.
type Options struct {
	Root                 string // repository root
	Cwd                  string // process working directory
	Slug                 string
	MissionType          string // "" → software-dev
	TargetBranch         string
	FriendlyName         *string
	PurposeTLDR          *string
	PurposeContext       *string
	PRBound              bool
	Topology             string
	ForceRecreateCoord   bool
	RetainBranches       bool
	RetainWorktrees      bool
	CommitToTarget       bool
	AllowDuplicate       bool
	MissionID, CreatedAt string // test seams
}

// Result is MissionCreationResult.
type Result struct {
	FeatureDir                string
	MissionSlug               string
	Meta                      *jsonx.Object
	TargetBranch              string
	CurrentBranch             string
	CreatedFiles              []string
	UncommittedFiles          []string
	CoordinationBranch        string
	CoordinationBranchCreated bool
	ScaffoldCommitSkipped     bool
}

func validate(slug string, friendly *string) (string, error) {
	if !mission.KebabPattern.MatchString(slug) {
		return "", fail("Invalid mission slug '%s'. Must be kebab-case (lowercase letters, numbers, hyphens only).\n\nValid examples:\n  - user-auth\n  - fix-bug-123\n  - 068-feature-name\n  - new-dashboard\n\nInvalid examples:\n  - User-Auth (uppercase)\n  - user_auth (underscores)", slug)
	}
	name := ""
	if friendly != nil {
		name = strings.Join(strings.Fields(*friendly), " ")
		if name == "" {
			return "", fail("Mission creation requires a non-empty friendly_name.")
		}
	}
	if name == "" {
		name = DisplayName(slug)
	}
	return name, nil
}

// DisplayName is default_mission_display_name.
func DisplayName(slug string) string {
	var parts []string
	for _, p := range strings.Split(strings.TrimSpace(slug), "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "Mission"
	}
	return strings.Join(parts, " ")
}

// IsWorktreeContext mirrors is_worktree_context.
func IsWorktreeContext(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == project.WorktreesDir {
			return true
		}
	}
	for d := path; ; {
		g := filepath.Join(d, ".git")
		if st, err := os.Lstat(g); err == nil {
			if st.IsDir() {
				return false
			}
			b, _ := os.ReadFile(g)
			return strings.HasPrefix(strings.TrimSpace(string(b)), "gitdir:")
		}
		p := filepath.Dir(d)
		if p == d {
			return false
		}
		d = p
	}
}

// ProtectedBranches resolves the protection policy branch set.
func ProtectedBranches(root string) map[string]bool {
	out := map[string]bool{}
	cfg := mission.ProjectConfig(root)
	if p := cfg.GetObject("protection"); p != nil {
		if v, ok := p.Get("protected_branches"); ok && v != nil {
			for _, x := range cfg.GetObject("protection").GetList("protected_branches") {
				if s, ok := x.(string); ok {
					out[s] = true
				}
			}
			return out
		}
	}
	out["main"] = true
	out["master"] = true
	return out
}

// ActivatedMissionTypes reads mission_type_activations (sorted).
func ActivatedMissionTypes(root string) []string {
	var out []string
	for _, x := range mission.ProjectConfig(root).GetList("mission_type_activations") {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

var specTemplates = map[string]string{
	"software-dev":  "packs/built-in/missions/software-dev/templates/spec-template.md",
	"documentation": "packs/built-in/missions/documentation/templates/documentation-spec-template.md",
	"research":      "packs/built-in/missions/research/templates/research-spec-template.md",
	"plan":          "packs/built-in/missions/plan/templates/plan-spec-skeleton.md",
}

// SpecTemplate resolves the activated mission type's spec template content.
func SpecTemplate(root, missionType string) ([]byte, error) {
	types := ActivatedMissionTypes(root)
	if len(types) == 0 {
		return nil, &Error{ErrorCode: "ACTIVE_CHARTER_CONFIG", Msg: "This project has no activated mission types, so a mission cannot be created. A mission requires at least one activated mission type. Provision the project's charter: run `spec-kitty init` (new project) or `spec-kitty upgrade` (existing project), or add a non-empty `mission_type_activations` list to .kittify/config.yaml (or the charter.yaml it points to)."}
	}
	found := false
	for _, t := range types {
		if t == missionType {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("Unknown mission type '%s'. Registered types: %s.", missionType, strings.Join(types, ", "))
	}
	for _, p := range []string{
		filepath.Join(root, ".kittify", "overrides", "templates", filepath.Base(specTemplates[missionType])),
	} {
		if b, err := os.ReadFile(p); err == nil && specTemplates[missionType] != "" {
			return b, nil
		}
	}
	rel, ok := specTemplates[missionType]
	if !ok {
		others := []string{}
		for _, t := range types {
			if t != missionType {
				others = append(others, t)
			}
		}
		msg := fmt.Sprintf("Mission type '%s' is activated but has no loadable profile.", missionType)
		if len(others) > 0 {
			msg += " Other activated mission types: " + strings.Join(others, ", ") + "."
		}
		return nil, errors.New(msg)
	}
	return assets.ReadFile(rel)
}

// TasksReadme renders tasks/README.md.
func TasksReadme(branch string) string {
	return strings.ReplaceAll(tasksReadmeTemplate, "{planning_branch}", branch)
}

const tasksReadmeTemplate = "# Tasks Directory\n\nThis directory contains work package (WP) prompt files.\n\n## Directory Structure (v0.9.0+)\n\n```\ntasks/\n├── WP01-setup-infrastructure.md\n├── WP02-user-authentication.md\n├── WP03-api-endpoints.md\n└── README.md\n```\n\nAll WP files are stored flat in `tasks/`. Status is tracked in `status.events.jsonl`, not in WP frontmatter.\n\n## Work Package File Format\n\nEach WP file **MUST** use YAML frontmatter:\n\n```yaml\n---\nwork_package_id: \"WP01\"\ntitle: \"Work Package Title\"\ndependencies: []\nplanning_base_branch: \"{planning_branch}\"\nmerge_target_branch: \"{planning_branch}\"\nbranch_strategy: \"Planning artifacts were generated on {planning_branch}; completed changes must merge back into {planning_branch}.\"\nsubtasks:\n  - \"T001\"\n  - \"T002\"\nphase: \"Phase 1 - Setup\"\nassignee: \"\"\nagent: \"\"\nshell_pid: \"\"\nhistory:\n  - timestamp: \"2025-01-01T00:00:00Z\"\n    agent: \"system\"\n    action: \"Prompt generated via /spec-kitty.tasks\"\n---\n\n# Work Package Prompt: WP01 – Work Package Title\n\n[Content follows...]\n```\n\n## Status Tracking\n\nStatus is tracked via the canonical event log (`status.events.jsonl`), not in WP frontmatter.\nUse `spec-kitty agent tasks move-task` to change WP status:\n\n```bash\nspec-kitty agent tasks move-task <WPID> --to <lane>\n```\n\nExample:\n```bash\nspec-kitty agent tasks move-task WP01 --to doing\n```\n\n## File Naming\n\n- Format: `WP01-kebab-case-slug.md`\n- Examples: `WP01-setup-infrastructure.md`, `WP02-user-auth.md`\n"

func isTracked(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return gitx.OK(root, "ls-files", "--error-unmatch", "--", filepath.ToSlash(rel))
}

// findLiveDuplicate implements the #4033 idempotency guard.
func findLiveDuplicate(root, slug, missionType string) (string, string, bool) {
	base := mission.StripNumericPrefix(slug)
	entries, _ := os.ReadDir(project.Specs(root))
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		mid8 := ""
		if name != base {
			if !strings.HasPrefix(name, base+"-") {
				continue
			}
			tail := name[len(base)+1:]
			if len(tail) != 8 || !isAlnum(tail) {
				continue
			}
			mid8 = tail
		}
		dir := project.Specs(root, name)
		meta, err := mission.ReadMeta(dir)
		if err != nil {
			return name, mid8, true
		}
		t := meta.GetString("mission_type")
		if t == "" {
			t = "software-dev"
		}
		if t != missionType {
			continue
		}
		if mid8 == "" {
			mid8 = meta.GetString("mid8")
		}
		if abandoned(root, dir) {
			continue
		}
		return name, mid8, true
	}
	return "", "", false
}

func isAlnum(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func abandoned(root, dir string) bool {
	snap, err := status.MaterializeSnapshot(dir)
	if err != nil {
		return false
	}
	pairs := snap.WorkPackages.Pairs()
	if len(pairs) > 0 {
		all := true
		for _, kv := range pairs {
			if kv.Value.(*jsonx.Object).GetString("lane") != string(status.Canceled) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	if snap.EventCount != 0 {
		return false
	}
	return !isTracked(root, filepath.Join(dir, "spec.md"))
}

// ensureCoordinationBranch mints or validates the coordination branch.
func ensureCoordinationBranch(root, slug, missionID, target string, force bool) (name string, created bool, skipped bool, err error) {
	name = lanes.MissionBranchName(slug, missionID)
	if !gitx.OK(root, "rev-parse", "--verify", "--quiet", target) {
		return name, false, true, nil
	}
	exists := gitx.BranchExists(root, name)
	if exists && force {
		wt := lanes.CoordWorktreePath(root, slug, missionID[:8])
		if st, e := os.Stat(wt); e == nil && st.IsDir() {
			gitx.Run(root, "worktree", "remove", "--force", wt)
		}
		if _, err = gitx.Run(root, "branch", "-D", name); err != nil {
			return
		}
		exists = false
	}
	if exists {
		if gitx.IsAncestor(root, name, target) {
			return name, false, false, nil
		}
		return name, false, false, &DivergedError{CoordinationBranch: name, TargetBranch: target,
			NextStep: fmt.Sprintf("Either rebase '%s' onto '%s', or re-run mission create with --force-recreate-coordination-branch to reset the branch to the target.", name, target)}
	}
	if _, err = gitx.Run(root, "branch", name, target); err != nil {
		return name, false, false, fmt.Errorf("Failed to create coordination branch '%s' off '%s': %v", name, target, err)
	}
	return name, true, false, nil
}

// CoordSeedTrailer is the trailer key on create-time coordination commits.
const CoordSeedTrailer = "Spec-Kitty-Coordination-Seed"

func touch(p string) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Create runs the mission creation core.
func Create(o Options) (*Result, error) {
	friendly, err := validate(o.Slug, o.FriendlyName)
	if err != nil {
		return nil, err
	}
	topology := o.Topology
	if topology == "" {
		topology = Coord
	}
	if o.CommitToTarget && topology != SingleBranch {
		return nil, fail("--commit-to-target is only valid for a single_branch mission (got topology '%s'). Pass --topology single_branch, or drop --commit-to-target.", topology)
	}
	if IsWorktreeContext(o.Cwd) {
		return nil, fail("Cannot create missions from inside a worktree. Run from the project root checkout.")
	}
	root := o.Root
	if root == "" {
		return nil, fail("Could not locate project root. Run from within spec-kitty repository.")
	}
	if !gitx.IsRepo(root) {
		return nil, fail("Not in a git repository. Mission creation requires git.")
	}
	if !gitx.OK(root, "rev-parse", "--verify", "--quiet", "HEAD") {
		return nil, fail("This checkout has no commits yet, so Spec Kitty cannot commit the mission scaffold.\n\nMake an initial commit first, then create the mission:\n  git commit --allow-empty -m 'Initial commit'\n\nIf the repository already has files staged, commit those instead.")
	}
	current := gitx.CurrentBranch(root)
	if current == "" || current == "HEAD" {
		return nil, fail("Must be on a branch to create missions (detached HEAD detected).")
	}
	planning := o.TargetBranch
	if planning == "" {
		planning = current
	}
	missionType := o.MissionType
	if missionType == "" {
		missionType = "software-dev"
	}
	if !o.AllowDuplicate {
		if name, mid8, ok := findLiveDuplicate(root, o.Slug, missionType); ok {
			return nil, &Error{ErrorCode: "MISSION_ALREADY_EXISTS", Msg: fmt.Sprintf("A mission named '%s' of type '%s' already exists and is not abandoned: %s (mid8 %s). Refusing to silently create a duplicate (#4033).\n\nIf the prior mission is genuinely abandoned (canceled, or never actually worked), re-run this create with no flag -- abandoned priors are auto-allowed.\n\nTo deliberately create a second mission with the same name, pass --allow-duplicate (create_mission_core(allow_duplicate=True) for programmatic callers).",
				mission.StripNumericPrefix(o.Slug), missionType, name, mid8)}
		}
	}
	tldr := friendly
	if o.PurposeTLDR != nil {
		tldr = strings.Join(strings.Fields(*o.PurposeTLDR), " ")
	}
	pctx := lifecycle.DefaultPurposeContext(friendly, planning)
	if o.PurposeContext != nil {
		pctx = strings.Join(strings.Fields(*o.PurposeContext), " ")
	}
	var perr []string
	if tldr == "" {
		perr = append(perr, "purpose_tldr is required and must be a non-empty string")
	}
	if pctx == "" {
		perr = append(perr, "purpose_context is required and must be a non-empty string")
	}
	if len(perr) > 0 {
		return nil, fail("%s", strings.Join(perr, " "))
	}
	tmpl, err := SpecTemplate(root, missionType)
	if err != nil {
		return nil, err
	}
	missionID := o.MissionID
	if missionID == "" {
		missionID = mission.NewMissionID()
	}
	mid8 := missionID[:8]
	dirName := mission.DirName(o.Slug, mid8)
	featureDir := project.Specs(root, dirName)
	coordRouted := MintsCoordination(topology)

	// scaffold
	for _, d := range []string{featureDir, filepath.Join(featureDir, "checklists"), filepath.Join(featureDir, "research"), filepath.Join(featureDir, "tasks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	touch(filepath.Join(featureDir, "tasks", ".gitkeep"))
	if !coordRouted {
		touch(filepath.Join(featureDir, status.EventsFile))
	}
	readme := filepath.Join(featureDir, "tasks", "README.md")
	if err := os.WriteFile(readme, []byte(TasksReadme(planning)), 0o644); err != nil {
		return nil, err
	}
	specFile := filepath.Join(featureDir, "spec.md")
	if _, err := os.Stat(specFile); err != nil {
		if err := os.WriteFile(specFile, tmpl, 0o644); err != nil {
			return nil, err
		}
	}

	// meta
	meta := mission.ReadMetaOrEmpty(featureDir)
	setDefault := func(k string, v any) {
		if !meta.Has(k) {
			meta.Set(k, v)
		}
	}
	createdAt := o.CreatedAt
	if createdAt == "" {
		createdAt = clock.NowUTCISO()
	}
	setDefault("mission_id", missionID)
	setDefault("mid8", mid8)
	setDefault("mission_number", nil)
	setDefault("slug", dirName)
	setDefault("mission_slug", dirName)
	setDefault("friendly_name", friendly)
	setDefault("purpose_tldr", tldr)
	setDefault("purpose_context", pctx)
	setDefault("mission_type", missionType)
	setDefault("target_branch", planning)
	setDefault("created_at", createdAt)
	for _, f := range []struct {
		k string
		v bool
	}{{"pr_bound", o.PRBound}, {"retain_branches", o.RetainBranches}, {"retain_worktrees", o.RetainWorktrees}, {"commit_to_target", o.CommitToTarget}} {
		if f.v {
			meta.Set(f.k, true)
		}
	}
	res := &Result{FeatureDir: featureDir, MissionSlug: dirName, TargetBranch: planning, CurrentBranch: current}
	coordSkipped := true
	if coordRouted {
		name, created, skipped, err := ensureCoordinationBranch(root, dirName, missionID, planning, o.ForceRecreateCoord)
		if err != nil {
			return nil, err
		}
		meta.Set("coordination_branch", name)
		res.CoordinationBranch = name
		res.CoordinationBranchCreated = created
		coordSkipped = skipped
	}
	meta.Set("topology", topology)
	setDefault("flattened", false)
	if err := mission.WriteMeta(featureDir, meta, true); err != nil {
		return nil, err
	}
	if missionType == "documentation" && !meta.Has("documentation_state") {
		meta.Set("documentation_state", jsonx.NewObject("iteration_mode", "initial", "divio_types_selected", []any{},
			"generators_configured", []any{}, "target_audience", "developers", "last_audit_date", nil, "coverage_percentage", 0.0))
		if err := mission.WriteMeta(featureDir, meta, true); err != nil {
			return nil, err
		}
	}
	res.Meta = meta

	// coordination seed
	statusDir := featureDir
	coordSeeded := false
	var coordWT string
	if coordRouted && !coordSkipped {
		coordWT = lanes.CoordWorktreePath(root, dirName, mid8)
		if st, err := os.Stat(coordWT); err != nil || !st.IsDir() {
			os.MkdirAll(filepath.Dir(coordWT), 0o755)
			if _, err := gitx.Run(root, "worktree", "add", coordWT, res.CoordinationBranch); err != nil {
				return nil, err
			}
		}
		statusDir = lanes.CoordMissionDir(root, dirName, mid8)
		if err := os.MkdirAll(statusDir, 0o755); err != nil {
			return nil, err
		}
		coordSeeded = true
	}

	// creation events
	puuid, pslug := mission.ProjectIdentity(root)
	if _, err := lifecycle.EmitMissionCreated(statusDir, lifecycle.MissionCreatedInput{
		MissionSlug: dirName, MissionID: missionID, MissionType: missionType, TargetBranch: planning,
		FriendlyName: friendly, PurposeTLDR: tldr, PurposeContext: pctx, CreatedAt: createdAt,
		Actor: lifecycle.LocalActor(root), ProjectUUID: puuid, ProjectSlug: pslug,
	}); err != nil {
		return nil, fail("Local canonical MissionCreated persistence failed for %s: %v. The partial scaffold is retained for explicit resume-probe diagnosis; do not retry create until it is repaired or removed.", mission.PyRepr(dirName), err)
	}
	specRel, _ := filepath.Rel(root, specFile)
	lifecycle.EmitArtifactPhase(statusDir, lifecycle.PhaseInput{EventType: lifecycle.SpecifyStarted, MissionSlug: dirName,
		Actor: "spec-kitty mission create", ArtifactPath: filepath.ToSlash(specRel)})

	if coordSeeded {
		msg := fmt.Sprintf("chore(%s): record mission creation\n\n%s: %s", dirName, CoordSeedTrailer, missionID)
		rel, _ := filepath.Rel(coordWT, status.EventsPath(statusDir))
		if _, err := gitx.Run(coordWT, "add", "--", filepath.ToSlash(rel)); err != nil {
			return nil, err
		}
		if !gitx.OK(coordWT, "diff", "--cached", "--quiet", "--", filepath.ToSlash(rel)) {
			if _, err := gitx.Run(coordWT, "commit", "-q", "-m", msg, "--", filepath.ToSlash(rel)); err != nil {
				return nil, fail("Failed to commit mission-creation events onto the coordination branch for %s: %v", mission.PyRepr(dirName), err)
			}
		}
	}

	// scaffold commit
	paths := []string{filepath.Join(featureDir, mission.MetaFile)}
	if !coordRouted {
		paths = append(paths, filepath.Join(featureDir, status.EventsFile))
	}
	paths = append(paths, readme, filepath.Join(featureDir, "tasks", ".gitkeep"))
	if ProtectedBranches(root)[planning] || gitx.CurrentBranch(root) != planning {
		res.ScaffoldCommitSkipped = true
	} else {
		var rel []string
		for _, p := range paths {
			r, _ := filepath.Rel(root, p)
			rel = append(rel, filepath.ToSlash(r))
		}
		if _, err := gitx.Run(root, append([]string{"add", "--"}, rel...)...); err != nil {
			return nil, fmt.Errorf("meta.json commit failed: %v", err)
		}
		if _, err := gitx.Run(root, append([]string{"commit", "-q", "-m", "Add scaffold for mission " + dirName, "--"}, rel...)...); err != nil {
			return nil, fmt.Errorf("meta.json commit failed: %v", err)
		}
	}

	// result file sets
	logPath := status.EventsPath(statusDir)
	res.CreatedFiles = []string{specFile, filepath.Join(featureDir, mission.MetaFile), readme}
	if coordSeeded {
		res.CreatedFiles = append(res.CreatedFiles, logPath)
	}
	res.UncommittedFiles = []string{specFile}
	if res.ScaffoldCommitSkipped {
		skipped := []string{filepath.Join(featureDir, mission.MetaFile), readme, filepath.Join(featureDir, "tasks", ".gitkeep")}
		if !coordSeeded {
			skipped = append(skipped, logPath)
		}
		for _, p := range skipped {
			found := false
			for _, c := range res.CreatedFiles {
				if c == p {
					found = true
				}
			}
			if !found {
				res.CreatedFiles = append(res.CreatedFiles, p)
			}
		}
		res.UncommittedFiles = append(res.UncommittedFiles, skipped...)
	}
	return res, nil
}
