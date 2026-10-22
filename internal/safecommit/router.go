package safecommit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Router statuses (CommitRouterResult.status) and reason codes.
const (
	StatusCommitted         = "committed"
	StatusUnchanged         = "unchanged"
	StatusNoOpWrongSurface  = "no_op_wrong_surface"
	StatusError             = "error"
	ReasonAlreadyCommitted  = "no_op_already_committed"
	ReasonNoChanges         = "no_op_no_changes"
	ReasonCoordRecordInRoot = "COORD_RECORD_IN_ROOT_CHECKOUT"
	ReasonPathUnroutable    = "PATH_UNROUTABLE"
	ReasonWrongSurface      = "WRONG_SURFACE"
	ReasonProtectedRefused  = "PROTECTED_BRANCH_REFUSED"
	envHatch                = "SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS"
)

// PathFate is one skipped/refused path.
type PathFate struct {
	Path       string
	Reason     string
	OwningPath string // "" = None
}

// SurfaceOutcome is one partition group's commit outcome.
type SurfaceOutcome struct {
	Surface    string // primary | coordination
	Branch     string
	Status     string // committed | unchanged | refused | error
	CommitHash string // "" = None
	Committed  []string
	Skipped    []PathFate
	Refused    []PathFate
	Diagnostic string // "" = None
}

// RouterResult is CommitRouterResult.
type RouterResult struct {
	Status       string
	PlacementRef string
	CommitHash   string
	CommitHashes [][2]string
	Diagnostic   string
	Reason       string
	Surfaces     []SurfaceOutcome
}

// Success reports committed/unchanged with no refused/error surface.
func (r *RouterResult) Success() bool {
	if r.Status != StatusCommitted && r.Status != StatusUnchanged {
		return false
	}
	for _, s := range r.Surfaces {
		if s.Status == "refused" || s.Status == "error" {
			return false
		}
	}
	return true
}

func optStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fatePayload(f PathFate) *jsonx.Object {
	return jsonx.NewObject("path", f.Path, "reason", f.Reason, "owning_path", optStr(f.OwningPath))
}

// Payload is _surface_payload.
func (s SurfaceOutcome) Payload() *jsonx.Object {
	committed := []any{}
	for _, c := range s.Committed {
		committed = append(committed, c)
	}
	skipped, refused := []any{}, []any{}
	for _, f := range s.Skipped {
		skipped = append(skipped, fatePayload(f))
	}
	for _, f := range s.Refused {
		refused = append(refused, fatePayload(f))
	}
	return jsonx.NewObject("surface", s.Surface, "branch", s.Branch, "status", s.Status, "commit_hash", optStr(s.CommitHash),
		"committed", committed, "skipped", skipped, "refused", refused, "diagnostic", optStr(s.Diagnostic))
}

// SurfacesPayload is commit_outcome_payload(result)["surfaces"].
func (r *RouterResult) SurfacesPayload() []any {
	out := []any{}
	for _, s := range r.Surfaces {
		out = append(out, s.Payload())
	}
	return out
}

// Render is render_commit_outcome: one line per surface plus per-path lines.
func (r *RouterResult) Render() []string {
	var lines []string
	for _, o := range r.Surfaces {
		label := fmt.Sprintf("%s (%s)", o.Surface, o.Branch)
		glyph := "✓"
		if o.Status != StatusCommitted && o.Status != StatusUnchanged {
			glyph = "✗"
		}
		switch {
		case o.Status == StatusCommitted:
			h := o.CommitHash
			if len(h) > 7 {
				h = h[:7]
			}
			lines = append(lines, fmt.Sprintf("%s %s: committed %s — %d files", glyph, label, h, len(o.Committed)))
		case o.Status == StatusUnchanged:
			lines = append(lines, fmt.Sprintf("%s %s: unchanged", glyph, label))
		case o.Status == StatusError && len(o.Refused) == 0:
			suffix := ""
			if o.Diagnostic != "" {
				suffix = " — " + o.Diagnostic
			}
			lines = append(lines, fmt.Sprintf("%s %s: error%s", glyph, label, suffix))
		case o.Status == "refused" && len(o.Refused) == 0:
			lines = append(lines, fmt.Sprintf("%s %s: refused", glyph, label))
		}
		for _, f := range o.Refused {
			lines = append(lines, fmt.Sprintf("✗ %s: refused — %s: %s", label, f.Path, f.Reason))
		}
		for _, f := range o.Skipped {
			if f.Reason == ReasonCoordRecordInRoot {
				lines = append(lines, fmt.Sprintf("✓ %s: skipped — %s: %s", label, f.Path, f.Reason))
			}
		}
	}
	return lines
}

