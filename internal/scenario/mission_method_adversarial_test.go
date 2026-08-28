package scenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const missionMethodDriftSeed int64 = 0x5eec0de

func TestMissionMethodQualificationContract(t *testing.T) {
	if missionMethodDriftSeed == 0 {
		t.Fatal("deterministic qualification seed is absent")
	}
	for _, field := range []workgraph.SemanticField{
		workgraph.FieldPlanningSourceCommit, workgraph.FieldMissionDefinition, workgraph.FieldWorkIdentity,
		workgraph.FieldTitle, workgraph.FieldDescription, workgraph.FieldDesignContext,
		workgraph.FieldCriterionIdentity, workgraph.FieldCriterionText, workgraph.FieldCriterionVerifier,
		workgraph.FieldCriterionRequired, workgraph.FieldCriterionMetadata, workgraph.FieldDependencies,
		workgraph.FieldWriteScopes, workgraph.FieldProviderMapping, workgraph.FieldLaneProjection,
	} {
		if field == "" {
			t.Fatal("semantic comparator catalog contains an unnamed field")
		}
	}
}

func TestMissionMethodSemanticDriftMutations1000(t *testing.T) {
	portable := runPortableMissionFixture(t, "drift-authority")
	// Generation one is the exact persisted two-item revision whose semantics
	// are being attacked. Generation two is the mission's current pointer; both
	// must remain byte-for-byte unchanged until an explicit reconciliation.
	base := portable.GenerationOne
	baseBytes, err := base.Encode()
	if err != nil {
		t.Fatal(err)
	}
	currentBytes, err := portable.GenerationTwo.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), portable.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	current := portable.MissionTwo
	baselineMission, err := store.LoadMission(context.Background(), current.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	baselineRows := missionMethodRowCounts(t, portable.Database)
	baselinePersistence := qualificationPersistenceSnapshot(t, portable.Database, current.MissionID)
	mutatedPersistence := baselinePersistence
	mutatedPersistence.Revisions = append([]qualificationRevisionRow(nil), baselinePersistence.Revisions...)
	mutatedPersistence.Revisions[0].EncodedRevision = append([]byte(nil), mutatedPersistence.Revisions[0].EncodedRevision...)
	mutatedPersistence.Revisions[0].EncodedRevision[0] ^= 1
	if reflect.DeepEqual(baselinePersistence, mutatedPersistence) {
		t.Fatal("persisted revision mutation control did not change authority snapshot")
	}

	mutants := qualificationDriftMutants()
	fieldCensus := make(map[workgraph.SemanticField]int)
	mutantCensus := make(map[string]int)
	random := rand.New(rand.NewSource(missionMethodDriftSeed))
	for index := 0; index < 1000; index++ {
		mutant := mutants[index%len(mutants)]
		params := qualificationRevisionParams(base)
		params.ID = ids.WorkRevisionID(fmt.Sprintf("revision-qualification-%04d", index))
		mutant.mutate(&params, index+1)
		params.Lanes = qualificationLanes(t, params)
		candidate, buildErr := workgraph.NewRevisionV2(params)
		if buildErr != nil {
			t.Fatalf("mutation=%d kind=%s build: %v", index, mutant.name, buildErr)
		}
		observedAt := time.Date(2026, 8, 28, 18, 0, 0, index, time.UTC)
		report, detectErr := workgraph.DetectDrift(base, candidate, observedAt)
		if detectErr != nil || report.SemanticStatus() != workgraph.SemanticChanged || len(report.Changes()) == 0 || !qualificationReportHasField(report, mutant.field) {
			t.Fatalf("mutation=%d kind=%s field=%s status=%s changes=%d err=%v", index, mutant.name, mutant.field, report.SemanticStatus(), len(report.Changes()), detectErr)
		}
		if len(report.AffectedOldWorkIDs()) == 0 && len(report.AffectedNewWorkIDs()) == 0 {
			t.Fatalf("mutation=%d has no bounded affected set", index)
		}
		for _, change := range report.Changes() {
			if len(change.ID()) != 64 || len(change.OldCommitment()) != 64 || len(change.NewCommitment()) != 64 {
				t.Fatalf("mutation=%d emitted unbounded change identity", index)
			}
		}
		fieldCensus[mutant.field]++
		mutantCensus[mutant.name]++
		observed, loadErr := store.LoadMission(context.Background(), current.MissionID)
		if loadErr != nil || !reflect.DeepEqual(observed, baselineMission) || missionMethodRowCounts(t, portable.Database) != baselineRows || !reflect.DeepEqual(qualificationPersistenceSnapshot(t, portable.Database, current.MissionID), baselinePersistence) {
			t.Fatalf("pure drift mutation=%d changed current authority: observed=%+v err=%v", index, observed, loadErr)
		}
		// Exercise deterministic input-order normalization without letting the
		// generated order become authority.
		if random.Intn(2) == 0 {
			params.WorkItems = reversed(params.WorkItems)
			params.ProviderObservations = reversed(params.ProviderObservations)
			params.Lanes = reversed(params.Lanes)
			normalized, normalizeErr := workgraph.NewRevisionV2(params)
			if normalizeErr != nil {
				t.Fatalf("mutation=%d shuffled build: %v", index, normalizeErr)
			}
			normalizedBytes, _ := normalized.Encode()
			candidateBytes, _ := candidate.Encode()
			if !bytes.Equal(candidateBytes, normalizedBytes) {
				t.Fatalf("mutation=%d input order changed canonical revision", index)
			}
		}
	}
	for _, mutant := range mutants {
		if fieldCensus[mutant.field] == 0 || mutantCensus[mutant.name] == 0 {
			t.Fatalf("deleted comparator mutant survived: %s/%s", mutant.name, mutant.field)
		}
	}
	afterBytes, _ := base.Encode()
	if !bytes.Equal(baseBytes, afterBytes) {
		t.Fatal("drift campaign mutated immutable generation-one bytes")
	}
	afterCurrent, _ := portable.GenerationTwo.Encode()
	if !bytes.Equal(currentBytes, afterCurrent) {
		t.Fatal("drift campaign mutated current generation bytes")
	}

	noiseParams := qualificationRevisionParams(base)
	noiseParams.ID = "revision-qualification-noise"
	for index := range noiseParams.ProviderObservations {
		noiseParams.ProviderObservations[index].Revision = fmt.Sprintf("provider-noise-%d", index)
	}
	noise, err := workgraph.NewRevisionV2(noiseParams)
	if err != nil {
		t.Fatal(err)
	}
	noiseReport, err := workgraph.DetectDrift(base, noise, time.Date(2026, 8, 28, 19, 0, 0, 0, time.UTC))
	if err != nil || noiseReport.SemanticStatus() != workgraph.SemanticObservationOnly || len(noiseReport.Changes()) != 0 {
		t.Fatalf("provider revision noise became semantic drift: status=%s err=%v", noiseReport.SemanticStatus(), err)
	}
}

