package consolidate

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestSingleBranchEvidenceMatchesOracle(t *testing.T) {
	python := oraclePython(t)
	for _, scenario := range []string{"missing", "independent", "self-review", "no-actors", "no-window", "bad-window", "empty-ownership", "excluded"} {
		t.Run(scenario, func(t *testing.T) {
			s := testkit.NewSandbox(t)
			s.Git("init", "-q", "-b", "main")
			md := filepath.Join(s.Dir, "kitty-specs/demo")
			s.WriteFile("kitty-specs/demo/meta.json", `{"topology":"single_branch"}`)
			owned := "[src/**]"
			if scenario == "empty-ownership" {
				owned = "[]"
			}
			s.WriteFile("kitty-specs/demo/tasks/WP01-demo.md", "---\nwork_package_id: WP01\ntitle: Demo\nowned_files: "+owned+"\n---\n# Demo\n")
			s.Git("add", ".")
			s.Git("commit", "-qm", "planning")
			base := s.Git("rev-parse", "HEAD")
			if scenario != "missing" {
				s.WriteFile("src/demo.go", "package demo\n")
				s.Git("add", ".")
				s.Git("commit", "-qm", "implementation")
			}
			if scenario == "bad-window" {
				base = "unknown"
			}
			m := &lanes.Manifest{Version: 1, MissionSlug: "demo", MissionBranch: "main", TargetBranch: "main", PlanningCommitSHA: &base, Lanes: []lanes.Lane{{LaneID: "planning", WPIDs: []string{"WP01"}}}}
			if scenario == "no-window" {
				m.PlanningCommitSHA = nil
			}
			if scenario != "no-actors" {
				reviewer := "reviewer"
				if scenario == "self-review" {
					reviewer = "implementer"
				}
				for i, e := range []*status.Event{{WPID: "WP01", From: status.Planned, To: status.InProgress, Actor: status.StrActor("implementer")}, {WPID: "WP01", From: status.InReview, To: status.Approved, Actor: status.StrActor(reviewer)}} {
					e.MissionSlug = "demo"
					e.EventID = fmt.Sprintf("01ARZ3NDEKTSV4RRFFQ69G5FA%d", i)
					e.At = "2026-10-24T00:00:00+00:00"
					e.ExecutionMode = "direct_repo"
					if err := status.AppendEvents(md, e); err != nil {
						t.Fatal(err)
					}
				}
			}
			excluded := map[string]bool{}
			excludedIDs := []any{}
			if scenario == "excluded" {
				excluded["WP01"] = true
				excludedIDs = append(excludedIDs, "WP01")
			}
			got := singleBranchEvidenceRefusal(s.Dir, md, m, map[string]string{"WP01": "approved"}, excluded)
			script := "import json\nfrom pathlib import Path\nfrom specify_cli.lanes.models import LanesManifest\nfrom specify_cli.consolidation.reconciliation import _single_branch_evidence_refusal\nm=LanesManifest.from_dict(json.loads(" + jsonString(jsonx.Dumps(m.ToObject(), jsonx.Line)) + "))\nbase=m.planning_commit_sha\nwindow=(base,m.mission_branch) if base else None\nprint(_single_branch_evidence_refusal(Path.cwd(),Path.cwd()/'kitty-specs/demo',m,{'WP01':{'lane':'approved'}},frozenset(" + jsonx.Dumps(excludedIDs, jsonx.Line) + "),window,is_single_branch=True) or '')"
			oracle := s.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatal(oracle.Stderr)
			}
			if got+"\n" != oracle.Stdout {
				t.Error(testkit.Diff(oracle.Stdout, got+"\n"))
			}
		})
	}
}