// PrintRendered prints Render() lines (markup=False).
func (r *RouterResult) PrintRendered(w io.Writer, wrap func(string) string) {
	for _, l := range r.Render() {
		fmt.Fprintln(w, wrap(l))
	}
}

// --- placement --------------------------------------------------------------

// IsPrimaryKind reports PRIMARY-partition membership.
func IsPrimaryKind(kind string) bool { return !mission.CoordinationKinds[kind] }

var commonPrimary = []string{"main", "master", "develop"}

// PrimaryBranch is git_ops.resolve_primary_branch(root) (default feature
// bias: the checked-out branch wins after origin/HEAD).
func PrimaryBranch(root string) string {
	if out, err := gitx.Run(root, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); b != "" {
			return b
		}
	}
	if current := gitx.CurrentBranch(root); current != "" && current != "HEAD" {
		return current
	}
	for _, b := range commonPrimary {
		if gitx.OK(root, "rev-parse", "--verify", b) {
			return b
		}
	}
	return "main"
}

// TargetBranch is get_feature_target_branch.
func TargetBranch(root, slug string) string {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, slug))
	if t := strings.TrimSpace(meta.GetString("target_branch")); t != "" {
		return t
	}
	return PrimaryBranch(root)
}

// Placement is resolve_placement_only(root, slug, kind=kind).ref (the E2
// short-circuit applies when the checkout carries the consolidated content;
// ResolvePlacement surfaces its refusal).
func Placement(root, slug, kind string) string {
	if ref, err := ResolvePlacement(root, slug, kind); err == nil {
		return ref
	}
	return topologyPlacement(root, slug, kind)
}

// topologyPlacement is the pre-E2 kind/topology projection.
func topologyPlacement(root, slug, kind string) string {
	target := TargetBranch(root, slug)
	if IsPrimaryKind(kind) {
		return target
	}
	s := coord.Resolve(root, slug)
	if s.CoordBranch != "" {
		return s.CoordBranch
	}
	return target
}

// --- router -----------------------------------------------------------------

// RouterOptions tune CommitForMission.
type RouterOptions struct {
	// CreatedThisInvocation are primary paths eligible for residue cleanup
	// after they were copied into the coordination worktree.
	CreatedThisInvocation map[string]bool
	// PrimaryRef retargets the PRIMARY group onto this branch.
	PrimaryRef string
}

func relpath(root, p string) string {
	r := ResolveCommitPath(realpath(root), p)
	if rel, err := filepath.Rel(realpath(root), r); err == nil && !strings.HasPrefix(rel, "..") {
		return toPosix(rel)
	}
	return toPosix(p)
}

// CommitForMission is commit_for_mission: commit files (absolute paths) to
// their kind-aware placement, splitting a mixed primary/coordination batch.
func CommitForMission(root, slug string, files []string, message, kind string, opts RouterOptions) *RouterResult {
	var primary, coordFiles []string
	for _, f := range files {
		if mission.IsCoordResidue(relpath(root, f)) {
			coordFiles = append(coordFiles, f)
		} else {
			primary = append(primary, f)
		}
	}
	callerPrimary := IsPrimaryKind(kind)
	if len(files) == 0 || (callerPrimary && len(coordFiles) == 0) || (!callerPrimary && len(primary) == 0) {
		return commitGroup(root, slug, files, message, kind, opts)
	}
	primaryKind, coordKind := kind, kind
	if !callerPrimary {
		primaryKind = representativeKind(root, primary, true, mission.KindSpec)
	} else {
		coordKind = representativeKind(root, coordFiles, false, mission.KindStatusState)
	}
	if len(primary) > 0 && len(coordFiles) > 0 && Placement(root, slug, primaryKind) == Placement(root, slug, coordKind) {
		return commitGroup(root, slug, files, message, kind, opts)
	}
	type group struct {
		kind  string
		files []string
	}
	var groups []group
	if len(primary) > 0 {
		groups = append(groups, group{primaryKind, primary})
	}
	if len(coordFiles) > 0 {
		groups = append(groups, group{coordKind, coordFiles})
	}
	var results []*RouterResult
	for _, g := range groups {
		results = append(results, commitGroup(root, slug, g.files, message, g.kind, opts))
	}
	var all []SurfaceOutcome
	var hashes [][2]string
	for _, r := range results {
		all = append(all, r.Surfaces...)
		hashes = append(hashes, r.CommitHashes...)
	}
	for _, r := range results {
		if r.Status == StatusError {
			out := *r
			out.Surfaces = all
			return &out
		}
	}
	for i, g := range groups {
		if IsPrimaryKind(g.kind) == callerPrimary {
			out := *results[i]
			out.CommitHashes, out.Surfaces = hashes, all
			return &out
		}
	}
	out := *results[0]
	out.CommitHashes, out.Surfaces = hashes, all
	return &out
}

