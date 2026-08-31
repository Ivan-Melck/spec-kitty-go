package m1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sort"
	"testing"
)

type observationPort struct {
	observation Observation
	observeErr  error
	probe       StaleProbe
	probeErr    error
}

func (p observationPort) ObserveM1(context.Context) (Observation, error) {
	return p.observation, p.observeErr
}
func (p observationPort) ProbeStaleAuthority(context.Context) (StaleProbe, error) {
	return p.probe, p.probeErr
}

func TestInvariantCleanControlAndExactCensus(t *testing.T) {
	result := AssertUniversal(t.Context(), validObservationPort())
	if !result.Passed() {
		t.Fatalf("clean observation failed: %+v", result.Assertions)
	}
	got := SortedAssertionIDs(result)
	want := append([]string(nil), InvariantIDs...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invariant census=%v want=%v", got, want)
	}
}

func TestInvariantObservationAndStaleProbeFailuresFailClosed(t *testing.T) {
	tests := []observationPort{
		{},
		{observation: validObservation(), observeErr: errors.New("inspection unavailable"), probe: validProbe()},
		{observation: validObservation(), probeErr: errors.New("probe unavailable")},
	}
	for index, port := range tests {
		result := AssertUniversal(t.Context(), port)
		if result.Passed() || len(result.Assertions) != len(InvariantIDs) {
			t.Fatalf("case %d vacuously passed: %+v", index, result)
		}
		for _, assertion := range result.Assertions {
			if assertion.ID == "" || (!assertion.Passed && assertion.Diagnostic == "") {
				t.Fatalf("case %d lacks detailed assertion: %+v", index, assertion)
			}
		}
	}
}

func TestInvariantMutantsEachKillTheirNamedAssertion(t *testing.T) {
	tests := []struct {
		id     string
		mutate func(*observationPort)
	}{
		{InvariantOneAuthority, func(p *observationPort) {
			p.observation.Authorities = append(p.observation.Authorities, p.observation.Authorities[0])
		}},
		{InvariantWorkspaceImmutable, func(p *observationPort) { p.observation.Workspaces[0].Transferred = true }},
		{InvariantStaleRefused, func(p *observationPort) { p.probe.Refused = false }},
		{InvariantPrimaryIsolation, func(p *observationPort) {
			p.observation.Workspaces[0].CanonicalPath = p.observation.Workspaces[0].PrimaryPath
		}},
		{InvariantReviewIndependent, func(p *observationPort) {
			p.observation.Reviews[0].ReviewerActorID = p.observation.Reviews[0].ContributorIDs[0]
		}},
		{InvariantIntegrationSerial, func(p *observationPort) { p.observation.Integration[0].ActiveAuthorities = 2 }},
		{InvariantEffectAttribution, func(p *observationPort) { p.observation.Effects[0].Dispatches = 2 }},
		{InvariantJournalComplete, func(p *observationPort) { p.observation.Playback.AdapterCalls = 1 }},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			port := validObservationPort()
			test.mutate(&port)
			result := AssertUniversal(t.Context(), port)
			var found bool
			for _, assertion := range result.Assertions {
				if assertion.ID == test.id {
					found = true
					if assertion.Passed || assertion.Diagnostic == "" {
						t.Fatalf("mutant survived named invariant: %+v", assertion)
					}
				}
			}
			if !found || result.Passed() {
				t.Fatalf("mutant not represented: %+v", result)
			}
		})
	}
}

func TestInvariantUnavailableDomainCapabilitiesFailClosed(t *testing.T) {
	tests := []struct {
		id     string
		mutate func(*CapabilityObservation)
	}{
		{InvariantOneAuthority, func(c *CapabilityObservation) { c.Authority = false }},
		{InvariantWorkspaceImmutable, func(c *CapabilityObservation) { c.Workspace = false }},
		{InvariantReviewIndependent, func(c *CapabilityObservation) { c.Review = false }},
		{InvariantIntegrationSerial, func(c *CapabilityObservation) { c.Integration = false }},
		{InvariantEffectAttribution, func(c *CapabilityObservation) { c.Effects = false }},
	}
	for _, test := range tests {
		port := validObservationPort()
		test.mutate(&port.observation.Capabilities)
		result := AssertUniversal(t.Context(), port)
		for _, assertion := range result.Assertions {
			if assertion.ID == test.id && assertion.Passed {
				t.Fatalf("unavailable %s domain passed", test.id)
			}
		}
	}
}

