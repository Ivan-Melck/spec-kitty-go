package jsonschema

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// testdata/agent_profile_golden.json is validate_agent_profile_yaml output
// from the oracle's python-jsonschema 4.26 (work-DOC/gen_js.py).
func TestAgentProfileMessagesMatchPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/agent_profile_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Instance json.RawMessage
		Errors   []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	schema, err := yamlx.ParseObject(assets.MustRead("charter/offering/schemas/agent-profile.schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v := New(schema)
	for _, c := range cases {
		inst, err := pyjson.LoadsBytes(c.Instance)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range v.IterErrors(inst) {
			got = append(got, e.PathString()+": "+e.Message)
		}
		if len(got) == 0 && len(c.Errors) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Errors) {
			t.Errorf("instance %s:\n got  %q\n want %q", c.Instance, got, c.Errors)
		}
	}
}
