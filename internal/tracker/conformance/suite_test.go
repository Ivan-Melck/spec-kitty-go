package conformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestDurableFakePortableConformance(t *testing.T) {
	clearProviderEnvironment(t)
	census, err := Run(context.Background(), t.TempDir(), fakeFactory, DefaultCases())
	if err != nil {
		t.Fatal(err)
	}
	if len(census.Expected) != 12 || len(census.Passed) != len(census.Expected) || len(census.Failed) != 0 || len(census.NotApplicable) != 0 {
		t.Fatalf("unexpected census: %+v", census)
	}
	path := filepath.Join(t.TempDir(), "census.json")
	if err := WriteCensus(path, census); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil || !json.Valid(encoded) || strings.Contains(string(encoded), "portable body") {
		t.Fatalf("census is invalid or leaked semantic payload: err=%v body=%s", err, encoded)
	}
}

func TestRegistryAndCapabilityMutantsFailClosed(t *testing.T) {
	cases := DefaultCases()
	t.Run("omitted-case", func(t *testing.T) {
		census, err := Run(context.Background(), t.TempDir(), fakeFactory, cases[1:])
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(census.Expected, idsOf(cases)) {
			t.Fatal("omitted case escaped the exact census")
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		mutant := append(append([]Case(nil), cases...), cases[0])
		if _, err := Run(context.Background(), t.TempDir(), fakeFactory, mutant); err == nil || !strings.Contains(err.Error(), "duplicate case") {
			t.Fatalf("duplicate mutant error=%v", err)
		}
	})
	t.Run("unsupported-promoted", func(t *testing.T) {
		mutant := append([]Case(nil), cases...)
		for index := range mutant {
			if mutant[index].ID == "catalog.create.replay" {
				mutant[index].Supported = func(tracker.ProviderCapabilities) bool { return true }
			}
		}
		if _, err := Run(context.Background(), t.TempDir(), fakeFactory, mutant); err == nil || !strings.Contains(err.Error(), "catalog.create.replay") {
			t.Fatalf("capability mutant error=%v", err)
		}
	})
	t.Run("terminal-omission", func(t *testing.T) {
		census := Census{Expected: []string{"case-a"}, Started: []string{"case-a"}, Terminal: []string{"case-a"}, Results: []Result{{ID: "case-a"}}}
		if err := ValidateCensus(census); err == nil || !strings.Contains(err.Error(), "terminal dispositions") {
			t.Fatalf("terminal omission error=%v", err)
		}
	})
	t.Run("unexpected-result-id", func(t *testing.T) {
		census := Census{Expected: []string{"case-a"}, Started: []string{"case-a"}, Terminal: []string{"case-a"}, Passed: []string{"case-a"}, Results: []Result{{ID: "case-b", Disposition: Passed}}}
		if err := ValidateCensus(census); err == nil || (!strings.Contains(err.Error(), "result ids") && !strings.Contains(err.Error(), "disposition disagrees")) {
			t.Fatalf("unexpected result mutant error=%v", err)
		}
	})
	t.Run("missing-required-authority", func(t *testing.T) {
		census := Census{Expected: []string{"case-a"}, Started: []string{"case-a"}, Terminal: []string{"case-a"}, Passed: []string{"case-a"}, Results: []Result{{ID: "case-a", Disposition: Passed, UniversalRequired: true, StaleProbeRequired: true}}}
		if err := ValidateCensus(census); err == nil || !strings.Contains(err.Error(), "authority evidence") {
			t.Fatalf("missing authority mutant error=%v", err)
		}
	})
}

func fakeFactory(ctx context.Context, root, caseID string) (Fixture, error) {
	return fakeFactoryForCase(ctx, root, caseID)
}

func fakeFactoryForCase(ctx context.Context, root, caseID string) (Fixture, error) {
	path := filepath.Join(root, "fake-tracker.json")
	store, err := fake.Open(path)
	if err != nil {
		return Fixture{}, err
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "external-1", Title: "portable contract", Criteria: []string{"observable result"}, Assignee: "original"}); err != nil {
		return Fixture{}, err
	}
	provider := fake.NewProvider(store)
	catalog := tracker.Catalog(provider)
	capabilities, err := provider.Capabilities(ctx)
	if err != nil {
		return Fixture{}, err
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project-1", WorkID: "work-1", Provider: "durable_fake", ExternalID: "external-1", MetadataSchema: 1}
	diagnostic := DiagnosticExpectation{Reference: reference}
	switch caseID {
	case "diagnostic.unavailable.typed":
		catalog = fakeUnavailableCatalog{Catalog: provider}
		diagnostic.Kind, diagnostic.RecoveryCode = tracker.ErrorUnavailable, "repair_provider_binding"
	case "observation.stale.not-normalized":
		record, readErr := provider.ReadWork(ctx, reference)
		if readErr != nil {
			return Fixture{}, readErr
		}
		diagnostic.ProviderToken, diagnostic.Assignee, diagnostic.Marker = record.Observation.ProviderToken, record.VisibleAssignee, record.AssignmentMarker
	case "safety.wrong-state.refused":
		diagnostic.Reference = reference
		diagnostic.Reference.ExternalID = "missing-wrong-state"
		diagnostic.Kind = tracker.ErrorNotFound
	}
	return Fixture{
		Capabilities: capabilities, Reference: reference,
		Catalog: catalog, Graph: provider, Discussion: provider, Assignment: provider,
		Diagnostic: diagnostic,
		MutationCount: func(ctx context.Context) (int, error) {
			history, err := store.History(ctx)
			return len(history), err
		},
	}, nil
}

type fakeUnavailableCatalog struct{ tracker.Catalog }

func (fakeUnavailableCatalog) ReadWork(context.Context, tracker.ExternalWorkRef) (tracker.WorkRecord, error) {
	return tracker.WorkRecord{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "repair_provider_binding", Message: "provider unavailable"}
}

func clearProviderEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"BEADS_DIR", "BD_JSON_ENVELOPE", "BD_NON_INTERACTIVE", "DOLT_CREDENTIALS_PATH", "DOLT_REMOTE_PASSWORD", "DOLT_REMOTE_USER", "DOLT_SERVER_HOST", "DOLT_SERVER_PORT"} {
		t.Setenv(key, "")
	}
}

func idsOf(cases []Case) []string {
	result := make([]string, len(cases))
	for index, test := range cases {
		result[index] = test.ID
	}
	return result
}
