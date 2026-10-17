package helpercmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type factorySpy struct {
	mu          sync.Mutex
	decision    controlroot.Decision
	classifyErr error
	startErr    error
	session     *sessionSpy
	classified  int
	observed    int
	started     int
}

func (f *factorySpy) Classify(context.Context, string) (controlroot.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.classified++
	return f.decision, f.classifyErr
}
func (f *factorySpy) Start(context.Context, string) (hostSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.session, nil
}
func (f *factorySpy) Observe(context.Context, string) (observationSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed++
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.session, nil
}

type sessionSpy struct {
	mu        sync.Mutex
	status    productionruntime.HostStatus
	progress  productionruntime.MissionProgress
	queryErr  error
	closeErr  error
	missionFn func(journalv2.ProjectID, journalv2.MissionID) (productionruntime.MissionProgress, error)
	executeFn func(decodedOperation) (TypedResult, error)
	queries   int
	executes  int
	closes    int
}

func (s *sessionSpy) Status() productionruntime.HostStatus { return s.status }
func (s *sessionSpy) Mission(project journalv2.ProjectID, missionID journalv2.MissionID) (productionruntime.MissionProgress, error) {
	s.mu.Lock()
	s.queries++
	fn := s.missionFn
	progress, err := s.progress, s.queryErr
	s.mu.Unlock()
	if fn != nil {
		return fn(project, missionID)
	}
	return progress, err
}
func (s *sessionSpy) Operation(_ context.Context, _ journalv2.ProjectID, _ journalv2.MissionID, operation string) (productionruntime.ProgressItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	if s.queryErr != nil {
		return productionruntime.ProgressItem{}, s.queryErr
	}
	return productionruntime.ProgressItem{Family: "operation", AggregateID: operation, LogicalVersion: 1, AuthorityState: "resolved", CanonicalState: []byte(`{"ok":true}`)}, nil
}
func (s *sessionSpy) Execute(_ context.Context, operation decodedOperation) (TypedResult, error) {
	s.mu.Lock()
	s.executes++
	fn := s.executeFn
	s.mu.Unlock()
	if fn != nil {
		return fn(operation)
	}
	return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"schema_version":1,"ok":true}`)}, nil
}
func (s *sessionSpy) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return s.closeErr
}

func TestQueriesUseOneHostAndNeverInvokeMutation(t *testing.T) {
	session := defaultSession()
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	root := filepath.Join(t.TempDir(), "authority")
	for _, args := range [][]string{
		{"--control-root", root, "status"},
		{"--control-root", root, "progress", "--project", "project-1", "--mission", "mission-1"},
		{"--control-root", root, "operation", "inspect", "--project", "project-1", "--mission", "mission-1", "--operation", "operation-1"},
	} {
		var stdout bytes.Buffer
		if code := runner.Run(t.Context(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); code != ExitSuccess {
			t.Fatalf("args=%v exit=%d output=%s", args, code, stdout.String())
		}
		var envelope Envelope
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.SchemaVersion != 1 || envelope.Outcome != OutcomeSuccess {
			t.Fatalf("args=%v envelope=%+v err=%v", args, envelope, err)
		}
	}
	if factory.observed != 3 || factory.started != 0 || session.closes != 3 || session.executes != 0 {
		t.Fatalf("observe=%d start=%d close=%d execute=%d", factory.observed, factory.started, session.closes, session.executes)
	}
}

func TestOfflineRootRefusesBeforeHostStart(t *testing.T) {
	disposition := controlroot.Disposition{SchemaVersion: 1, ControlRootSHA256: strings.Repeat("a", 64), Classification: controlroot.Historical, Findings: []controlroot.Finding{{Kind: controlroot.FindingHistorical, RelativePath: "control.db", DetailCode: "SQLITE_HEADER"}}, Action: controlroot.OfflineDispositionRequired, DiagnosisCode: "HISTORICAL_STATE_DETECTED"}
	factory := &factorySpy{decision: controlroot.Decision{Action: controlroot.OfflineDispositionRequired, Disposition: disposition}, session: defaultSession()}
	runner, _ := NewRunner(factory)
	var stdout bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", filepath.Join(t.TempDir(), "authority"), "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	var envelope Envelope
	_ = json.Unmarshal(stdout.Bytes(), &envelope)
	if code != ExitRefused || envelope.Diagnostic == nil || envelope.Diagnostic.Kind != controlroot.OfflineDispositionRequiredCode || factory.started != 0 {
		t.Fatalf("exit=%d envelope=%+v starts=%d", code, envelope, factory.started)
	}
}

func TestUnknownCommandAndSubcommandHelpDoNotStartHost(t *testing.T) {
	factory := admittedFactory(defaultSession())
	runner, _ := NewRunner(factory)
	root := filepath.Join(t.TempDir(), "authority")
	for _, args := range [][]string{{"--control-root", root, "invent"}, {"progress", "--help"}} {
		var stdout bytes.Buffer
		code := runner.Run(t.Context(), args, strings.NewReader(""), &stdout, &bytes.Buffer{})
		if args[0] == "progress" && code != ExitSuccess {
			t.Fatalf("help exit=%d output=%s", code, stdout.String())
		}
	}
	if factory.started != 0 || factory.classified != 0 {
		t.Fatalf("invalid/help reached host: classify=%d start=%d", factory.classified, factory.started)
	}
}

func TestInvalidCommandInputIsRejectedBeforeClassificationOrHostStart(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"missing progress identity", []string{"progress", "--project", "project-1"}, ""},
		{"malformed operation", []string{"operation", "execute", "--request", "-"}, `{"schema_version":1}`},
		{"malformed drive plan", []string{"drive", "--plan", "-"}, `{"schema_version":1,"operations":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := admittedFactory(defaultSession())
			runner, _ := NewRunner(factory)
			root := filepath.Join(t.TempDir(), "authority")
			args := append([]string{"--control-root", root}, test.args...)
			if code := runner.Run(t.Context(), args, strings.NewReader(test.stdin), &bytes.Buffer{}, &bytes.Buffer{}); code != ExitInvalid {
				t.Fatalf("exit=%d", code)
			}
			if factory.classified != 0 || factory.started != 0 {
				t.Fatalf("invalid input reached authority: classify=%d start=%d", factory.classified, factory.started)
			}
		})
	}
}

