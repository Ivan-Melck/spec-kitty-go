package prereview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// ArtifactPath is <dir>/tasks/<wp slug>/baseline-tests.json.
func ArtifactPath(dir, wpSlug string) string {
	return filepath.Join(dir, "tasks", wpSlug, "baseline-tests.json")
}

// CaptureOptions configures one baseline capture.
type CaptureOptions struct {
	RepoRoot   string // where the detached base worktree is created from
	BaseBranch string // ref the baseline is captured at
	WPID       string
	Artifact   string
	Source     *DeclaredSource
	Warn       func(string) // logger.warning sink
}

func (o CaptureOptions) warn(format string, a ...any) {
	if o.Warn != nil {
		o.Warn(fmt.Sprintf(format, a...))
	}
}

// Capture is capture_baseline via the injected ScopeSource: the cached
// artifact when present, nil when no command is declared, the -1 sentinel
// (not persisted) on any capture failure, else a saved baseline.
func Capture(o CaptureOptions) (*Baseline, error) {
	cached, err := LoadBaseline(o.Artifact)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return cached, nil
	}
	cmd, ok := o.Source.TestCommand()
	if !ok {
		return nil, nil
	}
	res, err := gitx.Exec(o.RepoRoot, nil, nil, "rev-parse", o.BaseBranch)
	if err != nil || res.Code != 0 {
		stderr := ""
		if err == nil {
			stderr = res.Stderr
		}
		o.warn("Could not resolve base branch %s: %s", o.BaseBranch, stderr)
		return Sentinel(o.WPID, o.BaseBranch, ""), nil
	}
	baseCommit := strings.TrimSpace(res.Stdout)
	tmp, err := os.MkdirTemp("", "")
	if err != nil {
		return Sentinel(o.WPID, o.BaseBranch, baseCommit), nil
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "baseline-worktree")
	add, err := gitx.Exec(o.RepoRoot, nil, nil, "worktree", "add", wt, o.BaseBranch, "--detach")
	if err != nil || add.Code != 0 {
		stderr := ""
		if err == nil {
			stderr = add.Stderr
		}
		o.warn("Could not create baseline worktree for %s: %s", o.BaseBranch, stderr)
		return Sentinel(o.WPID, o.BaseBranch, baseCommit), nil
	}
	raw := runForBaseline(cmd, wt)
	failures, perr := o.Source.ParseResults(raw)
	identity := o.Source.Identity(raw)
	if r, err := gitx.Exec(o.RepoRoot, nil, nil, "worktree", "remove", "--force", wt); err != nil || r.Code != 0 {
		gitx.Exec(o.RepoRoot, nil, nil, "worktree", "prune")
	}
	if perr != nil {
		return nil, perr
	}
	if len(failures) == 0 && identity[strings.LastIndex(identity, "/")+1:] == "text" {
		o.warn("Declared test command exited cleanly but produced no verifiable output (no JUnit artifact, no parseable failure line) for %s; refusing to store an ambiguous zero-failure baseline.", o.WPID)
		return Sentinel(o.WPID, o.BaseBranch, baseCommit), nil
	}
	runner := "custom"
	for _, part := range cmd {
		if strings.Contains(part, "pytest") {
			runner = "pytest"
		}
	}
	b := &Baseline{WPID: o.WPID, CapturedAt: UTCSecondStamp(), BaseBranch: o.BaseBranch, BaseCommit: baseCommit,
		TestRunner: runner, Total: len(failures), Failed: len(failures), Failures: failures, SourceIdentity: identity}
	if err := b.Save(o.Artifact); err != nil {
		return nil, err
	}
	return b, nil
}

// runForBaseline is baseline._run_command_for_baseline (lock-free).
func runForBaseline(cmd []string, dir string) *Raw {
	junit := junitArg(cmd)
	if junit != "" && !filepath.IsAbs(junit) {
		junit = filepath.Join(dir, junit)
	}
	state, raw, errText := RunRaw(cmd, RunOptions{Dir: dir, Timeout: CaptureTimeoutSeconds * time.Second})
	switch state {
	case RunTimedOut:
		return &Raw{ReturnCode: -1, Stderr: fmt.Sprintf("baseline command timed out after %ds", CaptureTimeoutSeconds), ArtifactPath: junit}
	case RunCancelled:
		return &Raw{ReturnCode: -1, Stderr: "baseline command cancelled: " + errText, ArtifactPath: junit}
	case RunLaunchFailed:
		return &Raw{ReturnCode: -1, Stderr: "baseline command failed to execute: " + errText, ArtifactPath: junit}
	}
	return raw
}