func TestInvariantJournalRejectsSplitOrderingAndDigestMutants(t *testing.T) {
	tests := []func(*Observation){
		func(o *Observation) { o.Journal.Operations[0].Events[1].RecordRevision++ },
		func(o *Observation) { o.Journal.Operations[0].Events[1].Index = 7 },
		func(o *Observation) { o.Journal.Operations[0].EventCount = 1 },
		func(o *Observation) { o.Journal.Operations[0].CanonicalResult = []byte(`{"mutated":true}`) },
		func(o *Observation) { o.Journal.Heads[0].LastEventID = "different" },
		func(o *Observation) { o.Journal.Heads[0].LogicalVersion++ },
		func(o *Observation) { o.Journal.Heads[0].RecordRevision++ },
		func(o *Observation) { o.Journal.Heads[0].PayloadDigest = o.Journal.Heads[1].PayloadDigest },
		func(o *Observation) { o.Journal.Operations[0].ObservedHeadKeys = []string{"authority/work-a"} },
	}
	for index, mutate := range tests {
		port := validObservationPort()
		mutate(&port.observation)
		result := AssertUniversal(t.Context(), port)
		if result.JournalComplete {
			t.Fatalf("journal mutant %d survived", index)
		}
	}
}

func TestInvariantJournalRejectsCrossOperationIdentityAndVersionReplay(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Observation)
	}{
		{name: "duplicate event ID", mutate: func(o *Observation) {
			o.Journal.Operations[1].Events[0].EventID = o.Journal.Operations[0].Events[0].EventID
			o.Journal.Heads[0].LastEventID = o.Journal.Operations[1].Events[0].EventID
		}},
		{name: "duplicate logical version", mutate: func(o *Observation) {
			o.Journal.Operations[1].Events[0].LogicalVersion = o.Journal.Operations[0].Events[0].LogicalVersion
			o.Journal.Heads[0].LogicalVersion = o.Journal.Operations[1].Events[0].LogicalVersion
		}},
		{name: "nonmonotonic logical version", mutate: func(o *Observation) {
			o.Journal.Operations[1].Events[0].LogicalVersion = 3
			o.Journal.Heads[0].LogicalVersion = 3
		}},
		{name: "duplicate operation revision", mutate: func(o *Observation) {
			o.Journal.Operations[1].RecordRevision = o.Journal.Operations[0].RecordRevision
			o.Journal.Operations[1].Events[0].RecordRevision = o.Journal.Operations[0].RecordRevision
			o.Journal.Heads[0].RecordRevision = o.Journal.Operations[0].RecordRevision
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			port := validObservationPort()
			appendSecondJournalOperation(&port.observation)
			test.mutate(&port.observation)
			if result := AssertUniversal(t.Context(), port); result.JournalComplete {
				t.Fatalf("cross-operation journal mutant passed: %+v", result.Assertions)
			}
		})
	}
}

func TestInvariantPredecessorEpochRequiresEpochStaleProbe(t *testing.T) {
	port := validObservationPort()
	port.probe = StaleProbe{Attempted: true, Refused: true, LeaseStale: true}
	result := AssertUniversal(t.Context(), port)
	if result.StaleAuthorityRefused {
		t.Fatalf("lease-only probe qualified predecessor epoch: %+v", result.Assertions)
	}
}

func validObservationPort() observationPort {
	return observationPort{observation: validObservation(), probe: validProbe()}
}

func validProbe() StaleProbe {
	return StaleProbe{Attempted: true, Refused: true, LeaseStale: true, EpochStale: true}
}

func appendSecondJournalOperation(observation *Observation) {
	result := []byte(`{"outcome":"advanced"}`)
	resultDigest := sha256.Sum256(result)
	payload := sha256.Sum256([]byte(`{"event":"three"}`))
	operation := JournalOperation{
		OperationID: "operation-2", RequestDigest: hex.EncodeToString(payload[:]), ResultDigest: hex.EncodeToString(resultDigest[:]),
		RecordRevision: 8, EventCount: 1, Terminal: true, CanonicalResult: result, ObservedHeadKeys: []string{"authority/work-a"},
		Events: []JournalEvent{{EventID: "event-3", Index: 0, Count: 1, RecordRevision: 8, AggregateKey: "authority/work-a", LogicalVersion: 2, PayloadDigest: hex.EncodeToString(payload[:])}},
	}
	observation.Journal.Operations = append(observation.Journal.Operations, operation)
	observation.Journal.Heads[0] = JournalHead{AggregateKey: "authority/work-a", LogicalVersion: 2, RecordRevision: 8, LastEventID: "event-3", PayloadDigest: hex.EncodeToString(payload[:])}
}

