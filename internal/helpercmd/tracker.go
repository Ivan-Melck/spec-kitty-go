package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

const trackerUsage = "usage: tracker assign|compensate|inspect-beads|catalog-beads|assignment-beads [options]"

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
	if action == "catalog-beads" {
		return runCatalogBeads(ctx, args[1:], stdout, stderr)
	}
	if action == "assignment-beads" {
		return runAssignmentBeads(ctx, args[1:], stdout, stderr)
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

const maxCatalogRequestBytes = 2 << 20

type beadsAssignmentInput struct {
	CorrelationID       string                  `json:"correlation_id"`
	OperationID         string                  `json:"operation_id"`
	ActorID             string                  `json:"actor_id"`
	ProcessID           string                  `json:"process_id"`
	WorkID              string                  `json:"work_id"`
	ExternalID          string                  `json:"external_id"`
	MetadataSchema      uint16                  `json:"metadata_schema"`
	CreatedByOperation  string                  `json:"created_by_operation"`
	MappingObservation  tracker.Revision        `json:"mapping_observation"`
	ExpectedObservation tracker.Revision        `json:"expected_observation"`
	Assignee            string                  `json:"assignee,omitempty"`
	RequiredGuarantee   tracker.AssignmentLevel `json:"required_guarantee"`
}

func runAssignmentBeads(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "assign" && args[0] != "compensate" {
		fmt.Fprintln(stderr, trackerUsage)
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet("tracker assignment-beads "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var database, project, executable, workspace, databaseIdentity, requestFile string
	var protocolSchema uint64
	var readOnly explicitBool
	flags.StringVar(&database, "db", "", "control database path")
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&executable, "executable", "", "absolute Beads executable path")
	flags.StringVar(&workspace, "workspace", "", "absolute canonical Beads workspace path")
	flags.StringVar(&databaseIdentity, "database-identity", "", "absolute existing Beads database directory from where --json")
	flags.Uint64Var(&protocolSchema, "protocol-schema", 0, "pinned JSON protocol schema")
	flags.Var(&readOnly, "read-only", "explicit binding read-only state")
	flags.StringVar(&requestFile, "request-file", "", "absolute path to bounded typed JSON request")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 || !readOnly.set || readOnly.value || protocolSchema == 0 || protocolSchema > uint64(^uint16(0)) || strings.TrimSpace(databaseIdentity) == "" {
		return encodeTrackerFailure(stdout, invalidInspectRequest("assignment binding is invalid"))
	}
	projectID, err := ids.ParseProjectID(project)
	if err != nil {
		return encodeTrackerFailure(stdout, invalidInspectRequest("project ID is invalid"))
	}
	input, err := readBeadsAssignmentInput(requestFile)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	operationID, operationErr := ids.ParseOperationID(input.OperationID)
	actorID, actorErr := ids.ParseActorID(input.ActorID)
	processID, processErr := ids.ParseProcessID(input.ProcessID)
	workID, workErr := ids.ParseWorkID(input.WorkID)
	created, createdErr := ids.ParseOperationID(input.CreatedByOperation)
	if operationErr != nil || actorErr != nil || processErr != nil || workErr != nil || createdErr != nil || input.CorrelationID == "" {
		return encodeTrackerFailure(stdout, invalidInspectRequest("assignment request identity is invalid"))
	}
	reference := tracker.ExternalWorkRef{
		ProjectID: projectID, WorkID: workID, Provider: "beads", ExternalID: input.ExternalID,
		MetadataSchema: input.MetadataSchema, CreatedByOperation: created, LastObservation: input.MappingObservation,
	}
	if err := reference.Validate(); err != nil {
		return encodeTrackerFailure(stdout, invalidInspectRequest("assignment work mapping is invalid"))
	}
	bound, err := beads.Open(beads.BindingConfig{
		ProjectID: projectID, ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databaseIdentity,
		ProtocolSchema: uint16(protocolSchema), ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized, ReadOnly: false,
	})
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	control, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	defer control.Close()
	saga := execution.NewTrackerSaga(control, bound, execution.SystemClock{})
	now := time.Now().UTC()
	var result execution.TrackerEffectResult
	if action == "assign" {
		result, err = saga.Assign(ctx, execution.TrackerAssignRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: input.CorrelationID, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, ExpectedRevision: input.ExpectedObservation,
			Assignee: input.Assignee, ExternalRef: reference, RequiredGuarantee: input.RequiredGuarantee,
		})
	} else {
		result, err = saga.Compensate(ctx, execution.TrackerCompensateRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: input.CorrelationID, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, ExpectedRevision: input.ExpectedObservation,
			ExternalRef: reference, RequiredGuarantee: input.RequiredGuarantee,
		})
	}
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: true, Result: &result})
	return 0
}

