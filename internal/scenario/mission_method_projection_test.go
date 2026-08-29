package scenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

type missionMethodProjectionAuthority struct {
	Core       dishonestMissionAuthority
	Supplement string
	Tracker    string
	Next       string
}

func TestMissionMethodProjectionAndGovernanceIndependence100(t *testing.T) {
	projectionCensus := make(map[string]int)
	for trial := 0; trial < 100; trial++ {
		fixture := runPortableMissionFixture(t, fmt.Sprintf("projection-%03d", trial))
		before := snapshotMissionMethodProjectionAuthority(t, fixture)
		path := filepath.Join(fixture.OutputRoot, "tasks.md")
		kind := mutateMissionMethodProjection(t, trial, fixture, path)
		projectionCensus[kind]++
		if kind == "symlink" {
			response := callMethodRaw(t, context.Background(), "projection", "tasks", map[string]any{
				"database": fixture.Database, "correlation_id": "correlation-symlink-refusal", "mission_id": fixture.MissionTwo.MissionID,
				"rendered_at": fixture.MissionTwo.UpdatedAt, "trusted_root": fixture.OutputRoot, "destination": "tasks.md",
			})
			if response.OK || response.Error == nil {
				t.Fatalf("projection symlink trial=%d was accepted", trial)
			}
		}
		after := snapshotMissionMethodProjectionAuthority(t, fixture)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("projection trial=%d kind=%s changed canonical authority\nbefore=%+v\nafter=%+v", trial, kind, before, after)
		}
	}
	for _, kind := range []string{"delete", "truncate", "edit", "checkbox", "stale", "invalid-utf8", "symlink", "other-mission"} {
		if projectionCensus[kind] == 0 {
			t.Fatalf("projection mutation %s was not exercised", kind)
		}
	}
	testMissionMethodGovernanceHistory100(t)
}

