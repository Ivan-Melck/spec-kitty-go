package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

type invariantEvidence struct {
	SchemaVersion      uint32                   `json:"schema_version"`
	Scenario           string                   `json:"scenario"`
	Cut                string                   `json:"cut"`
	DatabaseID         journalv2.DatabaseID     `json:"database_id"`
	RuntimeEpochID     journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	ProjectionRevision uint64                   `json:"projection_revision"`
	ProjectionSHA256   string                   `json:"projection_sha256"`
	Families           []string                 `json:"families"`
	AggregateCount     int                      `json:"aggregate_count"`
	OperationCount     int                      `json:"operation_count"`
	RepositoryCalls    int                      `json:"repository_calls"`
}

type authoritySnapshot struct {
	revision        uint64
	digest          string
	missionSnapshot string
	repositoryCalls int
}

// assertUniversalInvariants observes only the public readiness and progress
// views. The returned bytes are stable evidence suitable for campaign census
// records; they contain no process path or provider transcript.
func assertUniversalInvariants(t *testing.T, h *portableHarness, project, missionID, cut string) []byte {
	t.Helper()
	status := h.host.Status()
	if status.State != productionruntime.HostReady || status.DatabaseID == "" || status.RuntimeEpochID == "" || status.ProjectionRevision == 0 || status.ProjectionSHA256 == "" {
		t.Fatalf("scenario=%s cut=%s incomplete readiness: %+v", t.Name(), cut, status)
	}
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(project), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatalf("scenario=%s cut=%s progress: %v", t.Name(), cut, err)
	}
	if progress.SchemaVersion != productionruntime.ProgressSchemaVersion || progress.DatabaseID != status.DatabaseID || progress.RuntimeEpochID != status.RuntimeEpochID || progress.ProjectionRevision != status.ProjectionRevision || progress.ProjectionSHA256 != status.ProjectionSHA256 || progress.SnapshotSHA256 == "" {
		t.Fatalf("scenario=%s cut=%s readiness/progress disagreement: status=%+v progress=%+v", t.Name(), cut, status, progress)
	}
	seen := make(map[string]struct{}, len(progress.Items))
	families := make(map[string]struct{})
	work := make(map[string]map[string]map[string]any)
	operationCount := 0
	for _, item := range progress.Items {
		key := item.Family + "\x00" + item.AggregateID
		if item.Family == "" || item.AggregateID == "" || item.LogicalVersion == 0 || len(item.CanonicalState) == 0 {
			t.Fatalf("scenario=%s cut=%s incomplete authority item: %+v", t.Name(), cut, item)
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("scenario=%s cut=%s duplicate aggregate %q", t.Name(), cut, key)
		}
		seen[key] = struct{}{}
		families[item.Family] = struct{}{}
		canonical, canonicalErr := journalv2.CanonicalObject(item.CanonicalState)
		if canonicalErr != nil || !bytes.Equal(canonical, item.CanonicalState) {
			t.Fatalf("scenario=%s cut=%s noncanonical %s state: %v", t.Name(), cut, key, canonicalErr)
		}
		if item.LastPublicationID == "" {
			t.Fatalf("scenario=%s cut=%s authority item has no publication: %s", t.Name(), cut, key)
		}
		if _, decodeErr := journalv2.DecodePublicationID(item.LastPublicationID); decodeErr != nil {
			t.Fatalf("scenario=%s cut=%s invalid publication %s: %v", t.Name(), cut, key, decodeErr)
		}
		var state map[string]any
		if err := json.Unmarshal(item.CanonicalState, &state); err != nil {
			t.Fatalf("scenario=%s cut=%s decode %s: %v", t.Name(), cut, key, err)
		}
		if value, ok := uintField(state, "logical_version"); ok && value != item.LogicalVersion {
			t.Fatalf("scenario=%s cut=%s projection/head version mismatch %s: state=%d head=%d", t.Name(), cut, key, value, item.LogicalVersion)
		}
		if value, ok := textField(state, "project_id"); ok && value != project {
			t.Fatalf("scenario=%s cut=%s cross-project state %s: %s", t.Name(), cut, key, value)
		}
		if value, ok := textField(state, "mission_id"); ok && value != missionID {
			t.Fatalf("scenario=%s cut=%s cross-mission state %s: %s", t.Name(), cut, key, value)
		}
		if item.Family == "operation" {
			operation, decodeErr := journalv2.DecodeOperationStateV2(item.CanonicalState)
			if decodeErr != nil || string(operation.OperationID()) != item.AggregateID || uint64(operation.LogicalVersion()) != item.LogicalVersion || string(operation.LastPublicationID()) != item.LastPublicationID {
				t.Fatalf("scenario=%s cut=%s operation binding/result mismatch %s: state=%+v err=%v", t.Name(), cut, key, operation, decodeErr)
			}
			if operation.Status() == journalv2.StatusResolved || operation.Status() == journalv2.StatusRefused {
				if operation.ResultKind() == "" || operation.ResultSchemaVersion() == 0 || len(operation.Result()) == 0 || operation.ResultDigest().IsZero() {
					t.Fatalf("scenario=%s cut=%s terminal operation has no immutable result: %s", t.Name(), cut, key)
				}
			}
			operationCount++
		}
		if workID, ok := nestedTextField(state, "work_id"); ok {
			if work[workID] == nil {
				work[workID] = make(map[string]map[string]any)
			}
			work[workID][item.Family] = state
		}
	}
	for workID, byFamily := range work {
		assertWorkCoordinateAgreement(t, cut, workID, byFamily)
	}
	familyList := make([]string, 0, len(families))
	for family := range families {
		familyList = append(familyList, family)
	}
	sort.Strings(familyList)
	evidence := invariantEvidence{
		SchemaVersion: productionScenarioSchemaVersion, Scenario: t.Name(), Cut: cut,
		DatabaseID: status.DatabaseID, RuntimeEpochID: status.RuntimeEpochID,
		ProjectionRevision: status.ProjectionRevision, ProjectionSHA256: status.ProjectionSHA256,
		Families: familyList, AggregateCount: len(progress.Items), OperationCount: operationCount, RepositoryCalls: h.git.count(),
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip invariantEvidence
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.SchemaVersion != productionScenarioSchemaVersion || roundTrip.Scenario == "" || roundTrip.Cut == "" {
		t.Fatalf("invalid invariant evidence schema: %s err=%v", encoded, err)
	}
	return encoded
}

