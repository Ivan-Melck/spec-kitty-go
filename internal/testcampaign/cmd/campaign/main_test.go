package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestSourceIdentityFailsClosedOutsideGitRepository(t *testing.T) {
	directory := t.TempDir()
	commit, tree, dirty, err := sourceIdentity(directory)
	if err == nil || commit != "" || tree != "" || dirty {
		t.Fatalf("commit=%q tree=%q dirty=%t err=%v", commit, tree, dirty, err)
	}
}

func TestSourceIdentityIncludesRelevantUntrackedFiles(t *testing.T) {
	repository := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = repository
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.go")
	git("commit", "-qm", "fixture")
	if err := os.WriteFile(filepath.Join(repository, "new.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, dirty, err := sourceIdentity(repository)
	if err != nil || !dirty {
		t.Fatalf("relevant untracked source dirty=%t err=%v", dirty, err)
	}
}

func TestRunProfileRefusesDirtySourceBeforeExecuting(t *testing.T) {
	original := readCampaignSourceIdentity
	t.Cleanup(func() { readCampaignSourceIdentity = original })
	readCampaignSourceIdentity = func(string) (string, string, bool, error) {
		return strings.Repeat("a", 40), strings.Repeat("b", 40), true, nil
	}
	directory := t.TempDir()
	if _, _, err := runProfile("provider-portable", directory, "pre-dirty", time.Minute); err == nil || !strings.Contains(err.Error(), "dirty before") {
		t.Fatalf("dirty preflight error=%v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(directory, "pre-dirty.*")); len(matches) != 0 {
		t.Fatalf("profile executed before dirty refusal: %v", matches)
	}
}

func TestRunProfileRebindsSourceAfterExecutionAndRefusesDrift(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(working, "..", "..", "..", ".."))
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(working) })
	original := readCampaignSourceIdentity
	t.Cleanup(func() { readCampaignSourceIdentity = original })
	calls := 0
	readCampaignSourceIdentity = func(string) (string, string, bool, error) {
		calls++
		if calls == 1 {
			return strings.Repeat("a", 40), strings.Repeat("b", 40), false, nil
		}
		return strings.Repeat("a", 40), strings.Repeat("c", 40), false, nil
	}
	directory := t.TempDir()
	report, path, err := runProfile("provider-portable", directory, "post-drift", 2*time.Minute)
	if err == nil || report.Verdict == "pass" || !strings.Contains(strings.Join(report.Issues, " "), "source identity drifted") {
		t.Fatalf("post-run drift report=%+v path=%q err=%v", report, path, err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("truthful failed report missing: %v", statErr)
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

func TestNewVerifyRequestBindsConfiguredTimeoutAndDeadlineOutcome(t *testing.T) {
	started := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	profile := testcampaign.Profile{Name: "mission-contract", Package: "./internal/scenario", Pattern: "^TestMissionMethodQualificationContract$", ExpectedTests: []string{"TestMissionMethodQualificationContract"}}
	request := newVerifyRequest(profile, "github.com/spec-kitty/go-kitty3/internal/scenario", 124, started, started.Add(time.Minute), 4*time.Minute, true)
	if request.Campaign != profile.Name || request.Package == "" || request.Pattern != profile.Pattern || len(request.Expected) != 1 || request.ProcessExit != 124 || request.StartedAt != started || request.FinishedAt != started.Add(time.Minute) || request.Timeout != 4*time.Minute || !request.TimedOut {
		t.Fatalf("verify request lost timeout or source fields: %+v", request)
	}
	request.Expected[0] = "mutated"
	if profile.ExpectedTests[0] == "mutated" {
		t.Fatal("verify request leaked expected-test mutation into profile")
	}
}