func validObservation() Observation {
	result := []byte(`{"outcome":"committed"}`)
	digest := sha256.Sum256(result)
	payload := sha256.Sum256([]byte(`{"event":"one"}`))
	payload2 := sha256.Sum256([]byte(`{"event":"two"}`))
	restore := sha256.Sum256([]byte("stable-playback"))
	return Observation{
		Capabilities: CapabilityObservation{Authority: true, Workspace: true, Review: true, Integration: true, Effects: true},
		Journal: JournalObservation{Inspectable: true, Operations: []JournalOperation{{
			OperationID: "operation-1", RequestDigest: hex.EncodeToString(payload[:]), ResultDigest: hex.EncodeToString(digest[:]),
			RecordRevision: 7, EventCount: 2, Terminal: true, CanonicalResult: result, ObservedHeadKeys: []string{"authority/work-a", "workspace/W1"},
			Events: []JournalEvent{
				{EventID: "event-1", Index: 0, Count: 2, RecordRevision: 7, AggregateKey: "authority/work-a", LogicalVersion: 1, PayloadDigest: hex.EncodeToString(payload[:])},
				{EventID: "event-2", Index: 1, Count: 2, RecordRevision: 7, AggregateKey: "workspace/W1", LogicalVersion: 1, PayloadDigest: hex.EncodeToString(payload2[:])},
			},
		}}, Heads: []JournalHead{
			{AggregateKey: "authority/work-a", LogicalVersion: 1, RecordRevision: 7, LastEventID: "event-1", PayloadDigest: hex.EncodeToString(payload[:])},
			{AggregateKey: "workspace/W1", LogicalVersion: 1, RecordRevision: 7, LastEventID: "event-2", PayloadDigest: hex.EncodeToString(payload2[:])},
		}},
		Authorities: []AuthorityObservation{{WorkRevisionID: "revision-1", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-a", LeaseID: "lease-a", RuntimeEpochID: "epoch-2", Generation: 1, FenceToken: 1, Current: true}},
		Workspaces:  []WorkspaceObservation{{WorkspaceID: "W1", WorkRevisionID: "revision-1", WorkID: "work-a", LeaseID: "lease-a", RuntimeEpochID: "epoch-2", Generation: 1, CanonicalPath: "/repo/.worktrees/W1", OwnedBranch: "lane-a", Status: "active-bound", PrimaryPath: "/repo", Head: "abc", Registered: true}},
		Epoch:       EpochObservation{Inspectable: true, Current: "epoch-2", Predecessor: "epoch-1", EveryCurrentUses: true},
		Reviews:     []ReviewObservation{{CandidateID: "candidate-a", ContributorIDs: []string{"actor-a"}, ReviewerActorID: "reviewer-b", Verdict: "approved", CandidateImmutable: true}},
		Integration: []IntegrationObservation{{RepositoryID: "repo-1", TargetRef: "refs/heads/main", ActiveAuthorities: 1, CandidateID: "candidate-a", SuccessfulPublishes: 1, GitExpectedOld: "abc", GitObservedOld: "abc", GitResult: "def", JournalResult: "def"}},
		Effects:     []EffectObservation{{EffectID: "effect-1", OperationID: "operation-effect", ExpectedRevision: "provider-1", AppliedRevision: "provider-2", ExpectedValue: "human", AppliedValue: "agent", ExpectedMarker: "marker-before", AppliedMarker: "marker-operation", CurrentRevision: "provider-3", CurrentValue: "human", CurrentMarker: "marker-before", Dispatches: 1, CompensationAttempted: true, CompensationApplied: true, Attributable: true, HumanValuePreserved: true}},
		Playback:    PlaybackObservation{Inspectable: true, RestoreDigest: hex.EncodeToString(restore[:]), PlaybackDigest: hex.EncodeToString(restore[:]), StableCanonical: true},
	}
}