func assertWorkCoordinateAgreement(t *testing.T, cut, workID string, states map[string]map[string]any) {
	t.Helper()
	fields := []string{"work_revision_id", "run_id", "lease_id", "generation", "fence", "candidate", "policy_revision"}
	for _, field := range fields {
		var expected string
		for family, state := range states {
			value, ok := nestedScalarField(state, field)
			if !ok || value == "" {
				continue
			}
			if expected == "" {
				expected = value
				continue
			}
			if value != expected {
				t.Fatalf("cut=%s work=%s coordinate=%s disagreement in %s: want=%s got=%s", cut, workID, field, family, expected, value)
			}
		}
	}
	if executionState := states["execution_slot"]; executionState != nil {
		contributor, _ := nestedTextField(executionState, "actor_id")
		if reviewState := states["review_slot"]; reviewState != nil {
			reviewer, _ := nestedTextField(reviewState, "reviewer_actor_id")
			if contributor != "" && reviewer == contributor {
				t.Fatalf("cut=%s work=%s reviewer actor equals contributor actor %s", cut, workID, contributor)
			}
		}
	}
}

func textField(value map[string]any, name string) (string, bool) {
	text, ok := value[name].(string)
	return text, ok
}

func uintField(value map[string]any, name string) (uint64, bool) {
	number, ok := value[name].(float64)
	return uint64(number), ok && number >= 0 && number == float64(uint64(number))
}

func nestedTextField(value map[string]any, name string) (string, bool) {
	if text, ok := textField(value, name); ok {
		return text, true
	}
	for _, nestedName := range []string{"binding", "governance", "workspace"} {
		if nested, ok := value[nestedName].(map[string]any); ok {
			if text, found := nestedTextField(nested, name); found {
				return text, true
			}
		}
	}
	return "", false
}

func nestedScalarField(value map[string]any, name string) (string, bool) {
	if text, ok := nestedTextField(value, name); ok {
		return text, true
	}
	if number, ok := uintField(value, name); ok {
		return fmt.Sprint(number), true
	}
	for _, nestedName := range []string{"binding", "governance", "workspace"} {
		if nested, ok := value[nestedName].(map[string]any); ok {
			if scalar, found := nestedScalarField(nested, name); found {
				return scalar, true
			}
		}
	}
	return "", false
}

func captureAuthority(t *testing.T, h *portableHarness, project, missionID string) authoritySnapshot {
	t.Helper()
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(project), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return authoritySnapshot{revision: progress.ProjectionRevision, digest: progress.ProjectionSHA256, missionSnapshot: string(canonical), repositoryCalls: h.git.count()}
}

func assertAuthorityUnchanged(t *testing.T, before authoritySnapshot, h *portableHarness, project, missionID, refusal string) {
	t.Helper()
	after := captureAuthority(t, h, project, missionID)
	if after != before {
		t.Fatalf("refusal=%s mutated authority/effects: before=%+v after=%+v", refusal, before, after)
	}
}

func semanticProgressDigest(t *testing.T, h *portableHarness, project, missionID string) string {
	t.Helper()
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(project), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	if progress.ProjectionSHA256 == "" {
		t.Fatal("progress omitted canonical projection digest")
	}
	return progress.ProjectionSHA256
}

func assertStaleEpochRefusesBeforeRepository(t *testing.T, h *portableHarness, stale journalv2.RuntimeEpochID) {
	t.Helper()
	status := h.host.Status()
	beforeRevision, beforeDigest, beforeCalls := status.ProjectionRevision, status.ProjectionSHA256, h.git.count()
	_, err := h.host.ExecuteReadiness(t.Context(), stale, zeroReadinessCommand())
	if !productionruntime.IsKind(err, productionruntime.ErrorStaleEpoch) {
		t.Fatalf("stale epoch returned %v", err)
	}
	after := h.host.Status()
	if after.ProjectionRevision != beforeRevision || after.ProjectionSHA256 != beforeDigest || h.git.count() != beforeCalls {
		t.Fatalf("stale epoch changed authority or crossed repository boundary: before=(%d,%s,%d) after=(%d,%s,%d)", beforeRevision, beforeDigest, beforeCalls, after.ProjectionRevision, after.ProjectionSHA256, h.git.count())
	}
}

func zeroReadinessCommand() mission.ReadinessAuthorityCommandV2 {
	// Kept separate only to make accidental command construction at this probe
	// a compile-time error. The caller converts no untrusted or textual input.
	return mission.ReadinessAuthorityCommandV2{}
}
