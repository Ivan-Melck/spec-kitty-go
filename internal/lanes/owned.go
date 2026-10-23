package lanes

import "path/filepath"

// OwnedCheckoutKind is workspace.context._OWNED_CHECKOUT_KIND.
const OwnedCheckoutKind = "owned_checkout"

// OwnedWorkspace is _owned_checkout_workspace: an owned single_branch
// mission's WP resolves to the owned checkout itself (no lane, the fact's
// write branch).
func OwnedWorkspace(missionSlug, wpID, executionMode, ownedRoot, writeBranch string) *Workspace {
	if executionMode == "" {
		executionMode = "code_change"
	}
	return &Workspace{MissionSlug: missionSlug, WPID: wpID, ExecutionMode: executionMode, ResolutionKind: OwnedCheckoutKind,
		WorkspaceName: filepath.Base(ownedRoot), WorktreePath: ownedRoot, BranchName: writeBranch, LaneWPIDs: []string{}}
}
