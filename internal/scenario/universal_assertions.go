package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
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

// assertUniversalInvariants observes only the public readiness and progress
// views. The returned bytes are stable evidence suitable for campaign census
// records; they contain no process path or provider transcript.
func assertUniversalInvariants(t *testing.T, h *portableHarness, project, missionID, cut string) []byte {
	t.Helper()
	status := h.host.Status()
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(project), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatalf("scenario=%s cut=%s progress: %v", t.Name(), cut, err)
	}
	evidence, err := validateUniversalSnapshot(status, progress, project, missionID, h.git.count())
	if err != nil {
		t.Fatalf("scenario=%s cut=%s universal invariant: %v", t.Name(), cut, err)
	}
	evidence.Scenario = t.Name()
	evidence.Cut = cut
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

// validateUniversalSnapshot is the pure, adversarially testable half of the
// scenario oracle. It accepts only the public host/progress DTOs; no raw
// GapDB, projection repository, or provider transcript can satisfy a law.
func validateUniversalSnapshot(status productionruntime.HostStatus, progress productionruntime.MissionProgress, project, missionID string, repositoryCalls int) (invariantEvidence, error) {
	if status.State != productionruntime.HostReady || status.DatabaseID == "" || status.RuntimeEpochID == "" || status.ProjectionRevision == 0 || status.ProjectionSHA256 == "" {
		return invariantEvidence{}, fmt.Errorf("incomplete readiness")
	}
	if progress.SchemaVersion != productionruntime.ProgressSchemaVersion || progress.DatabaseID != status.DatabaseID || progress.RuntimeEpochID != status.RuntimeEpochID || progress.ProjectionRevision != status.ProjectionRevision || progress.ProjectionSHA256 != status.ProjectionSHA256 || progress.SnapshotSHA256 == "" || string(progress.ProjectID) != project || string(progress.MissionID) != missionID {
		return invariantEvidence{}, fmt.Errorf("readiness/progress disagreement")
	}
	seen := make(map[string]struct{}, len(progress.Items))
	families := make(map[string]struct{})
	work := make(map[string]map[string]map[string]any)
	itemKeys := make(map[string]productionruntime.ProgressItem, len(progress.Items))
	states := make(map[string]map[string]any, len(progress.Items))
	operations := make(map[string]journalv2.OperationStateV2)
	eventOwners := make(map[journalv2.EventID]string)
	operationCount := 0
	for _, item := range progress.Items {
		key := item.Family + "\x00" + item.AggregateID
		if item.Family == "" || item.AggregateID == "" || item.LogicalVersion == 0 || len(item.CanonicalState) == 0 {
			return invariantEvidence{}, fmt.Errorf("incomplete authority item %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return invariantEvidence{}, fmt.Errorf("duplicate aggregate %q", key)
		}
		seen[key] = struct{}{}
		itemKeys[key] = item
		families[item.Family] = struct{}{}
		canonical, canonicalErr := journalv2.CanonicalObject(item.CanonicalState)
		if canonicalErr != nil {
			return invariantEvidence{}, fmt.Errorf("noncanonical %s state: %w", key, canonicalErr)
		}
		if !bytes.Equal(canonical, item.CanonicalState) {
			return invariantEvidence{}, fmt.Errorf("noncanonical %s state", key)
		}
		if item.LastPublicationID == "" {
			return invariantEvidence{}, fmt.Errorf("authority item has no publication: %s", key)
		}
		if _, decodeErr := journalv2.DecodePublicationID(item.LastPublicationID); decodeErr != nil {
			return invariantEvidence{}, fmt.Errorf("invalid publication %s: %w", key, decodeErr)
		}
		var state map[string]any
		if err := json.Unmarshal(item.CanonicalState, &state); err != nil {
			return invariantEvidence{}, fmt.Errorf("decode %s: %w", key, err)
		}
		states[key] = state
		stateVersion, versionPresent := uintField(state, "logical_version")
		if !versionPresent {
			stateVersion, versionPresent = uintField(state, "version")
		}
		if !versionPresent || stateVersion != item.LogicalVersion {
			return invariantEvidence{}, fmt.Errorf("projection/head version mismatch %s", key)
		}
		if value, ok := textField(state, "project_id"); ok && value != project {
			return invariantEvidence{}, fmt.Errorf("cross-project state %s", key)
		}
		if value, ok := textField(state, "mission_id"); ok && value != missionID {
			return invariantEvidence{}, fmt.Errorf("cross-mission state %s", key)
		}
		if item.Family == "operation" {
			operation, decodeErr := journalv2.DecodeOperationStateV2(item.CanonicalState)
			if decodeErr != nil || string(operation.OperationID()) != item.AggregateID || uint64(operation.LogicalVersion()) != item.LogicalVersion || string(operation.LastPublicationID()) != item.LastPublicationID {
				return invariantEvidence{}, fmt.Errorf("operation binding/result mismatch %s", key)
			}
			if operation.Status() == journalv2.StatusResolved || operation.Status() == journalv2.StatusRefused {
				if operation.ResultKind() == "" || operation.ResultSchemaVersion() == 0 || len(operation.Result()) == 0 || operation.ResultDigest().IsZero() {
					return invariantEvidence{}, fmt.Errorf("terminal operation has no immutable result: %s", key)
				}
			}
			if len(operation.EventIDs()) == 0 {
				return invariantEvidence{}, fmt.Errorf("operation has no complete batch membership: %s", key)
			}
			for _, eventID := range operation.EventIDs() {
				if owner, duplicate := eventOwners[eventID]; duplicate && owner != item.AggregateID {
					return invariantEvidence{}, fmt.Errorf("event %s belongs to operations %s and %s", eventID, owner, item.AggregateID)
				}
				eventOwners[eventID] = item.AggregateID
			}
			operations[item.AggregateID] = operation
			operationCount++
		}
		if workID, ok := nestedTextField(state, "work_id"); ok {
			if work[workID] == nil {
				work[workID] = make(map[string]map[string]any)
			}
			work[workID][item.Family] = state
		}
	}
	if err := validateProgressFamilyMembership(progress, itemKeys); err != nil {
		return invariantEvidence{}, err
	}
	for key, state := range states {
		item := itemKeys[key]
		if item.Family == "operation" {
			continue
		}
		if operationID, ok := nestedTextField(state, "last_operation_id"); ok && operationID != "" && len(operations) != 0 {
			if _, present := operations[operationID]; !present {
				return invariantEvidence{}, fmt.Errorf("%s names absent operation %s", key, operationID)
			}
		}
		if item.Family == "external_effect" {
			effect, decodeErr := execution.DecodeExternalEffectStateV2(item.CanonicalState)
			if decodeErr != nil {
				return invariantEvidence{}, fmt.Errorf("decode %s: %w", key, decodeErr)
			}
			if operation, present := operations[string(effect.Intent.OperationID)]; present {
				foundEffect := false
				for _, effectID := range operation.EffectIDs() {
					if string(effectID) == item.AggregateID {
						foundEffect = true
						break
					}
				}
				if !foundEffect {
					return invariantEvidence{}, fmt.Errorf("effect %s is absent from operation batch membership", item.AggregateID)
				}
			}
			if (effect.Stage == execution.EffectAmbiguousV2 || effect.Stage == execution.EffectReconciliationRequiredV2) && (effect.PermissionMarker != "" || effect.Outcome != "") {
				return invariantEvidence{}, fmt.Errorf("ambiguous effect %s acquired retry, compensation, or terminal authority", item.AggregateID)
			}
		}
	}
	for workID, byFamily := range work {
		if err := validateWorkCoordinateAgreement(workID, byFamily); err != nil {
			return invariantEvidence{}, err
		}
	}
	familyList := make([]string, 0, len(families))
	for family := range families {
		familyList = append(familyList, family)
	}
	sort.Strings(familyList)
	return invariantEvidence{
		SchemaVersion: productionScenarioSchemaVersion,
		DatabaseID:    status.DatabaseID, RuntimeEpochID: status.RuntimeEpochID,
		ProjectionRevision: status.ProjectionRevision, ProjectionSHA256: status.ProjectionSHA256,
		Families: familyList, AggregateCount: len(progress.Items), OperationCount: operationCount, RepositoryCalls: repositoryCalls,
	}, nil
}

func validateProgressFamilyMembership(progress productionruntime.MissionProgress, all map[string]productionruntime.ProgressItem) error {
	checks := []struct {
		name     string
		items    []productionruntime.ProgressItem
		families map[string]struct{}
	}{
		{"effects", progress.Effects, map[string]struct{}{"external_effect": {}}},
		{"reviews", progress.Reviews, map[string]struct{}{"review_slot": {}}},
		{"acceptance", progress.Acceptance, map[string]struct{}{"acceptance": {}}},
		{"integration", progress.Integration, map[string]struct{}{"integration_target": {}, "integration_attempt": {}}},
		{"operations", progress.Operations, map[string]struct{}{"operation": {}}},
	}
	for _, check := range checks {
		expected := make(map[string]struct{})
		for key, item := range all {
			if _, include := check.families[item.Family]; include {
				expected[key] = struct{}{}
			}
		}
		actual := make(map[string]struct{}, len(check.items))
		for _, item := range check.items {
			key := item.Family + "\x00" + item.AggregateID
			if _, allowed := check.families[item.Family]; !allowed {
				return fmt.Errorf("%s contains foreign family %s", check.name, item.Family)
			}
			canonical, present := all[key]
			if !present || canonical.LogicalVersion != item.LogicalVersion || canonical.LastPublicationID != item.LastPublicationID || !bytes.Equal(canonical.CanonicalState, item.CanonicalState) {
				return fmt.Errorf("%s contains an unbound or stale member %s", check.name, key)
			}
			if _, duplicate := actual[key]; duplicate {
				return fmt.Errorf("%s duplicates member %s", check.name, key)
			}
			actual[key] = struct{}{}
		}
		if len(actual) != len(expected) {
			return fmt.Errorf("%s membership incomplete: got=%d want=%d", check.name, len(actual), len(expected))
		}
	}
	return nil
}

func validateWorkCoordinateAgreement(workID string, states map[string]map[string]any) error {
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
				return fmt.Errorf("work=%s coordinate=%s disagreement in %s", workID, field, family)
			}
		}
	}
	if executionState := states["execution_slot"]; executionState != nil {
		contributor, _ := nestedTextField(executionState, "actor_id")
		if reviewState := states["review_slot"]; reviewState != nil {
			reviewer, _ := nestedTextField(reviewState, "reviewer_actor_id")
			if contributor != "" && reviewer == contributor {
				return fmt.Errorf("work=%s reviewer actor equals contributor actor", workID)
			}
		}
	}
	return nil
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
