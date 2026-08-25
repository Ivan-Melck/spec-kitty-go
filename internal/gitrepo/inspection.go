package gitrepo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type Discrepancy struct {
	Fact     string `json:"fact"`
	Observed string `json:"observed"`
	Expected string `json:"expected"`
}

// Inspection is a read-only Git fact report. PreservedCommits names every
// exact commit observed or recorded so a later explicit reconciliation does
// not depend on a moving branch.
type Inspection struct {
	Known            bool          `json:"known"`
	Registered       bool          `json:"registered"`
	Clean            bool          `json:"clean"`
	CanonicalPath    string        `json:"canonical_path"`
	RecordedBranch   string        `json:"recorded_branch"`
	ObservedBranch   string        `json:"observed_branch"`
	RecordedHead     string        `json:"recorded_head"`
	ObservedHead     string        `json:"observed_head"`
	Discrepancies    []Discrepancy `json:"discrepancies"`
	PreservedCommits []string      `json:"preserved_commits"`
}

func (r *Repository) Inspect(ctx context.Context, record workspace.Record) (Inspection, error) {
	result := Inspection{CanonicalPath: record.CanonicalPath, RecordedBranch: record.OwnedBranch, RecordedHead: record.DependencyHead}
	expectedPath, err := r.WorkspacePath(record.WorkspaceID.String())
	if err != nil {
		return result, err
	}
	if record.CanonicalPath != expectedPath {
		result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "canonical_path", Observed: record.CanonicalPath, Expected: expectedPath})
		result.PreservedCommits = exactCommits(record.DependencyHead, record.ObservedHead)
		return result, nil
	}
	result.Known = true
	probe, err := r.Probe(ctx, record)
	if err != nil {
		return result, err
	}
	result.Registered, result.Clean = probe.Registered, probe.Clean
	result.ObservedBranch, result.ObservedHead = probe.Branch, probe.Head
	if !probe.Registered {
		result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "registration", Observed: "absent", Expected: "registered"})
	} else {
		if probe.Branch != record.OwnedBranch {
			result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "branch", Observed: probe.Branch, Expected: record.OwnedBranch})
		}
		if record.DependencyHead != "" && probe.Head != record.DependencyHead {
			result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "head", Observed: probe.Head, Expected: record.DependencyHead})
		}
		if !probe.Clean {
			result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "dirty", Observed: "true", Expected: "false"})
		}
		if record.GitDir != "" && probe.GitDir != record.GitDir {
			result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "git_dir", Observed: probe.GitDir, Expected: record.GitDir})
		}
		if record.IndexPath != "" && probe.IndexPath != record.IndexPath {
			result.Discrepancies = append(result.Discrepancies, Discrepancy{Fact: "index_path", Observed: probe.IndexPath, Expected: record.IndexPath})
		}
	}
	result.PreservedCommits = exactCommits(record.DependencyHead, record.ObservedHead, probe.Head)
	return result, nil
}

// Cleanup removes only a known, clean, inactive, non-quarantined worktree.
// It never deletes the owned branch, so the exact commit remains reachable.
func (r *Repository) Cleanup(ctx context.Context, record workspace.Record, leaseStatus string) error {
	inspection, err := r.Inspect(ctx, record)
	if err != nil {
		return err
	}
	if err := ValidateCleanup(record, leaseStatus, inspection); err != nil {
		return err
	}
	if _, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "remove", "--", record.CanonicalPath); err != nil {
		return &workspace.Error{Kind: workspace.KindGitFailure, Message: "remove verified cleanup-eligible worktree", Cause: err}
	}
	return nil
}

// ValidateCleanup is the single non-mutating cleanup policy decision used by
// inspection and cleanup command surfaces. Every refusal is reported so an
// operator does not have to fix hazards one at a time to discover the rest.
func ValidateCleanup(record workspace.Record, leaseStatus string, inspection Inspection) error {
	var reasons []string
	if leaseStatus == "active" {
		reasons = append(reasons, "active authority owns the workspace")
	}
	if record.Status == "quarantined" {
		reasons = append(reasons, "quarantined evidence is permanent until explicit reconciliation")
	}
	if !record.Ready() {
		reasons = append(reasons, "workspace never reached verified readiness")
	}
	if !inspection.Known || !inspection.Registered {
		reasons = append(reasons, "workspace is unknown or unregistered")
	}
	if !inspection.Clean {
		reasons = append(reasons, "workspace contains uncommitted changes")
	}
	if len(inspection.Discrepancies) != 0 {
		reasons = append(reasons, "workspace Git facts differ from the durable record")
	}
	if len(reasons) == 0 {
		return nil
	}
	return &workspace.Error{
		Kind: workspace.KindCleanupRefused, Message: "refuse workspace cleanup: " + strings.Join(reasons, "; "),
		Observed: map[string]string{"lease_status": leaseStatus, "workspace_status": record.Status, "registered": fmt.Sprint(inspection.Registered), "head": inspection.ObservedHead, "branch": inspection.ObservedBranch, "clean": fmt.Sprint(inspection.Clean)},
		Expected: map[string]string{"lease_status": "expired or surrendered", "workspace_status": "verified ready and non-quarantined", "registered": "true", "clean": "true", "head": inspection.RecordedHead, "branch": inspection.RecordedBranch},
	}
}

func exactCommits(values ...string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if isObjectID(value) && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
