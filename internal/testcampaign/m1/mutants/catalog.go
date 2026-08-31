package mutants

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Outcome string

const (
	OutcomeKilled   Outcome = "killed"
	OutcomeSurvived Outcome = "survived"
	OutcomeNotRun   Outcome = "not_run"
)

// Entry binds a stable mutant to the exact executable product test containing
// its clean control and defect fixture. The test traverses the real guard; this
// catalog does not reimplement the product rule with local booleans.
type Entry struct{ ID, Package, Test, Guard string }

type Result struct {
	ID             string  `json:"id"`
	RunID          string  `json:"run_id"`
	Outcome        Outcome `json:"outcome"`
	Guard          string  `json:"guard"`
	Package        string  `json:"package"`
	Test           string  `json:"test"`
	ArtifactSHA256 string  `json:"artifact_sha256"`
	Reason         string  `json:"reason"`
	Artifact       []byte  `json:"-"`
}

var catalog = []Entry{
	{"split-batch", "./internal/controljournal/application", "TestBatchRefusesDuplicatesInconsistencyAndSplitMutant", "validated batch single-publication guard"},
	{"pre-ack-publish", "./internal/controljournal/application", "TestServiceOneBatchStableIdentityAndNoSilentRetry", "application acknowledgement publication gate"},
	{"retry-new-operation-id", "./internal/controlstore/gapdb", "TestMutatingTransportFailuresNeverRetryWithANewOperation", "same-operation ambiguity reconciliation"},
	{"revision-fill-in", "./internal/controljournal/application", "TestBatchExistingHeadRequiresExactPriorRevision", "exact predecessor revision validation"},
	{"server-revision-in-value-or-key", "./internal/architecture", "TestM1DependencyJournalPurityMutants", "pure journal representation/source guard"},
	{"invalid-event-order", "./internal/controljournal/application", "TestBatchPreconditionDigestAndEventMembershipMutants", "event-group membership and ordering guard"},
	{"current-batch-checkpoint", "./internal/controljournal/checkpoint", "TestCheckpointRejectsCurrentBatchAndNonterminalWatermark", "checkpoint prior-prefix capability"},
	{"global-authority-cas", "./internal/controljournal/application", "TestServiceAggregateConcurrencyIsPerHeadNotGlobal", "per-aggregate authority CAS"},
	{"shared-projection-head", "./internal/controljournal/application", "TestServiceHeadPreflightRetainsCASRaceProtection", "independent aggregate head preflight"},
	{"runtime-coordinate-conflation", "./internal/provingground", "TestZombiePredecessorEpochRefusesIndependentlyOfLease", "runtime epoch independent from lease/fence"},
	{"workspace-reuse", "./internal/provingground", "TestWorkspaceReuseMutantSecondBindingForSameLeaseRefuses", "lease-scoped immutable workspace binding"},
	{"same-contributor-review", "./internal/provingground", "TestReviewActorIDIndependenceAndImmutableCandidateEvidence", "reviewer ActorID independence"},
	{"invalid-checkpoint", "./internal/controljournal/checkpoint", "TestRestoreExplicitEarlierFallbackAndAllInvalidRefusal", "validated checkpoint fallback"},
	{"playback-adapter-or-write", "./internal/controljournal/checkpoint", "TestRestorePlaybackDigestEqualityAcrossOneHundredValidHistories", "pure zero-I/O playback"},
	{"watch-as-journal", "./internal/controljournal/checkpoint", "TestRestoreReadSeamCannotUseWatchAsJournal", "ordinary sorted-scan journal reader"},
	{"misattributed-public-fields", "./internal/provingground", "TestWP08EffectInvariantMutantsCannotFabricateBindingOrAttribution", "effect provider identity and attribution"},
	{"fake-timeout", "./internal/sidecar/gapdb", "TestResponseDropPartialMalformedAndImpossibleResponsesAreNotGJ02", "complete durable response-drop cut"},
	{"gapdb-import-allowlist-or-internal-server", "./internal/architecture", "TestM1DependencyMutantControls", "M1 dependency/source boundary"},
	{"incomplete-signature", "./internal/architecture", "TestM1EvidenceMutantControls", "exact evidence signature predicate"},
}

func Catalog() []Entry { return append([]Entry(nil), catalog...) }

type GuardExecutor interface {
	ExecuteGuard(context.Context, Entry) (GuardObservation, error)
}
type GuardObservation struct {
	NamedTestStarted, NamedTestPassed bool
	Output                            []byte
}
type Runner struct {
	Root     string
	Executor GuardExecutor
}

