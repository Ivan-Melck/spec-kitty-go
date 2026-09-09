package hubnot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func richContent(t *testing.T, work string) workContent {
	t.Helper()
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"large":123456789012345678901234567890,"nested":{"null":null,"list":[true,"雪",1.234567890123456789e-80]}}`))
	if err != nil {
		t.Fatal(err)
	}
	def, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project", WorkID: ids.WorkID(work), Title: "Readable title", Description: "Detailed body", DesignContext: "Design reason", CrossDomainReason: "Cross-domain reason",
		Criteria:     []tracker.WorkCriterion{{ID: "z-first", Text: "first", Outcome: "observed", Verifier: tracker.VerifierKind("test"), Required: true, EvidenceHint: "proof"}, {ID: "a-second", Text: "second", Outcome: "observed", Verifier: tracker.VerifierKind("test")}},
		Dependencies: []ids.WorkID{"original-supplier"}, WriteScopes: []tracker.WriteScope{{Path: "internal/example", Domain: "domain", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "FR-002", Kind: tracker.ScopeRequiredOutcome}}, MetadataSchema: 1, ProviderMetadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := fromDefinition(def, "create-"+ids.OperationID(work))
	if err != nil {
		t.Fatal(err)
	}
	return content
}
func foreignState() issueState {
	return issueState{Title: "old", Body: "old", Status: "closed", Labels: []string{"foreign"}, Assignees: []string{"human"}, Relations: []issueRelation{{Kind: "related", Target: "sha256:" + strings.Repeat("b", 64)}}, Metadata: map[string]string{"foreign/key": "original"}}
}
func TestCodecCompleteMeaningAndForeignPreservation(t *testing.T) {
	content := richContent(t, "work")
	base := foreignState()
	before, _ := json.Marshal(base)
	encoded, err := encodeWorkState(base, content)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("encoding modified caller state")
	}
	got, mapped, err := decodeWorkState(encoded)
	if err != nil || !mapped {
		t.Fatalf("decode: %v %v", mapped, err)
	}
	if !reflect.DeepEqual(got, content) {
		t.Fatalf("complete meaning differs\ngot %#v\nwant %#v", got, content)
	}
	if encoded.Status != base.Status || !reflect.DeepEqual(encoded.Labels, base.Labels) || !reflect.DeepEqual(encoded.Assignees, base.Assignees) || !reflect.DeepEqual(encoded.Relations, base.Relations) || encoded.Metadata["foreign/key"] != "original" {
		t.Fatal("foreign native state lost")
	}
	encoded.Metadata["foreign/key"] = "edited"
	encoded.Labels[0] = "edited"
	if base.Metadata["foreign/key"] != "original" || base.Labels[0] != "foreign" {
		t.Fatal("state aliases caller memory")
	}
	got.Criteria[0].Text = "changed"
	again, _, err := decodeWorkState(encoded)
	if err != nil || again.Criteria[0].Text != content.Criteria[0].Text {
		t.Fatal("decoded content aliases")
	}
}
func TestCodecOpaqueStrictPrecisionAndDeepNesting(t *testing.T) {
	raw, err := os.ReadFile("testdata/codec-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, JSON string
		Valid      bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		Name, JSON string
		Valid      bool
	}{"depth-100", `{"deep":` + strings.Repeat("[", 100) + `null` + strings.Repeat("]", 100) + `}`, true})
	cases = append(cases, struct {
		Name, JSON string
		Valid      bool
	}{"invalid-utf8", string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}), false})
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			canonical, err := canonicalPayloadJSON([]byte(test.JSON))
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%v err=%v", test.Valid, err)
			}
			if err != nil {
				return
			}
			content := richContent(t, "work")
			content.Opaque = canonical
			state, err := encodeWorkState(foreignState(), content)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := decodeWorkState(state)
			if err != nil || !bytes.Equal(got.Opaque, canonical) {
				t.Fatalf("opaque fidelity lost: %s %v", got.Opaque, err)
			}
		})
	}
}
func TestCodecRefusesContradictionsAndCapacityWithoutChangingInput(t *testing.T) {
	content := richContent(t, "work")
	state, err := encodeWorkState(foreignState(), content)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "body", "criterion-order", "schema", "digest", "missing-chunk", "extra-chunk"} {
		t.Run(field, func(t *testing.T) {
			bad := cloneIssueState(state)
			switch field {
			case "title":
				bad.Title = "human edit"
			case "body":
				bad.Body = "human edit"
			case "criterion-order":
				bad.Criteria[0], bad.Criteria[1] = bad.Criteria[1], bad.Criteria[0]
			case "schema":
				bad.Metadata["go-kitty/schema"] = "unknown"
			case "digest":
				bad.Metadata["go-kitty/digest"] = strings.Repeat("0", 64)
			case "missing-chunk":
				delete(bad.Metadata, "go-kitty/chunk-000")
			case "extra-chunk":
				bad.Metadata["go-kitty/chunk-999"] = "x"
			}
			if _, _, err := decodeWorkState(bad); err == nil {
				t.Fatal("accepted contradictory owned state")
			}
		})
	}
	full := foreignState()
	for i := 0; i < 64; i++ {
		full.Metadata["foreign/"+strings.Repeat("x", i+1)] = "retained"
	}
	before, _ := json.Marshal(full)
	if _, err := encodeWorkState(full, content); err == nil {
		t.Fatal("foreign capacity silently discarded")
	}
	after, _ := json.Marshal(full)
	if !bytes.Equal(before, after) {
		t.Fatal("refusal mutated input")
	}
}

func TestCodecPreservesDeepestSupportedOpaqueAndOperation(t *testing.T) {
	deep := []byte(`{"deep":` + strings.Repeat("[", 9999) + `null` + strings.Repeat("]", 9999) + `}`)
	c := richContent(t, "work")
	c.Opaque = deep
	c.Operation = deep
	state, err := encodeWorkState(foreignState(), c)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := decodeWorkState(state)
	if err != nil || !bytes.Equal(got.Opaque, deep) || !bytes.Equal(got.Operation, deep) {
		t.Fatalf("deep payload damaged: %v", err)
	}
}
func TestCodecRefusesMalformedOwnedBaseAndAllowsSimpleWithoutCriteria(t *testing.T) {
	base := foreignState()
	base.Metadata["go-kitty/schema"] = "future"
	before, _ := json.Marshal(base)
	if _, err := encodeWorkState(base, richContent(t, "work")); err == nil {
		t.Fatal("overwrote unknown owned schema")
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated rejected base")
	}
	c, err := fromCatalog(tracker.CreateWorkRequest{ProjectID: "project", WorkID: "simple", OperationID: "create", Title: "Simple", MetadataSchema: 1})
	if err != nil {
		t.Fatal(err)
	}
	state, err := encodeWorkState(issueState{}, c)
	if err != nil {
		t.Fatal(err)
	}
	got, mapped, err := decodeWorkState(state)
	if err != nil || !mapped || got.Complete || len(got.Criteria) != 0 {
		t.Fatalf("simple empty criteria unsupported: %v", err)
	}
}

func TestCodecUTF8ChunkAndForeignCapacityBoundaries(t *testing.T) {
	c := richContent(t, "work")
	raw, err := json.Marshal(map[string]string{"text": strings.Repeat("雪", 5000)})
	if err != nil {
		t.Fatal(err)
	}
	c.Opaque = raw
	state, err := encodeWorkState(issueState{}, c)
	if err != nil {
		t.Fatal(err)
	}
	chunks := 0
	for key, value := range state.Metadata {
		if strings.Contains(key, "chunk-") {
			chunks++
			if len(value) > 4096 || !utf8.ValidString(value) {
				t.Fatal("chunk split UTF-8 or exceeded native bytes")
			}
		}
	}
	if chunks < 2 {
		t.Fatal("large payload was not chunked")
	}
	base := issueState{Metadata: map[string]string{}}
	for i := 0; i < 64-len(state.Metadata); i++ {
		base.Metadata[fmt.Sprintf("foreign/k%d", i)] = "retained"
	}
	exact, err := encodeWorkState(base, c)
	if err != nil || len(exact.Metadata) != 64 {
		t.Fatalf("exact key limit: %v", err)
	}
	base.Metadata["foreign/extra"] = "retained"
	if _, err := encodeWorkState(base, c); err == nil {
		t.Fatal("accepted overfull key set")
	}
	huge := issueState{Metadata: map[string]string{}}
	for i := 0; i < 40; i++ {
		huge.Metadata[fmt.Sprintf("foreign/k%d", i)] = strings.Repeat("\"", 4096)
	}
	before, _ := json.Marshal(huge)
	if _, err := encodeWorkState(huge, richContent(t, "work")); err == nil {
		t.Fatal("accepted oversized encoded state")
	}
	after, _ := json.Marshal(huge)
	if !bytes.Equal(before, after) {
		t.Fatal("capacity refusal changed foreign values")
	}
}

func TestCodecStoredJSONTextCannotDefault(t *testing.T) {
	for _, test := range []struct{ field, value string }{{"opaque", ""}, {"opaque", " "}, {"opaque", "null"}, {"operation", ""}, {"operation", "null"}} {
		t.Run(test.field+test.value, func(t *testing.T) {
			c := richContent(t, "work")
			state, err := encodeWorkState(foreignState(), c)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := contentData(c)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			object[test.field], _ = json.Marshal(test.value)
			raw, _ = json.Marshal(object)
			raw, err = canonicalPayloadJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			for key := range state.Metadata {
				if strings.HasPrefix(key, ownedPrefix+"chunk-") {
					delete(state.Metadata, key)
				}
			}
			state.Metadata[ownedPrefix+"chunks"] = "1"
			state.Metadata[ownedPrefix+"chunk-000"] = string(raw)
			state.Metadata[ownedPrefix+"bytes"] = strconv.Itoa(len(raw))
			sum := sha256.Sum256(raw)
			state.Metadata[ownedPrefix+"digest"] = hex.EncodeToString(sum[:])
			if _, _, err := decodeWorkState(state); err == nil {
				t.Fatal("stored invalid JSON text silently defaulted")
			}
		})
	}
}
func TestCodecOrdinarySerializationCannotLoseOpaqueOrOperation(t *testing.T) {
	a := richContent(t, "work")
	a.Operation = json.RawMessage(`{"semantic_digest":"first"}`)
	first, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b := cloneContent(a)
	b.Opaque = json.RawMessage(`{"different":true}`)
	second, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ordinary serialization lost opaque JSON")
	}
	b = cloneContent(a)
	b.Operation = json.RawMessage(`{"semantic_digest":"second"}`)
	second, err = json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ordinary serialization lost operation")
	}
	explicit, err := contentData(a)
	if err != nil || !bytes.Equal(first, explicit) {
		t.Fatal("ordinary and complete framing differ")
	}
}
