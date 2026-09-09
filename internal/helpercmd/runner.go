package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type observationSession interface {
	Status() productionruntime.HostStatus
	Mission(journalv2.ProjectID, journalv2.MissionID) (productionruntime.MissionProgress, error)
	Operation(journalv2.ProjectID, journalv2.MissionID, string) (productionruntime.ProgressItem, error)
	Close(context.Context) error
}

type hostSession interface {
	observationSession
	Execute(context.Context, decodedOperation) (TypedResult, error)
}

type HostFactory interface {
	Classify(context.Context, string) (controlroot.Decision, error)
	Observe(context.Context, string) (observationSession, error)
	Start(context.Context, string) (hostSession, error)
}

type Runner struct{ factory HostFactory }

func NewRunner(factory HostFactory) (*Runner, error) {
	if factory == nil {
		return nil, errors.New("host factory is required")
	}
	return &Runner{factory: factory}, nil
}

// Run preserves the old embeddable entry point while routing through the one
// native runner. Commands that consume stdin should use RunIO.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunIO(ctx, args, strings.NewReader(""), stdout, stderr)
}

func RunIO(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	runner, err := NewRunner(NewProductionFactory(InstallationFromEnvironment()))
	if err != nil {
		return render(stdout, stderr, "", "json", invalidEnvelope("", "arguments"))
	}
	return runner.Run(ctx, args, stdin, stdout, stderr)
}

type globalOptions struct {
	controlRoot string
	output      string
	command     string
	args        []string
}

type preparedCommand struct {
	projectID   journalv2.ProjectID
	missionID   journalv2.MissionID
	operationID string
	subcommand  string
	operation   *decodedOperation
	drive       []decodedOperation
}

func (r *Runner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, help, err := parseGlobal(args)
	if help {
		_, _ = io.WriteString(stdout, usage)
		return ExitSuccess
	}
	if err != nil {
		return render(stdout, stderr, "", "json", invalidEnvelope("", "arguments"))
	}
	if options.command == "help" {
		_, _ = io.WriteString(stdout, usage)
		return ExitSuccess
	}
	if options.command == "tracker" {
		return runTracker(ctx, options, stdin, stdout, stderr)
	}
	if !knownCommand(options.command) {
		return render(stdout, stderr, options.command, options.output, invalidEnvelope(options.command, "command"))
	}
	for _, argument := range options.args {
		if argument == "-h" || argument == "--help" {
			_, _ = io.WriteString(stdout, usage)
			return ExitSuccess
		}
	}
	if options.controlRoot == "" || !filepath.IsAbs(options.controlRoot) || filepath.Clean(options.controlRoot) != options.controlRoot {
		return render(stdout, stderr, options.command, options.output, invalidEnvelope(options.command, "control_root"))
	}
	prepared, prepareErr := prepare(options, stdin)
	if prepareErr != nil {
		return render(stdout, stderr, options.command, options.output, invalidEnvelope(options.command, prepareErr.Error()))
	}
	decision, classifyErr := r.factory.Classify(ctx, options.controlRoot)
	if classifyErr != nil {
		return render(stdout, stderr, options.command, options.output, failureEnvelope(options.command, classifyErr))
	}
	if decision.Action != controlroot.Initialize && decision.Action != controlroot.Recover {
		return render(stdout, stderr, options.command, options.output, failureEnvelope(options.command, &controlroot.OfflineDispositionError{Code: controlroot.OfflineDispositionRequiredCode, Disposition: decision.Disposition}))
	}
	queryOnly := options.command == "status" || options.command == "progress" || options.command == "operation" && prepared.subcommand == "inspect"
	if queryOnly && decision.Action == controlroot.Initialize {
		return render(stdout, stderr, options.command, options.output, failureEnvelope(options.command, &productionruntime.Error{Kind: productionruntime.ErrorNotReady, State: productionruntime.HostRecovering, Field: "control_root_uninitialized"}))
	}
	var session observationSession
	var startErr error
	if queryOnly {
		session, startErr = r.factory.Observe(ctx, options.controlRoot)
	} else {
		session, startErr = r.factory.Start(ctx, options.controlRoot)
	}
	if startErr != nil {
		return render(stdout, stderr, options.command, options.output, failureEnvelope(options.command, startErr))
	}
	envelope, code := r.invoke(ctx, session, options, prepared)
	closeErr := session.Close(context.WithoutCancel(ctx))
	if closeErr != nil && code == ExitSuccess {
		envelope, code = failureEnvelope(options.command, closeErr), ExitUnavailable
	}
	return render(stdout, stderr, options.command, options.output, envelope, code)
}