func TestMissionMethodNormalizationAndPureQueries100(t *testing.T) {
	want := qualificationRevision(t, qualificationRevisionSpec{}, "revision-normalized", 1)
	wantBytes, _ := want.Encode()
	for trial := 0; trial < 100; trial++ {
		got := qualificationRevision(t, qualificationRevisionSpec{reverse: trial%2 == 1}, "revision-normalized", 1)
		gotBytes, err := got.Encode()
		if err != nil || !bytes.Equal(gotBytes, wantBytes) || got.GraphDigest() != want.GraphDigest() || got.CriteriaDigest() != want.CriteriaDigest() {
			t.Fatalf("normalization trial=%d digest=%s/%s err=%v", trial, got.GraphDigest(), want.GraphDigest(), err)
		}
	}
	portable := runPortableMissionFixture(t, "pure-next-100")
	before := missionMethodRowCounts(t, portable.Database)
	for trial := 0; trial < 100; trial++ {
		result := callMethod(t, context.Background(), "mission", "next", map[string]any{
			"database": portable.Database, "correlation_id": fmt.Sprintf("correlation-pure-next-%03d", trial), "mission_id": portable.MissionTwo.MissionID,
		})
		if result.OperationID != "" || result.Kind != "mission_next" {
			t.Fatalf("pure next trial=%d envelope=%+v", trial, result)
		}
		if missionMethodRowCounts(t, portable.Database) != before {
			t.Fatalf("pure next trial=%d wrote authority", trial)
		}
	}
}

type qualificationDriftMutant struct {
	name   string
	field  workgraph.SemanticField
	mutate func(*workgraph.RevisionV2Params, int)
}

