// Package writecheckout holds the single_branch shared write-checkout
// guards every claim verb shares (implement, agent action implement,
// orchestrator-api start-implementation): the checkout claim lock
// (status.locking.write_checkout_claim_lock), the refusal order of
// implement_support._ensure_repo_root_checkout_available (wrong branch →
// occupied → dirty) and the claim base record_claim_base writes.
package writecheckout

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/branchctx"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// ClaimLockTimeout is status.locking.CHECKOUT_CLAIM_LOCK_TIMEOUT_SECONDS.
var ClaimLockTimeout = 120.0

// pyBranchRepr is repr(get_current_branch(...)): None for a detached HEAD.
func pyBranchRepr(b string) string {
	if b == "" {
		return "None"
	}
	return mission.PyRepr(b)
}

// IsSingleBranchMission is implement_support._is_single_branch_mission (the
// stored topology).
func IsSingleBranchMission(root, slug string) bool {
	return coord.Resolve(root, slug).Topology == coord.SingleBranch
}

// IsSingleBranchRepoRootLane is
// checkout_occupancy.is_single_branch_repo_root_lane for a resolved
// workspace: the repository-root lane of a single_branch Mission.
func IsSingleBranchRepoRootLane(root, slug string, ws *lanes.Workspace) bool {
	return ws != nil && ws.LaneID == lanes.PlanningLaneID && IsSingleBranchMission(root, slug)
}

// LockPath is _checkout_claim_lock_path: keyed on the resolved checkout
// root under <git common dir>/spec-kitty-locks/.
func LockPath(checkout string) (string, error) {
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		resolved = checkout
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	sum := sha1.Sum([]byte(resolved))
	common, err := gitx.CommonDir(checkout)
	if err != nil {
		return "", err
	}
	key := "__checkout-" + hex.EncodeToString(sum[:])[:16] + "__"
	return filepath.Join(common, "spec-kitty-locks", key+".status.lock"), nil
}

// describeHolder is _describe_holder(_read_holder(lock_path)).
func describeHolder(lockPath string) string {
	b, err := os.ReadFile(lockPath + ".holder")
	if err != nil {
		return "holder unknown (no holder record)"
	}
	var h map[string]any
	if json.Unmarshal(b, &h) != nil || h == nil {
		return "holder unknown (no holder record)"
	}
	field := func(k string) string {
		v, ok := h[k]
		if !ok {
			return "?"
		}
		switch t := v.(type) {
		case string:
			return t
		case float64:
			if t == float64(int64(t)) {
				return fmt.Sprintf("%d", int64(t))
			}
			return jsonx.FormatFloat(t)
		}
		return fmt.Sprint(v)
	}
	return fmt.Sprintf("held by pid %s (thread %s) since %s", field("pid"), field("thread"), field("acquired_at"))
}

// LockTimeoutError is FeatureStatusLockTimeoutError (error_code
// STATUS_LOCK_HELD).
type LockTimeoutError struct{ Msg string }

func (e *LockTimeoutError) Error() string { return e.Msg }

// ErrorCode is FeatureStatusLockTimeoutError.error_code.
func (e *LockTimeoutError) ErrorCode() string { return "STATUS_LOCK_HELD" }

// AcquireClaimLock is write_checkout_claim_lock(checkout): an exclusive
// flock polled up to timeout seconds, with the holder sidecar recorded
// while held. The returned release is nil on error.
func AcquireClaimLock(checkout string, timeout float64) (func(), error) {
	lockPath, err := LockPath(checkout)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		ok, err := tryFlock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			break
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, &LockTimeoutError{fmt.Sprintf("Timed out acquiring status lock %s after %ss: %s",
				lockPath, jsonx.FormatFloat(timeout), describeHolder(lockPath))}
		}
		time.Sleep(50 * time.Millisecond)
	}
	holder := lockPath + ".holder"
	payload := jsonx.NewObject("acquired_at", clock.NowUTCISO(), "pid", os.Getpid(), "thread", "MainThread")
	_ = os.WriteFile(holder, []byte(jsonx.Dumps(payload, jsonx.Options{Indent: -1, SortKeys: true})), 0o644)
	return func() {
		_ = os.Remove(holder)
		unflock(f)
		f.Close()
	}, nil
}

// OwnedStatusPrefixes is implement_support._owned_status_prefixes.
func OwnedStatusPrefixes(slug string) []string {
	return []string{
		"kitty-specs/" + slug + "/status.events.jsonl",
		"kitty-specs/" + slug + "/status.json",
		"kitty-specs/" + slug + "/meta.json",
		"kitty-specs/" + slug + "/mission-events.jsonl",
		".kittify/", ".spec-kitty/", "kitty-ops/",
	}
}

