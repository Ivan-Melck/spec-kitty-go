package ownedcheckout

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Fact is mission_runtime.OwnedCheckout: the validated ownership fact for
// one owned checkout, minted once per command.
type Fact struct {
	RepositoryRoot string // resolved repository-root checkout R
	OwnedRoot      string // resolved owned checkout P
	MissionDir     string // resolved P/kitty-specs/<slug>
	MissionSlug    string
	Topology       string
	WriteBranch    string // the branch P must be on and every owned write lands on
}

// Allowed topology sets.
var (
	LifecycleTopologies = []string{coord.SingleBranch}
	NextTopologies      = []string{coord.SingleBranch, coord.Lanes, coord.LanesWithCoord, coord.Coord}
)

// Options tune ResolveMission / ResolveOrAdopt.
type Options struct {
	Allowed        []string // nil = LifecycleTopologies
	TargetOverride *string
	DiscoverSole   bool
}

// SelectionRequired is OwnedMissionSelectionRequired: a handle-less
// discover_sole resolution found zero or several missions.
type SelectionRequired struct {
	Root     string // the claimed checkout the discovery listed
	Listings []mission.Listing
}

func (e *SelectionRequired) Error() string { return "mission selection required" }

// MetaReadError is MissionMetaReadError (meta.json present but corrupt).
type MetaReadError struct{ Path, Cause string }

func (e *MetaReadError) Error() string {
	return fmt.Sprintf("Cannot read %s: %s — fail-closed (meta.json exists but is corrupt or unreadable)", e.Path, e.Cause)
}

// loadMetaFailClosed is core.paths.load_meta_fail_closed.
func loadMetaFailClosed(dir string) (*jsonx.Object, error) {
	p := filepath.Join(dir, mission.MetaFile)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Expected JSON object in %s, got %s", p, pyjson.TypeName(v))}
	}
	return o, nil
}

// StoredTopology is MissionTopology.from_stored(meta["topology"]) ("" when
// absent or unknown).
func StoredTopology(meta *jsonx.Object) string {
	if meta == nil {
		return ""
	}
	v, _ := meta.Get("topology")
	s, ok := v.(string)
	if !ok {
		return ""
	}
	switch s {
	case coord.SingleBranch, coord.Lanes, coord.Coord, coord.LanesWithCoord:
		return s
	}
	return ""
}

