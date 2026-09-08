package external

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	restorev2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

const (
	performanceRecoveryRequestEnv = "SPEC_KITTY_M2_RECOVERY_REQUEST"
	performanceRecoveryOutputEnv  = "SPEC_KITTY_M2_RECOVERY_OUTPUT"
	performanceRecoveryDigestEnv  = "SPEC_KITTY_M2_RECOVERY_REQUEST_SHA256"
	maxRecoveryReceiptBytes       = 64 << 20
)

type performanceRecoveryRequestV1 struct {
	SchemaVersion        uint64    `json:"schema_version"`
	RunID                string    `json:"run_id"`
	RunIndex             int       `json:"run_index"`
	Repository           string    `json:"repository"`
	Executable           string    `json:"sidecar_executable"`
	RuntimeRoot          string    `json:"runtime_root"`
	DataRoot             string    `json:"data_root"`
	Source               m2.Source `json:"source"`
	HostAuthorityID      string    `json:"host_authority_id"`
	CheckpointRevision   uint64    `json:"checkpoint_revision"`
	CheckpointOperations int       `json:"checkpoint_operations"`
	ExpectedOperationIDs []string  `json:"expected_operation_ids"`
	DatasetSHA256        string    `json:"dataset_sha256"`
	StartTimeoutNS       int64     `json:"start_timeout_ns"`
	StopTimeoutNS        int64     `json:"stop_timeout_ns"`
	IncludeOperations    bool      `json:"include_operations"`
	ChildExecutable      string    `json:"child_executable"`
	ChildSHA256          string    `json:"child_sha256"`
}

type performanceRecoveryReceiptV1 struct {
	SchemaVersion        uint64                        `json:"schema_version"`
	RunID                string                        `json:"run_id"`
	RunIndex             int                           `json:"run_index"`
	SourceCommit         string                        `json:"source_commit"`
	SourceTree           string                        `json:"source_tree"`
	DatasetSHA256        string                        `json:"dataset_sha256"`
	RequestSHA256        string                        `json:"request_sha256"`
	ProcessID            int                           `json:"process_id"`
	ChildExecutable      string                        `json:"child_executable"`
	ChildSHA256          string                        `json:"child_sha256"`
	StoreRevision        uint64                        `json:"store_revision"`
	ProjectionSHA256     string                        `json:"projection_sha256"`
	ObservedOperations   int                           `json:"observed_operations"`
	PostCheckpointStates int                           `json:"post_checkpoint_states"`
	OperationsSHA256     string                        `json:"operations_sha256"`
	RuntimeOperations    []string                      `json:"runtime_operations"`
	DurationNS           int64                         `json:"duration_ns"`
	Operations           map[string]RecoveredOperation `json:"operations"`
}

type performanceRecoveryDatasetV1 struct {
	RunID                string   `json:"run_id"`
	SourceCommit         string   `json:"source_commit"`
	SourceTree           string   `json:"source_tree"`
	DataRoot             string   `json:"data_root"`
	HostAuthorityID      string   `json:"host_authority_id"`
	CheckpointRevision   uint64   `json:"checkpoint_revision"`
	CheckpointOperations int      `json:"checkpoint_operations"`
	ExpectedOperationIDs []string `json:"expected_operation_ids"`
}

func performanceRecoveryDatasetSHA256(config PerformanceConfig, hostAuthorityID string) (string, error) {
	encoded, err := m2.CanonicalJSON(performanceRecoveryDatasetV1{
		RunID: config.RunID, SourceCommit: config.Source.Commit, SourceTree: config.Source.Tree,
		DataRoot: config.DataRoot, HostAuthorityID: hostAuthorityID,
		CheckpointRevision: config.CheckpointRevision, CheckpointOperations: config.CheckpointOperations,
		ExpectedOperationIDs: config.ExpectedOperationIDs,
	})
	if err != nil {
		return "", err
	}
	return m2.SHA256(encoded), nil
}

