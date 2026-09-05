package scenario

import (
	"encoding/json"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
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
