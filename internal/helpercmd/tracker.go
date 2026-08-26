package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/beads"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

const trackerUsage = "usage: tracker assign|compensate|inspect-beads [options]"

type TrackerResponse struct {
	OK      bool                           `json:"ok"`
	Result  *execution.TrackerEffectResult `json:"result,omitempty"`
	Error   *execution.DiagnosticError     `json:"error,omitempty"`
	Message string                         `json:"message,omitempty"`
}

type BeadsInspectionResult struct {
	SchemaVersion uint16                       `json:"schema_version"`
	Binding       tracker.TrackerBinding       `json:"binding"`
	Capabilities  tracker.ProviderCapabilities `json:"capabilities"`
	Health        tracker.HealthStatus         `json:"health"`
	Diagnostics   []tracker.Diagnostic         `json:"diagnostics"`
}

type BeadsInspectionError struct {
	Kind         tracker.ErrorKind `json:"kind"`
	RecoveryCode string            `json:"recovery_code"`
	Message      string            `json:"message"`
}

type BeadsInspectionResponse struct {
	SchemaVersion uint16                 `json:"schema_version"`
	OK            bool                   `json:"ok"`
	Inspection    *BeadsInspectionResult `json:"inspection,omitempty"`
	Error         *BeadsInspectionError  `json:"error,omitempty"`
}

func RunTracker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, trackerUsage)
		return 2
	}
	action := args[0]
	if action == "inspect-beads" {
		return runInspectBeads(ctx, args[1:], stdout, stderr)
	}
	if action != "assign" && action != "compensate" {
		fmt.Fprintln(stderr, trackerUsage)
		return 2
	}
	flags := flag.NewFlagSet("tracker "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var database, trackerPath, barrier, operation, correlation, actor, process, provider, workKey, assignee string
	var expectedRevision uint64
	flags.StringVar(&database, "db", "", "control database path")
	flags.StringVar(&trackerPath, "tracker", "", "fake tracker store path")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&correlation, "correlation", "", "correlation ID")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.StringVar(&provider, "provider", "fake", "provider name")
	flags.StringVar(&workKey, "work-key", "", "provider work key")
	flags.StringVar(&assignee, "assignee", "", "assignment value")
	flags.Uint64Var(&expectedRevision, "expected-revision", 0, "expected provider revision")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	operationID, err := ids.ParseOperationID(operation)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "operation_id", err)
	}
	actorID, err := ids.ParseActorID(actor)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "actor_id", err)
	}
	processID, err := ids.ParseProcessID(process)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "process_id", err)
	}
	control, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	defer control.Close()
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	now := time.Now().UTC()
	saga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).
		WithFaults(faults.FileController{Directory: barrier, Participant: processID.String()})
	var result execution.TrackerEffectResult
	if action == "assign" {
		result, err = saga.Assign(ctx, execution.TrackerAssignRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, Provider: provider, WorkKey: workKey,
			ExpectedRevision: tracker.Revision(expectedRevision), Assignee: assignee,
		})
	} else {
		result, err = saga.Compensate(ctx, execution.TrackerCompensateRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, Provider: provider, WorkKey: workKey,
			ExpectedRevision: tracker.Revision(expectedRevision),
		})
	}
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: true, Result: &result})
	return 0
}

type explicitBool struct {
	value bool
	set   bool
}

func (value *explicitBool) String() string { return strconv.FormatBool(value.value) }
func (value *explicitBool) Set(raw string) error {
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return err
	}
	value.value, value.set = parsed, true
	return nil
}

