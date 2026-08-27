package tracker_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestWorkDefinitionObservationRoundTripsAndDefensivelyCopies(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	criteria := []tracker.WorkCriterion{{ID: "criterion-build", Text: "The binary builds", Outcome: "a reproducible binary is produced", Verifier: tracker.VerifierTest, Required: true}}
	dependencies := []ids.WorkID{"work-foundation"}
	scopes := []tracker.WriteScope{{Path: "internal/tracker", Domain: "tracker", Access: tracker.ScopeWriteExclusive}}
	provenance := []tracker.ScopeRef{{ID: "plan-provider-boundary", Kind: tracker.ScopePlanConcern}, {ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome}}
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"portable":{"key":"value"}}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-kitty", WorkID: "work-provider", Title: "Build provider boundary",
		Description: "Implement the complete provider-neutral planning boundary.", DesignContext: "Use stable internal identity and opaque provider evidence.",
		Criteria: criteria, Dependencies: dependencies, WriteScopes: scopes, Provenance: provenance,
		MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, ProviderMetadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := tracker.NewProviderObservation("fake-work-provider", []byte("canonical-provider-record"), 7, "digest", "opaque-token", now)
	if err != nil {
		t.Fatal(err)
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project-kitty", WorkID: "work-provider", Provider: "durable_fake", ExternalID: "fake-work-provider", MetadataSchema: 1, CreatedByOperation: "operation-create", LastObservation: 7}
	observed, err := tracker.NewWorkDefinitionObservation(definition, reference, observation, now)
	if err != nil {
		t.Fatal(err)
	}

	criteria[0].Outcome = "mutated"
	dependencies[0] = "mutated"
	scopes[0].Path = "mutated"
	provenance[0].ID = "mutated"
	if got := observed.Definition().Criteria()[0].Outcome; got != "a reproducible binary is produced" {
		t.Fatalf("criterion aliased caller memory: %q", got)
	}
	if got := observed.Definition().Dependencies()[0]; got != "work-foundation" {
		t.Fatalf("dependency aliased caller memory: %q", got)
	}
	returned := observed.Definition().WriteScopes()
	returned[0].Path = "also-mutated"
	if got := observed.Definition().WriteScopes()[0].Path; got != "internal/tracker" {
		t.Fatalf("write scope aliased returned memory: %q", got)
	}
	if !bytes.Contains(observed.CanonicalData(), []byte(`"work_id":"work-provider"`)) || !bytes.Contains(observed.CanonicalData(), []byte(`"provider_revision":7`)) {
		t.Fatalf("canonical observation omits identity: %s", observed.CanonicalData())
	}
}

func TestWorkDefinitionRejectsInvalidStructuredValues(t *testing.T) {
	valid := tracker.WorkDefinitionParams{
		ProjectID: "project-kitty", WorkID: "work-provider", Title: "Build provider boundary",
		Description: "Implement provider-neutral planning.", DesignContext: "Stable identity is authoritative.",
		Criteria:    []tracker.WorkCriterion{{ID: "criterion-1", Text: "Portable result", Outcome: "same semantic record", Verifier: tracker.VerifierTest, Required: true}},
		WriteScopes: []tracker.WriteScope{{Path: "internal/tracker", Domain: "tracker", Access: tracker.ScopeWriteExclusive}},
		Provenance:  []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}}, MetadataSchema: 1,
	}
	tests := []struct {
		name   string
		mutate func(*tracker.WorkDefinitionParams)
	}{
		{"empty design context", func(p *tracker.WorkDefinitionParams) { p.DesignContext = "" }},
		{"duplicate criterion", func(p *tracker.WorkDefinitionParams) { p.Criteria = append(p.Criteria, p.Criteria[0]) }},
		{"duplicate dependency", func(p *tracker.WorkDefinitionParams) { p.Dependencies = []ids.WorkID{"work-a", "work-a"} }},
		{"invalid write scope", func(p *tracker.WorkDefinitionParams) { p.WriteScopes[0].Path = "../private" }},
		{"unknown metadata schema", func(p *tracker.WorkDefinitionParams) { p.MetadataSchema = 99 }},
		{"duplicate provenance", func(p *tracker.WorkDefinitionParams) { p.Provenance = append(p.Provenance, p.Provenance[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Criteria = append([]tracker.WorkCriterion(nil), valid.Criteria...)
			candidate.WriteScopes = append([]tracker.WriteScope(nil), valid.WriteScopes...)
			candidate.Provenance = append([]tracker.ScopeRef(nil), valid.Provenance...)
			test.mutate(&candidate)
			if _, err := tracker.NewWorkDefinition(candidate); err == nil {
				t.Fatal("invalid work definition accepted")
			}
		})
	}
}

func TestWorkDefinitionObservationRejectsLookAlikeProviderIdentity(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-kitty", WorkID: "work-provider", Title: "Build provider boundary", Description: "Complete description", DesignContext: "Complete context",
		Criteria:    []tracker.WorkCriterion{{ID: "criterion-1", Text: "Portable result", Outcome: "same semantic record", Verifier: tracker.VerifierTest, Required: true}},
		WriteScopes: []tracker.WriteScope{{Path: "internal/tracker", Domain: "tracker", Access: tracker.ScopeWriteExclusive}},
		Provenance:  []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}}, MetadataSchema: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	providerObservation, err := tracker.NewProviderObservation("external-other", []byte("canonical"), 1, "", "token", now)
	if err != nil {
		t.Fatal(err)
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project-kitty", WorkID: "work-provider", Provider: "lookalike", ExternalID: "external-provider", MetadataSchema: 1, LastObservation: 1}
	if _, err := tracker.NewWorkDefinitionObservation(definition, reference, providerObservation, now); err == nil {
		t.Fatal("mismatched provider observation accepted")
	}
}
