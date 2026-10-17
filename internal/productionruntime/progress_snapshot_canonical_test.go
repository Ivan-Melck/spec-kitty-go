package productionruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Immutable historical oracle: public Marshal -> UseNumber map -> Marshal.
// Do not replace this with the production view or a durable codec.
func legacyProgressPreimage(value MissionProgress) ([]byte, error) {
	value.SnapshotSHA256 = ""
	wire, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return json.Marshal(object)
}

func legacyProgressDigest(value MissionProgress) (string, error) {
	canonical, err := legacyProgressPreimage(value)
	if err != nil {
		return "", err
	}
	if len(canonical) > MaxProgressSnapshotBytes-sha256.Size*2 {
		return "", progressSnapshotBudgetError()
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty.reducer-state.v2"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func TestProgressCanonicalSchemaBijectionAndLexicalTags(t *testing.T) {
	pairs := map[reflect.Type]reflect.Type{
		reflect.TypeFor[MissionProgress]():    reflect.TypeFor[canonicalMissionProgress](),
		reflect.TypeFor[ProgressItem]():       reflect.TypeFor[canonicalProgressItem](),
		reflect.TypeFor[ActionAvailability](): reflect.TypeFor[canonicalActionAvailability](),
		reflect.TypeFor[StateCount]():         reflect.TypeFor[canonicalStateCount](),
		reflect.TypeFor[RunningAuthority]():   reflect.TypeFor[canonicalRunningAuthority](),
	}
	for public, private := range pairs {
		t.Run(public.Name(), func(t *testing.T) {
			if public.NumField() != private.NumField() {
				t.Fatalf("field count: public=%d private=%d", public.NumField(), private.NumField())
			}
			seen := map[string]bool{}
			previous := ""
			for i := 0; i < private.NumField(); i++ {
				field := private.Field(i)
				tag := field.Tag.Get("json")
				key := strings.Split(tag, ",")[0]
				if !field.IsExported() || key == "" || key == "-" || key <= previous || seen[key] {
					t.Fatalf("ignored, duplicate or unordered private field %s tag=%q previous=%q", field.Name, tag, previous)
				}
				seen[key], previous = true, key
				peer, ok := public.FieldByName(field.Name)
				if !ok || !peer.IsExported() || peer.Tag.Get("json") != tag {
					t.Fatalf("no exact public field/tag for %s: %q", field.Name, tag)
				}
				want := peer.Type
				if mapped, ok := pairs[want]; ok {
					want = mapped
				} else if want.Kind() == reflect.Slice {
					if mapped, ok := pairs[want.Elem()]; ok {
						want = reflect.SliceOf(mapped)
					}
				}
				if field.Type != want {
					t.Fatalf("type drift %s: got=%v want=%v", field.Name, field.Type, want)
				}
			}
		})
	}
}

// Reflection is fixture-only, and gives each field a distinct nonzero value.
func populateProgressField(value reflect.Value, path string, maximum bool) {
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			populateProgressField(value.Field(i), path+"."+value.Type().Field(i).Name, maximum)
		}
	case reflect.String:
		value.SetString(path + "<>&\"\\\x00☃\u2028\u2029")
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Uint32, reflect.Uint64:
		n := uint64(len(path)*31 + 1)
		if maximum {
			n = math.MaxUint64
			if value.Kind() == reflect.Uint32 {
				n = math.MaxUint32
			}
		}
		value.SetUint(n)
	case reflect.Int:
		value.SetInt(-int64(len(path)*17 + 1))
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			// Unknown/noncanonical business JSON and arbitrary bytes stay opaque.
			value.SetBytes(append([]byte(`{"unknown":{"z":1,"a":2},"dup":1,"dup":2}`), 0, 0xff, 0xfe))
			return
		}
		value.Set(reflect.MakeSlice(value.Type(), 2, 2))
		for i := 0; i < value.Len(); i++ {
			populateProgressField(value.Index(i), fmt.Sprintf("%s[%d]", path, i), maximum)
		}
	default:
		panic("unhandled public fixture type: " + value.Type().String())
	}
}

func assertProgressPreimage(t *testing.T, value MissionProgress) {
	t.Helper()
	before, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want, err := legacyProgressPreimage(value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(canonicalMissionProgressView(value))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("preimage mismatch err=%v\ngot=%s\nwant=%s", err, got, want)
	}
	wantDigest, err := legacyProgressDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	gotDigest, err := progressSnapshotDigest(value)
	if err != nil || gotDigest != wantDigest {
		t.Fatalf("digest got=%s want=%s err=%v", gotDigest, wantDigest, err)
	}
	after, err := json.Marshal(value)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("caller mutated: %v", err)
	}
}