func knownCommand(command string) bool {
	switch command {
	case "initialize", "status", "progress", "operation", "drive":
		return true
	default:
		return false
	}
}

func (r *Runner) invoke(ctx context.Context, session observationSession, options globalOptions, prepared preparedCommand) (Envelope, int) {
	status := session.Status()
	base := Envelope{SchemaVersion: EnvelopeSchemaVersion, Command: options.command, Outcome: OutcomeSuccess, DatabaseID: status.DatabaseID, RuntimeEpochID: status.RuntimeEpochID, ProjectionRevision: status.ProjectionRevision}
	switch options.command {
	case "initialize":
		if len(options.args) != 0 {
			return invalidEnvelope(options.command, "arguments"), ExitInvalid
		}
		base.Host = &status
		return base, ExitSuccess
	case "status":
		if len(options.args) != 0 {
			return invalidEnvelope(options.command, "arguments"), ExitInvalid
		}
		base.Host = &status
		return base, ExitSuccess
	case "progress":
		progress, queryErr := session.Mission(prepared.projectID, prepared.missionID)
		if queryErr != nil {
			return failureEnvelope(options.command, queryErr), exitFor(queryErr)
		}
		base.Progress = &progress
		base.ProjectionRevision = progress.ProjectionRevision
		return base, ExitSuccess
	case "operation":
		if prepared.subcommand == "inspect" {
			return invokeOperationInspect(session, base, prepared)
		}
		return invokeOperation(ctx, session.(hostSession), base, prepared)
	case "drive":
		return invokeDrive(ctx, session.(hostSession), base, prepared)
	default:
		return invalidEnvelope(options.command, "command"), ExitInvalid
	}
}

func invokeOperationInspect(session observationSession, base Envelope, prepared preparedCommand) (Envelope, int) {
	item, err := session.Operation(prepared.projectID, prepared.missionID, prepared.operationID)
	if err != nil {
		return failureEnvelope("operation.inspect", err), exitFor(err)
	}
	base.Command, base.Item = "operation.inspect", &item
	return base, ExitSuccess
}

func invokeOperation(ctx context.Context, session hostSession, base Envelope, prepared preparedCommand) (Envelope, int) {
	switch prepared.subcommand {
	case "execute", "resume":
		decoded := *prepared.operation
		result, executeErr := session.Execute(ctx, decoded)
		base.Command, base.RequestSHA256 = "operation."+prepared.subcommand, decoded.requestSHA
		if executeErr != nil {
			failed := failureEnvelope(base.Command, executeErr)
			failed.RequestSHA256, failed.DatabaseID, failed.RuntimeEpochID = decoded.requestSHA, base.DatabaseID, base.RuntimeEpochID
			return failed, exitFor(executeErr)
		}
		if result.Kind != resultKindFor(decoded.request.Kind) || !json.Valid(result.Payload) {
			return failureEnvelope(base.Command, errors.New("capability returned an invalid typed result")), ExitUnavailable
		}
		base.Result = &result
		return base, ExitSuccess
	default:
		return invalidEnvelope("operation", "subcommand"), ExitInvalid
	}
}

func resultKindFor(kind OperationKind) ResultKind {
	switch kind {
	case OperationMission:
		return ResultMission
	case OperationReadiness:
		return ResultReadiness
	case OperationExecution:
		return ResultExecution
	case OperationWorkspaceRebind:
		return ResultWorkspace
	case OperationHandoff:
		return ResultHandoff
	case OperationReview:
		return ResultReview
	case OperationAcceptance:
		return ResultAcceptance
	case OperationIntegration:
		return ResultIntegration
	case OperationIntegrationCleanup:
		return ResultIntegrationCleanup
	case OperationEffect:
		return ResultEffect
	default:
		return ""
	}
}

