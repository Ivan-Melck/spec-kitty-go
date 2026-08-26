// Package conformance defines provider-neutral tracker contract tests.
//
// The registry is intentionally independent of Go test names. Provider
// qualification supplies a Factory and receives a deterministic census; it
// cannot hide an unsupported safety guarantee with t.Skip.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// Factory creates one isolated provider fixture for one stable case ID. The
// case ID lets deterministic subprocess fixtures predeclare an exact command
// script without sharing mutable provider state across cases.
type Factory func(context.Context, string, string) (Fixture, error)

// Fixture exposes only portable ports and observations. MutationCount must
// include every durable provider mutation and is used to prove refusal before
// mutation.
type Fixture struct {
	Capabilities  tracker.ProviderCapabilities
	Reference     tracker.ExternalWorkRef
	Catalog       tracker.Catalog
	Graph         tracker.Graph
	Discussion    tracker.Discussion
	Assignment    tracker.AssignmentProjection
	Diagnostic    DiagnosticExpectation
	MutationCount func(context.Context) (int, error)
	Close         func() error
}

// DiagnosticExpectation is provider state, not provider behavior. The shared
// case owns the public operation and every assertion.
type DiagnosticExpectation struct {
	Reference     tracker.ExternalWorkRef
	Kind          tracker.ErrorKind
	RecoveryCode  string
	ProviderToken string
	Assignee      string
	Marker        string
}

type Disposition string

const (
	Passed        Disposition = "passed"
	Failed        Disposition = "failed"
	NotApplicable Disposition = "not_applicable"
)

type Result struct {
	ID                    string      `json:"id"`
	Disposition           Disposition `json:"disposition"`
	CapabilityDisposition string      `json:"capability_disposition"`
	DiagnosticKind        string      `json:"diagnostic_kind,omitempty"`
	RecoveryCode          string      `json:"recovery_code,omitempty"`
	Operation             string      `json:"operation,omitempty"`
	MutationOutcome       string      `json:"mutation_outcome,omitempty"`
	RetrySafety           string      `json:"retry_safety,omitempty"`
	UniversalRequired     bool        `json:"universal_required"`
	UniversalStarted      bool        `json:"universal_started"`
	UniversalPassed       bool        `json:"universal_passed"`
	StaleProbeRequired    bool        `json:"stale_probe_required"`
	StaleProbeStarted     bool        `json:"stale_probe_started"`
	StaleProbePassed      bool        `json:"stale_probe_passed"`
}

type Census struct {
	Expected      []string `json:"expected"`
	Started       []string `json:"started"`
	Terminal      []string `json:"terminal"`
	Passed        []string `json:"passed"`
	Failed        []string `json:"failed"`
	NotApplicable []string `json:"not_applicable"`
	Results       []Result `json:"results"`
}

type Case struct {
	ID        string
	Supported func(tracker.ProviderCapabilities) bool
	Exercise  func(context.Context, Fixture, bool) error
}