func readBeadsAssignmentInput(path string) (beadsAssignmentInput, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return beadsAssignmentInput{}, invalidInspectRequest("request file path must be absolute and clean")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCatalogRequestBytes {
		return beadsAssignmentInput{}, invalidInspectRequest("request file must be a bounded regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return beadsAssignmentInput{}, invalidInspectRequest("request file cannot be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var input beadsAssignmentInput
	if err := decoder.Decode(&input); err != nil {
		return beadsAssignmentInput{}, invalidInspectRequest("assignment request JSON is invalid")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return beadsAssignmentInput{}, invalidInspectRequest("assignment request must contain one JSON object")
	}
	return input, nil
}

type beadsCatalogCriterion struct {
	ID           string               `json:"id"`
	Text         string               `json:"text"`
	Verifier     tracker.VerifierKind `json:"verifier"`
	Required     bool                 `json:"required"`
	EvidenceHint string               `json:"evidence_hint,omitempty"`
}

type beadsCatalogInput struct {
	WorkID              string                  `json:"work_id"`
	OperationID         string                  `json:"operation_id,omitempty"`
	ExternalID          string                  `json:"external_id,omitempty"`
	MetadataSchema      uint16                  `json:"metadata_schema"`
	CreatedByOperation  string                  `json:"created_by_operation,omitempty"`
	ExpectedObservation tracker.Revision        `json:"expected_observation,omitempty"`
	RequiredGuard       tracker.UpdateLevel     `json:"required_guard,omitempty"`
	Title               string                  `json:"title,omitempty"`
	Description         string                  `json:"description,omitempty"`
	DesignContext       string                  `json:"design_context,omitempty"`
	Criteria            []beadsCatalogCriterion `json:"criteria,omitempty"`
	ProviderMetadata    json.RawMessage         `json:"provider_metadata,omitempty"`
}

type BeadsCatalogRecord struct {
	Reference        tracker.ExternalWorkRef     `json:"reference"`
	Title            string                      `json:"title"`
	Description      string                      `json:"description"`
	DesignContext    string                      `json:"design_context"`
	Criteria         []tracker.Criterion         `json:"criteria"`
	VisibleAssignee  string                      `json:"visible_assignee,omitempty"`
	AssignmentMarker string                      `json:"assignment_marker,omitempty"`
	Observation      tracker.ProviderObservation `json:"observation"`
}

type BeadsCatalogMutation struct {
	Record    BeadsCatalogRecord  `json:"record"`
	Guarantee tracker.UpdateLevel `json:"guarantee"`
	Replayed  bool                `json:"replayed"`
	Noop      bool                `json:"noop"`
}

type BeadsCatalogResponse struct {
	SchemaVersion uint16                `json:"schema_version"`
	OK            bool                  `json:"ok"`
	Action        string                `json:"action"`
	Record        *BeadsCatalogRecord   `json:"record,omitempty"`
	Mutation      *BeadsCatalogMutation `json:"mutation,omitempty"`
	Error         *BeadsInspectionError `json:"error,omitempty"`
}

func runCatalogBeads(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "create" && args[0] != "read" && args[0] != "update") {
		fmt.Fprintln(stderr, trackerUsage)
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet("tracker catalog-beads "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var project, executable, workspace, databaseIdentity, expectedDigest, mode, requestFile string
	var protocolSchema uint64
	var readOnly explicitBool
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&executable, "executable", "", "absolute Beads executable path")
	flags.StringVar(&workspace, "workspace", "", "absolute canonical Beads workspace path")
	flags.StringVar(&databaseIdentity, "database-identity", "", "absolute existing Beads database directory from where --json")
	flags.Uint64Var(&protocolSchema, "protocol-schema", 0, "pinned JSON protocol schema")
	flags.StringVar(&mode, "mode", "", "embedded_serialized")
	flags.Var(&readOnly, "read-only", "explicit binding read-only state")
	flags.StringVar(&expectedDigest, "expected-digest", "", "optional pinned executable SHA-256")
	flags.StringVar(&requestFile, "request-file", "", "absolute path to bounded typed JSON request")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("unexpected positional arguments"))
	}
	projectID, err := ids.ParseProjectID(project)
	if err != nil || strings.TrimSpace(databaseIdentity) == "" || protocolSchema == 0 || protocolSchema > uint64(^uint16(0)) {
		return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("catalog binding is invalid"))
	}
	if tracker.ConcurrencyMode(mode) != tracker.ConcurrencyEmbeddedSerialized {
		return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("credential-free catalog command requires embedded_serialized mode"))
	}
	if !readOnly.set {
		return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("read-only state must be explicit"))
	}
	input, err := readBeadsCatalogInput(requestFile)
	if err != nil {
		return encodeBeadsCatalogFailure(stdout, action, err)
	}
	bound, err := beads.Open(beads.BindingConfig{
		ProjectID: projectID, ExecutablePath: executable, WorkspacePath: workspace,
		ProviderDatabaseIdentity: databaseIdentity, ProtocolSchema: uint16(protocolSchema),
		ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized, ReadOnly: readOnly.value,
		ExpectedExecutableDigest: expectedDigest,
	})
	if err != nil {
		return encodeBeadsCatalogFailure(stdout, action, err)
	}
	criteria := make([]tracker.Criterion, len(input.Criteria))
	for index, criterion := range input.Criteria {
		criteria[index] = tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
	}
	workID, err := ids.ParseWorkID(input.WorkID)
	if err != nil {
		return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("work ID is invalid"))
	}
	switch action {
	case "create":
		operationID, err := ids.ParseOperationID(input.OperationID)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("operation ID is invalid"))
		}
		metadata, err := tracker.NewOpaqueMetadata(input.ProviderMetadata)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("provider metadata is invalid"))
		}
		mutation, err := bound.CreateWork(ctx, tracker.CreateWorkRequest{
			ProjectID: projectID, WorkID: workID, OperationID: operationID, Title: input.Title,
			Description: input.Description, DesignContext: input.DesignContext, Criteria: criteria,
			MetadataSchema: input.MetadataSchema, ProviderMetadata: metadata,
		})
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, err)
		}
		return encodeBeadsCatalogMutation(stdout, action, mutation)
	case "read":
		reference, err := catalogExternalReference(projectID, workID, input)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, err)
		}
		record, err := bound.ReadWork(ctx, reference)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, err)
		}
		projected := projectCatalogRecord(record)
		_ = json.NewEncoder(stdout).Encode(BeadsCatalogResponse{SchemaVersion: 1, OK: true, Action: action, Record: &projected})
		return 0
	default:
		reference, err := catalogExternalReference(projectID, workID, input)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, err)
		}
		operationID, err := ids.ParseOperationID(input.OperationID)
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, invalidInspectRequest("operation ID is invalid"))
		}
		mutation, err := bound.UpdateWork(ctx, tracker.UpdateWorkRequest{
			ExternalRef: reference, OperationID: operationID, ExpectedObservation: input.ExpectedObservation,
			RequiredGuard: input.RequiredGuard, Title: input.Title, Description: input.Description,
			DesignContext: input.DesignContext, Criteria: criteria,
		})
		if err != nil {
			return encodeBeadsCatalogFailure(stdout, action, err)
		}
		return encodeBeadsCatalogMutation(stdout, action, mutation)
	}
}

