package productionruntime

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Companion to gapdb's named JSON ledger. Concrete private schemas, not generic
// Core/Marshal behavior, justify reconstructed inputs staying below4096.
func TestRetainedPrivateJSONTrustedInputBounds(t *testing.T) {
	binding := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: journalv2.HostAuthorityID(strings.Repeat("h", 128)), DatabaseID: journalv2.DatabaseID(strings.Repeat("d", 128)), AdmittedTupleSHA: strings.Repeat("a", 64), SidecarSHA: strings.Repeat("b", 64), WireProtocol: math.MaxUint64, StorageProtocol: math.MaxUint64, CreatedOperationID: strings.Repeat("o", 128)}
	if _, err := journalv2.NewHostAuthorityID(string(binding.HostAuthorityID)); err != nil {
		t.Fatal(err)
	}
	if _, err := journalv2.NewOperationID(binding.CreatedOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := journalv2.NewStoreRevisionV2(binding.DatabaseID, math.MaxUint64, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	current := runtimeCurrentV2{SchemaVersion: 2, RuntimeEpochID: journalv2.RuntimeEpochID("epoch-" + strings.Repeat("f", 32)), PredecessorEpochID: journalv2.RuntimeEpochID("epoch-" + strings.Repeat("e", 32)), HostAuthorityID: binding.HostAuthorityID, DatabaseID: binding.DatabaseID, RecoveredStoreRevision: math.MaxUint64 - 1, ProjectionSHA256: strings.Repeat("c", 64), CreatedOperationID: "runtime-admit-" + strings.Repeat("d", 32), RecoveryAttemptID: "restore-" + strings.Repeat("a", 32), Status: "ready"}
	if !retainedPrivateCurrentValid(current, binding, 1, math.MaxUint64) {
		t.Fatal("maximal schema input invalid")
	}
	type wrapper struct {
		SchemaVersion uint32           `json:"schema_version"`
		Current       runtimeCurrentV2 `json:"current"`
	}
	for _, value := range []any{binding, current, wrapper{2, current}} {
		retainedPrivateJSONNoHooks(t, reflect.TypeOf(value))
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > 4096 {
			t.Fatal("trusted Marshal input bound", len(raw), err)
		}
	}
	for _, row := range []struct {
		name    string
		maximum int
		build   func() ([]byte, error)
	}{
		{"binding", 722, binding.canonical},
		{"current", 737, func() ([]byte, error) { return retainedPrivateCanonical(current) }},
		{"wrapper", 768, func() ([]byte, error) { return runtimeEpochRecordBytes(current) }},
		{"head", 97, func() ([]byte, error) { return retainedPrivateHeadBytes(current.RuntimeEpochID) }},
		{"operation", 164, func() ([]byte, error) { return retainedPrivateOperationBytes(current) }},
		{"event", 187, func() ([]byte, error) { return retainedPrivateEventBytes(current) }},
	} {
		raw, err := row.build()
		if err != nil || len(raw) > row.maximum {
			t.Fatal("source-generated private size", row.name, len(raw), row.maximum, err)
		}
		t.Logf("trusted %s maximum encoded bytes=%d; not heap slots", row.name, len(raw))
	}
}
func retainedPrivateJSONNoHooks(t *testing.T, typ reflect.Type) {
	t.Helper()
	for _, candidate := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		for _, contract := range []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler]()} {
			if candidate.Implements(contract) {
				t.Fatal("custom JSON/text callback invalidates fixed schema recipe", candidate, contract)
			}
		}
	}
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			retainedPrivateJSONNoHooks(t, typ.Field(i).Type)
		}
	} else if typ.Kind() != reflect.String && typ.Kind() != reflect.Uint32 && typ.Kind() != reflect.Uint64 {
		t.Fatal("untraced private schema kind", typ)
	}
}

func TestRetainedPrivateJSONActualFailedPrefixes(t *testing.T) {
	work := retainedPrivateWork{ctx: context.Background(), policy: defaultRetainedPrivatePolicy()}
	for _, n := range []int{1, 1024, 1025, 3072, 3073, 4096} {
		var current runtimeCurrentV2
		if err := work.decode(bytes.Repeat([]byte{' '}, n), &current); err == nil {
			t.Fatal("whitespace bad prefix accepted", n)
		}
	}
	if err := work.decode(bytes.Repeat([]byte{' '}, 4097), new(runtimeCurrentV2)); err == nil {
		t.Fatal("oversized input reached accepted decode")
	}
}

func TestRetainedPrivateJSONAliasFailureReconstruction(t *testing.T) {
	// Canonical case aliases are retained by generic Core but folded by typed
	// JSON decoding. A last bad wrapper can therefore reach BOTH its input
	// Core fallback and reconstruction fallback before refusal. Healthy-path
	// fallback counts must NOT silently be reused for every failure prefix.
	f := newRetainedPrivateFixture(t, 64)
	oldest := f.current[0]
	key := runtimeEpochRecordKey(oldest.RuntimeEpochID)
	row := f.reader.rows[key]
	var object map[string]any
	if err := json.Unmarshal(row.Value, &object); err != nil {
		t.Fatal(err)
	}
	object["CURRENT"] = map[string]any{"status": "<"}
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil || !bytes.Contains(canonical, []byte(`\u003c`)) || len(canonical) > 4096 {
		t.Fatal("canonical escaped alias fixture", err)
	}
	var decoded struct {
		SchemaVersion uint32           `json:"schema_version"`
		Current       runtimeCurrentV2 `json:"current"`
	}
	work := retainedPrivateWork{ctx: context.Background(), policy: defaultRetainedPrivatePolicy()}
	if err := work.decode(canonical, &decoded); err != nil || decoded.Current != oldest {
		t.Fatal("bad prefix failed before reconstruction", err)
	}
	rebuilt, err := runtimeEpochRecordBytes(decoded.Current)
	if err != nil || bytes.Equal(rebuilt, canonical) {
		t.Fatal("alias did not require reconstruction refusal", err)
	}
	row.Value = canonical
	f.reader.rows[key] = row
	got, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
	retainedPrivateAssertRefused(t, got, err)
	if len(f.reader.trace) != 193 || f.reader.trace[len(f.reader.trace)-1] != key {
		t.Fatal("wrong final independently accepted prefix", len(f.reader.trace))
	}
	t.Log("source-valid 193-row fake-reader prefix refused after input canonicalization and reconstruction; no native receipt, fallback-count telemetry or memory measurement")
}
