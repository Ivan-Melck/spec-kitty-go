package m2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type CommandReceipt struct {
	SchemaVersion int      `json:"schema_version"`
	Stage         string   `json:"stage"`
	ExitCode      int      `json:"exit_code"`
	RawSHA256     string   `json:"raw_sha256"`
	Runs          int      `json:"runs"`
	Passes        int      `json:"passes"`
	Failures      int      `json:"failures"`
	Skips         int      `json:"skips"`
	Packages      []string `json:"packages"`
	Verdict       string   `json:"verdict"`
}

func RunAbsenceQualification(ctx context.Context, repository, sidecar string, attempt *Attempt) ([]ArtifactInput, error) {
	if attempt == nil || !filepath.IsAbs(repository) || !filepath.IsAbs(sidecar) {
		return nil, fmt.Errorf("absence qualification requires absolute paths and a reserved attempt")
	}
	if digest, err := fileDigest(sidecar); err != nil || digest != WP16AbsenceSidecarSHA256 {
		return nil, fmt.Errorf("absence qualification requires the frozen WP16 sidecar")
	}
	working := filepath.Join(attempt.Directory, ".working-absence")
	command := exec.CommandContext(ctx, filepath.Join(repository, "scripts/m2-absence/inspect.sh"), working)
	command.Dir = repository
	command.Env = append(os.Environ(), "SPEC_KITTY_WP15_GAPDBD="+sidecar)
	raw, err := command.CombinedOutput()
	if captureErr := attempt.Capture("logs/absence-command.txt", raw); captureErr != nil {
		return nil, captureErr
	}
	if err != nil {
		return nil, fmt.Errorf("WP16 absence qualification failed: %w", err)
	}
	inputs, err := collectEvidenceTree(filepath.Join(working, "evidence"), "absence", "absence-evidence")
	if err != nil {
		return nil, err
	}
	if !containsInput(inputs, "absence/manifest.json") || !containsInput(inputs, "absence/absence-tests.json") || !containsInput(inputs, "absence/layer-mutants.json") || !containsInput(inputs, "absence/runtime-result.json") {
		return nil, fmt.Errorf("WP16 absence evidence is incomplete")
	}
	for _, name := range []string{"expected-absence-tests.txt", "expected-layer-mutants.txt"} {
		data, err := os.ReadFile(filepath.Join(repository, "scripts", "m2-absence", name))
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, ArtifactInput{Path: "absence/" + name, Kind: "absence-contract", Data: data})
	}
	if err := removePrivateWorkingTree(attempt.Directory, working); err != nil {
		return nil, err
	}
	inputs = append(inputs, ArtifactInput{Path: "logs/absence-command.txt", Kind: "absence-command", Data: raw})
	slices.SortFunc(inputs, func(a, b ArtifactInput) int { return strings.Compare(a.Path, b.Path) })
	return inputs, nil
}

