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
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

var safeLabel = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func main() {
	profileName := flag.String("profile", "", "curated campaign profile")
	artifactDir := flag.String("artifact-dir", "", "directory for JSON transcript and report")
	label := flag.String("label", "", "artifact label; defaults to the profile name")
	timeout := flag.Duration("timeout", 20*time.Minute, "go test process deadline")
	flag.Parse()
	if err := run(*profileName, *artifactDir, *label, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(profileName, artifactDir, label string, timeout time.Duration) error {
	profile, err := testcampaign.ProfileByName(profileName)
	if err != nil {
		return err
	}
	if artifactDir == "" {
		return errors.New("artifact-dir is required")
	}
	if label == "" {
		label = profile.Name
	}
	if !safeLabel.MatchString(label) {
		return fmt.Errorf("artifact label %q is unsafe", label)
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	transcriptPath := filepath.Join(artifactDir, label+".go-test.jsonl")
	stderrPath := filepath.Join(artifactDir, label+".stderr.log")
	reportPath := filepath.Join(artifactDir, label+".report.json")
	transcript, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create transcript: %w", err)
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = transcript.Close()
		return fmt.Errorf("create stderr transcript: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
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
		return fmt.Errorf("open transcript: %w", err)
	}
	report, verifyErr := testcampaign.Verify(input, testcampaign.VerifyRequest{
		Campaign: profile.Name, Package: profile.Package, Pattern: profile.Pattern,
		Expected: profile.ExpectedTests, ProcessExit: processExit,
		StartedAt: startedAt, FinishedAt: finishedAt,
	})
	if closeErr := input.Close(); closeErr != nil && verifyErr == nil {
		verifyErr = closeErr
	}
	reportFile, err := os.OpenFile(reportPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	encoder := json.NewEncoder(reportFile)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(report)
	closeErr := reportFile.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode report: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close report: %w", closeErr)
	}
	fmt.Printf("campaign=%s verdict=%s duration_ms=%d report=%s\n", profile.Name, report.Verdict, report.DurationMS, reportPath)
	if verifyErr != nil {
		return verifyErr
	}
	if runErr != nil {
		return fmt.Errorf("go test failed: %w", runErr)
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
