package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

func TestConcurrentIndependentAuthoritySmoke(t *testing.T) {
	runIndependentMissionPairs(t, 3)
}

func TestConcurrentIndependentAuthorityCampaign100(t *testing.T) {
	requireFullCampaign(t)
	runIndependentMissionPairs(t, 100)
}

func TestConcurrentSameAggregateConflictSmoke(t *testing.T) {
	runSameAggregateConflicts(t, 3)
}

func TestConcurrentSameAggregateConflictCampaign100(t *testing.T) {
	requireFullCampaign(t)
	runSameAggregateConflicts(t, 100)
}

func runIndependentMissionPairs(t *testing.T, trials int) {
	t.Helper()
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: fmt.Sprintf("%s-%d", t.Name(), trials)})
	for trial := range trials {
		commands := []mission.ReadinessAuthorityCommandV2{
			missionCreateCommand(t, h.host.RuntimeEpochID(), fmt.Sprintf("independent-%03d-a", trial), fmt.Sprintf("create-%03d-a", trial)),
			missionCreateCommand(t, h.host.RuntimeEpochID(), fmt.Sprintf("independent-%03d-b", trial), fmt.Sprintf("create-%03d-b", trial)),
		}
		results, errs := executeMissionPair(t, h, commands)
		for index := range 2 {
			if errs[index] != nil || results[index].PublicationID() == "" {
				t.Fatalf("trial=%d participant=%d result=%+v err=%v", trial, index, results[index], errs[index])
			}
		}
		assertUniversalInvariants(t, h, "project-concurrency", string(commands[0].MissionID), fmt.Sprintf("independent-%03d", trial))
	}
	if h.host.Status().State != productionruntime.HostReady {
		t.Fatalf("independent campaign degraded production host: %+v", h.host.Status())
	}
	t.Logf("campaign seed=%s trials=%d participants=%d outcome=pass", t.Name(), trials, trials*2)
}

func runSameAggregateConflicts(t *testing.T, trials int) {
	t.Helper()
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: fmt.Sprintf("%s-%d", t.Name(), trials)})
	for trial := range trials {
		started := time.Now()
		missionID := fmt.Sprintf("contended-%03d", trial)
		commands := []mission.ReadinessAuthorityCommandV2{
			missionCreateCommand(t, h.host.RuntimeEpochID(), missionID, fmt.Sprintf("contender-%03d-a", trial)),
			missionCreateCommand(t, h.host.RuntimeEpochID(), missionID, fmt.Sprintf("contender-%03d-b", trial)),
		}
		results, errs := executeMissionPair(t, h, commands)
		winners, losers := 0, 0
		for index := range 2 {
			switch {
			case errs[index] == nil && results[index].PublicationID() != "":
				winners++
			case mission.IsMissionAuthorityKindV2(errs[index], mission.MissionAuthorityVersionConflictV2):
				losers++
			default:
				t.Fatalf("trial=%d participant=%d unexpected result=%+v err=%v", trial, index, results[index], errs[index])
			}
		}
		if winners != 1 || losers != 1 {
			t.Fatalf("trial=%d winners=%d losers=%d", trial, winners, losers)
		}
		invariants := assertUniversalInvariants(t, h, "project-concurrency", missionID, fmt.Sprintf("same-aggregate-%03d", trial))
		if trials == 100 {
			receipt, err := iterationreceipt.New(
				iterationreceipt.DefinitionID("fault/concurrent-claims"), trial, missionID, "mission-head-cas", "one-winner-one-version-conflict",
				time.Since(started), map[string]int{"winners": winners, "typed_losers": losers}, json.RawMessage(invariants),
			)
			if err != nil {
				t.Fatal(err)
			}
			iterationreceipt.Emit(t, receipt)
		}
	}
	t.Logf("campaign seed=%s trials=%d one_winner=%d typed_losers=%d", t.Name(), trials, trials, trials)
}

func missionCreateCommand(t *testing.T, epoch journalv2.RuntimeEpochID, missionID, operation string) mission.ReadinessAuthorityCommandV2 {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"concurrency"}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: "project-concurrency", MissionID: journalv2.MissionID(missionID),
		DefinitionRevision: "definition-concurrency", DefinitionDigest: digest,
		Version: 1, Lifecycle: mission.MissionOpen,
		LastOperationID: journalv2.OperationID(operation), ObservedAt: now.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: journalv2.OperationID(operation),
		ProjectID: "project-concurrency", MissionID: journalv2.MissionID(missionID),
		ActorID: journalv2.ActorID("actor-" + operation), ProcessID: journalv2.ProcessID("process-" + operation),
		OriginatingRuntimeEpochID: epoch, DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: now.Format(time.RFC3339), EventType: mission.ReadinessEventCreatedV2, Updated: state,
	}
}

func executeMissionPair(t *testing.T, h *portableHarness, commands []mission.ReadinessAuthorityCommandV2) ([2]mission.ReadinessAuthorityResultV2, [2]error) {
	t.Helper()
	var results [2]mission.ReadinessAuthorityResultV2
	var errs [2]error
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for index := range 2 {
		index := index
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			results[index], errs[index] = h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), commands[index])
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	// A stable-prefix observation may deliberately report ambiguity while the
	// other publisher is moving durable state. Resolve only by replaying the
	// identical operation binding; never invent a replacement identity.
	for index := range 2 {
		for attempt := 0; attempt < 8 && mission.IsMissionAuthorityKindV2(errs[index], mission.MissionAuthorityAmbiguousV2); attempt++ {
			results[index], errs[index] = h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), commands[index])
		}
	}
	return results, errs
}

func requireFullCampaign(t *testing.T) {
	t.Helper()
	if os.Getenv("SPEC_KITTY_WP14_CAMPAIGN") != "100" {
		t.Skip("set SPEC_KITTY_WP14_CAMPAIGN=100 for the exact bounded campaign")
	}
}
