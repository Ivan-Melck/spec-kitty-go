package workspace

import (
	"bytes"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestWorkspaceBindingPathAndClaimAreImmutableV2(t *testing.T) {
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BindingV2){
		"workspace":     func(v *BindingV2) { v.WorkspaceID = "workspace-other" },
		"project":       func(v *BindingV2) { v.ProjectID = "project-other" },
		"mission":       func(v *BindingV2) { v.MissionID = "mission-other" },
		"work revision": func(v *BindingV2) { v.WorkRevisionID = "revision-other" },
		"work":          func(v *BindingV2) { v.WorkID = "work-other" },
		"run":           func(v *BindingV2) { v.RunID = "run-other" },
		"repository": func(v *BindingV2) {
			v.RepositoryID = "repository-other"
			v.PathClaimDigest, _ = WorkspacePathClaimDigestV2(v.RepositoryID, v.ManagedRoot, v.CanonicalPath)
		},
		"lease":      func(v *BindingV2) { v.LeaseID = "lease-other" },
		"generation": func(v *BindingV2) { v.Generation++ },
		"fence":      func(v *BindingV2) { v.Fence = strings.Repeat("b", 32) },
		"epoch":      func(v *BindingV2) { v.RuntimeEpochID = "epoch-other" },
		"actor":      func(v *BindingV2) { v.ActorID = "actor-other" },
		"process":    func(v *BindingV2) { v.ProcessID = "process-other" },
		"lane":       func(v *BindingV2) { v.LaneID = "lane-other" },
		"managed root": func(v *BindingV2) {
			v.ManagedRoot = "/srv/spec-kitty"
			v.PathClaimDigest, _ = WorkspacePathClaimDigestV2(v.RepositoryID, v.ManagedRoot, v.CanonicalPath)
		},
		"path": func(v *BindingV2) {
			v.CanonicalPath = "/srv/spec-kitty/workspaces/other"
			v.PathClaimDigest, _ = WorkspacePathClaimDigestV2(v.RepositoryID, v.ManagedRoot, v.CanonicalPath)
		},
		"branch": func(v *BindingV2) { v.OwnedBranch = "kitty/other" },
		"base":   func(v *BindingV2) { v.PinnedBase = strings.Repeat("b", 40) },
		"dependencies": func(v *BindingV2) {
			v.Dependencies = []DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("c", 40)}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			prior := StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: binding, Stage: StageCreationRequestedV2, EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
			next := prior
			next.LogicalVersion++
			next.Observation = ObservationV2{Registered: true}
			mutate(&next.Binding)
			next.Stage = StageWorktreeObservedV2
			envelope := workspaceEnvelopeFixtureV2(t, prior, next, "preparation_observed", "event-next")
			if _, err := ReduceEventV2(prior.CanonicalBytes(), envelope); !IsKind(err, KindWorkspaceConflict) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestWorkspaceCanonicalPathRefusesAliasesAndOutsideRootV2(t *testing.T) {
	for _, value := range []string{"relative/w1", "/srv/spec-kitty/workspaces", "/srv/spec-kitty/other/w1", "/srv/spec-kitty/workspaces/../w1", "/srv/spec-kitty/workspaces/w1/..", "/srv/spec-kitty/workspaces//w1", "/"} {
		if err := ValidateCanonicalPathV2("/srv/spec-kitty/workspaces", value); !IsKind(err, KindPathUnsafe) {
			t.Fatalf("%q: %v", value, err)
		}
	}
	if err := ValidateCanonicalPathV2("/srv/spec-kitty/workspaces", "/srv/spec-kitty/workspaces/w1"); err != nil {
		t.Fatal(err)
	}
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	for name, mutate := range map[string]func(*PathIdentityV2){
		"not_absolute":      func(v *PathIdentityV2) { v.Absolute = false },
		"noncanonical":      func(v *PathIdentityV2) { v.Canonical = false },
		"outside_root":      func(v *PathIdentityV2) { v.WithinManagedRoot = false },
		"aliased":           func(v *PathIdentityV2) { v.AliasFree = false },
		"symlink_ambiguous": func(v *PathIdentityV2) { v.SymlinkFree = false },
	} {
		t.Run(name, func(t *testing.T) {
			changed := binding
			mutate(&changed.PathIdentity)
			if _, err := NewBindingContributionV2(BindingContributionInputV2{Binding: changed, EffectID: "effect-create", EventID: "event-create", OccurredAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}); !IsKind(err, KindInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestWorkspaceReducerLifecycleAndQuarantineTerminalityV2(t *testing.T) {
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	state := StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: binding, Stage: StageCreationRequestedV2, EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
	steps := []struct {
		stage StageV2
		kind  string
	}{{StageWorktreeObservedV2, "preparation_observed"}, {StageBaseVerifiedV2, "preparation_advanced"}, {StageDependenciesRequestedV2, "preparation_requested"}, {StageDependenciesObservedV2, "preparation_observed"}, {StageOwnershipVerifiedV2, "preparation_advanced"}, {StageReadyV2, "ready_published"}, {StageQuarantinedV2, "quarantined"}}
	priorBytes := state.CanonicalBytes()
	for i, step := range steps {
		next := state
		next.LogicalVersion++
		next.Stage = step.stage
		next.Observation = observationForStageFixtureV2(binding, step.stage)
		next.OccurredAt = time.Date(2026, 9, 4, 12, i+1, 0, 0, time.UTC).Format(time.RFC3339Nano)
		if step.stage == StageQuarantinedV2 {
			next.QuarantineReason = QuarantineLeaseExpired
		}
		envelope := workspaceEnvelopeFixtureV2(t, state, next, step.kind, journalv2.EventID("event-step-"+string(rune('a'+i))))
		got, err := ReduceEventV2(priorBytes, envelope)
		if err != nil {
			t.Fatalf("step %s: %v", step.stage, err)
		}
		if !bytes.Equal(got, next.CanonicalBytes()) {
			t.Fatalf("step %s nondeterministic", step.stage)
		}
		state, priorBytes = next, got
	}
	resurrect := state
	resurrect.LogicalVersion++
	resurrect.Stage = StageReadyV2
	resurrect.QuarantineReason = ""
	if _, err := ReduceEventV2(priorBytes, workspaceEnvelopeFixtureV2(t, state, resurrect, "ready_published", "event-resurrect")); !IsKind(err, KindWorkspaceConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestWorkspaceReducerRejectsUnknownStageEventAndVersionsV2(t *testing.T) {
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	initial := StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: binding, Stage: StageCreationRequestedV2, EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
	unknown := initial
	unknown.Stage = "unknown"
	if err := unknown.Validate(); !IsKind(err, KindInvalid) {
		t.Fatalf("unknown stage: %v", err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	if _, err := registry.Resolve("workspace", WorkspaceEventSchemaV2+1, WorkspaceReducerV2); err == nil {
		t.Fatal("unknown event schema resolved")
	}
	if _, err := registry.Resolve("workspace", WorkspaceEventSchemaV2, WorkspaceReducerV2+1); err == nil {
		t.Fatal("unknown reducer resolved")
	}
	next := initial
	next.LogicalVersion++
	next.Stage = StageWorktreeObservedV2
	next.Observation = ObservationV2{Registered: true}
	payload, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: "preparation_observed", PriorDigest: mustStateDigestV2(t, initial), State: next})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := WorkspaceCoordinateV2(binding)
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-wrong-type", EventIndex: 0, EventCount: 1, EventType: "workspace.ready_published", Family: "workspace", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: 2, OperationID: "operation-wrong-type", OperationLogicalVersion: 1, PublicationID: journalv2.PublicationID(strings.Repeat("a", 64)), CommittedRuntimeEpochID: binding.RuntimeEpochID, ActorID: journalv2.ActorID(binding.ActorID), ProcessID: journalv2.ProcessID(binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(binding.WorkRevisionID), Payload: payload, Observations: []byte(`{}`)}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReduceEventV2(initial.CanonicalBytes(), envelope); !IsKind(err, KindInvalid) {
		t.Fatalf("event type mismatch: %v", err)
	}
}

func mustStateDigestV2(t *testing.T, state StateV2) string {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

func observationForStageFixtureV2(binding BindingV2, stage StageV2) ObservationV2 {
	observation := ObservationV2{Registered: true, Head: binding.PinnedBase}
	if stage == StageDependenciesObservedV2 || stage == StageOwnershipVerifiedV2 || stage == StageReadyV2 || stage == StageQuarantinedV2 {
		observation.Clean, observation.DependencyHead = true, binding.PinnedBase
		observation.Branch = binding.OwnedBranch
		observation.GitDirIdentity = "gitdir-identity"
		observation.IndexIdentity = "index-identity"
	}
	if stage == StageReadyV2 || stage == StageQuarantinedV2 {
		observation.TargetBefore = binding.PinnedBase
		observation.TargetAfter = binding.PinnedBase
	}
	return observation
}

func TestCurrentWorkspacePredicateUsesWholeCapsuleV2(t *testing.T) {
	b := workspaceBindingFixtureV2(t, "18", "/srv/spec-kitty/workspaces/w18")
	state := StateV2{SchemaVersion: 1, LogicalVersion: 7, Binding: b, Stage: StageReadyV2, Observation: readyObservationFixtureV2(b), OccurredAt: "2026-09-04T12:00:00Z"}
	capsule, err := NewAuthorityCapsuleV2(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCurrentV2(state, capsule); err != nil {
		t.Fatal(err)
	}
	mutants := []func(*BindingV2){
		func(v *BindingV2) { v.RuntimeEpochID = "epoch-old" },
		func(v *BindingV2) { v.Fence = "old-fence-123456" },
		func(v *BindingV2) { v.WorkspaceID = "workspace-old" },
		func(v *BindingV2) {
			v.CanonicalPath = "/srv/spec-kitty/workspaces/w17"
			v.PathClaimDigest, _ = WorkspacePathClaimDigestV2(v.RepositoryID, v.ManagedRoot, v.CanonicalPath)
		},
		func(v *BindingV2) { v.LaneID = "lane-other" },
		func(v *BindingV2) {
			v.ManagedRoot = "/srv/spec-kitty/other"
			v.CanonicalPath = "/srv/spec-kitty/other/w18"
			v.PathClaimDigest, _ = WorkspacePathClaimDigestV2(v.RepositoryID, v.ManagedRoot, v.CanonicalPath)
		},
		func(v *BindingV2) { v.OwnedBranch = "kitty/other" },
		func(v *BindingV2) { v.PinnedBase = strings.Repeat("b", 40) },
		func(v *BindingV2) {
			v.Dependencies = []DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("c", 40)}}
		},
	}
	for _, mutate := range mutants {
		changed := b
		mutate(&changed)
		changedCapsule, buildErr := NewAuthorityCapsuleV2(changed)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err := ValidateCurrentV2(state, changedCapsule); !IsKind(err, KindStaleAuthority) {
			t.Fatalf("got %v", err)
		}
	}
	b.Dependencies = []DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("d", 40)}}
	if got := capsule.Binding(); len(got.Dependencies) != 0 {
		t.Fatal("capsule retained caller-owned dependency slice")
	}
	exposed := capsule.Binding()
	exposed.Dependencies = append(exposed.Dependencies, DependencyV2{WorkID: "work-dependency", Commit: strings.Repeat("e", 40)})
	if got := capsule.Binding(); len(got.Dependencies) != 0 {
		t.Fatal("capsule exposed mutable dependency slice")
	}
	state.Stage, state.QuarantineReason = StageQuarantinedV2, "expired"
	if err := ValidateCurrentV2(state, capsule); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("got %v", err)
	}
}

func workspaceBindingFixtureV2(t *testing.T, generation, workspacePath string) BindingV2 {
	t.Helper()
	claim, err := WorkspacePathClaimDigestV2("repository-1", "/srv/spec-kitty/workspaces", workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	var gen uint64 = 17
	if generation == "18" {
		gen = 18
	}
	return BindingV2{WorkspaceID: ids.WorkspaceID("workspace-" + generation), ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RunID: ids.RunID("run-" + generation), LeaseID: ids.LeaseID("lease-" + generation), Generation: gen, Fence: strings.Repeat(generation[:1], 32), RuntimeEpochID: journalv2.RuntimeEpochID("epoch-" + generation), ActorID: "actor-1", ProcessID: "process-1", RepositoryID: "repository-1", LaneID: "lane-1", ManagedRoot: "/srv/spec-kitty/workspaces", CanonicalPath: workspacePath, PathIdentity: verifiedPathIdentityFixtureV2(), PathClaimDigest: claim, OwnedBranch: "kitty/work-" + generation, PinnedBase: strings.Repeat("a", 40)}
}

func verifiedPathIdentityFixtureV2() PathIdentityV2 {
	return PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}
}

func workspaceEnvelopeFixtureV2(t *testing.T, prior, next StateV2, kind string, eventID journalv2.EventID) journalv2.EventEnvelopeV2 {
	t.Helper()
	priorDigest := ""
	if prior.LogicalVersion > 0 {
		digest, _ := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
		priorDigest = digest.String()
	}
	payload, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: kind, PriorDigest: priorDigest, State: next})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := WorkspaceCoordinateV2(next.Binding)
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: eventID, EventIndex: 0, EventCount: 1, EventType: "workspace." + kind, Family: "workspace", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), OperationID: "operation-test", OperationLogicalVersion: 1, PublicationID: journalv2.PublicationID(strings.Repeat("a", 64)), CommittedRuntimeEpochID: next.Binding.RuntimeEpochID, ActorID: journalv2.ActorID(next.Binding.ActorID), ProcessID: journalv2.ProcessID(next.Binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(next.Binding.WorkRevisionID), Payload: payload, Observations: []byte(`{}`)}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}
