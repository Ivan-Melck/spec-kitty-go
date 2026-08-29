package readmodel

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestSnapshotValidation(t *testing.T) {
	valid := SnapshotParams{MissionID: "mission-1", StateVersion: 3, CurrentStep: "implementation", CurrentWorkRevisionID: "revision-2"}
	snapshot, err := NewSnapshot(valid)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MissionID() != valid.MissionID || snapshot.StateVersion() != 3 || snapshot.CurrentStep() != "implementation" || snapshot.CurrentWorkRevisionID() != valid.CurrentWorkRevisionID {
		t.Fatalf("snapshot lost facts: %#v", snapshot)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SnapshotParams){
		"mission":  func(value *SnapshotParams) { value.MissionID = ids.MissionID("../bad") },
		"version":  func(value *SnapshotParams) { value.StateVersion = 0 },
		"step":     func(value *SnapshotParams) { value.CurrentStep = " bad" },
		"revision": func(value *SnapshotParams) { value.CurrentWorkRevisionID = "bad/value" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if _, err := NewSnapshot(candidate); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}

func TestSnapshotAllowsExplicitlyAbsentRevision(t *testing.T) {
	value, err := NewSnapshot(SnapshotParams{MissionID: "mission-1", StateVersion: 1, CurrentStep: "specify"})
	if err != nil || value.CurrentWorkRevisionID() != "" {
		t.Fatalf("absent revision should be representable: %#v %v", value, err)
	}
}
