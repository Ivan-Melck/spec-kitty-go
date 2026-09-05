package scenario

import (
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
	RepositoryCalls    int                      `json:"repository_calls"`
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
		Families: familyList, AggregateCount: len(progress.Items), RepositoryCalls: h.git.count(),
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
	before := h.git.count()
	_, err := h.host.ExecuteMission(t.Context(), stale, zeroMissionCommand())
	if !productionruntime.IsKind(err, productionruntime.ErrorStaleEpoch) {
		t.Fatalf("stale epoch returned %v", err)
	}
	if after := h.git.count(); after != before {
		t.Fatalf("stale epoch crossed repository boundary: before=%d after=%d", before, after)
	}
}

func zeroMissionCommand() mission.MissionAuthorityCommandV2 {
	// Kept separate only to make accidental command construction at this probe
	// a compile-time error. The caller converts no untrusted or textual input.
	return mission.MissionAuthorityCommandV2{}
}

func invariantFailure(scenario, cut, law string, cause error) error {
	return fmt.Errorf("scenario=%s cut=%s law=%s: %w", scenario, cut, law, cause)
}