func mutateMissionMethodProjection(t *testing.T, trial int, fixture portableMissionResult, path string) string {
	t.Helper()
	switch trial % 8 {
	case 0:
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return "delete"
	case 1:
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return "truncate"
	case 2:
		if err := os.WriteFile(path, []byte("arbitrary derived edit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return "edit"
	case 3:
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content = bytes.Replace(content, []byte("- [ ]"), []byte("- [x]"), 1)
		if err = os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return "checkbox"
	case 4:
		if err := os.WriteFile(path, []byte(fixture.FirstTasks), 0o600); err != nil {
			t.Fatal(err)
		}
		return "stale"
	case 5:
		if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xfd}, 0o600); err != nil {
			t.Fatal(err)
		}
		return "invalid-utf8"
	case 6:
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("outside sentinel\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		return "symlink"
	default:
		if err := os.WriteFile(path, []byte("# NON-AUTHORITATIVE\nmission: mission-other\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return "other-mission"
	}
}

func snapshotMissionMethodProjectionAuthority(t *testing.T, fixture portableMissionResult) missionMethodProjectionAuthority {
	t.Helper()
	trackerBytes, err := os.ReadFile(fixture.TrackerStore)
	if err != nil {
		t.Fatal(err)
	}
	next := callMethod(t, context.Background(), "mission", "next", map[string]any{
		"database": fixture.Database, "correlation_id": "correlation-projection-authority", "mission_id": fixture.MissionTwo.MissionID,
	})
	nextBytes, err := json.Marshal(next.Result)
	if err != nil {
		t.Fatal(err)
	}
	return missionMethodProjectionAuthority{
		Core:       observeDishonestLocalAuthority(t, fixture.Database, fixture.MissionTwo.MissionID),
		Supplement: snapshotMissionMethodSupplementalTables(t, fixture.Database),
		Tracker:    fmt.Sprintf("%x", sha256.Sum256(trackerBytes)), Next: fmt.Sprintf("%x", sha256.Sum256(nextBytes)),
	}
}

func snapshotMissionMethodSupplementalTables(t *testing.T, path string) string {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	queries := []string{
		`SELECT artifact_revision_id,mission_id,kind,revision_number,content_digest,source_ref,source_revision,created_by,created_at,revision_json FROM phase3_artifact_revisions ORDER BY artifact_revision_id`,
		`SELECT artifact_revision_id,verdict,validation_json FROM phase3_artifact_validations ORDER BY artifact_revision_id`,
		`SELECT approval_id,artifact_revision_id,verdict,actor_id,evidence_digest,governance_context_id,submitted_at,approval_json FROM phase3_artifact_approvals ORDER BY approval_id`,
		`SELECT reconciliation_id,old_work_revision_id,new_work_revision_id,work_id,work_status,disposition,operation,allowed,reason FROM phase3_eligibility_facts ORDER BY reconciliation_id,work_id,work_status,operation`,
		`SELECT decision_id,mission_id,status,version,decision_json FROM phase3_decisions ORDER BY decision_id`,
		`SELECT blocker_id,mission_id,status,version,blocker_json FROM phase3_blockers ORDER BY blocker_id`,
		`SELECT operation_id,mission_id,action_key,successor,actor_id,process_id,governance_context_id,governance_commitment,receipt,occurred_at FROM phase3_mission_actions ORDER BY operation_id`,
		`SELECT context_id,schema_version,aggregate_algorithm,aggregate_digest,created_at FROM governance_contexts ORDER BY context_id`,
		`SELECT context_id,position,kind,entry_ref,revision,commitment_algorithm,commitment_digest FROM governance_context_entries ORDER BY context_id,position`,
	}
	hash := sha256.New()
	for _, query := range queries {
		rows, queryErr := database.Query(query)
		if queryErr != nil {
			t.Fatalf("supplemental authority query: %T", queryErr)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err = rows.Scan(destinations...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			canonical := make([]string, len(values))
			for index, value := range values {
				canonical[index], err = canonicalDishonestAuthorityValue(value, bytes.HasSuffix([]byte(columns[index]), []byte("_json")))
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
			}
			encoded, _ := json.Marshal(canonical)
			hash.Write(encoded)
			hash.Write([]byte{'\n'})
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func testMissionMethodGovernanceHistory100(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "governance.db")
	at := time.Date(2026, 8, 28, 21, 0, 0, 0, time.UTC)
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	source := missionMethodActionContext(t, "governance-history", "original", at)
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: "mission-governance-history", ProjectID: "project-governance-history", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-create-governance-history", ActorID: "actor-history", ProcessID: "process-history", Governance: source, OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	intentPolicy, err := artifact.PolicyFor(artifact.KindIntent)
	if err != nil {
		t.Fatal(err)
	}
	intentConcerns := intentPolicy.RequiredConcerns()
	if intentConcerns == nil {
		intentConcerns = []artifact.Concern{}
	}
	callMethod(t, ctx, "artifact", "publish", map[string]any{
		"database": path, "correlation_id": "correlation-history-intent", "operation_id": "operation-history-intent",
		"artifact_revision_id": "artifact-history-intent", "mission_id": created.Instance.MissionID, "kind": artifact.KindIntent,
		"source_ref": "intent.md", "source_revision": strings.Repeat("b", 40), "created_by": "actor-history", "created_at": at.Add(time.Second),
		"content": []byte("governance history intent"), "present_concerns": intentConcerns, "validated_at": at.Add(time.Second),
		"expected_latest_revision_id": "",
	})
	store, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := governancecontext.NewMissionAction(governancecontext.MissionActionParams{Source: source, MissionID: created.Instance.MissionID, StepKey: created.Instance.CurrentStep.String(), ActionKey: "mission.specify", ActorID: "actor-history", SelectedEntries: source.Entries()})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := sqlite.NewMissionActionAdapter(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	command := mission.ActionCommand{SchemaVersion: mission.ActionCommandSchemaVersion, CorrelationID: "correlation-history", OperationID: "operation-action-history", MissionID: created.Instance.MissionID, ExpectedMission: created.Instance, ActionKey: mission.StepSpecifying.String(), Successor: mission.StepSpecifying, GovernanceAction: "mission.specify", ActorID: "actor-history", ProcessID: "process-history", Governance: binding, PayloadDigest: strings.Repeat("a", 64), OccurredAt: at.Add(2 * time.Second)}
	result, err := mission.NewActionService(adapter, map[string]mission.ActionHandler{"mission.specify": mission.AdvanceActionHandler{}}).Execute(ctx, command)
	if err != nil || result.Replayed {
		t.Fatalf("persist action=%+v err=%v", result, err)
	}
	store.Close()
	wantOutcome, _ := json.Marshal(result.Outcome)
	wantCommitment := binding.Commitment().Digest()
	wantEntries := source.Entries()
	if grants := binding.Grants(); !reflect.DeepEqual(grants, []governancecontext.AuthorityGrant{governancecontext.GrantMissionTransition}) {
		t.Fatalf("original action grant=%v", grants)
	}
	for trial := 0; trial < 100; trial++ {
		// A new mutable source exists, but replay authority remains the original
		// sealed context and selected-entry commitments.
		_ = missionMethodActionContext(t, fmt.Sprintf("governance-new-%03d", trial), fmt.Sprintf("new-%03d", trial), at.Add(time.Duration(trial+2)*time.Second))
		reopened, openErr := sqlite.Open(ctx, path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		reopenedAdapter, adapterErr := sqlite.NewMissionActionAdapter(reopened, registry)
		if adapterErr != nil {
			reopened.Close()
			t.Fatal(adapterErr)
		}
		replay, replayErr := mission.NewActionService(reopenedAdapter, map[string]mission.ActionHandler{"mission.specify": mission.AdvanceActionHandler{}}).Execute(ctx, command)
		gotOutcome, _ := json.Marshal(replay.Outcome)
		if replayErr != nil || !replay.Replayed || replay.Receipt != result.Receipt || !bytes.Equal(wantOutcome, gotOutcome) {
			reopened.Close()
			t.Fatalf("governance trial=%d replay=%+v err=%v", trial, replay, replayErr)
		}
		var storedContext, storedCommitment, storedActor, storedProcess, storedAction, storedSuccessor string
		if queryErr := reopenedRawAction(path, &storedContext, &storedCommitment, &storedActor, &storedProcess, &storedAction, &storedSuccessor); queryErr != nil || storedContext != binding.ContextID().String() || storedCommitment != wantCommitment || storedActor != command.ActorID.String() || storedProcess != command.ProcessID.String() || storedAction != mission.StepSpecifying.String() || storedSuccessor != mission.StepSpecifying.String() {
			reopened.Close()
			t.Fatalf("governance trial=%d context=%s commitment=%s actor=%s process=%s err=%v", trial, storedContext, storedCommitment, storedActor, storedProcess, queryErr)
		}
		assertMissionMethodStoredGovernance(t, path, source, wantEntries)
		if grants := command.Governance.Grants(); !reflect.DeepEqual(grants, []governancecontext.AuthorityGrant{governancecontext.GrantMissionTransition}) {
			reopened.Close()
			t.Fatalf("governance trial=%d grant changed: %v", trial, grants)
		}
		reopened.Close()
	}
	mutated := wantCommitment[:len(wantCommitment)-1]
	if mutated == wantCommitment {
		t.Fatal("omitted governance commitment mutant survived")
	}
	if strings.Contains(strings.ToLower(string(wantOutcome)), "password") || strings.Contains(strings.ToLower(string(wantOutcome)), "credential") {
		t.Fatal("governance history captured unrelated private content")
	}
}

func missionMethodActionContext(t *testing.T, id, revision string, at time.Time) governancecontext.Context {
	t.Helper()
	kinds := []governancecontext.EntryKind{governancecontext.KindConfig, governancecontext.KindDoctrine, governancecontext.KindMissionType, governancecontext.KindProfile}
	refs := []string{"config.default", "doctrine.default", "software-development", "implementer-ivan"}
	entries := make([]governancecontext.Entry, 0, len(kinds))
	for index, kind := range kinds {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", kind, refs[index], revision)))
		commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, fmt.Sprintf("%x", digest))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := governancecontext.NewEntry(kind, refs[index], revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	value, err := governancecontext.NewContext(ids.GovernanceContextID(id), governancecontext.SchemaVersion, entries, at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func reopenedRawAction(path string, contextID, commitment, actor, process, action, successor *string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer database.Close()
	return database.QueryRow(`SELECT governance_context_id,governance_commitment,actor_id,process_id,action_key,successor FROM phase3_mission_actions WHERE operation_id='operation-action-history'`).Scan(contextID, commitment, actor, process, action, successor)
}

func assertMissionMethodStoredGovernance(t *testing.T, path string, source governancecontext.Context, wantEntries []governancecontext.Entry) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var schema uint16
	var algorithm, digest, created string
	if err = database.QueryRow(`SELECT schema_version,aggregate_algorithm,aggregate_digest,created_at FROM governance_contexts WHERE context_id=?`, source.ID()).Scan(&schema, &algorithm, &digest, &created); err != nil || schema != source.SchemaVersion() || algorithm != source.Aggregate().Algorithm() || digest != source.Aggregate().Digest() || created != source.CreatedAt().UTC().Format(time.RFC3339Nano) {
		t.Fatalf("sealed governance header schema=%d algorithm=%s digest=%s created=%s err=%v", schema, algorithm, digest, created, err)
	}
	rows, err := database.Query(`SELECT position,kind,entry_ref,revision,commitment_algorithm,commitment_digest FROM governance_context_entries WHERE context_id=? ORDER BY position`, source.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	position := 0
	for rows.Next() {
		var storedPosition int
		var kind, reference, revision, entryAlgorithm, entryDigest string
		if err = rows.Scan(&storedPosition, &kind, &reference, &revision, &entryAlgorithm, &entryDigest); err != nil {
			t.Fatal(err)
		}
		if position >= len(wantEntries) {
			t.Fatal("stored governance contains an unrelated entry")
		}
		want := wantEntries[position]
		if storedPosition != position || kind != string(want.Kind()) || reference != want.Ref() || revision != want.Revision() || entryAlgorithm != want.Commitment().Algorithm() || entryDigest != want.Commitment().Digest() {
			t.Fatalf("stored governance entry position=%d kind=%s ref=%s revision=%s", storedPosition, kind, reference, revision)
		}
		position++
	}
	if err = rows.Err(); err != nil || position != len(wantEntries) {
		t.Fatalf("stored governance entry census=%d/%d err=%v", position, len(wantEntries), err)
	}
}