// DefaultCases is the closed portable registry used by fake, dishonest, and
// real-provider qualification. Callers may copy but cannot mutate the source.
func DefaultCases() []Case {
	cases := []Case{
		{ID: "validation.capabilities.complete", Supported: func(tracker.ProviderCapabilities) bool { return true }, Exercise: exerciseCapabilities},
		{ID: "diagnostic.unavailable.typed", Supported: func(tracker.ProviderCapabilities) bool { return true }, Exercise: exerciseUnavailable},
		{ID: "observation.stale.not-normalized", Supported: func(tracker.ProviderCapabilities) bool { return true }, Exercise: exerciseStaleObservation},
		{ID: "safety.wrong-state.refused", Supported: func(tracker.ProviderCapabilities) bool { return true }, Exercise: exerciseWrongState},
	}
	cases = append(cases, catalogCases()...)
	cases = append(cases, graphCases()...)
	cases = append(cases, discussionCases()...)
	cases = append(cases, assignmentCases()...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return append([]Case(nil), cases...)
}

func exerciseUnavailable(ctx context.Context, fixture Fixture, _ bool) error {
	_, err := fixture.Catalog.ReadWork(ctx, fixture.Diagnostic.Reference)
	if !tracker.IsKind(err, fixture.Diagnostic.Kind) {
		return fmt.Errorf("unavailable diagnostic=%v", err)
	}
	var typed *tracker.Error
	if !errors.As(err, &typed) || typed.RecoveryAction() != fixture.Diagnostic.RecoveryCode {
		return fmt.Errorf("unavailable recovery=%v", err)
	}
	return nil
}

func exerciseStaleObservation(ctx context.Context, fixture Fixture, _ bool) error {
	record, err := fixture.Catalog.ReadWork(ctx, fixture.Diagnostic.Reference)
	if err != nil {
		return err
	}
	if record.Observation.ProviderToken != fixture.Diagnostic.ProviderToken || record.VisibleAssignee != fixture.Diagnostic.Assignee || record.AssignmentMarker != fixture.Diagnostic.Marker {
		return fmt.Errorf("stale provider response was normalized")
	}
	return nil
}

func exerciseWrongState(ctx context.Context, fixture Fixture, _ bool) error {
	_, err := fixture.Catalog.ReadWork(ctx, fixture.Diagnostic.Reference)
	if !tracker.IsKind(err, fixture.Diagnostic.Kind) {
		return fmt.Errorf("wrong-state diagnostic=%v", err)
	}
	return nil
}

func exerciseCapabilities(ctx context.Context, fixture Fixture, _ bool) error {
	if err := fixture.Capabilities.Validate(); err != nil {
		return err
	}
	for _, capability := range []tracker.CapabilityName{
		tracker.CapabilityCatalogRead, tracker.CapabilityCatalogCreate, tracker.CapabilityCatalogUpdate,
		tracker.CapabilityBlockingGraph, tracker.CapabilityDiscussion, tracker.CapabilityAssignment,
		tracker.CapabilityObservationRevision, tracker.CapabilitySafeCompensation,
	} {
		err := fixture.Capabilities.Require(capability)
		if err == nil {
			continue
		}
		if !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) && !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) {
			return fmt.Errorf("capability=%s diagnostic=%v", capability, err)
		}
	}
	if fixture.Catalog == nil {
		return fmt.Errorf("catalog port is absent")
	}
	if _, err := fixture.Catalog.ReadWork(ctx, fixture.Reference); err != nil {
		return fmt.Errorf("declared catalog read failed: %w", err)
	}
	return nil
}

func Run(ctx context.Context, root string, factory Factory, cases []Case) (Census, error) {
	if factory == nil {
		return Census{}, fmt.Errorf("conformance: provider factory is required")
	}
	if len(cases) == 0 {
		return Census{}, fmt.Errorf("conformance: case registry is empty")
	}
	seen := make(map[string]struct{}, len(cases))
	for _, test := range cases {
		if test.ID == "" || test.Supported == nil || test.Exercise == nil {
			return Census{}, fmt.Errorf("conformance: incomplete registered case %q", test.ID)
		}
		if _, duplicate := seen[test.ID]; duplicate {
			return Census{}, fmt.Errorf("conformance: duplicate case id %q", test.ID)
		}
		seen[test.ID] = struct{}{}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Census{}, fmt.Errorf("conformance: create fixture root: %w", err)
	}
	census := Census{}
	for _, test := range cases {
		census.Expected = append(census.Expected, test.ID)
		census.Started = append(census.Started, test.ID)
		caseRoot := filepath.Join(root, test.ID)
		fixture, err := factory(ctx, caseRoot, test.ID)
		if err != nil {
			return census, fmt.Errorf("conformance: create fixture case=%s: %w", test.ID, err)
		}
		if err := fixture.Capabilities.Validate(); err != nil {
			if fixture.Close != nil {
				_ = fixture.Close()
			}
			return census, fmt.Errorf("conformance: invalid capability report case=%s: %w", test.ID, err)
		}
		if fixture.MutationCount == nil {
			if fixture.Close != nil {
				_ = fixture.Close()
			}
			return census, fmt.Errorf("conformance: mutation census is required case=%s", test.ID)
		}
		before, err := fixture.MutationCount(ctx)
		if err != nil {
			return census, fmt.Errorf("conformance: %s mutation pre-observation: %w", test.ID, err)
		}
		supported := test.Supported(fixture.Capabilities)
		result := Result{ID: test.ID, CapabilityDisposition: "unsupported"}
		if supported {
			result.CapabilityDisposition = "supported"
		}
		err = test.Exercise(ctx, fixture, supported)
		after, countErr := fixture.MutationCount(ctx)
		if countErr != nil && err == nil {
			err = fmt.Errorf("mutation post-observation: %w", countErr)
		}
		if !supported && after != before && err == nil {
			err = fmt.Errorf("unsupported guarantee mutated provider: before=%d after=%d", before, after)
		}
		if fixture.Close != nil {
			if closeErr := fixture.Close(); closeErr != nil {
				if err == nil {
					err = fmt.Errorf("fixture cleanup: %w", closeErr)
				} else {
					err = errors.Join(err, closeErr)
				}
			}
		}
		if err != nil {
			result.Disposition = Failed
			if typed, ok := err.(*tracker.Error); ok {
				result.DiagnosticKind = string(typed.Kind)
				result.RecoveryCode = typed.RecoveryAction()
			}
			census.Failed = append(census.Failed, test.ID)
		} else {
			result.Disposition = Passed
			census.Passed = append(census.Passed, test.ID)
		}
		census.Terminal = append(census.Terminal, test.ID)
		census.Results = append(census.Results, result)
		if err != nil {
			return census, fmt.Errorf("conformance: case=%s: %w", test.ID, err)
		}
	}
	if err := ValidateCensus(census); err != nil {
		return census, err
	}
	return census, nil
}

