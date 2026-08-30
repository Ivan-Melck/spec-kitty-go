package gapdb

import (
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

func TestOwnerStartupProofBindsOneEpochAcrossLifecycleAliases(t *testing.T) {
	lifecycle := startupProofLifecycle("database-proof")
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
	unrecovered.provenance.Readiness.ProductRecovered = false
	unrecovered.provenance.Readiness.ReadyForProductRecovery = false
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
	if _, err := startupProofLifecycle("database-invalid-epoch").BindOwnerStartupEpochV1(""); err == nil {
		t.Fatal("empty epoch bound")
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
				ProductRecovered: true, ReadyForProductRecovery: true,
			},
			AdmittedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		},
	}
}

func statusWithOwnerStartedAt(value string) gapdbapi.StatusResult {
	return gapdbapi.StatusResult{OwnerStartedAt: value}
}