func representativeKind(root string, files []string, expectPrimary bool, fallback string) string {
	for _, f := range files {
		k := mission.ArtifactKindForPath(relpath(root, f))
		if k != "" && IsPrimaryKind(k) == expectPrimary {
			return k
		}
	}
	return fallback
}

var preTasksKinds = map[string]bool{mission.KindSpec: true, mission.KindDataModel: true, mission.KindResearch: true, mission.KindChecklist: true, mission.KindContract: true}

func fates(root string, files []string, reason string) []PathFate {
	var out []PathFate
	for _, f := range files {
		out = append(out, PathFate{Path: relpath(root, f), Reason: reason})
	}
	return out
}

func commitGroup(root, slug string, files []string, message, kind string, opts RouterOptions) *RouterResult {
	placement := Placement(root, slug, kind)
	s := coord.Resolve(root, slug)
	primaryTarget := TargetBranch(root, slug)
	useCoord := coord.RoutesThroughCoordination(s.Topology) && placement != primaryTarget
	if !useCoord && opts.PrimaryRef != "" && opts.PrimaryRef != placement {
		placement = opts.PrimaryRef
		primaryTarget = placement
	}
	surface := "primary"
	if useCoord {
		surface = "coordination"
	}
	if !useCoord && coord.IsProtected(root, slug, placement) {
		var remedy string
		if preTasksKinds[kind] {
			remedy = fmt.Sprintf("No tasks have been generated for this mission yet, so the finalize-tasks --target-branch override has nothing durable to attach to and would silently revert. Start a mission on a topic branch instead: 'spec-kitty agent mission create %s --start-branch <topic-branch>'.", slug)
		} else {
			remedy = fmt.Sprintf("Check out or create a non-protected topic branch, then persist it onto this mission with: 'spec-kitty agent mission finalize-tasks --mission %s --target-branch <topic-branch>'.", slug)
		}
		diag := fmt.Sprintf("Refusing to commit planning artifacts to the protected branch '%s'. This mission's target_branch is protected. %s Planning artifacts must land on a topic branch. To commit on the current protected branch anyway, set %s=1.", placement, remedy, envHatch)
		return &RouterResult{Status: StatusNoOpWrongSurface, PlacementRef: placement, Diagnostic: diag,
			Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "refused", Refused: fates(root, files, ReasonProtectedRefused), Diagnostic: diag}}}
	}
	worktree, commitPaths := root, files
	if useCoord {
		if err := s.EnsureWorktree(); err != nil || s.CoordWorktree == "" {
			diag := fmt.Sprintf("Coordination worktree resolution failed for mission %s (mid8=%s): %v. Refusing to fall back to the primary checkout, which would silently misroute a coordination-kind artifact (INV-3).", Repr(slug), Repr(s.Mid8), err)
			return &RouterResult{Status: StatusError, PlacementRef: placement, Diagnostic: diag,
				Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "error", Refused: fates(root, files, StatusError), Diagnostic: diag}}}
		}
		worktree = s.CoordWorktree
		commitPaths = stageInCoord(root, s, files, opts.CreatedThisInvocation)
	}
	if len(commitPaths) == 0 {
		if useCoord && len(dirtyIn(root, files)) > 0 {
			diag := fmt.Sprintf("Artifact(s) written to the primary checkout routed to the coordination placement (%s) where nothing was staged; the commit would no-op against the wrong surface and the artifact remains uncommitted in the primary tree. Commit it to its own (primary) surface instead.", placement)
			return &RouterResult{Status: StatusNoOpWrongSurface, PlacementRef: placement, Diagnostic: diag,
				Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "refused", Refused: fates(root, files, ReasonWrongSurface), Diagnostic: diag}}}
		}
		return &RouterResult{Status: StatusUnchanged, PlacementRef: placement, Reason: ReasonAlreadyCommitted,
			Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "unchanged", Skipped: fates(root, files, ReasonAlreadyCommitted)}}}
	}
	var absent []string
	for _, p := range commitPaths {
		if !exists(p) {
			absent = append(absent, p)
		}
	}
	if len(absent) > 0 {
		diag := fmt.Sprintf("Artifact(s) not present at resolved placement (%s, worktree=%s); commit would no-op against the wrong surface and was not created.", placement, worktree)
		return &RouterResult{Status: StatusNoOpWrongSurface, PlacementRef: placement, Diagnostic: diag,
			Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "refused", Refused: fates(root, absent, ReasonWrongSurface), Diagnostic: diag}}}
	}
	res, err := Commit(root, worktree, placement, message, commitPaths, Options{})
	if err != nil {
		if IsCode(err, CodeStagedTreeUnchanged) {
			r := &RouterResult{Status: StatusUnchanged, PlacementRef: placement, Reason: ReasonNoChanges,
				Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "unchanged", Skipped: fates(root, commitPaths, ReasonNoChanges)}}}
			if useCoord {
				if dirty := dirtyIn(root, files); len(dirty) > 0 {
					dset := map[string]bool{}
					for _, d := range dirty {
						dset[relpath(root, d)] = true
					}
					var skipped []PathFate
					seen := map[string]bool{}
					var rels []string
					for _, f := range files {
						if rp := relpath(root, f); !seen[rp] {
							seen[rp] = true
							rels = append(rels, rp)
						}
					}
					sort.Strings(rels)
					for _, rp := range rels {
						reason := ReasonAlreadyCommitted
						if dset[rp] {
							reason = ReasonCoordRecordInRoot
						}
						skipped = append(skipped, PathFate{Path: rp, Reason: reason})
					}
					r.Surfaces = []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "unchanged", Skipped: skipped}}
				}
			}
			return r
		}
		diag := err.Error()
		return &RouterResult{Status: StatusError, PlacementRef: placement, Diagnostic: diag,
			Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "error", Refused: fates(root, commitPaths, StatusError), Diagnostic: diag}}}
	}
	var committed []string
	for _, p := range commitPaths {
		committed = append(committed, relpath(root, p))
	}
	return &RouterResult{Status: StatusCommitted, PlacementRef: placement, CommitHash: res.SHA, CommitHashes: [][2]string{{placement, res.SHA}},
		Surfaces: []SurfaceOutcome{{Surface: surface, Branch: placement, Status: "committed", CommitHash: res.SHA, Committed: committed}}}
}

