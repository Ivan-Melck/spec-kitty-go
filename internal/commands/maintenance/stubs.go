package maintenance

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// stub registers a not-yet-ported leaf so the command tree and help listing
// stay complete.
func stub(name, short string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Short: short,
		Long:  longHelp["migrate_"+name],
		Run: func(ctx *cli.Context) error {
			fmt.Fprintf(ctx.Stderr, "Error: `migrate %s` is not supported by this build.\n", name)
			return cli.Exit(1)
		},
	}
}

func newBackfillMergeCommitCmd() *cli.Command {
	return stub("backfill-merge-commit", "Record a GitHub PR's real merge commit as a mission's review baseline (#4231).")
}
func newCharterEncodingCmd() *cli.Command {
	return stub("charter-encoding", "Scan charter content for non-UTF-8 encodings; normalize-or-fail-loud.")
}
func newBackfillProvenanceCmd() *cli.Command {
	return stub("backfill-provenance", "FR-014: backfill provenance onto legacy acceptance-matrix.json invariants.")
}
func newNormalizeLifecycleCmd() *cli.Command {
	return stub("normalize-lifecycle", "Normalize legacy ``kitty-specs`` missions for the MVP lifecycle model.")
}
func newRewriteOpposedByCmd() *cli.Command {
	return stub("rewrite-opposed-by", "Rewrite a pack's legacy ``opposed_by`` entries into DRG edges.")
}
func newBackfillRuntimeStateCmd() *cli.Command {
	return stub("backfill-runtime-state", "Seed legacy runtime state as events, verify fail-closed, and flip status_phase.")
}
func newBackfillWPStatusCmd() *cli.Command {
	return stub("backfill-wp-status", "Seed the lane events a Mission's WP files lack, so the status snapshot counts every WP (#5579).")
}
func newRebaselineDossierHashesCmd() *cli.Command {
	return stub("rebaseline-dossier-hashes", "One-time re-baseline of recorded dossier snapshot hashes (FR-009, WP05).")
}
func newRepinHooksCmd() *cli.Command {
	return stub("repin-hooks", "Re-pin this repository's pre-commit hook to the current interpreter (#254).")
}
