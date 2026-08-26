package beads

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const serverReadinessLifetime = 5 * time.Second

// Readiness is an immutable proof that one exact binding was compatible and
// healthy at preflight. It grants no Spec Kitty execution authority.
type Readiness struct {
	binding         tracker.TrackerBinding
	executableSize  int64
	executableMTime time.Time
	serverIdentity  string
	serverEndpoint  string
	serverQualified bool
	qualifiedAt     time.Time
	expiresAt       time.Time
}

// BoundAdapter pins all commands to one validated project binding.
type BoundAdapter struct {
	adapter    *Adapter
	binding    tracker.TrackerBinding
	locker     writerLocker
	connection providerConnection
	now        func() time.Time
}

func newConfiguredBoundAdapter(binding tracker.TrackerBinding, adapter *Adapter, locker writerLocker, connection providerConnection) (*BoundAdapter, error) {
	if err := validatePinnedBinding(binding); err != nil {
		return nil, err
	}
	if adapter == nil {
		adapter = newAdapter(nil)
	}
	if locker == nil {
		locker = filesystemWriterLocker{}
	}
	if binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		if connection.serverIdentity == "" || connection.serverEndpoint == "" {
			return nil, invalidBinding("server identity and endpoint are not pinned", nil)
		}
	} else if connection.serverIdentity != "" || connection.serverEndpoint != "" || len(connection.credentials) != 0 {
		return nil, invalidBinding("embedded binding contains server connection facts", nil)
	}
	connection.credentials = cloneCredentials(connection.credentials)
	return &BoundAdapter{adapter: adapter, binding: binding, locker: locker, connection: connection, now: time.Now}, nil
}

func (bound *BoundAdapter) commandBinding() commandBinding {
	result := defaultBinding(bound.binding.ExecutablePath, bound.binding.WorkspacePath, bound.binding.WorkspacePath)
	result.credentials = cloneCredentials(bound.connection.credentials)
	return result
}

