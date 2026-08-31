package mutants

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

var expectedIDs = []string{
	"split-batch", "pre-ack-publish", "retry-new-operation-id", "revision-fill-in", "server-revision-in-value-or-key",
	"invalid-event-order", "current-batch-checkpoint", "global-authority-cas", "shared-projection-head", "runtime-coordinate-conflation",
	"workspace-reuse", "same-contributor-review", "invalid-checkpoint", "playback-adapter-or-write", "watch-as-journal",
	"misattributed-public-fields", "fake-timeout", "gapdb-import-allowlist-or-internal-server", "incomplete-signature",
}

type recordingExecutor struct {
	observation GuardObservation
	err         error
	entries     []Entry
}

func (e *recordingExecutor) ExecuteGuard(_ context.Context, entry Entry) (GuardObservation, error) {
	e.entries = append(e.entries, entry)
	return e.observation, e.err
}

func TestExactNineteenMutantsBindDistinctNamedProductGuards(t *testing.T) {
	entries := Catalog()
	ids := make([]string, len(entries))
	seen := make(map[string]string)
	for i, entry := range entries {
		ids[i] = entry.ID
		if entry.Package == "" || entry.Test == "" || entry.Guard == "" {
			t.Fatalf("incomplete catalog entry: %+v", entry)
		}
		key := entry.Package + "/" + entry.Test
		if prior, exists := seen[key]; exists {
			t.Fatalf("mutants %s and %s share a product guard test", prior, entry.ID)
		}
		seen[key] = entry.ID
	}
	if !reflect.DeepEqual(ids, expectedIDs) {
		t.Fatalf("mutant census=%v want=%v", ids, expectedIDs)
	}
}

func TestCatalogOutcomeRequiresExactNamedRunAndPassEvents(t *testing.T) {
	tests := []struct {
		name string
		obs  GuardObservation
		err  error
		want Outcome
	}{
		{"killed", GuardObservation{NamedTestStarted: true, NamedTestPassed: true}, nil, OutcomeKilled},
		{"survived", GuardObservation{NamedTestStarted: true}, nil, OutcomeSurvived},
		{"not run", GuardObservation{}, nil, OutcomeNotRun},
		{"guard failed", GuardObservation{NamedTestStarted: true}, errors.New("test failed"), OutcomeSurvived},
	}
	for _, test := range tests {
		executor := &recordingExecutor{observation: test.obs, err: test.err}
		result := (Runner{Executor: executor}).Run(t.Context(), "run-m1", Catalog()[0])
		if result.Outcome != test.want || result.Reason == "" || len(result.ArtifactSHA256) != 64 || len(executor.entries) != 1 {
			t.Fatalf("%s result=%+v calls=%d", test.name, result, len(executor.entries))
		}
	}
}

func TestCancelledAndMissingRunNeverFabricateKilled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	executor := &recordingExecutor{err: context.Canceled}
	if result := (Runner{Executor: executor}).Run(ctx, "run", Catalog()[0]); result.Outcome != OutcomeNotRun {
		t.Fatalf("cancelled guard fabricated kill: %+v", result)
	}
	if result := (Runner{Executor: executor}).Run(t.Context(), "", Catalog()[0]); result.Outcome != OutcomeNotRun {
		t.Fatalf("missing RunID fabricated kill: %+v", result)
	}
}

// Every result below comes from the actual product/architecture guard test.
func TestCatalogProductGuardsExecuteAndKillAllNineteen(t *testing.T) {
	results := (Runner{}).RunAll(t.Context(), "run-m1-product-guards")
	if len(results) != len(expectedIDs) {
		t.Fatalf("results=%d want=%d", len(results), len(expectedIDs))
	}
	for _, result := range results {
		if result.Outcome != OutcomeKilled {
			t.Fatalf("product mutant survived or did not run: %+v", result)
		}
	}
}
