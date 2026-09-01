package faults

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalplayback "github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/provingground"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

var crownInternalSegmentIDs = []string{
	"concurrent-a-a-w1-b-b-w2", "gj-01", "gj-02",
	"expire-quarantine-w1", "gj-03", "gj-04", "replace-a-c-w3",
	"zombie-write-refusal", "candidate-w2", "candidate-w3", "review-w2",
	"review-w3", "integrate-w2", "integrate-w3", "gj-05",
	"owner-restart", "restore-new-epoch", "predecessor-refusal",
	"playback-equality",
}

type CrownInput struct {
	RunID string
	Seed  uint64
}

type CrownExecution struct {
	RunID       string
	Source      m1.SourceBinding
	Segments    []m1.CrownSegment
	Artifacts   [][]byte
	Faults      []Record
	Trial       m1.OrderedTrial
	Invariants  m1.InvariantSummary
	Observation m1.ObservationPort
	Reviews     []basereview.Record
}

// RunOrderedCrown executes the product-owned middle of the decisive trial.
// External scenarios precede it; focused campaigns and mutants follow it in
// the sibling crown orchestrator. No caller-provided boolean can mint facts.
func (p *ProductionExecutor) RunOrderedCrown(ctx context.Context, input CrownInput) (CrownExecution, error) {
	result := CrownExecution{RunID: input.RunID}
	if p == nil || input.RunID == "" || input.Seed == 0 || p.RunID != input.RunID {
		return result, fmt.Errorf("crown executor, immutable RunID, and nonzero seed are required")
	}
	source, err := m1.ObserveSource(ctx, p.Root, p.Lifecycle)
	if err != nil {
		return result, err
	}
	result.Source = source
	environment, err := p.productionEnvironment(ctx, input.RunID)
	if err != nil {
		return result, err
	}
	primaryBefore, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return result, err
	}
	recorder := crownRecorder{ctx: ctx, runID: input.RunID, environment: environment}

	if err := recorder.capture("concurrent-a-a-w1-b-b-w2", func() (any, error) {
		return startCrownClaims(ctx, environment)
	}); err != nil {
		return recorder.finish(result), err
	}

	for index, faultID := range []string{"GJ-01", "GJ-02"} {
		fault := (Runner{RunID: input.RunID, Seed: input.Seed + uint64(index), Executor: p}).Run(ctx, faultID)
		result.Faults = append(result.Faults, fault)
		if err := recorder.capture(strings.ToLower(faultID), func() (any, error) {
			if fault.Outcome != OutcomePass {
				return fault, fmt.Errorf("%s did not pass: %s", faultID, fault.Diagnostic)
			}
			return fault, nil
		}); err != nil {
			return recorder.finish(result), err
		}
	}

	oldW1 := *environment.workA
	oldW2 := environment.baseline
	if err := recorder.capture("expire-quarantine-w1", func() (any, error) {
		err := environment.runtime.Expire(ctx, "crown-expire-w1", oldW1.Authority, oldW1.Run.Deadline)
		binding, ok := environment.runtime.Binding(oldW1.Authority.WorkspaceID)
		if err != nil || !ok || binding.Status != "quarantined" {
			return binding, fmt.Errorf("W1 was not durably quarantined: %w", err)
		}
		return binding, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	// The GJ-03/GJ-04 attempts are deliberately completed before W3 exists.
	// They may allocate fault-attempt workspaces, but those identities are never
	// relabelled as W3 and can never become the definitive candidate workspace.
	var gj03 Record
	for index, faultID := range []string{"GJ-03", "GJ-04"} {
		fault := (Runner{RunID: input.RunID, Seed: input.Seed + uint64(index+2), Executor: p}).Run(ctx, faultID)
		result.Faults = append(result.Faults, fault)
		if err := recorder.capture(strings.ToLower(faultID), func() (any, error) {
			if fault.Outcome != OutcomePass {
				return fault, fmt.Errorf("%s did not pass: %s", faultID, fault.Diagnostic)
			}
			return fault, nil
		}); err != nil {
			return recorder.finish(result), err
		}
		if faultID == "GJ-03" {
			gj03 = fault
			if err := validateCrownFaultWorkspaceIdentities(oldW1, oldW2, gj03, ""); err != nil {
				return recorder.finish(result), err
			}
		}
	}
	if environment.workA != nil {
		attempt := *environment.workA
		if err := environment.runtime.Expire(ctx, "crown-expire-fault-attempt", attempt.Authority, attempt.Run.Deadline); err != nil {
			return recorder.finish(result), err
		}
	}
	var finalW3 provingground.Delivery
	if err := recorder.capture("replace-a-c-w3", func() (any, error) {
		var replaceErr error
		finalW3, replaceErr = startCrownWorkspace(ctx, environment, "crown-claim-w3", "crown-bind-w3", "work-a", "actor-a", "process-c", "workspace-crown-w3", "runs/crown-w3", time.Now().UTC())
		if replaceErr != nil {
			return finalW3, replaceErr
		}
		if finalW3.Authority.LeaseID == oldW1.Authority.LeaseID || finalW3.Authority.WorkspaceID == oldW1.Authority.WorkspaceID || finalW3.Authority.ActorID != "actor-a" || finalW3.Authority.ProcessID != "process-c" {
			return finalW3, fmt.Errorf("replacement authority was not Actor A/Process C in a fresh workspace")
		}
		if identityErr := validateCrownFaultWorkspaceIdentities(oldW1, oldW2, gj03, finalW3.Authority.WorkspaceID); identityErr != nil {
			return finalW3, identityErr
		}
		environment.workA = &finalW3
		return finalW3, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	zombieAuthoritySuccesses := 0
	if err := recorder.capture("zombie-write-refusal", func() (any, error) {
		oldBinding, ok := environment.runtime.Binding(oldW1.Authority.WorkspaceID)
		freshBinding, freshOK := environment.runtime.Binding(finalW3.Authority.WorkspaceID)
		if !ok || !freshOK {
			return nil, fmt.Errorf("zombie/fresh workspace binding is absent")
		}
		before, err := observeWorktreeContent(ctx, freshBinding.CanonicalPath)
		if err != nil {
			return nil, err
		}
		zombieBytes := []byte("crown stale POSIX writer\n")
		zombiePath := oldBinding.CanonicalPath + "/zombie-crown.txt"
		if err := os.WriteFile(zombiePath, zombieBytes, 0o600); err != nil {
			return nil, err
		}
		readBack, err := os.ReadFile(zombiePath)
		if err != nil {
			return nil, err
		}
		staleErr := environment.runtime.Authorize(oldW1.Authority, "crown-zombie-crossing", time.Now().UTC())
		if staleErr == nil {
			zombieAuthoritySuccesses++
		}
		after, err := observeWorktreeContent(ctx, freshBinding.CanonicalPath)
		facts := map[string]any{"zombie_bytes_written": bytes.Equal(readBack, zombieBytes), "stale_refused": provingground.IsOutcome(staleErr, provingground.OutcomeStaleLease), "fresh_unchanged": before == after, "w1": oldBinding, "w3": freshBinding}
		if err != nil || facts["zombie_bytes_written"] != true || facts["stale_refused"] != true || facts["fresh_unchanged"] != true {
			return facts, fmt.Errorf("zombie isolation failed: %w", err)
		}
		return facts, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	reviews, integrator, err := crownReviewComposition(ctx, environment)
	if err != nil {
		return recorder.finish(result), err
	}
	environment.reviews, environment.integrator = reviews, integrator
	w2, err := prepareCrownCandidate(ctx, environment, reviews, oldW2, "w2", "reviewer-w2", source.Commit)
	if err != nil {
		return recorder.finish(result), err
	}
	w3, err := prepareCrownCandidate(ctx, environment, reviews, finalW3, "w3", "reviewer-w3", source.Commit)
	if err != nil {
		return recorder.finish(result), err
	}
	for _, entry := range []struct {
		id    string
		value any
	}{{"candidate-w2", crownHandoffArtifact{w2.Handoff, w2.HandoffEvidence}}, {"candidate-w3", crownHandoffArtifact{w3.Handoff, w3.HandoffEvidence}}, {"review-w2", crownReviewArtifact{w2.Review, w2.ReviewEvidence}}, {"review-w3", crownReviewArtifact{w3.Review, w3.ReviewEvidence}}} {
		entry := entry
		if err := recorder.capture(entry.id, func() (any, error) { return entry.value, nil }); err != nil {
			return recorder.finish(result), err
		}
	}
	var integrations []baseintegration.Record
	if err := recorder.capture("integrate-w2", func() (any, error) {
		record, integrateErr := integrator.Integrate(ctx, w2.Item)
		if integrateErr != nil {
			return record, integrateErr
		}
		integrations = append(integrations, record)
		return record, nil
	}); err != nil {
		return recorder.finish(result), err
	}
	if err := recorder.capture("integrate-w3", func() (any, error) {
		record, integrateErr := integrator.Integrate(ctx, w3.Item)
		if integrateErr != nil {
			return record, integrateErr
		}
		integrations = append(integrations, record)
		if record.Status != baseintegration.StatusDone {
			return record, fmt.Errorf("second serialized publication is absent")
		}
		return record, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	fault := (Runner{RunID: input.RunID, Seed: input.Seed + 4, Executor: p}).Run(ctx, "GJ-05")
	result.Faults = append(result.Faults, fault)
	if err := recorder.capture("gj-05", func() (any, error) {
		if fault.Outcome != OutcomePass {
			return fault, fmt.Errorf("GJ-05 did not pass: %s", fault.Diagnostic)
		}
		return fault, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	predecessorEpoch := string(environment.runtime.QualificationSnapshot().RuntimeEpochID)
	predecessorUmbrellaRunID := environment.runID
	predecessorSupervisorRunID := p.Lifecycle.Provenance().SupervisorRunID
	predecessorCapsule := environment.baseline.Authority
	var restartArtifact []byte
	if err := recorder.capture("owner-restart", func() (any, error) {
		barrier := newCrownBarrier(2)
		factsArtifact, facts, restartErr := p.executeFocusedEpoch(ctx, FocusedInput{RunID: input.RunID, CampaignID: "restart-epoch-nfr007", AttemptID: "crown-owner-restart", Index: 0, Seed: input.Seed + 5, Order: [2]int{0, 1}, Barrier: barrier}, environment)
		restartArtifact = factsArtifact
		if restartErr != nil {
			return facts, restartErr
		}
		if err := validateFocusedFactCensus("restart-epoch-nfr007", facts); err != nil {
			return facts, err
		}
		return json.RawMessage(factsArtifact), nil
	}); err != nil {
		return recorder.finish(result), err
	}
	freshEpoch := string(environment.runtime.QualificationSnapshot().RuntimeEpochID)
	freshUmbrellaRunID := environment.runID
	freshSupervisorRunID := p.Lifecycle.Provenance().SupervisorRunID
	if err := recorder.capture("restore-new-epoch", func() (any, error) {
		restored, _, restoreErr := restoreAndJournal(ctx, environment)
		if restoreErr != nil || !restored.Ready || freshEpoch == predecessorEpoch {
			return restored.Observation, fmt.Errorf("fresh epoch restore failed: %w", restoreErr)
		}
		return restored.Observation, nil
	}); err != nil {
		return recorder.finish(result), err
	}
	predecessorRefused := false
	predecessorReasonKind := ""
	if err := recorder.capture("predecessor-refusal", func() (any, error) {
		staleErr := environment.runtime.Authorize(predecessorCapsule, "crown-predecessor-probe", time.Now().UTC())
		predecessorRefused = provingground.IsOutcome(staleErr, provingground.OutcomeStaleEpoch)
		if predecessorRefused {
			predecessorReasonKind = "runtime_epoch_conflict"
		}
		if !predecessorRefused {
			return errorString(staleErr), fmt.Errorf("predecessor capsule was not epoch-stale: %w", staleErr)
		}
		return map[string]any{"refused": predecessorRefused, "kind": predecessorReasonKind}, nil
	}); err != nil {
		return recorder.finish(result), err
	}
	var restoreDigest, playbackDigest string
	var playbackCalls, playbackWrites uint64
	if err := recorder.capture("playback-equality", func() (any, error) {
		restored, journal, restoreErr := restoreAndJournal(ctx, environment)
		if restoreErr != nil {
			return nil, restoreErr
		}
		played, playErr := journalplayback.RunV1(journalplayback.InputV1{Journal: journal, Reducers: environment.reducers})
		if playErr != nil {
			return nil, playErr
		}
		restoreDigest, playbackDigest = hex.EncodeToString(restored.CanonicalProjectionDigest[:]), hex.EncodeToString(played.CanonicalProjectionDigest[:])
		playbackCalls, playbackWrites = played.Observation.AdapterCalls, played.Observation.DurableWrites
		if compareErr := journalplayback.CompareDigestV1(restored.CanonicalProjectionDigest, played); compareErr != nil || playbackCalls != 0 || playbackWrites != 0 {
			return played, fmt.Errorf("playback mismatch or I/O: %w", compareErr)
		}
		return played, nil
	}); err != nil {
		return recorder.finish(result), err
	}

	primaryAfter, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return recorder.finish(result), err
	}
	reviewObservations := []m1.ReviewObservation{w2.Observation, w3.Observation}
	integrationObservations := make([]m1.IntegrationObservation, 0, len(integrations))
	for _, record := range integrations {
		integrationObservations = append(integrationObservations, m1.IntegrationObservation{RepositoryID: record.RepositoryID.String(), TargetRef: record.TargetRef, ActiveAuthorities: 0, CandidateID: record.Candidate, SuccessfulPublishes: 1, GitExpectedOld: record.PreflightTarget, GitObservedOld: record.PreflightTarget, GitResult: record.ResultCommit, JournalResult: record.ResultCommit})
	}
	effects := append([]m1.EffectObservation(nil), fault.Effects...)
	if len(effects) != 1 {
		return recorder.finish(result), fmt.Errorf("GJ-05 exact effect observation census=%d want=1", len(effects))
	}
	overlay := qualificationOverlay{reviews: reviewObservations, integration: integrationObservations, primary: m1.PrimaryObservation{Inspectable: true, Before: gitSnapshotObservation(primaryBefore), After: gitSnapshotObservation(primaryAfter)}}
	observation, err := observeComposedRuntimeWithOverlay(ctx, environment, effects, overlay)
	if err != nil {
		return recorder.finish(result), err
	}
	invariants := m1.AssertUniversal(ctx, observation)
	if !invariants.Passed() {
		return recorder.finish(result), fmt.Errorf("terminal crown universal invariants failed: %+v", invariants)
	}
	publicationOrder := make([]string, len(integrations))
	for i := range integrations {
		publicationOrder[i] = integrations[i].Candidate
	}
	oldW1Binding, oldW1Known := environment.runtime.Binding(oldW1.Authority.WorkspaceID)
	w2CandidateSameWorkspace := w2.Handoff.WorkspaceID.String() == string(oldW2.Authority.WorkspaceID)
	w3CandidateSameWorkspace := w3.Handoff.WorkspaceID.String() == string(finalW3.Authority.WorkspaceID)
	reviewsIndependent := w2.CrownReview.ActorIndependent && w3.CrownReview.ActorIndependent
	serialized := len(integrations) == 2 && integrations[0].DoneAt.Before(integrations[1].DoneAt) && integrations[0].ResultCommit == integrations[1].PreflightTarget
	fr044 := oldW1Known && oldW1Binding.Status == "quarantined" && w2CandidateSameWorkspace && w3CandidateSameWorkspace && zombieAuthoritySuccesses == 0
	sc013 := fr044 && reviewsIndependent && serialized && invariants.Passed() && restoreDigest == playbackDigest && playbackCalls == 0 && playbackWrites == 0
	implementationA, err := crownParticipant(oldW1)
	if err != nil {
		return recorder.finish(result), err
	}
	implementationB, err := crownParticipant(oldW2)
	if err != nil {
		return recorder.finish(result), err
	}
	replacementC, err := crownParticipant(finalW3)
	if err != nil {
		return recorder.finish(result), err
	}
	result.Trial = m1.OrderedTrial{SchemaVersion: 1, RunID: input.RunID, TrialID: m1.OrderedTrialID, Started: len(recorder.segments) > 0, Terminal: len(recorder.segments) == len(crownInternalSegmentIDs), Outcome: "pass", FR044Satisfied: fr044, SC013Satisfied: sc013,
		Identities:         m1.CrownIdentities{ImplementationA: implementationA, ImplementationB: implementationB, ReplacementC: replacementC},
		WorkspaceIsolation: m1.CrownWorkspaceIsolation{W1Quarantined: oldW1Known && oldW1Binding.Status == "quarantined", W2Preserved: w2CandidateSameWorkspace, W3Fresh: w3CandidateSameWorkspace && finalW3.Authority.WorkspaceID != oldW1.Authority.WorkspaceID, ZombieAuthoritySuccesses: zombieAuthoritySuccesses},
		OwnerRestart:       m1.CrownOwnerRestart{SameUmbrellaRun: predecessorUmbrellaRunID == input.RunID && freshUmbrellaRunID == input.RunID && predecessorSupervisorRunID != freshSupervisorRunID, BeforeRuntimeEpochID: predecessorEpoch, AfterRuntimeEpochID: freshEpoch, NewEpochAdmitted: freshEpoch != predecessorEpoch},
		OldCapsule:         m1.CrownOldCapsule{PredecessorRuntimeEpochID: predecessorEpoch, Refused: predecessorRefused, ReasonKind: predecessorReasonKind},
		RestorePlayback:    m1.CrownRestorePlayback{RestoreDigest: restoreDigest, PlaybackDigest: playbackDigest, DigestsEqual: restoreDigest == playbackDigest, PlaybackAdapterCalls: playbackCalls, PlaybackDurableWrites: playbackWrites},
		Reviews:            []m1.CrownReview{w2.CrownReview, w3.CrownReview}, Integration: m1.CrownIntegration{TargetRef: integrations[0].TargetRef, CandidateIDs: []string{w2.Handoff.Candidate, w3.Handoff.Candidate}, PublicationOrder: publicationOrder, CompetingAttempts: len([]provingground.IntegrationItem{w2.Item, w3.Item}), Serialized: serialized, SuccessfulPublications: len(integrations), FalseCompletions: 0}}
	result.Invariants, result.Observation = invariants, observation
	result.Reviews = []basereview.Record{w2.Review, w3.Review}
	_ = restartArtifact
	finished := recorder.finish(result)
	if len(finished.Segments) != len(crownInternalSegmentIDs) {
		return finished, fmt.Errorf("internal crown segment census=%d want=%d", len(finished.Segments), len(crownInternalSegmentIDs))
	}
	for i := range finished.Segments {
		if finished.Segments[i].ID != crownInternalSegmentIDs[i] {
			return finished, fmt.Errorf("internal crown segment order drifted at %d", i)
		}
	}
	return finished, nil
}

func validateCrownFaultWorkspaceIdentities(oldW1, oldW2 provingground.Delivery, gj03 Record, definitiveW3 controljournal.WorkspaceID) error {
	orphan := controljournal.WorkspaceID(gj03.Facts.PhysicalWorkspaceID)
	recovered := controljournal.WorkspaceID(gj03.Facts.RecoveredWorkspaceID)
	if gj03.Outcome != OutcomePass || orphan == "" || recovered == "" || orphan == recovered ||
		orphan == oldW1.Authority.WorkspaceID || orphan == oldW2.Authority.WorkspaceID ||
		recovered == oldW1.Authority.WorkspaceID || recovered == oldW2.Authority.WorkspaceID {
		return fmt.Errorf("GJ-03 orphan and recovered workspace identities are not distinct from W1/W2")
	}
	if definitiveW3 != "" && (definitiveW3 == orphan || definitiveW3 == recovered || definitiveW3 == oldW1.Authority.WorkspaceID || definitiveW3 == oldW2.Authority.WorkspaceID) {
		return fmt.Errorf("definitive W3 reused an existing or GJ-03 fault-attempt workspace")
	}
	return nil
}

type crownRecorder struct {
	ctx         context.Context
	runID       string
	environment *productionEnvironment
	segments    []m1.CrownSegment
	artifacts   [][]byte
}

func (r *crownRecorder) capture(id string, run func() (any, error)) error {
	before, err := crownRevision(r.ctx, r.environment)
	if err != nil {
		return err
	}
	value, runErr := run()
	after, afterErr := crownRevision(r.ctx, r.environment)
	if afterErr != nil {
		runErr = errors.Join(runErr, afterErr)
	}
	outcome := "pass"
	if runErr != nil {
		outcome = "fail"
	}
	artifact, _ := m1.CanonicalJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		RunID         string `json:"run_id"`
		ID            string `json:"id"`
		Before, After uint64
		Outcome       string `json:"outcome"`
		Value         any    `json:"value"`
		Diagnostic    string `json:"diagnostic,omitempty"`
	}{1, r.runID, id, before, after, outcome, value, bounded(errorString(runErr))})
	r.segments = append(r.segments, m1.CrownSegment{RunID: r.runID, Sequence: 0, ID: id, BeforeDurableRevision: before, AfterDurableRevision: after, Started: true, Terminal: true, Outcome: outcome, ArtifactSHA256: m1.SHA256Hex(artifact)})
	r.artifacts = append(r.artifacts, artifact)
	return runErr
}

func (r *crownRecorder) finish(result CrownExecution) CrownExecution {
	result.Segments = append([]m1.CrownSegment(nil), r.segments...)
	result.Artifacts = append([][]byte(nil), r.artifacts...)
	return result
}

func crownRevision(ctx context.Context, environment *productionEnvironment) (uint64, error) {
	inspection, err := environment.port.Inspect(ctx)
	return inspection.CurrentRevision, err
}

func startCrownClaims(ctx context.Context, environment *productionEnvironment) (map[string]provingground.Delivery, error) {
	now := time.Now().UTC()
	governance, err := qualificationGovernance(now)
	if err != nil {
		return nil, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return nil, err
	}
	type request struct{ label, work, actor, process, workspaceID, branch string }
	requests := []request{{"w1", "work-a", "actor-a", "process-a", "workspace-crown-w1", "runs/crown-w1"}, {"w2", "work-baseline", "actor-b", "process-b", "workspace-crown-w2", "runs/crown-w2"}}
	start := make(chan struct{})
	type response struct {
		label    string
		delivery provingground.Delivery
		err      error
	}
	responses := make(chan response, 2)
	for i, req := range requests {
		go func(index int, req request) {
			<-start
			delivery, runErr := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID("crown-claim-" + req.label), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: ids.WorkID(req.work), ActorID: ids.ActorID(req.actor), ProcessID: ids.ProcessID(req.process), Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID("crown-bind-" + req.label), WorkspaceID: controljournal.WorkspaceID(req.workspaceID), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-crown-" + req.label), OwnedBranch: req.branch, PinnedBase: base, Now: now.Add(time.Duration(index+1) * time.Millisecond)})
			responses <- response{req.label, delivery, runErr}
		}(i, req)
	}
	close(start)
	result := make(map[string]provingground.Delivery, 2)
	for range 2 {
		response := <-responses
		if response.err != nil {
			return nil, response.err
		}
		result[response.label] = response.delivery
	}
	w1, w2 := result["w1"], result["w2"]
	if w1.Authority.LeaseID == w2.Authority.LeaseID || w1.Authority.WorkspaceID == w2.Authority.WorkspaceID || w1.Run.GitDir == w2.Run.GitDir || w1.Run.IndexPath == w2.Run.IndexPath {
		return nil, fmt.Errorf("concurrent claims did not receive isolated authorities/workspaces")
	}
	environment.workA, environment.baseline = &w1, w2
	return result, nil
}

func startCrownWorkspace(ctx context.Context, environment *productionEnvironment, claimOp, bindOp, work, actor, process, workspaceID, branch string, now time.Time) (provingground.Delivery, error) {
	governance, err := qualificationGovernance(now)
	if err != nil {
		return provingground.Delivery{}, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return provingground.Delivery{}, err
	}
	return environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID(claimOp), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: ids.WorkID(work), ActorID: ids.ActorID(actor), ProcessID: ids.ProcessID(process), Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID(bindOp), WorkspaceID: controljournal.WorkspaceID(workspaceID), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-" + workspaceID), OwnedBranch: branch, PinnedBase: base, Now: now.Add(time.Millisecond)})
}

type crownEligibility struct{}

func (crownEligibility) WorkEligibilityFact(_ context.Context, revision ids.WorkRevisionID, work ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	return workgraph.NewEligibilityFact("crown-reconciliation", revision, "revision-qualification-next", work, status, workgraph.Continue, operation)
}

func crownReviewComposition(ctx context.Context, environment *productionEnvironment) (*provingground.ReviewCoordinator, *provingground.IntegrationCoordinator, error) {
	reviews, err := provingground.NewReviewCoordinator(ctx, environment.runtime, environment.app, environment.port, environment.repository, crownEligibility{}, map[ids.WorkID][]string{
		"work-a":        {"criterion-crown"},
		"work-baseline": {"criterion-crown"},
	})
	if err != nil {
		return nil, nil, err
	}
	integrator, err := provingground.NewIntegrationCoordinator(reviews)
	if err != nil {
		return nil, nil, err
	}
	return reviews, integrator, nil
}

type crownCandidate struct {
	Item            provingground.IntegrationItem `json:"item"`
	Handoff         handoff.Record                `json:"handoff"`
	HandoffEvidence crownCandidateEvidenceV1      `json:"handoff_evidence"`
	Review          basereview.Record             `json:"review"`
	ReviewEvidence  crownReviewEvidenceV1         `json:"review_evidence"`
	Observation     m1.ReviewObservation          `json:"observation"`
	CrownReview     m1.CrownReview                `json:"crown_review"`
}

type crownHandoffArtifact struct {
	Record   handoff.Record           `json:"record"`
	Evidence crownCandidateEvidenceV1 `json:"evidence"`
}

type crownReviewArtifact struct {
	Record   basereview.Record     `json:"record"`
	Evidence crownReviewEvidenceV1 `json:"evidence"`
}

type crownCandidateEvidenceV1 struct {
	SchemaVersion   int    `json:"schema_version"`
	Candidate       string `json:"candidate"`
	WorkspaceID     string `json:"workspace_id"`
	ActorID         string `json:"actor_id"`
	ProcessID       string `json:"process_id"`
	RunID           string `json:"run_id"`
	WorkID          string `json:"work_id"`
	SourceCommit    string `json:"source_commit"`
	LeaseID         string `json:"lease_id"`
	LeaseGeneration uint64 `json:"lease_generation"`
}

type crownReviewEvidenceV1 struct {
	SchemaVersion           int                          `json:"schema_version"`
	Candidate               string                       `json:"candidate"`
	CandidateEvidenceSHA256 string                       `json:"candidate_evidence_sha256"`
	ReviewerActorID         string                       `json:"reviewer_actor_id"`
	ReviewerProcessID       string                       `json:"reviewer_process_id"`
	Criteria                []basereview.CriterionResult `json:"criteria"`
}

func canonicalCrownEvidence(value any) (string, []byte, error) {
	encoded, err := m1.CanonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	return m1.SHA256Hex(encoded), encoded, nil
}

func validateCrownHandoffArtifact(artifact crownHandoffArtifact, sourceCommit string) error {
	digest, _, err := canonicalCrownEvidence(artifact.Evidence)
	if err != nil {
		return fmt.Errorf("canonicalize crown handoff evidence: %w", err)
	}
	contributors := artifact.Record.Contributors
	if digest != artifact.Record.EvidenceDigest || artifact.Evidence.SourceCommit != sourceCommit ||
		artifact.Evidence.Candidate != artifact.Record.Candidate || artifact.Evidence.WorkspaceID != artifact.Record.WorkspaceID.String() ||
		artifact.Evidence.RunID != artifact.Record.RunID.String() || artifact.Evidence.WorkID != artifact.Record.WorkID.String() ||
		artifact.Evidence.LeaseID != artifact.Record.LeaseID.String() || int64(artifact.Evidence.LeaseGeneration) != artifact.Record.LeaseGeneration ||
		len(contributors) != 1 || artifact.Evidence.ActorID != contributors[0].String() {
		return fmt.Errorf("crown handoff evidence does not bind its admitted record and source")
	}
	return nil
}

func validateCrownReviewArtifact(artifact crownReviewArtifact, candidateEvidenceDigest string) error {
	digest, _, err := canonicalCrownEvidence(artifact.Evidence)
	if err != nil {
		return fmt.Errorf("canonicalize crown review evidence: %w", err)
	}
	if digest != artifact.Record.EvidenceDigest || artifact.Evidence.CandidateEvidenceSHA256 != candidateEvidenceDigest ||
		artifact.Evidence.Candidate != artifact.Record.Candidate || artifact.Evidence.ReviewerActorID != artifact.Record.ReviewerActorID.String() ||
		artifact.Evidence.ReviewerProcessID != artifact.Record.ReviewerProcessID.String() || !reflect.DeepEqual(artifact.Evidence.Criteria, artifact.Record.Criteria) {
		return fmt.Errorf("crown review evidence does not bind its admitted record and candidate evidence")
	}
	return nil
}

func newCrownHandoffRequest(label, candidate, sourceCommit string, delivery provingground.Delivery, workID ids.WorkID, fact handoff.EligibilityFact, now time.Time) (handoff.Request, crownCandidateEvidenceV1, error) {
	evidence := crownCandidateEvidenceV1{
		SchemaVersion: 1, Candidate: candidate, WorkspaceID: string(delivery.Authority.WorkspaceID),
		ActorID: string(delivery.Authority.ActorID), ProcessID: string(delivery.Authority.ProcessID),
		RunID: string(delivery.Authority.RunID), WorkID: workID.String(), SourceCommit: sourceCommit,
		LeaseID: string(delivery.Authority.LeaseID), LeaseGeneration: delivery.Authority.LeaseGeneration,
	}
	evidenceDigest, _, err := canonicalCrownEvidence(evidence)
	if err != nil {
		return handoff.Request{}, crownCandidateEvidenceV1{}, err
	}
	request := handoff.Request{SchemaVersion: 1, CorrelationID: "crown-handoff-" + label, OperationID: ids.OperationID("crown-handoff-op-" + label), HandoffID: ids.HandoffID("crown-handoff-" + label), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: workID, RunID: ids.RunID(delivery.Authority.RunID), WorkspaceID: ids.WorkspaceID(delivery.Authority.WorkspaceID), LeaseID: ids.LeaseID(delivery.Authority.LeaseID), LeaseGeneration: int64(delivery.Authority.LeaseGeneration), Token: fmt.Sprint(delivery.Authority.FenceToken), ActorID: ids.ActorID(delivery.Authority.ActorID), ProcessID: ids.ProcessID(delivery.Authority.ProcessID), Candidate: candidate, Contributors: []ids.ActorID{ids.ActorID(delivery.Authority.ActorID)}, EvidenceDigest: evidenceDigest, SubmittedAt: now, ExpectedEligibility: fact}
	return request, evidence, nil
}

func newCrownReviewRequest(label, candidate, reviewer, candidateEvidenceDigest string, delivery provingground.Delivery, workID ids.WorkID, fact basereview.EligibilityFact, now time.Time) (basereview.Request, crownReviewEvidenceV1, error) {
	criteria := []basereview.CriterionResult{{CriterionID: "criterion-crown", Status: basereview.Passed, Evidence: "exact crown candidate inspected"}}
	reviewerProcess := reviewer + "-process"
	evidence := crownReviewEvidenceV1{SchemaVersion: 1, Candidate: candidate, CandidateEvidenceSHA256: candidateEvidenceDigest, ReviewerActorID: reviewer, ReviewerProcessID: reviewerProcess, Criteria: criteria}
	evidenceDigest, _, err := canonicalCrownEvidence(evidence)
	if err != nil {
		return basereview.Request{}, crownReviewEvidenceV1{}, err
	}
	request := basereview.Request{SchemaVersion: 1, CorrelationID: "crown-review-" + label, OperationID: ids.OperationID("crown-review-op-" + label), ReviewID: ids.ReviewID("crown-review-" + label), HandoffID: ids.HandoffID("crown-handoff-" + label), WorkRevisionID: "revision-qualification", WorkID: workID, LeaseID: ids.LeaseID(delivery.Authority.LeaseID), LeaseGeneration: int64(delivery.Authority.LeaseGeneration), Token: "crown-review-" + label, ActorID: ids.ActorID(reviewer), ProcessID: ids.ProcessID(reviewerProcess), Candidate: candidate, Verdict: basereview.Approved, Criteria: criteria, EvidenceDigest: evidenceDigest, SubmittedAt: now.Add(time.Nanosecond), ExpectedEligibility: fact}
	return request, evidence, nil
}

func prepareCrownCandidate(ctx context.Context, environment *productionEnvironment, reviews *provingground.ReviewCoordinator, delivery provingground.Delivery, label, reviewer, sourceCommit string) (crownCandidate, error) {
	result := crownCandidate{}
	now := time.Now().UTC()
	refreshed, err := environment.runtime.Heartbeat(ctx, controljournal.OperationID("crown-heartbeat-"+label), delivery.Authority, now, qualificationLeaseTTL)
	if err != nil {
		return result, err
	}
	delivery.Authority = refreshed
	delivery.Run.Deadline = now.Add(qualificationLeaseTTL)
	if delivery.Authority.WorkspaceID == environment.baseline.Authority.WorkspaceID {
		environment.baseline = delivery
	}
	if environment.workA != nil && delivery.Authority.WorkspaceID == environment.workA.Authority.WorkspaceID {
		environment.workA = &delivery
	}
	binding, ok := environment.runtime.Binding(delivery.Authority.WorkspaceID)
	if !ok {
		return result, fmt.Errorf("crown candidate %s workspace binding is absent", label)
	}
	commit := exec.CommandContext(ctx, "git", "-C", binding.CanonicalPath, "commit", "--allow-empty", "-m", "crown candidate "+label)
	if output, commitErr := commit.CombinedOutput(); commitErr != nil {
		return result, fmt.Errorf("create crown candidate %s: %s: %w", label, bounded(string(output)), commitErr)
	}
	head := exec.CommandContext(ctx, "git", "-C", binding.CanonicalPath, "rev-parse", "HEAD")
	output, err := head.Output()
	if err != nil {
		return result, err
	}
	candidate := strings.TrimSpace(string(output))
	workID := ids.WorkID(delivery.Run.WorkID)
	handoffFact, err := crownEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", workID, workgraph.StatusActive, workgraph.EligibilityHandoff)
	if err != nil {
		return result, err
	}
	handoffRequest, handoffEvidence, err := newCrownHandoffRequest(label, candidate, sourceCommit, delivery, workID, handoffFact, now)
	if err != nil {
		return result, err
	}
	result.HandoffEvidence = handoffEvidence
	result.Handoff, err = reviews.SubmitCandidate(ctx, delivery.Authority, handoffRequest)
	if err != nil {
		return result, err
	}
	if err := validateCrownHandoffArtifact(crownHandoffArtifact{result.Handoff, result.HandoffEvidence}, sourceCommit); err != nil {
		return result, err
	}
	reviewFact, err := crownEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", workID, workgraph.StatusHandedOff, workgraph.EligibilityReview)
	if err != nil {
		return result, err
	}
	reviewRequest, reviewEvidence, err := newCrownReviewRequest(label, candidate, reviewer, handoffRequest.EvidenceDigest, delivery, workID, reviewFact, now)
	if err != nil {
		return result, err
	}
	result.ReviewEvidence = reviewEvidence
	reviewID := reviewRequest.ReviewID
	result.Review, err = reviews.SubmitReview(ctx, delivery.Authority, reviewRequest)
	if err != nil {
		return result, err
	}
	if err := validateCrownReviewArtifact(crownReviewArtifact{result.Review, result.ReviewEvidence}, handoffRequest.EvidenceDigest); err != nil {
		return result, err
	}
	integrationFact, err := crownEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", workID, workgraph.StatusApproved, workgraph.EligibilityIntegration)
	if err != nil {
		return result, err
	}
	result.Item = provingground.IntegrationItem{Capsule: delivery.Authority, Request: baseintegration.Request{SchemaVersion: 1, CorrelationID: "crown-integration-" + label, OperationID: ids.OperationID("crown-integrate-" + label), ReviewID: reviewID, RepositoryID: "repository-qualification", TargetRef: "refs/heads/main", ActorID: "integrator-crown", ProcessID: "integrator-crown-process", Candidate: candidate, LeaseTTL: qualificationLeaseTTL, StartedAt: now.Add(2 * time.Nanosecond), ExpectedEligibility: integrationFact}}
	contributors := []string{string(delivery.Authority.ActorID)}
	independent := reviewer != string(delivery.Authority.ActorID)
	result.Observation = m1.ReviewObservation{CandidateID: candidate, ContributorIDs: contributors, ReviewerActorID: reviewer, Verdict: string(result.Review.Verdict), CandidateImmutable: result.Handoff.Verification.ObservedBranchHead == candidate}
	result.CrownReview = m1.CrownReview{CandidateID: candidate, ContributorActorIDs: contributors, ReviewerActorID: reviewer, ActorIndependent: independent, Verdict: string(result.Review.Verdict)}
	return result, nil
}

func crownParticipant(delivery provingground.Delivery) (m1.CrownParticipant, error) {
	observed := struct {
		actor, process, workspace string
	}{string(delivery.Authority.ActorID), string(delivery.Authority.ProcessID), string(delivery.Authority.WorkspaceID)}
	switch observed {
	case struct{ actor, process, workspace string }{"actor-a", "process-a", "workspace-crown-w1"}:
		return m1.CrownParticipant{ActorID: "A", ProcessID: "A", WorkspaceID: "W1"}, nil
	case struct{ actor, process, workspace string }{"actor-b", "process-b", "workspace-crown-w2"}:
		return m1.CrownParticipant{ActorID: "B", ProcessID: "B", WorkspaceID: "W2"}, nil
	case struct{ actor, process, workspace string }{"actor-a", "process-c", "workspace-crown-w3"}:
		return m1.CrownParticipant{ActorID: "A", ProcessID: "C", WorkspaceID: "W3"}, nil
	default:
		return m1.CrownParticipant{}, fmt.Errorf("observed crown authority does not match a frozen evidence participant")
	}
}

type crownBarrier struct {
	mu        sync.Mutex
	remaining int
	release   chan struct{}
}

func newCrownBarrier(participants int) *crownBarrier {
	return &crownBarrier{remaining: participants, release: make(chan struct{})}
}
func (b *crownBarrier) Arrive(ctx context.Context) error {
	b.mu.Lock()
	b.remaining--
	if b.remaining == 0 {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
		return nil
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