func cloneCredentials(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type wherePayload struct {
	WorkspacePath string `json:"workspace_path"`
	Path          string `json:"path"`
	DatabasePath  string `json:"database_path"`
	DatabaseID    string `json:"database_id"`
	WorkspaceID   string `json:"workspace_id"`
}

func (payload wherePayload) workspace() string {
	for _, candidate := range []string{payload.WorkspacePath, payload.Path, payload.DatabasePath} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (payload wherePayload) databaseIdentity() string {
	if payload.DatabaseID != "" {
		return payload.DatabaseID
	}
	return payload.WorkspaceID
}

type infoPayload struct {
	SchemaVersion  uint16 `json:"schema_version"`
	ProtocolSchema uint16 `json:"protocol_schema"`
	StorageMode    string `json:"storage_mode"`
	Mode           string `json:"mode"`
	ReadOnly       bool   `json:"read_only"`
	ServerID       string `json:"server_id"`
	ServerEndpoint string `json:"server_endpoint"`
	ServerHealthy  *bool  `json:"server_healthy"`
}

func (payload infoPayload) schema() uint16 {
	if payload.ProtocolSchema != 0 {
		return payload.ProtocolSchema
	}
	return payload.SchemaVersion
}

func (payload infoPayload) mode() string {
	if payload.StorageMode != "" {
		return payload.StorageMode
	}
	return payload.Mode
}

type doctorPayload struct {
	Healthy        *bool  `json:"healthy"`
	Status         string `json:"status"`
	ServerID       string `json:"server_id"`
	ServerEndpoint string `json:"server_endpoint"`
}

func (bound *BoundAdapter) Preflight(ctx context.Context) (Readiness, error) {
	if bound == nil || bound.adapter == nil {
		return Readiness{}, invalidBinding("bound adapter is missing", nil)
	}
	if err := validatePinnedBinding(bound.binding); err != nil {
		return Readiness{}, err
	}
	info, err := os.Stat(bound.binding.ExecutablePath)
	if err != nil {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "repair_provider_binding", Message: "provider executable is unavailable", Cause: err}
	}
	digest, err := digestFile(bound.binding.ExecutablePath)
	if err != nil || digest != bound.binding.ExecutableDigest {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "install_verified_provider", Message: "provider executable changed after binding", Cause: err}
	}
	binding := bound.commandBinding()
	versionRequest, err := newVersionCommand(binding)
	if err != nil {
		return Readiness{}, err
	}
	versionResponse, err := bound.adapter.execute(ctx, versionRequest)
	if err != nil {
		return Readiness{}, err
	}
	identity, err := decodeExecutableIdentity(versionResponse.payload)
	if err != nil {
		result := versionResponse.result
		portable, _ := translateFailure(result, err)
		return Readiness{}, portable
	}
	if identity.Version != bound.binding.ExecutableVersion {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorVersionSkew, RecoveryCode: "install_supported_provider", Message: "observed provider version differs from binding"}
	}
	whereRequest, err := newWhereCommand(binding)
	if err != nil {
		return Readiness{}, err
	}
	whereResponse, err := bound.adapter.execute(ctx, whereRequest)
	if err != nil {
		return Readiness{}, err
	}
	var where wherePayload
	if json.Unmarshal(whereResponse.payload, &where) != nil || where.workspace() == "" {
		return Readiness{}, malformedInspection("workspace observation is malformed", nil)
	}
	observedWorkspace, _, err := canonicalExisting(where.workspace(), true)
	if err != nil {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "repair_provider_binding", Message: "observed provider workspace is unavailable", Cause: err}
	}
	if observedWorkspace != bound.binding.WorkspacePath {
		return Readiness{}, workspaceMismatch(bound.binding.WorkspaceIdentity, deriveWorkspaceIdentity(observedWorkspace, where.databaseIdentity()))
	}
	observedIdentity := deriveWorkspaceIdentity(observedWorkspace, where.databaseIdentity())
	// A provider that does not expose a database identity is still pinned by its
	// canonical path. If it does expose one, the binding must have included it.
	if observedIdentity != bound.binding.WorkspaceIdentity {
		return Readiness{}, workspaceMismatch(bound.binding.WorkspaceIdentity, observedIdentity)
	}
	infoRequest, err := newInfoCommand(binding)
	if err != nil {
		return Readiness{}, err
	}
	infoResponse, err := bound.adapter.execute(ctx, infoRequest)
	if err != nil {
		return Readiness{}, err
	}
	var providerInfo infoPayload
	if json.Unmarshal(infoResponse.payload, &providerInfo) != nil {
		return Readiness{}, malformedInspection("provider information is malformed", nil)
	}
	if infoResponse.metadata.schemaVersion != SupportedProtocolSchema || providerInfo.schema() != SupportedProtocolSchema {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorSchemaSkew, RecoveryCode: "install_supported_provider", Message: "provider protocol schema is unsupported"}
	}
	if providerInfo.ReadOnly || bound.binding.ReadOnly {
		return Readiness{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "use_writable_provider", Message: "provider binding is read-only"}
	}
	wantMode := string(bound.binding.ConcurrencyMode)
	if providerInfo.mode() != wantMode {
		return Readiness{}, invalidBinding("observed provider concurrency mode differs from binding", nil)
	}
	readiness := Readiness{binding: bound.binding, executableSize: info.Size(), executableMTime: info.ModTime()}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		doctorBinding := binding
		doctorBinding.timeout = defaultReadTimeout
		doctorRequest, buildErr := newDoctorCommand(doctorBinding)
		if buildErr != nil {
			return Readiness{}, buildErr
		}
		doctorResponse, executeErr := bound.adapter.execute(ctx, doctorRequest)
		if executeErr != nil {
			return Readiness{}, executeErr
		}
		var doctor doctorPayload
		if json.Unmarshal(doctorResponse.payload, &doctor) != nil {
			return Readiness{}, malformedInspection("provider health response is malformed", nil)
		}
		healthy := doctor.Healthy != nil && *doctor.Healthy || strings.EqualFold(doctor.Status, "healthy")
		serverID := doctor.ServerID
		if serverID == "" {
			serverID = providerInfo.ServerID
		}
		serverEndpoint := doctor.ServerEndpoint
		if serverEndpoint == "" {
			serverEndpoint = providerInfo.ServerEndpoint
		}
		if !healthy || serverID != bound.connection.serverIdentity || serverEndpoint != bound.connection.serverEndpoint ||
			providerInfo.ServerID != bound.connection.serverIdentity || providerInfo.ServerEndpoint != bound.connection.serverEndpoint {
			return Readiness{}, &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "repair_provider_server", Message: "provider server is not qualified healthy"}
		}
		now := bound.now().UTC()
		readiness.serverIdentity, readiness.serverEndpoint, readiness.serverQualified = serverID, serverEndpoint, true
		readiness.qualifiedAt, readiness.expiresAt = now, now.Add(serverReadinessLifetime)
	}
	return readiness, nil
}