// ExpectedWriteBranch is expected_write_branch(meta): the minted
// mission_branch of a single_branch mission, else target_branch.
func ExpectedWriteBranch(meta *jsonx.Object) string {
	target := ""
	if meta != nil {
		if v, ok := meta.Get("target_branch"); ok {
			if s, ok := v.(string); ok {
				target = s
			}
		}
	}
	if StoredTopology(meta) != coord.SingleBranch {
		return target
	}
	if v, ok := meta.Get("mission_branch"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return target
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func requireAllowedTopology(meta *jsonx.Object, topology string, allowed []string) error {
	if meta == nil || topology == "" {
		return refuse(OwnedTopologyUnsupported, "--owned-checkout could not determine the mission's stored topology.")
	}
	if !contains(allowed, topology) {
		names := append([]string{}, allowed...)
		sort.Strings(names)
		return refuse(OwnedTopologyUnsupported, fmt.Sprintf("--owned-checkout requires one of: %s (mission is %s).", strings.Join(names, ", "), topology))
	}
	allowsCoord := false
	for _, t := range allowed {
		if coord.RoutesThroughCoordination(t) {
			allowsCoord = true
		}
	}
	if v, _ := meta.Get("coordination_branch"); !allowsCoord && pyjson.Truthy(v) {
		return refuse(OwnedTopologyUnsupported, "--owned-checkout currently requires a mission with no coordination_branch.")
	}
	return nil
}

// CurrentBranch is core.git_ops.get_current_branch ("" for detached HEAD /
// not a repository).
func CurrentBranch(dir string) string {
	if !isDir(dir) {
		return ""
	}
	res, err := gitx.Exec(dir, nil, nil, "branch", "--show-current")
	if err != nil {
		return ""
	}
	if res.Code == 0 {
		return strings.TrimSpace(res.Stdout)
	}
	out, err := gitx.Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return strings.TrimSpace(out)
}

func branchMatches(current, writeBranch string, override *string, target string) bool {
	return current != "" && writeBranch != "" && current == writeBranch && (override == nil || *override == target)
}

func pyReprOrNone(s string) string {
	if s == "" {
		return "None"
	}
	return mission.PyRepr(s)
}

func requireWriteBranch(root string, meta *jsonx.Object, override *string) (string, error) {
	current := CurrentBranch(root)
	target := ""
	if v, ok := meta.Get("target_branch"); ok && pyjson.Truthy(v) {
		target = pyjson.Str(v)
	}
	expected := ExpectedWriteBranch(meta)
	if !branchMatches(current, expected, override, target) {
		return "", refuse(OwnedBranchRefused, fmt.Sprintf("The current branch (%s) must match the mission's expected write branch (%s); detached HEAD is unsupported.",
			pyReprOrNone(current), mission.PyRepr(expected)))
	}
	return expected, nil
}

// isCoordinationWorktree is _is_coordination_worktree (fail closed: the
// registry error propagates).
func isCoordinationWorktree(checkout, repositoryRoot string) (bool, error) {
	t, err := ClassifyWorktreeTopology(checkout, repositoryRoot)
	if err != nil {
		return false, err
	}
	return t == TopologyCoord, nil
}

// isLaneWorktreeOfMission is _is_lane_worktree_of_mission.
func isLaneWorktreeOfMission(checkout, repositoryRoot, dir string) bool {
	m, err := lanes.Read(dir)
	if err != nil || m == nil {
		return false
	}
	resolved := PyResolve(checkout)
	for _, l := range m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		if PyResolve(lanes.WorktreePath(repositoryRoot, filepath.Base(dir), l.LaneID)) == resolved {
			return true
		}
	}
	return false
}

func requireNotMissionWorktree(checkout, repositoryRoot, dir string) error {
	isCoord, err := isCoordinationWorktree(checkout, repositoryRoot)
	if err != nil {
		return refuse(WorktreeRegistryUnavailableCode, err.Error())
	}
	if isCoord || (dir != "" && isLaneWorktreeOfMission(checkout, repositoryRoot, dir)) {
		return refuse(OwnedCheckoutIsMissionWorktree, missionWorktreeRefusal)
	}
	return nil
}

func resolveMissionDirBestEffort(root, handle string) string {
	if strings.TrimSpace(handle) == "" {
		return ""
	}
	r, err := mission.ResolveIdentity(root, handle)
	if err != nil {
		return ""
	}
	return PyResolve(r.Dir)
}

func resolveOwnedDirectory(root, handle string) (*mission.Resolved, string, error) {
	if strings.TrimSpace(handle) == "" {
		return nil, "", refuse(MissionContextUnresolved, "--owned-checkout requires an explicit --mission.")
	}
	r, err := mission.ResolveIdentity(root, handle)
	if err != nil {
		return nil, "", refuse(MissionContextUnresolved, err.Error())
	}
	dir := PyResolve(r.Dir)
	if !isWithin(dir, PyResolve(project.Specs(root))) {
		return nil, "", refuse(OwnedMissionPathRefused, "Mission directory escapes the selected checkout.")
	}
	return r, dir, nil
}

func discoverSoleHandle(claimed string) (string, error) {
	l := mission.List(claimed)
	if len(l) == 1 {
		return l[0].Slug, nil
	}
	return "", &SelectionRequired{Root: claimed, Listings: l}
}

// ResolveMission is resolve_owned_mission: validate ownership before
// reading mission data; never fall back to the repository root.
func ResolveMission(repositoryRoot, checkout, handle string, opts Options) (*Fact, error) {
	allowed := opts.Allowed
	if allowed == nil {
		allowed = LifecycleTopologies
	}
	claim, err := requireOwnedClaim(repositoryRoot, checkout)
	if err != nil {
		return nil, err
	}
	root := claim.Claimed
	if opts.DiscoverSole && strings.TrimSpace(handle) == "" {
		h, err := discoverSoleHandle(root)
		if err != nil {
			return nil, err
		}
		handle = h
	}
	if err := requireNotMissionWorktree(checkout, repositoryRoot, resolveMissionDirBestEffort(repositoryRoot, handle)); err != nil {
		return nil, err
	}
	m, dir, err := resolveOwnedDirectory(root, handle)
	if err != nil {
		return nil, err
	}
	meta, err := loadMetaFailClosed(dir)
	if err != nil {
		return nil, err
	}
	topology := StoredTopology(meta)
	if err := requireAllowedTopology(meta, topology, allowed); err != nil {
		return nil, err
	}
	wb, err := requireWriteBranch(root, meta, opts.TargetOverride)
	if err != nil {
		return nil, err
	}
	f := &Fact{
		RepositoryRoot: PyResolve(repositoryRoot),
		OwnedRoot:      PyResolve(root),
		MissionDir:     PyResolve(dir),
		MissionSlug:    m.Slug,
		Topology:       topology,
		WriteBranch:    wb,
	}
	if f.IsProtected(f.WriteBranch, "") {
		return nil, refuse(OwnedBranchRefused, "Protected destination refused: "+f.WriteBranch)
	}
	entries, _ := os.ReadDir(f.MissionDir)
	for _, e := range entries {
		p := filepath.Join(f.MissionDir, e.Name())
		if _, err := filepath.EvalSymlinks(p); err != nil {
			if _, lerr := os.Lstat(p); lerr == nil && isLoop(p) {
				return nil, refuse(OwnedMissionPathRefused, "Path is outside the selected mission: "+p)
			}
		}
		if !isWithin(PyResolve(p), f.MissionDir) {
			return nil, refuse(OwnedMissionPathRefused, "Path is outside the selected mission: "+p)
		}
	}
	return f, nil
}

func isLoop(p string) bool {
	_, err := os.Stat(p)
	return err != nil && strings.Contains(err.Error(), "too many levels of symbolic links")
}

// CreateRoot is OwnedCreateRoot: the validated owned root for `agent
// mission create --owned-checkout`, before the mission exists.
type CreateRoot struct {
	RepositoryRoot string
	Checkout       string
}

// ResolveCreateRoot is resolve_owned_create_root.
func ResolveCreateRoot(repositoryRoot, checkout string) (*CreateRoot, error) {
	c, err := requireOwnedClaim(repositoryRoot, checkout)
	if err != nil {
		return nil, err
	}
	return &CreateRoot{RepositoryRoot: PyResolve(repositoryRoot), Checkout: PyResolve(c.Claimed)}, nil
}

// statusReadRoot is core.paths.get_status_read_root(cwd).
func statusReadRoot(cwd string) string {
	cur := PyResolve(cwd)
	for {
		marker := filepath.Join(cur, ".git")
		if isFile(marker) {
			if worktreeGitdir(marker) {
				return cur
			}
			break
		}
		if isDir(marker) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return project.MainRepoRoot(PyResolve(cwd))
}

func worktreeGitdir(marker string) bool {
	b, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	c := strings.TrimSpace(string(b))
	if !strings.HasPrefix(c, "gitdir:") {
		return false
	}
	g := strings.TrimSpace(strings.SplitN(c, ":", 2)[1])
	return filepath.Base(filepath.Dir(g)) == "worktrees" && strings.HasSuffix(filepath.Base(filepath.Dir(filepath.Dir(g))), ".git")
}

// adoptableToplevel is _adoptable_toplevel.
func adoptableToplevel(repositoryRoot, cwd string) string {
	top := PyResolve(statusReadRoot(cwd))
	if top == repositoryRoot {
		return ""
	}
	if c, err := isCoordinationWorktree(top, repositoryRoot); err != nil || c {
		return ""
	}
	return top
}

// Adopt is adopt_owned_checkout: validated flagless adoption. Returns nil
// whenever adoption does not apply; an error only for the
// MISSION_CONTEXT_CONFLICT case (or a corrupt meta.json).
func Adopt(repositoryRoot, cwd, handle string, allowed []string) (*Fact, error) {
	repositoryRoot = PyResolve(repositoryRoot)
	if strings.TrimSpace(handle) == "" {
		return nil, nil
	}
	top := adoptableToplevel(repositoryRoot, cwd)
	if top == "" {
		return nil, nil
	}
	tm, err := mission.ResolveIdentity(top, handle)
	if err != nil {
		return nil, nil
	}
	if isLaneWorktreeOfMission(top, repositoryRoot, PyResolve(tm.Dir)) {
		return nil, nil
	}
	if pm, err := mission.ResolveIdentity(repositoryRoot, handle); err == nil && pm.MissionID != tm.MissionID {
		return nil, refuse(MissionContextConflict, fmt.Sprintf("Mission selector resolves to different identities in the primary checkout (%s, %s) and the caller-owned checkout (%s, %s); refusing to guess.",
			pm.Slug, pm.MissionID, tm.Slug, tm.MissionID))
	}
	f, err := ResolveMission(repositoryRoot, top, handle, Options{Allowed: allowed})
	if err != nil {
		if _, ok := err.(*Error); ok {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// ResolveOrAdopt is _owned_checkout.resolve_owned_or_adopt. claim is the raw
// --owned-checkout value (nil when the flag was not given).
func ResolveOrAdopt(repositoryRoot string, claim *string, handle, cwd string, opts Options) (*Fact, error) {
	root := PyResolve(project.MainRepoRoot(repositoryRoot))
	allowed := opts.Allowed
	if allowed == nil {
		allowed = LifecycleTopologies
	}
	if claim != nil {
		checkout := *claim
		if checkout == "" {
			checkout = "."
		}
		if !filepath.IsAbs(checkout) {
			checkout = filepath.Join(cwd, checkout)
		}
		return ResolveMission(root, checkout, handle, Options{Allowed: allowed, TargetOverride: opts.TargetOverride, DiscoverSole: opts.DiscoverSole})
	}
	return Adopt(root, cwd, handle, allowed)
}

// RefuseAction is refuse_owned_action: validate the claim (and the mission
// when a handle is given), then refuse OWNED_ACTION_UNSUPPORTED.
func RefuseAction(repositoryRoot, claim, handle, action string) error {
	root := PyResolve(project.MainRepoRoot(repositoryRoot))
	if claim == "" {
		claim = "."
	}
	if strings.TrimSpace(handle) != "" {
		if _, err := ResolveMission(root, claim, handle, Options{Allowed: LifecycleTopologies}); err != nil {
			return err
		}
	} else if _, err := ResolveCreateRoot(root, claim); err != nil {
		return err
	}
	return refuse(OwnedActionUnsupported, fmt.Sprintf("'%s' does not support --owned-checkout yet. Use 'spec-kitty next --owned-checkout <P>' or 'spec-kitty agent tasks move-task --owned-checkout <P>' instead.", action))
}

// WouldAdopt is invoking_checkout_would_adopt.
func WouldAdopt(repositoryRoot, cwd string, allowed []string) bool {
	if allowed == nil {
		allowed = LifecycleTopologies
	}
	top := adoptableToplevel(PyResolve(repositoryRoot), cwd)
	if top == "" {
		return false
	}
	current := CurrentBranch(top)
	for _, l := range mission.List(top) {
		if !mission.SafePathSegment(l.Slug) {
			continue
		}
		dir := project.Specs(top, l.Slug)
		meta := mission.ReadMetaOrEmpty(dir)
		if !branchMatches(current, ExpectedWriteBranch(meta), nil, ExpectedWriteBranch(meta)) {
			continue
		}
		f, err := Adopt(repositoryRoot, cwd, l.Slug, allowed)
		if err != nil || f != nil {
			return true
		}
	}
	return false
}

// RequireUnstagedIndex is require_unstaged_index.
func RequireUnstagedIndex(f *Fact) error {
	if !gitx.OK(f.OwnedRoot, "diff", "--cached", "--quiet") {
		return refuse(OwnedIndexRefused, "The selected checkout must have no staged changes before this operation.")
	}
	return nil
}

// IsProtected is ProtectionPolicy.resolve_for_owned(fact,
// missionSlug).is_protected(branch): the union of R's and P's protected sets,
// with the fact's own mission's commit_to_target bypass ("" = own mission).
func (f *Fact) IsProtected(branch, missionSlug string) bool {
	return isProtectedOwned(f.RepositoryRoot, f.OwnedRoot, f.MissionDir, missionSlug == "" || missionSlug == filepath.Base(f.MissionDir), branch)
}

// IsProtectedCreate is resolve_for_owned over an OwnedCreateMission.
func (c *CreateRoot) IsProtected(missionDir, branch string) bool {
	return isProtectedOwned(c.RepositoryRoot, c.Checkout, missionDir, true, branch)
}

func isProtectedOwned(repoRoot, checkout, missionDir string, scoped bool, branch string) bool {
	if coord.HatchActive() {
		return false
	}
	if scoped {
		if meta, err := loadMetaFailClosed(missionDir); err == nil && meta != nil && StoredTopology(meta) == coord.SingleBranch {
			if v, ok := meta.Get("commit_to_target"); ok {
				if b, ok := v.(bool); ok && b {
					if t, ok := meta.Get("target_branch"); ok {
						if ts, ok := t.(string); ok && ts != "" && ts == branch {
							return false
						}
					}
				}
			}
		}
	}
	return coord.ProtectedBranches(repoRoot)[branch] || coord.ProtectedBranches(checkout)[branch]
}