func invokeDrive(ctx context.Context, session hostSession, base Envelope, prepared preparedCommand) (Envelope, int) {
	result := DriveResult{SchemaVersion: 1, Steps: make([]DriveStep, 0, len(prepared.drive))}
	pending := append([]decodedOperation(nil), prepared.drive...)
	touched := make(map[string]decodedOperation)
	for len(pending) != 0 {
		ready := make([]int, 0, len(pending))
		seenScope := make(map[string]bool)
		stopReason, stopCode := "action_not_authorized", ExitRefused
		for index, decoded := range pending {
			key := string(decoded.projectID) + "\x00" + string(decoded.missionID) + "\x00" + decoded.scope
			if seenScope[key] {
				continue
			}
			progress, progressErr := session.Mission(decoded.projectID, decoded.missionID)
			if progressErr != nil {
				if decoded.request.Action == string(mission.ActionMissionCreate) && progressNotFound(progressErr) {
					ready = append(ready, index)
					seenScope[key] = true
					continue
				}
				stopReason, stopCode = "mission_progress_unavailable", exitFor(progressErr)
				continue
			}
			if actionAuthorized(progress.Actions, decoded.request.Action, decoded.scope) {
				ready = append(ready, index)
				seenScope[key] = true
			}
		}
		if len(ready) == 0 {
			base.Outcome, result.StopReason, base.Drive = OutcomeStopped, stopReason, &result
			return base, stopCode
		}
		type waveResult struct {
			step DriveStep
			err  error
		}
		wave := make([]waveResult, len(ready))
		var wait sync.WaitGroup
		for outputIndex, pendingIndex := range ready {
			outputIndex, decoded := outputIndex, pending[pendingIndex]
			wait.Add(1)
			go func() {
				defer wait.Done()
				typedResult, executeErr := session.Execute(ctx, decoded)
				step := DriveStep{Action: decoded.request.Action, Scope: decoded.scope, OperationID: string(decoded.operation), RequestSHA256: decoded.requestSHA, Outcome: OutcomeSuccess}
				if executeErr != nil {
					step.Outcome = failureEnvelope("drive", executeErr).Outcome
				} else if typedResult.Kind != resultKindFor(decoded.request.Kind) || !json.Valid(typedResult.Payload) {
					executeErr = errors.New("capability returned an invalid typed result")
					step.Outcome = OutcomeUnavailable
				} else {
					step.Result = &typedResult
				}
				wave[outputIndex] = waveResult{step: step, err: executeErr}
			}()
		}
		wait.Wait()
		for outputIndex, pendingIndex := range ready {
			result.Steps = append(result.Steps, wave[outputIndex].step)
			decoded := pending[pendingIndex]
			touched[string(decoded.projectID)+"\x00"+string(decoded.missionID)] = decoded
			if wave[outputIndex].err != nil {
				base.Outcome, result.StopReason, base.Drive = OutcomeStopped, "operation_failed", &result
				return base, exitFor(wave[outputIndex].err)
			}
		}
		remove := make(map[int]bool, len(ready))
		for _, index := range ready {
			remove[index] = true
		}
		next := make([]decodedOperation, 0, len(pending)-len(ready))
		for index, decoded := range pending {
			if !remove[index] {
				next = append(next, decoded)
			}
		}
		pending = next
	}
	result.Terminal = len(touched) != 0
	for _, decoded := range touched {
		progress, err := session.Mission(decoded.projectID, decoded.missionID)
		if err != nil || !terminalLifecycle(progress.Lifecycle) {
			result.Terminal = false
			break
		}
	}
	if !result.Terminal {
		base.Outcome, result.StopReason, base.Drive = OutcomeStopped, "plan_exhausted", &result
		return base, ExitRefused
	}
	base.Drive = &result
	return base, ExitSuccess
}

func progressNotFound(err error) bool {
	var progressErr *productionruntime.ProgressError
	return errors.As(err, &progressErr) && progressErr.Kind == productionruntime.ProgressNotFound
}

