package setup

// mainCheckoutRunFailed is upgrade._main_checkout_run_failed: any reason
// other than a worktree failure (or held drift) fails the main checkout's run.
func (o *upgradeOutcome) mainCheckoutRunFailed() bool {
	for _, r := range o.reasons() {
		if r != "worktree_failure" && r != "surface_drift" {
			return true
		}
	}
	return false
}

// noCommitReason is upgrade._no_commit_reason (FR-023).
func noCommitReason(root string, o *upgradeOutcome, b *gitBaseline, dryRun, shouldCommit bool) string {
	if dryRun || o.committed {
		return ""
	}
	churn := b != nil && len(upgradeCommitCandidates(root, b)) > 0
	switch {
	case !o.success:
		if churn {
			return failedRunLeftUncommitted
		}
		return ""
	case !shouldCommit:
		return ""
	case b == nil:
		return baselineUnavailable
	case len(o.activationErrors) > 0:
		return repairErrorsLeftUncommitted
	case o.mainCheckoutRunFailed():
		if churn {
			return failedRunLeftUncommitted
		}
		return ""
	case b.paths[".kittify/metadata.yaml"] && !churn:
		return metadataDirtyAtBaselineWarn
	}
	return ""
}