func qualificationDriftMutants() []qualificationDriftMutant {
	return []qualificationDriftMutant{
		{"title", workgraph.FieldTitle, func(p *workgraph.RevisionV2Params, i int) { p.WorkItems[0].Title = fmt.Sprintf("Changed title %d", i) }},
		{"description", workgraph.FieldDescription, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].Description = fmt.Sprintf("Changed description %d.", i)
		}},
		{"design", workgraph.FieldDesignContext, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].DesignContext = fmt.Sprintf("Changed design %d.", i)
		}},
		{"criterion-id", workgraph.FieldCriterionIdentity, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].Criteria[0].ID = fmt.Sprintf("criterion-a-%d", i)
		}},
		{"criterion-text", workgraph.FieldCriterionText, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].Criteria[0].Text = fmt.Sprintf("Run changed test %d.", i)
		}},
		{"criterion-verifier", workgraph.FieldCriterionVerifier, func(p *workgraph.RevisionV2Params, _ int) {
			p.WorkItems[0].Criteria[0].VerifierKind = tracker.VerifierReview.String()
		}},
		{"criterion-required", workgraph.FieldCriterionRequired, func(p *workgraph.RevisionV2Params, _ int) {
			p.WorkItems[0].Criteria[0].Required = !p.WorkItems[0].Criteria[0].Required
		}},
		{"criterion-outcome", workgraph.FieldCriterionMetadata, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].Criteria[0].Outcome = fmt.Sprintf("Changed outcome %d.", i)
		}},
		{"criterion-evidence", workgraph.FieldCriterionMetadata, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].Criteria[0].EvidenceHint = fmt.Sprintf("changed evidence %d", i)
		}},
		{"metadata-schema", workgraph.FieldCriterionMetadata, func(p *workgraph.RevisionV2Params, i int) {
			if p.WorkItems[0].Criteria[0].Metadata == nil {
				p.WorkItems[0].Criteria[0].Metadata = map[string]any{}
			}
			p.WorkItems[0].Criteria[0].Metadata["provider_schema"] = i + 1
		}},
		{"provider-canonical-content", workgraph.FieldDescription, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[1].Description = fmt.Sprintf("Provider canonical content %d.", i)
		}},
		{"dependency", workgraph.FieldDependencies, func(p *workgraph.RevisionV2Params, _ int) { p.WorkItems[1].Dependencies = nil }},
		{"scope", workgraph.FieldWriteScopes, func(p *workgraph.RevisionV2Params, i int) {
			p.WorkItems[0].WriteScopes[0].Path = fmt.Sprintf("internal/changed-%d", i)
		}},
		{"provider", workgraph.FieldProviderMapping, func(p *workgraph.RevisionV2Params, i int) {
			p.ProviderObservations[0].Provider = fmt.Sprintf("provider-%d", i)
		}},
		{"external-mapping", workgraph.FieldProviderMapping, func(p *workgraph.RevisionV2Params, i int) {
			p.ProviderObservations[0].ExternalID = fmt.Sprintf("external-a-%d", i)
		}},
		{"planning-source", workgraph.FieldPlanningSourceCommit, func(p *workgraph.RevisionV2Params, i int) { p.PlanningSourceCommit = fmt.Sprintf("%040x", i+1) }},
		{"mission-definition", workgraph.FieldMissionDefinition, func(p *workgraph.RevisionV2Params, i int) { p.MissionDefinition.Digest = fmt.Sprintf("%064x", i+1) }},
		{"work-addition", workgraph.FieldWorkIdentity, addQualificationWork},
		{"work-removal", workgraph.FieldWorkIdentity, removeQualificationWork},
		{"work-replacement", workgraph.FieldWorkIdentity, replaceQualificationWork},
		{"lane-projection", workgraph.FieldLaneProjection, func(p *workgraph.RevisionV2Params, _ int) {
			p.WorkItems[1].WriteScopes[0] = p.WorkItems[0].WriteScopes[0]
		}},
	}
}

