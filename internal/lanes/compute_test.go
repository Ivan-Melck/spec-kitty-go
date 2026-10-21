package lanes

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

type goldenCase struct {
	Name      string              `json:"name"`
	Slug      string              `json:"slug"`
	MissionID *string             `json:"mission_id"`
	Target    string              `json:"target"`
	Graph     map[string][]string `json:"graph"`
	Ownership map[string]struct {
		Mode  string
		Files []string
	} `json:"ownership"`
	Bodies   map[string]string `json:"bodies"`
	Topology string            `json:"topology"`
	MBranch  string            `json:"mission_branch"`
	Previous json.RawMessage   `json:"previous"`
	Frozen   *struct {
		Bindings map[string]string `json:"bindings"`
		Retired  []string          `json:"retired"`
		Reserved []string          `json:"reserved"`
	} `json:"frozen"`
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// TestComputeGolden compares Compute with compute_lanes outputs generated
// from the oracle's Python package (testdata/compute_golden.json).
func TestComputeGolden(t *testing.T) {
	cb, err := os.ReadFile("testdata/compute_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	gb, err := os.ReadFile("testdata/compute_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(cb, &cases); err != nil {
		t.Fatal(err)
	}
	gv, err := jsonx.Decode(gb)
	if err != nil {
		t.Fatal(err)
	}
	golden := gv.([]any)
	for i, c := range cases {
		in := ComputeInput{DependencyGraph: c.Graph, Ownership: map[string]*Ownership{}, MissionSlug: c.Slug, TargetBranch: c.Target,
			WPBodies: c.Bodies, MissionID: c.MissionID, Topology: c.Topology, MissionBranch: c.MBranch}
		for k, v := range c.Ownership {
			in.Ownership[k] = &Ownership{ExecutionMode: v.Mode, OwnedFiles: v.Files}
		}
		if len(c.Previous) > 0 {
			dir := t.TempDir()
			os.WriteFile(dir+"/lanes.json", c.Previous, 0o644)
			prev, err := Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			in.Previous = prev
		}
		if c.Frozen != nil {
			in.Frozen = &FrozenMembership{Bindings: c.Frozen.Bindings, Retired: set(c.Frozen.Retired), ReservedIDs: set(c.Frozen.Reserved)}
		}
		want := golden[i].(*jsonx.Object)
		m, err := Compute(in)
		if e, ok := want.Get("err"); ok {
			got := ""
			switch x := err.(type) {
			case *ComputationError:
				got = "LaneComputationError: " + x.Error()
			case *MembershipFrozenError:
				got = "LaneMembershipFrozenError: " + x.Error()
			case *DependencyCycleError:
				got = "LaneDependencyCycleError: " + x.Error()
			case nil:
				got = "<nil>"
			default:
				got = x.Error()
			}
			if got != e {
				t.Errorf("%s: error\n got %q\nwant %q", c.Name, got, e)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		m.ComputedAt = "X"
		w := jsonx.Dumps(want.GetObject("ok"), jsonx.Pretty)
		g := jsonx.Dumps(m.ToObject(), jsonx.Pretty)
		if w != g {
			t.Errorf("%s: manifest differs\nwant %s\n got %s", c.Name, w, g)
		}
	}
}