func runPerformanceRecoveryChild(ctx context.Context, config PerformanceConfig, hostAuthorityID string, run int, datasetSHA, childSHA string) (RecoveryObservation, performanceRecoveryReceiptV1, error) {
	var observation RecoveryObservation
	var receipt performanceRecoveryReceiptV1
	runRoot := filepath.Join(config.RuntimeRoot, fmt.Sprintf("recovery-%02d", run))
	request := performanceRecoveryRequestV1{
		SchemaVersion: 1, RunID: config.RunID, RunIndex: run, Repository: config.Repository,
		Executable: config.Executable, RuntimeRoot: filepath.Join(runRoot, "runtime"), DataRoot: config.DataRoot,
		Source: config.Source, HostAuthorityID: hostAuthorityID,
		CheckpointRevision: config.CheckpointRevision, CheckpointOperations: config.CheckpointOperations,
		ExpectedOperationIDs: append([]string(nil), config.ExpectedOperationIDs...), DatasetSHA256: datasetSHA,
		StartTimeoutNS: int64(config.StartTimeout), StopTimeoutNS: int64(config.StopTimeout), IncludeOperations: run == 0,
		ChildExecutable: config.RecoveryExecutable, ChildSHA256: childSHA,
	}
	requestBytes, err := m2.CanonicalJSON(request)
	if err != nil {
		return observation, receipt, err
	}
	if len(requestBytes) > maxRecoveryReceiptBytes {
		return observation, receipt, fmt.Errorf("performance recovery request exceeds bound")
	}
	if err := os.MkdirAll(runRoot, 0o700); err != nil {
		return observation, receipt, err
	}
	requestPath := filepath.Join(runRoot, "request.json")
	outputPath := filepath.Join(runRoot, "receipt.json")
	if err := writePerformanceRecoveryCreateOnce(requestPath, requestBytes); err != nil {
		return observation, receipt, err
	}
	requestSHA := m2.SHA256(requestBytes)
	command := exec.CommandContext(ctx, config.RecoveryExecutable,
		"-test.run=^TestM2AcceptedAuthorityPerformanceCampaign$", "-test.count=1", "-test.timeout=15m")
	command.Dir = config.Repository
	command.Env = performanceRecoveryChildEnvironment(os.Environ(), requestPath, outputPath, requestSHA)
	transcript := boundedBuffer{limit: 1 << 20}
	command.Stdout, command.Stderr = &transcript, &transcript
	if err := command.Run(); err != nil {
		return observation, receipt, fmt.Errorf("performance recovery child %d: %w: %s", run, err, boundedRecoveryDiagnostic(transcript.Bytes()))
	}
	if transcript.exceeded {
		return observation, receipt, fmt.Errorf("performance recovery child transcript exceeds bound")
	}
	receiptBytes, err := readPerformanceRecoveryArtifact(outputPath)
	if err != nil {
		return observation, receipt, fmt.Errorf("performance recovery child %d receipt: %w", run, err)
	}
	if receipt, err = decodePerformanceRecoveryReceipt(request, requestSHA, receiptBytes); err != nil {
		return observation, receipt, err
	}
	observation = RecoveryObservation{
		DurationNS: receipt.DurationNS, ProjectionSHA256: receipt.ProjectionSHA256,
		ObservedOperations: receipt.ObservedOperations, Operations: receipt.Operations,
		RuntimeOperations: append([]string(nil), receipt.RuntimeOperations...),
	}
	return observation, receipt, nil
}

func performanceRecoveryChildEnvironment(base []string, requestPath, outputPath, requestSHA string) []string {
	result := make([]string, 0, len(base)+3)
	for _, value := range base {
		key, _, found := bytes.Cut([]byte(value), []byte{'='})
		if found && (string(key) == performanceRecoveryRequestEnv || string(key) == performanceRecoveryOutputEnv || string(key) == performanceRecoveryDigestEnv) {
			continue
		}
		result = append(result, value)
	}
	return append(result, performanceRecoveryRequestEnv+"="+requestPath, performanceRecoveryOutputEnv+"="+outputPath, performanceRecoveryDigestEnv+"="+requestSHA)
}