func qualificationRevisionParams(revision workgraph.RevisionV2) workgraph.RevisionV2Params {
	items := make([]workgraph.WorkItemV2Params, 0, len(revision.WorkItems()))
	for _, item := range revision.WorkItems() {
		criteria := make([]workgraph.CriterionV2Params, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			metadata := criterion.Metadata()
			outcome, _ := metadata[workgraph.CriterionMetadataOutcome].(string)
			evidence, _ := metadata[workgraph.CriterionMetadataEvidenceHint].(string)
			delete(metadata, workgraph.CriterionMetadataOutcome)
			delete(metadata, workgraph.CriterionMetadataEvidenceHint)
			criteria = append(criteria, workgraph.CriterionV2Params{
				ID: criterion.ID(), Text: criterion.Text(), VerifierKind: criterion.VerifierKind(), Required: criterion.Required(),
				Outcome: outcome, EvidenceHint: evidence, Metadata: metadata,
			})
		}
		items = append(items, workgraph.WorkItemV2Params{
			WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(),
			Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: item.WriteScopes(),
		})
	}
	return workgraph.RevisionV2Params{
		ID: revision.ID(), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), Generation: revision.Generation(),
		PredecessorID: revision.PredecessorID(), PlanningSourceCommit: revision.PlanningSourceCommit(), MissionDefinition: revision.MissionDefinition(),
		DependencyPolicy: revision.DependencyPolicy(), WorkItems: items, Lanes: revision.Lanes(), ProviderObservations: revision.ProviderObservations(),
		FinalizedBy: revision.FinalizedBy(), FinalizedAt: revision.FinalizedAt(), GovernanceContextID: revision.GovernanceContextID(),
	}
}

func qualificationLanes(t *testing.T, params workgraph.RevisionV2Params) []workgraph.LaneV2 {
	t.Helper()
	work := make([]workgraph.WorkObservation, 0, len(params.WorkItems))
	for _, item := range params.WorkItems {
		criteria := make([]workgraph.CriterionObservation, 0, len(item.Criteria))
		for _, criterion := range item.Criteria {
			requirement := workgraph.CriterionOptional
			if criterion.Required {
				requirement = workgraph.CriterionRequired
			}
			criteria = append(criteria, workgraph.CriterionObservation{
				ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: criterion.VerifierKind, Requirement: requirement,
			})
		}
		scopes := make([]workgraph.WriteScopeObservation, 0, len(item.WriteScopes))
		for _, scope := range item.WriteScopes {
			scopes = append(scopes, workgraph.WriteScopeObservation{Path: scope.Path, Domain: scope.Domain, Access: scope.Access, SerializationKey: scope.SerializationKey})
		}
		observed, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{
			ProjectID: params.ProjectID, WorkID: item.WorkID, Title: item.Title, Description: item.Description, DesignContext: item.DesignContext,
			Criteria: criteria, Dependencies: item.Dependencies, WriteScopes: scopes,
			Provenance: []workgraph.ScopeReference{{ID: "plan-core", Kind: workgraph.ScopePlanConcern}},
		})
		if err != nil {
			t.Fatalf("construct lane observation: %v", err)
		}
		work = append(work, observed)
	}
	draft, err := workgraph.NewDraftObservation(params.ProjectID, params.MissionID, "plan-qualification", strings.Repeat("d", 64), []workgraph.ApprovedScope{{ID: "plan-core", Kind: workgraph.ScopePlanConcern, MissionID: params.MissionID}}, work)
	if err != nil {
		t.Fatalf("construct lane draft: %v", err)
	}
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatalf("collapse qualification lanes: %v", err)
	}
	return lanes
}

func addQualificationWork(params *workgraph.RevisionV2Params, index int) {
	item := params.WorkItems[0]
	item.WorkID = ids.WorkID(fmt.Sprintf("work-added-%d", index))
	item.Title = fmt.Sprintf("Added work %d", index)
	item.Dependencies = nil
	item.Criteria[0].ID = fmt.Sprintf("criterion-added-%d", index)
	item.WriteScopes[0].Path = fmt.Sprintf("internal/added-%d", index)
	params.WorkItems = append(params.WorkItems, item)
	observation := params.ProviderObservations[0]
	observation.WorkID = item.WorkID
	observation.ExternalID = fmt.Sprintf("external-added-%d", index)
	params.ProviderObservations = append(params.ProviderObservations, observation)
}

func removeQualificationWork(params *workgraph.RevisionV2Params, _ int) {
	removed := params.WorkItems[len(params.WorkItems)-1].WorkID
	params.WorkItems = params.WorkItems[:len(params.WorkItems)-1]
	params.ProviderObservations = slicesDeleteWorkObservation(params.ProviderObservations, removed)
	for index := range params.WorkItems {
		params.WorkItems[index].Dependencies = slicesDeleteWorkID(params.WorkItems[index].Dependencies, removed)
	}
}