func terminalLifecycle(lifecycle string) bool {
	switch lifecycle {
	case string(mission.MissionCompleted), string(mission.MissionFailed), string(mission.MissionCanceled):
		return true
	default:
		return false
	}
}

func prepare(options globalOptions, stdin io.Reader) (preparedCommand, error) {
	var prepared preparedCommand
	switch options.command {
	case "initialize":
		if len(options.args) != 0 {
			return prepared, errors.New("arguments")
		}
	case "status":
		if len(options.args) != 0 {
			return prepared, errors.New("arguments")
		}
	case "progress":
		project, missionID, _, err := parseMissionIdentity("progress", options.args)
		if err != nil {
			return prepared, errors.New("identity")
		}
		prepared.projectID, prepared.missionID = project, missionID
	case "operation":
		if len(options.args) == 0 {
			return prepared, errors.New("subcommand")
		}
		prepared.subcommand = options.args[0]
		switch prepared.subcommand {
		case "inspect":
			flags := flag.NewFlagSet("operation inspect", flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			var project, missionID string
			flags.StringVar(&project, "project", "", "project ID")
			flags.StringVar(&missionID, "mission", "", "mission ID")
			flags.StringVar(&prepared.operationID, "operation", "", "operation ID")
			if err := flags.Parse(options.args[1:]); err != nil || flags.NArg() != 0 || project == "" || missionID == "" || prepared.operationID == "" {
				return prepared, errors.New("arguments")
			}
			projectID, projectErr := journalv2.NewProjectID(project)
			mission, missionErr := journalv2.NewMissionID(missionID)
			if _, operationErr := journalv2.NewOperationID(prepared.operationID); projectErr != nil || missionErr != nil || operationErr != nil {
				return prepared, errors.New("identity")
			}
			prepared.projectID, prepared.missionID = projectID, mission
		case "execute", "resume":
			request, err := readOperationArg(prepared.subcommand, options.args[1:], stdin)
			if err != nil {
				return prepared, errors.New("request")
			}
			decoded, err := decodeOperation(request)
			if err != nil {
				return prepared, errors.New("request")
			}
			prepared.operation = &decoded
		default:
			return prepared, errors.New("subcommand")
		}
	case "drive":
		flags := flag.NewFlagSet("drive", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var path string
		flags.StringVar(&path, "plan", "", "bounded typed drive plan, or - for stdin")
		if err := flags.Parse(options.args); err != nil || flags.NArg() != 0 || path == "" {
			return prepared, errors.New("plan")
		}
		reader, closeReader, err := requestReader(path, stdin)
		if err != nil {
			return prepared, errors.New("plan")
		}
		defer closeReader()
		var plan DrivePlan
		if err := readBoundedJSON(reader, &plan); err != nil || plan.SchemaVersion != RequestSchemaVersion || len(plan.Operations) == 0 || len(plan.Operations) > MaxDriveOperations {
			return prepared, errors.New("plan")
		}
		prepared.drive = make([]decodedOperation, len(plan.Operations))
		for index, request := range plan.Operations {
			decoded, err := decodeOperation(request)
			if err != nil {
				return preparedCommand{}, errors.New("operation")
			}
			prepared.drive[index] = decoded
		}
	}
	return prepared, nil
}

func actionAuthorized(actions []productionruntime.ActionAvailability, action, scope string) bool {
	for _, candidate := range actions {
		if candidate.Action == action && candidate.Scope == scope && candidate.Status == "authorized" {
			return true
		}
	}
	return false
}

func readOperationArg(name string, args []string, stdin io.Reader) (OperationRequest, error) {
	flags := flag.NewFlagSet("operation "+name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var path string
	flags.StringVar(&path, "request", "", "typed operation request, or - for stdin")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || path == "" {
		return OperationRequest{}, errors.New("request path is required")
	}
	reader, closeReader, err := requestReader(path, stdin)
	if err != nil {
		return OperationRequest{}, err
	}
	defer closeReader()
	var request OperationRequest
	return request, readBoundedJSON(reader, &request)
}

func requestReader(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return file, func() { _ = file.Close() }, nil
}

func parseGlobal(args []string) (globalOptions, bool, error) {
	flags := flag.NewFlagSet("spec-kitty", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options globalOptions
	flags.StringVar(&options.controlRoot, "control-root", "", "absolute Spec Kitty control root")
	flags.StringVar(&options.output, "output", "json", "json or human")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return options, true, nil
	} else if err != nil {
		return options, false, err
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		return options, false, errors.New("command is required")
	}
	if options.output != "json" && options.output != "human" {
		return options, false, errors.New("output mode is invalid")
	}
	options.command, options.args = remaining[0], remaining[1:]
	return options, false, nil
}

func parseMissionIdentity(name string, args []string) (journalv2.ProjectID, journalv2.MissionID, bool, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var project, missionID string
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&missionID, "mission", "", "mission ID")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return "", "", true, nil
	}
	if err != nil || flags.NArg() != 0 || project == "" || missionID == "" {
		return "", "", false, errors.New("project and mission are required")
	}
	projectID, projectErr := journalv2.NewProjectID(project)
	mission, missionErr := journalv2.NewMissionID(missionID)
	if projectErr != nil || missionErr != nil {
		return "", "", false, errors.New("project or mission identity is invalid")
	}
	return projectID, mission, false, nil
}

func render(stdout, stderr io.Writer, command, mode string, values ...any) int {
	envelope := Envelope{SchemaVersion: EnvelopeSchemaVersion, Command: command, Outcome: OutcomeInvalid}
	code := ExitInvalid
	for _, value := range values {
		switch typed := value.(type) {
		case Envelope:
			envelope = typed
			code = exitForOutcome(typed.Outcome)
		case int:
			// Exit status is derived exclusively from the closed outcome. Callers
			// may retain a local diagnostic code, but it cannot contradict JSON.
			_ = typed
		}
	}
	if mode == "human" {
		_, _ = fmt.Fprintf(stdout, "%s: %s\n", envelope.Command, envelope.Outcome)
		if envelope.Diagnostic != nil {
			_, _ = fmt.Fprintf(stderr, "%s", envelope.Diagnostic.Kind)
			if envelope.Diagnostic.Field != "" {
				_, _ = fmt.Fprintf(stderr, ": %s", envelope.Diagnostic.Field)
			}
			_, _ = fmt.Fprintln(stderr)
		}
		return code
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(envelope)
	return code
}

func invalidEnvelope(command, field string) Envelope {
	return Envelope{SchemaVersion: EnvelopeSchemaVersion, Command: command, Outcome: OutcomeInvalid, Diagnostic: &Diagnostic{Kind: "INVALID_COMMAND", Field: field, RetrySafety: "never"}}
}

func failureEnvelope(command string, err error) Envelope {
	outcome, diagnostic := classifyError(err)
	return Envelope{SchemaVersion: EnvelopeSchemaVersion, Command: command, Outcome: outcome, Diagnostic: &diagnostic}
}

func classifyError(err error) (Outcome, Diagnostic) {
	if err == nil {
		return OutcomeSuccess, Diagnostic{}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return OutcomeCanceled, Diagnostic{Kind: "CANCELED", RetrySafety: "same_request"}
	}
	var offline *controlroot.OfflineDispositionError
	if errors.As(err, &offline) {
		return OutcomeRefused, Diagnostic{Kind: controlroot.OfflineDispositionRequiredCode, Field: offline.Disposition.DiagnosisCode, RetrySafety: "after_offline_disposition", RecoveryActions: []string{"inspect_disposition", "perform_offline_migration_or_archive"}}
	}
	var runtimeErr *productionruntime.Error
	if errors.As(err, &runtimeErr) {
		outcome := OutcomeUnavailable
		if runtimeErr.Kind == productionruntime.ErrorInvalidConfiguration || runtimeErr.Kind == productionruntime.ErrorStartup {
			outcome = OutcomeRefused
		}
		return outcome, Diagnostic{Kind: string(runtimeErr.Kind), Field: runtimeErr.Field, RetrySafety: "after_diagnosis"}
	}
	var progressErr *productionruntime.ProgressError
	if errors.As(err, &progressErr) {
		outcome := OutcomeUnavailable
		if progressErr.Kind == productionruntime.ProgressNotFound {
			outcome = OutcomeNotFound
		} else if progressErr.Kind == productionruntime.ProgressInvalidQuery {
			outcome = OutcomeInvalid
		}
		return outcome, Diagnostic{Kind: string(progressErr.Kind), Field: progressErr.Field, RetrySafety: "read_only"}
	}
	var missionErr *mission.MissionAuthorityErrorV2
	if errors.As(err, &missionErr) {
		outcome := OutcomeRefused
		switch missionErr.Kind {
		case mission.MissionAuthorityInvalidRequestV2, mission.MissionAuthorityInvalidTransitionV2:
			outcome = OutcomeInvalid
		case mission.MissionAuthorityNotFoundV2:
			outcome = OutcomeNotFound
		case mission.MissionAuthorityIdempotencyConflictV2, mission.MissionAuthorityVersionConflictV2:
			outcome = OutcomeConflict
		case mission.MissionAuthorityAmbiguousV2:
			outcome = OutcomeAmbiguous
		case mission.MissionAuthorityStaleEpochV2, mission.MissionAuthorityCorruptV2:
			outcome = OutcomeUnavailable
		}
		return outcome, Diagnostic{Kind: string(missionErr.Kind), Field: missionErr.Field, RetrySafety: "same_operation_id"}
	}
	var executionErr *execution.DiagnosticError
	if errors.As(err, &executionErr) {
		outcome := outcomeForExecution(executionErr.Kind)
		recovery := append([]string(nil), executionErr.RecoveryActions...)
		return outcome, Diagnostic{Kind: string(executionErr.Kind), RetrySafety: string(executionErr.RetrySafety), RecoveryActions: recovery}
	}
	var workspaceErr *workspace.Error
	if errors.As(err, &workspaceErr) {
		return classifyWorkspaceError(workspaceErr)
	}
	var handoffErr *handoff.Error
	if errors.As(err, &handoffErr) {
		outcome := OutcomeRefused
		switch handoffErr.Kind {
		case handoff.KindInvalid:
			outcome = OutcomeInvalid
		case handoff.KindNotFound:
			outcome = OutcomeNotFound
		case handoff.KindIdempotencyConflict, handoff.KindConflict, handoff.KindCandidateMismatch:
			outcome = OutcomeConflict
		case handoff.KindStaleAuthority:
			outcome = OutcomeUnavailable
		}
		return outcome, Diagnostic{Kind: string(handoffErr.Kind), RetrySafety: "same_operation_id", RecoveryActions: append([]string(nil), handoffErr.RecoveryActions...)}
	}
	var reviewErr *review.Error
	if errors.As(err, &reviewErr) {
		outcome := OutcomeRefused
		switch reviewErr.Kind {
		case review.KindInvalid:
			outcome = OutcomeInvalid
		case review.KindNotFound:
			outcome = OutcomeNotFound
		case review.KindIdempotencyConflict, review.KindConflict, review.KindCandidateMismatch:
			outcome = OutcomeConflict
		case review.KindStaleAuthority:
			outcome = OutcomeUnavailable
		}
		return outcome, Diagnostic{Kind: string(reviewErr.Kind), RetrySafety: "same_operation_id"}
	}
	var integrationErr *integration.Error
	if errors.As(err, &integrationErr) {
		outcome := OutcomeRefused
		switch integrationErr.Kind {
		case integration.KindInvalid:
			outcome = OutcomeInvalid
		case integration.KindIdempotencyConflict, integration.KindConflict, integration.KindTargetMoved, integration.KindAuthorityBusy:
			outcome = OutcomeConflict
		case integration.KindRepairRequired:
			outcome = OutcomeAmbiguous
		case integration.KindStoreFailure, integration.KindGitFailure, integration.KindStaleAuthority, integration.KindEvidenceUnavailable:
			outcome = OutcomeUnavailable
		}
		// Integration and cleanup can leave durable intent or pending-cleanup
		// facts. Never advise a fresh request after a typed failure.
		return outcome, Diagnostic{Kind: string(integrationErr.Kind), RetrySafety: "reconcile_same_operation", RecoveryActions: []string{"inspect_operation", "resume_same_operation_id"}}
	}
	var graphErr *workgraph.Error
	if errors.As(err, &graphErr) {
		outcome := OutcomeInvalid
		switch graphErr.Kind {
		case workgraph.KindNotFound, workgraph.KindMissingNode:
			outcome = OutcomeNotFound
		case workgraph.KindAmbiguousID:
			outcome = OutcomeAmbiguous
		case workgraph.KindRevisionConflict, workgraph.KindReconciliationConflict, workgraph.KindProviderChanged:
			outcome = OutcomeConflict
		case workgraph.KindFinalized:
			outcome = OutcomeRefused
		}
		return outcome, Diagnostic{Kind: string(graphErr.Kind), RetrySafety: "same_operation_id"}
	}
	return OutcomeUnavailable, Diagnostic{Kind: "COMMAND_UNAVAILABLE", RetrySafety: "after_diagnosis"}
}

func outcomeForExecution(kind execution.ErrorKind) Outcome {
	switch kind {
	case execution.KindInvalidCommand:
		return OutcomeInvalid
	case execution.KindNotFound:
		return OutcomeNotFound
	case execution.KindIdempotencyConflict, execution.KindAuthorityConflict, execution.KindRepositoryConflict, execution.KindTargetChanged, execution.KindDependencyConflict:
		return OutcomeConflict
	case execution.KindExternalAttributionLost, execution.KindDecisionRequired, execution.KindSafeCompensationUnsupported:
		return OutcomeAmbiguous
	case execution.KindControlStoreBusy, execution.KindStaleAuthority, execution.KindGitFailure, execution.KindTrackerUnavailable:
		return OutcomeUnavailable
	default:
		return OutcomeRefused
	}
}

func classifyWorkspaceError(err *workspace.Error) (Outcome, Diagnostic) {
	outcome := OutcomeRefused
	switch err.Kind {
	case workspace.KindInvalid, workspace.KindPathUnsafe:
		outcome = OutcomeInvalid
	case workspace.KindBindingConflict, workspace.KindWorkspaceConflict, workspace.KindTargetMoved, workspace.KindDependencyConflict:
		outcome = OutcomeConflict
	case workspace.KindStaleAuthority, workspace.KindGitFailure:
		outcome = OutcomeUnavailable
	case workspace.KindCleanupRefused:
		outcome = OutcomeAmbiguous
	}
	retry := "same_operation_id"
	if err.Kind == workspace.KindCleanupRefused {
		retry = "reconcile_same_operation"
	}
	return outcome, Diagnostic{Kind: string(err.Kind), RetrySafety: retry}
}

func exitFor(err error) int {
	outcome, _ := classifyError(err)
	return exitForOutcome(outcome)
}

func exitForOutcome(outcome Outcome) int {
	switch outcome {
	case OutcomeSuccess:
		return ExitSuccess
	case OutcomeInvalid:
		return ExitInvalid
	case OutcomeRefused, OutcomeStopped:
		return ExitRefused
	case OutcomeAmbiguous:
		return ExitAmbiguous
	case OutcomeConflict:
		return ExitConflict
	case OutcomeNotFound:
		return ExitNotFound
	case OutcomeCanceled:
		return ExitCanceled
	default:
		return ExitUnavailable
	}
}

const usage = `spec-kitty — native local mission authority

Usage:
  spec-kitty [--output json|human] tracker ACTION --binding FILE [--input FILE|-]
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] initialize
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] status
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] progress --project ID --mission ID
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation inspect --project ID --mission ID --operation ID
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation execute --request FILE
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] operation resume --request FILE
  spec-kitty --control-root ABSOLUTE_PATH [--output json|human] drive --plan FILE

The request and plan value may be '-' to read strict JSON from stdin.
Physical storage, backend selection, waivers, importers, and generic state
transitions are intentionally not command-line options.
`