// Inspect returns useful bounded diagnostics even when mutation readiness is
// unavailable. It invokes only typed no-init/no-fix protocol requests.
func (bound *BoundAdapter) Inspect(ctx context.Context, request tracker.InspectRequest) (tracker.Inspection, error) {
	if bound == nil || request.ProjectID == "" || request.ProjectID != bound.binding.ProjectID {
		return tracker.Inspection{}, invalidBinding("inspection project does not match binding", nil)
	}
	readiness, err := bound.Preflight(ctx)
	health := tracker.HealthHealthy
	var diagnostics []tracker.Diagnostic
	if err != nil {
		health = tracker.HealthDegraded
		var portable *tracker.Error
		if errors.As(err, &portable) {
			if (portable.Kind == tracker.ErrorUnavailable && portable.RecoveryAction() != "use_writable_provider") || portable.Kind == tracker.ErrorTimeout {
				health = tracker.HealthUnavailable
			}
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: portable.Kind, RecoveryCode: portable.RecoveryAction(), Message: portable.Message, Capability: portable.Capability})
		} else {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorMalformedProtocol, RecoveryCode: "repair_provider_binding", Message: "provider inspection failed"})
		}
	}
	capabilities := capabilitiesForCurrentPorts(bound.binding.ConcurrencyMode, err == nil, readiness.serverQualified)
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Kind != diagnostics[j].Kind {
			return diagnostics[i].Kind < diagnostics[j].Kind
		}
		return diagnostics[i].RecoveryCode < diagnostics[j].RecoveryCode
	})
	return tracker.NewInspection(bound.binding, capabilities, health, diagnostics)
}

func capabilitiesForCurrentPorts(mode tracker.ConcurrencyMode, compatible, serverQualified bool) tracker.ProviderCapabilities {
	writers := tracker.ConcurrentWritersNone
	if compatible && mode == tracker.ConcurrencyEmbeddedSerialized {
		writers = tracker.ConcurrentWritersAdapterSerialized
	}
	if compatible && mode == tracker.ConcurrencyServerMultiwriter && serverQualified {
		writers = tracker.ConcurrentWritersProviderNative
	}
	// WP04 implements inspection and the writer boundary only. Future ports
	// remain unsupported until their production implementations are qualified.
	return tracker.ProviderCapabilities{
		CatalogRead: tracker.SupportUnsupported, CatalogCreate: tracker.SupportUnsupported,
		CatalogUpdate: tracker.UpdateUnsupported, BlockingGraph: tracker.SupportUnsupported,
		Discussion: tracker.DiscussionUnsupported, Assignment: tracker.AssignmentUnsupported,
		ObservationRevision: tracker.ObservationRevisionUnsupported,
		SafeCompensation:    tracker.CompensationUnsupported, ConcurrentWriters: writers,
		PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported,
	}
}

func malformedInspection(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorMalformedProtocol, RecoveryCode: "repair_provider_binding", Message: message, Cause: cause}
}

func (bound *BoundAdapter) validateReadiness(readiness Readiness) error {
	if bound == nil || !sameBinding(bound.binding, readiness.binding) {
		return invalidBinding("mutation readiness is absent, stale, or belongs to another binding", nil)
	}
	info, err := os.Stat(bound.binding.ExecutablePath)
	if err != nil || info.Size() != readiness.executableSize || !info.ModTime().Equal(readiness.executableMTime) {
		return &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "rerun_provider_preflight", Message: "provider executable identity changed after preflight", Cause: err}
	}
	digest, err := digestFile(bound.binding.ExecutablePath)
	if err != nil || digest != bound.binding.ExecutableDigest {
		return &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "rerun_provider_preflight", Message: "provider executable bytes changed after preflight", Cause: err}
	}
	if bound.binding.ReadOnly {
		return &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "use_writable_provider", Message: "provider binding is read-only"}
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter && (!readiness.serverQualified || readiness.serverIdentity == "") {
		return &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "repair_provider_server", Message: "server-mode provider is not qualified"}
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		now := bound.now().UTC()
		if readiness.serverIdentity != bound.connection.serverIdentity || readiness.serverEndpoint != bound.connection.serverEndpoint ||
			readiness.qualifiedAt.IsZero() || !readiness.expiresAt.After(now) {
			return &tracker.Error{Kind: tracker.ErrorUnavailable, RecoveryCode: "rerun_provider_preflight", Message: "server-mode readiness is stale or belongs to another server"}
		}
	}
	return nil
}

func (bound *BoundAdapter) executeMutation(ctx context.Context, readiness Readiness, request command) (commandResponse, error) {
	// Compatibility refusal deliberately precedes writer-lock acquisition.
	if err := bound.validateReadiness(readiness); err != nil {
		return commandResponse{result: commandResult{kind: request.kind, mutationOutcome: mutationNotStarted}}, err
	}
	if !request.mutating {
		return commandResponse{}, invalidBinding("mutation executor requires a mutating typed command", nil)
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		return bound.adapter.execute(ctx, request)
	}
	lock, err := bound.locker.acquire(ctx, bound.binding)
	if err != nil {
		return commandResponse{result: commandResult{kind: request.kind, mutationOutcome: mutationNotStarted}}, err
	}
	defer lock.release()
	return bound.adapter.execute(ctx, request)
}

// Ensure interface conformance stays visible at the adapter boundary.
var _ tracker.Inspector = (*BoundAdapter)(nil)