func readBeadsCatalogInput(path string) (beadsCatalogInput, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return beadsCatalogInput{}, invalidInspectRequest("request file path must be absolute and clean")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCatalogRequestBytes {
		return beadsCatalogInput{}, invalidInspectRequest("request file must be a bounded regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return beadsCatalogInput{}, invalidInspectRequest("request file cannot be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var input beadsCatalogInput
	if err := decoder.Decode(&input); err != nil {
		return beadsCatalogInput{}, invalidInspectRequest("catalog request JSON is invalid")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return beadsCatalogInput{}, invalidInspectRequest("catalog request must contain one JSON object")
	}
	return input, nil
}

func catalogExternalReference(projectID ids.ProjectID, workID ids.WorkID, input beadsCatalogInput) (tracker.ExternalWorkRef, error) {
	created, err := ids.ParseOperationID(input.CreatedByOperation)
	if err != nil {
		return tracker.ExternalWorkRef{}, invalidInspectRequest("creation operation ID is invalid")
	}
	reference := tracker.ExternalWorkRef{
		ProjectID: projectID, WorkID: workID, Provider: "beads", ExternalID: input.ExternalID,
		MetadataSchema: input.MetadataSchema, CreatedByOperation: created, LastObservation: input.ExpectedObservation,
	}
	if err := reference.Validate(); err != nil {
		return tracker.ExternalWorkRef{}, invalidInspectRequest("external work reference is invalid")
	}
	return reference, nil
}

func projectCatalogRecord(record tracker.WorkRecord) BeadsCatalogRecord {
	criteria := record.Criteria()
	if criteria == nil {
		criteria = []tracker.Criterion{}
	}
	return BeadsCatalogRecord{
		Reference: record.ExternalRef, Title: record.Title, Description: record.Description,
		DesignContext: record.DesignContext, Criteria: criteria, VisibleAssignee: record.VisibleAssignee,
		AssignmentMarker: record.AssignmentMarker, Observation: record.Observation,
	}
}

func encodeBeadsCatalogMutation(stdout io.Writer, action string, mutation tracker.WorkMutation) int {
	projected := BeadsCatalogMutation{
		Record: projectCatalogRecord(mutation.Record), Guarantee: mutation.Guarantee,
		Replayed: mutation.Replayed, Noop: mutation.Noop,
	}
	_ = json.NewEncoder(stdout).Encode(BeadsCatalogResponse{SchemaVersion: 1, OK: true, Action: action, Mutation: &projected})
	return 0
}

func encodeBeadsCatalogFailure(stdout io.Writer, action string, err error) int {
	result := BeadsInspectionError{Kind: tracker.ErrorUnavailable, RecoveryCode: "operator_decision", Message: "catalog operation failed"}
	var portable *tracker.Error
	if errors.As(err, &portable) {
		result.Kind, result.RecoveryCode, result.Message = portable.Kind, portable.RecoveryAction(), portable.Message
	}
	_ = json.NewEncoder(stdout).Encode(BeadsCatalogResponse{SchemaVersion: 1, OK: false, Action: action, Error: &result})
	return 1
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
	flags.StringVar(&databaseIdentity, "database-identity", "", "absolute existing Beads database directory from where --json")
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
		return encodeBeadsInspectionFailure(stdout, invalidInspectRequest("database directory is required"))
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