func replaceQualificationWork(params *workgraph.RevisionV2Params, index int) {
	old := params.WorkItems[len(params.WorkItems)-1].WorkID
	replacement := ids.WorkID(fmt.Sprintf("work-replacement-%d", index))
	params.WorkItems[len(params.WorkItems)-1].WorkID = replacement
	for itemIndex := range params.WorkItems {
		for dependencyIndex := range params.WorkItems[itemIndex].Dependencies {
			if params.WorkItems[itemIndex].Dependencies[dependencyIndex] == old {
				params.WorkItems[itemIndex].Dependencies[dependencyIndex] = replacement
			}
		}
	}
	for observationIndex := range params.ProviderObservations {
		if params.ProviderObservations[observationIndex].WorkID == old {
			params.ProviderObservations[observationIndex].WorkID = replacement
			params.ProviderObservations[observationIndex].ExternalID = fmt.Sprintf("external-replacement-%d", index)
		}
	}
}

func slicesDeleteWorkObservation(values []workgraph.ProviderObservationV2, workID ids.WorkID) []workgraph.ProviderObservationV2 {
	result := values[:0]
	for _, value := range values {
		if value.WorkID != workID {
			result = append(result, value)
		}
	}
	return result
}

func slicesDeleteWorkID(values []ids.WorkID, workID ids.WorkID) []ids.WorkID {
	result := values[:0]
	for _, value := range values {
		if value != workID {
			result = append(result, value)
		}
	}
	return result
}