func (r Runner) Run(ctx context.Context, runID string, entry Entry) Result {
	result := Result{ID: entry.ID, RunID: runID, Guard: entry.Guard, Package: entry.Package, Test: entry.Test, Outcome: OutcomeNotRun}
	if runID == "" || entry.ID == "" || entry.Package == "" || entry.Test == "" || entry.Guard == "" {
		result.Reason = "mutant entry or RunID is incomplete"
		result.Artifact = artifactBytes(result, nil)
		result.ArtifactSHA256 = digestBytes(result.Artifact)
		return result
	}
	executor := r.Executor
	if executor == nil {
		root := r.Root
		if root == "" {
			var err error
			root, err = moduleRoot()
			if err != nil {
				result.Reason = bounded(err.Error())
				result.Artifact = artifactBytes(result, nil)
				result.ArtifactSHA256 = digestBytes(result.Artifact)
				return result
			}
		}
		executor = GoTestExecutor{Root: root}
	}
	observation, err := executor.ExecuteGuard(ctx, entry)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Outcome = OutcomeNotRun
		} else {
			result.Outcome = OutcomeSurvived
		}
		result.Reason = bounded(err.Error())
	} else if !observation.NamedTestStarted {
		result.Reason = "named product guard did not run"
	} else if !observation.NamedTestPassed {
		result.Outcome = OutcomeSurvived
		result.Reason = "named product guard did not kill its defect fixture"
	} else {
		result.Outcome = OutcomeKilled
		result.Reason = "clean control and defect fixture passed the named product guard test"
	}
	result.Artifact = artifactBytes(result, observation.Output)
	result.ArtifactSHA256 = digestBytes(result.Artifact)
	return result
}

func (r Runner) RunAll(ctx context.Context, runID string) []Result {
	results := make([]Result, len(catalog))
	for i, entry := range catalog {
		results[i] = r.Run(ctx, runID, entry)
	}
	return results
}

// GoTestExecutor consumes structured events. A deleted or misspelled test
// cannot look green through Go's successful "no tests to run" behavior.
type GoTestExecutor struct{ Root string }

func (e GoTestExecutor) ExecuteGuard(ctx context.Context, entry Entry) (GuardObservation, error) {
	if e.Root == "" || !strings.HasPrefix(entry.Package, "./internal/") || strings.ContainsAny(entry.Test, "^$|[]()\\") {
		return GuardObservation{}, fmt.Errorf("unsafe guard command identity")
	}
	command := exec.CommandContext(ctx, "go", "test", entry.Package, "-run", "^"+entry.Test+"$", "-count=1", "-json")
	command.Dir = e.Root
	encoded, err := command.CombinedOutput()
	if len(encoded) > 256<<10 {
		encoded = encoded[:256<<10]
	}
	observation := GuardObservation{Output: append([]byte(nil), encoded...)}
	scanner := bufio.NewScanner(strings.NewReader(string(encoded)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Test != entry.Test {
			continue
		}
		switch event.Action {
		case "run":
			observation.NamedTestStarted = true
		case "pass":
			observation.NamedTestPassed = true
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return observation, scanErr
	}
	if err != nil {
		return observation, fmt.Errorf("product guard %s/%s failed: %w", entry.Package, entry.Test, err)
	}
	return observation, nil
}

func moduleRoot() (string, error) {
	command := exec.Command("go", "env", "GOMOD")
	encoded, err := command.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(encoded))
	if path == "" || path == os.DevNull || filepath.Base(path) != "go.mod" {
		return "", fmt.Errorf("go module root is unavailable")
	}
	return filepath.Dir(path), nil
}

func artifactBytes(result Result, output []byte) []byte {
	encoded, _ := json.Marshal(struct {
		SchemaVersion uint64  `json:"schema_version"`
		ID            string  `json:"id"`
		RunID         string  `json:"run_id"`
		Outcome       Outcome `json:"outcome"`
		Guard         string  `json:"guard"`
		Package       string  `json:"package"`
		Test          string  `json:"test"`
		Reason        string  `json:"reason"`
		Output        []byte  `json:"structured_go_test_events"`
	}{1, result.ID, result.RunID, result.Outcome, result.Guard, result.Package, result.Test, result.Reason, output})
	return append(encoded, '\n')
}
func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
func bounded(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