func validatePerformanceRecoveryReceipt(request performanceRecoveryRequestV1, requestSHA string, receipt performanceRecoveryReceiptV1) error {
	operationsBytes, err := m2.CanonicalJSON(receipt.Operations)
	if err != nil {
		return err
	}
	if receipt.SchemaVersion != 1 || receipt.RunID != request.RunID || receipt.RunIndex != request.RunIndex ||
		receipt.SourceCommit != request.Source.Commit || receipt.SourceTree != request.Source.Tree ||
		receipt.DatasetSHA256 != request.DatasetSHA256 || receipt.RequestSHA256 != requestSHA || receipt.ProcessID <= 0 ||
		receipt.ChildExecutable != request.ChildExecutable || receipt.ChildSHA256 != request.ChildSHA256 ||
		receipt.StoreRevision < request.CheckpointRevision || receipt.ProjectionSHA256 == "" ||
		receipt.ObservedOperations != len(request.ExpectedOperationIDs) || receipt.PostCheckpointStates < receipt.ObservedOperations || receipt.PostCheckpointStates > receipt.ObservedOperations+1 || receipt.DurationNS <= 0 ||
		receipt.OperationsSHA256 != m2.SHA256(operationsBytes) || len(receipt.RuntimeOperations) != 0 ||
		request.IncludeOperations != (len(receipt.Operations) != 0) {
		return fmt.Errorf("performance recovery child %d receipt identity or census differs", request.RunIndex)
	}
	if request.IncludeOperations && len(receipt.Operations) != receipt.PostCheckpointStates {
		return fmt.Errorf("performance recovery child %d operation evidence differs", request.RunIndex)
	}
	return nil
}

func decodePerformanceRecoveryReceipt(request performanceRecoveryRequestV1, requestSHA string, encoded []byte) (performanceRecoveryReceiptV1, error) {
	return decodePerformanceRecoveryReceiptBounded(request, requestSHA, encoded, maxRecoveryReceiptBytes)
}

func decodePerformanceRecoveryReceiptBounded(request performanceRecoveryRequestV1, requestSHA string, encoded []byte, maximum int) (performanceRecoveryReceiptV1, error) {
	var receipt performanceRecoveryReceiptV1
	if maximum <= 0 || maximum > maxRecoveryReceiptBytes || len(encoded) == 0 || len(encoded) > maximum {
		return receipt, fmt.Errorf("performance recovery receipt size is outside bound")
	}
	if err := m2.DecodeStrict(encoded, &receipt); err != nil {
		return receipt, fmt.Errorf("performance recovery receipt is malformed: %w", err)
	}
	if err := validatePerformanceRecoveryReceipt(request, requestSHA, receipt); err != nil {
		return performanceRecoveryReceiptV1{}, err
	}
	return receipt, nil
}

