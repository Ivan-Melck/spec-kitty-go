package workspace

import (
	"errors"
	"sort"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestWorkspaceControlledMutantCensusV2(t *testing.T) {
	b := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	initial := StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: b, Stage: StageCreationRequestedV2, EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
	ready := initial
	ready.LogicalVersion = 7
	ready.Stage = StageReadyV2
	ready.Observation = readyObservationFixtureV2(b)
	quarantined := ready
	quarantined.LogicalVersion = 8
	quarantined.Stage = StageQuarantinedV2
	quarantined.QuarantineReason = "expired"
	capsule := capsuleForBindingV2(b)
	mutants := map[string]func(*testing.T) bool{
		"M-WORKSPACE-MUTABLE-LEASE-REPOSITORY-PATH": func(t *testing.T) bool {
			next := initial
			next.LogicalVersion++
			next.Stage = StageWorktreeObservedV2
			next.Observation = ObservationV2{Registered: true}
			next.Binding.LeaseID = "lease-mutant"
			event := workspaceEnvelopeFixtureV2(t, initial, next, "preparation_observed", "event-mutant-binding")
			_, err := ReduceEventV2(initial.CanonicalBytes(), event)
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-PATH-REUSED-BY-REPLACEMENT": func(t *testing.T) bool {
			other := b
			other.WorkspaceID = "workspace-other"
			other.RunID, other.LeaseID, other.Generation = "run-other", "lease-other", b.Generation+1
			other.Fence = "other-fence-000000000000000000000"
			first, firstErr := AppendPlanContributionV2(journalv2.CommitPlanInput{}, mustBindingContributionV2(t, b, "first"))
			second, secondErr := AppendPlanContributionV2(journalv2.CommitPlanInput{}, mustBindingContributionV2(t, other, "second"))
			return firstErr == nil && secondErr == nil && first.WorkspacePathClaim != nil && second.WorkspacePathClaim != nil && first.WorkspacePathClaim.ClaimDigest() == second.WorkspacePathClaim.ClaimDigest()
		},
		"M-WORKSPACE-QUARANTINE-RETURNS-READY": func(t *testing.T) bool {
			next := quarantined
			next.LogicalVersion++
			next.Stage = StageReadyV2
			next.QuarantineReason = ""
			_, err := ReduceEventV2(quarantined.CanonicalBytes(), workspaceEnvelopeFixtureV2(t, quarantined, next, "ready_published", "event-mutant-resurrect"))
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-EXPIRY-WITHOUT-QUARANTINE": func(t *testing.T) bool {
			return quarantineContributionKillsV2(t, ready, quarantined, capsule)
		},
		"M-WORKSPACE-SAME-VALUE-CAS-AS-ASSERTION": func(*testing.T) bool {
			target := journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: journalv2.AggregateCoordinate{Kind: "execution_slot", ID: "slot-mutant", Scope: journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(b.ProjectID), MissionID: journalv2.MissionID(b.MissionID), WorkRevisionID: journalv2.WorkRevisionID(b.WorkRevisionID), WorkID: journalv2.WorkID(b.WorkID)}}}
			digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"state":"current"}`))
			assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: target, Kind: journalv2.ConditionRecordRevision, ObservedRevision: 7, ExpectedLogicalVersion: 3, ObservedValueDigest: digest})
			return err == nil && validateExecutionAssertionForTestV2(assertion, b)
		},
		"M-WORKSPACE-EXECUTION-EXPIRES-BINDING-PUBLISHES": func(*testing.T) bool {
			return IsKind(ValidateCurrentV2(ready, AuthorityCapsuleV2{}), KindStaleAuthority)
		},
		"M-WORKSPACE-BINDING-WINS-EXPIRY-OMITS-QUARANTINE": func(t *testing.T) bool {
			input, err := AppendPlanContributionV2(journalv2.CommitPlanInput{}, mustQuarantineContributionV2(t, ready))
			return err == nil && len(input.Events) == 1 && input.Events[0].EventType == "workspace.quarantined" && len(input.Heads) == 1
		},
		"M-WORKSPACE-CLEANUP-ROLLS-BACK-QUARANTINE": func(t *testing.T) bool {
			resurrected := quarantined
			resurrected.LogicalVersion++
			resurrected.Stage, resurrected.QuarantineReason = StageReadyV2, ""
			_, err := ReduceEventV2(quarantined.CanonicalBytes(), workspaceEnvelopeFixtureV2(t, quarantined, resurrected, "ready_published", "event-cleanup-mutant"))
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-ADAPTER-BEFORE-INTENT": func(*testing.T) bool {
			journal := &preparationJournalProbeV2{state: initial}
			effects := &preparationEffectsProbeV2{createErr: errors.New("cut")}
			service, _ := NewPreparationServiceV2(journal, effects)
			_, _ = service.EnsureReady(t.Context(), b)
			return journal.trace[0] == "observe_durable" && effects.trace[0] == "observe_external"
		},
		"M-WORKSPACE-RESTART-ALLOCATES-NEW-IDENTITY": func(t *testing.T) bool {
			journal := &preparationJournalProbeV2{state: initial}
			effects := &preparationEffectsProbeV2{createErr: errors.New("lost response")}
			service, _ := NewPreparationServiceV2(journal, effects)
			_, _ = service.EnsureReady(t.Context(), b)
			effects.observation = readyObservationFixtureV2(b)
			result, err := service.EnsureReady(t.Context(), b)
			return err == nil && result.Stage == StageReadyV2 && bindingEqualV2(result.Binding, b) && effects.createCalls == 1
		},
		"M-WORKSPACE-READY-WITHOUT-FINAL-OBSERVATION": func(t *testing.T) bool {
			_, err := NewTransitionContributionV2(TransitionContributionInputV2{Prior: readyStateBeforePublicationV2(b), RecordRevision: 7, EventID: "event-ready-mutant", NextStage: StageReadyV2, Observation: ObservationV2{}, OccurredAt: time.Date(2026, 9, 4, 12, 1, 0, 0, time.UTC)})
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-ZOMBIE-PASSES-GATE": func(*testing.T) bool { return IsKind(ValidateCurrentV2(quarantined, capsule), KindStaleAuthority) },
		"M-WORKSPACE-EPOCH-OR-FENCE-ONLY-GATE": func(*testing.T) bool {
			changed := capsule
			changed.RunID = "run-other"
			return IsKind(ValidateCurrentV2(ready, changed), KindStaleAuthority)
		},
		"M-WORKSPACE-PROCESS-MUTEX-AUTHORITY": func(t *testing.T) bool {
			input, err := AppendPlanContributionV2(journalv2.CommitPlanInput{}, mustBindingContributionV2(t, b, "provider-claim"))
			return err == nil && input.WorkspacePathClaim != nil && input.WorkspacePathClaim.ClaimDigest() == mustClaimV2(t, b)
		},
		"M-WORKSPACE-TWO-EVENTS-ONE-AGGREGATE": func(t *testing.T) bool {
			next := initial
			next.LogicalVersion += 2
			next.Stage = StageBaseVerifiedV2
			next.Observation = ObservationV2{Registered: true, Head: b.PinnedBase}
			_, err := ReduceEventV2(initial.CanonicalBytes(), workspaceEnvelopeFixtureV2(t, initial, next, "preparation_advanced", "event-mutant-skip"))
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-RESTART-TRUSTS-UNPROVEN-PROJECTION": func(*testing.T) bool {
			other := initial
			other.Binding.WorkspaceID = "workspace-other"
			journal := &preparationJournalProbeV2{state: other}
			effects := &preparationEffectsProbeV2{}
			service, _ := NewPreparationServiceV2(journal, effects)
			_, err := service.EnsureReady(t.Context(), b)
			return IsKind(err, KindWorkspaceConflict)
		},
		"M-WORKSPACE-ADAPTER-LEAKS-INTO-KERNEL": func(*testing.T) bool {
			registrations := ReducerRegistrationsV2()
			return len(registrations) == 1 && registrations[0].Owner == "internal/workspace"
		},
	}
	ids := make([]string, 0, len(mutants))
	for id, kill := range mutants {
		ids = append(ids, id)
		if !kill(t) {
			t.Errorf("mutant survived: %s", id)
		}
	}
	sort.Strings(ids)
	if len(ids) != 17 {
		t.Fatalf("mutant census=%d", len(ids))
	}
	want := "M-WORKSPACE-ADAPTER-BEFORE-INTENT\nM-WORKSPACE-ADAPTER-LEAKS-INTO-KERNEL\nM-WORKSPACE-BINDING-WINS-EXPIRY-OMITS-QUARANTINE\nM-WORKSPACE-CLEANUP-ROLLS-BACK-QUARANTINE\nM-WORKSPACE-EPOCH-OR-FENCE-ONLY-GATE\nM-WORKSPACE-EXECUTION-EXPIRES-BINDING-PUBLISHES\nM-WORKSPACE-EXPIRY-WITHOUT-QUARANTINE\nM-WORKSPACE-MUTABLE-LEASE-REPOSITORY-PATH\nM-WORKSPACE-PATH-REUSED-BY-REPLACEMENT\nM-WORKSPACE-PROCESS-MUTEX-AUTHORITY\nM-WORKSPACE-QUARANTINE-RETURNS-READY\nM-WORKSPACE-READY-WITHOUT-FINAL-OBSERVATION\nM-WORKSPACE-RESTART-ALLOCATES-NEW-IDENTITY\nM-WORKSPACE-RESTART-TRUSTS-UNPROVEN-PROJECTION\nM-WORKSPACE-SAME-VALUE-CAS-AS-ASSERTION\nM-WORKSPACE-TWO-EVENTS-ONE-AGGREGATE\nM-WORKSPACE-ZOMBIE-PASSES-GATE"
	if got := joinLinesV2(ids); got != want {
		t.Fatalf("mutant manifest drift:\n%s", got)
	}
}

func mustBindingContributionV2(t *testing.T, binding BindingV2, suffix string) PlanContributionV2 {
	t.Helper()
	contribution, err := NewBindingContributionV2(BindingContributionInputV2{Binding: binding, EffectID: journalv2.EffectID("effect-" + suffix), EventID: journalv2.EventID("event-" + suffix), OccurredAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return contribution
}

func readyStateBeforePublicationV2(binding BindingV2) StateV2 {
	return StateV2{SchemaVersion: 1, LogicalVersion: 6, Binding: binding, Stage: StageOwnershipVerifiedV2, Observation: readyObservationFixtureV2(binding), EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
}

func mustQuarantineContributionV2(t *testing.T, prior StateV2) PlanContributionV2 {
	t.Helper()
	contribution, err := NewQuarantineContributionV2(QuarantineContributionInputV2{Prior: prior, RecordRevision: 17, EventID: "event-quarantine-mutant", Reason: QuarantineLeaseExpired, OccurredAt: time.Date(2026, 9, 4, 13, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return contribution
}

func quarantineContributionKillsV2(t *testing.T, prior, expected StateV2, capsule AuthorityCapsuleV2) bool {
	t.Helper()
	contribution := mustQuarantineContributionV2(t, prior)
	input, err := AppendPlanContributionV2(journalv2.CommitPlanInput{}, contribution)
	if err != nil || len(input.Events) != 1 {
		return false
	}
	next, err := contribution.State()
	if err != nil || next.Stage != expected.Stage || next.Binding.WorkspaceID != expected.Binding.WorkspaceID {
		return false
	}
	reduced, err := ReduceEventV2(prior.CanonicalBytes(), workspaceEnvelopeFixtureV2(t, prior, next, "quarantined", input.Events[0].EventID))
	if err != nil {
		return false
	}
	state, err := DecodeStateV2(reduced)
	return err == nil && state.Stage == StageQuarantinedV2 && IsKind(ValidateCurrentV2(state, capsule), KindStaleAuthority)
}

func validateExecutionAssertionForTestV2(a journalv2.AuthorityAssertionV2, b BindingV2) bool {
	target := a.Target()
	return a.Kind() == journalv2.ConditionRecordRevision && target.Kind == journalv2.ConditionTargetAggregateHead && target.Coordinate.Kind == "execution_slot" && target.Coordinate.Scope.WorkID == journalv2.WorkID(b.WorkID)
}
func mustClaimV2(t *testing.T, b BindingV2) journalv2.Digest {
	t.Helper()
	d, err := WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func joinLinesV2(values []string) string {
	result := ""
	for i, v := range values {
		if i > 0 {
			result += "\n"
		}
		result += v
	}
	return result
}

var _ = time.Second