// isOwnedPath is checkout_occupancy._is_owned_path.
func isOwnedPath(p string, prefixes []string) bool {
	p = strings.TrimSuffix(p, "/")
	for _, pre := range prefixes {
		if strings.HasSuffix(pre, "/") {
			d := strings.TrimSuffix(pre, "/")
			if p == d || strings.HasPrefix(p, d+"/") {
				return true
			}
		} else if p == pre {
			return true
		}
	}
	return false
}

// GitStatusError is the GitCommandError a failing `git status` raises in
// the dirty scan.
type GitStatusError struct {
	Checkout string
	Code     int
	Stderr   string
}

func (e *GitStatusError) Error() string {
	return fmt.Sprintf("git status failed in %s: %s", e.Checkout, strings.TrimSpace(e.Stderr))
}

// Exception is the uncaught chain upstream prints for the failure: the
// RuntimeError raised by _git_status_entries (with outer, the caller's
// frames down to the dirty scan) from the kernel GitCommandError.
func (e *GitStatusError) Exception(outer ...rich.Frame) *rich.Exception {
	detail := "no error output"
	if s := strings.TrimSpace(e.Stderr); s != "" {
		detail = strings.SplitN(s, "\n", 2)[0]
	}
	cause := &rich.Exception{Type: "GitCommandError",
		Value: fmt.Sprintf("git status --porcelain=v1 -z -- failed in %s (exit %d): %s", e.Checkout, e.Code, detail),
		Frames: []rich.Frame{{Name: "_git_status_entries", Line: 1850}, {Name: "status_entries", Line: 301},
			{Name: "_git", Line: 267}, {Name: "run_git", Line: 103}}}
	frames := append(append([]rich.Frame{}, outer...), rich.Frame{Name: "_ensure_repo_root_checkout_available", Line: 261},
		rich.Frame{Name: "dirty_paths", Line: 347}, rich.Frame{Name: "_git_status_entries", Line: 1852})
	return &rich.Exception{Type: "RuntimeError", Value: e.Error(), Frames: frames, Cause: cause}
}

