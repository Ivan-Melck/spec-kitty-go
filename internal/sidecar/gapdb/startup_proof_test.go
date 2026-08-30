package gapdb

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

func TestOwnerStartupProofBindsOneEpochAcrossLifecycleAliases(t *testing.T) {
	lifecycle := startupProofLifecycle("database-proof")
	productProof := markTestProductRecovered(t, lifecycle, "project-proof", "mission-proof")
	if facts, err := productProof.Facts(); err != nil || facts.Source.ProjectID != "project-proof" || facts.Binding.DatabaseID != "database-proof" {
		t.Fatalf("product restore facts=%+v err=%v", facts, err)
	}
	productFacts, _ := productProof.Facts()
	productFacts.Source.ProjectID = "forged-project"
	if again, err := productProof.Facts(); err != nil || again.Source.ProjectID != "project-proof" {
		t.Fatalf("product proof changed through facts copy: %+v err=%v", again, err)
	}
	proof, err := lifecycle.BindOwnerStartupEpochV1("epoch-one")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := proof.Facts()
	if err != nil || facts.RuntimeEpochID != "epoch-one" || facts.DatabaseID != "database-proof" || !strings.HasPrefix(facts.HostInstanceID, "host-") {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
	// Value copies cannot mutate the sealed proof.
	facts.DatabaseID = "forged"
	again, err := proof.Facts()
	if err != nil || again.DatabaseID != "database-proof" {
		t.Fatalf("proof changed through facts copy: %+v err=%v", again, err)
	}
	if _, err := lifecycle.BindOwnerStartupEpochV1("epoch-one"); err != nil {
		t.Fatalf("same epoch retry refused: %v", err)
	}
	alias := startupProofLifecycle("database-proof")
	_ = markTestProductRecovered(t, alias, "project-proof", "mission-proof")
	if _, err := alias.BindOwnerStartupEpochV1("epoch-two"); !IsFailure(err, FailureOwnership) {
		t.Fatalf("same host lifetime rebound: %v", err)
	}
	aliasProof, err := alias.BindOwnerStartupEpochV1("epoch-one")
	if err != nil {
		t.Fatalf("same host lifetime did not share proof: %v", err)
	}
	if aliasFacts, err := aliasProof.Facts(); err != nil || aliasFacts != again {
		t.Fatalf("alias facts=%+v err=%v want=%+v", aliasFacts, err, again)
	}
}

func TestOwnerStartupProofRefusesUnadmittedStoppedAndManualValues(t *testing.T) {
	unrecovered := startupProofLifecycle("database-unrecovered")
	for name, lifecycle := range map[string]*Lifecycle{
		"unadmitted":  {},
		"stopped":     startupProofLifecycle("database-stopped"),
		"unrecovered": unrecovered,
	} {
		if name == "stopped" {
			lifecycle.stopped = true
		}
		t.Run(name, func(t *testing.T) {
			if _, err := lifecycle.BindOwnerStartupEpochV1("epoch-refused"); err == nil {
				t.Fatal("invalid lifecycle minted startup proof")
			}
		})
	}
	if _, err := (OwnerStartupProofV1{}).Facts(); !IsFailure(err, FailureOwnership) {
		t.Fatalf("manual proof error=%v", err)
	}
	if _, err := (ProductRestoreProofV1{}).Facts(); !IsFailure(err, FailureOwnership) {
		t.Fatalf("manual product restore proof error=%v", err)
	}
	if _, err := startupProofLifecycle("database-invalid-epoch").BindOwnerStartupEpochV1(""); err == nil {
		t.Fatal("empty epoch bound")
	}
}

func TestProductRestoreProofBindsExactDatabaseAndOwnerLifetime(t *testing.T) {
	original := startupProofLifecycle("database-restore-binding")
	admission := testRestoreAdmission(t, original, "project-binding", "mission-binding")
	mismatch := startupProofLifecycle("database-restore-binding")
	mismatch.process.PID++
	mismatch.process.StartTicks++
	mismatch.provenance.Process = mismatch.process
	if _, err := mismatch.MarkProductRecovered(admission); !IsFailure(err, FailureReadiness) {
		t.Fatalf("foreign owner lifetime accepted restore admission: %v", err)
	}
	otherDatabase := startupProofLifecycle("database-other")
	if _, err := otherDatabase.MarkProductRecovered(admission); !IsFailure(err, FailureReadiness) {
		t.Fatalf("foreign database accepted restore admission: %v", err)
	}
}

func TestProductReadinessSurfaceAcceptsOnlySealedRestoreAdmission(t *testing.T) {
	method, found := reflect.TypeOf((*Lifecycle)(nil)).MethodByName("MarkProductRecovered")
	want := reflect.TypeOf(journalcheckpoint.RestoreAdmissionV1{})
	if !found || method.Type.NumIn() != 2 || method.Type.In(1) != want || method.Type.NumOut() != 2 ||
		method.Type.Out(0) != reflect.TypeOf(ProductRestoreProofV1{}) {
		t.Fatalf("product readiness surface drifted: found=%t type=%v", found, method.Type)
	}
}

func startupProofLifecycle(databaseID string) *Lifecycle {
	return &Lifecycle{
		pidfd:   -1,
		process: ProcessIdentity{PID: 4242, StartTicks: 77, Device: 3, Inode: 4, Size: 5, SHA256: strings.Repeat("b", 64)},
		provenance: SidecarProvenance{
			SchemaVersion: 1, SupervisorRunID: "supervisor-run", DatabaseID: databaseID,
			Executable:  ExecutableProvenance{ExecutablePath: "/opt/spec-kitty/gapdbd", SHA256: strings.Repeat("b", 64)},
			Process:     ProcessIdentity{PID: 4242, StartTicks: 77, Device: 3, Inode: 4, Size: 5, SHA256: strings.Repeat("b", 64)},
			Observation: CompositeObservation{Status: statusWithOwnerStartedAt("2026-08-30T12:00:00Z")},
			Readiness: ReadinessPredicates{
				ByteRecovered: true, Healthy: true, Configured: true, Admitted: true,
			},
			AdmittedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		},
	}
}

type pristineRestoreReaderV1 struct{}

func (pristineRestoreReaderV1) CheckpointHeadsV1(context.Context) ([]journalcheckpoint.CheckpointHeadV1, error) {
	return nil, nil
}
func (pristineRestoreReaderV1) CheckpointBlobV1(context.Context, journalcheckpoint.CheckpointID) ([]byte, error) {
	return nil, nil
}
func (pristineRestoreReaderV1) JournalPageV1(context.Context, journalcheckpoint.WatermarkV1, string) (journalcheckpoint.JournalScanPageV1, error) {
	return journalcheckpoint.JournalScanPageV1{ObservedRevision: 100}, nil
}

func markTestProductRecovered(t *testing.T, lifecycle *Lifecycle, project, mission string) ProductRestoreProofV1 {
	t.Helper()
	admission := testRestoreAdmission(t, lifecycle, project, mission)
	proof, err := lifecycle.MarkProductRecovered(admission)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func testRestoreAdmission(t *testing.T, lifecycle *Lifecycle, project, mission string) journalcheckpoint.RestoreAdmissionV1 {
	t.Helper()
	binding, err := lifecycle.ProductRestoreBindingV1()
	if err != nil {
		t.Fatal(err)
	}
	events, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{
		EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	reducers, err := reducer.NewRegistryV1([]reducer.EntryV1{{
		AggregateKind: "mission", EventType: "mission.created", EventSchemaVersion: 1,
		ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	restored, err := journalcheckpoint.RestoreV1(t.Context(), journalcheckpoint.RestoreInputV1{
		Reader: pristineRestoreReaderV1{},
		Source: journalcheckpoint.SourceIdentityV1{
			ProjectID: controljournal.ProjectID(project), MissionID: controljournal.MissionID(mission), RunID: "run-restore",
			ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest,
		},
		Binding: binding, EventRegistry: events, Reducers: reducers,
	})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := restored.AdmissionV1()
	if err != nil {
		t.Fatal(err)
	}
	return admission
}

func statusWithOwnerStartedAt(value string) gapdbapi.StatusResult {
	return gapdbapi.StatusResult{OwnerStartedAt: value}
}
