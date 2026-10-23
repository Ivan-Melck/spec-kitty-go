package orchestrator

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
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// checkoutClaimLockTimeout is status.locking.CHECKOUT_CLAIM_LOCK_TIMEOUT_SECONDS.
var checkoutClaimLockTimeout = 120.0

// pyBranchRepr is repr(get_current_branch(...)): None for a detached HEAD.
func pyBranchRepr(b string) string {
	if b == "" {
		return "None"
	}
	return mission.PyRepr(b)
}

// isSingleBranchMission is implement_support._is_single_branch_mission (the
// stored topology).
func isSingleBranchMission(root, slug string) bool {
	return coord.Resolve(root, slug).Topology == coord.SingleBranch
}

// checkoutClaimLockPath is _checkout_claim_lock_path: keyed on the resolved
// checkout root under <git common dir>/spec-kitty-locks/.
func checkoutClaimLockPath(checkout string) (string, error) {
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

// describeLockHolder is _describe_holder(_read_holder(lock_path)).
func describeLockHolder(lockPath string) string {
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

// lockTimeoutError is FeatureStatusLockTimeoutError.
type lockTimeoutError struct{ msg string }

func (e *lockTimeoutError) Error() string { return e.msg }

// acquireCheckoutClaimLock is write_checkout_claim_lock(checkout): an
// exclusive flock polled up to the timeout, with the holder sidecar
// recorded while held.
func acquireCheckoutClaimLock(checkout string, timeout float64) (func(), error) {
	lockPath, err := checkoutClaimLockPath(checkout)
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
			return nil, &lockTimeoutError{fmt.Sprintf("Timed out acquiring status lock %s after %ss: %s",
				lockPath, jsonx.FormatFloat(timeout), describeLockHolder(lockPath))}
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

// enterCheckoutClaimLock is _enter_checkout_claim_lock: only a
// single_branch repo-root lane WP takes the lock; the returned release is
// never nil.
func (v *verb) enterCheckoutClaimLock(m *orch.Mission, handle, wpID string) (func(), error) {
	noop := func() {}
	_, l, legacy := laneAssignment(m, handle, wpID)
	if legacy != nil || l == nil || l.LaneID != lanes.PlanningLaneID || !isSingleBranchMission(m.Root, m.Slug) {
		return noop, nil
	}
	release, err := acquireCheckoutClaimLock(m.Root, checkoutClaimLockTimeout)
	if err != nil {
		if _, ok := err.(*lockTimeoutError); ok {
			return noop, v.fail("STATUS_LOCK_HELD", err.Error(),
				identity(m.StatusDir, "wp_id", wpID, "lock_timeout_seconds", jsonx.Float(checkoutClaimLockTimeout)))
		}
		return noop, nil
	}
	return release, nil
}

// ownedStatusPrefixes is implement_support._owned_status_prefixes.
func ownedStatusPrefixes(slug string) []string {
	return []string{
		"kitty-specs/" + slug + "/status.events.jsonl",
		"kitty-specs/" + slug + "/status.json",
		"kitty-specs/" + slug + "/meta.json",
		"kitty-specs/" + slug + "/mission-events.jsonl",
		".kittify/", ".spec-kitty/", "kitty-ops/",
	}
}

// isOwnedStatusPath is checkout_occupancy._is_owned_path.
func isOwnedStatusPath(p string, prefixes []string) bool {
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

// dirtyPaths is checkout_occupancy.dirty_paths over `git status -z`
// (default untracked mode: an untracked directory is one collapsed entry).
func dirtyPaths(checkout string, prefixes []string) ([]string, error) {
	out, err := gitx.Run(checkout, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, fmt.Errorf("git status failed in %s: %s", checkout, strings.TrimSpace(err.Error()))
	}
	fields := strings.Split(out, "\x00")
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
		if p == "" || isOwnedStatusPath(p, prefixes) {
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

// inProgressWPsInWriteCheckout is
// checkout_occupancy.in_progress_wps_in_write_checkout.
func inProgressWPsInWriteCheckout(root, checkout, exSlug, exWP string) [][2]string {
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

// ensureRepoRootCheckoutOrFail is _ensure_repo_root_checkout_or_fail
// (implement_support._ensure_repo_root_checkout_available): the
// single_branch write-checkout refusals, in order wrong branch → occupied →
// dirty.
func (v *verb) ensureRepoRootCheckoutOrFail(m *orch.Mission, handle, wpID string) error {
	resolved, err := lanes.ResolveWorkspace(m.Root, m.Slug, wpID)
	if err != nil {
		return v.fail("LANE_ALLOCATION_FAILED", err.Error(), identity(m.StatusDir, "wp_id", wpID, "reason", err.Error()))
	}
	if !isSingleBranchMission(m.Root, m.Slug) {
		return nil
	}
	refuse := func(code, msg string) error {
		return v.fail(code, msg, identity(m.StatusDir, "wp_id", wpID, "error_code", code, "message", msg))
	}
	checkout := resolved.WorktreePath
	expected := resolved.BranchName
	if expected == "" {
		expected = lanes.SingleBranchWriteRef(m.Root, m.Slug, branchctx.FeatureTargetBranch(project.Specs(m.Root, m.Slug), m.Root))
	}
	current := gitx.CurrentBranch(checkout)
	if current != expected {
		return refuse("WRITE_CHECKOUT_WRONG_BRANCH", fmt.Sprintf(
			"The write checkout at %s is on branch %s, but %s %s expects %s. Check out %s in %s before retrying.",
			checkout, pyBranchRepr(current), handle, wpID, mission.PyRepr(expected), mission.PyRepr(expected), checkout))
	}
	if occ := inProgressWPsInWriteCheckout(m.Root, checkout, handle, wpID); len(occ) > 0 {
		om, ow := occ[0][0], occ[0][1]
		return refuse("WRITE_CHECKOUT_OCCUPIED", fmt.Sprintf(
			"%s %s is already in_progress in the shared write checkout at %s on branch %s. "+
				"Move %s out of in_progress (approve, reject, or block it) before claiming %s %s. "+
				"If %s is finished or abandoned, run: spec-kitty agent tasks move-task %s --to blocked --mission %s --note \"<reason>\" "+
				"(use --to canceled instead if the work is abandoned)",
			om, ow, checkout, mission.PyRepr(expected), ow, handle, wpID, om, ow, om))
	}
	statusDir := project.Specs(m.Root, m.Slug)
	_, logErr := os.Stat(filepath.Join(statusDir, "status.events.jsonl"))
	resume := logErr == nil && wpLane(reduceStatus(statusDir), wpID) == string(status.InProgress)
	if !resume {
		dirty, err := dirtyPaths(checkout, ownedStatusPrefixes(handle))
		if err != nil {
			return v.fail("LANE_ALLOCATION_FAILED", err.Error(), identity(m.StatusDir, "wp_id", wpID, "reason", err.Error()))
		}
		if len(dirty) > 0 {
			return refuse("WRITE_CHECKOUT_DIRTY", fmt.Sprintf(
				"The write checkout at %s has uncommitted changes: %s. Commit or stash them before claiming %s %s.",
				checkout, strings.Join(dirty, ", "), handle, wpID))
		}
	}
	return nil
}
