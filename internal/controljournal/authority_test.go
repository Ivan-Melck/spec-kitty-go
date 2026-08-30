package controljournal

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func validAuthorityCapsule() AuthorityCapsuleV1 {
	return AuthorityCapsuleV1{
		SchemaVersion: 1, ProjectID: "project-1", MissionID: "mission-1",
		WorkRevisionID: "revision-1", ActorID: "actor-1", ProcessID: "process-1",
		RunID: "run-1", LeaseID: "lease-1", WorkspaceID: "workspace-1",
		RuntimeEpochID: "epoch-1", LeaseGeneration: 2, FenceToken: 3,
		AuthorityHeadRecordRevision: 4,
	}
}

func TestAuthorityCapsuleFrozenThirteenFieldContract(t *testing.T) {
	typeOf := reflect.TypeOf(AuthorityCapsuleV1{})
	expected := []string{
		"SchemaVersion", "ProjectID", "MissionID", "WorkRevisionID", "ActorID",
		"ProcessID", "RunID", "LeaseID", "WorkspaceID", "RuntimeEpochID",
		"LeaseGeneration", "FenceToken", "AuthorityHeadRecordRevision",
	}
	if typeOf.NumField() != len(expected) {
		t.Fatalf("authority capsule field count=%d want=%d", typeOf.NumField(), len(expected))
	}
	for index, name := range expected {
		if typeOf.Field(index).Name != name {
			t.Fatalf("authority capsule field %d=%s want=%s", index, typeOf.Field(index).Name, name)
		}
	}
}

func TestAuthorityCapsuleCanonicalRoundTripAndClosedObject(t *testing.T) {
	capsule := validAuthorityCapsule()
	encoded, err := EncodeAuthorityCapsuleV1(capsule)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAuthorityCapsuleV1(encoded)
	if err != nil || decoded != capsule {
		t.Fatalf("round trip=%+v err=%v", decoded, err)
	}
	reencoded, err := EncodeAuthorityCapsuleV1(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("noncanonical round trip: %s != %s err=%v", encoded, reencoded, err)
	}

	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, _ := json.Marshal(object)
	if _, err := DecodeAuthorityCapsuleV1(unknown); err == nil {
		t.Fatal("accepted additional authority property")
	}
	delete(object, "unexpected")
	delete(object, "actor_id")
	missing, _ := json.Marshal(object)
	if _, err := DecodeAuthorityCapsuleV1(missing); err == nil {
		t.Fatal("accepted missing required authority property")
	}
}

func TestAuthorityCapsuleConstructorAndWireMutantsFailClosed(t *testing.T) {
	mutants := map[string]func(*AuthorityCapsuleV1){
		"schema":           func(value *AuthorityCapsuleV1) { value.SchemaVersion++ },
		"project":          func(value *AuthorityCapsuleV1) { value.ProjectID = "" },
		"mission":          func(value *AuthorityCapsuleV1) { value.MissionID = "" },
		"work revision":    func(value *AuthorityCapsuleV1) { value.WorkRevisionID = "" },
		"actor":            func(value *AuthorityCapsuleV1) { value.ActorID = "" },
		"process":          func(value *AuthorityCapsuleV1) { value.ProcessID = "" },
		"run":              func(value *AuthorityCapsuleV1) { value.RunID = "" },
		"lease":            func(value *AuthorityCapsuleV1) { value.LeaseID = "" },
		"workspace":        func(value *AuthorityCapsuleV1) { value.WorkspaceID = "" },
		"epoch":            func(value *AuthorityCapsuleV1) { value.RuntimeEpochID = "" },
		"lease generation": func(value *AuthorityCapsuleV1) { value.LeaseGeneration = 0 },
		"fence":            func(value *AuthorityCapsuleV1) { value.FenceToken = 0 },
		"head revision":    func(value *AuthorityCapsuleV1) { value.AuthorityHeadRecordRevision = 0 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			value := validAuthorityCapsule()
			mutate(&value)
			if _, err := NewAuthorityCapsuleV1(value); err == nil {
				t.Fatal("constructor accepted authority mutant")
			}
			encoded, _ := json.Marshal(authorityCapsuleWireV1{
				SchemaVersion: value.SchemaVersion, ProjectID: string(value.ProjectID),
				MissionID: string(value.MissionID), WorkRevisionID: string(value.WorkRevisionID),
				ActorID: string(value.ActorID), ProcessID: string(value.ProcessID),
				RunID: string(value.RunID), LeaseID: string(value.LeaseID),
				WorkspaceID: string(value.WorkspaceID), RuntimeEpochID: string(value.RuntimeEpochID),
				LeaseGeneration: value.LeaseGeneration, FenceToken: value.FenceToken,
				AuthorityHeadRecordRevision: value.AuthorityHeadRecordRevision,
			})
			if _, err := DecodeAuthorityCapsuleV1(encoded); err == nil {
				t.Fatal("decoder accepted authority mutant")
			}
		})
	}
}