func removePrivateWorkingTree(attemptRoot, root string) error {
	if !filepath.IsAbs(attemptRoot) || filepath.Clean(attemptRoot) != attemptRoot ||
		filepath.Base(attemptRoot) != "evidence" || filepath.Base(filepath.Dir(filepath.Dir(attemptRoot))) != ".attempts" {
		return fmt.Errorf("private working tree requires a narrow absolute attempt root")
	}
	attemptInfo, err := os.Lstat(attemptRoot)
	if err != nil {
		return err
	}
	if attemptInfo.Mode()&os.ModeSymlink != 0 || !attemptInfo.IsDir() {
		return fmt.Errorf("private working tree attempt root must be a real directory")
	}
	resolvedAttempt, err := filepath.EvalSymlinks(attemptRoot)
	if err != nil || resolvedAttempt != attemptRoot {
		return fmt.Errorf("private working tree attempt root must not traverse symbolic links")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root != filepath.Join(attemptRoot, ".working-absence") {
		return fmt.Errorf("private working tree must be the reserved attempt absence directory")
	}
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("private working tree must be a real directory")
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// WalkDir never follows symbolic links. Only directories need owner
		// permissions restored for unlinking read-only Go module/toolchain
		// caches; files and external symlink targets remain untouched.
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.Chmod(path, info.Mode().Perm()|0o700)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func RunRaceQualification(ctx context.Context, repository, sidecar string, attempt *Attempt) ([]ArtifactInput, error) {
	arguments := []string{"test", "-json", "-race", "-p=1", "-count=1", "-timeout=20m", "./internal/scenario", "./internal/productionruntime", "./internal/testcampaign/m2", "-run", "^(TestTwoItemMissionRestartAfterEachDurableBoundary|TestConcurrentIndependentAuthoritySmoke|TestConcurrentSameAggregateConflictSmoke|TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch|TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound|TestFrozenCensusRejectsDeletionReorderAndSubstitution|TestRealGitTwoItemCrownUsesConcreteAdapterAndWorktrees|TestTwoItemRealGitGapDBCrown)$"}
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = repository
	command.Env = append(os.Environ(), "SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="+sidecar, "SPEC_KITTY_M2_QUALIFICATION_CANDIDATE=true")
	raw, commandErr := command.CombinedOutput()
	exitCode := 0
	if commandErr != nil {
		exitCode = 1
		if ctx.Err() != nil {
			exitCode = 124
		}
	}
	receipt, verifyErr := VerifyRaceReceipt(raw, exitCode)
	receiptBytes, err := CanonicalJSON(receipt)
	if err != nil {
		return nil, err
	}
	if err := attempt.Capture("race/events.jsonl", raw); err != nil {
		return nil, err
	}
	if err := attempt.Capture("race.json", receiptBytes); err != nil {
		return nil, err
	}
	if verifyErr != nil {
		return nil, verifyErr
	}
	return []ArtifactInput{{Path: "race/events.jsonl", Kind: "race-raw", Data: raw}, {Path: "race.json", Kind: "race", Data: receiptBytes}}, nil
}

var raceExpected = map[string]string{
	"TestTwoItemMissionRestartAfterEachDurableBoundary":                 "github.com/spec-kitty/go-kitty3/internal/scenario",
	"TestTwoItemRealGitGapDBCrown":                                      "github.com/spec-kitty/go-kitty3/internal/scenario",
	"TestConcurrentIndependentAuthoritySmoke":                           "github.com/spec-kitty/go-kitty3/internal/scenario",
	"TestConcurrentSameAggregateConflictSmoke":                          "github.com/spec-kitty/go-kitty3/internal/scenario",
	"TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch": "github.com/spec-kitty/go-kitty3/internal/productionruntime",
	"TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound":        "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2",
	"TestFrozenCensusRejectsDeletionReorderAndSubstitution":             "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2",
	"TestRealGitTwoItemCrownUsesConcreteAdapterAndWorktrees":            "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2",
}

func VerifyRaceReceipt(raw []byte, exitCode int) (CommandReceipt, error) {
	receipt, err := VerifyGoCommandReceipt(raw, "race", exitCode)
	if err != nil {
		return receipt, err
	}
	run, pass := map[string]int{}, map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return receipt, err
		}
		parent := strings.SplitN(event.Test, "/", 2)[0]
		wantPackage, known := raceExpected[parent]
		if event.Test != "" && (!known || event.Package != wantPackage) {
			return receipt, fmt.Errorf("race transcript contains unmatched test %s in %s", event.Test, event.Package)
		}
		if event.Test == parent {
			if event.Action == "run" {
				run[parent]++
			}
			if event.Action == "pass" {
				pass[parent]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return receipt, err
	}
	for name := range raceExpected {
		if run[name] != 1 || pass[name] != 1 {
			return receipt, fmt.Errorf("race test %s receipts run=%d pass=%d want=1/1", name, run[name], pass[name])
		}
	}
	return receipt, nil
}

func VerifyGoCommandReceipt(raw []byte, stage string, exitCode int) (CommandReceipt, error) {
	receipt := CommandReceipt{SchemaVersion: 1, Stage: stage, ExitCode: exitCode, RawSHA256: SHA256(raw), Verdict: "fail"}
	packages := map[string]struct{}{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return receipt, fmt.Errorf("malformed go command event: %w", err)
		}
		if event.Package != "" {
			packages[event.Package] = struct{}{}
		}
		if event.Test == "" {
			continue
		}
		switch event.Action {
		case "run":
			receipt.Runs++
		case "pass":
			receipt.Passes++
		case "fail":
			receipt.Failures++
		case "skip":
			receipt.Skips++
		}
	}
	if err := scanner.Err(); err != nil {
		return receipt, err
	}
	for name := range packages {
		receipt.Packages = append(receipt.Packages, name)
	}
	slices.Sort(receipt.Packages)
	if exitCode != 0 || receipt.Runs == 0 || receipt.Runs != receipt.Passes || receipt.Failures != 0 || receipt.Skips != 0 {
		return receipt, fmt.Errorf("%s receipt incomplete: run=%d pass=%d fail=%d skip=%d exit=%d", stage, receipt.Runs, receipt.Passes, receipt.Failures, receipt.Skips, exitCode)
	}
	receipt.Verdict = "pass"
	return receipt, nil
}

func collectEvidenceTree(root, prefix, kind string) ([]ArtifactInput, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var inputs []ArtifactInput
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence tree contains symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maximumFile {
			return fmt.Errorf("evidence member is not a bounded regular file")
		}
		total += info.Size()
		if total > maximumTotal {
			return fmt.Errorf("evidence tree exceeds total budget")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		artifactPath := strings.TrimSuffix(prefix, "/") + "/" + filepath.ToSlash(relative)
		inputs = append(inputs, ArtifactInput{Path: artifactPath, Kind: kind, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(inputs, func(a, b ArtifactInput) int { return strings.Compare(a.Path, b.Path) })
	if len(inputs) == 0 || len(inputs) > maximumFiles {
		return nil, fmt.Errorf("evidence tree census is empty or over budget")
	}
	return inputs, nil
}

func containsInput(inputs []ArtifactInput, path string) bool {
	for _, input := range inputs {
		if input.Path == path {
			return true
		}
	}
	return false
}