// DirtyPaths is checkout_occupancy.dirty_paths over `git status -z`
// (default untracked mode: an untracked directory is one collapsed entry).
func DirtyPaths(checkout string, prefixes []string) ([]string, error) {
	r, err := gitx.Exec(checkout, nil, nil, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, &GitStatusError{Checkout: checkout, Code: 127, Stderr: err.Error()}
	}
	if r.Code != 0 {
		return nil, &GitStatusError{Checkout: checkout, Code: r.Code, Stderr: r.Stderr}
	}
	fields := strings.Split(r.Stdout, "\x00")
	var paths []string
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 4 {
			continue
		}
		xy, p := e[:2], e[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the rename source follows; report the new path
		}
		if p == "" || isOwnedPath(p, prefixes) {
			continue
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// writesToAnotherBranch is checkout_occupancy._writes_to_another_branch.
func writesToAnotherBranch(meta *jsonx.Object, topology, lanesTarget, current string) bool {
	if current == "" {
		return false
	}
	metaTarget, ok := meta.Get("target_branch")
	mt, isStr := metaTarget.(string)
	if !ok || !isStr || mt == "" || lanesTarget == "" {
		return false
	}
	ref := func(target string) string {
		if topology == coord.SingleBranch {
			if mb, ok := meta.Get("mission_branch"); ok {
				if s, ok := mb.(string); ok && strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
		return target
	}
	return ref(mt) == ref(lanesTarget) && ref(mt) != current
}

func reduceStatus(statusDir string) *status.Snapshot {
	snap, err := status.ReduceDir(statusDir)
	if err != nil || snap == nil {
		return &status.Snapshot{WorkPackages: jsonx.NewObject(), Summary: jsonx.NewObject()}
	}
	return snap
}

func wpLane(snap *status.Snapshot, id string) string {
	if st := snap.WP(id); st != nil {
		if v, ok := st.Get("lane"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return string(status.Planned)
}

// InProgressWPsInWriteCheckout is
// checkout_occupancy.in_progress_wps_in_write_checkout: (mission, wp) pairs
// in_progress in a single_branch repo-root lane of the checkout, except
// (exSlug, exWP).
func InProgressWPsInWriteCheckout(root, checkout, exSlug, exWP string) [][2]string {
	rc, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		rc = checkout
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		rr = root
	}
	if rc != rr {
		return nil
	}
	current := gitx.CurrentBranch(rc)
	var out [][2]string
	for _, m := range mission.Index(root) {
		meta, err := mission.ReadMeta(m.Dir)
		if err != nil || meta == nil {
			continue
		}
		topology := coord.TopologyFromMeta(meta, m.Dir)
		if topology != coord.SingleBranch {
			continue
		}
		manifest, err := lanes.Read(m.Dir)
		if err != nil || manifest == nil {
			continue
		}
		ids := map[string]bool{}
		for _, l := range manifest.Lanes {
			if l.LaneID == lanes.PlanningLaneID {
				for _, id := range l.WPIDs {
					ids[id] = true
				}
			}
		}
		if len(ids) == 0 || migrate.IsMissionCompleted(m.Dir) {
			continue
		}
		if writesToAnotherBranch(meta, topology, manifest.TargetBranch, current) {
			continue
		}
		snap := reduceStatus(project.Specs(root, m.Slug))
		for _, id := range snap.WorkPackages.Keys() {
			if !ids[id] || (m.Slug == exSlug && id == exWP) {
				continue
			}
			if wpLane(snap, id) == string(status.InProgress) {
				out = append(out, [2]string{m.Slug, id})
			}
		}
	}
	return out
}

// RefusalError is one of WriteCheckoutWrongBranchError /
// WriteCheckoutOccupiedError / WriteCheckoutDirtyError.
type RefusalError struct{ Code, Msg string }

func (e *RefusalError) Error() string { return e.Msg }

// EnsureAvailable is _ensure_repo_root_checkout_available: on a
// single_branch Mission, the write-checkout refusals in order wrong branch
// → occupied → dirty (a *RefusalError), or the dirty scan's
// *GitStatusError. A no-op for every other topology. handle names the
// Mission in the messages (the caller's mission_slug argument).
func EnsureAvailable(root, handle, slug, wpID string, ws *lanes.Workspace) error {
	if !IsSingleBranchMission(root, slug) {
		return nil
	}
	checkout := ws.WorktreePath
	expected := ws.BranchName
	if expected == "" {
		expected = lanes.SingleBranchWriteRef(root, slug, branchctx.FeatureTargetBranch(project.Specs(root, slug), root))
	}
	current := gitx.CurrentBranch(checkout)
	if current != expected {
		return &RefusalError{"WRITE_CHECKOUT_WRONG_BRANCH", fmt.Sprintf(
			"The write checkout at %s is on branch %s, but %s %s expects %s. Check out %s in %s before retrying.",
			checkout, pyBranchRepr(current), handle, wpID, mission.PyRepr(expected), mission.PyRepr(expected), checkout)}
	}
	if occ := InProgressWPsInWriteCheckout(root, checkout, handle, wpID); len(occ) > 0 {
		om, ow := occ[0][0], occ[0][1]
		return &RefusalError{"WRITE_CHECKOUT_OCCUPIED", fmt.Sprintf(
			"%s %s is already in_progress in the shared write checkout at %s on branch %s. "+
				"Move %s out of in_progress (approve, reject, or block it) before claiming %s %s. "+
				"If %s is finished or abandoned, run: spec-kitty agent tasks move-task %s --to blocked --mission %s --note \"<reason>\" "+
				"(use --to canceled instead if the work is abandoned)",
			om, ow, checkout, mission.PyRepr(expected), ow, handle, wpID, om, ow, om)}
	}
	statusDir := project.Specs(root, slug)
	_, logErr := os.Stat(filepath.Join(statusDir, status.EventsFile))
	resume := logErr == nil && wpLane(reduceStatus(statusDir), wpID) == string(status.InProgress)
	if !resume {
		dirty, err := DirtyPaths(checkout, OwnedStatusPrefixes(handle))
		if err != nil {
			return err
		}
		if len(dirty) > 0 {
			return &RefusalError{"WRITE_CHECKOUT_DIRTY", fmt.Sprintf(
				"The write checkout at %s has uncommitted changes: %s. Commit or stash them before claiming %s %s.",
				checkout, strings.Join(dirty, ", "), handle, wpID)}
		}
	}
	return nil
}

// RecordClaimBase is lanes.claim_base.record_claim_base: the write
// checkout's HEAD as refs/spec-kitty/wp-base/<mission>/<wp>, idempotent by
// absence.
func RecordClaimBase(root, checkout, slug, wpID string) error {
	if lanes.ReadClaimBase(root, slug, wpID) != "" {
		return nil
	}
	head, err := gitx.Run(checkout, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil || strings.TrimSpace(head) == "" {
		return fmt.Errorf("cannot record claim base for %s/%s: HEAD does not resolve in %s", slug, wpID, checkout)
	}
	if _, err := gitx.Run(root, "update-ref", lanes.ClaimBaseRefPrefix+"/"+slug+"/"+wpID, strings.TrimSpace(head)); err != nil {
		return fmt.Errorf("cannot record claim base for %s/%s: git update-ref failed in %s", slug, wpID, root)
	}
	return nil
}

// GuardRepoRootClaim is implement_support.guard_repo_root_claim: the
// refusals, then the claim base.
func GuardRepoRootClaim(root, handle, slug, wpID string, ws *lanes.Workspace) error {
	if err := EnsureAvailable(root, handle, slug, wpID, ws); err != nil {
		return err
	}
	return RecordClaimBase(root, root, slug, wpID)
}
