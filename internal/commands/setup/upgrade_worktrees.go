package setup

// Worktree fan-out of the migration runner (upgrade/runner.py
// _upgrade_worktrees): every non-integrating checkout under .worktrees/ gets
// the worktree-capable migrations, its own metadata bookkeeping, schema
// stamp and (with auto_commit) its own upgrade commit.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

type worktreeRun struct {
	warnings, errors, failures []string
}

const (
	failedRunLeftUncommitted = "The upgrade failed, so its changes were left uncommitted — review them, then re-run `spec-kitty upgrade` or commit them yourself."
	baselineUnavailable      = "Could not read the git status before the upgrade; nothing was committed — review the changes and commit them yourself."
)

// isIntegratingWorktree is runner._is_integrating_worktree.
func isIntegratingWorktree(wt string) bool {
	if !lexists(filepath.Join(wt, ".git")) {
		return false
	}
	r, err := gitx.Exec(wt, nil, nil, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || r.Code != 0 {
		return true
	}
	b := strings.TrimSpace(r.Stdout)
	if b == "" || b == "HEAD" {
		return true
	}
	return strings.HasPrefix(b, "kitty/mission-") && len(b) > len("kitty/mission-")
}

func worktreesToUpgrade(root string) []string {
	var out []string
	for _, wt := range subdirs(filepath.Join(root, ".worktrees")) {
		if !isIntegratingWorktree(wt) {
			out = append(out, wt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return filepath.Base(out[i]) < filepath.Base(out[j]) })
	return out
}

func upgradeWorktrees(root, target string, migrations []migrationInfo, dryRun, autoCommit bool) worktreeRun {
	var res worktreeRun
	var wtMigs []migrationInfo
	for _, m := range migrations {
		if m.runsOnWorktrees() {
			wtMigs = append(wtMigs, m)
		}
	}
	if len(migrations) > 0 && len(wtMigs) == 0 {
		return res
	}
	if !pathExists(filepath.Join(root, ".worktrees")) {
		return res
	}
	var mainMD *kittify.Metadata
	if !dryRun {
		mainMD = kittify.LoadMetadata(root)
	}
	fallback := clock.NowUTCISO()
	for _, wt := range worktreesToUpgrade(root) {
		name := filepath.Base(wt)
		prefix := "Worktree " + name + ": "
		upgradeable := pathExists(filepath.Join(wt, ".kittify")) ||
			(len(wtMigs) > 0 && (pathExists(filepath.Join(wt, "kitty-specs")) || pathExists(filepath.Join(wt, ".specify"))))
		if !upgradeable {
			continue
		}
		var baseline *gitBaseline
		if autoCommit && !dryRun {
			baseline = captureGitBaseline(wt)
		}
		md := kittify.LoadMetadata(wt)
		synthesized := md == nil
		if md == nil {
			md = kittify.NewMetadata(detectProjectVersion(wt))
		}
		wtFrom := md.Version
		dirty := synthesized
		held := map[string]bool{}
		errored, failed := false, false
		record := func(id, result string, notes *string) bool {
			if dryRun {
				return false
			}
			if md.RecordMigration(id, result, notes) {
				md.Save(wt)
				return true
			}
			return false
		}
		for _, m := range wtMigs {
			if md.HasMigration(m.ID) && !m.reselectWhenRecorded(wt) {
				continue
			}
			needed, derr := m.detectErr(wt)
			if derr != nil {
				errored = true
				res.errors = append(res.errors, fmt.Sprintf("%sCannot safely detect %s: %s", prefix, m.ID, derr))
				n := "Cannot safely detect: " + derr.Error()
				if record(m.ID, "failed", &n) {
					dirty = true
				}
				continue
			}
			if !needed {
				n := "Not applicable"
				if record(m.ID, "skipped", &n) {
					dirty = true
				}
				continue
			}
			if okApply, reason := m.canApply(wt); !okApply {
				res.warnings = append(res.warnings, fmt.Sprintf("%sCannot apply %s: %s", prefix, m.ID, reason))
				continue
			}
			r, aerr := m.applyErr(wt, dryRun)
			if aerr != nil {
				errored = true
				res.errors = append(res.errors, fmt.Sprintf("%sCannot apply %s: %s", prefix, m.ID, aerr))
				continue
			}
			if r.ManualReview {
				for _, p := range r.Preserved {
					held[p] = true
				}
			}
			if r.Success {
				var notes *string
				if len(r.Changes) > 0 {
					n := strings.Join(r.Changes, "; ")
					notes = &n
				}
				if record(m.ID, "success", notes) {
					dirty = true
				}
				for _, w := range r.Warnings {
					res.warnings = append(res.warnings, prefix+w)
				}
			} else {
				if !dryRun {
					var notes *string
					if len(r.Errors) > 0 {
						n := strings.Join(r.Errors, "; ")
						notes = &n
					}
					if md.RecordMigration(m.ID, "failed", notes) {
						md.Save(wt)
					}
				}
				failed = true
				for _, e := range r.Errors {
					res.errors = append(res.errors, prefix+e)
					res.failures = append(res.failures, prefix+e)
				}
			}
		}
		if dryRun {
			continue
		}
		bookkeepingOnly := !dirty && md.Version != target
		if md.Version != target {
			md.Version = target
			dirty = true
		}
		if dirty {
			if bookkeepingOnly {
				md.LastUpgradedAt = fallback
				if mainMD != nil && mainMD.LastUpgradedAt != "" {
					md.LastUpgradedAt = mainMD.LastUpgradedAt
				}
			} else {
				md.LastUpgradedAt = clock.NowUTCISO()
			}
			md.Save(wt)
		}
		if !failed {
			kittify.StampSchemaVersion(wt)
		}
		if autoCommit {
			res.warnings = append(res.warnings, commitWorktreeChurn(wt, baseline, wtFrom, target, failed || errored, sortedKeys(held))...)
		}
	}
	return res
}

// commitWorktreeChurn is runner._commit_worktree_churn.
func commitWorktreeChurn(wt string, baseline *gitBaseline, from, to string, failed bool, held []string) []string {
	prefix := "Worktree " + filepath.Base(wt) + ": "
	committed := false
	reason := ""
	switch {
	case baseline == nil:
		reason = baselineUnavailable
	case failed:
		if len(upgradeCommitCandidates(wt, baseline)) > 0 {
			reason = failedRunLeftUncommitted
		}
	default:
		var paths []string
		committed, paths, reason = commitUpgradeChurn(wt, baseline, from, to)
		_ = paths
	}
	var warnings []string
	if reason != "" {
		warnings = append(warnings, prefix+reason)
	}
	if len(held) > 0 && !failed {
		joined := strings.Join(held, ", ")
		if committed {
			warnings = append(warnings, prefix+"Held for manual review (not committed): "+joined)
		} else {
			warnings = append(warnings, fmt.Sprintf("%sSkipped auto-commit of %d file(s) held for manual review: %s", prefix, len(held), joined))
		}
	}
	return warnings
}

var _ = os.Stat