// RunPerformanceRecoverySampleChildIfRequested executes one isolated recovery
// sample when the private request coordinate is present. The caller is the
// exact source-built scenario test binary; no production command imports this
// qualification-only package.
func RunPerformanceRecoverySampleChildIfRequested(ctx context.Context) (bool, error) {
	if os.Getenv(performanceRecoveryRequestEnv) == "" {
		return false, nil
	}
	requestPath, outputPath, expectedSHA := os.Getenv(performanceRecoveryRequestEnv), os.Getenv(performanceRecoveryOutputEnv), os.Getenv(performanceRecoveryDigestEnv)
	if !filepath.IsAbs(requestPath) || !filepath.IsAbs(outputPath) || expectedSHA == "" {
		return true, fmt.Errorf("performance recovery child environment is incomplete")
	}
	requestBytes, err := readPerformanceRecoveryArtifact(requestPath)
	if err != nil || len(requestBytes) == 0 || len(requestBytes) > maxRecoveryReceiptBytes || m2.SHA256(requestBytes) != expectedSHA {
		return true, fmt.Errorf("performance recovery request is absent, oversized, or substituted: %w", err)
	}
	var request performanceRecoveryRequestV1
	if err := m2.DecodeStrict(requestBytes, &request); err != nil {
		return true, err
	}
	if err := validatePerformanceRecoveryRequest(request); err != nil {
		return true, err
	}
	config := PerformanceConfig{
		RunID: request.RunID, Repository: request.Repository, Executable: request.Executable,
		RuntimeRoot: request.RuntimeRoot, DataRoot: request.DataRoot, Source: request.Source,
		Observations: len(request.ExpectedOperationIDs), RecoveryRuns: 1,
		CheckpointRevision: request.CheckpointRevision, CheckpointOperations: request.CheckpointOperations,
		ExpectedOperationIDs: request.ExpectedOperationIDs,
		StartTimeout:         time.Duration(request.StartTimeoutNS), StopTimeout: time.Duration(request.StopTimeoutNS),
	}
	state, err := startPerformanceRuntime(ctx, config)
	if err != nil {
		return true, err
	}
	defer state.close()
	started := time.Now()
	recovered, err := restorev2.RestoreV2(ctx, restorev2.InputV2{
		Reader: state.adapter, EventRegistry: state.registry, Reducers: state.reducers,
		HostAuthorityID:     journalv2.HostAuthorityID(request.HostAuthorityID),
		PriorRuntimeEpochID: "epoch-performance-before-checkpoint",
		RecoveryAttemptID:   fmt.Sprintf("performance-recovery-%02d", request.RunIndex),
		Budgets:             restorev2.BudgetsForLogicalCPUsV2(runtime.GOMAXPROCS(0)),
	})
	duration := time.Since(started)
	if err != nil {
		return true, err
	}
	postCheckpointStates := countOperationStates(recovered) - request.CheckpointOperations
	operations := map[string]RecoveredOperation{}
	runtimeOperations := []string{}
	if request.IncludeOperations {
		operations, runtimeOperations, err = operationsFromEvents(ctx, state.adapter, recovered, request.CheckpointRevision)
		if err != nil {
			return true, err
		}
		if postCheckpointStates != len(operations) || len(runtimeOperations) != 0 {
			return true, fmt.Errorf("performance recovery operation state/event census differs")
		}
		if err := validatePerformanceOperationCensus(operations, request.ExpectedOperationIDs, func(operation string) (journalv2.OperationBindingV2, error) {
			return readPerformanceOperationBinding(ctx, state.adapter, recovered.StoreRevision(), operation)
		}); err != nil {
			return true, err
		}
	} else if postCheckpointStates < len(request.ExpectedOperationIDs) || postCheckpointStates > len(request.ExpectedOperationIDs)+1 {
		return true, fmt.Errorf("performance recovery operation-state census differs")
	}
	operationsBytes, err := m2.CanonicalJSON(operations)
	if err != nil {
		return true, err
	}
	childExecutable, err := os.Executable()
	if err != nil {
		return true, err
	}
	childExecutable, err = filepath.EvalSymlinks(childExecutable)
	if err != nil || childExecutable != request.ChildExecutable {
		return true, fmt.Errorf("performance recovery child executable path differs: %w", err)
	}
	childSHA, err := digestPerformanceRecoveryExecutable(childExecutable)
	if err != nil || childSHA != request.ChildSHA256 {
		return true, fmt.Errorf("performance recovery child executable digest differs: %w", err)
	}
	receipt := performanceRecoveryReceiptV1{
		SchemaVersion: 1, RunID: request.RunID, RunIndex: request.RunIndex,
		SourceCommit: request.Source.Commit, SourceTree: request.Source.Tree,
		DatasetSHA256: request.DatasetSHA256, RequestSHA256: expectedSHA, ProcessID: os.Getpid(), ChildExecutable: childExecutable, ChildSHA256: childSHA,
		StoreRevision: recovered.StoreRevision().CurrentRevision(), ProjectionSHA256: recovered.Playback().CanonicalProjectionDigest().String(),
		ObservedOperations: len(request.ExpectedOperationIDs), PostCheckpointStates: postCheckpointStates, OperationsSHA256: m2.SHA256(operationsBytes),
		RuntimeOperations: runtimeOperations, DurationNS: duration.Nanoseconds(),
	}
	receipt.Operations = operations
	encoded, err := m2.CanonicalJSON(receipt)
	if err != nil {
		return true, err
	}
	return true, writePerformanceRecoveryCreateOnce(outputPath, encoded)
}

