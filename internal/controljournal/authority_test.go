package controljournal

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
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
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("authority test path unavailable")
	}
	encoded, err := os.ReadFile(filepath.Join(filepath.Dir(current), "..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", "authority-capsule-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Properties) != 13 || len(schema.Required) != len(schema.Properties) {
		t.Fatalf("authority schema is not a closed thirteen-field object: %+v", schema)
	}
	schemaProperties := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		schemaProperties = append(schemaProperties, name)
	}
	sort.Strings(schemaProperties)
	required := append([]string(nil), schema.Required...)
	sort.Strings(required)
	if !reflect.DeepEqual(required, schemaProperties) {
		t.Fatalf("authority schema required/property drift required=%v properties=%v", required, schemaProperties)
	}
	wireType := reflect.TypeOf(authorityCapsuleWireV1{})
	wireProperties := make([]string, wireType.NumField())
	for index := range wireProperties {
		wireProperties[index] = wireType.Field(index).Tag.Get("json")
	}
	sortedWire := append([]string(nil), wireProperties...)
	sort.Strings(sortedWire)
	if !reflect.DeepEqual(sortedWire, schemaProperties) {
		t.Fatalf("authority runtime/schema field drift runtime=%v schema=%v", sortedWire, schemaProperties)
	}
	if !reflect.DeepEqual(wireProperties, sortedWire) {
		t.Fatalf("authority wire declaration is not lexicographic: %v", wireProperties)
	}
	if reflect.TypeOf(AuthorityCapsuleV1{}).NumField() != len(schemaProperties) {
		t.Fatalf("authority semantic field count does not mirror schema")
	}
	for _, name := range schemaProperties {
		if _, present := schema.Properties[name]; !present {
			t.Fatalf("schema property disappeared during mirror: %s", name)
		}
	}
}

func TestAuthorityCapsuleCanonicalRoundTripAndClosedObject(t *testing.T) {
	capsule := validAuthorityCapsule()
	encoded, err := EncodeAuthorityCapsuleV1(capsule)
	if err != nil {
		t.Fatal(err)
	}
	keys := jsonObjectKeys(t, encoded)
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Fatalf("authority encoding keys are not lexicographic: %v", keys)
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

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	reversed := append([]string(nil), sorted...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	mutants := map[string][]byte{
		"reordered":  marshalRawObject(t, raw, reversed),
		"whitespace": append([]byte(" "), encoded...),
		"duplicate":  append([]byte(`{"actor_id":"actor-1",`), encoded[1:]...),
		"trailing":   append(append([]byte(nil), encoded...), ' '),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAuthorityCapsuleV1(mutant); err == nil {
				t.Fatal("accepted noncanonical authority bytes")
			}
		})
	}
}

func jsonObjectKeys(t *testing.T, encoded []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("not an object: token=%v err=%v", token, err)
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatalf("non-string object key: %v", token)
		}
		keys = append(keys, key)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		t.Fatalf("object close token=%v err=%v", token, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("trailing object token err=%v", err)
	}
	return keys
}

func marshalRawObject(t *testing.T, values map[string]json.RawMessage, order []string) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoded.WriteByte('{')
	for index, key := range order {
		value, present := values[key]
		if !present {
			t.Fatalf("missing raw key %s", key)
		}
		if index > 0 {
			encoded.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(key)
		encoded.Write(keyBytes)
		encoded.WriteByte(':')
		encoded.Write(value)
	}
	encoded.WriteByte('}')
	return encoded.Bytes()
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
