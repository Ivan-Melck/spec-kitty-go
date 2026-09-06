// Package external owns M2's direct use of the admitted provider adapter.
// It is qualification tooling only and is never reachable from production.
package external

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

const performancePrefix = "m2/performance/"

type PerformanceConfig struct {
	RunID        string
	Repository   string
	Executable   string
	RuntimeRoot  string
	DataRoot     string
	Source       m2.Source
	Warmup       int
	Observations int
	RecoveryRuns int
	StartTimeout time.Duration
	StopTimeout  time.Duration
}

// MeasurePerformance measures the admitted external process and storage-neutral
// adapter. Every sample is retained. The caller, not this function, decides
// whether the reference host is admissible.
func MeasurePerformance(ctx context.Context, config PerformanceConfig) (m2.RawPerformance, error) {
	result := m2.RawPerformance{SchemaVersion: 1, RunID: config.RunID, SourceCommit: config.Source.Commit, SourceTree: config.Source.Tree, RecoveryOperations: config.Observations, WarmupExcluded: config.Warmup, Method: "monotonic-nearest-rank-inclusive"}
	if config.RunID == "" || config.Repository == "" || config.Executable == "" || config.Observations < 10_000 || config.RecoveryRuns < 30 || config.Warmup < 0 {
		return result, fmt.Errorf("M2 performance configuration is incomplete")
	}
	adoption := filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-adoption.json")
	admissionRecord := filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-admission.json")
	admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(adoption, admissionRecord)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(config.RuntimeRoot, 0o700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(config.DataRoot, 0o700); err != nil {
		return result, err
	}

	start := func(sequence int) (*sidecargapdb.Lifecycle, *storegapdb.Adapter, func(), error) {
		lifecycle, startErr := sidecargapdb.Start(ctx, sidecargapdb.LifecycleConfig{
			ExecutablePath: config.Executable, AdmissionRecordPath: admissionRecord, Admission: admission,
			RuntimeRoot: config.RuntimeRoot, DataDirectory: config.DataRoot,
			SupervisorRunID:     fmt.Sprintf("%s-performance-%02d", config.RunID, sequence),
			ProductPolicySHA256: m2.SHA256([]byte("m2-performance-policy-v1")),
			StartTimeout:        config.StartTimeout, StopTimeout: config.StopTimeout,
		})
		if startErr != nil {
			return nil, nil, nil, startErr
		}
		evidence, evidenceErr := lifecycle.Evidence()
		if evidenceErr != nil {
			_ = lifecycle.Stop(context.Background())
			return nil, nil, nil, evidenceErr
		}
		adapter, closer, dialErr := storegapdb.Dial(evidence.ConfiguredDaemonSocketPath, 5*time.Second)
		if dialErr != nil {
			_ = lifecycle.Stop(context.Background())
			return nil, nil, nil, dialErr
		}
		closeAll := func() {
			_ = closer.Close()
			_ = lifecycle.Stop(context.Background())
		}
		return lifecycle, adapter, closeAll, nil
	}

	_, adapter, closeAll, err := start(0)
	if err != nil {
		return result, err
	}
	for index := 0; index < config.Warmup+config.Observations; index++ {
		key := fmt.Sprintf("%s%05d", performancePrefix, index%config.Observations)
		started := time.Now()
		_, mutateErr := adapter.ConditionalMutate(ctx, fmt.Sprintf("%s-put-%05d", config.RunID, index), storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: key, Condition: storegapdb.Condition{Kind: storegapdb.ConditionAny}, Value: []byte(fmt.Sprintf("value-%05d", index))}, storegapdb.AckDurable)
		elapsed := time.Since(started)
		if mutateErr != nil {
			result.Failures++
			closeAll()
			return result, mutateErr
		}
		if index >= config.Warmup {
			result.MutationMS = append(result.MutationMS, milliseconds(elapsed))
		}
	}
	for index := 0; index < config.Warmup+config.Observations; index++ {
		key := fmt.Sprintf("%s%05d", performancePrefix, index%config.Observations)
		started := time.Now()
		record, getErr := adapter.Get(ctx, key)
		elapsed := time.Since(started)
		if getErr != nil || record.Key != key {
			result.Failures++
			closeAll()
			return result, fmt.Errorf("query observation %d failed: %w", index, getErr)
		}
		if index >= config.Warmup {
			result.QueryMS = append(result.QueryMS, milliseconds(elapsed))
		}
	}
	closeAll()

	for run := 0; run < config.RecoveryRuns; run++ {
		started := time.Now()
		_, recovered, stop, startErr := start(run + 1)
		if startErr != nil {
			result.Failures++
			return result, startErr
		}
		count, scanErr := scanAll(ctx, recovered)
		result.RecoveryMS = append(result.RecoveryMS, milliseconds(time.Since(started)))
		stop()
		if scanErr != nil || count != config.Observations {
			result.Failures++
			return result, fmt.Errorf("recovery run %d restored %d/%d records: %w", run, count, config.Observations, scanErr)
		}
	}
	return result, nil
}

func scanAll(ctx context.Context, adapter *storegapdb.Adapter) (int, error) {
	count, cursor := 0, ""
	for {
		page, err := adapter.Scan(ctx, storegapdb.ScanRequest{Prefix: performancePrefix, Limit: 500, Cursor: cursor, MaxBytes: 8 << 20})
		if err != nil {
			return count, err
		}
		count += len(page.Records)
		if !page.Truncated {
			return count, nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return count, fmt.Errorf("scan cursor did not advance")
		}
		cursor = page.Cursor
	}
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Nanoseconds()) / float64(time.Millisecond)
}