func reversed[T any](values []T) []T {
	result := append([]T(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

type qualificationRevisionRow struct {
	ID, GraphDigest, CriteriaDigest string
	Generation                      uint64
	EncodedRevision                 []byte
}

type qualificationPersistence struct {
	MissionPointer string
	CurrentID      string
	Generation     uint64
	Revisions      []qualificationRevisionRow
}

func qualificationPersistenceSnapshot(t *testing.T, path string, missionID ids.MissionID) qualificationPersistence {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var result qualificationPersistence
	if err = database.QueryRow(`SELECT COALESCE(current_work_revision_id,'') FROM phase3_missions WHERE mission_id=?`, missionID).Scan(&result.MissionPointer); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT work_revision_id,generation FROM phase3_current_work_revisions WHERE mission_id=?`, missionID).Scan(&result.CurrentID, &result.Generation); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`SELECT work_revision_id,generation,graph_digest,criteria_digest,encoded_revision FROM phase3_work_revisions_v2 WHERE mission_id=? ORDER BY generation,work_revision_id`, missionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row qualificationRevisionRow
		if err = rows.Scan(&row.ID, &row.Generation, &row.GraphDigest, &row.CriteriaDigest, &row.EncodedRevision); err != nil {
			t.Fatal(err)
		}
		row.EncodedRevision = append([]byte(nil), row.EncodedRevision...)
		result.Revisions = append(result.Revisions, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

type qualificationRevisionSpec struct {
	title, description, design, criterionID, criterionText, outcome, evidence      string
	verifier                                                                       tracker.VerifierKind
	optional, noDependency, addWork, removeWork, replaceWork, mergeScopes, reverse bool
	scopePath, provider, externalA, planning, definitionDigest                     string
}

func qualificationRevision(t *testing.T, spec qualificationRevisionSpec, revisionID ids.WorkRevisionID, providerRevision tracker.Revision) workgraph.RevisionV2 {
	t.Helper()
	definitions := qualificationDefinitions(t, spec)
	observations := make([]tracker.WorkDefinitionObservation, 0, len(definitions))
	provider := spec.provider
	if provider == "" {
		provider = "durable_fake"
	}
	for index, definition := range definitions {
		externalID := "external-" + definition.WorkID().String()
		if definition.WorkID() == "work-a" && spec.externalA != "" {
			externalID = spec.externalA
		}
		canonical := definition.CanonicalData()
		digest := sha256.Sum256(canonical)
		providerObservation, err := tracker.NewProviderObservation(externalID, canonical, providerRevision+tracker.Revision(index), fmt.Sprintf("%x", digest), fmt.Sprintf("token-%d", index), time.Date(2026, 8, 28, 12, 0, index, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		reference := tracker.ExternalWorkRef{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Provider: provider, ExternalID: externalID, MetadataSchema: definition.MetadataSchema(), CreatedByOperation: ids.OperationID("operation-create-" + definition.WorkID().String()), LastObservation: providerObservation.Revision}
		observation, err := tracker.NewWorkDefinitionObservation(definition, reference, providerObservation, providerObservation.ObservedAt)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	if spec.reverse {
		for left, right := 0, len(observations)-1; left < right; left, right = left+1, right-1 {
			observations[left], observations[right] = observations[right], observations[left]
		}
	}
	finalization, err := dishonestFinalizationObservation("project-qualification", "mission-qualification", "artifact-plan-qualification", strings.Repeat("a", 64), observations)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	planning := spec.planning
	if planning == "" {
		planning = strings.Repeat("b", 40)
	}
	definitionDigest := definition.Commitment().String()
	if spec.definitionDigest != "" {
		definitionDigest = spec.definitionDigest
	}
	revision, err := workgraph.BuildGenerationOneCandidate(workgraph.GenerationOneCandidateParams{
		ID: revisionID, Observation: finalization, PlanningSourceCommit: planning,
		MissionDefinition: workgraph.MissionDefinitionIdentity{Key: definition.ID().String(), Version: uint64(definition.Version()), Digest: definitionDigest},
		DependencyPolicy:  workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-qualification",
		FinalizedAt: time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC), GovernanceContextID: "governance-qualification",
	})
	if err != nil {
		t.Fatalf("build qualification revision: %v", err)
	}
	return revision
}

func qualificationDefinitions(t *testing.T, spec qualificationRevisionSpec) []tracker.WorkDefinition {
	t.Helper()
	metadata, _ := tracker.NewOpaqueMetadata([]byte(`{}`))
	title := spec.title
	if title == "" {
		title = "Implement A"
	}
	description := spec.description
	if description == "" {
		description = "Implement the first bounded unit."
	}
	design := spec.design
	if design == "" {
		design = "Follow the approved plan."
	}
	criterionID := spec.criterionID
	if criterionID == "" {
		criterionID = "criterion-a"
	}
	criterionText := spec.criterionText
	if criterionText == "" {
		criterionText = "Run the unit test."
	}
	outcome := spec.outcome
	if outcome == "" {
		outcome = "The unit test passes."
	}
	evidence := spec.evidence
	if evidence == "" {
		evidence = "go test output"
	}
	verifier := spec.verifier
	if verifier == "" {
		verifier = tracker.VerifierTest
	}
	scopePath := spec.scopePath
	if scopePath == "" {
		scopePath = "internal/a"
	}
	makeDefinition := func(params tracker.WorkDefinitionParams) tracker.WorkDefinition {
		params.ProjectID, params.MetadataSchema, params.ProviderMetadata = "project-qualification", tracker.WorkDefinitionMetadataSchemaV1, metadata
		definition, err := tracker.NewWorkDefinition(params)
		if err != nil {
			t.Fatal(err)
		}
		return definition
	}
	a := makeDefinition(tracker.WorkDefinitionParams{WorkID: "work-a", Title: title, Description: description, DesignContext: design,
		Criteria:    []tracker.WorkCriterion{{ID: criterionID, Text: criterionText, Outcome: outcome, Verifier: verifier, Required: !spec.optional, EvidenceHint: evidence}},
		WriteScopes: []tracker.WriteScope{{Path: scopePath, Domain: "workgraph", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}})
	if spec.removeWork {
		return []tracker.WorkDefinition{a}
	}
	bID := ids.WorkID("work-b")
	if spec.replaceWork {
		bID = "work-c"
	}
	dependencies := []ids.WorkID{"work-a"}
	if spec.noDependency {
		dependencies = nil
	}
	bScope := "internal/b"
	if spec.mergeScopes {
		bScope = "internal/a/nested"
	}
	b := makeDefinition(tracker.WorkDefinitionParams{WorkID: bID, Title: "Implement B", Description: "Implement the dependent bounded unit.", DesignContext: "Consume the first unit.",
		Criteria: []tracker.WorkCriterion{{ID: "criterion-b", Text: "Review the behavior.", Outcome: "The behavior is approved.", Verifier: tracker.VerifierReview, Required: true, EvidenceHint: "review record"}}, Dependencies: dependencies,
		WriteScopes: []tracker.WriteScope{{Path: bScope, Domain: "mission", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}})
	result := []tracker.WorkDefinition{a, b}
	if spec.addWork {
		result = append(result, makeDefinition(tracker.WorkDefinitionParams{WorkID: "work-c", Title: "Implement C", Description: "Implement the added bounded unit.", DesignContext: "Remain independent.", Criteria: []tracker.WorkCriterion{{ID: "criterion-c", Text: "Run C.", Outcome: "C passes.", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test output"}}, WriteScopes: []tracker.WriteScope{{Path: "internal/c", Domain: "other", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}}))
	}
	return result
}

func qualificationReportHasField(report workgraph.DriftReport, field workgraph.SemanticField) bool {
	for _, change := range report.Changes() {
		if change.Field() == field {
			return true
		}
	}
	return false
}