func runInspectBeads(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tracker inspect-beads", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var project, executable, workspace, databaseIdentity, mode, expectedDigest string
	var serverIdentity, serverEndpoint, serverHost, serverPort string
	var credentialsPath, remoteUser, remotePassword string
	var protocolSchema uint64
	var readOnly explicitBool
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&executable, "executable", "", "absolute Beads executable path")
	flags.StringVar(&workspace, "workspace", "", "absolute canonical Beads workspace path")
	flags.StringVar(&databaseIdentity, "database-identity", "", "provider database identity")
	flags.Uint64Var(&protocolSchema, "protocol-schema", 0, "pinned JSON protocol schema")
	flags.StringVar(&mode, "mode", "", "embedded_serialized or server_multiwriter")
	flags.Var(&readOnly, "read-only", "explicit binding read-only state")
	flags.StringVar(&expectedDigest, "expected-digest", "", "optional pinned executable SHA-256")
	flags.StringVar(&serverIdentity, "server-identity", "", "pinned server identity")
	flags.StringVar(&serverEndpoint, "server-endpoint", "", "pinned server host:port")
	flags.StringVar(&serverHost, "dolt-server-host", "", "allowlisted Dolt server host")
	flags.StringVar(&serverPort, "dolt-server-port", "", "allowlisted Dolt server port")
	flags.StringVar(&credentialsPath, "dolt-credentials-path", "", "allowlisted Dolt credentials path")
	flags.StringVar(&remoteUser, "dolt-remote-user", "", "allowlisted Dolt remote user")
	flags.StringVar(&remotePassword, "dolt-remote-password", "", "allowlisted Dolt remote password")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("unexpected positional arguments"))
	}
	projectID, err := ids.ParseProjectID(project)
	if err != nil {
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("project ID is invalid"))
	}
	if strings.TrimSpace(databaseIdentity) == "" {
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("database identity is required"))
	}
	if protocolSchema == 0 || protocolSchema > uint64(^uint16(0)) {
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("protocol schema is invalid"))
	}
	var concurrency tracker.ConcurrencyMode
	switch tracker.ConcurrencyMode(mode) {
	case tracker.ConcurrencyEmbeddedSerialized:
		concurrency = tracker.ConcurrencyEmbeddedSerialized
	case tracker.ConcurrencyServerMultiwriter:
		concurrency = tracker.ConcurrencyServerMultiwriter
	default:
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("concurrency mode is invalid"))
	}
	if !readOnly.set {
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("read-only state must be explicit"))
	}
	credentials := map[string]string{}
	for key, value := range map[string]string{
		"DOLT_SERVER_HOST": serverHost, "DOLT_SERVER_PORT": serverPort,
		"DOLT_CREDENTIALS_PATH": credentialsPath, "DOLT_REMOTE_USER": remoteUser,
		"DOLT_REMOTE_PASSWORD": remotePassword,
	} {
		if value != "" {
			credentials[key] = value
		}
	}
	inspection, err := beads.InspectProvider(ctx, beads.BindingConfig{
		ProjectID: projectID, ExecutablePath: executable, WorkspacePath: workspace,
		ProviderDatabaseIdentity: databaseIdentity, ProtocolSchema: uint16(protocolSchema),
		ConcurrencyMode: concurrency, ReadOnly: readOnly.value, ExpectedExecutableDigest: expectedDigest,
		ServerIdentity: serverIdentity, ServerEndpoint: serverEndpoint, Credentials: credentials,
	})
	if err != nil {
		return encodeBeadsInspectionFailure(stdout, err)
	}
	diagnostics := inspection.Diagnostics()
	if diagnostics == nil {
		diagnostics = []tracker.Diagnostic{}
	}
	result := BeadsInspectionResult{SchemaVersion: 1, Binding: inspection.Binding, Capabilities: inspection.Capabilities, Health: inspection.Health, Diagnostics: diagnostics}
	_ = json.NewEncoder(stdout).Encode(BeadsInspectionResponse{SchemaVersion: 1, OK: true, Inspection: &result})
	return 0
}

func invalidInspectRequest(message string) error {
	return &tracker.Error{Kind: tracker.ErrorInvalidRequest, RecoveryCode: "repair_request", Message: message}
}

func encodeBeadsInspectionFailure(stdout io.Writer, err error) int {
	result := BeadsInspectionError{Kind: tracker.ErrorUnavailable, RecoveryCode: "operator_decision", Message: "provider inspection failed"}
	var portable *tracker.Error
	if errors.As(err, &portable) {
		result.Kind, result.RecoveryCode, result.Message = portable.Kind, portable.RecoveryAction(), portable.Message
	}
	_ = json.NewEncoder(stdout).Encode(BeadsInspectionResponse{SchemaVersion: 1, OK: false, Error: &result})
	return 1
}

func encodeTrackerInvalid(stdout io.Writer, correlation, operation, actor, process, provider, workKey string, revision uint64, field string, cause error) int {
	operationID, _ := ids.ParseOperationID(operation)
	diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, field+" is invalid", execution.RetryNever, correlation, operationID,
		map[string]string{"operation_id": operation, "actor_id": actor, "process_id": process, "provider": provider, "provider_work_key": workKey, "expected_provider_revision": strconv.FormatUint(revision, 10)},
		map[string]string{field: "invalid"}, map[string]string{field: "portable command-envelope value"}, time.Now().UTC())
	diagnostic.Cause = cause
	return encodeTrackerFailure(stdout, diagnostic)
}

func encodeTrackerFailure(stdout io.Writer, err error) int {
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: false, Error: diagnostic})
		return 0
	}
	_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: false, Message: err.Error()})
	return 1
}
