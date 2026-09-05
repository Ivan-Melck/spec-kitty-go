package helpercmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

type factorySpy struct {
	mu          sync.Mutex
	decision    controlroot.Decision
	classifyErr error
	startErr    error
	session     *sessionSpy
	classified  int
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

type sessionSpy struct {
	mu        sync.Mutex
	status    productionruntime.HostStatus
	progress  productionruntime.MissionProgress
	queryErr  error
	missionFn func(journalv2.ProjectID, journalv2.MissionID) (productionruntime.MissionProgress, error)
	executeFn func(decodedOperation) (json.RawMessage, error)
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
func (s *sessionSpy) Operation(_ journalv2.ProjectID, _ journalv2.MissionID, operation string) (productionruntime.ProgressItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	if s.queryErr != nil {
		return productionruntime.ProgressItem{}, s.queryErr
	}
	return productionruntime.ProgressItem{Family: "operation", AggregateID: operation, LogicalVersion: 1, AuthorityState: "resolved", CanonicalState: []byte(`{"ok":true}`)}, nil
}
func (s *sessionSpy) Execute(_ context.Context, operation decodedOperation) (json.RawMessage, error) {
	s.mu.Lock()
	s.executes++
	fn := s.executeFn
	s.mu.Unlock()
	if fn != nil {
		return fn(operation)
	}
	return json.RawMessage(`{"schema_version":1,"ok":true}`), nil
}
func (s *sessionSpy) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return nil
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
	if factory.started != 3 || session.closes != 3 || session.executes != 0 {
		t.Fatalf("start=%d close=%d execute=%d", factory.started, session.closes, session.executes)
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
	session.executeFn = func(operation decodedOperation) (json.RawMessage, error) {
		if firstSHA == "" {
			firstSHA = operation.requestSHA
		} else if operation.requestSHA != firstSHA {
			return nil, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}
		}
		return json.RawMessage(`{"operation_id":"operation-resume-1","replayed":true}`), nil
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
	session.executeFn = func(operation decodedOperation) (json.RawMessage, error) {
		if admittedSHA == "" {
			admittedSHA = operation.requestSHA
			return json.RawMessage(`{"ok":true}`), nil
		}
		if operation.requestSHA != admittedSHA {
			return nil, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}
		}
		return json.RawMessage(`{"ok":true}`), nil
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
	session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionPause), Status: "unavailable", BlockingPredicates: []string{"provider"}}}
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
	session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionCreate), Status: "authorized"}}
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

func TestDriveExecutesIndependentMissionActionsConcurrently(t *testing.T) {
	session := defaultSession()
	session.missionFn = func(_ journalv2.ProjectID, _ journalv2.MissionID) (productionruntime.MissionProgress, error) {
		return productionruntime.MissionProgress{}, &productionruntime.ProgressError{Kind: productionruntime.ProgressNotFound, Field: "mission_id"}
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	session.executeFn = func(decodedOperation) (json.RawMessage, error) {
		entered <- struct{}{}
		<-release
		return json.RawMessage(`{"ok":true}`), nil
	}
	left := missionCreateRequestFor(t, "mission-left", "operation-left")
	right := missionCreateRequestFor(t, "mission-right", "operation-right")
	plan, _ := json.Marshal(DrivePlan{SchemaVersion: 1, Operations: []OperationRequest{left, right}})
	factory := admittedFactory(session)
	runner, _ := NewRunner(factory)
	done := make(chan int, 1)
	root := filepath.Join(t.TempDir(), "authority")
	go func() {
		done <- runner.Run(t.Context(), []string{"--control-root", root, "drive", "--plan", "-"}, bytes.NewReader(plan), &bytes.Buffer{}, &bytes.Buffer{})
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("independent actions were serialized")
		}
	}
	close(release)
	if code := <-done; code != ExitRefused { // both creates succeeded; missing terminal progress is an honest stop.
		t.Fatalf("exit=%d", code)
	}
	if session.executes != 2 {
		t.Fatalf("executes=%d", session.executes)
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
	disposition := controlroot.Disposition{SchemaVersion: 1, ControlRootSHA256: strings.Repeat("a", 64), Classification: controlroot.EmptySupported, Findings: []controlroot.Finding{}, Action: controlroot.Initialize, DiagnosisCode: "EMPTY_SUPPORTED"}
	return &factorySpy{decision: controlroot.Decision{Action: controlroot.Initialize, Disposition: disposition}, session: session}
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
	return OperationRequest{SchemaVersion: 1, Action: string(mission.ActionMissionCreate), Kind: OperationMission, Payload: payload}
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
	}{
		{&mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityIdempotencyConflictV2, Field: "operation_id"}, OutcomeConflict, ExitConflict},
		{&mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityAmbiguousV2, Field: "operation"}, OutcomeAmbiguous, ExitAmbiguous},
		{&productionruntime.ProgressError{Kind: productionruntime.ProgressNotFound, Field: "mission_id"}, OutcomeNotFound, ExitNotFound},
		{context.Canceled, OutcomeCanceled, ExitCanceled},
		{errors.New("provider prose must not escape"), OutcomeUnavailable, ExitUnavailable},
	}
	for _, test := range tests {
		outcome, diagnostic := classifyError(test.err)
		if outcome != test.outcome || exitFor(test.err) != test.code || strings.Contains(diagnostic.Kind, "provider prose") {
			t.Fatalf("err=%v outcome=%s diagnostic=%+v", test.err, outcome, diagnostic)
		}
	}
}