// dirtyIn returns the files present on disk that carry uncommitted content
// in checkout root.
func dirtyIn(root string, files []string) []string {
	var out []string
	for _, f := range files {
		if !exists(f) {
			continue
		}
		rel := relpath(root, f)
		if strings.HasPrefix(rel, "/") {
			continue
		}
		if e, err := StatusEntries(root, "", rel); err == nil && len(e) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// stageInCoord is _stage_artifacts_in_coord_worktree (mission-aware form).
func stageInCoord(root string, s *coord.Surface, files []string, created map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	wt := realpath(s.CoordWorktree)
	for _, src := range files {
		rel := relpath(root, src)
		parts := strings.Split(rel, "/")
		base := parts[len(parts)-1]
		if parts[0] == ".worktrees" {
			if base != "analysis-report.md" && strings.HasPrefix(realpath(src), wt+string(filepath.Separator)) {
				add(src)
			}
			continue
		}
		k := mission.ArtifactKindForPath(rel)
		if k == mission.KindStatusState || k == mission.KindDecisionLog {
			if sub := missionRelative(rel); sub != "" {
				add(filepath.Join(s.CoordDir, filepath.FromSlash(sub)))
			}
			continue
		}
		if base == "analysis-report.md" {
			continue
		}
		dst := filepath.Join(s.CoordWorktree, filepath.FromSlash(rel))
		if exists(src) {
			if data, err := os.ReadFile(src); err == nil {
				os.MkdirAll(filepath.Dir(dst), 0o755)
				st, _ := os.Stat(src)
				os.WriteFile(dst, data, st.Mode().Perm())
				if created[src] {
					if b2, err := os.ReadFile(dst); err == nil && string(b2) == string(data) {
						os.Remove(src)
					}
				}
			}
		}
		add(dst)
	}
	return out
}

func missionRelative(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "kitty-specs" {
			if i+2 < len(parts) {
				return strings.Join(parts[i+2:], "/")
			}
			return ""
		}
	}
	return ""
}
