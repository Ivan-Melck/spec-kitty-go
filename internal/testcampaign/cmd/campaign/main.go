package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

var safeLabel = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var readCampaignSourceIdentity = sourceIdentity

type profileResult struct {
	Name       string `json:"name"`
	ReportPath string `json:"report_path"`
	Verdict    string `json:"verdict"`
	DurationMS int64  `json:"duration_ms"`
}

type campaignReport struct {
	SchemaVersion int             `json:"schema_version"`
	Campaign      string          `json:"campaign"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	DurationMS    int64           `json:"duration_ms"`
	Profiles      []profileResult `json:"profiles"`
	SourceCommit  string          `json:"source_commit,omitempty"`
	SourceTree    string          `json:"source_tree,omitempty"`
	SourceDirty   bool            `json:"source_dirty"`
	Verdict       string          `json:"verdict"`
	Issues        []string        `json:"issues,omitempty"`
}

func main() {
	profileName := flag.String("profile", "", "one curated test profile")
	campaignName := flag.String("campaign", "", "one authoritative multi-profile campaign")
	artifactDir := flag.String("artifact-dir", "", "directory for JSON transcripts and reports")
	label := flag.String("label", "", "artifact label; valid only with -profile")
	timeout := flag.Duration("timeout", 20*time.Minute, "deadline for each go test profile")
	flag.Parse()
	if err := run(*profileName, *campaignName, *artifactDir, *label, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(profileName, campaignName, artifactDir, label string, timeout time.Duration) error {
	if (profileName == "") == (campaignName == "") {
		return errors.New("exactly one of -profile or -campaign is required")
	}
	if artifactDir == "" {
		return errors.New("artifact-dir is required")
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	if campaignName != "" {
		if label != "" {
			return errors.New("label is not valid with -campaign")
		}
		return runCampaign(campaignName, artifactDir, timeout)
	}
	_, _, err := runProfile(profileName, artifactDir, label, timeout)
	return err
}

func runCampaign(name, artifactDir string, timeout time.Duration) error {
	campaign, err := testcampaign.CampaignByName(name)
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	report := campaignReport{SchemaVersion: 1, Campaign: campaign.Name, StartedAt: startedAt, Verdict: "fail"}
	for _, profileName := range campaign.Profiles {
		profileReport, reportPath, profileErr := runProfile(profileName, artifactDir, "", timeout)
		report.Profiles = append(report.Profiles, profileResult{
			Name: profileName, ReportPath: filepath.Base(reportPath), Verdict: profileReport.Verdict, DurationMS: profileReport.DurationMS,
		})
		if sourceErr := bindCampaignSource(&report, profileReport); sourceErr != nil {
			report.Issues = append(report.Issues, fmt.Sprintf("profile %s: %v", profileName, sourceErr))
		}
		if profileErr != nil {
			report.Issues = append(report.Issues, fmt.Sprintf("profile %s: %v", profileName, profileErr))
			break
		}
	}
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = report.FinishedAt.Sub(startedAt).Milliseconds()
	if len(report.Issues) == 0 && len(report.Profiles) == len(campaign.Profiles) {
		report.Verdict = "pass"
	}
	reportPath := filepath.Join(artifactDir, name+".campaign.json")
	if err := writeJSON(reportPath, report); err != nil {
		return err
	}
	fmt.Printf("campaign=%s verdict=%s duration_ms=%d report=%s\n", report.Campaign, report.Verdict, report.DurationMS, reportPath)
	if report.Verdict != "pass" {
		return fmt.Errorf("campaign %s failed: %s", name, strings.Join(report.Issues, "; "))
	}
	return nil
}

func runProfile(profileName, artifactDir, label string, timeout time.Duration) (testcampaign.Report, string, error) {
	profile, err := testcampaign.ProfileByName(profileName)
	if err != nil {
		return testcampaign.Report{}, "", err
	}
	if label == "" {
		label = profile.Name
	}
	if !safeLabel.MatchString(label) {
		return testcampaign.Report{}, "", fmt.Errorf("artifact label %q is unsafe", label)
	}
	preCommit, preTree, preDirty, err := readCampaignSourceIdentity(".")
	if err != nil {
		return testcampaign.Report{}, "", fmt.Errorf("bind source before profile execution: %w", err)
	}
	if preDirty {
		return testcampaign.Report{}, "", errors.New("profile source is dirty before execution")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	packagePath, err := resolvePackage(ctx, profile.Package)
	if err != nil {
		return testcampaign.Report{}, "", err
	}
	transcriptPath := filepath.Join(artifactDir, label+".go-test.jsonl")
	stderrPath := filepath.Join(artifactDir, label+".stderr.log")
	reportPath := filepath.Join(artifactDir, label+".report.json")
	transcript, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return testcampaign.Report{}, reportPath, fmt.Errorf("create transcript: %w", err)
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = transcript.Close()
		return testcampaign.Report{}, reportPath, fmt.Errorf("create stderr transcript: %w", err)
	}

	args := []string{"test", "-json", "-count=1", profile.Package, "-timeout", timeout.String()}
	if profile.Pattern != "" {
		args = append(args, "-run", profile.Pattern)
	}
	command := exec.CommandContext(ctx, "go", args...)
	command.Stdout = io.MultiWriter(os.Stdout, transcript)
	command.Stderr = io.MultiWriter(os.Stderr, stderr)
	startedAt := time.Now().UTC()
	runErr := command.Run()
	finishedAt := time.Now().UTC()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	processExit := exitCode(runErr)
	if closeErr := transcript.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
		processExit = 1
	}
	if closeErr := stderr.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
		processExit = 1
	}

	input, err := os.Open(transcriptPath)
	if err != nil {
		return testcampaign.Report{}, reportPath, fmt.Errorf("open transcript: %w", err)
	}
	report, verifyErr := testcampaign.Verify(input, newVerifyRequest(profile, packagePath, processExit, startedAt, finishedAt, timeout, timedOut))
	if closeErr := input.Close(); closeErr != nil && verifyErr == nil {
		verifyErr = closeErr
	}
	commit, tree, dirty, identityErr := readCampaignSourceIdentity(".")
	if identityErr != nil {
		report.Verdict = "fail"
		report.Issues = append(report.Issues, "source identity: "+identityErr.Error())
		if verifyErr == nil {
			verifyErr = fmt.Errorf("source identity: %w", identityErr)
		}
	} else {
		report.SourceCommit, report.SourceTree, report.SourceDirty = commit, tree, dirty
		if commit != preCommit || tree != preTree || dirty {
			report.Verdict = "fail"
			report.Issues = append(report.Issues, "source identity drifted or became dirty during profile execution")
			if verifyErr == nil {
				verifyErr = errors.New("source identity drifted or became dirty during profile execution")
			}
		}
	}
	if err := writeJSON(reportPath, report); err != nil {
		return report, reportPath, err
	}
	fmt.Printf("profile=%s verdict=%s duration_ms=%d report=%s\n", profile.Name, report.Verdict, report.DurationMS, reportPath)
	if verifyErr != nil {
		return report, reportPath, verifyErr
	}
	if runErr != nil {
		return report, reportPath, fmt.Errorf("go test failed: %w", runErr)
	}
	return report, reportPath, nil
}

func newVerifyRequest(profile testcampaign.Profile, packagePath string, processExit int, startedAt, finishedAt time.Time, timeout time.Duration, timedOut bool) testcampaign.VerifyRequest {
	return testcampaign.VerifyRequest{
		Campaign: profile.Name, Package: packagePath, Pattern: profile.Pattern,
		Expected: append([]string(nil), profile.ExpectedTests...), ProcessExit: processExit,
		StartedAt: startedAt, FinishedAt: finishedAt,
		Timeout: timeout, TimedOut: timedOut,
	}
}

func resolvePackage(ctx context.Context, selector string) (string, error) {
	command := exec.CommandContext(ctx, "go", "list", "-f", "{{.ImportPath}}", selector)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve package %s: %w", selector, err)
	}
	packagePath := strings.TrimSpace(string(output))
	if packagePath == "" || strings.Contains(packagePath, "\n") {
		return "", fmt.Errorf("resolve package %s returned %q", selector, packagePath)
	}
	return packagePath, nil
}

func bindCampaignSource(report *campaignReport, profile testcampaign.Report) error {
	if profile.SourceCommit == "" || profile.SourceTree == "" {
		return errors.New("profile source identity is incomplete")
	}
	if report.SourceCommit == "" {
		report.SourceCommit, report.SourceTree, report.SourceDirty = profile.SourceCommit, profile.SourceTree, profile.SourceDirty
		return nil
	}
	if report.SourceCommit != profile.SourceCommit || report.SourceTree != profile.SourceTree || report.SourceDirty != profile.SourceDirty {
		return fmt.Errorf("profile source identity drifted: got %s/%s dirty=%t want %s/%s dirty=%t",
			profile.SourceCommit, profile.SourceTree, profile.SourceDirty,
			report.SourceCommit, report.SourceTree, report.SourceDirty)
	}
	return nil
}

func sourceIdentity(directory string) (commit, tree string, dirty bool, err error) {
	if commit, err = gitOutput(directory, "rev-parse", "HEAD"); err != nil {
		return "", "", false, err
	}
	if tree, err = gitOutput(directory, "rev-parse", "HEAD^{tree}"); err != nil {
		return "", "", false, err
	}
	status, err := gitOutput(directory, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", "", false, err
	}
	if commit == "" || tree == "" {
		return "", "", false, errors.New("git returned an empty commit or tree identity")
	}
	return commit, tree, status != "", nil
}

func gitOutput(directory string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func writeJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode report: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close report: %w", closeErr)
	}
	return nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
		return exitErr.ExitCode()
	}
	return 1
}