func ValidateCensus(census Census) error {
	for _, list := range []*[]string{&census.Expected, &census.Started, &census.Terminal, &census.Passed, &census.Failed, &census.NotApplicable} {
		sort.Strings(*list)
		if duplicate(*list) != "" {
			return fmt.Errorf("conformance census: duplicate id %q", duplicate(*list))
		}
	}
	if !equalStrings(census.Expected, census.Started) || !equalStrings(census.Expected, census.Terminal) {
		return fmt.Errorf("conformance census: expected, started, and terminal ids differ")
	}
	terminal := append(append([]string(nil), census.Passed...), census.Failed...)
	terminal = append(terminal, census.NotApplicable...)
	sort.Strings(terminal)
	if !equalStrings(census.Expected, terminal) {
		return fmt.Errorf("conformance census: terminal dispositions are incomplete or unexpected")
	}
	if len(census.Results) != len(census.Expected) {
		return fmt.Errorf("conformance census: result count=%d expected=%d", len(census.Results), len(census.Expected))
	}
	resultIDs := make([]string, 0, len(census.Results))
	dispositions := make(map[string]Disposition, len(census.Expected))
	for _, id := range census.Passed {
		dispositions[id] = Passed
	}
	for _, id := range census.Failed {
		dispositions[id] = Failed
	}
	for _, id := range census.NotApplicable {
		dispositions[id] = NotApplicable
	}
	for _, result := range census.Results {
		if result.ID == "" {
			return fmt.Errorf("conformance census: result id is absent")
		}
		resultIDs = append(resultIDs, result.ID)
		if result.Disposition != Passed && result.Disposition != Failed && result.Disposition != NotApplicable {
			return fmt.Errorf("conformance census: result=%s has no terminal disposition", result.ID)
		}
		if dispositions[result.ID] != result.Disposition {
			return fmt.Errorf("conformance census: result=%s disposition disagrees with terminal census", result.ID)
		}
		if result.UniversalRequired != result.StaleProbeRequired {
			return fmt.Errorf("conformance census: result=%s authority and stale applicability differ", result.ID)
		}
		if result.UniversalRequired && (!result.UniversalStarted || !result.UniversalPassed || !result.StaleProbeStarted || !result.StaleProbePassed) {
			return fmt.Errorf("conformance census: result=%s required authority evidence is incomplete", result.ID)
		}
		if !result.UniversalRequired && (result.UniversalStarted || result.UniversalPassed || result.StaleProbeStarted || result.StaleProbePassed) {
			return fmt.Errorf("conformance census: result=%s records an inapplicable authority pass", result.ID)
		}
	}
	sort.Strings(resultIDs)
	if duplicate(resultIDs) != "" || !equalStrings(census.Expected, resultIDs) {
		return fmt.Errorf("conformance census: result ids are duplicate, missing, or unexpected")
	}
	return nil
}

func WriteCensus(path string, census Census) error {
	if err := ValidateCensus(census); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(census, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func duplicate(values []string) string {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return values[i]
		}
	}
	return ""
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func expectKind(err error, kinds ...tracker.ErrorKind) error {
	for _, kind := range kinds {
		if tracker.IsKind(err, kind) {
			return nil
		}
	}
	return fmt.Errorf("expected typed refusal %v, got %v", kinds, err)
}
