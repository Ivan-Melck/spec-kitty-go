package main

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestSourceIdentityFailsClosedOutsideGitRepository(t *testing.T) {
	directory := t.TempDir()
	commit, tree, dirty, err := sourceIdentity(directory)
	if err == nil || commit != "" || tree != "" || dirty {
		t.Fatalf("commit=%q tree=%q dirty=%t err=%v", commit, tree, dirty, err)
	}
}

func TestSourceIdentityReturnsCompleteCurrentRepositoryBinding(t *testing.T) {
	commit, tree, _, err := sourceIdentity(".")
	if err != nil || commit == "" || tree == "" {
		t.Fatalf("commit=%q tree=%q err=%v", commit, tree, err)
	}
}

func TestBindCampaignSourceRejectsIncompleteAndDriftingProfiles(t *testing.T) {
	report := campaignReport{}
	if err := bindCampaignSource(&report, testcampaign.Report{}); err == nil {
		t.Fatal("incomplete source identity was accepted")
	}
	first := testcampaign.Report{SourceCommit: "commit-1", SourceTree: "tree-1"}
	if err := bindCampaignSource(&report, first); err != nil {
		t.Fatal(err)
	}
	if report.SourceCommit != first.SourceCommit || report.SourceTree != first.SourceTree {
		t.Fatalf("campaign source not bound: %+v", report)
	}
	for name, profile := range map[string]testcampaign.Report{
		"commit": {SourceCommit: "commit-2", SourceTree: "tree-1"},
		"tree":   {SourceCommit: "commit-1", SourceTree: "tree-2"},
		"dirty":  {SourceCommit: "commit-1", SourceTree: "tree-1", SourceDirty: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := bindCampaignSource(&report, profile); err == nil {
				t.Fatal("source drift was accepted")
			}
		})
	}
}
