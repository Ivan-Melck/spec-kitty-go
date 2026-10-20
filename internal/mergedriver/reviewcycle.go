package mergedriver

import (
	"path/filepath"
	"strings"
)

// Conflict markers used by the review-cycle driver.
const (
	ConflictMarkerOurs   = "<<<<<<< ours"
	ConflictMarkerSep    = "======="
	ConflictMarkerTheirs = ">>>>>>> theirs"
)

// ReconcileReviewCycleTexts returns the resolved document and whether the
// two sides collided (identical texts resolve to ours unchanged).
func ReconcileReviewCycleTexts(ours, theirs string) (string, bool) {
	if ours == theirs {
		return ours, false
	}
	return strings.Join([]string{ConflictMarkerOurs, ours, ConflictMarkerSep, theirs, ConflictMarkerTheirs}, "\n"), true
}

// RunReviewCycle is run_review_cycle_driver (merge-driver-review-cycle):
// best-effort, never aborting; a divergent collision embeds both documents
// behind conflict markers and returns a notice.
func RunReviewCycle(basePath, oursPath, theirsPath string) (Outcome, error) {
	_, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	var texts [2]string
	for i, p := range []string{ours, theirs} {
		if exists(p) {
			if texts[i], err = readTextCrash(p); err != nil {
				return Outcome{}, err
			}
		}
	}
	doc, collided := ReconcileReviewCycleTexts(texts[0], texts[1])
	if err := writeText(ours, doc); err != nil {
		return Outcome{}, err
	}
	if !collided {
		return Outcome{}, nil
	}
	return Outcome{Notice: "review-cycle verdict collision at " + filepath.Base(ours) + ": two distinct " +
		"best-effort renders collided under one filename (T017 create-window " +
		"hazard); both embedded verbatim behind conflict markers -- " +
		"non-aborting (FR-014/D-PLAN-6: the .md is non-authoritative, unread " +
		"prose; the squash proceeds)"}, nil
}
