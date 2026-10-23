package orchestrator

import (
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/writecheckout"
)

// enterCheckoutClaimLock is _enter_checkout_claim_lock: only a
// single_branch repo-root lane WP takes the lock; the returned release is
// never nil.
func (v *verb) enterCheckoutClaimLock(m *orch.Mission, handle, wpID string) (func(), error) {
	noop := func() {}
	_, l, legacy := laneAssignment(m, handle, wpID)
	if legacy != nil || l == nil || l.LaneID != lanes.PlanningLaneID || !writecheckout.IsSingleBranchMission(m.Root, m.Slug) {
		return noop, nil
	}
	release, err := writecheckout.AcquireClaimLock(m.Root, writecheckout.ClaimLockTimeout)
	if err != nil {
		if _, ok := err.(*writecheckout.LockTimeoutError); ok {
			return noop, v.fail("STATUS_LOCK_HELD", err.Error(),
				identity(m.StatusDir, "wp_id", wpID, "lock_timeout_seconds", jsonx.Float(writecheckout.ClaimLockTimeout)))
		}
		return noop, nil
	}
	return release, nil
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
	err = writecheckout.EnsureAvailable(m.Root, handle, m.Slug, wpID, resolved)
	switch e := err.(type) {
	case nil:
		return nil
	case *writecheckout.RefusalError:
		return v.fail(e.Code, e.Msg, identity(m.StatusDir, "wp_id", wpID, "error_code", e.Code, "message", e.Msg))
	default:
		return v.fail("LANE_ALLOCATION_FAILED", err.Error(), identity(m.StatusDir, "wp_id", wpID, "reason", err.Error()))
	}
}
