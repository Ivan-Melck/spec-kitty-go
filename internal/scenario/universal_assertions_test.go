package scenario

import (
	"encoding/json"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

func TestUniversalInvariantOracleControlledMutants(t *testing.T) {
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	h.createMission("project-oracle", "mission-oracle", "operation-oracle")
	status := h.host.Status()
	progress, err := h.host.Progress().Mission("project-oracle", "mission-oracle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateUniversalSnapshot(status, progress, "project-oracle", "mission-oracle", 0); err != nil {
		t.Fatalf("valid public snapshot refused: %v", err)
	}

	mutants := map[string]func(*productionruntime.HostStatus, *productionruntime.MissionProgress){
		"host_not_ready": func(status *productionruntime.HostStatus, _ *productionruntime.MissionProgress) {
			status.State = productionruntime.HostDegraded
		},
		"database_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.DatabaseID = "database-substituted"
		},
		"epoch_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.RuntimeEpochID = "epoch-substituted"
		},
		"projection_revision_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.ProjectionRevision++
		},
		"projection_digest_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.ProjectionSHA256 = "substituted"
		},
		"snapshot_digest_missing": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.SnapshotSHA256 = ""
		},
		"mission_scope_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.MissionID = "mission-substituted"
		},
		"incomplete_item": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items[0].AggregateID = ""
		},
		"duplicate_aggregate": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items = append(progress.Items, progress.Items[0])
		},
		"noncanonical_state": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items[0].CanonicalState = append(progress.Items[0].CanonicalState, ' ')
		},
		"publication_missing": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items[0].LastPublicationID = ""
		},
		"publication_malformed": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items[0].LastPublicationID = "not-a-publication"
		},
		"logical_version_disagreement": func(_ *productionruntime.HostStatus, progress *productionruntime.MissionProgress) {
			progress.Items[0].LogicalVersion++
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			mutantStatus := status
			mutantProgress := cloneMissionProgressForOracle(t, progress)
			mutate(&mutantStatus, &mutantProgress)
			if _, err := validateUniversalSnapshot(mutantStatus, mutantProgress, "project-oracle", "mission-oracle", 0); err == nil {
				t.Fatal("universal invariant mutant survived")
			}
		})
	}
}

func TestWorkCoordinateAgreementControlledMutants(t *testing.T) {
	valid := map[string]map[string]any{
		"execution_slot": {"work_revision_id": "revision-1", "run_id": "run-1", "lease_id": "lease-1", "generation": float64(1), "fence": "fence-1", "actor_id": "actor-implementer"},
		"review_slot":    {"work_revision_id": "revision-1", "run_id": "run-1", "reviewer_actor_id": "actor-reviewer"},
	}
	if err := validateWorkCoordinateAgreement("work-1", valid); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]map[string]map[string]any{
		"coordinate_substitution": cloneWorkStates(t, valid),
		"self_review":             cloneWorkStates(t, valid),
	}
	mutants["coordinate_substitution"]["review_slot"]["run_id"] = "run-2"
	mutants["self_review"]["review_slot"]["reviewer_actor_id"] = "actor-implementer"
	for name, states := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := validateWorkCoordinateAgreement("work-1", states); err == nil {
				t.Fatal("work-coordinate mutant survived")
			}
		})
	}
}

func TestTerminalSemanticEquivalenceControlledMutants(t *testing.T) {
	want := terminalSemanticProgress{
		Lifecycle: "completed", WorkRevisionID: "revision-1", Generation: 1, Complete: true, TotalWork: 1,
		WorkCounts:     []productionruntime.StateCount{{State: "completed", Count: 1}},
		Items:          []terminalSemanticItem{{Family: "mission", AggregateID: "mission-1", AuthorityState: "completed", SemanticSHA256: "state-sha", Fenced: true}},
		Families:       []string{"mission"},
		Tracker:        []terminalTrackerEvidence{{WorkID: "work-1", Revision: 2, Assignee: "actor-1", Marker: "operation-1"}},
		Repository:     repositoryTerminalEvidence{Targets: []string{"repository\x00main\x00result"}, Publications: []string{"effect\x00result"}},
		SemanticSHA256: "projection-sha",
		Operations:     []terminalOperationEvidence{{OperationID: "operation-1", ResultKind: "fixture.result", ResultBody: `{"ok":true}`, ResultSHA256: "result-sha"}},
	}
	if err := validateTerminalSemanticEquivalence(want, want); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*terminalSemanticProgress){
		"mission_summary":     func(value *terminalSemanticProgress) { value.Complete = false },
		"work_count":          func(value *terminalSemanticProgress) { value.WorkCounts[0].Count++ },
		"canonical_item":      func(value *terminalSemanticProgress) { value.Items[0].SemanticSHA256 = "substituted" },
		"fence_presence":      func(value *terminalSemanticProgress) { value.Items[0].Fenced = false },
		"family_coverage":     func(value *terminalSemanticProgress) { value.Families = nil },
		"tracker_attribution": func(value *terminalSemanticProgress) { value.Tracker[0].Marker = "operation-other" },
		"repository_result":   func(value *terminalSemanticProgress) { value.Repository.Targets[0] = "repository\x00main\x00other" },
		"projection_digest":   func(value *terminalSemanticProgress) { value.SemanticSHA256 = "other" },
		"operation_result":    func(value *terminalSemanticProgress) { value.Operations[0].ResultSHA256 = "other" },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			got := cloneTerminalSemanticProgress(t, want)
			mutate(&got)
			if err := validateTerminalSemanticEquivalence(want, got); err == nil {
				t.Fatal("terminal semantic mutant survived")
			}
		})
	}
}

func assertUniversalTerminalMembershipMutants(t *testing.T, status productionruntime.HostStatus, progress productionruntime.MissionProgress, project, missionID string, repositoryCalls int) {
	t.Helper()
	mutants := map[string]func(*productionruntime.MissionProgress){
		"effect_membership_omitted": func(value *productionruntime.MissionProgress) { value.Effects = nil },
		"review_membership_duplicated": func(value *productionruntime.MissionProgress) {
			value.Reviews = append(value.Reviews, value.Reviews[0])
		},
		"acceptance_membership_omitted": func(value *productionruntime.MissionProgress) { value.Acceptance = value.Acceptance[1:] },
		"integration_member_unbound": func(value *productionruntime.MissionProgress) {
			value.Integration[0].AggregateID = "attempt-substituted"
		},
	}
	ids := m2.PerformanceTerminalMembershipMutantIDsV1()
	if len(ids) != len(mutants) {
		t.Fatalf("terminal membership mutant census=%d implementations=%d", len(ids), len(mutants))
	}
	for _, name := range ids {
		mutate, ok := mutants[name]
		if !ok {
			t.Fatalf("terminal membership mutant %q has no implementation", name)
		}
		t.Run("terminal-membership-mutant/"+name, func(t *testing.T) {
			candidate := cloneMissionProgressForOracle(t, progress)
			mutate(&candidate)
			if _, err := validateUniversalSnapshot(status, candidate, project, missionID, repositoryCalls); err == nil {
				t.Fatal("terminal membership mutant survived")
			}
		})
	}
}

func cloneTerminalSemanticProgress(t *testing.T, value terminalSemanticProgress) terminalSemanticProgress {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone terminalSemanticProgress
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func cloneMissionProgressForOracle(t *testing.T, value productionruntime.MissionProgress) productionruntime.MissionProgress {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone productionruntime.MissionProgress
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func cloneWorkStates(t *testing.T, value map[string]map[string]any) map[string]map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
