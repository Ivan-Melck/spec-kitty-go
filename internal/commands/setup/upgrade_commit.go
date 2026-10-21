package setup

// noCommitReason is upgrade._no_commit_reason (FR-023).
func noCommitReason(root string, o *upgradeOutcome, b *gitBaseline, dryRun, shouldCommit bool) string {
	if dryRun || o.committed {
		return ""
	}
	churn := b != nil && len(upgradeCommitCandidates(root, b)) > 0
	const failed = "The upgrade failed, so its changes were left uncommitted — review them, then re-run `spec-kitty upgrade` or commit them yourself."
	switch {
	case !o.success:
		if churn {
			return failed
		}
		return ""
	case !shouldCommit:
		return ""
	case b == nil:
		return "Could not read the git status before the upgrade; nothing was committed — review the changes and commit them yourself."
	case len(o.activationErrors) > 0:
		return "Activation or repair preparation reported errors, so the upgrade changes were left uncommitted — review them and commit them yourself."
	case len(o.surfaceRepairFails) > 0:
		if churn {
			return failed
		}
		return ""
	case b.paths[".kittify/metadata.yaml"] && !churn:
		return ".kittify/metadata.yaml had uncommitted changes before the upgrade; its new schema stamp was left uncommitted — commit it yourself."
	}
	return ""
}
