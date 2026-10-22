package consolidate

import (
	"strings"
	"testing"
)

func TestRenderBoundRefusals(t *testing.T) {
	got := RenderBoundRefusals([]BoundRefusal{
		{Code: LaneMovedAfterApproval, LaneID: "lane-a", Branch: "kitty/mission-x-lane-a", WPIDs: []string{"WP01"},
			Commits: []string{"1111111aaaa", "2222222bbbb", "3333333cccc", "4444444dddd"}, Path: "src/a.py"},
		{Code: ApprovalStampMissing, LaneID: "lane-b", Branch: "kitty/mission-x-lane-b", WPIDs: []string{"WP02"}},
	}, "x")
	want := strings.Join([]string{
		"LANE_MOVED_AFTER_APPROVAL: branch 'kitty/mission-x-lane-a' (lane-a carries WP01) holds content committed after its approval stamp (the lane commit recorded when review approved it): 1111111, 2222222, 3333333 and 1 more (e.g. 'src/a.py'); see one with `git show 1111111`.",
		"Recovery: send WP01 back for review so the new content is reviewed:",
		"  spec-kitty agent tasks move-task WP01 --to in_progress --mission x",
		"Then approve it again and re-run spec-kitty consolidate.",
		"APPROVAL_STAMP_MISSING: WP02 (lane-b) has no approval stamp, so the commit review approved cannot be determined.",
		"Recovery, either attest the approval after checking by hand that the lane holds only reviewed work:",
		`  spec-kitty consolidate --mission x --attest-approved-reviewed WP02 --attest-reason "<what you checked>"`,
		"or send WP02 back for review:",
		"  spec-kitty agent tasks move-task WP02 --to in_progress --mission x",
		"and, after approving it again, re-run spec-kitty consolidate.",
	}, "\n")
	if got != want {
		t.Errorf("render:\n%s\nwant:\n%s", got, want)
	}
	if codes := BoundRefusalCodes(got); strings.Join(codes, ",") != "LANE_MOVED_AFTER_APPROVAL,APPROVAL_STAMP_MISSING" {
		t.Errorf("codes = %v", codes)
	}
}

func TestIsBookkeepingPath(t *testing.T) {
	for p, want := range map[string]bool{
		"kitty-specs/m/status.json":                      true,
		"x/kitty-specs/m/meta.json":                      true,
		"kitty-specs/other/meta.json":                    false,
		".kittify/derived/m/x.json":                      true,
		"kitty-ops/01ARZ3NDEKTSV4RRFFQ69G5FAV.jsonl":     true,
		"src/kitty-ops/01ARZ3NDEKTSV4RRFFQ69G5FAV.jsonl": false,
		"src/meta.json":                                  false,
	} {
		if got := isBookkeepingPath(p, "m", ""); got != want {
			t.Errorf("isBookkeepingPath(%q) = %v, want %v", p, got, want)
		}
	}
	// nested alias coordination file of a bare-slug coordination claim
	if !isBookkeepingPath("kitty-specs/m-01ABCDEF/status.events.jsonl", "m", ".worktrees/m-01ABCDEF-coord/kitty-specs/m-01ABCDEF") {
		t.Error("nested alias status log should be bookkeeping")
	}
	if isBookkeepingPath("kitty-specs/m-01ABCDEF/spec.md", "m", ".worktrees/m-01ABCDEF-coord/kitty-specs/m-01ABCDEF") {
		t.Error("nested alias planning file is content")
	}
}

func TestUnmetApprovedContent(t *testing.T) {
	blob := func(s string) pstate { return pstate{s, true} }
	lane := &laneContent{laneID: "lane-a", wpIDs: []string{"WP01"}, ancestors: map[string]bool{},
		final: map[string]pstate{"src/a.py": blob("new"), "src/gone.py": {}}}
	target := map[string]pstate{"src/a.py": blob("old"), "src/gone.py": blob("old-gone")}
	base := map[string]pstate{"src/a.py": blob("old"), "src/gone.py": blob("old-gone")}
	got := unmetApprovedContent([]*laneContent{lane}, func(p string) pstate { return target[p] }, func(p string) pstate { return base[p] },
		func(string) bool { return false }, nil)
	if len(got) != 2 || got[0].path != "src/a.py" || got[0].found != "unchanged" || got[1].path != "src/gone.py" || got[1].expected.present {
		t.Fatalf("unmet = %+v", got)
	}
	d := &divergence{contentMissing: got}
	if s := d.describe(); !strings.Contains(s, "APPROVED_CONTENT_MISSING: approved WP01 (lane lane-a) file 'src/a.py' on the target still holds its pre-consolidation content") ||
		!strings.Contains(s, "its deletion of 'src/gone.py' is not applied to the target") {
		t.Errorf("describe = %s", s)
	}
	target["src/a.py"] = blob("new")
	target["src/gone.py"] = pstate{}
	if got := unmetApprovedContent([]*laneContent{lane}, func(p string) pstate { return target[p] }, func(p string) pstate { return base[p] },
		func(string) bool { return false }, nil); len(got) != 0 {
		t.Errorf("landed content reported missing: %+v", got)
	}
}

func TestRollbackRender(t *testing.T) {
	r := &RollbackReport{outcomes: []branchOutcome{
		{branch: "main", kind: okRestored, snapshot: "aaaaaaaaaa", observed: "bbbbbbbbbb", restoredTo: "cccccccccc"},
		{branch: "kitty/mission-x", kind: okNotRestored, observed: "dddddddddd", reason: "moved by another actor since this run", expected: "eeeeeeeeee"},
		{branch: "kitty/mission-x-lane-a", kind: okUnchangedByRun, observed: "ffffffffff"},
	}}
	want := "Rollback, restored to the record's restore targets:\n" +
		"  restored   main                    bbbbbbb -> ccccccc\n" +
		"  NOT restored kitty/mission-x         observed ddddddd, expected eeeeeee -- moved by another actor since this run\n" +
		"  kept       kitty/mission-x-lane-a  (lane branch: not moved by consolidation; at fffffff)"
	if got := r.Render(); got != want {
		t.Errorf("render:\n%s\nwant:\n%s", got, want)
	}
	if r.fullyRestored() {
		t.Error("a NOT restored branch is not a full restore")
	}
}