func validatePerformanceRecoveryRequest(request performanceRecoveryRequestV1) error {
	if request.SchemaVersion != 1 || request.RunID == "" || request.RunIndex < 0 || !filepath.IsAbs(request.Repository) ||
		!filepath.IsAbs(request.Executable) || !filepath.IsAbs(request.RuntimeRoot) || !filepath.IsAbs(request.DataRoot) ||
		!filepath.IsAbs(request.ChildExecutable) || request.ChildSHA256 == "" ||
		request.HostAuthorityID == "" || request.CheckpointRevision == 0 || request.CheckpointOperations < 0 ||
		len(request.ExpectedOperationIDs) != 10_000 || !slices.IsSorted(request.ExpectedOperationIDs) ||
		request.StartTimeoutNS <= 0 || request.StopTimeoutNS <= 0 {
		return fmt.Errorf("performance recovery request is incomplete")
	}
	for index, operation := range request.ExpectedOperationIDs {
		if operation == "" || index > 0 && operation == request.ExpectedOperationIDs[index-1] {
			return fmt.Errorf("performance recovery operation census is noncanonical")
		}
	}
	config := PerformanceConfig{RunID: request.RunID, Source: request.Source, DataRoot: request.DataRoot, CheckpointRevision: request.CheckpointRevision, CheckpointOperations: request.CheckpointOperations, ExpectedOperationIDs: request.ExpectedOperationIDs}
	datasetSHA, err := performanceRecoveryDatasetSHA256(config, request.HostAuthorityID)
	if err != nil || datasetSHA != request.DatasetSHA256 {
		return fmt.Errorf("performance recovery dataset identity differs: %w", err)
	}
	return nil
}

func digestPerformanceRecoveryExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return "", fmt.Errorf("performance recovery child executable is outside bound: %w", err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil || int64(len(encoded)) != info.Size() {
		return "", fmt.Errorf("read performance recovery child executable: %w", err)
	}
	return m2.SHA256(encoded), nil
}

func writePerformanceRecoveryCreateOnce(path string, encoded []byte) error {
	if !filepath.IsAbs(path) || len(encoded) == 0 || len(encoded) > maxRecoveryReceiptBytes {
		return fmt.Errorf("performance recovery artifact path or size is invalid")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readPerformanceRecoveryArtifact(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("performance recovery artifact must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) || info.Size() <= 0 || info.Size() > maxRecoveryReceiptBytes {
		return nil, fmt.Errorf("performance recovery artifact size is outside bound: %w", err)
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxRecoveryReceiptBytes+1))
	if err != nil || int64(len(encoded)) != info.Size() {
		return nil, fmt.Errorf("read performance recovery artifact: %w", err)
	}
	return encoded, nil
}

func boundedRecoveryDiagnostic(encoded []byte) string {
	const maximum = 4096
	if len(encoded) > maximum {
		encoded = encoded[len(encoded)-maximum:]
	}
	return strconv.QuoteToASCII(string(encoded))
}