func TestOperationResumePreservesExactTypedIdentity(t *testing.T) {
	request := missionCreateRequest(t, "operation-resume-1")
	var firstSHA string
	session := defaultSession()
	session.executeFn = func(operation decodedOperation) (TypedResult, error) {
		if firstSHA == "" {
			firstSHA = operation.requestSHA
		} else if operation.requestSHA != firstSHA {
			return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}
		}
		return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"operation_id":"operation-resume-1","replayed":true}`)}, nil
	}
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	root := filepath.Join(t.TempDir(), "authority")
	encoded, _ := json.Marshal(request)
	for _, verb := range []string{"execute", "resume"} {
		var stdout bytes.Buffer
		args := []string{"--control-root", root, "operation", verb, "--request", "-"}
		if code := runner.Run(t.Context(), args, bytes.NewReader(encoded), &stdout, &bytes.Buffer{}); code != ExitSuccess {
			t.Fatalf("%s exit=%d output=%s", verb, code, stdout.String())
		}
		var envelope Envelope
		_ = json.Unmarshal(stdout.Bytes(), &envelope)
		if envelope.RequestSHA256 != firstSHA || envelope.RequestSHA256 == "" {
			t.Fatalf("%s request identity=%q want=%q", verb, envelope.RequestSHA256, firstSHA)
		}
	}
	if session.executes != 2 || session.closes != 2 {
		t.Fatalf("execute=%d close=%d", session.executes, session.closes)
	}
}

func TestOperationIDReuseWithDifferentTypedRequestIsConflict(t *testing.T) {
	request := missionCreateRequest(t, "operation-conflict-1")
	var admittedSHA string
	session := defaultSession()
	session.executeFn = func(operation decodedOperation) (TypedResult, error) {
		if admittedSHA == "" {
			admittedSHA = operation.requestSHA
			return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"ok":true}`)}, nil
		}
		if operation.requestSHA != admittedSHA {
			return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}
		}
		return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"ok":true}`)}, nil
	}
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	root := filepath.Join(t.TempDir(), "authority")
	run := func(request OperationRequest) (int, Envelope) {
		encoded, _ := json.Marshal(request)
		var stdout bytes.Buffer
		code := runner.Run(t.Context(), []string{"--control-root", root, "operation", "resume", "--request", "-"}, bytes.NewReader(encoded), &stdout, &bytes.Buffer{})
		var envelope Envelope
		_ = json.Unmarshal(stdout.Bytes(), &envelope)
		return code, envelope
	}
	if code, _ := run(request); code != ExitSuccess {
		t.Fatal(code)
	}
	var payload missionCommandWire
	_ = json.Unmarshal(request.Payload, &payload)
	payload.ActorID = "actor-2"
	request.Payload, _ = json.Marshal(payload)
	code, envelope := run(request)
	if code != ExitConflict || envelope.Diagnostic == nil || envelope.Diagnostic.Kind != string(mission.MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("exit=%d envelope=%+v", code, envelope)
	}
}

func TestDriveStopsBeforeUnauthorizedMutation(t *testing.T) {
	session := defaultSession()
	session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionPause), Scope: "mission:mission-1", Status: "unavailable", BlockingPredicates: []string{"provider"}}}
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	request := missionCreateRequest(t, "operation-drive-1")
	request.Action = string(mission.ActionMissionPause)
	var payload missionCommandWire
	_ = json.Unmarshal(request.Payload, &payload)
	payload.EventType = mission.MissionEventPausedV2
	request.Payload, _ = json.Marshal(payload)
	plan, _ := json.Marshal(DrivePlan{SchemaVersion: 1, Operations: []OperationRequest{request}})
	var stdout bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", filepath.Join(t.TempDir(), "authority"), "drive", "--plan", "-"}, bytes.NewReader(plan), &stdout, &bytes.Buffer{})
	if code != ExitRefused || session.executes != 0 {
		t.Fatalf("exit=%d executes=%d output=%s", code, session.executes, stdout.String())
	}
}

func TestDriveDoesNotClaimTerminalWhenBoundedPlanIsExhausted(t *testing.T) {
	session := defaultSession()
	session.progress.Lifecycle = string(mission.MissionOpen)
	session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionCreate), Scope: "mission:mission-1", Status: "authorized"}}
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	request := missionCreateRequest(t, "operation-drive-active")
	plan, _ := json.Marshal(DrivePlan{SchemaVersion: 1, Operations: []OperationRequest{request}})
	var stdout bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", filepath.Join(t.TempDir(), "authority"), "drive", "--plan", "-"}, bytes.NewReader(plan), &stdout, &bytes.Buffer{})
	var envelope Envelope
	_ = json.Unmarshal(stdout.Bytes(), &envelope)
	if code != ExitRefused || envelope.Drive == nil || envelope.Drive.Terminal || envelope.Drive.StopReason != "plan_exhausted" || session.queries < 2 {
		t.Fatalf("exit=%d queries=%d envelope=%+v", code, session.queries, envelope)
	}
}

func TestDriveExecutesIndependentWorkScopesInOneMissionConcurrently(t *testing.T) {
	session := defaultSession()
	session.missionFn = func(_ journalv2.ProjectID, _ journalv2.MissionID) (productionruntime.MissionProgress, error) {
		return productionruntime.MissionProgress{
			Lifecycle: string(mission.MissionCompleted),
			Actions: []productionruntime.ActionAvailability{
				{Action: string(mission.ActionExecutionClaim), Scope: "work:work-left", Status: "authorized"},
				{Action: string(mission.ActionExecutionClaim), Scope: "work:work-right", Status: "authorized"},
			},
		}, nil
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	session.executeFn = func(decodedOperation) (TypedResult, error) {
		entered <- struct{}{}
		<-release
		return TypedResult{Kind: ResultExecution, Payload: json.RawMessage(`{"ok":true}`)}, nil
	}
	left := executionClaimRequest(t, "work-left", "operation-left")
	right := executionClaimRequest(t, "work-right", "operation-right")
	plan, _ := json.Marshal(DrivePlan{SchemaVersion: 1, Operations: []OperationRequest{left, right}})
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	done := make(chan int, 1)
	root := filepath.Join(t.TempDir(), "authority")
	var stdout bytes.Buffer
	go func() {
		done <- runner.Run(t.Context(), []string{"--control-root", root, "drive", "--plan", "-"}, bytes.NewReader(plan), &stdout, &bytes.Buffer{})
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("independent actions were serialized")
		}
	}
	close(release)
	if code := <-done; code != ExitSuccess {
		t.Fatalf("exit=%d", code)
	}
	if session.executes != 2 {
		t.Fatalf("executes=%d", session.executes)
	}
	var envelope Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.Drive == nil || len(envelope.Drive.Steps) != 2 {
		t.Fatalf("drive envelope=%+v err=%v output=%s", envelope, err, stdout.String())
	}
	for _, step := range envelope.Drive.Steps {
		if step.Result == nil || step.Result.Kind != ResultExecution || !bytes.Equal(step.Result.Payload, []byte(`{"ok":true}`)) {
			t.Fatalf("drive discarded typed result: %+v", step)
		}
	}
}

func TestDriveRequiresExactAuthorizedScope(t *testing.T) {
	session := defaultSession()
	session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionExecutionClaim), Scope: "work:other-work", Status: "authorized"}}
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	plan, _ := json.Marshal(DrivePlan{SchemaVersion: 1, Operations: []OperationRequest{executionClaimRequest(t, "work-target", "operation-scope")}})
	var stdout bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", filepath.Join(t.TempDir(), "authority"), "drive", "--plan", "-"}, bytes.NewReader(plan), &stdout, &bytes.Buffer{})
	if code != ExitRefused || session.executes != 0 {
		t.Fatalf("scope mismatch crossed authority: exit=%d executes=%d output=%s", code, session.executes, stdout.String())
	}
}

func TestClosedReadinessFactRetainsItsExactWorkScope(t *testing.T) {
	digest, err := journalv2.ParseDigest("definition", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	scoped := mission.ScopedMissionFactV2{
		Kind: mission.ScopedFactDecisionV2, FactID: "decision-close-scope",
		Scope:            mission.ActionRef{ID: mission.ActionAcceptanceEvaluate, Scope: mission.AuthorityScope{Kind: mission.ScopeWork, Identity: "work-scoped"}},
		EvidenceIdentity: "decision-evidence", EvidenceDigest: strings.Repeat("b", 64),
	}
	expected, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: digest,
		Version: 1, Lifecycle: mission.MissionOpen, ActiveFacts: []mission.ScopedMissionFactV2{scoped},
		LastOperationID: "operation-fact-open", ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: digest,
		Version: 2, Lifecycle: mission.MissionOpen, LastOperationID: "operation-fact-close", ObservedAt: "2026-09-05T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: scoped.FactID, Kind: mission.MissionFactDecisionV2,
		Status: mission.MissionFactOpenV2, LogicalVersion: 1, DetailDigest: digest, LastOperationID: "operation-fact-open", ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	successor, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: scoped.FactID, Kind: mission.MissionFactDecisionV2,
		Status: mission.MissionFactResolvedV2, LogicalVersion: 2, DetailDigest: digest, LastOperationID: "operation-fact-close", ObservedAt: "2026-09-05T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(readinessCommandWire{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: "operation-fact-close", ProjectID: "project-1", MissionID: "mission-1",
		ActorID: "actor-1", ProcessID: "process-1", RuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1",
		ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: "2026-09-05T12:01:00Z",
		EventType: mission.ReadinessEventFactClosedV2, Expected: expected.CanonicalBytes(), Updated: updated.CanonicalBytes(),
		RelatedFact: &readinessFactTransitionWire{Prior: prior.CanonicalBytes(), Successor: successor.CanonicalBytes(), ExpectedRecordRevision: 7},
	})
	request := OperationRequest{SchemaVersion: 1, Action: string(mission.ActionDecisionResolve), Scope: "mission:mission-1", Kind: OperationReadiness, Payload: payload}
	if _, err := decodeOperation(request); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("closed fact accepted wrong mission scope: %v", err)
	}
	request.Scope = "work:work-scoped"
	decoded, err := decodeOperation(request)
	if err != nil || decoded.scope != request.Scope {
		t.Fatalf("closed fact lost prior work scope: decoded=%+v err=%v", decoded, err)
	}
}

func TestTypedResultDiscriminatorMustMatchOperationKind(t *testing.T) {
	session := defaultSession()
	session.executeFn = func(decodedOperation) (TypedResult, error) {
		return TypedResult{Kind: ResultReadiness, Payload: json.RawMessage(`{"ok":true}`)}, nil
	}
	runner, _ := NewRunner(admittedFactory(session))
	request := missionCreateRequest(t, "operation-result-kind")
	encoded, _ := json.Marshal(request)
	var stdout bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", filepath.Join(t.TempDir(), "authority"), "operation", "execute", "--request", "-"}, bytes.NewReader(encoded), &stdout, &bytes.Buffer{})
	var envelope Envelope
	_ = json.Unmarshal(stdout.Bytes(), &envelope)
	if code != ExitUnavailable || envelope.Outcome != OutcomeUnavailable || envelope.Result != nil {
		t.Fatalf("mismatched result escaped: exit=%d envelope=%+v", code, envelope)
	}
}

func TestEveryDeclaredNativeOperationHasAConstructibleWireAndResult(t *testing.T) {
	governance, err := execution.ExecutionGovernanceFromContextV2(helperGovernanceContext(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []OperationRequest{missionCreateRequest(t, "operation-wire-mission"), readinessCreateRequest(t), executionClaimRequest(t, "work-wire", "operation-wire-execution")} {
		decoded, decodeErr := decodeOperation(request)
		if decodeErr != nil {
			t.Fatalf("%s wire: %v", request.Kind, decodeErr)
		}
		if decoded.operation == "" || resultKindFor(request.Kind) == "" {
			t.Fatalf("%s wire did not retain its closed operation/result identity", request.Kind)
		}
	}
	tests := []struct {
		kind    OperationKind
		action  string
		scope   string
		payload map[string]any
		result  ResultKind
	}{
		{OperationWorkspaceRebind, string(mission.ActionWorkspaceAllocate), "work:work-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "work_id": "work-1", "operation_id": "operation-rebind", "governance": governance}, ResultWorkspace},
		{OperationHandoff, string(mission.ActionCandidateHandoff), "work:work-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "work_id": "work-1", "operation_id": "operation-handoff", "governance": governance}, ResultHandoff},
		{OperationReview, string(mission.ActionReviewRecord), "work:work-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "work_id": "work-1", "operation_id": "operation-review", "governance": governance}, ResultReview},
		{OperationAcceptance, string(mission.ActionAcceptanceEvaluate), "mission:mission-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "operation_id": "operation-acceptance", "governance": governance}, ResultAcceptance},
		{OperationIntegration, string(mission.ActionIntegrationPublish), "mission:mission-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "operation_id": "operation-integration", "governance": governance}, ResultIntegration},
		{OperationIntegrationCleanup, string(mission.ActionIntegrationPublish), "mission:mission-1", map[string]any{"project_id": "project-1", "mission_id": "mission-1", "cleanup_operation_id": "operation-cleanup", "governance": governance}, ResultIntegrationCleanup},
		{OperationEffect, string(mission.ActionEffectReconcile), "action:effect.reconcile", map[string]any{
			"action":         execution.EffectRequireReconcileV2,
			"intent":         map[string]any{"operation_id": "operation-effect", "target": map[string]any{"project_id": "project-1"}, "governance": governance},
			"execution_slot": map[string]any{"mission_id": "mission-1", "governance": governance},
		}, ResultEffect},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			decoded, err := decodeOperation(OperationRequest{SchemaVersion: 1, Kind: test.kind, Action: test.action, Scope: test.scope, Payload: mustJSON(test.payload)})
			if err != nil {
				t.Fatalf("declared operation has no constructible wire: %v", err)
			}
			if decoded.operation == "" || decoded.projectID == "" || decoded.missionID == "" || resultKindFor(test.kind) != test.result {
				t.Fatalf("declared operation is not closed: decoded=%+v result=%q", decoded, resultKindFor(test.kind))
			}
		})
	}
}

func TestNativeWireRejectsCamelCaseAtNestedBoundaries(t *testing.T) {
	governance, err := execution.ExecutionGovernanceFromContextV2(helperGovernanceContext(t))
	if err != nil {
		t.Fatal(err)
	}
	request := OperationRequest{SchemaVersion: 1, Kind: OperationEffect, Action: string(mission.ActionEffectReconcile), Scope: "action:effect.reconcile", Payload: mustJSON(map[string]any{
		"action":         execution.EffectRequireReconcileV2,
		"intent":         map[string]any{"operation_id": "operation-effect-camel", "target": map[string]any{"ProjectID": "project-1"}, "governance": governance},
		"execution_slot": map[string]any{"mission_id": "mission-1", "governance": governance},
	})}
	if _, err := decodeOperation(request); err == nil || !strings.Contains(err.Error(), "ProjectID") {
		t.Fatalf("nested non-snake-case field escaped strict DTO: %v", err)
	}
}

func TestCurrentEpochSelectorResolvesWithoutRewritingCanonicalRequest(t *testing.T) {
	request := missionCreateRequest(t, "operation-current-selector")
	setRequestEpoch(t, &request, RuntimeEpochCurrent)
	decoded, err := decodeOperation(request)
	if err != nil {
		t.Fatal(err)
	}
	originalPayload := append([]byte(nil), decoded.request.Payload...)
	originalSHA := decoded.requestSHA
	capabilities := &capabilityDispatchSpy{want: OperationMission, currentEpoch: "epoch-current", origin: "epoch-original", originFound: true}
	_, err = (productionSession{capabilities: capabilities}).Execute(t.Context(), decoded)
	if !errors.Is(err, errCapabilityReached) {
		t.Fatalf("selector request did not reach mission capability: %v", err)
	}
	if capabilities.originCalls != 1 || capabilities.missionEpoch != "epoch-original" {
		t.Fatalf("selector resolution origin_calls=%d mission_epoch=%q", capabilities.originCalls, capabilities.missionEpoch)
	}
	if operationEpoch(decoded) != RuntimeEpochCurrent || decoded.requestSHA != originalSHA || !bytes.Equal(decoded.request.Payload, originalPayload) {
		t.Fatalf("canonical request was rewritten: epoch=%q sha=%q payload=%s", operationEpoch(decoded), decoded.requestSHA, decoded.request.Payload)
	}

	concrete := missionCreateRequest(t, "operation-concrete-epoch")
	setRequestEpoch(t, &concrete, "epoch-forged")
	decoded, err = decodeOperation(concrete)
	if err != nil {
		t.Fatal(err)
	}
	capabilities = &capabilityDispatchSpy{want: OperationMission, currentEpoch: "epoch-current"}
	_, err = (productionSession{capabilities: capabilities}).Execute(t.Context(), decoded)
	if !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityStaleEpochV2) {
		t.Fatalf("forged concrete epoch was not refused: %v", err)
	}
	if capabilities.originCalls != 1 || capabilities.calls != 0 || operationEpoch(decoded) != "epoch-forged" {
		t.Fatalf("concrete epoch was normalized or admitted: origin_calls=%d capability_calls=%d request_epoch=%q", capabilities.originCalls, capabilities.calls, operationEpoch(decoded))
	}
}

var errCapabilityReached = errors.New("capability reached")

type capabilityDispatchSpy struct {
	want         OperationKind
	calls        int
	currentEpoch journalv2.RuntimeEpochID
	origin       journalv2.RuntimeEpochID
	originFound  bool
	originCalls  int
	missionEpoch journalv2.RuntimeEpochID
}

func (s *capabilityDispatchSpy) reached(kind OperationKind) error {
	if kind != s.want {
		return errors.New("wrong capability")
	}
	s.calls++
	return errCapabilityReached
}
func (s *capabilityDispatchSpy) RuntimeEpochID() journalv2.RuntimeEpochID { return s.currentEpoch }
func (s *capabilityDispatchSpy) OperationOrigin(context.Context, journalv2.OperationID) (journalv2.RuntimeEpochID, bool, error) {
	s.originCalls++
	return s.origin, s.originFound, nil
}
func (s *capabilityDispatchSpy) ExecuteMission(_ context.Context, _ journalv2.RuntimeEpochID, command mission.MissionAuthorityCommandV2) (mission.MissionAuthorityResultV2, error) {
	s.missionEpoch = command.OriginatingRuntimeEpochID
	return mission.MissionAuthorityResultV2{}, s.reached(OperationMission)
}
func (s *capabilityDispatchSpy) ExecuteReadiness(context.Context, journalv2.RuntimeEpochID, mission.ReadinessAuthorityCommandV2) (mission.ReadinessAuthorityResultV2, error) {
	return mission.ReadinessAuthorityResultV2{}, s.reached(OperationReadiness)
}
func (s *capabilityDispatchSpy) ExecuteWork(context.Context, journalv2.RuntimeEpochID, execution.ExecutionAuthorityRequestV2) (execution.ExecutionAuthorityResultV2, error) {
	return execution.ExecutionAuthorityResultV2{}, s.reached(OperationExecution)
}
func (s *capabilityDispatchSpy) RebindWorkspaceRuntimeEpoch(context.Context, journalv2.RuntimeEpochID, execution.WorkspaceEpochRebindRequestV2) (execution.WorkspaceAuthorityResultV2, error) {
	return execution.WorkspaceAuthorityResultV2{}, s.reached(OperationWorkspaceRebind)
}
func (s *capabilityDispatchSpy) SubmitHandoff(context.Context, journalv2.RuntimeEpochID, handoff.AuthorityRequestV2) (handoff.AuthorityResultV2, error) {
	return handoff.AuthorityResultV2{}, s.reached(OperationHandoff)
}
func (s *capabilityDispatchSpy) SubmitReview(context.Context, journalv2.RuntimeEpochID, review.AuthorityRequestV2) (review.AuthorityResultV2, error) {
	return review.AuthorityResultV2{}, s.reached(OperationReview)
}
func (s *capabilityDispatchSpy) ExecuteAcceptance(context.Context, journalv2.RuntimeEpochID, integration.AcceptanceAuthorityRequestV2) (integration.AcceptanceResultV2, error) {
	return integration.AcceptanceResultV2{}, s.reached(OperationAcceptance)
}
func (s *capabilityDispatchSpy) ExecuteIntegration(context.Context, journalv2.RuntimeEpochID, integration.IntegrationAuthorityRequestV2) (integration.IntegrationAttemptStateV2, error) {
	return integration.IntegrationAttemptStateV2{}, s.reached(OperationIntegration)
}
func (s *capabilityDispatchSpy) ExecuteIntegrationCleanup(context.Context, journalv2.RuntimeEpochID, integration.IntegrationCleanupRequestV2) (integration.IntegrationAttemptStateV2, error) {
	return integration.IntegrationAttemptStateV2{}, s.reached(OperationIntegrationCleanup)
}
func (s *capabilityDispatchSpy) ExecuteEffect(context.Context, journalv2.RuntimeEpochID, execution.EffectAuthorityRequestV2) (execution.EffectAuthorityResultV2, error) {
	return execution.EffectAuthorityResultV2{}, s.reached(OperationEffect)
}

func TestRenderExitCategoryAlwaysAgreesWithEnvelopeOutcome(t *testing.T) {
	var stdout bytes.Buffer
	code := render(&stdout, &bytes.Buffer{}, "drive", "json", Envelope{SchemaVersion: 1, Command: "drive", Outcome: OutcomeStopped}, ExitConflict)
	if code != ExitRefused {
		t.Fatalf("stopped outcome rendered contradictory exit=%d", code)
	}
}

func TestMachineEnvelopeDoesNotDependOnHumanRenderer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "authority")
	jsonFactory := admittedFactory(defaultSession())
	humanFactory := admittedFactory(defaultSession())
	jsonRunner, _ := NewRunner(jsonFactory)
	humanRunner, _ := NewRunner(humanFactory)
	var machine, human bytes.Buffer
	if code := jsonRunner.Run(t.Context(), []string{"--control-root", root, "status"}, strings.NewReader(""), &machine, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	if code := humanRunner.Run(t.Context(), []string{"--control-root", root, "--output", "human", "status"}, strings.NewReader(""), &human, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	var envelope Envelope
	if err := json.Unmarshal(machine.Bytes(), &envelope); err != nil || envelope.Host == nil || !strings.Contains(human.String(), "success") {
		t.Fatalf("machine=%s human=%s err=%v", machine.String(), human.String(), err)
	}
}

func admittedFactory(session *sessionSpy) *factorySpy {
	disposition := controlroot.Disposition{SchemaVersion: 1, ControlRootSHA256: strings.Repeat("a", 64), Classification: controlroot.CurrentSupported, Findings: []controlroot.Finding{}, Action: controlroot.Recover, DiagnosisCode: "CURRENT_SUPPORTED"}
	return &factorySpy{decision: controlroot.Decision{Action: controlroot.Recover, Disposition: disposition}, session: session}
}

func defaultSession() *sessionSpy {
	return &sessionSpy{
		status:   productionruntime.HostStatus{SchemaVersion: 1, State: productionruntime.HostReady, DatabaseID: "database-1", RuntimeEpochID: "epoch-1", ProjectionRevision: 1, ProjectionSHA256: strings.Repeat("a", 64)},
		progress: productionruntime.MissionProgress{SchemaVersion: 1, DatabaseID: "database-1", RuntimeEpochID: "epoch-1", ProjectID: "project-1", MissionID: "mission-1", ProjectionRevision: 1, ProjectionSHA256: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("b", 64), Complete: true},
	}
}

func missionCreateRequest(t *testing.T, operationID journalv2.OperationID) OperationRequest {
	return missionCreateRequestFor(t, "mission-1", operationID)
}

func missionCreateRequestFor(t *testing.T, missionID journalv2.MissionID, operationID journalv2.OperationID) OperationRequest {
	t.Helper()
	digestBytes := sha256.Sum256([]byte("definition"))
	digest, err := journalv2.ParseDigest("definition", strings.ToLower(strings.TrimSpace(bytesToHex(digestBytes[:]))))
	if err != nil {
		t.Fatal(err)
	}
	state, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: missionID, DefinitionRevision: "definition-1", DefinitionDigest: digest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "initialized", LastOperationID: operationID, ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(missionCommandWire{
		SchemaVersion: 1, OperationID: operationID, ProjectID: "project-1", MissionID: missionID, ActorID: "actor-1", ProcessID: "process-1",
		RuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: state.CanonicalBytes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return OperationRequest{SchemaVersion: 1, Action: string(mission.ActionMissionCreate), Scope: "mission:" + string(missionID), Kind: OperationMission, Payload: payload}
}

func executionClaimRequest(t *testing.T, workID string, operationID journalv2.OperationID) OperationRequest {
	t.Helper()
	payload, err := json.Marshal(executionCommandWire{
		Kind: execution.ExecutionAdmitV2, OperationID: operationID, ProjectID: "project-1", MissionID: "mission-1",
		WorkRevisionID: "revision-1", WorkID: executionWorkID(workID), RunID: ids.RunID("run-" + workID),
		ActorID: "actor-1", ProcessID: "process-1", LeaseID: ids.LeaseID("lease-" + workID), Generation: 1,
		Fence: strings.Repeat("f", 32), ObservedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		Deadline: time.Date(2026, 9, 5, 12, 5, 0, 0, time.UTC), RuntimeEpochID: "epoch-1",
		Governance:       helperGovernanceContext(t),
		OperationEventID: journalv2.EventID("event-operation-" + workID), SlotEventID: journalv2.EventID("event-slot-" + workID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return OperationRequest{SchemaVersion: 1, Action: string(mission.ActionExecutionClaim), Scope: "work:" + workID, Kind: OperationExecution, Payload: payload}
}

func readinessCreateRequest(t *testing.T) OperationRequest {
	t.Helper()
	digest, err := journalv2.ParseDigest("definition", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: digest, Version: 1, Lifecycle: mission.MissionOpen, LastOperationID: "operation-wire-readiness", ObservedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(readinessCommandWire{SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: "operation-wire-readiness", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1", RuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.ReadinessEventCreatedV2, Updated: state.CanonicalBytes()})
	if err != nil {
		t.Fatal(err)
	}
	return OperationRequest{SchemaVersion: 1, Action: string(mission.ActionMissionCreate), Scope: "mission:mission-1", Kind: OperationReadiness, Payload: payload}
}

func executionWorkID(value string) ids.WorkID { return ids.WorkID(value) }

func helperGovernanceContext(t *testing.T) governancecontext.Context {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind     governancecontext.EntryKind
		ref      string
		revision string
	}{
		{governancecontext.KindConfig, "config.helper", "policy-1"},
		{governancecontext.KindDoctrine, "doctrine.helper", "doctrine-1"},
		{governancecontext.KindProfile, "profile.helper", "profile-1"},
		{governancecontext.KindMissionType, "mission-type.helper", "mission-type-1"},
	} {
		commitment, err := governancecontext.CommitSHA256([]byte(item.ref))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	value, err := governancecontext.NewContext("governance-helper", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 11, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func bytesToHex(value []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, current := range value {
		result[index*2], result[index*2+1] = digits[current>>4], digits[current&15]
	}
	return string(result)
}

func TestFailureClassificationIsClosed(t *testing.T) {
	tests := []struct {
		err     error
		outcome Outcome
		code    int
		retry   string
	}{
		{&mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}, OutcomeConflict, ExitConflict, "same_operation_id"},
		{&mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityAmbiguousV2, Field: "operation"}, OutcomeAmbiguous, ExitAmbiguous, "same_operation_id"},
		{execution.NewError(execution.KindDecisionRequired, "hidden", execution.RetryDecisionRequired, nil), OutcomeAmbiguous, ExitAmbiguous, string(execution.RetryDecisionRequired)},
		{&workspace.Error{Kind: workspace.KindCleanupRefused, Message: "hidden"}, OutcomeAmbiguous, ExitAmbiguous, "reconcile_same_operation"},
		{&handoff.Error{Kind: handoff.KindNotFound, Message: "hidden"}, OutcomeNotFound, ExitNotFound, "same_operation_id"},
		{&review.Error{Kind: review.KindReviewerNotIndependent, Message: "hidden"}, OutcomeRefused, ExitRefused, "same_operation_id"},
		{&integration.Error{Kind: integration.KindRepairRequired, Message: "hidden"}, OutcomeAmbiguous, ExitAmbiguous, "reconcile_same_operation"},
		{&integration.Error{Kind: integration.KindGitFailure, Message: "hidden"}, OutcomeUnavailable, ExitUnavailable, "reconcile_same_operation"},
		{&workgraph.Error{Kind: workgraph.KindAmbiguousID, Message: "hidden"}, OutcomeAmbiguous, ExitAmbiguous, "same_operation_id"},
		{&productionruntime.ProgressError{Kind: productionruntime.ProgressNotFound, Field: "mission_id"}, OutcomeNotFound, ExitNotFound, "read_only"},
		{context.Canceled, OutcomeCanceled, ExitCanceled, "same_request"},
		{errors.New("provider prose must not escape"), OutcomeUnavailable, ExitUnavailable, "after_diagnosis"},
	}
	for _, test := range tests {
		outcome, diagnostic := classifyError(test.err)
		if outcome != test.outcome || exitFor(test.err) != test.code || diagnostic.RetrySafety != test.retry || strings.Contains(diagnostic.Kind, "provider prose") {
			t.Fatalf("err=%v outcome=%s diagnostic=%+v", test.err, outcome, diagnostic)
		}
	}
}

func TestMutationSensitiveFailuresNeverRecommendANewOperation(t *testing.T) {
	for _, err := range []error{
		&integration.Error{Kind: integration.KindRepairRequired, Message: "after external mutation"},
		&integration.Error{Kind: integration.KindGitFailure, Message: "cleanup may be pending"},
		&workspace.Error{Kind: workspace.KindCleanupRefused, Message: "cleanup may be pending"},
		execution.NewError(execution.KindExternalAttributionLost, "provider state changed", execution.RetryDecisionRequired, nil),
	} {
		_, diagnostic := classifyError(err)
		if strings.Contains(diagnostic.RetrySafety, "new") || diagnostic.RetrySafety == "never" || diagnostic.RetrySafety == "after_diagnosis" {
			t.Fatalf("mutation-sensitive error permits unsafe retry: err=%v diagnostic=%+v", err, diagnostic)
		}
	}
}

func TestOwnedProductionSurfaceHasOneHostAndNoLegacyStorageVocabulary(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, directory := range []string{"internal/helpercmd", "cmd/spec-kitty", "cmd/spk-phase1-helper"} {
		entries, readErr := os.ReadDir(filepath.Join(repository, directory))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Join(repository, directory, entry.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			files[filepath.Join(directory, entry.Name())] = string(raw)
		}
	}
	if violations := productionSurfaceViolations(files); len(violations) != 0 {
		t.Fatalf("production command boundary violations: %v", violations)
	}
	if strings.Count(files["internal/helpercmd/production.go"], "productionruntime.Start(") != 1 {
		t.Fatal("production command surface must have exactly one host composition call")
	}
}

func TestOwnedProductionSurfaceGuardKillsBoundaryMutants(t *testing.T) {
	mutants := []string{
		"import \"github.com/spec-kitty/go-kitty3/internal/controlstore/" + "sqlite\"",
		"flags.String(\"" + "db\", \"\", \"database\")",
		"flags.String(\"" + "backend\", \"\", \"provider\")",
		"sql.Open(\"driver\", path)",
		"productionruntime.Start(ctx, config); productionruntime.Start(ctx, config)",
	}
	for _, mutant := range mutants {
		if got := productionSurfaceViolations(map[string]string{"mutant.go": mutant}); len(got) == 0 {
			t.Fatalf("boundary mutant survived: %s", mutant)
		}
	}
}

func productionSurfaceViolations(files map[string]string) []string {
	forbidden := []string{
		"controlstore/" + "sqlite",
		"modernc.org/" + "sqlite",
		"database/" + "sql",
		"\"" + "db\"",
		"\"" + "backend\"",
		"\"--" + "db\"",
		"\"--" + "backend\"",
		"\"--" + "eligibility",
		"\"--" + "fallback",
		"\"--" + "waiver",
		"\"--" + "import",
		"sql." + "Open(",
	}
	var violations []string
	for name, source := range files {
		for _, fragment := range forbidden {
			if strings.Contains(source, fragment) {
				violations = append(violations, name+":"+fragment)
			}
		}
		if name != "internal/helpercmd/production.go" && strings.Contains(source, "productionruntime.Start(") {
			violations = append(violations, name+":second_composition_root")
		}
	}
	return violations
}

func TestTrackerEarlyDispatchNeverTouchesHostFactory(t *testing.T) {
	factory := &factorySpy{}
	runner, _ := NewRunner(factory)
	for _, mode := range []string{"json", "human"} {
		for action := range providerActions {
			for _, tail := range [][]string{{"--help"}, {"--binding", "/nonexistent/binding"}, {"--binding", "--help"}} {
				var out, diagnostic bytes.Buffer
				args := append([]string{"--output", mode, "tracker", action}, tail...)
				_ = runner.Run(t.Context(), args, strings.NewReader(""), &out, &diagnostic)
				if factory.classified != 0 || factory.observed != 0 || factory.started != 0 {
					t.Fatalf("tracker reached host: %v", args)
				}
				if tail[len(tail)-1] == "--help" && len(tail) == 2 && strings.Contains(out.String(), "Usage:") {
					t.Fatal("flag value was interpreted as help")
				}
			}
		}
	}
}