func TestProgressCanonicalEachPublicFieldConversion(t *testing.T) {
	root := reflect.TypeFor[MissionProgress]()
	for i := 0; i < root.NumField(); i++ {
		field := root.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			var value MissionProgress
			populateProgressField(reflect.ValueOf(&value).Elem().Field(i), field.Name, false)
			assertProgressPreimage(t, value)
		})
	}
	// Isolate every nested mapping, not just nested values within full arrays.
	for _, name := range []string{"Items", "Actions", "WorkCounts", "Running"} {
		field, _ := root.FieldByName(name)
		for i := 0; i < field.Type.Elem().NumField(); i++ {
			nested := field.Type.Elem().Field(i)
			t.Run(name+"."+nested.Name, func(t *testing.T) {
				var value MissionProgress
				array := reflect.ValueOf(&value).Elem().FieldByName(name)
				array.Set(reflect.MakeSlice(array.Type(), 1, 1))
				populateProgressField(array.Index(0).Field(i), name+"."+nested.Name, false)
				assertProgressPreimage(t, value)
			})
		}
	}
}

func TestProgressCanonicalZeroEmptyMaximumAndMultiplicity(t *testing.T) {
	assertProgressPreimage(t, MissionProgress{})
	var empty MissionProgress
	root := reflect.ValueOf(&empty).Elem()
	for i := 0; i < root.NumField(); i++ {
		if root.Field(i).Kind() == reflect.Slice {
			root.Field(i).Set(reflect.MakeSlice(root.Field(i).Type(), 0, 0))
		}
	}
	assertProgressPreimage(t, empty)
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte(`{"unknownDurableBusiness":true}`)} {
		for _, list := range [][]string{nil, {}, {"<>&\x00☃", "duplicate", "duplicate"}} {
			assertProgressPreimage(t, MissionProgress{
				Items:   []ProgressItem{{CanonicalState: raw}},
				Actions: []ActionAvailability{{BlockingPredicates: list, ResponsibleProducers: list}},
			})
		}
	}
	var maximum MissionProgress
	populateProgressField(reflect.ValueOf(&maximum).Elem(), "root", true)
	assertProgressPreimage(t, maximum)
	// Arrays retain business order, repeated entries, and all destinations.
	maximum.Items = make([]ProgressItem, MaxProgressItems)
	for i := range maximum.Items {
		maximum.Items[i] = ProgressItem{Family: "operation", AggregateID: fmt.Sprint(MaxProgressItems - i), LogicalVersion: math.MaxUint64, CanonicalState: []byte{0xff, 0, '<', '>'}}
	}
	maximum.Operations = maximum.Items
	maximum.PendingReconciliation = maximum.Items
	assertProgressPreimage(t, maximum)
}

func TestProgressCanonicalViewSharesOpaquePayloadWithoutCopy(t *testing.T) {
	value := MissionProgress{Items: []ProgressItem{{CanonicalState: []byte{1, 2, 3}}}, SnapshotSHA256: "caller-hash"}
	view := canonicalMissionProgressView(value)
	if &view.Items[0].CanonicalState[0] != &value.Items[0].CanonicalState[0] || view.SnapshotSHA256 != "" || value.SnapshotSHA256 != "caller-hash" {
		t.Fatal("view copied opaque bytes or changed hash ownership")
	}
}

// Controlled allocation comparison only, not p95 qualification. Both paths
// hash exactly the same public value; setup is excluded from timed work.
func BenchmarkProgressCanonicalDigest(b *testing.B) {
	for _, count := range []int{64, MaxProgressItems} {
		value := MissionProgress{Complete: true, ProjectionRevision: math.MaxUint64, Generation: math.MaxUint64, Items: make([]ProgressItem, count)}
		for i := range value.Items {
			value.Items[i] = ProgressItem{Family: "operation", AggregateID: fmt.Sprint(i), LogicalVersion: math.MaxUint64, CanonicalState: []byte(`{"unknownDurableBusiness":"<>&☃","stage":"pending_reconciliation"}`)}
		}
		value.Operations, value.PendingReconciliation = value.Items, value.Items
		for _, path := range []struct {
			name   string
			digest func(MissionProgress) (string, error)
		}{{"legacy", legacyProgressDigest}, {"canonical", progressSnapshotDigest}} {
			b.Run(fmt.Sprintf("items=%d/%s", count, path.name), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := path.digest(value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
